package redis

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/protocol"
)

func liveBase(t *testing.T) (string, map[string]string) {
	t.Helper()
	raw := os.Getenv("VEGALOAD_TEST_REDIS")
	if raw == "" {
		t.Skip("VEGALOAD_TEST_REDIS is not set")
	}
	opts := map[string]string{"allow_writes": "true"}
	if name := os.Getenv("VEGALOAD_TEST_REDIS_PASSWORD_ENV"); name != "" {
		opts["password_env"] = name
	}
	return raw, opts
}

func liveRun(t *testing.T, url, body string, opts map[string]string, timeout time.Duration) (protocol.Result, Reply) {
	t.Helper()
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	d, err := New(protocol.Target{URL: url, Body: []byte(body), Options: opts}, timeout)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	return d.Run(context.Background())
}

func TestLive_TypesPipelineTransactionAndArgs(t *testing.T) {
	url, opts := liveBase(t)
	for _, proto := range []string{"2", "3"} {
		o := cloneOpts(opts)
		o["protocol"] = proto
		key := "vegaload:test:str:" + proto
		t.Cleanup(func() { liveDel(url, opts, key) })
		res, rep := liveRun(t, url, "SET ? ?", withArgs(o, `["`+key+`", "ann"]`), 0)
		if !res.Success || rep.Value != "OK" {
			t.Fatalf("proto %s SET: %+v %+v", proto, res.Err, rep)
		}
		res, rep = liveRun(t, url, "GET ?", withArgs(o, `["`+key+`"]`), 0)
		if !res.Success || rep.Value != "ann" {
			t.Fatalf("proto %s GET: %+v %#v", proto, res.Err, rep.Value)
		}
		res, rep = liveRun(t, url, "INCR ?", withArgs(o, `["vegaload:test:n:`+proto+`"]`), 0)
		t.Cleanup(func() { liveDel(url, opts, "vegaload:test:n:"+proto) })
		if !res.Success || rep.Value != int64(1) {
			t.Fatalf("proto %s INCR: %+v %#v", proto, res.Err, rep.Value)
		}
		list := "vegaload:test:list:" + proto
		t.Cleanup(func() { liveDel(url, opts, list) })
		if res, _ = liveRun(t, url, "RPUSH ? a b", withArgs(o, `["`+list+`"]`), 0); !res.Success {
			t.Fatal(res.Err)
		}
		hash := "vegaload:test:hash:" + proto
		t.Cleanup(func() { liveDel(url, opts, hash) })
		if res, _ = liveRun(t, url, "HSET ? f ann", withArgs(o, `["`+hash+`"]`), 0); !res.Success {
			t.Fatal(res.Err)
		}
		set := "vegaload:test:set:" + proto
		t.Cleanup(func() { liveDel(url, opts, set) })
		if res, _ = liveRun(t, url, "SADD ? a", withArgs(o, `["`+set+`"]`), 0); !res.Success {
			t.Fatal(res.Err)
		}
		zset := "vegaload:test:zset:" + proto
		t.Cleanup(func() { liveDel(url, opts, zset) })
		if res, _ = liveRun(t, url, "ZADD ? 1 a", withArgs(o, `["`+zset+`"]`), 0); !res.Success {
			t.Fatal(res.Err)
		}
		stream := "vegaload:test:stream:" + proto
		t.Cleanup(func() { liveDel(url, opts, stream) })
		res, rep = liveRun(t, url, "XADD ? * f v", withArgs(o, `["`+stream+`"]`), 0)
		if !res.Success {
			t.Fatalf("XADD: %v", res.Err)
		}
		if res, rep = liveRun(t, url, "XLEN ?", withArgs(o, `["`+stream+`"]`), 0); !res.Success || rep.Value != int64(1) {
			t.Fatalf("XLEN: %+v %#v", res.Err, rep.Value)
		}
		res, rep = liveRun(t, url, "XRANGE ? - +", withArgs(o, `["`+stream+`"]`), 0)
		if !res.Success || rep.RowCount < 1 {
			t.Fatalf("XRANGE: %+v rowCount %d", res.Err, rep.RowCount)
		}
		if res, _ = liveRun(t, url, "XREAD COUNT 1 STREAMS ? 0", withArgs(o, `["`+stream+`"]`), 0); !res.Success {
			t.Fatal(res.Err)
		}
	}

	o := cloneOpts(opts)
	pipeKey := "vegaload:test:pipe"
	t.Cleanup(func() { liveDel(url, opts, pipeKey) })
	res, rep := liveRun(t, url, "SET ? one\nSET ? two", withArgs(o, `["`+pipeKey+`", "`+pipeKey+`"]`), 0)
	if !res.Success || len(rep.Values) != 2 || rep.Values[0] != "OK" || rep.Values[1] != "OK" {
		t.Fatalf("pipeline: %+v %+v", res.Err, rep)
	}
	res, rep = liveRun(t, url, "GET ?\nGET ?", withArgs(cloneOpts(opts), `["`+pipeKey+`", "`+pipeKey+`"]`), 0)
	// The second SET replaced the value. Both reads see "two", in order.
	if !res.Success || rep.Values[0] != "two" || rep.Values[1] != "two" {
		t.Fatalf("pipeline read: %+v %#v", res.Err, rep.Values)
	}
	txKey := "vegaload:test:tx"
	t.Cleanup(func() { liveDel(url, opts, txKey) })
	o = cloneOpts(opts)
	o["transaction"] = "true"
	res, rep = liveRun(t, url, "SET ? one\nSET ? two", withArgs(o, `["`+txKey+`", "`+txKey+`"]`), 0)
	if !res.Success || rep.Values[0] != "OK" || rep.Values[1] != "OK" {
		t.Fatalf("transaction: %+v %+v", res.Err, rep)
	}

	exact := "vegaload:test:exact"
	t.Cleanup(func() { liveDel(url, opts, exact) })
	raw := "a b \"q\"\r\nü"
	res, _ = liveRun(t, url, "SET ? ?", withArgs(cloneOpts(opts), mustJSON(exact, raw)), 0)
	if !res.Success {
		t.Fatal(res.Err)
	}
	res, rep = liveRun(t, url, "GET ?", withArgs(cloneOpts(opts), mustJSON(exact)), 0)
	if !res.Success || rep.Value != raw {
		t.Fatalf("round trip %#v err %v", rep.Value, res.Err)
	}
}

