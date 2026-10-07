# mqtt_tcp.py -- the same scenario as mqtt-tcp.vl.js, in Python: talk to a
# TCP service and an MQTT broker, and use what they answer.
#
# You need a broker on localhost:1883 and a TCP service that answers a line,
# such as Redis on localhost:6379. Python scenarios need python3.
#
#   vegaload run -vus 2 -duration 10s mqtt_tcp.py
#
# A call does not raise when the network fails. It returns a reply with
# ok False and error set, so check it. A call that is set up wrongly (an
# unknown option) does raise.


def iteration():
    # The body is sent as written, so "\r\n" is the real CR LF.
    ping = tcp.send("tcp://localhost:6379", body="PING\r\n", until="\r\n")
    check(ping, {
        "redis answers": lambda r: r.ok,
        "reply is PONG": lambda r: r.body == "+PONG\r\n",
    })

    # Publish, then read it back through the broker in one call. {id} makes
    # the topic and the message unique for this call.
    rt = mqtt.roundtrip("mqtt://localhost:1883", topic="vegaload/demo/{id}", body="order {id}")
    check(rt, {
        "message came back": lambda r: r.ok and len(r.messages) == 1,
        "message is ours": lambda r: r.ok and r.messages[0].body.startswith("order "),
    })
