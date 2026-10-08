package mysql

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/protocol"
)

// These tests need a real MySQL or MariaDB server. They run only when
// VEGALOAD_TEST_MYSQL holds its URL, such as
// mysql://root@127.0.0.1:3306/app?tls=false (the user and tls may be put
// in the URL here, the test moves them into options). A password comes
// from the environment variable named by VEGALOAD_TEST_MYSQL_PASSWORD_ENV.
// The tests create and drop their own tables.

func liveTarget(t *testing.T, sql string, opts map[string]string) protocol.Target {
	t.Helper()
	raw := os.Getenv("VEGALOAD_TEST_MYSQL")
	if raw == "" {
		t.Skip("set VEGALOAD_TEST_MYSQL to run the tests against a real MySQL or MariaDB server")
	}
	o := map[string]string{}
	if env := os.Getenv("VEGALOAD_TEST_MYSQL_PASSWORD_ENV"); env != "" {
		o["password_env"] = env
	}
	base := raw
	if i := strings.Index(base, "?"); i >= 0 {
		for _, kv := range strings.Split(base[i+1:], "&") {
			k, v, ok := strings.Cut(kv, "=")
			if ok && strings.EqualFold(k, "tls") {
				o["tls"] = v
			}
		}
		base = base[:i]
	}
	scheme := ""
	rest := base
	switch {
	case strings.HasPrefix(base, "mysql://"):
		scheme = "mysql://"
		rest = strings.TrimPrefix(base, "mysql://")
	case strings.HasPrefix(base, "mariadb://"):
		scheme = "mariadb://"
		rest = strings.TrimPrefix(base, "mariadb://")
	}
	if u, hostdb, ok := strings.Cut(rest, "@"); ok && scheme != "" {
		o["username"] = u
		base = scheme + hostdb
	}
	for k, v := range opts {
		o[k] = v
	}
	return protocol.Target{URL: base, Body: []byte(sql), Options: o}
}

func liveRun(t *testing.T, sql string, opts map[string]string) (protocol.Result, Reply) {
	t.Helper()
	d, err := New(liveTarget(t, sql, opts), 10*time.Second)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer d.Close()
	return d.Run(context.Background())
}

func liveWrite(t *testing.T, sql string, opts map[string]string) (protocol.Result, Reply) {
	t.Helper()
	o := map[string]string{"allow_writes": "true"}
	for k, v := range opts {
		o[k] = v
	}
	return liveRun(t, sql, o)
}

func TestLive_TypesArgsAndModes(t *testing.T) {
	setup, _ := liveWrite(t, "drop table if exists vegaload_live; create table vegaload_live("+
		"id bigint primary key, name varchar(64), price decimal(8,2), ok tinyint, "+
		"at datetime, doc json, raw blob, big bigint unsigned); "+
		"insert into vegaload_live values (1,'ann',9.50,1,'2026-01-02 03:04:05','{\"k\":[1,2]}',x'6869',18446744073709551615), "+
		"(2,'bob',null,0,null,null,null,null)", nil)
	if !setup.Success {
		t.Fatal(setup.Err)
	}
	t.Cleanup(func() { liveWrite(t, "drop table if exists vegaload_live", nil) })

	for _, mode := range []string{"simple", "prepared"} {
		t.Run(mode, func(t *testing.T) {
			res, rep := liveRun(t, "select id, name, price, ok, at, doc, raw, big from vegaload_live where id >= ? order by id",
				map[string]string{"args": "[1]", "query_mode": mode, "min_rows": "2"})
			if !res.Success {
				t.Fatal(res.Err)
			}
			r := rep.Rows[0]
			if r[0] != int64(1) || r[1] != "ann" || r[3] != int64(1) {
				t.Errorf("row 1 = %#v", r)
			}
			if price := fmt.Sprint(r[2]); price != "9.50" && price != "9.5" {
				t.Errorf("price = %#v", r[2])
			}
			if at := fmt.Sprint(r[4]); !strings.Contains(at, "2026-01-02") {
				t.Errorf("datetime = %#v", r[4])
			}
			switch doc := r[5].(type) {
			case map[string]any:
				if doc["k"] == nil {
					t.Errorf("json = %#v", r[5])
				}
			case string:
				if !strings.Contains(doc, "k") {
					t.Errorf("json = %#v", r[5])
				}
			default:
				t.Errorf("json = %#v", r[5])
			}
			if r[6] != "hi" {
				t.Errorf("blob = %#v", r[6])
			}
			if r[7] != "18446744073709551615" {
				t.Errorf("unsigned bigint = %#v", r[7])
			}
			for _, i := range []int{2, 4, 5, 6, 7} {
				if rep.Rows[1][i] != nil {
					t.Errorf("row 2 column %d = %#v, want NULL", i, rep.Rows[1][i])
				}
			}
		})
	}
}

