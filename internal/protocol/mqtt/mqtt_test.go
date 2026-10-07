package mqtt

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

func target(url string, body string, opts map[string]string) protocol.Target {
	return protocol.Target{URL: url, Body: []byte(body), Options: opts}
}

func do(t *testing.T, tg protocol.Target, timeout time.Duration) protocol.Result {
	t.Helper()
	d, err := New(tg, timeout)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	res, err := d.Do(context.Background())
	if err != nil {
		t.Fatalf("Do returned a run-stopping error: %v", err)
	}
	return res
}

func TestPublish_AllQoSLevels(t *testing.T) {
	b := startBroker(t)
	for _, qos := range []string{"0", "1", "2"} {
		res := do(t, target(b.url(), "hello", map[string]string{"topic": "load/t" + qos, "qos": qos}), 3*time.Second)
		if !res.Success {
			t.Fatalf("qos %s: %v", qos, res.Err)
		}
		if res.BytesSent != 5 {
			t.Errorf("qos %s: BytesSent = %d, want 5", qos, res.BytesSent)
		}
	}
	// Paho sends QoS 0 as is. The broker must have seen all three.
	deadline := time.Now().Add(2 * time.Second)
	for len(b.pubs()) < 3 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	got := b.pubs()
	if len(got) != 3 {
		t.Fatalf("broker saw %d messages, want 3", len(got))
	}
	for i, p := range got {
		wantTopic := "load/t" + string(rune('0'+i))
		if p.topic != wantTopic || string(p.payload) != "hello" || int(p.qos) != i {
			t.Errorf("message %d = %+v", i, p)
		}
	}
}

func TestPublish_RetainFlagReachesBroker(t *testing.T) {
	b := startBroker(t)
	res := do(t, target(b.url(), "x", map[string]string{"topic": "r", "qos": "1", "retain": "true"}), 3*time.Second)
	if !res.Success {
		t.Fatal(res.Err)
	}
	if p := b.pubs(); len(p) != 1 || !p[0].retain {
		t.Errorf("published = %+v, want one retained message", p)
	}
}

func TestEachIterationUsesANewClientID(t *testing.T) {
	b := startBroker(t)
	d, err := New(target(b.url(), "x", map[string]string{"topic": "t", "client_id": "load"}), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if res, _ := d.Do(context.Background()); !res.Success {
			t.Fatal(res.Err)
		}
	}
	seen := map[string]bool{}
	for _, id := range b.ids() {
		if !strings.HasPrefix(id, "load-") {
			t.Errorf("client id %q does not use the prefix", id)
		}
		if seen[id] {
			t.Errorf("client id %q was used twice: a broker would close the older connection", id)
		}
		seen[id] = true
	}
	if len(seen) != 5 {
		t.Errorf("saw %d ids, want 5", len(seen))
	}
}

func TestIDPlaceholderInTopicAndPayload(t *testing.T) {
	b := startBroker(t)
	res := do(t, target(b.url(), "from {id}", map[string]string{"topic": "u/{id}/in"}), 3*time.Second)
	if !res.Success {
		t.Fatal(res.Err)
	}
	id := b.ids()[0]
	p := b.pubs()
	deadline := time.Now().Add(2 * time.Second)
	for len(p) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		p = b.pubs()
	}
	if len(p) != 1 || p[0].topic != "u/"+id+"/in" || string(p[0].payload) != "from "+id {
		t.Errorf("published = %+v, want {id} replaced by %q", p, id)
	}
}

func TestLogin(t *testing.T) {
	b := startBroker(t)
	b.user, b.pass = "alice", "s3cret"
	t.Setenv("MQTT_TEST_PW", "s3cret")

	ok := do(t, target(b.url(), "x", map[string]string{"topic": "t", "username": "alice", "password_env": "MQTT_TEST_PW"}), 3*time.Second)
	if !ok.Success {
		t.Fatalf("right login failed: %v", ok.Err)
	}

	t.Setenv("MQTT_TEST_BAD", "wrong")
	bad := do(t, target(b.url(), "x", map[string]string{"topic": "t", "username": "alice", "password_env": "MQTT_TEST_BAD"}), 3*time.Second)
	if bad.Success || bad.Err == nil {
		t.Fatalf("wrong password must fail: %+v", bad)
	}

	none := do(t, target(b.url(), "x", map[string]string{"topic": "t"}), 3*time.Second)
	if none.Success {
		t.Fatal("no login must fail when the broker needs one")
	}
}

