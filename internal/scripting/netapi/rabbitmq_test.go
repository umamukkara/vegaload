package netapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/protocol/rabbitmq/rabbitmqtest"
)

func TestRabbitMQ_ClientIsKeptAndReplyShape(t *testing.T) {
	s := rabbitmqtest.Start(t)
	c := NewProtoClient(nil, 5*time.Second)
	defer c.Close()
	opts := map[string]string{"exchange": "amq.topic", "routing_key": "orders.{id}"}
	for i := 0; i < 4; i++ {
		r, err := c.RabbitMQ(context.Background(), "roundtrip", ProtoCall{URL: s.URL(), Body: []byte("ping"), Options: cloneOptions(opts)})
		if err != nil || !r.OK || len(r.RabbitMessages) != 1 || r.RabbitMessages[0].Body != "ping" {
			t.Fatalf("call %d: %v ok=%v msgs=%d err=%s", i, err, r.OK, len(r.RabbitMessages), r.Error)
		}
	}
	if n := s.ConnCount(); n != 1 {
		t.Errorf("%d connections for 4 calls, want 1", n)
	}
	if len(c.rmqConns) != 1 {
		t.Errorf("%d clients kept, want 1", len(c.rmqConns))
	}
	r, err := c.Call(context.Background(), "rabbitmq.roundtrip", ProtoCall{URL: s.URL(), Body: []byte("ping"), Options: cloneOptions(opts)})
	if err != nil {
		t.Fatal(err)
	}
	f := r.Fields()
	msgs := f["messages"].([]any)
	m := msgs[0].(map[string]any)
	if _, topic := m["topic"]; f["ok"] != true || m["body"] != "ping" || m["routingKey"] == "" || m["messageId"] == "" || topic {
		t.Fatalf("fields = %#v", f)
	}
	if _, ok := f["value"]; ok {
		t.Fatal("a rabbitmq reply must not grow redis fields")
	}
	if _, ok := f["records"]; ok {
		t.Fatal("a rabbitmq reply must not grow kafka fields")
	}
	if f["text"] != "" {
		t.Fatalf("text = %#v", f["text"])
	}
	info, err := c.RabbitMQ(context.Background(), "admin", ProtoCall{
		URL: s.URL(), Options: map[string]string{"action": "queue_declare", "queue": "q", "allow_writes": "true"},
	})
	if err != nil || !info.OK || !strings.Contains(info.RabbitText, "queue=q") {
		t.Fatalf("%v %+v", err, info)
	}
	c.Close()
	if len(c.rmqConns) != 0 {
		t.Error("Close must release the clients")
	}
}

func TestRabbitMQ_RefusesModeAndPasswordEnv(t *testing.T) {
	c := NewProtoClient(nil, time.Second)
	_, err := c.RabbitMQ(context.Background(), "publish", ProtoCall{
		URL: "amqp://127.0.0.1:1", Options: map[string]string{"mode": "consume"},
	})
	if err == nil || !strings.Contains(err.Error(), "function you called sets it") {
		t.Fatal(err)
	}
	_, err = c.RabbitMQ(context.Background(), "publish", ProtoCall{
		URL: "amqp://127.0.0.1:1", Options: map[string]string{"password_env": "X"},
	})
	if err == nil || !strings.Contains(err.Error(), "pass password: env.NAME") {
		t.Fatal(err)
	}
}

func TestRabbitMQ_AckNeedsTheConnectionFlag(t *testing.T) {
	s := rabbitmqtest.Start(t)
	c := NewProtoClient(nil, time.Second)
	defer c.Close()
	r, err := c.RabbitMQ(context.Background(), "consume", ProtoCall{
		URL: s.URL(), Options: map[string]string{"queue": "q", "ack": "ack"},
	})
	if err != nil || r.OK || !strings.Contains(r.Error, "pass allow_writes: true") || s.ConnCount() != 0 {
		t.Fatalf("err %v reply %+v conns %d", err, r, s.ConnCount())
	}
}

func TestProtoFunctions_ListsRabbitMQ(t *testing.T) {
	f := ProtoFunctions()["rabbitmq"]
	if len(f) != 4 || f[0] != "publish" || f[1] != "consume" || f[2] != "roundtrip" || f[3] != "admin" {
		t.Fatalf("%v", f)
	}
}
