# scenarios

Ready-to-run examples against [`../sample-app`](../sample-app), plus two
scripted scenario files. Start the sample app first:

```
cd ../sample-app && go run .
```

Then, from the repository root, with `vegaload` built (`go build ./cmd/vegaload`
or `go install ./cmd/vegaload`):

## 1. A protocol-direct baseline run

No scenario file needed -- `-target`/`-protocol` drive the sample app's
`/widgets` endpoint directly:

```
vegaload run -target http://127.0.0.1:8080/widgets -protocol http1 -vus 10 -duration 30s
```

This writes a self-contained HTML report (and opens it) by default. Keep the
JSON summary too, for the next two steps:

```
vegaload run -target http://127.0.0.1:8080/widgets -protocol http1 \
  -vus 10 -duration 30s -out baseline.json
```

### The same widgets over the other protocols

The sample app serves the same data over every protocol VegaLoad speaks
(see [`../sample-app/README.md`](../sample-app/README.md) for each
endpoint's behavior):

```
# HTTP/2, over plain TCP (h2c) on the same port
vegaload run -target http://127.0.0.1:8080/widgets -protocol http2 -vus 10 -duration 30s

# WebSocket: connect, send one message, wait for its echo
vegaload run -target ws://127.0.0.1:8080/ws/echo -protocol websocket -body hello -vus 10 -duration 30s

# gRPC: the standard health check, then the widget service
vegaload run -target 127.0.0.1:9090 -protocol grpc \
  -method /grpc.health.v1.Health/Check -vus 10 -duration 30s
vegaload run -target 127.0.0.1:9090 -protocol grpc \
  -method /widgets.v1.WidgetService/GetWidget -body $'\x08\x02' -vus 10 -duration 30s

# gRPC create, with the same ~3% injected failure as POST /widgets
vegaload run -target 127.0.0.1:9090 -protocol grpc \
  -method /widgets.v1.WidgetService/CreateWidget -body $'\x0a\x06flange' -vus 10 -duration 30s
```

A gRPC `-body` is already-encoded protobuf (`$'...'` is bash/zsh syntax for
those bytes); the sample app's README explains how the two above are built.

## 2. Explain the results

```
vegaload diagnose baseline.json
```

With the sample app's injected latency and ~3% POST failure rate, a run
against `/widgets` (GET-only, so no failures) is a clean baseline; try it
against `-method POST` to see `diagnose` actually have something to flag.

## 3. Compare a later run against the baseline

Keep the baseline JSON from step 1, run again after a change, then diff:

```
vegaload run -target http://127.0.0.1:8080/widgets -protocol http1 \
  -vus 10 -duration 30s -out candidate.json
vegaload compare baseline.json candidate.json
```

Exit code is non-zero when error rate or p95 latency got worse (strict by
default). Optional slack for CI noise:

```
vegaload compare -error-rate-delta 0.01 -p95-ratio 1.2 baseline.json candidate.json
```

Without running the sample app, the checked-in fixtures under
[`compare/`](./compare/) show both outcomes:

```
# ok — candidate is no worse than baseline (exit 0)
vegaload compare compare/baseline.json compare/candidate-ok.json

# regressed — higher error rate and p95 (exit 1)
vegaload compare compare/baseline.json compare/candidate-regressed.json
```

## Gate a run on pass/fail thresholds

`-threshold` makes a run fail on its own numbers, which is what a CI step
needs. Exit code 3 means the run finished but broke a threshold:

```
vegaload run -target http://127.0.0.1:8080/widgets -protocol http1 \
  -vus 10 -duration 30s \
  -threshold "p95 < 300ms" -threshold "error_rate < 1%"
echo $?   # 0 if both held, 3 if either broke
```

Let a baseline set the bar, then reuse it on later runs:

```
vegaload diagnose -no-llm -output json baseline.json > gate.json
vegaload run -target http://127.0.0.1:8080/widgets -protocol http1 \
  -vus 10 -duration 30s -thresholds gate.json
```

A CI job is the same command; the build fails when the exit code is not 0:

```
- run: vegaload run -target "$TARGET" -protocol http1 -vus 20 -duration 1m -threshold "p95 < 300ms" -threshold "error_rate < 1%"
```

## 4. Generate a runbook from an OpenAPI spec

`generate_from_spec` (the MCP tool) and `vegaload new -from-openapi` (the CLI
command behind it) are the same thing -- a Markdown runbook of one
ready-to-run command per endpoint, not a chained scenario (an OpenAPI spec
describes each endpoint on its own, not how their responses should feed
into each other -- see `internal/openapi`'s doc comment):

```
vegaload new -from-openapi ../sample-app/openapi.json sample-app
```

This writes `sample-app.vegaload-plan.md` with a `vegaload run` command for
each of `/health`, `/widgets` (GET and POST), and `/widgets/{id}`.

## 5. The scripted scenario

`smoke.vl.js` is here as the "a test is a real file" example (see
`AGENTS.md`), not a load test of the sample app -- it exercises VegaLoad's VU
pool and executor shapes against plain JS without making any network call:

```
vegaload run -vus 5 -duration 5s smoke.vl.js
```

`http-ws-chain.vl.js` (and its Python twin, `http_ws_chain.py`) is the real
FR-CLI-08 shape: a scenario's `http`/`ws` globals make real calls against the
sample app, carrying a value from one into the next -- here, a widget's
freshly-created id from a `POST /widgets` call into a `/ws/echo` WebSocket
message:

```
vegaload run -vus 5 -duration 10s http-ws-chain.vl.js
vegaload run -vus 5 -duration 10s http_ws_chain.py   # same flow, Python
```

(flags before the scenario file -- see `vegaload run -h`)

## More scenario flows

`crud-flow.vl.js` (and its Python twin, `crud_flow.py`) is a session in one
scenario: create a widget, read it back by the id the create returned, then
list all widgets and check the new one is in the list. Each step uses what
the one before it returned, which a single fixed `-target` cannot do:

```
vegaload run -vus 5 -duration 10s crud-flow.vl.js
vegaload run -vus 5 -duration 10s crud_flow.py   # same flow, Python
```

The sample app fails about 3% of creates on purpose, so expect a failed
iteration or two per hundred. A much higher failure rate means a step in
the flow is really broken.

## Load shapes

Everything above uses the default shape, a fixed number of virtual users
(`-vus`) for a fixed time (`-duration`). `-executor` picks another one. The
commands below use the sample app's `/widgets` endpoint, and every one of
them also works with a scenario file in place of `-target`/`-protocol`:

```
# Ramp: climb to 10 users over 30s, to 20 over the next 30s, then drain
vegaload run -target http://127.0.0.1:8080/widgets -protocol http1 \
  -executor ramp -vus 10 -stages 10:30s,20:30s,0:10s

# Step: hold 5 users for 30s, jump to 10 for 30s, then to 20 for 30s
vegaload run -target http://127.0.0.1:8080/widgets -protocol http1 \
  -executor step -vus 10 -stages 5:30s,10:30s,20:30s

# Constant arrival rate: start 50 iterations every second, using as many
# users as it takes, up to -max-vus
vegaload run -target http://127.0.0.1:8080/widgets -protocol http1 \
  -executor constant-arrival-rate -rate 50 -max-vus 100 -duration 30s
```

Use a fixed or ramped number of users to ask "how does it behave with this
many people?". Use a constant arrival rate to ask "can it keep up with this
many requests a second?": if the server slows down, VegaLoad keeps arriving
at the same rate instead of waiting, so a slow server shows up as growing
latency and dropped arrivals, not as a lower request rate.

## 6. Do all of this from an agent instead

```
vegaload init
```

registers this same CLI as an MCP server (and a Claude Code / Cursor skill
bundle) for the project you run it in, so an agent can call `run_test`,
`diagnose_failure`, `compare_reports`, `generate_from_spec`, and the rest of
[FR-MCP-03's tool set](../../AGENTS.md) directly -- each one shelling out to
the exact commands above. See the top-level `README.md` for the full
walkthrough.