func TestSubscribe_RetainedMessage(t *testing.T) {
	b := startBroker(t)
	b.setRetained("sensors/temp", []byte("21.5C"))
	res := do(t, target(b.url(), "", map[string]string{"mode": "subscribe", "topic": "sensors/+", "qos": "1", "expect": "21.5"}), 3*time.Second)
	if !res.Success {
		t.Fatalf("subscribe failed: %v", res.Err)
	}
	if res.BytesReceived != 5 {
		t.Errorf("BytesReceived = %d, want 5", res.BytesReceived)
	}
}

func TestSubscribe_WrongTextFails(t *testing.T) {
	b := startBroker(t)
	b.setRetained("a", []byte("hello"))
	res := do(t, target(b.url(), "", map[string]string{"mode": "subscribe", "topic": "a", "expect": "bye"}), 3*time.Second)
	if res.Success || !strings.Contains(res.Err.Error(), "did not contain") {
		t.Fatalf("want a failure about the text, got %+v", res)
	}
}

func TestSubscribe_NoMessageTimesOut(t *testing.T) {
	b := startBroker(t)
	start := time.Now()
	res := do(t, target(b.url(), "", map[string]string{"mode": "subscribe", "topic": "quiet"}), 300*time.Millisecond)
	if res.Success {
		t.Fatal("no message must fail")
	}
	if time.Since(start) > 2*time.Second {
		t.Errorf("took %v: the 300ms timeout was not applied", time.Since(start))
	}
}

func TestSubscribe_Count(t *testing.T) {
	b := startBroker(t)
	b.setRetained("a/1", []byte("one"))
	b.setRetained("a/2", []byte("two"))
	res := do(t, target(b.url(), "", map[string]string{"mode": "subscribe", "topic": "a/#", "count": "2"}), 3*time.Second)
	if !res.Success || res.BytesReceived != 6 {
		t.Fatalf("count=2: %+v", res)
	}
	res = do(t, target(b.url(), "", map[string]string{"mode": "subscribe", "topic": "a/#", "count": "3"}), 300*time.Millisecond)
	if res.Success {
		t.Fatal("count=3 with two messages must fail")
	}
}

func TestRoundtrip(t *testing.T) {
	b := startBroker(t)
	res := do(t, target(b.url(), "ping {id}", map[string]string{"mode": "roundtrip", "topic": "rt/{id}", "qos": "1"}), 3*time.Second)
	if !res.Success {
		t.Fatalf("roundtrip failed: %v", res.Err)
	}
	if res.BytesSent == 0 || res.BytesSent != res.BytesReceived {
		t.Errorf("sent %d, received %d: the message should come back whole", res.BytesSent, res.BytesReceived)
	}
}

