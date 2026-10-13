---
title: "Test an MQTT broker"
description: "The `mqtt` driver does one job per iteration: connect, do the job, close."
sidebar_position: 3
---

# Test an MQTT broker

The `mqtt` driver does one job per iteration: connect, do the job, close. The
job is set with `-opt mode=...`. The body is the message payload.

```
# Publish a message and wait for the broker's ack (the default mode).
vegaload run -target mqtt://127.0.0.1:1883 -protocol mqtt -body 'hello' \
  -opt topic=load/test -opt qos=1 -vus 20 -duration 30s

# Publish, and wait until the same message comes back. This measures
# delivery through the broker. {id} is a unique client id for each iteration.
vegaload run -target mqtt://127.0.0.1:1883 -protocol mqtt -body 'ping {id}' \
  -opt mode=roundtrip -opt topic='load/{id}' -opt qos=1

# Subscribe and wait for 5 messages that contain "ok".
vegaload run -target mqtt://127.0.0.1:1883 -protocol mqtt \
  -opt mode=subscribe -opt topic='sensors/#' -opt count=5 -opt expect=ok
```

The options are `mode` (`publish`, `subscribe`, `roundtrip`), `topic`,
`qos`, `retain`, `username`, `password_env`, `client_id`, `count`, `expect`
and `keepalive`. Put the password in an environment variable and pass its
name with `-opt password_env=NAME` (it needs `username`), so it is not on
the command line. In `roundtrip` mode the body must contain `{id}`, so each
user can tell its own message from the messages of other users. Use `mqtts://` for TLS, with `-insecure` if the certificate is not
trusted. `mqtts://` takes the four options under "TLS with your own CA". Every iteration uses a new client id, because a broker closes an
older connection that has the same id. The same host allowlist and caps
apply as for HTTP targets.
