package mysql

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/protocol"
	"github.com/vegaload/vegaload/internal/protocol/mysql/mysqltest"
)

func target(url, sql string, opts map[string]string) protocol.Target {
	if opts == nil {
		opts = map[string]string{}
	}
	return protocol.Target{URL: url, Body: []byte(sql), Options: opts}
}

func run(t *testing.T, tg protocol.Target, timeout time.Duration) (protocol.Result, Reply) {
	t.Helper()
	d, err := New(tg, timeout)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	res, rep := d.Run(context.Background())
	return res, rep
}

func people(string) []mysqltest.Result {
	return []mysqltest.Result{{
		Columns: []mysqltest.Column{mysqltest.Int("id"), mysqltest.Text("name"), mysqltest.Text("note")},
		Rows:    [][]any{{"1", "ann", nil}, {"2", "bob", "hi"}},
	}}
}

func TestQuery_ReadsRowsAndTypes(t *testing.T) {
	s := mysqltest.Start(t, func(string) []mysqltest.Result {
		return []mysqltest.Result{{
			Columns: []mysqltest.Column{
				mysqltest.Int("id"),
				mysqltest.Text("name"),
				{Name: "n", Type: "DOUBLE"},
				{Name: "price", Type: "DECIMAL"},
				{Name: "doc", Type: "JSON"},
				{Name: "day", Type: "DATE"},
				{Name: "big", Type: "UNSIGNED BIGINT"},
				mysqltest.Text("note"),
			},
			Rows: [][]any{{
				"42", "ann", "1.5", "9.50", `{"a":1}`, "2026-01-02", "18446744073709551615", nil,
			}},
		}}
	})
	res, rep := run(t, target(s.URL(), "select * from people", map[string]string{"username": "app"}), 3*time.Second)
	if !res.Success {
		t.Fatalf("failed: %v", res.Err)
	}
	if s.ConnCount() != 1 {
		t.Errorf("connections = %d", s.ConnCount())
	}
	if rep.RowCount != 1 || len(rep.Rows) != 1 {
		t.Fatalf("reply = %+v", rep)
	}
	row := rep.Rows[0]
	if row[0] != int64(42) || row[1] != "ann" || row[2] != 1.5 || row[3] != "9.50" {
		t.Errorf("row = %#v", row)
	}
	doc, ok := row[4].(map[string]any)
	if !ok || doc["a"] != float64(1) {
		t.Errorf("json = %#v", row[4])
	}
	if row[5] != "2026-01-02" || row[6] != "18446744073709551615" || row[7] != nil {
		t.Errorf("tail = %#v", row[5:])
	}
	logins := s.Logins()
	if len(logins) != 1 || logins[0].User != "app" || logins[0].DB != "testdb" {
		t.Errorf("login = %+v", logins)
	}
	inits := s.Inits()
	if len(inits) != 2 || !strings.EqualFold(inits[0], "SET SESSION TRANSACTION READ ONLY") {
		t.Errorf("inits = %q", inits)
	}
	// The setup statements are not counted as the user's SQL.
	if q := s.Queries(); len(q) != 1 || q[0] != "select * from people" {
		t.Errorf("queries = %q", q)
	}
}

