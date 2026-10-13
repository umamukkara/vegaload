---
title: "Test a RabbitMQ server"
description: "The `rabbitmq` driver speaks AMQP 0-9-1."
sidebar_position: 8
---

# Test a RabbitMQ server

The `rabbitmq` driver speaks AMQP 0-9-1. It publishes and consumes with
exchanges, queues, routing keys, acknowledgements and prefetch. A publish
with confirms reports how long the broker took to confirm. A roundtrip
reports how long the message took to come back, and that time includes
declaring the temporary queue. It uses a pure Go client, so there is
nothing to install. It is tested with RabbitMQ 3.13 and 4.x. LavinMQ and
similar brokers may work. Only RabbitMQ is tested.

The target is `amqp://host[:port][/vhost]` or `amqps://` for TLS. The
default ports are 5672 and 5671. `-protocol` stays `rabbitmq`. There is no
`rabbitmq://` scheme. The URL takes no query string, and a password in the
URL is refused. The path is the vhost: empty or `/` means `/`, `/orders`
means `orders`, and `/%2F` means a vhost whose name is `/`.

```
# Publish one message and wait for the broker to confirm it.
vegaload run -target amqp://127.0.0.1:5672 -protocol rabbitmq \
  -opt mode=publish -opt exchange=amq.topic -opt routing_key=vegaload.test \
  -body 'hello' \
  -opt password_env=RABBITMQ_PASSWORD \
  -vus 10 -duration 30s

# Send a message and read that same message back. No queue to set up.
vegaload run -target amqp://127.0.0.1:5672 -protocol rabbitmq \
  -opt mode=roundtrip -opt exchange=amq.topic -opt routing_key=vegaload.test \
  -body 'hello'
```

The job options are `mode` (`publish`, `consume`, `roundtrip`, `admin`),
`exchange`, `routing_key`, `queue`, `bind_key`, `count`, `confirm`,
`mandatory`, `persistent`, `content_type`, `priority`, `expiration`,
`headers`, `ack`, `prefetch`, `expect`, `action`, `durable`,
`auto_delete`, `queue_type` and `exchange_type`.

The connection options are `username`, `password_env`, `vhost`, `tls`
(`false`, `true`, `skip-verify`), `connection` (`shared` or `per_call`),
`channels`, `heartbeat`, `connection_name`, `allow_writes` and
`allow_admin`. `ca_file`, `cert_file`, `key_file` and `tls_server_name`
are in [TLS with your own CA](/guides/tls).

- Publishing is always allowed. `consume` with `ack=requeue` (the default)
  is always allowed, and it puts the messages back. That is a peek: the
  broker sets the redelivered flag, the order can change, and a quorum
  queue counts the delivery. `ack=ack` removes the messages and needs
  `allow_writes=true`. A roundtrip is always allowed, because it uses only
  a queue it created. `queue_info` is always allowed. `queue_declare`,
  `exchange_declare` and `queue_lifecycle` need `allow_writes=true`.
  `queue_delete`, `exchange_delete` and `queue_purge` need both
  `allow_writes=true` and `allow_admin=true`. A RabbitMQ user with limited
  permissions is the real lock.
- One connection is shared by every user (`connection=shared`, the
  default). `channels` is how many calls may use it at once (default 10,
  from 1 to 1000). Each call uses one channel and does not take a second
  while it holds that one. Publish keeps a healthy confirm-mode channel
  and uses it again. Consume, roundtrip and admin open a channel and
  close it. `connection=per_call` dials for that call and closes it after.
  A dead connection is dialled again by the next call. A call never
  retries itself.
- Put the password in an environment variable and pass its name with
  `-opt password_env=NAME`. The default user is `guest` with password
  `guest` when you set neither. Any other username needs `password_env`.
  The user can also be the user in the URL. Give it in one place.
  RabbitMQ waits about 3 seconds before it refuses a login, so a call
  timeout shorter than that reports `timed out` instead of `refused the login`.
- RabbitMQ 4.3 and newer refuse a queue that is not durable and not exclusive, unless the server sets `deprecated_features.permit.transient_nonexcl_queues = true`. This affects `queue_declare` and `queue_lifecycle` with `durable=false`. If you see a refusal when a call declares a queue, set that option on the server, or use a durable queue. Older versions are not affected.
- `vhost` defaults to `/`. It can also be the path of the URL. Give it in
  one place.
