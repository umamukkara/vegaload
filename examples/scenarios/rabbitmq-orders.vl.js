// rabbitmq-orders.vl.js -- publish an order through RabbitMQ and read it back.
//
// You need a broker on localhost:5672. Put the password in an environment
// variable:
//   RABBITMQ_PASSWORD=secret vegaload run -vus 4 -duration 10s -secret-env RABBITMQ_PASSWORD rabbitmq-orders.vl.js
//
// A call does not throw when the broker fails. It returns {ok, error},
// so check it. A call that is set up wrongly (an unknown option) does throw.
// Each user keeps one connection for the whole run.
export default function () {
  const url = "amqp://localhost:5672";
  const login = { password: env.RABBITMQ_PASSWORD };

  // No queue to create, and no write flag: the roundtrip uses a queue of its own.
  const rt = rabbitmq.roundtrip(url, {
    ...login,
    exchange: "amq.topic",
    routing_key: "vegaload.orders.{id}",
    body: "order {id}",
  });
  check(rt, {
    "order roundtrip": (r) => r.ok && r.messages.length === 1 && r.messages[0].body.indexOf("order ") === 0,
  });

  // Declare a queue, publish into it, and take the message off.
  // ack: "ack" removes the message, so this connection allows writes.
  const q = "vegaload.orders." + Math.floor(Math.random() * 1e9);
  const writes = { ...login, allow_writes: true };
  const declared = rabbitmq.admin(url, { ...writes, action: "queue_declare", queue: q, durable: false });
  check(declared, { "queue declared": (r) => r.ok && r.text.indexOf("queue=" + q) === 0 });
  if (!declared.ok) return;

  const sent = rabbitmq.publish(url, { ...writes, routing_key: q, body: "hello" });
  check(sent, { "order published": (r) => r.ok });

  const got = rabbitmq.consume(url, { ...writes, queue: q, ack: "ack", expect: "hello" });
  check(got, {
    "order consumed": (r) => r.ok && r.messages.length === 1 && r.messages[0].body === "hello",
  });

  const removed = rabbitmq.admin(url, {
    ...writes,
    allow_admin: true,
    action: "queue_delete",
    queue: q,
  });
  check(removed, { "queue deleted": (r) => r.ok });
}
