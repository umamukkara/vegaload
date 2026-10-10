package python

import (
	"errors"
	"strings"
	"testing"

	"github.com/vegaload/vegaload/internal/protocol/rabbitmq/rabbitmqtest"
	"github.com/vegaload/vegaload/internal/scripting/netapi"
)

func TestRabbitMQ_RoundtripPublishAndConsume(t *testing.T) {
	s := rabbitmqtest.Start(t)
	must(t, nil, `
url = "`+s.URL()+`"
rt = rabbitmq.roundtrip(url, exchange="amq.topic", routing_key="orders.{id}", body="ping")
assert rt.ok, rt.error
assert len(rt.messages) == 1 and rt.messages[0].body == "ping", rt.messages
assert rt.messages[0].routingKey.startswith("orders."), rt.messages[0].routingKey
assert "topic" not in rt.messages[0]
assert "value" not in rt and "records" not in rt
declared = rabbitmq.admin(url, action="queue_declare", queue="q", allow_writes=True, durable=False)
assert declared.ok and declared.text.startswith("queue=q"), declared.error or declared.text
sent = rabbitmq.publish(url, routing_key="q", body="hello", allow_writes=True)
assert sent.ok, sent.error
got = rabbitmq.consume(url, queue="q", ack="ack", expect="hello", allow_writes=True)
assert got.ok and len(got.messages) == 1 and got.messages[0].body == "hello", got.error or got.messages
denied = rabbitmq.consume(url, queue="missing", ack="ack")
assert denied.ok is False and "pass allow_writes: true" in denied.error, denied.error
`)
}

func TestRabbitMQ_OneConnectionForManyCalls(t *testing.T) {
	s := rabbitmqtest.Start(t)
	must(t, nil, `
url = "`+s.URL()+`"
for i in range(4):
    r = rabbitmq.roundtrip(url, body="ping")
    assert r.ok, r.error
`)
	if n := s.ConnCount(); n != 1 {
		t.Errorf("%d connections for 4 calls, want 1", n)
	}
}

func TestRabbitMQ_SetupMistakesRaise(t *testing.T) {
	for name, body := range map[string]string{
		"unknown option": `rabbitmq.publish("amqp://127.0.0.1:9", body="x", bogus=1)`,
		"mode":           `rabbitmq.publish("amqp://127.0.0.1:9", body="x", mode="consume")`,
		"password_env":   `rabbitmq.publish("amqp://127.0.0.1:9", body="x", password_env="X")`,
	} {
		if err := runIteration(t, nil, body); err == nil {
			t.Errorf("%s: want the script to raise", name)
		}
	}
}

func TestRabbitMQ_SafetyCheckRefusesTheHost(t *testing.T) {
	check := func(h string) error {
		if h == "mq.example.com" {
			return errors.New("host not allowed")
		}
		return nil
	}
	if err := runIteration(t, netapi.SafetyCheck(check), `rabbitmq.publish("amqp://mq.example.com", body="x")`); err == nil || !strings.Contains(err.Error(), "host not allowed") {
		t.Errorf("err = %v", err)
	}
}