- `tls=false` is the default for `amqp://`. `amqps://` means TLS. `true`
  checks the certificate and the host name. With `-insecure`, `true` does
  not check the certificate. `skip-verify` uses TLS and does not check the
  certificate. `tls=false` with `amqps://` is an error. TLS is 1.2 or
  newer.
- `heartbeat` defaults to 10s. The range is 1s to 5m, or `0`. `0` asks the
  broker to turn heartbeats off. This client library still accepts the
  broker's own interval when the broker proposes one, which RabbitMQ does
  (about 60s), so heartbeats stay on against a real broker.
- `connection_name` defaults to `vegaload`. The broker shows it on the
  connection.
- `{id}` in the body, routing key, bind key or queue name is one value for
  the whole call. `{n}` is 1, 2, and so on, once per message. In a queue
  name or bind key, `{n}` is 1. Header values are not changed. Every
  message id is `<id>-<n>`.
- `count` defaults to 1, from 1 to 10000. A publish of more than one
  message sends them together, and the call time is the whole batch.
  `confirm=true` (the default) waits until the broker has confirmed every
  message. `confirm=false` returns when the last write is done.
  `mandatory` defaults to true when confirms are on, and false when they
  are off. A message that no queue receives fails the call. The error
  names the exchange and the routing key. `mandatory=true` needs
  `confirm=true`. A roundtrip always uses `mandatory=true`.
- `persistent=true` asks the broker to write the message to disk
  (`delivery mode` 2). The default is 1. `expiration` is how long the
  message may live, as a duration such as `5s`, sent as milliseconds.
  `priority` is 1 to 255. `headers` is a JSON object of string, number and
  boolean values.
- `consume` needs `queue`. It checks the queue with a passive declare on
  its own short-lived channel first, so a missing queue is `NOT_FOUND`
  and the connection stays usable. That check is one extra round trip.
  It then reads `count` messages. Fewer than `count` fails when the call
  times out. `expect` must match every body. `prefetch` defaults to
  `count`, and at most 1000. It must be at least `count`, or the broker
  will not deliver the rest before the final acknowledgement and the call
  waits until it times out. `0` is refused. `ack=requeue` puts every
  message back. `ack=ack` removes them, and only when every message was
  read and every `expect` passed. A timeout or a failed check puts nothing
  back by hand: closing the channel requeues them.
- `roundtrip` declares a temporary exclusive queue, binds it when the
  exchange is named, publishes, and waits until each of this call's
  messages comes back. It matches them by message id and ignores any
  other message. The default exchange needs no binding: the routing key
  is the temporary queue's name, and `routing_key` is refused. A named
  exchange must already exist (`amq.topic`, `amq.direct`, `amq.fanout`,
  or one you declared). `bind_key` defaults to `routing_key`. The
  temporary queue is deleted when the call ends, including when it fails.
- `admin` runs one action. `queue_info` reports
  `queue=NAME messages=N consumers=N`. `queue_declare` uses `durable`
  (default true), `auto_delete` (default false) and `queue_type`
  (`classic`, `quorum`, `stream`, or unset). Declaring `stream` is
  allowed. Consuming from a stream queue is not tested.
  `exchange_declare` uses `exchange_type` (`direct` by default, or
  `fanout` or `topic`). `headers` is refused. A name that starts with
  `amq.` is the broker's. `queue_lifecycle` needs `{id}` in the queue
  name, so it never deletes a queue that was already there. It declares
  the queue and deletes it.
- The body may be empty. The most it may be is 16777216 bytes.
- A call that takes too long says `timed out after <duration>`. If the
  broker has blocked the connection, the error says so and includes the
  broker's reason. A refused login names the user and never the password.
- One endpoint only. There is no AMQP 1.0 and no RabbitMQ Streams
  (port 5552). There is no management HTTP API, no shovel, no federation,
  no `basic.get`, and no dead-letter or delayed-message setup. An
  unroutable message fails the call unless you set `mandatory=false`.
- The host allowlist and caps apply as for HTTP targets.