func TestNew_DoesNotConnect(t *testing.T) {
	s := mysqltest.Start(t, people)
	d, err := New(target(s.URL(), "select 1", nil), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_ = d.Close()
	if s.ConnCount() != 0 {
		t.Errorf("New connected %d times", s.ConnCount())
	}
}

func TestQuery_SeveralStatementsGiveTheLastRows(t *testing.T) {
	s := mysqltest.Start(t, func(string) []mysqltest.Result {
		return []mysqltest.Result{
			{},
			{Affected: 3},
			{Columns: []mysqltest.Column{mysqltest.Int("n")}, Rows: [][]any{{"7"}}},
			{},
		}
	})
	res, rep := run(t, target(s.URL(), "begin; update t set a=1; select 7; commit", nil), 3*time.Second)
	if !res.Success {
		t.Fatal(res.Err)
	}
	if rep.RowCount != 1 || rep.Rows[0][0] != int64(7) {
		t.Errorf("rows = %#v", rep.Rows)
	}
	// Several statements do not report an affected-row count.
	if rep.RowsAffected != 0 || rep.LastInsertID != 0 {
		t.Errorf("affected %d id %d", rep.RowsAffected, rep.LastInsertID)
	}
	if q := s.Queries(); len(q) < 1 || !strings.HasPrefix(q[0], "begin;") {
		t.Errorf("queries = %q", q)
	}
	if !hasQuery(s, "ROLLBACK") {
		t.Errorf("begin text did not roll back: %q", s.Queries())
	}
}

func TestExec_InsertReportsAffectedAndID(t *testing.T) {
	s := mysqltest.Start(t, func(string) []mysqltest.Result {
		return []mysqltest.Result{{Affected: 1, LastInsertID: 9}}
	})
	res, rep := run(t, target(s.URL(), "insert into t (a) values (1)", map[string]string{"allow_writes": "true"}), 3*time.Second)
	if !res.Success {
		t.Fatal(res.Err)
	}
	if rep.RowsAffected != 1 || rep.LastInsertID != 9 || rep.RowCount != 0 {
		t.Errorf("reply = %+v", rep)
	}
}

func TestQuery_ServerErrorIsAFailureWithTheCode(t *testing.T) {
	s := mysqltest.Start(t, func(string) []mysqltest.Result {
		return []mysqltest.Result{{ErrNumber: 1146, ErrState: "42S02", ErrMessage: "Table 'db.t' doesn't exist"}}
	})
	res, _ := run(t, target(s.URL(), "select * from t", nil), 3*time.Second)
	if res.Success || res.Err == nil {
		t.Fatal("want a failure")
	}
	msg := res.Err.Error()
	if !strings.Contains(msg, "doesn't exist") || !strings.Contains(msg, "error 1146") || !strings.Contains(msg, "42S02") {
		t.Errorf("error = %v", res.Err)
	}
}

func TestReadOnlyRefusal_SaysHowToAllowWrites(t *testing.T) {
	s := mysqltest.Start(t, func(string) []mysqltest.Result {
		return []mysqltest.Result{{ErrNumber: 1792, ErrState: "25006", ErrMessage: "Cannot execute statement in a READ ONLY transaction"}}
	})
	res, _ := run(t, target(s.URL(), "update t set a = 1", nil), 3*time.Second)
	if res.Success || res.Err == nil || !strings.Contains(res.Err.Error(), "allow_writes=true") || !strings.Contains(res.Err.Error(), "1792") {
		t.Errorf("error = %v", res.Err)
	}

	res, _ = run(t, target(s.URL(), "update t set a = 1", map[string]string{"allow_writes": "true"}), 3*time.Second)
	if res.Err == nil || strings.Contains(res.Err.Error(), "allow_writes") {
		t.Errorf("allow_writes already true: %v", res.Err)
	}

	conn, err := NewConn(target(s.URL(), "", nil), 3*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	call, err := conn.Call(nil, []byte("update t set a = 1"), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	res, _ = call.Run(context.Background())
	if res.Err == nil || strings.Contains(res.Err.Error(), "-opt") || !strings.Contains(res.Err.Error(), "allow_writes: true") {
		t.Errorf("script: %v", res.Err)
	}
}

func TestAllowWrites_SkipsTheReadOnlySetting(t *testing.T) {
	s := mysqltest.Start(t, people)
	if res, _ := run(t, target(s.URL(), "select 1", map[string]string{"allow_writes": "true"}), 3*time.Second); !res.Success {
		t.Fatal(res.Err)
	}
	for _, q := range s.Inits() {
		if strings.Contains(strings.ToUpper(q), "READ ONLY") {
			t.Errorf("allow_writes still sent %q", q)
		}
	}
}

func TestChecks_MinRowsAndExpect(t *testing.T) {
	s := mysqltest.Start(t, people)
	cases := []struct {
		opts map[string]string
		ok   bool
		want string
	}{
		{map[string]string{"min_rows": "2"}, true, ""},
		{map[string]string{"min_rows": "3"}, false, "has 2 rows, want at least 3"},
		{map[string]string{"expect": "bob"}, true, ""},
		{map[string]string{"expect": "zed"}, false, `contains "zed"`},
	}
	for _, c := range cases {
		res, _ := run(t, target(s.URL(), "select 1", c.opts), 3*time.Second)
		if res.Success != c.ok {
			t.Errorf("%v: success = %v, err %v", c.opts, res.Success, res.Err)
		}
		if c.want != "" && (res.Err == nil || !strings.Contains(res.Err.Error(), c.want)) {
			t.Errorf("%v: error = %v, want it to contain %q", c.opts, res.Err, c.want)
		}
	}
}

func TestMaxRows_KeepsFewButCountsAll(t *testing.T) {
	s := mysqltest.Start(t, func(string) []mysqltest.Result {
		rows := make([][]any, 50)
		for i := range rows {
			rows[i] = []any{"x"}
		}
		return []mysqltest.Result{{Columns: []mysqltest.Column{mysqltest.Text("a")}, Rows: rows}}
	})
	res, rep := run(t, target(s.URL(), "select a", map[string]string{"max_rows": "5", "min_rows": "50"}), 3*time.Second)
	if !res.Success {
		t.Fatal(res.Err)
	}
	if rep.RowCount != 50 || len(rep.Rows) != 5 || res.BytesReceived != 50 {
		t.Errorf("count %d kept %d bytes %d", rep.RowCount, len(rep.Rows), res.BytesReceived)
	}
}

func TestArgs_AreFilledIn(t *testing.T) {
	s := mysqltest.Start(t, people)
	sql := "select * from t where id = ? and name = ? and note = ? and ok = ? and x is ? and n = ?"
	res, _ := run(t, target(s.URL(), sql, map[string]string{
		"args": `["o'brien", "a\\b", "x'); drop table t; --", true, null, -5]`,
	}), 3*time.Second)
	if !res.Success {
		t.Fatal(res.Err)
	}
	q := s.Queries()
	if len(q) != 1 {
		t.Fatalf("queries = %q", q)
	}
	for _, part := range []string{
		"'o''brien'",
		"_utf8mb4 X'615c62'",
		"'x''); drop table t; --'",
		"TRUE",
		"NULL",
		"(-5)",
	} {
		if !strings.Contains(q[0], part) {
			t.Errorf("sent %q, missing %s", q[0], part)
		}
	}
}

func TestArgs_WithSeveralStatements(t *testing.T) {
	s := mysqltest.Start(t, func(string) []mysqltest.Result {
		return []mysqltest.Result{
			{},
			{Affected: 1},
			{Columns: []mysqltest.Column{mysqltest.Int("n")}, Rows: [][]any{{"7"}}},
			{},
		}
	})
	res, rep := run(t, target(s.URL(), "begin; update t set a = 1 where id = ?; select 7; commit",
		map[string]string{"args": "[1]", "min_rows": "1"}), 3*time.Second)
	if !res.Success {
		t.Fatal(res.Err)
	}
	if q := s.Queries(); len(q) < 1 || !strings.Contains(q[0], "id =  1 ") {
		t.Errorf("sent %q", q)
	}
	if rep.RowCount != 1 || rep.Rows[0][0] != int64(7) {
		t.Errorf("reply = %+v", rep)
	}
}

func TestArgs_WrongCountIsAConfigError(t *testing.T) {
	for name, tg := range map[string]protocol.Target{
		"too few":  target("mysql://h/db", "select ?, ?", map[string]string{"args": "[1]"}),
		"too many": target("mysql://h/db", "select ?", map[string]string{"args": "[1, 2]"}),
		"none":     target("mysql://h/db", "select 1", map[string]string{"args": "[1]"}),
	} {
		if d, err := New(tg, time.Second); err == nil {
			_ = d.Close()
			t.Errorf("%s: want an error", name)
		}
	}
	if d, err := New(target("mysql://h/db", "select ?", nil), time.Second); err != nil {
		t.Errorf("no args: %v", err)
	} else {
		_ = d.Close()
	}
}

func TestPool_ConnectionsAreKeptAndCapped(t *testing.T) {
	var inflight, peak atomic.Int32
	s := mysqltest.Start(t, func(string) []mysqltest.Result {
		n := inflight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(30 * time.Millisecond)
		inflight.Add(-1)
		return people("")
	})
	d, err := New(target(s.URL(), "select 1", map[string]string{"pool": "3"}), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if res, _ := d.Do(context.Background()); !res.Success {
				t.Errorf("call failed: %v", res.Err)
			}
		}()
	}
	wg.Wait()
	if n := s.ConnCount(); n > 3 {
		t.Errorf("%d connections for pool=3", n)
	}
	if peak.Load() > 3 {
		t.Errorf("%d queries at once for pool=3", peak.Load())
	}
	if len(s.Queries()) != 12 {
		t.Errorf("%d queries, want 12", len(s.Queries()))
	}
}

func TestTimeout_IsReported(t *testing.T) {
	gate := make(chan struct{})
	s := mysqltest.Start(t, func(string) []mysqltest.Result {
		<-gate
		return people("")
	})
	defer close(gate)
	start := time.Now()
	res, _ := run(t, target(s.URL(), "select sleep(9)", nil), 300*time.Millisecond)
	if res.Success || res.Err == nil || !strings.Contains(res.Err.Error(), "timed out") {
		t.Errorf("result = %+v", res)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("took %s", time.Since(start))
	}
}

func TestRunEnding_StopsTheCall(t *testing.T) {
	gate := make(chan struct{})
	s := mysqltest.Start(t, func(string) []mysqltest.Result {
		<-gate
		return people("")
	})
	defer close(gate)
	d, err := New(target(s.URL(), "select 1", nil), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	start := time.Now()
	res, _ := d.Run(ctx)
	if res.Success || time.Since(start) > 3*time.Second {
		t.Errorf("success %v after %s, err %v", res.Success, time.Since(start), res.Err)
	}
}

func TestUnreachableServer_IsAFailureNotAStop(t *testing.T) {
	d, err := New(target("mysql://127.0.0.1:1/db", "select 1", nil), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	res, err := d.Do(context.Background())
	if err != nil {
		t.Fatalf("Do must not return a run-stopping error: %v", err)
	}
	if res.Success || res.Err == nil || !strings.Contains(res.Err.Error(), "could not connect to 127.0.0.1:1") {
		t.Errorf("result = %+v", res)
	}
}

func TestPassword_FromEnvIsSentAndNeverShown(t *testing.T) {
	s := mysqltest.StartWithPassword(t, people, "s3cret-pw")
	t.Setenv("MYSQL_TEST_PASSWORD", "s3cret-pw")
	res, _ := run(t, target(s.URL(), "select 1", map[string]string{"username": "app", "password_env": "MYSQL_TEST_PASSWORD"}), 3*time.Second)
	if !res.Success {
		t.Fatalf("right password refused: %v", res.Err)
	}
	t.Setenv("MYSQL_TEST_PASSWORD", "wrong-pw")
	res, _ = run(t, target(s.URL(), "select 1", map[string]string{"username": "app", "password_env": "MYSQL_TEST_PASSWORD"}), 3*time.Second)
	if res.Success {
		t.Fatal("wrong password accepted")
	}
	if res.Err != nil && (strings.Contains(res.Err.Error(), "wrong-pw") || strings.Contains(res.Err.Error(), "s3cret-pw")) {
		t.Errorf("the error shows a password: %v", res.Err)
	}
}

func TestNewWithPassword_ForScripts(t *testing.T) {
	s := mysqltest.StartWithPassword(t, people, "pw1")
	pw := "pw1"
	conn, err := NewConn(protocol.Target{URL: s.URL(), Options: map[string]string{"username": "app"}}, 3*time.Second, &pw)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	d, err := conn.Call(map[string]string{"username": "app"}, []byte("select 1"), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res, _ := d.Run(context.Background()); !res.Success {
		t.Fatalf("failed: %v", res.Err)
	}
	d2, err := conn.Call(map[string]string{"username": "app"}, []byte("select 2"), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res, _ := d2.Run(context.Background()); !res.Success {
		t.Fatalf("failed: %v", res.Err)
	}
	if s.ConnCount() != 1 {
		t.Errorf("%d connections, want 1", s.ConnCount())
	}
}

func TestDirtyConnection_FailureIsDiscardedAndNextCallIsReadOnly(t *testing.T) {
	s := mysqltest.Start(t, func(sql string) []mysqltest.Result {
		if strings.Contains(sql, "1/0") {
			return []mysqltest.Result{{ErrNumber: 1690, ErrState: "22003", ErrMessage: "division by zero"}}
		}
		return []mysqltest.Result{{Columns: []mysqltest.Column{mysqltest.Int("n")}, Rows: [][]any{{"1"}}}}
	})
	d, err := New(target(s.URL(), "begin; select 1/0", map[string]string{"pool": "1"}), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if res, _ := d.Run(context.Background()); res.Success {
		t.Fatal("the failing text should fail")
	}
	next, err := d.Call(map[string]string{"pool": "1"}, []byte("select 1"), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res, _ := next.Run(context.Background()); !res.Success {
		t.Fatal(res.Err)
	}
	if s.ConnCount() < 2 {
		t.Errorf("connections = %d, want the failed one discarded", s.ConnCount())
	}
	readOnly := 0
	for _, q := range s.Inits() {
		if strings.Contains(strings.ToUpper(q), "READ ONLY") {
			readOnly++
		}
	}
	if readOnly < 2 {
		t.Errorf("read-only was sent %d times, inits %q", readOnly, s.Inits())
	}
}

func TestDirtyConnection_SetDiscardsTheConnection(t *testing.T) {
	s := mysqltest.Start(t, func(string) []mysqltest.Result {
		return []mysqltest.Result{{}}
	})
	d, err := New(target(s.URL(), "set @a = 1", map[string]string{"pool": "1"}), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if res, _ := d.Run(context.Background()); !res.Success {
		t.Fatal(res.Err)
	}
	next, err := d.Call(map[string]string{"pool": "1"}, []byte("select 1"), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res, _ := next.Run(context.Background()); !res.Success {
		t.Fatal(res.Err)
	}
	if s.ConnCount() != 2 {
		t.Errorf("connections = %d, want 2", s.ConnCount())
	}
}

func TestNew_ConfigMistakes(t *testing.T) {
	t.Setenv("MYSQL_TEST_EMPTY", "")
	cases := map[string]protocol.Target{
		"wrong scheme":      target("postgres://h/db", "select 1", nil),
		"no host":           target("mysql:///db", "select 1", nil),
		"no sql":            target("mysql://h/db", "  ", nil),
		"unknown option":    target("mysql://h/db", "select 1", map[string]string{"bogus": "1"}),
		"password in url":   target("mysql://app:pw@h/db", "select 1", nil),
		"bad tls":           target("mysql://h/db", "select 1", map[string]string{"tls": "maybe"}),
		"bad query_mode":    target("mysql://h/db", "select 1", map[string]string{"query_mode": "fast"}),
		"prepared multi":    target("mysql://h/db", "select 1; select 2", map[string]string{"query_mode": "prepared"}),
		"prepared set":      target("mysql://h/db", "set @a = 1", map[string]string{"query_mode": "prepared"}),
		"pool zero":         target("mysql://h/db", "select 1", map[string]string{"pool": "0"}),
		"pool text":         target("mysql://h/db", "select 1", map[string]string{"pool": "many"}),
		"allow_writes text": target("mysql://h/db", "select 1", map[string]string{"allow_writes": "sure"}),
		"min_rows negative": target("mysql://h/db", "select 1", map[string]string{"min_rows": "-1"}),
		"max_rows zero":     target("mysql://h/db", "select 1", map[string]string{"max_rows": "0"}),
		"args not a list":   target("mysql://h/db", "select 1", map[string]string{"args": `{"a":1}`}),
		"args trailing":     target("mysql://h/db", "select 1", map[string]string{"args": `[1] x`}),
		"password no user":  target("mysql://h/db", "select 1", map[string]string{"password_env": "MYSQL_TEST_EMPTY"}),
		"env not set":       target("mysql://h/db", "select 1", map[string]string{"username": "u", "password_env": "MYSQL_TEST_NOT_SET_ANYWHERE"}),
		"two databases":     target("mysql://h/one", "select 1", map[string]string{"database": "two"}),
		"two users":         target("mysql://a@h/one", "select 1", map[string]string{"username": "b"}),
		"tls both":          target("mysql://h/db?tls=false", "select 1", map[string]string{"tls": "true"}),
		"old option":        target("mysql://h/db", "select 1", map[string]string{"read_only": "true"}),
	}
	for name, tg := range cases {
		_, err := New(tg, time.Second)
		if err == nil {
			t.Errorf("%s: want an error", name)
			continue
		}
		if !strings.HasPrefix(err.Error(), "mysql: ") {
			t.Errorf("%s: error %q does not start with mysql:", name, err)
		}
	}
	if _, err := New(target("mysql://h/db", "select 1", map[string]string{"read_only": "true"}), time.Second); err == nil || !strings.Contains(err.Error(), "allow_writes") {
		t.Errorf("read_only: %v", err)
	}
}

func TestPrepared_RefusesSessionStatements(t *testing.T) {
	for _, sql := range []string{
		"set @a = 1",
		"use otherdb",
		"lock tables t read",
		"xa start 'x'",
		"unlock tables",
		"begin",
		"start transaction",
	} {
		_, err := New(target("mysql://h/db", sql, map[string]string{"query_mode": "prepared"}), time.Second)
		if err == nil || !strings.Contains(err.Error(), "query_mode=prepared cannot run") {
			t.Errorf("%s: %v", sql, err)
		}
	}
	if _, err := New(target("mysql://h/db", "select 1", map[string]string{"query_mode": "prepared"}), time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestNew_URLSecretsAreRefusedByName(t *testing.T) {
	secret := "hunter2"
	for _, raw := range []string{
		"mysql://h/db?password=" + secret,
		"mysql://h/db?passfile=/tmp/" + secret,
		"mysql://h/db?passwd=" + secret,
		"mysql://h/db?foo=" + secret,
		"mysql://app:" + secret + "@h/db",
	} {
		_, err := New(target(raw, "select 1", nil), time.Second)
		if err == nil || strings.Contains(err.Error(), secret) {
			t.Errorf("%s: %v", raw, err)
		}
	}
	_, err := New(protocol.Target{URL: "mysql:///db?password=" + secret, Body: []byte("select 1")}, time.Second)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Errorf("no host: %v", err)
	}
}

func TestNew_AcceptsTheUsualForms(t *testing.T) {
	for _, u := range []string{
		"mysql://localhost",
		"mysql://localhost:3307/db",
		"mariadb://u@db.internal/app",
		"mysql://[::1]:3306/db",
		"mysql://localhost/db?tls=false",
		"mysql://localhost/db?tls=preferred",
	} {
		d, err := New(target(u, "select 1", nil), time.Second)
		if err != nil {
			t.Errorf("%s: %v", u, err)
			continue
		}
		_ = d.Close()
	}
	d, err := New(target("mysql://localhost/db?tls=false", "select 1", map[string]string{"tls": "false"}), time.Second)
	if err != nil {
		t.Errorf("same tls in both places: %v", err)
	} else {
		_ = d.Close()
	}
}

func TestDriverName(t *testing.T) {
	d, err := New(target("mysql://localhost/db", "select 1", nil), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.Name() != "mysql" {
		t.Errorf("Name = %q", d.Name())
	}
}

func hasQuery(s *mysqltest.Server, want string) bool {
	for _, q := range s.Queries() {
		if strings.TrimSpace(q) == want {
			return true
		}
	}
	return false
}
