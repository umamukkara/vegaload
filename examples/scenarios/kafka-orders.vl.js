// kafka-orders.vl.js -- a scenario that writes an order to Kafka, reads
// it back, and checks the answer. JavaScript only for now.
//
// You need a broker on localhost:9092 with a topic named "orders":
//   vegaload run -vus 2 -duration 10s kafka-orders.vl.js
//
// A call does not throw when the network fails. It returns {ok, error},
// so check it. A call that is set up wrongly (an unknown option) does throw.
export default function () {
  const url = "kafka://localhost:9092";

  const sent = kafka.produce(url, {
    topic: "orders",
    key: "customer-" + Math.floor(Math.random() * 100),
    value: JSON.stringify({ item: "book", qty: 1 }),
  });
  check(sent, {
    "order stored": (r) => r.ok,
    "broker gave an offset": (r) => r.ok && r.records[0].offset >= 0,
  });

  // Write records and read exactly those records back through the broker.
  const rt = kafka.roundtrip(url, { topic: "orders", value: "check {id}", count: 2 });
  check(rt, {
    "roundtrip worked": (r) => r.ok && r.records.length === 2,
  });
}
