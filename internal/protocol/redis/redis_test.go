package redis

import (
	"context"
	"errors"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/protocol"
	"github.com/vegaload/vegaload/internal/protocol/redis/redistest"
)

func mustDriver(t *testing.T, url, body string, opts map[string]string, timeout time.Duration) *Driver {
	t.Helper()
	if timeout == 0 {
		timeout = time.Second
	}
	d, err := New(protocol.Target{URL: url, Body: []byte(body), Options: opts}, timeout)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func okHandler(args []string) redistest.Value { return redistest.SimpleString("OK") }

func TestNew_DoesNotConnect(t *testing.T) {
	s := redistest.Start(t, okHandler)
	mustDriver(t, s.URL(), "PING", nil, 0)
	if n := s.ConnCount(); n != 0 {
		t.Fatalf("New opened %d connections", n)
	}
}

func TestRun_ReplyTypes(t *testing.T) {
	s := redistest.Start(t, func(args []string) redistest.Value {
		switch strings.ToUpper(args[0]) {
		case "GET":
			if len(args) > 1 && args[1] == "missing" {
				return redistest.NilValue()
			}
			return redistest.BulkString("ann")
		case "INCR":
			return redistest.Int(3)
		case "LRANGE":
			return redistest.ArrayOf(redistest.BulkString("a"), redistest.BulkString("b"), redistest.NilValue())
		case "BINARY":
			return redistest.BulkString(string([]byte{0xff}))
		default:
			return redistest.SimpleString("PONG")
		}
	})
	d := mustDriver(t, s.URL(), "GET user:42", nil, 0)
	res, rep := d.Run(context.Background())
	if !res.Success || rep.Value != "ann" || rep.RowCount != 1 {
		t.Fatalf("%+v %+v", res, rep)
	}
	d = mustDriver(t, s.URL(), "GET missing", nil, 0)
	res, rep = d.Run(context.Background())
	if !res.Success || rep.Value != nil || rep.RowCount != 0 {
		t.Fatalf("missing key: %+v %+v", res, rep)
	}
	d = mustDriver(t, s.URL(), "INCR n", map[string]string{"allow_writes": "true"}, 0)
	res, rep = d.Run(context.Background())
	if !res.Success || rep.Value != int64(3) || rep.RowCount != 1 {
		t.Fatalf("int: %+v %+v", res, rep)
	}
	d = mustDriver(t, s.URL(), "LRANGE k 0 -1", map[string]string{"max_rows": "2"}, 0)
	res, rep = d.Run(context.Background())
	vals, _ := rep.Value.([]any)
	if !res.Success || rep.RowCount != 3 || len(vals) != 2 || vals[0] != "a" || vals[2-1] != "b" {
		t.Fatalf("array: %+v %#v", res, rep)
	}
	d = mustDriver(t, s.URL(), "BINARY", map[string]string{"allow_writes": "true"}, 0)
	res, rep = d.Run(context.Background())
	if !res.Success || rep.Value != "\uFFFD" {
		t.Fatalf("binary: %+v %#v", res, rep.Value)
	}
}

func TestNormalize_RESP3Shapes(t *testing.T) {
	m, n, b := normalize(map[any]any{"k": int64(2), "s": "hi"}, 10)
	obj, _ := m.(map[string]any)
	if n != 1 || obj["k"] != int64(2) || obj["s"] != "hi" || b < 2 {
		t.Fatalf("%#v n=%d b=%d", m, n, b)
	}
	if v, _, _ := normalize(float64(1.5), 10); v != 1.5 {
		t.Fatalf("float %#v", v)
	}
	if v, _, _ := normalize(true, 10); v != true {
		t.Fatalf("bool %#v", v)
	}
	if v, _, _ := normalize(big.NewInt(99), 10); v != "99" {
		t.Fatalf("bigint %#v", v)
	}
	// Bytes count the whole array, including the element max_rows drops.
	_, n, b = normalize([]any{"abcd", "ef"}, 1)
	if n != 2 || b != 6 {
		t.Fatalf("count %d bytes %d", n, b)
	}
}

func TestRun_ServerErrorKeepsTheCode(t *testing.T) {
	s := redistest.Start(t, func([]string) redistest.Value {
		return redistest.ErrorString("WRONGTYPE Operation against a key holding the wrong kind of value")
	})
	d := mustDriver(t, s.URL(), "GET k", nil, 0)
	res, _ := d.Run(context.Background())
	if res.Success || !strings.Contains(res.Err.Error(), "WRONGTYPE") {
		t.Fatal(res.Err)
	}
}

func TestRun_Pipeline(t *testing.T) {
	s := redistest.Start(t, func(args []string) redistest.Value {
		if strings.EqualFold(args[0], "GET") {
			return redistest.BulkString(args[1])
		}
		return redistest.ErrorString("WRONGTYPE bad type")
	})
	d := mustDriver(t, s.URL(), "GET a\nGET b", nil, 0)
	res, rep := d.Run(context.Background())
	if !res.Success || rep.Value != "b" || len(rep.Values) != 2 || rep.Values[0] != "a" {
		t.Fatalf("%+v %+v", res, rep)
	}
	cmds := s.Commands()
	if len(cmds) != 2 || strings.Join(cmds[0], " ") != "GET a" || strings.Join(cmds[1], " ") != "GET b" {
		t.Fatalf("commands %#v", cmds)
	}
	for _, c := range cmds {
		if strings.EqualFold(c[0], "MULTI") {
			t.Fatal("a pipeline is not a transaction")
		}
	}

	d = mustDriver(t, s.URL(), "GET a\nSET secret b\nGET c", map[string]string{"allow_writes": "true"}, 0)
	res, rep = d.Run(context.Background())
	if res.Success || !strings.Contains(res.Err.Error(), "command 2 (SET):") || !strings.Contains(res.Err.Error(), "WRONGTYPE") {
		t.Fatal(res.Err)
	}
	if strings.Contains(res.Err.Error(), "secret") {
		t.Fatalf("error includes an argument: %v", res.Err)
	}
	if rep.Values[0] != "a" || rep.Values[1] != nil || rep.Values[2] != "c" {
		t.Fatalf("partial replies %#v", rep.Values)
	}
}

func TestRun_TransactionSendsMultiExec(t *testing.T) {
	s := redistest.Start(t, func(args []string) redistest.Value {
		return redistest.BulkString("ok")
	})
	d := mustDriver(t, s.URL(), "GET a", map[string]string{"transaction": "true"}, 0)
	res, rep := d.Run(context.Background())
	if !res.Success || rep.Value != "ok" {
		t.Fatalf("%+v %+v", res, rep)
	}
	var sawMulti, sawExec, sawGet bool
	for _, c := range s.Commands() {
		switch strings.ToUpper(c[0]) {
		case "MULTI":
			sawMulti = true
		case "EXEC":
			sawExec = true
		case "GET":
			sawGet = true
		}
	}
	if !sawMulti || !sawExec || !sawGet {
		t.Fatalf("commands %#v", s.Commands())
	}
}

func TestRun_PlaceholderBytes(t *testing.T) {
	s := redistest.Start(t, func(args []string) redistest.Value {
		if strings.EqualFold(args[0], "GET") {
			return redistest.BulkString(args[1])
		}
		return redistest.SimpleString("OK")
	})
	d := mustDriver(t, s.URL(), "SET ? ?", map[string]string{
		"allow_writes": "true",
		"args":         `["a b", "say \"hi\"\r\nFLUSHALL"]`,
	}, 0)
	res, _ := d.Run(context.Background())
	if !res.Success {
		t.Fatal(res.Err)
	}
	cmds := s.Commands()
	if len(cmds) != 1 || len(cmds[0]) != 3 || cmds[0][0] != "SET" || cmds[0][1] != "a b" || cmds[0][2] != "say \"hi\"\r\nFLUSHALL" {
		t.Fatalf("%#v", cmds)
	}
}

func TestRun_Checks(t *testing.T) {
	s := redistest.Start(t, func(args []string) redistest.Value {
		if len(args) > 1 && args[1] == "missing" {
			return redistest.NilValue()
		}
		return redistest.ArrayOf(redistest.BulkString("a"), redistest.BulkString("b"), redistest.BulkString("c"))
	})
	d := mustDriver(t, s.URL(), "GET missing", map[string]string{"min_rows": "1"}, 0)
	res, _ := d.Run(context.Background())
	if res.Success || !strings.Contains(res.Err.Error(), "at least 1") {
		t.Fatal(res.Err)
	}
	d = mustDriver(t, s.URL(), "LRANGE k 0 -1", map[string]string{"min_rows": "3", "max_rows": "1", "expect": "a"}, 0)
	res, rep := d.Run(context.Background())
	if !res.Success || rep.RowCount != 3 || len(rep.Value.([]any)) != 1 {
		t.Fatalf("%+v %+v", res, rep)
	}
	d = mustDriver(t, s.URL(), "LRANGE k 0 -1", map[string]string{"max_rows": "1", "expect": "b"}, 0)
	res, _ = d.Run(context.Background())
	if res.Success {
		t.Fatal("expect must look only at kept elements")
	}
}

func TestSafety_ReadWriteAdmin(t *testing.T) {
	s := redistest.Start(t, okHandler)
	for name := range readCommands {
		d := mustDriver(t, s.URL(), name, nil, 0)
		res, _ := d.Run(context.Background())
		if !res.Success {
			t.Errorf("read %s: %v", name, res.Err)
		}
	}
	before := len(s.Commands())
	for _, name := range []string{"SET", "del", "expire", "INCR", "getex", "getdel", "touch", "publish", "xadd", "xreadgroup", "xack", "eval", "evalsha", "fcall", "wait", "blpop", "notacommand"} {
		d := mustDriver(t, s.URL(), name+" a", nil, 0)
		res, _ := d.Run(context.Background())
		if res.Success || !strings.Contains(res.Err.Error(), "allow_writes=true") {
			t.Errorf("write %s: %v", name, res.Err)
		}
	}
	if len(s.Commands()) != before {
		t.Fatalf("a refused command was sent: %d to %d", before, len(s.Commands()))
	}

	d := mustDriver(t, s.URL(), "GET a\nGET b\nSET c d\nGET e", nil, 0)
	res, _ := d.Run(context.Background())
	if res.Success || len(s.Commands()) != before {
		t.Fatalf("pipeline with a write was sent: %v %#v", res.Err, s.Commands()[before:])
	}

	d = mustDriver(t, s.URL(), "SET a b", map[string]string{"allow_writes": "true"}, 0)
	res, _ = d.Run(context.Background())
	if !res.Success {
		t.Fatal(res.Err)
	}

	for _, name := range []string{"SELECT 2", "AUTH x", "HELLO 3", "SUBSCRIBE ch", "WATCH k", "MULTI", "EXEC", "CLIENT SETNAME n"} {
		d = mustDriver(t, s.URL(), name, map[string]string{"allow_writes": "true", "allow_admin": "true"}, 0)
		res, _ = d.Run(context.Background())
		if res.Success || !strings.Contains(res.Err.Error(), "not supported") {
			t.Errorf("never %s: %v", name, res.Err)
		}
	}
	if len(s.Commands()) != before+1 { // the allowed SET
		t.Fatalf("a never-allowed command was sent, commands=%d", len(s.Commands()))
	}

	d = mustDriver(t, s.URL(), "FLUSHALL", map[string]string{"allow_writes": "true"}, 0)
	res, _ = d.Run(context.Background())
	if res.Success || !strings.Contains(res.Err.Error(), "allow_admin=true") {
		t.Fatal(res.Err)
	}
	d = mustDriver(t, s.URL(), "FLUSHALL", map[string]string{"allow_writes": "true", "allow_admin": "true"}, 0)
	if res, _ = d.Run(context.Background()); !res.Success {
		t.Fatal(res.Err)
	}
	d = mustDriver(t, s.URL(), "CONFIG GET save", nil, 0)
	if res, _ = d.Run(context.Background()); !res.Success {
		t.Fatal(res.Err)
	}
	d = mustDriver(t, s.URL(), "CONFIG SET save \"\"", map[string]string{"allow_writes": "true"}, 0)
	res, _ = d.Run(context.Background())
	if res.Success || !strings.Contains(res.Err.Error(), "CONFIG SET") || !strings.Contains(res.Err.Error(), "allow_admin") {
		t.Fatal(res.Err)
	}
	d = mustDriver(t, s.URL(), "ACL WHOAMI", nil, 0)
	if res, _ = d.Run(context.Background()); !res.Success {
		t.Fatal(res.Err)
	}
	d = mustDriver(t, s.URL(), "ACL SETUSER u", map[string]string{"allow_writes": "true"}, 0)
	res, _ = d.Run(context.Background())
	if res.Success || !strings.Contains(res.Err.Error(), "ACL SETUSER") {
		t.Fatal(res.Err)
	}
}

func TestSafety_Hints(t *testing.T) {
	s := redistest.Start(t, okHandler)
	d := mustDriver(t, s.URL(), "SET a b", nil, 0)
	res, _ := d.Run(context.Background())
	if !strings.Contains(res.Err.Error(), "-opt allow_writes=true") {
		t.Fatal(res.Err)
	}
	conn, err := NewConn(protocol.Target{URL: s.URL()}, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	call, err := conn.Call(nil, []byte("SET a b"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	res, _ = call.Run(context.Background())
	if !strings.Contains(res.Err.Error(), "pass allow_writes: true in the connection options") {
		t.Fatal(res.Err)
	}
}

func TestCall_KeepsParentFlags(t *testing.T) {
	s := redistest.Start(t, okHandler)
	conn, err := NewConn(protocol.Target{
		URL:     s.URL(),
		Options: map[string]string{"allow_writes": "true", "allow_admin": "true"},
	}, time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	call, err := conn.Call(map[string]string{}, []byte("FLUSHALL"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	res, _ := call.Run(context.Background())
	if !res.Success {
		t.Fatalf("Call dropped the parent's flags: %v", res.Err)
	}
	if err := call.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := conn.Call(nil, []byte("PING"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res, _ = again.Run(context.Background()); !res.Success {
		t.Fatal(res.Err)
	}
}

func TestRun_RetriesOff(t *testing.T) {
	s := redistest.Start(t, okHandler)
	s.CloseAfterUserCommands(1)
	d := mustDriver(t, s.URL(), "PING", nil, time.Second)
	res, _ := d.Run(context.Background())
	if res.Success {
		t.Fatal("the closed connection should fail the call")
	}
	if n := len(s.Commands()); n != 1 {
		t.Fatalf("command was sent %d times, want 1", n)
	}
}

func TestRun_HelloFallback(t *testing.T) {
	s := redistest.Start(t, okHandler)
	s.DisableHello()
	d := mustDriver(t, s.URL(), "PING", nil, 0)
	res, rep := d.Run(context.Background())
	if !res.Success || rep.Value != "PONG" {
		t.Fatalf("%+v %+v", res, rep)
	}
	var sawHello bool
	for _, c := range s.Setup() {
		if strings.EqualFold(c[0], "HELLO") {
			sawHello = true
		}
	}
	if !sawHello {
		t.Fatalf("setup %#v", s.Setup())
	}
}

func TestRun_TimeoutAndCancel(t *testing.T) {
	s := redistest.Start(t, func([]string) redistest.Value {
		time.Sleep(5 * time.Second)
		return redistest.SimpleString("OK")
	})
	d := mustDriver(t, s.URL(), "GET k", nil, 300*time.Millisecond)
	start := time.Now()
	res, _ := d.Run(context.Background())
	if time.Since(start) > 3*time.Second {
		t.Fatalf("waited %s, want the call timeout", time.Since(start))
	}
	if res.Success || !strings.Contains(res.Err.Error(), "timed out after 300ms") {
		t.Fatal(res.Err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d = mustDriver(t, s.URL(), "GET k", nil, time.Second)
	res, err := d.Do(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !errors.Is(res.Err, context.Canceled) {
		t.Fatalf("%v", res.Err)
	}
}

func TestRun_UnreachableIsAFailedResult(t *testing.T) {
	d := mustDriver(t, "redis://127.0.0.1:1", "PING", nil, time.Second)
	res, err := d.Do(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if res.Success || !strings.Contains(res.Err.Error(), "could not connect to 127.0.0.1:1") {
		t.Fatal(res.Err)
	}
}

func TestPassword_SentAndHidden(t *testing.T) {
	const secret = "s3cret-value"
	t.Setenv("REDIS_PW", secret)
	s := redistest.StartWithPassword(t, okHandler, secret)
	d := mustDriver(t, s.URL(), "PING", map[string]string{"password_env": "REDIS_PW", "username": "ann"}, 0)
	res, _ := d.Run(context.Background())
	if !res.Success {
		t.Fatal(res.Err)
	}
	var sawUser, sawPass bool
	for _, c := range s.Setup() {
		if !strings.EqualFold(c[0], "HELLO") {
			continue
		}
		for i, a := range c {
			if a == "ann" {
				sawUser = true
			}
			if a == secret {
				sawPass = true
				_ = i
			}
		}
	}
	if !sawUser || !sawPass {
		t.Fatalf("setup did not log in as ann: %#v", s.Setup())
	}

	t.Setenv("REDIS_PW", "wrong-password")
	d = mustDriver(t, s.URL(), "PING", map[string]string{"password_env": "REDIS_PW"}, 0)
	res, _ = d.Run(context.Background())
	if res.Success || !strings.Contains(res.Err.Error(), "WRONGPASS") {
		t.Fatal(res.Err)
	}
	if strings.Contains(res.Err.Error(), "wrong-password") || strings.Contains(res.Err.Error(), secret) {
		t.Fatalf("password leaked: %v", res.Err)
	}

	pw := secret
	conn, err := NewConn(protocol.Target{URL: s.URL(), Options: map[string]string{"username": "ann"}}, time.Second, &pw)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	call, err := conn.Call(nil, []byte("PING"), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res, _ = call.Run(context.Background()); !res.Success {
		t.Fatal(res.Err)
	}
}

func TestDatabase_Select(t *testing.T) {
	s := redistest.Start(t, okHandler)
	d := mustDriver(t, s.URL(), "PING", map[string]string{"database": "2"}, 0)
	if res, _ := d.Run(context.Background()); !res.Success {
		t.Fatal(res.Err)
	}
	if !sawSelect(s, "2") {
		t.Fatalf("setup %#v", s.Setup())
	}
	s0 := redistest.Start(t, okHandler)
	d = mustDriver(t, s0.URL(), "PING", map[string]string{"database": "0"}, 0)
	if res, _ := d.Run(context.Background()); !res.Success {
		t.Fatal(res.Err)
	}
	if sawSelect(s0, "0") || sawSelect(s0, "2") {
		t.Fatalf("database 0 must not SELECT: %#v", s0.Setup())
	}
	s2 := redistest.Start(t, okHandler)
	d = mustDriver(t, s2.URL()+"/2", "PING", nil, 0)
	if res, _ := d.Run(context.Background()); !res.Success {
		t.Fatal(res.Err)
	}
	if !sawSelect(s2, "2") {
		t.Fatalf("setup %#v", s2.Setup())
	}
}

func sawSelect(s *redistest.Server, db string) bool {
	for _, c := range s.Setup() {
		if strings.EqualFold(c[0], "SELECT") && len(c) > 1 && c[1] == db {
			return true
		}
	}
	return false
}

func TestTLS(t *testing.T) {
	s := redistest.StartTLS(t, okHandler)
	if _, err := New(protocol.Target{URL: s.URL(), Body: []byte("PING"), Options: map[string]string{"tls": "false"}}, time.Second); err == nil || !strings.Contains(err.Error(), "tls=false") {
		t.Fatal(err)
	}
	d := mustDriver(t, s.URL(), "PING", map[string]string{"tls": "true"}, time.Second)
	res, _ := d.Run(context.Background())
	if res.Success {
		t.Fatal("a self-signed certificate must fail verification")
	}
	d = mustDriver(t, s.URL(), "PING", map[string]string{"tls": "skip-verify"}, time.Second)
	if res, _ = d.Run(context.Background()); !res.Success {
		t.Fatal(res.Err)
	}
	plain := strings.Replace(s.URL(), "rediss://", "redis://", 1)
	d, err := New(protocol.Target{URL: plain, Body: []byte("PING"), Options: map[string]string{"tls": "true"}, InsecureSkipVerify: true}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if res, _ = d.Run(context.Background()); !res.Success {
		t.Fatal(res.Err)
	}
}

func TestConfigMistakes(t *testing.T) {
	cases := []struct {
		url  string
		opts map[string]string
		body string
		want string
		hide string
	}{
		{"http://h", nil, "PING", "unsupported scheme", ""},
		{"redis://", nil, "PING", "no host", ""},
		{"redis://:s3cret@127.0.0.1:1", nil, "PING", "do not put a password in the URL", "s3cret"},
		{"redis://127.0.0.1:1?password=s3cret", nil, "PING", `setting "password"`, "s3cret"},
		{"redis://127.0.0.1:1?foo=s3cret", nil, "PING", `setting "foo"`, "s3cret"},
		{"redis://ann@127.0.0.1:1", map[string]string{"username": "bob"}, "PING", "not both", ""},
		{"redis://127.0.0.1:1/2", map[string]string{"database": "3"}, "PING", "not both", ""},
		{"redis://127.0.0.1:1", map[string]string{"pool": "0"}, "PING", "pool must be", ""},
		{"redis://127.0.0.1:1", map[string]string{"pool": "1001"}, "PING", "pool must be", ""},
		{"redis://127.0.0.1:1", map[string]string{"protocol": "1"}, "PING", "protocol must be 2 or 3", ""},
		{"redis://127.0.0.1:1", map[string]string{"database": "256"}, "PING", "0 to 255", ""},
		{"redis://127.0.0.1:1", map[string]string{"database": "-1"}, "PING", "0 to 255", ""},
		{"redis://127.0.0.1:1", map[string]string{"allow_admin": "true"}, "PING", "needs allow_writes=true", ""},
		{"redis://127.0.0.1:1", map[string]string{"nope": "1"}, "PING", "unknown option nope", ""},
		{"redis://127.0.0.1:1", map[string]string{"tls": "maybe"}, "PING", "tls must be", ""},
		{"redis://127.0.0.1:1", nil, "", "the command is required", ""},
		{"redis://127.0.0.1:1", map[string]string{"max_rows": "0"}, "PING", "max_rows", ""},
	}
	for _, c := range cases {
		_, err := New(protocol.Target{URL: c.url, Body: []byte(c.body), Options: c.opts}, time.Second)
		if err == nil || !strings.Contains(err.Error(), c.want) || !strings.HasPrefix(err.Error(), "redis:") {
			t.Errorf("%s %v: %v, want %q", c.url, c.opts, err, c.want)
			continue
		}
		if c.hide != "" && strings.Contains(err.Error(), c.hide) {
			t.Errorf("%s leaked %q: %v", c.url, c.hide, err)
		}
	}
}

func TestRun_ConcurrentPoolOne(t *testing.T) {
	s := redistest.Start(t, func(args []string) redistest.Value {
		if strings.EqualFold(args[0], "GET") {
			return redistest.BulkString("v")
		}
		return redistest.SimpleString("PONG")
	})
	runMany := func(body string) {
		t.Helper()
		d := mustDriver(t, s.URL(), body, map[string]string{"pool": "1"}, 5*time.Second)
		var wg sync.WaitGroup
		errCh := make(chan error, 16)
		for i := 0; i < 16; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				res, _ := d.Run(context.Background())
				if !res.Success {
					errCh <- res.Err
				}
			}()
		}
		wg.Wait()
		close(errCh)
		for err := range errCh {
			t.Errorf("%s: %v", body, err)
		}
	}
	runMany("PING")
	runMany("GET a\nGET b")
}

func TestName(t *testing.T) {
	d := mustDriver(t, "redis://127.0.0.1:1", "PING", nil, 0)
	if d.Name() != "redis" {
		t.Fatal(d.Name())
	}
}
