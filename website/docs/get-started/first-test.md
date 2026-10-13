---
title: Your first test
description: Run a load test from the command line and open the report.
sidebar_position: 2
---

Everything below uses [`examples/sample-app`](https://github.com/vegaload/vegaload/tree/main/examples/sample-app), a
small widgets service with injected latency and a ~3% failure rate on
creates, served over HTTP/1.1, HTTP/2, WebSocket, and gRPC, built
specifically so this walkthrough has something real to point `vegaload` at.
Start it in its own terminal first:

```
cd examples/sample-app
go run .
```

Leave it running, and do the rest from a second terminal at the repository
root.

### 1. Run a load test, no scenario file needed

Protocol-direct mode drives a target straight from CLI flags — useful for a
quick check, or for an agent that just got a URL and doesn't need to write a
script first:

```
./vegaload run -target http://127.0.0.1:8080/widgets -protocol http1 -vus 10 -duration 30s
```

This runs 10 virtual users against the sample app for 30 seconds, prints a
summary, and writes (and opens) a self-contained HTML report — one file,
with the full latency distribution and a requests/errors-over-time chart,
nothing else to host.
