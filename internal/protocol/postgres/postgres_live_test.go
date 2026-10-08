package postgres

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/protocol"
)

// These tests need a real PostgreSQL server. They run only when
// VEGALOAD_TEST_POSTGRES holds its URL, such as
// postgres://postgres@localhost:5432/postgres?sslmode=disable (the user and
// sslmode may be put in the URL here, the test moves them into options).
// A password comes from PGPASSWORD. The tests create and drop one table.

func liveTarget(t *testing.T, sql string, opts map[string]string) protocol.Target {
	t.Helper()
	raw := os.Getenv("VEGALOAD_TEST_POSTGRES")
	if raw == "" {
		t.Skip("set VEGALOAD_TEST_POSTGRES to run the tests against a real PostgreSQL server")
	}
	// Split "postgres://user@host:port/db?sslmode=x" into the pieces the driver takes.
	o := map[string]string{"sslmode": "disable"}
	base := raw
	if i := strings.Index(base, "?"); i >= 0 {
		for _, kv := range strings.Split(base[i+1:], "&") {
			if k, v, ok := strings.Cut(kv, "="); ok {
				o[k] = v
			}
		}
		base = base[:i]
	}
	if rest, ok := strings.CutPrefix(base, "postgres://"); ok {
		if u, hostdb, ok := strings.Cut(rest, "@"); ok {
			o["username"] = u
			base = "postgres://" + hostdb
		}
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

// liveWrite is liveRun for SQL that changes data, which needs allow_writes.
func liveWrite(t *testing.T, sql string, opts map[string]string) (protocol.Result, Reply) {
	t.Helper()
	o := map[string]string{"allow_writes": "true"}
	for k, v := range opts {
		o[k] = v
	}
	return liveRun(t, sql, o)
}

func TestLive_TypesArgsAndModes(t *testing.T) {
	setup, _ := liveWrite(t, "drop table if exists vegaload_live; create table vegaload_live(id int primary key, name text, price numeric(8,2), ok boolean, at timestamptz, doc jsonb, raw bytea); "+
		"insert into vegaload_live values (1,'ann',9.50,true,'2026-01-02 03:04:05+00','{\"k\":[1,2]}','\\x6869'), (2,'bob',null,false,null,null,null)", nil)
	if !setup.Success {
		t.Fatal(setup.Err)
	}
	t.Cleanup(func() { liveWrite(t, "drop table if exists vegaload_live", nil) })

	for _, mode := range []string{"simple", "extended"} {
		t.Run(mode, func(t *testing.T) {
			res, rep := liveRun(t, "select id, name, price, ok, at, doc, raw from vegaload_live where id >= $1 order by id",
				map[string]string{"args": "[1]", "query_mode": mode, "min_rows": "2"})
			if !res.Success {
				t.Fatal(res.Err)
			}
			r := rep.Rows[0]
			if r[0] != int32(1) || r[1] != "ann" || r[2] != "9.5" && r[2] != "9.50" || r[3] != true {
				t.Errorf("row 1 = %#v", r)
			}
			if r[4] != "2026-01-02T03:04:05Z" {
				t.Errorf("timestamp = %#v", r[4])
			}
			if doc, ok := r[5].(map[string]any); !ok || doc["k"] == nil {
				t.Errorf("jsonb = %#v", r[5])
			}
			if r[6] != "hi" {
				t.Errorf("bytea = %#v", r[6])
			}
			for i := 4; i < 7; i++ {
				if rep.Rows[1][i] != nil {
					t.Errorf("row 2 column %d = %#v, want NULL", i, rep.Rows[1][i])
				}
			}
		})
	}
}

func TestLive_TransactionAndRowsAffected(t *testing.T) {
	res, rep := liveWrite(t, "create temp table x(a int); begin; insert into x values (1),(2); select count(*) as c from x; commit", nil)
	if !res.Success {
		t.Fatal(res.Err)
	}
	if rep.Rows[0][0] != int64(2) || rep.RowsAffected < 2 {
		t.Errorf("rows %v affected %d", rep.Rows, rep.RowsAffected)
	}
}

// Read-only is the default: a write is refused, and the error says how to
// allow it. With allow_writes the same SQL works.
func TestLive_ReadOnlyByDefault(t *testing.T) {
	res, _ := liveRun(t, "create table vegaload_ro(a int)", nil)
	if res.Success {
		liveWrite(t, "drop table if exists vegaload_ro", nil)
		t.Fatal("the default allowed a write")
	}
	if !strings.Contains(res.Err.Error(), "read-only") || !strings.Contains(res.Err.Error(), "allow_writes=true") {
		t.Errorf("error = %v", res.Err)
	}
	if res, _ := liveRun(t, "select 1", nil); !res.Success {
		t.Errorf("a read failed: %v", res.Err)
	}
	if res, _ := liveWrite(t, "create table vegaload_ro(a int); drop table vegaload_ro", nil); !res.Success {
		t.Errorf("allow_writes=true refused a write: %v", res.Err)
	}
}

func TestLive_AFailedTransactionDoesNotPoisonThePool(t *testing.T) {
	d, err := New(liveTarget(t, "begin; select 1/0", map[string]string{"pool": "1"}), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if res, _ := d.Run(context.Background()); res.Success {
		t.Fatal("division by zero succeeded")
	}
	ok, err := d.Call(map[string]string{"pool": "1", "sslmode": d.target.Options["sslmode"], "username": d.target.Options["username"]}, []byte("select 1"), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res, _ := ok.Run(context.Background()); !res.Success {
		t.Errorf("the next call failed: %v", res.Err)
	}
}

func TestLive_Timeout(t *testing.T) {
	d, err := New(liveTarget(t, "select pg_sleep(5)", nil), 300*time.Millisecond)
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

// Several statements and args together, in the default mode. The reply is the
// last statement that returned rows, and the update really ran.
func TestLive_SeveralStatementsWithArgs(t *testing.T) {
	if res, _ := liveWrite(t, "drop table if exists vegaload_tx; create table vegaload_tx(id int primary key, a int); insert into vegaload_tx values (1, 0), (2, 0)", nil); !res.Success {
		t.Fatal(res.Err)
	}
	t.Cleanup(func() { liveWrite(t, "drop table if exists vegaload_tx", nil) })

	res, rep := liveWrite(t, "begin; update vegaload_tx set a = $2 where id = $1; select a from vegaload_tx where id = $1; commit",
		map[string]string{"args": "[1, 5]", "min_rows": "1"})
	if !res.Success {
		t.Fatal(res.Err)
	}
	if len(rep.Rows) != 1 || rep.Rows[0][0] != int32(5) || rep.CommandTag != "COMMIT" || rep.RowsAffected < 2 {
		t.Errorf("reply = %+v", rep)
	}
	_, after := liveRun(t, "select a from vegaload_tx order by id", nil)
	if after.Rows[0][0] != int32(5) || after.Rows[1][0] != int32(0) {
		t.Errorf("table = %v", after.Rows)
	}
}

// A value that looks like SQL stays a value, in both modes.
func TestLive_ArgsCannotChangeTheStatement(t *testing.T) {
	if res, _ := liveWrite(t, "drop table if exists vegaload_inj; create table vegaload_inj(name text)", nil); !res.Success {
		t.Fatal(res.Err)
	}
	t.Cleanup(func() { liveWrite(t, "drop table if exists vegaload_inj", nil) })
	for _, mode := range []string{"simple", "extended"} {
		for _, val := range []string{`x'); drop table vegaload_inj; --`, `back\slash'quote`, "$1 -- $$", "-5"} {
			arg, _ := json.Marshal([]string{val})
			res, rep := liveWrite(t, "insert into vegaload_inj values ($1) returning name", map[string]string{"args": string(arg), "query_mode": mode})
			if !res.Success {
				t.Fatalf("%s %q: %v", mode, val, res.Err)
			}
			if rep.Rows[0][0] != val {
				t.Errorf("%s: stored %#v, want %q", mode, rep.Rows[0][0], val)
			}
		}
	}
	_, n := liveRun(t, "select count(*) from vegaload_inj", nil)
	if n.Rows[0][0] != int64(8) {
		t.Errorf("rows = %v, want 8", n.Rows[0][0])
	}
}

func TestLive_URLSslmodeIsUsed(t *testing.T) {
	tg := liveTarget(t, "select 1", nil)
	delete(tg.Options, "sslmode")
	tg.URL += "?sslmode=disable"
	d, err := New(tg, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if res, _ := d.Run(context.Background()); !res.Success {
		t.Fatalf("failed: %v", res.Err)
	}
}
