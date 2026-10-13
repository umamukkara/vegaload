---
title: Protocol options
description: The -opt keys and modes each protocol accepts, from vegaload protocols.
sidebar_position: 3
---

# Protocol options

In a protocol-direct run, `-opt key=value` sets an option for the protocol. Repeat the flag for more than one:

```
vegaload run -target kafka://localhost:9092 -protocol kafka \
  -opt mode=produce -opt topic=orders -opt acks=all
```

In a scenario, the same keys go in the options object of the call, as in `mqtt.publish(url, { topic: "sensors/1", qos: 1 })`. See [Scenarios](../guides/scenarios.md).

This page lists the keys. The guide for each protocol says what they mean and shows runs you can copy. The lists come from `vegaload protocols`, so they match the binary. Run `vegaload protocols NAME` for one protocol.

## TLS keys

Protocols with TLS accept four more keys: `ca_file`, `cert_file`, `key_file` and `tls_server_name`. They are left out of the table below. SFTP is not TLS: its `key_file` is an SSH private key and stays in the table. TCP uses them only with `tls=true`. UDP has no TLS. See [TLS options](../guides/tls.md).

## Keys by protocol

| Protocol | Since | Modes | Keys | Guide |
| --- | --- | --- | --- | --- |
| `http1` | 0.1.0 | - | only the TLS keys | [Guide](../guides/http.md) |
| `http2` | 0.1.0 | - | only the TLS keys | [Guide](../guides/http.md) |
| `grpc` | 0.1.0 | - | only the TLS keys | [Guide](../guides/grpc.md) |
| `websocket` | 0.1.0 | - | only the TLS keys | [Guide](../guides/websocket.md) |
| `mqtt` | 0.6.0 | `publish`, `subscribe`, `roundtrip` | `mode`, `topic`, `qos`, `retain`, `username`, `password_env`, `client_id`, `count`, `expect`, `keepalive` | [Guide](../guides/mqtt.md) |
| `kafka` | 0.6.0 | `produce`, `consume`, `roundtrip`, `admin` | `mode`, `topic`, `key`, `acks`, `compression`, `count`, `expect`, `from`, `action`, `partitions`, `replication`, `sasl`, `username`, `password_env`, `client_id` | [Guide](../guides/kafka.md) |
| `postgres` | 0.7.0 | - | `username`, `password_env`, `database`, `sslmode`, `application_name`, `pool`, `allow_writes`, `query_mode`, `args`, `min_rows`, `expect`, `max_rows` | [Guide](../guides/postgres.md) |
| `mysql` | 0.7.0 | - | `username`, `password_env`, `database`, `tls`, `pool`, `allow_writes`, `query_mode`, `args`, `min_rows`, `expect`, `max_rows` | [Guide](../guides/mysql.md) |
| `redis` | 0.7.0 | - | `username`, `password_env`, `database`, `tls`, `pool`, `protocol`, `allow_writes`, `allow_admin`, `transaction`, `args`, `min_rows`, `expect`, `max_rows` | [Guide](../guides/redis.md) |
| `rabbitmq` | 0.7.0 | `publish`, `consume`, `roundtrip`, `admin` | `mode`, `exchange`, `routing_key`, `queue`, `bind_key`, `count`, `confirm`, `mandatory`, `persistent`, `content_type`, `priority`, `expiration`, `headers`, `ack`, `prefetch`, `expect`, `action`, `durable`, `auto_delete`, `queue_type`, `exchange_type`, `username`, `password_env`, `vhost`, `tls`, `connection`, `channels`, `heartbeat`, `connection_name`, `allow_writes`, `allow_admin` | [Guide](../guides/rabbitmq.md) |
| `ftp` | 0.7.0 | `connect`, `download`, `upload`, `list`, `stat`, `delete`, `roundtrip` | `mode`, `path`, `size`, `fill`, `expect_size`, `expect_sha256`, `expect`, `limit`, `hidden`, `username`, `password_env`, `tls`, `tls_verify`, `tls_max`, `sessions`, `connection`, `data_host`, `epsv`, `allow_writes`, `allow_admin` | [Guide](../guides/ftp.md) |
| `sftp` | 0.9.0 | `connect`, `download`, `upload`, `list`, `stat`, `delete`, `roundtrip` | `mode`, `path`, `size`, `fill`, `expect_size`, `expect_sha256`, `expect`, `limit`, `hidden`, `username`, `password_env`, `key_file`, `key_passphrase_env`, `host_key`, `connections`, `connection`, `allow_writes`, `allow_admin`, `concurrent_writes` | [Guide](../guides/sftp.md) |
| `tcp` | 0.6.0 | - | `escape`, `expect`, `max`, `read`, `tls`, `until` | [Guide](../guides/tcp-udp.md) |
| `udp` | 0.6.0 | - | `escape`, `expect`, `reply` | [Guide](../guides/tcp-udp.md) |

## Write and admin guards

Some protocols can change data on the target. Those calls are off unless you set a key:

- `allow_writes` allows writes. Postgres, MySQL, Redis, RabbitMQ, FTP and SFTP have it.
- `allow_admin` allows admin work. Redis, RabbitMQ, FTP and SFTP have it.

Without the key, VegaLoad refuses the call. Each guide says what is refused.

Do not put a password in an `-opt` value. Use `password_env` (or `key_passphrase_env` for SFTP). Its value is the name of an environment variable that holds the secret.
