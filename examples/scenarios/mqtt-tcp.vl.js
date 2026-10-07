// mqtt-tcp.vl.js -- a scenario that talks to an MQTT broker and a TCP
// service, and uses what they answer. JavaScript only for now.
//
// You need a broker on localhost:1883 and a TCP service that answers a
// line, such as Redis on localhost:6379. Then:
//   vegaload run -vus 2 -duration 10s mqtt-tcp.vl.js
//
// A call does not throw when the network fails. It returns {ok, error},
// so check it. A call that is set up wrongly (an unknown option) does throw.
export default function () {
  // Redis answers "+PONG\r\n". The body is sent as written, no backslash
  // escapes, so "\r\n" here is the real CR LF.
  const ping = tcp.send("tcp://localhost:6379", { body: "PING\r\n", until: "\r\n" });
  check(ping, {
    "redis answers": (r) => r.ok,
    "reply is PONG": (r) => r.body === "+PONG\r\n",
  });

  // Publish, then read it back through the broker in one call. {id} makes
  // the topic and the message unique for this call.
  const rt = mqtt.roundtrip("mqtt://localhost:1883", {
    topic: "vegaload/demo/{id}",
    body: "order {id}",
  });
  check(rt, {
    "message came back": (r) => r.ok && r.messages.length === 1,
    "message is ours": (r) => r.ok && r.messages[0].body.startsWith("order "),
  });
}
