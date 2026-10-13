---
title: Test a WebSocket service
description: Load test a WebSocket endpoint with one command, or hold a connection open in a scenario.
sidebar_position: 3
---

# Test a WebSocket service

`-protocol websocket` takes `ws://` and `wss://` targets. In protocol-direct mode, each iteration connects, sends the `-body` message, waits for one reply, and closes.

The examples run against the [sample app](https://github.com/vegaload/vegaload/tree/main/examples/sample-app). Start it in its own terminal:

```bash
cd examples/sample-app
go run .
```

It echoes every message at `ws://127.0.0.1:8080/ws/echo`, with 5 to 15 ms of delay. Localhost is always allowed. For any other host, see [Safety](/ai-agents/safety).

## One command, no file

```bash
vegaload run -target ws://127.0.0.1:8080/ws/echo -protocol websocket \
  -body hello -vus 5 -duration 3s
```

Each iteration opens a connection, sends `hello`, waits for the echo, and closes. On a laptop this makes about 1,400 iterations in 3 seconds, with p50 near 10 ms. The report has a Protocols section for the WebSocket calls.

Leave out `-body` to measure only the handshake: connect and close, with no message. That runs much faster, because the echo delay applies to messages only. It can make tens of thousands of connections in a few seconds.

:::caution
A handshake-only run opens a new TCP connection each time. The operating system keeps each closed connection's local port reserved for a while (TIME_WAIT). A fast run can use up most of the available local ports. The next run that opens new connections then fails many iterations at once, with near-zero latency. If you see that, wait a minute and run again. It is a limit of the machine running VegaLoad, not of the server.
:::

## TLS

For `wss://` targets, VegaLoad checks the certificate. To trust your own CA, pass `ca_file` with `-opt`. See [TLS with your own CA](/guides/tls).

## Pass or fail limits

A threshold can target the protocol:

```bash
vegaload run -target ws://127.0.0.1:8080/ws/echo -protocol websocket -body hello \
  -vus 5 -duration 3s -threshold 'p95{protocol="websocket"} < 100ms'
```

See [Thresholds](/guides/thresholds).

## A connection with many messages

A scenario can hold a connection open and send many messages. The script has `ws.connect`, `ws.send`, `ws.receive` and `ws.close`. It can also carry a value from an HTTP call into a WebSocket call.

[`http-ws-chain.vl.js`](https://github.com/vegaload/vegaload/blob/main/examples/scenarios/http-ws-chain.vl.js) (Python: [`http_ws_chain.py`](https://github.com/vegaload/vegaload/blob/main/examples/scenarios/http_ws_chain.py)) creates a widget over HTTP, opens the WebSocket, sends a message with the new id, and checks that the echo matches:

```bash
vegaload run -vus 5 -duration 10s http-ws-chain.vl.js
```

The report shows the HTTP and WebSocket calls as two protocols, and each call (`connect`, `send`, `receive`) on its own. The host allow-list is checked for every call, not only the first. See [Scenarios](/guides/scenarios).

## Options

`vegaload protocols websocket` lists the options and the scenario calls. WebSocket takes `ca_file`, `cert_file`, `key_file` and `tls_server_name`. Connections from a browser page on another origin are refused by the sample app. VegaLoad is not a browser, so this does not affect it.
