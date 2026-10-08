package postgres

import (
	"context"
	"reflect"
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

// In simple mode the driver puts the values into the text, quoted, so a quote
// in a value cannot change the statement.
func TestArgs_AreFilledIn(t *testing.T) {
	s := postgrestest.Start(t, people)
	res, _ := run(t, target(s.URL(), "select * from t where id = $1 and name = $2 and ok = $3 and x is not distinct from $4",
		map[string]string{"args": `[42, "o'brien", true, null]`}), 3*time.Second)
	if !res.Success {
		t.Fatal(res.Err)
	}
	want := "select * from t where id =  42  and name = 'o''brien' and ok = true and x is not distinct from NULL"
	if q := s.Queries(); len(q) != 1 || q[0] != want {
		t.Errorf("sent %q, want %q", q, want)
	}
}

// Several statements and args work together in simple mode. The reply is the
// last statement that returned rows, and every statement is counted.
func TestArgs_WithSeveralStatements(t *testing.T) {
	s := postgrestest.Start(t, func(string) []postgrestest.Result {
		return []postgrestest.Result{
			{Tag: "BEGIN"},
			{Tag: "UPDATE 1"},
			{Columns: []postgrestest.Column{postgrestest.Int("n")}, Rows: [][]any{{"7"}}},
			{Tag: "COMMIT"},
		}
	})
	res, rep := run(t, target(s.URL(), "begin; update t set a = 1 where id = $1; select 7; commit",
		map[string]string{"args": "[1]", "min_rows": "1"}), 3*time.Second)
	if !res.Success {
		t.Fatal(res.Err)
	}
	if q := s.Queries(); len(q) != 1 || q[0] != "begin; update t set a = 1 where id =  1 ; select 7; commit" {
		t.Errorf("sent %q", q)
	}
	if rep.RowCount != 1 || rep.Rows[0][0] != int32(7) || rep.RowsAffected != 1+1 || rep.CommandTag != "COMMIT" {
		t.Errorf("reply = %+v", rep)
	}
}

func TestArgs_WrongCountIsAConfigError(t *testing.T) {
	for name, tg := range map[string]protocol.Target{
		"too few":  target("postgres://h/db", "select $1, $2", map[string]string{"args": "[1]"}),
		"too many": target("postgres://h/db", "select $1", map[string]string{"args": "[1, 2]"}),
		"none":     target("postgres://h/db", "select 1", map[string]string{"args": "[1]"}),
	} {
		if d, err := New(tg, time.Second); err == nil {
			_ = d.Close()
			t.Errorf("%s: want an error", name)
		}
	}
	// With no args, a $1 is left for the server to judge.
	if d, err := New(target("postgres://h/db", "select $1", nil), time.Second); err != nil {
		t.Errorf("no args: %v", err)
	} else {
		_ = d.Close()
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

func TestReadOnlyIsTheDefault(t *testing.T) {
	s := postgrestest.Start(t, people)
	if res, _ := run(t, target(s.URL(), "select 1", map[string]string{"application_name": "lt-7"}), 3*time.Second); !res.Success {
		t.Fatal(res.Err)
	}
	st := s.Startups()[0]
	if st["default_transaction_read_only"] != "on" || st["application_name"] != "lt-7" {
		t.Errorf("startup = %v", st)
	}
}

func TestAllowWrites_LeavesTheServerDefault(t *testing.T) {
	s := postgrestest.Start(t, people)
	if res, _ := run(t, target(s.URL(), "select 1", map[string]string{"allow_writes": "true"}), 3*time.Second); !res.Success {
		t.Fatal(res.Err)
	}
	if v, ok := s.Startups()[0]["default_transaction_read_only"]; ok {
		t.Errorf("allow_writes=true still sent default_transaction_read_only=%s", v)
	}
}

// A write refused by the server's read-only setting says how to allow it.
func TestReadOnlyRefusal_SaysHowToAllowWrites(t *testing.T) {
	s := postgrestest.Start(t, func(string) []postgrestest.Result {
		return []postgrestest.Result{{ErrCode: "25006", ErrMessage: "cannot execute UPDATE in a read-only transaction"}}
	})
	res, _ := run(t, target(s.URL(), "update t set a = 1", nil), 3*time.Second)
	if res.Success || res.Err == nil || !strings.Contains(res.Err.Error(), "allow_writes=true") || !strings.Contains(res.Err.Error(), "25006") {
		t.Errorf("error = %v", res.Err)
	}
}

// SQL can turn the read-only setting off for the session. The next call must
// not inherit that: the pool puts the setting back first.
func TestReadOnly_IsPutBackOnTheNextCall(t *testing.T) {
	s := postgrestest.Start(t, people)
	d, err := New(target(s.URL(), "set default_transaction_read_only = off", map[string]string{"pool": "1"}), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if res, _ := d.Run(context.Background()); !res.Success {
		t.Fatal(res.Err)
	}
	next, err := d.Call(map[string]string{"pool": "1", "sslmode": "disable"}, []byte("select 1"), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res, _ := next.Run(context.Background()); !res.Success {
		t.Fatal(res.Err)
	}
	want := []string{"set default_transaction_read_only = off", "set default_transaction_read_only = on", "select 1"}
	if got := s.Queries(); !reflect.DeepEqual(got, want) {
		t.Errorf("queries = %q, want %q", got, want)
	}
	if s.ConnCount() != 1 {
		t.Errorf("connections = %d, want 1 (the test needs the pool to reuse it)", s.ConnCount())
	}
}

// A normal call sends nothing extra: the server already reports read-only.
func TestReadOnly_NoExtraQueryWhenAlreadyOn(t *testing.T) {
	s := postgrestest.Start(t, people)
	if res, _ := run(t, target(s.URL(), "select 1", nil), 3*time.Second); !res.Success {
		t.Fatal(res.Err)
	}
	if got := s.Queries(); len(got) != 1 {
		t.Errorf("queries = %q, want only the user's SQL", got)
	}
}

// With allow_writes the driver never touches the setting.
func TestAllowWrites_NeverSetsReadOnly(t *testing.T) {
	s := postgrestest.Start(t, people)
	if res, _ := run(t, target(s.URL(), "select 1", map[string]string{"allow_writes": "true"}), 3*time.Second); !res.Success {
		t.Fatal(res.Err)
	}
	if got := s.Queries(); len(got) != 1 {
		t.Errorf("queries = %q, want only the user's SQL", got)
	}
}

// The hint fits how the call was made, and is left out when writes were
// already allowed (a replica, or a read-only role).
func TestReadOnlyRefusal_HintFitsTheCaller(t *testing.T) {
	refuse := func(string) []postgrestest.Result {
		return []postgrestest.Result{{ErrCode: "25006", ErrMessage: "cannot execute UPDATE in a read-only transaction"}}
	}
	s := postgrestest.Start(t, refuse)

	res, _ := run(t, target(s.URL(), "update t set a = 1", map[string]string{"allow_writes": "true"}), 3*time.Second)
	if res.Err == nil || strings.Contains(res.Err.Error(), "allow_writes") || !strings.Contains(res.Err.Error(), "25006") {
		t.Errorf("allow_writes already true: error = %v", res.Err)
	}

	conn, err := NewConn(target(s.URL(), "", nil), 3*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	call, err := conn.Call(map[string]string{"sslmode": "disable"}, []byte("update t set a = 1"), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	res, _ = call.Run(context.Background())
	if res.Err == nil || strings.Contains(res.Err.Error(), "-opt") || !strings.Contains(res.Err.Error(), "allow_writes: true") {
		t.Errorf("script: error = %v", res.Err)
	}
}

// The old option name is gone, and it says what the options are.
func TestReadOnlyOptionIsGone(t *testing.T) {
	_, err := New(target("postgres://h/db", "select 1", map[string]string{"read_only": "true"}), time.Second)
	if err == nil || !strings.Contains(err.Error(), "allow_writes") {
		t.Errorf("error = %v", err)
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
		"allow_writes text": target("postgres://h/db", "select 1", map[string]string{"allow_writes": "sure"}),
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

func TestURLQuery_SslmodeAndApplicationNameAreHonoured(t *testing.T) {
	s := postgrestest.Start(t, people)
	// The fake server refuses TLS, so sslmode=require must fail, which shows
	// that the URL's sslmode was used and not the default (prefer, which
	// would have connected).
	tg := protocol.Target{URL: s.URL() + "?sslmode=require", Body: []byte("select 1")}
	if res, _ := run(t, tg, 3*time.Second); res.Success {
		t.Error("sslmode=require in the URL was ignored: the call worked without TLS")
	}
	tg = protocol.Target{URL: s.URL() + "?sslmode=disable&application_name=from-url", Body: []byte("select 1")}
	if res, _ := run(t, tg, 3*time.Second); !res.Success {
		t.Fatalf("failed: %v", res.Err)
	}
	if st := s.Startups(); st[len(st)-1]["application_name"] != "from-url" {
		t.Errorf("startup = %v", st[len(st)-1])
	}
	// The same setting in the URL and in -opt is fine if they agree, and an
	// error if they differ.
	ok := protocol.Target{URL: s.URL() + "?sslmode=disable", Body: []byte("select 1"), Options: map[string]string{"sslmode": "disable"}}
	if d, err := New(ok, time.Second); err != nil {
		t.Errorf("same value twice: %v", err)
	} else {
		_ = d.Close()
	}
	bad := protocol.Target{URL: s.URL() + "?sslmode=disable", Body: []byte("select 1"), Options: map[string]string{"sslmode": "require"}}
	if _, err := New(bad, time.Second); err == nil {
		t.Error("different values must be an error")
	}
}

func TestURLQuery_OtherSettingsAreRefusedWithoutShowingValues(t *testing.T) {
	for _, raw := range []string{
		"postgres://h/db?password=hunter2",
		"postgres://h/db?sslpassword=hunter2",
		"postgres://h/db?passfile=/tmp/hunter2",
		"postgres://h/db?connect_timeout=hunter2",
		"postgres://h/db?options=-c%20hunter2",
		"postgres://h/db?sslmode=disable&sslmode=require",
	} {
		_, err := New(protocol.Target{URL: raw, Body: []byte("select 1")}, time.Second)
		if err == nil {
			t.Errorf("%s: want an error", raw)
			continue
		}
		if strings.Contains(err.Error(), "hunter2") {
			t.Errorf("%s: the error shows the value: %v", raw, err)
		}
	}
	// A URL with no host does not echo the URL back either.
	_, err := New(protocol.Target{URL: "postgres:///db?password=hunter2", Body: []byte("select 1")}, time.Second)
	if err == nil || strings.Contains(err.Error(), "hunter2") {
		t.Errorf("no host: %v", err)
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