func TestLive_InsertReportsAffectedAndID(t *testing.T) {
	if res, _ := liveWrite(t, "drop table if exists vegaload_ids; create table vegaload_ids (id int primary key auto_increment, item varchar(32) not null)", nil); !res.Success {
		t.Fatal(res.Err)
	}
	t.Cleanup(func() { liveWrite(t, "drop table if exists vegaload_ids", nil) })

	res, rep := liveWrite(t, "insert into vegaload_ids (item) values ('book')", nil)
	if !res.Success {
		t.Fatal(res.Err)
	}
	if rep.RowsAffected != 1 || rep.LastInsertID < 1 {
		t.Fatalf("insert reply = %+v", rep)
	}
	id := rep.LastInsertID
	res, rep = liveWrite(t, "update vegaload_ids set item = 'pen' where id = ?", map[string]string{"args": fmt.Sprintf("[%d]", id)})
	if !res.Success {
		t.Fatal(res.Err)
	}
	if rep.RowsAffected != 1 {
		t.Errorf("update affected = %d", rep.RowsAffected)
	}
}

func TestLive_TransactionWithArgs(t *testing.T) {
	if res, _ := liveWrite(t, "drop table if exists vegaload_tx; create table vegaload_tx(id int primary key, a int); insert into vegaload_tx values (1, 0), (2, 0)", nil); !res.Success {
		t.Fatal(res.Err)
	}
	t.Cleanup(func() { liveWrite(t, "drop table if exists vegaload_tx", nil) })

	res, rep := liveWrite(t, "begin; update vegaload_tx set a = ? where id = ?; select a from vegaload_tx where id = ?; commit",
		map[string]string{"args": "[5, 1, 1]", "min_rows": "1"})
	if !res.Success {
		t.Fatal(res.Err)
	}
	if len(rep.Rows) != 1 || rep.Rows[0][0] != int64(5) || rep.RowsAffected != 0 {
		t.Errorf("reply = %+v", rep)
	}
	_, after := liveRun(t, "select a from vegaload_tx order by id", nil)
	if after.Rows[0][0] != int64(5) || after.Rows[1][0] != int64(0) {
		t.Errorf("table = %v", after.Rows)
	}
}

func TestLive_ArgsCannotChangeTheStatement(t *testing.T) {
	if res, _ := liveWrite(t, "drop table if exists vegaload_inj; create table vegaload_inj(name varchar(255))", nil); !res.Success {
		t.Fatal(res.Err)
	}
	t.Cleanup(func() { liveWrite(t, "drop table if exists vegaload_inj", nil) })
	for _, mode := range []string{"simple", "prepared"} {
		for _, val := range []string{`x'); drop table vegaload_inj; --`, `back\slash'quote`, "? --", "-5"} {
			arg, _ := json.Marshal([]string{val})
			res, _ := liveWrite(t, "insert into vegaload_inj values (?)", map[string]string{"args": string(arg), "query_mode": mode})
			if !res.Success {
				t.Fatalf("%s %q: %v", mode, val, res.Err)
			}
		}
	}
	_, n := liveRun(t, "select name from vegaload_inj", nil)
	if n.RowCount != 8 {
		t.Fatalf("rows = %d, want 8 (the table is still there)", n.RowCount)
	}
	want := map[string]int{}
	for _, val := range []string{`x'); drop table vegaload_inj; --`, `back\slash'quote`, "? --", "-5"} {
		want[val] = 2
	}
	for _, row := range n.Rows {
		got, _ := row[0].(string)
		want[got]--
	}
	for val, left := range want {
		if left != 0 {
			t.Errorf("value %q stored %d times, want 2", val, 2-left)
		}
	}
}

func TestLive_ReadOnlyByDefault(t *testing.T) {
	res, _ := liveRun(t, "create table vegaload_ro(a int)", nil)
	if res.Success {
		liveWrite(t, "drop table if exists vegaload_ro", nil)
		t.Fatal("the default allowed a write")
	}
	if res.Err == nil || !strings.Contains(res.Err.Error(), "allow_writes=true") {
		t.Errorf("error = %v", res.Err)
	}
	if res, _ := liveRun(t, "select 1", nil); !res.Success {
		t.Errorf("a read failed: %v", res.Err)
	}
	if res, _ := liveWrite(t, "create table vegaload_ro(a int); drop table vegaload_ro", nil); !res.Success {
		t.Errorf("allow_writes=true refused a write: %v", res.Err)
	}
}