func TestRoundtrip_ManyAtOnceOnOneTopic(t *testing.T) {
	// Every user publishes to and listens on the same topic, so each one
	// also sees the others' messages. Each must still find its own.
	b := startBroker(t)
	d, err := New(target(b.url(), "msg {id}", map[string]string{"mode": "roundtrip", "topic": "shared"}), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
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

func TestRoundtrip_OtherUsersMessageDoesNotCount(t *testing.T) {
	// The broker hands back a message that belongs to someone else.
	b := startBroker(t)
	b.decoy = []byte("msg some-other-client-id")
	res := do(t, target(b.url(), "msg {id}", map[string]string{"mode": "roundtrip", "topic": "shared"}), 400*time.Millisecond)
	if res.Success {
		t.Fatal("another user's message must not count as our own")
	}
}

func TestRoundtrip_LongerIDDoesNotMatch(t *testing.T) {
	// Ids are counted, so x-1 is the start of x-10. A message that only
	// starts with our id is someone else's and must not count.
	b := startBroker(t)
	b.suffix = []byte("0")
	res := do(t, target(b.url(), "msg {id}", map[string]string{"mode": "roundtrip", "topic": "shared"}), 400*time.Millisecond)
	if res.Success {
		t.Fatal("a message that only starts with our id must not count")
	}
}

func TestConnectRefused(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()
	res := do(t, target("mqtt://"+addr, "x", map[string]string{"topic": "t"}), time.Second)
	if res.Success || res.Err == nil {
		t.Fatalf("a closed port must fail: %+v", res)
	}
}

func TestRunEndCutsCallShort(t *testing.T) {
	// A broker that accepts the connection and never answers. The test
	// also checks that the driver closes its side of the connection.
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
	d, _ := New(target("mqtt://"+l.Addr().String(), "x", map[string]string{"topic": "t"}), 10*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(150*time.Millisecond, cancel)
	start := time.Now()
	res, _ := d.Do(ctx)
	if res.Success || time.Since(start) > 2*time.Second {
		t.Errorf("cancel should end the call quickly: success=%v after %v", res.Success, time.Since(start))
	}
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Error("the connection was left open after the run ended")
	}
}

func TestMQTTS(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.NotFoundHandler())
	srv.StartTLS()
	defer srv.Close()
	ln, err := tls.Listen("tcp", "127.0.0.1:0", srv.TLS)
	if err != nil {
		t.Fatal(err)
	}
	b := newFakeBroker(t, ln)
	url := "mqtts://" + b.ln.Addr().String()

	tg := target(url, "secure", map[string]string{"topic": "t", "qos": "1"})
	tg.InsecureSkipVerify = true
	if res := do(t, tg, 3*time.Second); !res.Success {
		t.Fatalf("mqtts with -insecure failed: %v", res.Err)
	}

	tg.InsecureSkipVerify = false
	if res := do(t, tg, 3*time.Second); res.Success {
		t.Fatal("an untrusted certificate must fail without -insecure")
	}
}

func TestNew_ConfigErrors(t *testing.T) {
	cases := []struct {
		name string
		tg   protocol.Target
		want string
	}{
		{"wrong scheme", target("http://h:1", "", map[string]string{"topic": "t"}), "scheme"},
		{"no host", target("mqtt://", "", map[string]string{"topic": "t"}), "no host"},
		{"no topic", target("mqtt://h", "", nil), "topic is required"},
		{"unknown option", target("mqtt://h", "", map[string]string{"topic": "t", "tpoic": "x"}), "unknown option tpoic"},
		{"bad mode", target("mqtt://h", "", map[string]string{"topic": "t", "mode": "fly"}), "mode"},
		{"bad qos", target("mqtt://h", "", map[string]string{"topic": "t", "qos": "3"}), "qos"},
		{"qos text", target("mqtt://h", "", map[string]string{"topic": "t", "qos": "high"}), "whole number"},
		{"bad retain", target("mqtt://h", "", map[string]string{"topic": "t", "retain": "maybe"}), "true or false"},
		{"bad count", target("mqtt://h", "", map[string]string{"topic": "t", "count": "0"}), "count"},
		{"bad keepalive", target("mqtt://h", "", map[string]string{"topic": "t", "keepalive": "0s"}), "keepalive"},
		{"wildcard publish", target("mqtt://h", "", map[string]string{"topic": "a/#"}), "wildcard"},
		{"wildcard roundtrip", target("mqtt://h", "", map[string]string{"topic": "a/+", "mode": "roundtrip"}), "wildcard"},
		{"unset password env", target("mqtt://h", "", map[string]string{"topic": "t", "username": "u", "password_env": "VEGALOAD_SURELY_UNSET_VAR"}), "not set"},
		{"password without user", target("mqtt://h", "", map[string]string{"topic": "t", "password_env": "PATH"}), "username"},
		{"roundtrip without id", target("mqtt://h", "same body", map[string]string{"topic": "t", "mode": "roundtrip"}), "{id}"},
		{"clean is gone", target("mqtt://h", "", map[string]string{"topic": "t", "clean": "false"}), "unknown option clean"},
		{"expect when publishing", target("mqtt://h", "", map[string]string{"topic": "t", "expect": "x"}), "subscribe mode"},
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

func TestNew_DefaultPorts(t *testing.T) {
	d, err := New(target("mqtt://broker.local", "", map[string]string{"topic": "t"}), time.Second)
	if err != nil || d.broker != "tcp://broker.local:1883" {
		t.Errorf("mqtt default: %v, %v", d, err)
	}
	d, err = New(target("mqtts://broker.local", "", map[string]string{"topic": "t"}), time.Second)
	if err != nil || d.broker != "ssl://broker.local:8883" {
		t.Errorf("mqtts default: %v, %v", d, err)
	}
}

func TestSubscribeAllowsWildcards(t *testing.T) {
	if _, err := New(target("mqtt://h", "", map[string]string{"topic": "a/#", "mode": "subscribe"}), time.Second); err != nil {
		t.Errorf("wildcard in subscribe mode: %v", err)
	}
}

func TestNameAndClose(t *testing.T) {
	d, _ := New(target("mqtt://h", "", map[string]string{"topic": "t"}), time.Second)
	if d.Name() != "mqtt" || d.Close() != nil {
		t.Error("Name should be mqtt, and Close should do nothing")
	}
}
