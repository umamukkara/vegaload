# sample-app

A tiny HTTP service to load test, so VegaLoad's getting-started walkthrough
(see [`../README.md`](../README.md)) has something real to point at instead
of a placeholder URL.

It is not part of VegaLoad itself -- it has its own `go.mod` -- it's just a
target worth running `vegaload` against.

## What it does

An in-memory "widgets" API, with latency and failures injected on purpose so
a baseline run has something for `vegaload diagnose` to say:

| Method | Path            | Behavior                                                        |
|--------|-----------------|-------------------------------------------------------------------|
| GET    | `/health`       | always `200`, near-zero latency                                   |
| GET    | `/widgets`      | lists widgets, 10-40ms simulated latency                          |
| GET    | `/widgets/{id}` | one widget, or `404` if the id doesn't exist                      |
| POST   | `/widgets`      | creates a widget, 20-60ms latency, and a ~3% random `500` |

[`openapi.json`](./openapi.json) describes the same four operations, for the
`generate_from_spec` / `vegaload new -from-openapi` walkthrough.

## Running it

```
cd examples/sample-app
go run .                      # listens on 127.0.0.1:8080
go run . -addr :9090          # or pick your own address
```

Leave it running in one terminal, then drive load at it with `vegaload` from
another -- see [`../README.md`](../README.md) and
[`../scenarios/README.md`](../scenarios/README.md).