func TestLive_AFailedStatementDoesNotPoisonThePool(t *testing.T) {
	d, err := New(liveTarget(t, "begin; select * from vegaload_missing_xyz", map[string]string{"pool": "1"}), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if res, _ := d.Run(context.Background()); res.Success {
		t.Fatal("a missing table succeeded")
	}
	ok, err := d.Call(nil, []byte("select 1"), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res, _ := ok.Run(context.Background()); !res.Success {
		t.Errorf("the next call failed: %v", res.Err)
	}
	denied, err := d.Call(nil, []byte("create table vegaload_poison(a int)"), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	res, _ := denied.Run(context.Background())
	if res.Success || res.Err == nil || !strings.Contains(res.Err.Error(), "allow_writes=true") {
		t.Errorf("the next call could write: %+v", res)
	}
}

func TestLive_Timeout(t *testing.T) {
	d, err := New(liveTarget(t, "select sleep(5)", nil), 300*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	start := time.Now()
	res, _ := d.Run(context.Background())
	if res.Success || !strings.Contains(res.Err.Error(), "timed out") || time.Since(start) > 3*time.Second {
		t.Errorf("success %v err %v after %s", res.Success, res.Err, time.Since(start))
	}
}

func TestLive_NoBackslashEscapes(t *testing.T) {
	// Default mode: the ? after the escaped quote is inside the string, so
	// there is one placeholder. NO_BACKSLASH_ESCAPES ends that string at the
	// quote, so the same text has two. A new pool must use the new mode.
	sql := `select ?, 'a\'?'`
	res, _ := liveRun(t, sql, map[string]string{"args": `["Z"]`})
	if !res.Success {
		t.Fatalf("default mode: %v", res.Err)
	}
	got, rep := liveWrite(t, "select @@global.sql_mode", nil)
	if !got.Success || len(rep.Rows) != 1 {
		t.Fatal(got.Err)
	}
	original := fmt.Sprint(rep.Rows[0][0])
	set, _ := liveWrite(t, "set global sql_mode = concat(@@global.sql_mode, ',NO_BACKSLASH_ESCAPES')", nil)
	if !set.Success {
		t.Skipf("cannot set global sql_mode: %v", set.Err)
	}
	t.Cleanup(func() {
		arg, _ := json.Marshal([]string{original})
		liveWrite(t, "set global sql_mode = ?", map[string]string{"args": string(arg)})
	})

	d, err := New(liveTarget(t, sql, map[string]string{"args": `["Z"]`}), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	res, _ = d.Run(context.Background())
	if res.Err == nil || !strings.Contains(res.Err.Error(), "more ? placeholders") {
		t.Fatalf("new pool did not use NO_BACKSLASH_ESCAPES: %v", res.Err)
	}
}

// A prepared call must not hold a pool connection while the statement takes
// another one. pool=1 used to wait until the timeout. pool=2 with more
// callers than connections used to fail every call.
func TestLive_PreparedPoolOneSucceeds(t *testing.T) {
	res, rep := liveRun(t, "select ?", map[string]string{
		"query_mode": "prepared", "pool": "1", "args": "[1]",
	})
	if !res.Success {
		t.Fatal(res.Err)
	}
	if len(rep.Rows) != 1 || fmt.Sprint(rep.Rows[0][0]) != "1" {
		t.Fatalf("rows = %#v", rep.Rows)
	}
}

func TestLive_PreparedManyCallersShareThePool(t *testing.T) {
	d, err := New(liveTarget(t, "select ?", map[string]string{
		"query_mode": "prepared", "pool": "2", "args": "[1]",
	}), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var wg sync.WaitGroup
	errc := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, _ := d.Run(context.Background())
			if !res.Success {
				errc <- res.Err
			}
		}()
	}
	wg.Wait()
	close(errc)
	n := 0
	for err := range errc {
		n++
		t.Errorf("call failed: %v", err)
	}
	if n != 0 {
		t.Errorf("%d of 16 calls failed", n)
	}
}

func TestLive_PreparedSetIsRefused(t *testing.T) {
	_, err := New(liveTarget(t, "set @a = 1", map[string]string{"query_mode": "prepared"}), 10*time.Second)
	if err == nil || !strings.Contains(err.Error(), "prepared") || !strings.Contains(err.Error(), "set") {
		t.Fatalf("error = %v", err)
	}
}
