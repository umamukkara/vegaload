package kafka

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/protocol"
)

func target(url, body string, opts map[string]string) protocol.Target {
	return protocol.Target{URL: url, Body: []byte(body), Options: opts}
}

func newDriver(t *testing.T, tg protocol.Target, timeout time.Duration) *Driver {
	t.Helper()
	d, err := New(tg, timeout)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func do(t *testing.T, tg protocol.Target, timeout time.Duration) protocol.Result {
	t.Helper()
	d := newDriver(t, tg, timeout)
	res, err := d.Do(context.Background())
	if err != nil {
		t.Fatalf("Do returned a run-stopping error: %v", err)
	}
	return res
}

func TestProduce(t *testing.T) {
	b := startBroker(t)
	b.AddTopic("orders", 3)
	res := do(t, target(b.URL(), "hello", map[string]string{"topic": "orders", "key": "k1", "count": "4"}), 5*time.Second)
	if !res.Success {
		t.Fatalf("produce failed: %v", res.Err)
	}
	if res.BytesSent != 4*(5+2) {
		t.Errorf("BytesSent = %d, want %d", res.BytesSent, 4*7)
	}
	if got := b.ProducedCount(); got != 4 {
		t.Errorf("broker stored %d records, want 4", got)
	}
}

func TestProduce_AllAcksAndCompression(t *testing.T) {
	b := startBroker(t)
	b.AddTopic("t", 1)
	n := 0
	for _, acks := range []string{"all", "leader", "none"} {
		for _, comp := range []string{"none", "gzip", "snappy", "lz4", "zstd"} {
			res := do(t, target(b.URL(), "payload payload payload", map[string]string{"topic": "t", "acks": acks, "compression": comp}), 5*time.Second)
			if !res.Success {
				t.Fatalf("acks=%s compression=%s: %v", acks, comp, res.Err)
			}
			n++
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for b.ProducedCount() < n && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := b.ProducedCount(); got != n {
		t.Errorf("broker stored %d records, want %d", got, n)
	}
}

func TestProduce_IDPlaceholderGivesEachCallItsOwnTopic(t *testing.T) {
	b := startBroker(t)
	d := newDriver(t, target(b.URL(), "x {id}", map[string]string{"topic": "t-{id}"}), 5*time.Second)
	// The topics do not exist yet, so the first call must fail clearly.
	res, _ := d.Do(context.Background())
	if res.Success || res.Err == nil || !strings.Contains(res.Err.Error(), "producing to t-vegaload-") {
		t.Fatalf("want a failure that names the topic, got %+v", res)
	}
}

func TestProduce_UnknownTopicFailsFast(t *testing.T) {
	b := startBroker(t)
	start := time.Now()
	res := do(t, target(b.URL(), "x", map[string]string{"topic": "nope"}), 5*time.Second)
	if res.Success || res.Err == nil {
		t.Fatalf("an unknown topic must fail: %+v", res)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("took %v: an unknown topic should not wait for the whole timeout", time.Since(start))
	}
}

func TestConsume(t *testing.T) {
	b := startBroker(t)
	b.AddTopic("logs", 1)
	if res := do(t, target(b.URL(), "line ok", map[string]string{"topic": "logs", "count": "3"}), 5*time.Second); !res.Success {
		t.Fatal(res.Err)
	}
	res := do(t, target(b.URL(), "", map[string]string{"mode": "consume", "topic": "logs", "count": "3", "expect": "ok"}), 5*time.Second)
	if !res.Success {
		t.Fatalf("consume failed: %v", res.Err)
	}
	if res.BytesReceived != 3*7 {
		t.Errorf("BytesReceived = %d, want 21", res.BytesReceived)
	}
}

func TestConsume_WrongTextFails(t *testing.T) {
	b := startBroker(t)
	b.AddTopic("logs", 1)
	do(t, target(b.URL(), "hello", map[string]string{"topic": "logs"}), 5*time.Second)
	res := do(t, target(b.URL(), "", map[string]string{"mode": "consume", "topic": "logs", "expect": "bye"}), 5*time.Second)
	if res.Success || res.Err == nil || !strings.Contains(res.Err.Error(), "did not contain") {
		t.Fatalf("want a failure about the text, got %+v", res)
	}
}

func TestConsume_NotEnoughRecordsTimesOut(t *testing.T) {
	b := startBroker(t)
	b.AddTopic("logs", 1)
	do(t, target(b.URL(), "one", map[string]string{"topic": "logs"}), 5*time.Second)
	start := time.Now()
	res := do(t, target(b.URL(), "", map[string]string{"mode": "consume", "topic": "logs", "count": "2"}), 700*time.Millisecond)
	if res.Success || res.Err == nil || !strings.Contains(res.Err.Error(), "within -timeout") {
		t.Fatalf("want a timeout, got %+v", res)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("took %v: the 700ms timeout was not applied", time.Since(start))
	}
}

func TestConsume_FromEndSeesNothingOld(t *testing.T) {
	b := startBroker(t)
	b.AddTopic("logs", 1)
	do(t, target(b.URL(), "old", map[string]string{"topic": "logs"}), 5*time.Second)
	res := do(t, target(b.URL(), "", map[string]string{"mode": "consume", "topic": "logs", "from": "end"}), 600*time.Millisecond)
	if res.Success {
		t.Fatal("from=end must not return records that were there before")
	}
}

func TestConsume_UnknownTopicFails(t *testing.T) {
	b := startBroker(t)
	res := do(t, target(b.URL(), "", map[string]string{"mode": "consume", "topic": "nope"}), 700*time.Millisecond)
	if res.Success {
		t.Fatal("an unknown topic must fail")
	}
}

func TestRoundtrip(t *testing.T) {
	b := startBroker(t)
	b.AddTopic("rt", 4)
	res := do(t, target(b.URL(), "ping {id}", map[string]string{"mode": "roundtrip", "topic": "rt", "count": "3"}), 5*time.Second)
	if !res.Success {
		t.Fatalf("roundtrip failed: %v", res.Err)
	}
	if res.BytesSent == 0 || res.BytesReceived == 0 {
		t.Errorf("sent %d, received %d: both should count the records", res.BytesSent, res.BytesReceived)
	}
}

func TestRoundtrip_ManyAtOnceOnOneTopic(t *testing.T) {
	// Each user reads back exactly its own records, even when others write
	// to the same partitions at the same time.
	b := startBroker(t)
	b.AddTopic("shared", 1)
	d := newDriver(t, target(b.URL(), "msg {id}", map[string]string{"mode": "roundtrip", "topic": "shared"}), 10*time.Second)
	const users = 8
	results := make(chan protocol.Result, users)
	for i := 0; i < users; i++ {
		go func() {
			res, _ := d.Do(context.Background())
			results <- res
		}()
	}
	for i := 0; i < users; i++ {
		if res := <-results; !res.Success {
			t.Errorf("user %d: %v", i, res.Err)
		}
	}
}

func TestAdmin_TopicLifecycle(t *testing.T) {
	b := startBroker(t)
	d := newDriver(t, target(b.URL(), "", map[string]string{"mode": "admin", "action": "topic_lifecycle", "topic": "tmp-{id}", "partitions": "3"}), 5*time.Second)
	for i := 0; i < 3; i++ {
		if res, _ := d.Do(context.Background()); !res.Success {
			t.Fatalf("lifecycle %d: %v", i, res.Err)
		}
	}
	if names := b.TopicNames(); len(names) != 0 {
		t.Errorf("topics left behind: %v", names)
	}
}

func TestAdmin_CreateListDelete(t *testing.T) {
	b := startBroker(t)
	admin := func(opts map[string]string) protocol.Result {
		opts["mode"] = "admin"
		return do(t, target(b.URL(), "", opts), 5*time.Second)
	}
	if res := admin(map[string]string{"action": "create_topic", "topic": "alpha", "partitions": "2"}); !res.Success {
		t.Fatalf("create: %v", res.Err)
	}
	if !b.HasTopic("alpha") {
		t.Fatal("the topic was not created")
	}
	if res := admin(map[string]string{"action": "create_topic", "topic": "alpha"}); res.Success || !strings.Contains(res.Err.Error(), "already exists") {
		t.Fatalf("creating twice must fail with the broker's reason, got %+v", res)
	}
	if res := admin(map[string]string{"action": "list_topics", "expect": "alpha"}); !res.Success {
		t.Fatalf("list: %v", res.Err)
	}
	if res := admin(map[string]string{"action": "list_topics", "expect": "beta"}); res.Success {
		t.Fatal("a listing without the expected text must fail")
	}
	if res := admin(map[string]string{"action": "describe_cluster", "expect": "1 brokers"}); !res.Success {
		t.Fatalf("describe: %v", res.Err)
	}
	b.Groups = []string{"billing"}
	if res := admin(map[string]string{"action": "list_groups", "expect": "billing"}); !res.Success {
		t.Fatalf("groups: %v", res.Err)
	}
	if res := admin(map[string]string{"action": "delete_topic", "topic": "alpha"}); !res.Success {
		t.Fatalf("delete: %v", res.Err)
	}
	if b.HasTopic("alpha") {
		t.Fatal("the topic was not deleted")
	}
	if res := admin(map[string]string{"action": "delete_topic", "topic": "alpha"}); res.Success {
		t.Fatal("deleting a missing topic must fail")
	}
}

func TestSASLPlain(t *testing.T) {
	b := startBroker(t)
	b.User, b.Pass = "alice", "s3cret"
	b.AddTopic("t", 1)
	t.Setenv("KAFKA_TEST_PW", "s3cret")
	t.Setenv("KAFKA_TEST_BAD", "wrong")
	opts := func(env string) map[string]string {
		return map[string]string{"topic": "t", "sasl": "plain", "username": "alice", "password_env": env}
	}
	if res := do(t, target(b.URL(), "x", opts("KAFKA_TEST_PW")), 5*time.Second); !res.Success {
		t.Fatalf("right login failed: %v", res.Err)
	}
	if res := do(t, target(b.URL(), "x", opts("KAFKA_TEST_BAD")), 1500*time.Millisecond); res.Success {
		t.Fatal("wrong password must fail")
	}
	if res := do(t, target(b.URL(), "x", map[string]string{"topic": "t"}), 2*time.Second); res.Success {
		t.Fatal("no login must fail when the broker needs one")
	}
}

func TestKafkaS(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.NotFoundHandler())
	srv.StartTLS()
	defer srv.Close()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", srv.TLS)
	if err != nil {
		t.Fatal(err)
	}
	b := newFakeBroker(t, ln)
	b.AddTopic("t", 1)
	url := "kafkas://" + ln.Addr().String()

	tg := target(url, "secure", map[string]string{"topic": "t"})
	tg.InsecureSkipVerify = true
	if res := do(t, tg, 5*time.Second); !res.Success {
		t.Fatalf("kafkas with -insecure failed: %v", res.Err)
	}
	tg.InsecureSkipVerify = false
	if res := do(t, tg, 2*time.Second); res.Success {
		t.Fatal("an untrusted certificate must fail without -insecure")
	}
}

func TestConnectRefused(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()
	res := do(t, target("kafka://"+addr, "x", map[string]string{"topic": "t"}), time.Second)
	if res.Success || res.Err == nil {
		t.Fatalf("a closed port must fail: %+v", res)
	}
}

func TestRunEndCutsCallShort(t *testing.T) {
	for _, mode := range []string{"produce", "consume", "roundtrip", "admin"} {
		t.Run(mode, func(t *testing.T) {
			// A broker that accepts the connection and never answers.
			l, _ := net.Listen("tcp", "127.0.0.1:0")
			defer l.Close()
			closed := make(chan struct{})
			go func() {
				c, err := l.Accept()
				if err != nil {
					return
				}
				defer c.Close()
				_, _ = io.Copy(io.Discard, c) // returns when the driver closes
				close(closed)
			}()
			opts := map[string]string{"topic": "t", "mode": mode}
			if mode == "admin" {
				opts["action"] = "list_topics"
			}
			body := "x {id}"
			if mode == "consume" || mode == "admin" {
				body = ""
			}
			d, err := New(target("kafka://"+l.Addr().String(), body, opts), 10*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			time.AfterFunc(200*time.Millisecond, cancel)
			start := time.Now()
			res, _ := d.Do(ctx)
			if res.Success || time.Since(start) > 3*time.Second {
				t.Errorf("cancel should end the call quickly: success=%v after %v", res.Success, time.Since(start))
			}
			// The shared client lives until Close. After that, no
			// connection may be left open.
			d.Close()
			select {
			case <-closed:
			case <-time.After(2 * time.Second):
				t.Error("a connection was left open")
			}
		})
	}
}

func TestNew_ConfigErrors(t *testing.T) {
	o := func(kv ...string) map[string]string {
		m := map[string]string{}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return m
	}
	cases := []struct {
		name string
		tg   protocol.Target
		want string
	}{
		{"wrong scheme", target("http://h:1", "", o("topic", "t")), "scheme"},
		{"no host", target("kafka://", "", o("topic", "t")), "no host"},
		{"no topic", target("kafka://h", "", nil), "topic is required"},
		{"unknown option", target("kafka://h", "", o("topic", "t", "tpoic", "x")), "unknown option tpoic"},
		{"bad mode", target("kafka://h", "", o("topic", "t", "mode", "fly")), "mode"},
		{"bad acks", target("kafka://h", "", o("topic", "t", "acks", "some")), "acks"},
		{"bad compression", target("kafka://h", "", o("topic", "t", "compression", "rar")), "compression"},
		{"bad count", target("kafka://h", "", o("topic", "t", "count", "0")), "count"},
		{"count text", target("kafka://h", "", o("topic", "t", "count", "many")), "whole number"},
		{"bad from", target("kafka://h", "", o("topic", "t", "mode", "consume", "from", "middle")), "from"},
		{"from in produce", target("kafka://h", "", o("topic", "t", "from", "end")), "only used in consume"},
		{"key in consume", target("kafka://h", "", o("topic", "t", "mode", "consume", "key", "k")), "only used in produce"},
		{"action in produce", target("kafka://h", "", o("topic", "t", "action", "list_topics")), "only used in admin"},
		{"roundtrip acks none", target("kafka://h", "", o("topic", "t", "mode", "roundtrip", "acks", "none")), "acks=none"},
		{"expect when producing", target("kafka://h", "", o("topic", "t", "expect", "x")), "consume mode"},
		{"id in consume topic", target("kafka://h", "", o("topic", "t-{id}", "mode", "consume")), "{id}"},
		{"consume with body", target("kafka://h", "body", o("topic", "t", "mode", "consume")), "body"},
		{"admin without action", target("kafka://h", "", o("mode", "admin")), "needs -opt action"},
		{"admin bad action", target("kafka://h", "", o("mode", "admin", "action", "explode")), "action"},
		{"admin create needs topic", target("kafka://h", "", o("mode", "admin", "action", "create_topic")), "needs -opt topic"},
		{"admin with count", target("kafka://h", "", o("mode", "admin", "action", "list_topics", "count", "2")), "count"},
		{"expect on create", target("kafka://h", "", o("mode", "admin", "action", "create_topic", "topic", "t", "expect", "x")), "expect"},
		{"bad partitions", target("kafka://h", "", o("mode", "admin", "action", "create_topic", "topic", "t", "partitions", "0")), "partitions"},
		{"username without sasl", target("kafka://h", "", o("topic", "t", "username", "u")), "need -opt sasl"},
		{"sasl without user", target("kafka://h", "", o("topic", "t", "sasl", "plain")), "username"},
		{"sasl without password", target("kafka://h", "", o("topic", "t", "sasl", "plain", "username", "u")), "password_env"},
		{"unset password env", target("kafka://h", "", o("topic", "t", "sasl", "plain", "username", "u", "password_env", "VEGALOAD_SURELY_UNSET_VAR")), "not set"},
		{"bad sasl", target("kafka://h", "", o("topic", "t", "sasl", "magic", "username", "u", "password_env", "PATH")), "sasl"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := New(c.tg, time.Second)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v, want it to contain %q", err, c.want)
			}
		})
	}
}

func TestNew_ScramMechanismsAccepted(t *testing.T) {
	t.Setenv("KAFKA_TEST_PW", "pw")
	for _, m := range []string{"plain", "scram-sha-256", "scram-sha-512"} {
		d, err := New(target("kafka://h", "x", map[string]string{"topic": "t", "sasl": m, "username": "u", "password_env": "KAFKA_TEST_PW"}), time.Second)
		if err != nil {
			t.Errorf("sasl=%s: %v", m, err)
			continue
		}
		d.Close()
	}
}

func TestNew_DefaultPorts(t *testing.T) {
	// The seed address is the first entry in the client's options; the
	// simplest check is to see that both schemes are accepted.
	for _, u := range []string{"kafka://broker.local", "kafkas://broker.local", "kafka://broker.local:19092"} {
		d, err := New(target(u, "", map[string]string{"topic": "t"}), time.Second)
		if err != nil {
			t.Errorf("%s: %v", u, err)
			continue
		}
		d.Close()
	}
}

func TestNameAndClose(t *testing.T) {
	d, _ := New(target("kafka://h", "", map[string]string{"topic": "t"}), time.Second)
	if d.Name() != "kafka" || d.Close() != nil {
		t.Error("Name should be kafka, and Close should not fail")
	}
}

func TestErrorsNameTheRealReason(t *testing.T) {
	// A refused connection and a wrong password both look like "timed out"
	// to the client. The driver must say what really happened.
	b := startBroker(t)
	b.User, b.Pass = "alice", "s3cret"
	b.AddTopic("t", 1)
	t.Setenv("KAFKA_TEST_BAD", "wrong")
	bad := do(t, target(b.URL(), "x", map[string]string{"topic": "t", "sasl": "plain", "username": "alice", "password_env": "KAFKA_TEST_BAD"}), 1500*time.Millisecond)
	if bad.Success || bad.Err == nil || !strings.Contains(bad.Err.Error(), "wrong user name or password") {
		t.Errorf("a wrong password should be named, got %+v", bad)
	}
	refused := do(t, target("kafka://127.0.0.1:1", "x", map[string]string{"topic": "t"}), 1500*time.Millisecond)
	if refused.Success || refused.Err == nil || !strings.Contains(refused.Err.Error(), "unable to open connection to broker") {
		t.Errorf("a failed connection should be named, got %+v", refused)
	}
}
