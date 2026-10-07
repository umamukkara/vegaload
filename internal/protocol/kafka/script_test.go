package kafka

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/protocol"
)

func conn(t *testing.T, url string, opts map[string]string, pw *string) *Driver {
	t.Helper()
	d, err := NewConn(protocol.Target{URL: url, Options: opts}, 5*time.Second, pw)
	if err != nil {
		t.Fatalf("NewConn: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func call(t *testing.T, c *Driver, body string, opts map[string]string) (protocol.Result, Reply) {
	t.Helper()
	d, err := c.Call(opts, []byte(body), 5*time.Second)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	return d.Run(context.Background())
}

func TestScript_OneConnectionManyJobs(t *testing.T) {
	b := startBroker(t)
	b.AddTopic("orders", 2)
	c := conn(t, b.URL(), nil, nil)

	res, rep := call(t, c, "hello", map[string]string{"topic": "orders", "key": "k1", "count": "2"})
	if !res.Success {
		t.Fatalf("produce: %v", res.Err)
	}
	if len(rep.Records) != 2 || string(rep.Records[0].Value) != "hello" || string(rep.Records[0].Key) != "k1" || rep.Records[0].Topic != "orders" {
		t.Fatalf("produce records: %+v", rep.Records)
	}

	res, rep = call(t, c, "", map[string]string{"mode": "consume", "topic": "orders", "count": "2"})
	if !res.Success {
		t.Fatalf("consume: %v", res.Err)
	}
	if len(rep.Records) != 2 || string(rep.Records[1].Value) != "hello" {
		t.Fatalf("consume records: %+v", rep.Records)
	}

	res, rep = call(t, c, "ping {id}", map[string]string{"mode": "roundtrip", "topic": "orders"})
	if !res.Success || len(rep.Records) != 1 || !strings.HasPrefix(string(rep.Records[0].Value), "ping vegaload-") {
		t.Fatalf("roundtrip: %v %+v", res.Err, rep.Records)
	}

	res, rep = call(t, c, "", map[string]string{"mode": "admin", "action": "list_topics"})
	if !res.Success || !strings.Contains(rep.Text, "orders") {
		t.Fatalf("admin: %v %q", res.Err, rep.Text)
	}
}

func TestScript_ProduceReusesTheConnection(t *testing.T) {
	b := startBroker(t)
	b.AddTopic("t", 1)
	c := conn(t, b.URL(), nil, nil)
	for i := 0; i < 6; i++ {
		if res, _ := call(t, c, "x", map[string]string{"topic": "t"}); !res.Success {
			t.Fatal(res.Err)
		}
	}
	if n := b.ConnCount(); n > 3 {
		t.Errorf("%d connections for 6 produce calls: the client should be reused", n)
	}
}

func TestScript_FailedCheckReturnsNoRecords(t *testing.T) {
	b := startBroker(t)
	b.AddTopic("t", 1)
	c := conn(t, b.URL(), nil, nil)
	if res, _ := call(t, c, "abc", map[string]string{"topic": "t"}); !res.Success {
		t.Fatal(res.Err)
	}
	res, rep := call(t, c, "", map[string]string{"mode": "consume", "topic": "t", "expect": "zzz"})
	if res.Success {
		t.Fatal("wrong text must fail")
	}
	if len(rep.Records) != 0 {
		t.Fatalf("the record that failed the check is not counted: %+v", rep.Records)
	}
}

func TestScript_ConfigMistakesAreErrors(t *testing.T) {
	b := startBroker(t)
	c := conn(t, b.URL(), nil, nil)
	cases := map[string]map[string]string{
		"no topic":          {},
		"unknown option":    {"topic": "t", "bogus": "1"},
		"bad mode":          {"topic": "t", "mode": "nope"},
		"admin no action":   {"mode": "admin"},
		"roundtrip no ack":  {"mode": "roundtrip", "topic": "t", "acks": "none"},
		"from with produce": {"topic": "t", "from": "end"},
	}
	for name, opts := range cases {
		if _, err := c.Call(opts, nil, time.Second); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
	if _, err := NewConn(protocol.Target{URL: b.URL(), Options: map[string]string{"topic": "t"}}, time.Second, nil); err == nil {
		t.Error("topic is not a connection option")
	}
	if _, err := NewConn(protocol.Target{URL: b.URL(), Options: map[string]string{"username": "u"}}, time.Second, nil); err == nil {
		t.Error("username without sasl must fail")
	}
	pw := "p"
	if _, err := NewConn(protocol.Target{URL: b.URL(), Options: map[string]string{"sasl": "plain", "username": "u", "password_env": "X"}}, time.Second, &pw); err == nil {
		t.Error("password and password_env together must fail")
	}
	if _, err := NewConn(protocol.Target{URL: b.URL(), Options: map[string]string{"sasl": "plain", "username": "u"}}, time.Second, nil); err == nil {
		t.Error("sasl without any password must fail")
	}
}

func TestScript_PasswordGivenDirectly(t *testing.T) {
	b := startBroker(t)
	b.User, b.Pass = "alice", "s3cret"
	b.AddTopic("t", 1)
	opts := map[string]string{"sasl": "plain", "username": "alice"}
	good, bad := "s3cret", "wrong"
	c := conn(t, b.URL(), opts, &good)
	if res, _ := call(t, c, "x", map[string]string{"topic": "t", "sasl": "plain", "username": "alice"}); !res.Success {
		t.Fatalf("right password failed: %v", res.Err)
	}
	d, err := NewConn(protocol.Target{URL: b.URL(), Options: opts}, time.Second, &bad)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	cd, err := d.Call(map[string]string{"topic": "t", "sasl": "plain", "username": "alice"}, []byte("x"), 1500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if res, _ := cd.Run(context.Background()); res.Success {
		t.Fatal("wrong password must fail")
	}
}
