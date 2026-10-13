---
title: Test a gRPC service
description: Load test a gRPC method from the command line with an encoded body, or call it from a scenario with JSON.
sidebar_position: 4
---

# Test a gRPC service

`-protocol grpc` takes `host:port`, `grpc://` and `grpcs://` targets. It makes unary calls. You name the method with `-method`, in the form `/package.Service/Method`.

The examples run against the [sample app](https://github.com/vegaload/vegaload/tree/main/examples/sample-app). Start it in its own terminal:

```bash
cd examples/sample-app
go run .
```

It serves gRPC at `127.0.0.1:9090`: the standard health service, and a small `widgets.v1.WidgetService`. Localhost is always allowed. For any other host, see [Safety](/ai-agents/safety).

## One command, no file

A call with no body, to the health service:

```bash
vegaload run -target 127.0.0.1:9090 -protocol grpc \
  -method /grpc.health.v1.Health/Check -vus 5 -duration 3s
```

On a laptop this makes tens of thousands of calls in 3 seconds, with sub-millisecond latency, because the health service adds no delay. List the widgets:

```bash
vegaload run -target 127.0.0.1:9090 -protocol grpc \
  -method /widgets.v1.WidgetService/ListWidgets -vus 5 -duration 3s
```

About 600 calls, with p50 near 25 ms.

## The body is encoded protobuf

In protocol-direct mode, VegaLoad sends `-body` as bytes that are already encoded as protobuf. It does not turn JSON into protobuf for you here. For the sample service, the bytes are short enough to write by hand in bash or zsh, with `$'...'`:

| Call | `-body` |
| --- | --- |
| `Health/Check`, `ListWidgets` | leave it out (an empty message) |
| `GetWidget { id: 2 }` | `$'\x08\x02'` |
| `CreateWidget { name: "flange" }` | `$'\x0a\x06flange'` |

A create:

```bash
vegaload run -target 127.0.0.1:9090 -protocol grpc \
  -method /widgets.v1.WidgetService/CreateWidget -body $'\x0a\x06flange' \
  -vus 5 -duration 3s -out grpc-create.json
vegaload diagnose grpc-create.json
```

The sample app fails about 3% of creates with `UNAVAILABLE` on purpose. The run shows a failure rate near 3%.

The byte after `\x0a` is the name's length in bytes, in hex. For `gear` it is `$'\x0a\x04gear'`. A wrong length does not hang. Every call fails at once with `INVALID_ARGUMENT`.

:::tip
A run with a 100% error rate and latency under a millisecond almost always means a bad `-body`. Check the encoded bytes first.
:::

`$'...'` is bash and zsh syntax. In fish or PowerShell, write the bytes another way. For a real service, encode the message with your own tools, or use a scenario, below.

## TLS

For `grpcs://` targets, VegaLoad checks the certificate. To trust your own CA, or to send a client certificate, pass `ca_file`, `cert_file`, `key_file` and `tls_server_name` with `-opt`. See [TLS with your own CA](/guides/tls).

## Pass or fail limits

```bash
vegaload run -target 127.0.0.1:9090 -protocol grpc \
  -method /widgets.v1.WidgetService/ListWidgets -vus 5 -duration 3s \
  -threshold 'p95{protocol="grpc"} < 100ms'
```

See [Thresholds](/guides/thresholds).

## JSON in a scenario

A scenario can call a method with JSON. The script has `grpc.call(url, {method, body})`. It takes a JSON body and returns a JSON reply, with no `.proto` file, when the server offers **reflection**. VegaLoad asks the server for the message types on the first call to a method, and keeps the answer.

```js
export default function () {
  const reply = grpc.call("localhost:50051", {
    method: "/pkg.Greeter/SayHello",
    body: { name: "x" },
  });
  check(reply, { greeted: (r) => r.ok && r.json.message === "Hello x" });
}
```

The reply has `status` (the gRPC code, 0 when it worked), `statusName` (such as `NotFound`), `body` (the reply as JSON text) and `json` (the same, parsed). A server's error status is a reply with `ok` false, not an exception.

Without reflection, a JSON body throws. For such a server, pass `encoding: "base64"`. The `body` and the reply are then the encoded message in base64. The sample app has no reflection.

Only unary methods work for now. See [Scenarios](/guides/scenarios) and [`mixed-protocols.vl.js`](https://github.com/vegaload/vegaload/blob/main/examples/scenarios/mixed-protocols.vl.js).

## Options

`vegaload protocols grpc` lists the options and the scenario call. gRPC takes `ca_file`, `cert_file`, `key_file` and `tls_server_name`.
