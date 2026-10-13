---
title: "TLS with your own CA"
description: "These four options are the same for Redis, RabbitMQ, FTP, MySQL, PostgreSQL, HTTPS (`http1` and `http2`), WebSocket (`wss`), gRPC (`grpcs`), MQTT (`mqtts`), Kafka (`kafkas`) and TCP with `tls=true`."
sidebar_position: 11
---

# TLS with your own CA

These four options are the same for Redis, RabbitMQ, FTP, MySQL, PostgreSQL, HTTPS (`http1` and `http2`), WebSocket (`wss`), gRPC (`grpcs`), MQTT (`mqtts`), Kafka (`kafkas`) and TCP with `tls=true`. Pass them with `-opt`, or in a scenario options object. They are not separate flags. Plain `http`, `ws`, `grpc`, `mqtt`, `kafka` and UDP reject them, because TLS is off.

| Key | Meaning |
| --- | --- |
| `ca_file` | A PEM file with one or more CA certificates. These are the only CAs that are trusted. The system roots are not used. To trust the system roots as well, put them in this file too. |
| `cert_file` | A PEM client certificate. Put intermediates after it. |
| `key_file` | The PEM private key for `cert_file`. |
| `tls_server_name` | The name to send as SNI and to check on the certificate. Use it when you connect by IP, or by a name that is not on the certificate. |

`cert_file` and `key_file` go together. One without the other is an error. With both, the client certificate is sent when the server asks for it.

`ca_file` cannot be combined with `-insecure`, `tls=skip-verify`, or `tls_verify=false`. Those modes do not check the certificate. `cert_file` and `key_file` are allowed with them. PostgreSQL `sslmode=prefer` and `sslmode=require` also skip the check, so `ca_file` with either of them is an error: it needs `sslmode=verify-full`.

If TLS is off, any of the four is an error. The files are read when the client is built, not on every call.

An encrypted private key is refused. PKCS#12 and JKS are not read. CRLs and OCSP are not checked. A certificate is not pinned by fingerprint. FTP still defaults to TLS 1.2 (`tls_max`).

```
# A private CA, connecting by IP. The certificate is for redis.internal.example.
vegaload run -target rediss://10.0.0.5:6379 -protocol redis \
  -body PING \
  -opt ca_file=./certs/ca.pem \
  -opt tls_server_name=redis.internal.example

# Mutual TLS. The server asks for the client certificate.
vegaload run -target amqps://10.0.0.8:5671 -protocol rabbitmq \
  -opt mode=publish -opt mandatory=false \
  -body hello \
  -opt ca_file=./certs/ca.pem \
  -opt cert_file=./certs/client.pem \
  -opt key_file=./certs/client.key \
  -opt tls_server_name=rabbit.internal.example
```

A scenario passes the same keys, for example `ca_file: "certs/ca.pem"`.