func TestLive_SafetyAndPublish(t *testing.T) {
	url, opts := liveBase(t)
	key := "vegaload:test:ro"
	ro := cloneOpts(opts)
	delete(ro, "allow_writes")
	res, _ := liveRun(t, url, "SET ? x", withArgs(ro, `["`+key+`"]`), 0)
	if res.Success || !strings.Contains(res.Err.Error(), "allow_writes") {
		t.Fatal(res.Err)
	}
	res, rep := liveRun(t, url, "GET ?", withArgs(ro, `["`+key+`"]`), 0)
	if !res.Success || rep.Value != nil {
		t.Fatalf("key exists after a refused SET: %+v %#v", res.Err, rep.Value)
	}
	if res, _ = liveRun(t, url, "SET ? x", withArgs(cloneOpts(opts), `["`+key+`"]`), 0); !res.Success {
		t.Fatal(res.Err)
	}
	t.Cleanup(func() { liveDel(url, opts, key) })
	res, _ = liveRun(t, url, "FLUSHDB", cloneOpts(opts), 0)
	if res.Success || !strings.Contains(res.Err.Error(), "allow_admin") {
		t.Fatal(res.Err)
	}
	ch := "vegaload:test:ch"
	res, rep = liveRun(t, url, "PUBLISH ? hello", withArgs(cloneOpts(opts), `["`+ch+`"]`), 0)
	if !res.Success || rep.Value != int64(0) {
		t.Fatalf("PUBLISH: %+v %#v", res.Err, rep.Value)
	}
}

