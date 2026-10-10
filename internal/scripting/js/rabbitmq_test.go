package js

import (
	"errors"
	"strings"
	"testing"

	"github.com/vegaload/vegaload/internal/protocol/rabbitmq/rabbitmqtest"
	"github.com/vegaload/vegaload/internal/scripting/netapi"
)

func TestRabbitMQ_RoundtripPublishAndConsume(t *testing.T) {
	s := rabbitmqtest.Start(t)
	must(t, nil, assertFn+`
		const url = "`+s.URL()+`";
		const rt = rabbitmq.roundtrip(url, {exchange: "amq.topic", routing_key: "orders.{id}", body: "ping"});
		assert(rt.ok, rt.error);
		assert(rt.messages.length === 1 && rt.messages[0].body === "ping", JSON.stringify(rt.messages));
		assert(rt.messages[0].routingKey.indexOf("orders.") === 0, rt.messages[0].routingKey);
		assert(rt.messages[0].topic === undefined, "mqtt shape");
		assert(rt.value === undefined && rt.records === undefined, "no other protocol fields");
		const declared = rabbitmq.admin(url, {action: "queue_declare", queue: "q", allow_writes: true, durable: false});
		assert(declared.ok && declared.text.indexOf("queue=q") === 0, declared.error || declared.text);
		const sent = rabbitmq.publish(url, {routing_key: "q", body: "hello", allow_writes: true});
		assert(sent.ok, sent.error);
		const got = rabbitmq.consume(url, {queue: "q", ack: "ack", expect: "hello", allow_writes: true});
		assert(got.ok && got.messages.length === 1 && got.messages[0].body === "hello", got.error || JSON.stringify(got.messages));
		const denied = rabbitmq.consume(url, {queue: "missing", ack: "ack"});
		assert(denied.ok === false && denied.error.indexOf("pass allow_writes: true") >= 0, denied.error);
	`)
	if n := s.ConnCount(); n < 1 {
		t.Fatalf("connections %d", n)
	}
}

func TestRabbitMQ_OneConnectionForManyCalls(t *testing.T) {
	s := rabbitmqtest.Start(t)
	must(t, nil, assertFn+`
		const url = "`+s.URL()+`";
		for (let i = 0; i < 4; i++) {
			const r = rabbitmq.roundtrip(url, {body: "ping"});
			assert(r.ok, r.error);
		}
	`)
	if n := s.ConnCount(); n != 1 {
		t.Errorf("%d connections for 4 calls, want 1", n)
	}
}

func TestRabbitMQ_SetupMistakesThrow(t *testing.T) {
	for name, body := range map[string]string{
		"unknown option": `rabbitmq.publish("amqp://127.0.0.1:9", {body: "x", bogus: 1});`,
		"mode":           `rabbitmq.publish("amqp://127.0.0.1:9", {body: "x", mode: "consume"});`,
		"password_env":   `rabbitmq.publish("amqp://127.0.0.1:9", {body: "x", password_env: "X"});`,
	} {
		if err := runScript(t, nil, body); err == nil {
			t.Errorf("%s: want the script to throw", name)
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
	if err := runScript(t, netapi.SafetyCheck(check), `rabbitmq.publish("amqp://mq.example.com", {body: "x"});`); err == nil || !strings.Contains(err.Error(), "host not allowed") {
		t.Errorf("err = %v", err)
	}
}
