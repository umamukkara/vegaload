---
title: "Test a raw TCP or UDP service"
description: "For a service that speaks its own protocol, use the `tcp` or `udp` driver."
sidebar_position: 2
---

# Test a raw TCP or UDP service

For a service that speaks its own protocol, use the `tcp` or `udp` driver.
Each iteration opens a connection, sends the body, optionally checks the
reply, and closes. Driver settings go in repeatable `-opt key=value` flags.

```
# Does the service answer PING with PONG? Read until the reply contains PONG.
vegaload run -target tcp://127.0.0.1:6379 -protocol tcp \
  -body 'PING\r\n' -opt expect=PONG -vus 10 -duration 20s

# Read exactly 8 bytes back, or read until a delimiter, or only connect.
vegaload run -target tcp://127.0.0.1:7000 -protocol tcp -body 'hello\n' -opt read=8
vegaload run -target tcp://127.0.0.1:25 -protocol tcp -opt until='\r\n' -opt expect=220
vegaload run -target tcp://127.0.0.1:7000 -protocol tcp

# Send one UDP datagram. Add -opt reply=true or -opt expect=... to wait for an answer.
vegaload run -target udp://127.0.0.1:514 -protocol udp -body '<13>test message\n'
```

The `tcp` options are `read`, `until`, `expect`, `max`, `tls` and `escape`.
The `udp` options are `reply`, `expect` and `escape`. In `-body`, `expect`
and `until`, the text may use `\n`, `\r`, `\t`, `\0`, `\\` and `\xNN`. Pass
`-opt escape=false` to send backslashes as they are. Use `-opt tls=true` for
a TLS service, with `-insecure` if its certificate is not trusted. `tls=true` takes the four options under "TLS with your own CA". UDP has no TLS, so those options are an error. A
misspelled option is an error, not ignored. The same host allowlist and
caps apply as for HTTP targets. Each iteration opens a new connection, so a
very high rate on one machine can run out of local ports. Keep the rate
moderate, or spread the load over more machines.