func TestLive_BlockingAndPool(t *testing.T) {
	url, opts := liveBase(t)
	o := cloneOpts(opts)
	conn, err := NewConn(protocol.Target{URL: url, Options: o}, 5*time.Second, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	blocked, err := conn.Call(nil, []byte("BLPOP vegaload:test:none 5"), 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	res, _ := blocked.Run(context.Background())
	elapsed := time.Since(start)
	if res.Success || elapsed < 200*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("BLPOP elapsed %s err %v", elapsed, res.Err)
	}
	next, err := conn.Call(nil, []byte("PING"), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res, _ = next.Run(context.Background()); !res.Success {
		t.Fatalf("pool unusable after a timeout: %v", res.Err)
	}

	runMany := func(body string) {
		t.Helper()
		d, err := New(protocol.Target{URL: url, Body: []byte(body), Options: withPool(o, "1")}, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		defer d.Close()
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
	runMany("PING\nPING")
}

func TestLive_ACLUser(t *testing.T) {
	url, opts := liveBase(t)
	admin := cloneOpts(opts)
	admin["allow_admin"] = "true"
	const user = "vegaload-test"
	const pw = "vegaload-acl-pw"
	res, _ := liveRun(t, url, "ACL SETUSER ? on >"+pw+" ~vegaload:test:* +get +set +ping", withArgs(admin, `["`+user+`"]`), 0)
	if !res.Success {
		t.Fatal(res.Err)
	}
	t.Cleanup(func() {
		// admin must not still carry the args from the SETUSER call.
		gone, _ := liveRun(t, url, "ACL DELUSER "+user, cloneOpts(admin), 0)
		if !gone.Success {
			t.Errorf("deleting ACL user %s: %v", user, gone.Err)
		}
	})
	pass := pw
	d, err := NewConn(protocol.Target{URL: url, Options: map[string]string{
		"username": user, "allow_writes": "true", "pool": "1",
	}}, 5*time.Second, &pass)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	call, err := d.Call(nil, []byte("SET vegaload:test:acl 1"), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if res, _ = call.Run(context.Background()); !res.Success {
		t.Fatal(res.Err)
	}
	t.Cleanup(func() { liveDel(url, opts, "vegaload:test:acl") })
	call, err = d.Call(nil, []byte("SET other:key 1"), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	res, _ = call.Run(context.Background())
	if res.Success || !strings.Contains(res.Err.Error(), "NOPERM") {
		t.Fatalf("%v", res.Err)
	}
}

func TestLive_CommandInfoAgrees(t *testing.T) {
	url, opts := liveBase(t)
	o := cloneOpts(opts)
	delete(o, "allow_writes")
	for name := range readCommands {
		res, rep := liveRun(t, url, "COMMAND INFO "+name, o, 0)
		if !res.Success {
			t.Fatalf("COMMAND INFO %s: %v", name, res.Err)
		}
		flags := commandFlags(rep.Value)
		for _, f := range flags {
			if f == "write" || f == "admin" {
				t.Errorf("%s is on the read list but COMMAND INFO says %s (%v)", name, f, flags)
			}
		}
	}
	all := cloneOpts(opts)
	all["max_rows"] = "1000000"
	res, rep := liveRun(t, url, "COMMAND", all, 10*time.Second)
	if !res.Success {
		t.Fatal(res.Err)
	}
	list, _ := rep.Value.([]any)
	for _, item := range list {
		spec, _ := item.([]any)
		if len(spec) == 0 {
			continue
		}
		name := strings.ToLower(fmt.Sprint(spec[0]))
		flags := commandFlags(spec)
		write := false
		for _, f := range flags {
			if f == "write" {
				write = true
			}
		}
		if !write {
			continue
		}
		if classify(name, "") == classRead && !isContainer(name) {
			t.Errorf("%s has the write flag and is classified as a read", name)
		}
	}
}

func commandFlags(v any) []string {
	var out []string
	switch x := v.(type) {
	case []any:
		if len(x) == 1 {
			if inner, ok := x[0].([]any); ok {
				return commandFlags(inner)
			}
		}
		for _, el := range x {
			switch e := el.(type) {
			case []any:
				texts := true
				var flags []string
				for _, f := range e {
					s, ok := f.(string)
					if !ok {
						texts = false
						break
					}
					flags = append(flags, strings.ToLower(s))
				}
				if texts && len(flags) > 0 && looksLikeFlags(flags) {
					out = append(out, flags...)
				}
			}
		}
	}
	return out
}

func looksLikeFlags(flags []string) bool {
	for _, f := range flags {
		switch f {
		case "write", "readonly", "admin", "fast", "denyoom", "noscript", "loading", "stale", "skip_monitor":
			return true
		}
	}
	return false
}

func liveDel(url string, opts map[string]string, key string) {
	o := cloneOpts(opts)
	o["allow_writes"] = "true"
	d, err := New(protocol.Target{URL: url, Body: []byte("DEL " + key), Options: o}, 5*time.Second)
	if err != nil {
		return
	}
	d.Run(context.Background())
	d.Close()
}

func cloneOpts(in map[string]string) map[string]string {
	out := make(map[string]string, len(in)+2)
	for k, v := range in {
		out[k] = v
	}
	return out
}

func withArgs(opts map[string]string, args string) map[string]string {
	o := cloneOpts(opts)
	o["args"] = args
	return o
}

func withPool(opts map[string]string, pool string) map[string]string {
	o := cloneOpts(opts)
	o["pool"] = pool
	return o
}

func mustJSON(values ...string) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, v := range values {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconvQuote(v))
	}
	b.WriteByte(']')
	return b.String()
}

func strconvQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\', '"':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}
