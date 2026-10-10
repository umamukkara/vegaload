package rabbitmqtest

import (
	"bytes"
	"context"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

func TestMatchTopic(t *testing.T) {
	cases := []struct {
		pat, key string
		ok       bool
	}{
		{"a.#", "a", true},
		{"a.#", "a.b", true},
		{"a.#", "a.b.c", true},
		{"#.b", "b", true},
		{"#.b", "a.b", true},
		{"#.b", "a.c", false},
		{"*.*", "a", false},
		{"*.*", "a.b", true},
		{"*.*", "a.b.c", false},
		{"a.*.c", "a.b.c", true},
		{"a.*.c", "a.c", false},
		{"#", "a.b", true},
		{"orders.*", "orders.new", true},
		{"orders.*", "orders.new.x", false},
	}
	for _, c := range cases {
		if got := matchTopic(c.pat, c.key); got != c.ok {
			t.Errorf("match %q %q = %v, want %v", c.pat, c.key, got, c.ok)
		}
	}
}

func TestFrameRoundTrip(t *testing.T) {
	p := props{
		ContentType:  "text/plain",
		DeliveryMode: 2,
		Priority:     4,
		Expiration:   "30000",
		MessageID:    "m1",
		Timestamp:    time.Unix(1_700_000_000, 0).UTC(),
		Headers:      map[string]any{"n": int32(7), "s": "v", "b": true},
	}
	body := bytes.Repeat([]byte("x"), 300)
	hdr, err := encodeHeader(60, uint64(len(body)), p)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := writeFrame(&buf, wireFrame{Type: frameHeader, Channel: 1, Body: hdr}); err != nil {
		t.Fatal(err)
	}
	f, err := readFrame(&buf)
	if err != nil {
		t.Fatal(err)
	}
	_, size, got, err := decodeHeader(f.Body)
	if err != nil {
		t.Fatal(err)
	}
	if size != uint64(len(body)) || got.MessageID != "m1" || got.DeliveryMode != 2 || got.Priority != 4 || got.Expiration != "30000" {
		t.Fatalf("header = %+v size %d", got, size)
	}
	if got.Headers["s"] != "v" || got.Headers["n"] != int32(7) || got.Headers["b"] != true {
		t.Fatalf("headers = %#v", got.Headers)
	}
	parts := splitBody(bytes.Repeat([]byte("a"), frameMax-7))
	if len(parts) != 2 || len(parts[0]) != frameMax-8 || len(parts[1]) != 1 {
		t.Fatalf("split lens %d %d", len(parts), len(parts[0]))
	}
}

func dial(t *testing.T, s *Server) *amqp.Connection {
	t.Helper()
	conn, err := amqp.DialConfig(s.URL()+"/?heartbeat=0", amqp.Config{
		SASL:   []amqp.Authentication{&amqp.PlainAuth{Username: "guest", Password: "guest"}},
		Vhost:  "/",
		Locale: "en_US",
		Dial:   amqp.DefaultDial(2 * time.Second),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestBroker_PublishConsumeConfirm(t *testing.T) {
	s := Start(t)
	conn := dial(t, s)
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ch.QueueDeclare("q", true, false, false, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := ch.Confirm(false); err != nil {
		t.Fatal(err)
	}
	body := bytes.Repeat([]byte("ab"), 150*1024)
	dc, err := ch.PublishWithDeferredConfirmWithContext(context.Background(), "", "q", true, false, amqp.Publishing{
		Body: body, MessageId: "m", ContentType: "text/plain", DeliveryMode: 2,
		Priority: 3, Expiration: "30000", Timestamp: time.Unix(1_700_000_000, 0),
		Headers: amqp.Table{"k": "v"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ack, err := dc.WaitContext(context.Background())
	if err != nil || !ack {
		t.Fatalf("confirm %v %v", ack, err)
	}
	pubs := s.Published()
	if len(pubs) != 1 || !bytes.Equal(pubs[0].Body, body) || pubs[0].MessageID != "m" || pubs[0].DeliveryMode != 2 || pubs[0].Headers["k"] != "v" {
		t.Fatalf("published headers %#v len %d", pubs[0].Headers, len(pubs[0].Body))
	}
	if err := ch.Qos(2, 0, false); err != nil {
		t.Fatal(err)
	}
	ds, err := ch.Consume("q", "c", false, false, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := <-ds
	if !bytes.Equal(got.Body, body) || got.Redelivered {
		t.Fatalf("delivery redelivered=%v len=%d", got.Redelivered, len(got.Body))
	}
	if err := got.Nack(false, true); err != nil {
		t.Fatal(err)
	}
	again := <-ds
	if !again.Redelivered || !bytes.Equal(again.Body, body) {
		t.Fatalf("requeue redelivered=%v", again.Redelivered)
	}
	if err := again.Ack(false); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && (s.Depth("q") != 0 || s.Unacked("q") != 0) {
		time.Sleep(5 * time.Millisecond)
	}
	if s.Depth("q") != 0 || s.Unacked("q") != 0 {
		t.Fatalf("depth %d unacked %d methods %v", s.Depth("q"), s.Unacked("q"), s.Methods())
	}
}

func TestBroker_NotRoutedBeforeAck(t *testing.T) {
	s := Start(t)
	conn := dial(t, s)
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	ret := ch.NotifyReturn(make(chan amqp.Return, 1))
	if err := ch.Confirm(false); err != nil {
		t.Fatal(err)
	}
	dc, err := ch.PublishWithDeferredConfirmWithContext(context.Background(), "", "missing", true, false, amqp.Publishing{Body: []byte("x")})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-ret:
		if r.ReplyCode != 312 {
			t.Fatalf("return %d %s", r.ReplyCode, r.ReplyText)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no return")
	}
	if ack, err := dc.WaitContext(context.Background()); err != nil || !ack {
		t.Fatalf("ack after return %v %v", ack, err)
	}
}

func TestBroker_PassiveMissingKeepsConnection(t *testing.T) {
	s := Start(t)
	conn := dial(t, s)
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	_, err = ch.QueueDeclare("missing", false, false, false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	ch2, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	_, err = ch2.QueueDeclare("nope", true, false, false, false, nil)
	// passive is the library's QueueDeclarePassive... QueueInspect uses passive.
	if _, err = ch2.QueueInspect("absent"); err == nil {
		t.Fatal("expected NOT_FOUND")
	} else if amqpErr, ok := err.(*amqp.Error); !ok || amqpErr.Code != 404 {
		t.Fatalf("%v", err)
	}
	ch3, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ch3.QueueDeclare("other", false, false, false, false, nil); err != nil {
		t.Fatal(err)
	}
}

func TestBroker_TopicAndLogin(t *testing.T) {
	s := Start(t)
	s.Apply(Config{Users: map[string]string{"guest": "guest"}, Vhosts: []string{"/"}})
	bad, err := amqp.DialConfig(s.URL(), amqp.Config{
		SASL:   []amqp.Authentication{&amqp.PlainAuth{Username: "guest", Password: "nope"}},
		Vhost:  "/",
		Locale: "en_US",
		Dial:   amqp.DefaultDial(2 * time.Second),
	})
	if err == nil {
		_ = bad.Close()
		t.Fatal("wrong password was accepted")
	}
	conn := dial(t, s)
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ch.QueueDeclare("q", false, true, false, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := ch.QueueBind("q", "orders.*", "amq.topic", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := ch.Confirm(false); err != nil {
		t.Fatal(err)
	}
	dc, err := ch.PublishWithDeferredConfirmWithContext(context.Background(), "amq.topic", "orders.new", true, false, amqp.Publishing{Body: []byte("hi")})
	if err != nil {
		t.Fatal(err)
	}
	if ack, err := dc.WaitContext(context.Background()); err != nil || !ack {
		t.Fatalf("confirm %v %v", ack, err)
	}
	if s.Depth("q") != 1 {
		t.Fatalf("depth %d", s.Depth("q"))
	}
	if err := ch.ExchangeDeclare("h", "headers", false, false, false, false, nil); err == nil {
		t.Fatal("headers exchange was accepted")
	}
}

func TestBroker_RedeclareAndExclusive(t *testing.T) {
	s := Start(t)
	conn := dial(t, s)
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ch.QueueDeclare("q", true, false, false, false, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := ch.QueueDeclare("q", false, false, false, false, nil); err == nil {
		t.Fatal("different durable was accepted")
	}
	ch2, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ch2.QueueDeclare("exq", false, false, true, false, nil); err != nil {
		t.Fatal(err)
	}
	other := dial(t, s)
	och, err := other.Channel()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := och.QueueDeclare("exq", false, false, true, false, nil); err == nil {
		t.Fatal("another connection locked the exclusive queue")
	}
	_ = conn.Close()
	time.Sleep(50 * time.Millisecond)
	names := s.Queues()
	for _, n := range names {
		if n == "exq" {
			t.Fatal("exclusive queue survived its connection")
		}
	}
}
