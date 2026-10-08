package postgres

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/protocol"
	"github.com/vegaload/vegaload/internal/protocol/postgres/postgrestest"
)

func target(url, sql string, opts map[string]string) protocol.Target {
	if opts == nil {
		opts = map[string]string{}
	}
	if _, ok := opts["sslmode"]; !ok {
		opts["sslmode"] = "disable"
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

// people answers every query with two rows.
func people(string) []postgrestest.Result {
	return []postgrestest.Result{{
		Columns: []postgrestest.Column{postgrestest.Int("id"), postgrestest.Text("name"), postgrestest.Text("note")},
		Rows:    [][]any{{"1", "ann", nil}, {"2", "bob", "hi"}},
	}}
}

func TestQuery_ReadsRows(t *testing.T) {
	s := postgrestest.Start(t, people)
	res, rep := run(t, target(s.URL(), "select * from people", map[string]string{"username": "app"}), 3*time.Second)
	if !res.Success {
		t.Fatalf("failed: %v", res.Err)
	}
	if res.BytesSent != int64(len("select * from people")) || res.BytesReceived != 1+3+1+3+2 {
		t.Errorf("bytes sent %d received %d", res.BytesSent, res.BytesReceived)
	}
	if strings.Join(rep.Columns, ",") != "id,name,note" || rep.RowCount != 2 || len(rep.Rows) != 2 {
		t.Fatalf("reply = %+v", rep)
	}
	// An int4 column is a number, text is text, NULL is nil.
	if rep.Rows[0][0] != int32(1) || rep.Rows[0][1] != "ann" || rep.Rows[0][2] != nil || rep.Rows[1][2] != "hi" {
		t.Errorf("rows = %#v", rep.Rows)
	}
	if rep.CommandTag != "SELECT 2" || rep.RowsAffected != 2 {
		t.Errorf("tag %q affected %d", rep.CommandTag, rep.RowsAffected)
	}
	st := s.Startups()
	if len(st) != 1 || st[0]["user"] != "app" || st[0]["database"] != "testdb" || st[0]["application_name"] != "vegaload" {
		t.Errorf("startup = %v", st)
	}
}

func TestQuery_SeveralStatementsGiveTheLastRows(t *testing.T) {
	s := postgrestest.Start(t, func(string) []postgrestest.Result {
		return []postgrestest.Result{
			{Tag: "BEGIN"},
			{Tag: "UPDATE 3"},
			{Columns: []postgrestest.Column{postgrestest.Int("n")}, Rows: [][]any{{"7"}}},
			{Tag: "COMMIT"},
		}
	})
	res, rep := run(t, target(s.URL(), "begin; update t set a=1; select 7; commit", nil), 3*time.Second)
	if !res.Success {
		t.Fatal(res.Err)
	}
	if rep.RowCount != 1 || rep.Rows[0][0] != int32(7) {
		t.Errorf("rows = %#v", rep.Rows)
	}
	if rep.RowsAffected != 3+1 || rep.CommandTag != "COMMIT" {
		t.Errorf("affected %d tag %q", rep.RowsAffected, rep.CommandTag)
	}
}

func TestQuery_ServerErrorIsAFailureWithTheCode(t *testing.T) {
	s := postgrestest.Start(t, func(string) []postgrestest.Result {
		return []postgrestest.Result{{ErrCode: "42P01", ErrMessage: `relation "nosuch" does not exist`}}
	})
	res, _ := run(t, target(s.URL(), "select * from nosuch", nil), 3*time.Second)
	if res.Success || res.Err == nil {
		t.Fatal("want a failure")
	}
	if !strings.Contains(res.Err.Error(), `relation "nosuch" does not exist`) || !strings.Contains(res.Err.Error(), "42P01") {
		t.Errorf("error = %v", res.Err)
	}
}

func TestChecks_MinRowsAndExpect(t *testing.T) {
	s := postgrestest.Start(t, people)
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
	s := postgrestest.Start(t, func(string) []postgrestest.Result {
		rows := make([][]any, 50)
		for i := range rows {
			rows[i] = []any{"x"}
		}
		return []postgrestest.Result{{Columns: []postgrestest.Column{postgrestest.Text("a")}, Rows: rows}}
	})
	res, rep := run(t, target(s.URL(), "select a", map[string]string{"max_rows": "5", "min_rows": "50"}), 3*time.Second)
	if !res.Success {
		t.Fatal(res.Err)
	}
	if rep.RowCount != 50 || len(rep.Rows) != 5 || res.BytesReceived != 50 {
		t.Errorf("count %d kept %d bytes %d", rep.RowCount, len(rep.Rows), res.BytesReceived)
	}
}

// In simple mode the client puts the values into the text, quoted, so a quote
// in a value cannot change the statement.
func TestArgs_AreFilledIn(t *testing.T) {
	s := postgrestest.Start(t, people)
	res, _ := run(t, target(s.URL(), "select * from t where id = $1 and name = $2 and ok = $3 and x is not distinct from $4",
		map[string]string{"args": `[42, "o'brien", true, null]`}), 3*time.Second)
	if !res.Success {
		t.Fatal(res.Err)
	}
	q := s.Queries()
	if len(q) != 1 || !strings.Contains(q[0], "'42'") || !strings.Contains(q[0], "'o''brien'") || !strings.Contains(q[0], "'t'") || !strings.Contains(q[0], "null") {
		t.Errorf("sent %q", q)
	}
}

func TestPool_ConnectionsAreKeptAndCapped(t *testing.T) {
	var inflight, peak atomic.Int32
	s := postgrestest.Start(t, func(string) []postgrestest.Result {
		n := inflight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
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
	s := postgrestest.Start(t, func(string) []postgrestest.Result {
		<-gate
		return people("")
	})
	defer close(gate)
	start := time.Now()
	res, _ := run(t, target(s.URL(), "select pg_sleep(9)", nil), 300*time.Millisecond)
	if res.Success || res.Err == nil || !strings.Contains(res.Err.Error(), "timed out") {
		t.Errorf("result = %+v", res)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("took %s", time.Since(start))
	}
}

func TestRunEnding_StopsTheCall(t *testing.T) {
	gate := make(chan struct{})
	s := postgrestest.Start(t, func(string) []postgrestest.Result {
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
		t.Errorf("success %v after %s", res.Success, time.Since(start))
	}
}

func TestUnreachableServer_IsAFailureNotAStop(t *testing.T) {
	d, err := New(target("postgres://127.0.0.1:1/db", "select 1", nil), time.Second)
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
	s := postgrestest.StartWithPassword(t, people, "s3cret-pw")

	t.Setenv("PG_TEST_PASSWORD", "s3cret-pw")
	res, _ := run(t, target(s.URL(), "select 1", map[string]string{"username": "app", "password_env": "PG_TEST_PASSWORD"}), 3*time.Second)
	if !res.Success {
		t.Fatalf("right password refused: %v", res.Err)
	}

	t.Setenv("PG_TEST_PASSWORD", "wrong-pw")
	res, _ = run(t, target(s.URL(), "select 1", map[string]string{"username": "app", "password_env": "PG_TEST_PASSWORD"}), 3*time.Second)
	if res.Success {
		t.Fatal("wrong password accepted")
	}
	if res.Err != nil && (strings.Contains(res.Err.Error(), "wrong-pw") || strings.Contains(res.Err.Error(), "s3cret-pw")) {
		t.Errorf("the error shows a password: %v", res.Err)
	}
}

func TestNewWithPassword_ForScripts(t *testing.T) {
	s := postgrestest.StartWithPassword(t, people, "pw1")
	pw := "pw1"
	conn, err := NewConn(protocol.Target{URL: s.URL(), Options: map[string]string{"username": "app", "sslmode": "disable"}}, 3*time.Second, &pw)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	d, err := conn.Call(map[string]string{"username": "app", "sslmode": "disable"}, []byte("select 1"), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res, _ := d.Run(context.Background()); !res.Success {
		t.Fatalf("failed: %v", res.Err)
	}
	// A second call reuses the pool, so there is one connection.
	d2, _ := conn.Call(map[string]string{"username": "app", "sslmode": "disable"}, []byte("select 2"), 3*time.Second)
	if res, _ := d2.Run(context.Background()); !res.Success {
		t.Fatalf("failed: %v", res.Err)
	}
	if s.ConnCount() != 1 {
		t.Errorf("%d connections, want 1", s.ConnCount())
	}
}

func TestReadOnlyAndApplicationName_ReachTheServer(t *testing.T) {
	s := postgrestest.Start(t, people)
	res, _ := run(t, target(s.URL(), "select 1", map[string]string{"read_only": "true", "application_name": "lt-7"}), 3*time.Second)
	if !res.Success {
		t.Fatal(res.Err)
	}
	st := s.Startups()[0]
	if st["default_transaction_read_only"] != "on" || st["application_name"] != "lt-7" {
		t.Errorf("startup = %v", st)
	}
}

func TestNew_ConfigMistakes(t *testing.T) {
	t.Setenv("PG_TEST_EMPTY", "")
	cases := map[string]protocol.Target{
		"wrong scheme":      target("mysql://h/db", "select 1", nil),
		"no host":           target("postgres:///db", "select 1", nil),
		"no sql":            target("postgres://h/db", "  ", nil),
		"unknown option":    target("postgres://h/db", "select 1", map[string]string{"bogus": "1"}),
		"password in url":   target("postgres://app:pw@h/db", "select 1", nil),
		"bad sslmode":       target("postgres://h/db", "select 1", map[string]string{"sslmode": "maybe"}),
		"bad query_mode":    target("postgres://h/db", "select 1", map[string]string{"query_mode": "fast"}),
		"pool zero":         target("postgres://h/db", "select 1", map[string]string{"pool": "0"}),
		"pool text":         target("postgres://h/db", "select 1", map[string]string{"pool": "many"}),
		"read_only text":    target("postgres://h/db", "select 1", map[string]string{"read_only": "sure"}),
		"min_rows negative": target("postgres://h/db", "select 1", map[string]string{"min_rows": "-1"}),
		"max_rows zero":     target("postgres://h/db", "select 1", map[string]string{"max_rows": "0"}),
		"args not a list":   target("postgres://h/db", "select 1", map[string]string{"args": `{"a":1}`}),
		"args trailing":     target("postgres://h/db", "select 1", map[string]string{"args": `[1] x`}),
		"password no user":  target("postgres://h/db", "select 1", map[string]string{"password_env": "PG_TEST_EMPTY"}),
		"env not set":       target("postgres://h/db", "select 1", map[string]string{"username": "u", "password_env": "PG_TEST_NOT_SET_ANYWHERE"}),
		"two databases":     target("postgres://h/one", "select 1", map[string]string{"database": "two"}),
		"two users":         target("postgres://a@h/one", "select 1", map[string]string{"username": "b"}),
	}
	for name, tg := range cases {
		d, err := New(tg, time.Second)
		if err == nil {
			_ = d.Close()
			t.Errorf("%s: want an error", name)
			continue
		}
		if !strings.HasPrefix(err.Error(), "postgres: ") {
			t.Errorf("%s: error %q does not start with postgres:", name, err)
		}
		if strings.Contains(err.Error(), "app:pw") {
			t.Errorf("%s: the error shows the password: %v", name, err)
		}
	}
}

func TestNew_AcceptsTheUsualForms(t *testing.T) {
	for _, u := range []string{
		"postgres://localhost", "postgres://localhost:5433/db", "postgresql://u@db.internal/app",
		"postgres://[::1]:5432/db",
	} {
		d, err := New(target(u, "select 1", nil), time.Second)
		if err != nil {
			t.Errorf("%s: %v", u, err)
			continue
		}
		_ = d.Close()
	}
}

func TestParseArgs(t *testing.T) {
	got, err := parseArgs(`[1, 2.5, "x", true, null, {"a": [1]}]`)
	if err != nil {
		t.Fatal(err)
	}
	if got[0] != int64(1) || got[1] != 2.5 || got[2] != "x" || got[3] != true || got[4] != nil || got[5] != `{"a":[1]}` {
		t.Errorf("args = %#v", got)
	}
	// A big whole number is not rounded through a float.
	got, err = parseArgs(`[9007199254740993]`)
	if err != nil || got[0] != int64(9007199254740993) {
		t.Errorf("big number = %#v %v", got, err)
	}
}

func TestDriverName(t *testing.T) {
	d, err := New(target("postgres://localhost/db", "select 1", nil), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if d.Name() != "postgres" {
		t.Errorf("Name = %q", d.Name())
	}
}
