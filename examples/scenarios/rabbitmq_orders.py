# rabbitmq_orders.py -- the same scenario as rabbitmq-orders.vl.js, in Python.
#
# You need a broker on localhost:5672. Put the password in an environment
# variable. Python scenarios need python3.
#   RABBITMQ_PASSWORD=secret vegaload run -vus 4 -duration 10s -secret-env RABBITMQ_PASSWORD rabbitmq_orders.py
#
# A call does not raise when the broker fails. It returns a reply with
# ok False and error set, so check it. A call that is set up wrongly (an
# unknown option) does raise.

import random

URL = "amqp://localhost:5672"


def iteration():
    login = dict(password=env.RABBITMQ_PASSWORD)

    # No queue to create, and no write flag: the roundtrip uses a queue of its own.
    rt = rabbitmq.roundtrip(
        URL,
        **login,
        exchange="amq.topic",
        routing_key="vegaload.orders.{id}",
        body="order {id}",
    )
    check(rt, {"order roundtrip": lambda r: r.ok and len(r.messages) == 1 and r.messages[0].body.startswith("order ")})

    # Declare a queue, publish into it, and take the message off.
    # ack="ack" removes the message, so this connection allows writes.
    q = "vegaload.orders." + str(random.randint(0, 10**9))
    writes = dict(login, allow_writes=True)
    declared = rabbitmq.admin(URL, **writes, action="queue_declare", queue=q, durable=False)
    check(declared, {"queue declared": lambda r: r.ok and r.text.startswith("queue=" + q)})
    if not declared.ok:
        return

    sent = rabbitmq.publish(URL, **writes, routing_key=q, body="hello")
    check(sent, {"order published": lambda r: r.ok})

    got = rabbitmq.consume(URL, **writes, queue=q, ack="ack", expect="hello")
    check(got, {"order consumed": lambda r: r.ok and len(r.messages) == 1 and r.messages[0].body == "hello"})

    removed = rabbitmq.admin(URL, **writes, allow_admin=True, action="queue_delete", queue=q)
    check(removed, {"queue deleted": lambda r: r.ok})
