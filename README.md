# VegaLoad

VegaLoad is a thin, open-source load testing tool: a single static binary
with a scriptable core engine, four protocol drivers (HTTP/1.1, HTTP/2,
gRPC, WebSocket), and a self-contained HTML report — no server, no account,
no telemetry.

It's also agent-native: `vegaload init` registers VegaLoad as an MCP server
for Claude Code, Cursor, or any other MCP-capable agent host, so an agent
can run tests, read results, and explain failures through the same CLI
commands a human would type. It is never agent-*mandatory* — every feature
works from a plain terminal with nothing else installed. See `AGENTS.md` for
the design principle behind that split.

## Status

Phase 0 (core engine, CLI, protocols, scripting) and Phase 1 (the HTML/JSON
report) are done. Phase 2 (the MCP server, skill bundles, and a versioned
eval suite for the MCP tools) is also done — this README's walkthrough
covers all three. A Harness RT bridge (`--move-to-harness`) is intentionally
out of scope for now.

## Install

With Homebrew (macOS and Linux), after the first tagged GitHub release:

```
brew install vegaload/tap/vegaload
vegaload version
```

`brew tap vegaload/tap` followed by `brew install vegaload` does the same
thing. Upgrade with `brew upgrade vegaload`. Until a tag exists, build from
source below.

Or build from source (Go 1.22+):

```
git clone https://github.com/vegaload/vegaload
cd vegaload
go build -o vegaload ./cmd/vegaload
./vegaload version
```

`go install github.com/vegaload/vegaload/cmd/vegaload@latest` also works
once the repository is public. Release archives for Linux, macOS and
Windows are attached to each [GitHub release](https://github.com/vegaload/vegaload/releases).
A scratch-based Docker image builds from the
included `Dockerfile` (`docker build .`); it carries nothing but the binary
and CA certificates, so Python-scripted scenarios (which shell out to a
local `python3`) need a different base image — see the `Dockerfile`'s
comment.

## Getting started

Everything below uses [`examples/sample-app`](./examples/sample-app), a
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

### 2. Or write a scenario file

```
./vegaload new my-scenario
```

scaffolds `my-scenario.vl.js` (JavaScript by default; `-python` for a
`.py` scenario shelling out to a local `python3`). A scenario's `http` and
`ws` globals make real HTTP and WebSocket calls, carrying a value from one
into the next — a script can create something over HTTP, then open a
WebSocket and read frames until it's done, the way
[`examples/scenarios/http-ws-chain.vl.js`](./examples/scenarios/http-ws-chain.vl.js)
does against the sample app (its Python twin,
[`http_ws_chain.py`](./examples/scenarios/http_ws_chain.py), does the same
thing):

```
./vegaload run -vus 5 -duration 10s examples/scenarios/http-ws-chain.vl.js
```

(flags before the scenario file — `vegaload`'s flag parser stops at the
first non-flag argument; run `vegaload run -h` for the full flag list.) A
host outside localhost needs `-allow-target` or `-yes` — the same FR-CLI-06
allowlist protocol-direct mode's `-target` uses, just enforced per call
since a script's own targets aren't known until it runs. A scenario with no
network calls at all is still useful for pure per-iteration logic against
VegaLoad's VU pool and executor shapes (fixed-VU, ramp, step,
constant-arrival-rate) —
[`examples/scenarios/smoke.vl.js`](./examples/scenarios/smoke.vl.js) is a
minimal one:

```
./vegaload run -vus 5 -duration 5s examples/scenarios/smoke.vl.js
```

To load test an actual HTTP target without writing a script at all, use
protocol-direct mode (step 1) instead — or generate a runbook of
protocol-direct commands from an OpenAPI spec:

```
./vegaload new -from-openapi examples/sample-app/openapi.json sample-app
```

writes `sample-app.vegaload-plan.md`: one ready-to-run `vegaload run`
command per endpoint the spec declares.

### 3. Explain a run's results

Keep a run's JSON summary alongside its HTML report with `-out`:

```
./vegaload run -target http://127.0.0.1:8080/widgets -protocol http1 \
  -vus 10 -duration 30s -out baseline.json
./vegaload diagnose baseline.json
```

`diagnose` reports the failure rate, flags a latency long tail (p99 more
than 5x p50), and says whether failures were concentrated in one period or
spread out — plus suggested pass/fail thresholds (a p95 ceiling, an error
rate ceiling) derived from that same run, for writing into a CI gate. Add a
bring-your-own-LLM connector for a plain-English narrative on top of the
rule-based findings:

```
export VEGALOAD_LLM_PROVIDER=openai       # or anthropic, ollama
export VEGALOAD_LLM_API_KEY=sk-...
./vegaload diagnose baseline.json
```

Nothing about a run or its results leaves your machine unless you configure
this yourself (`-no-llm` skips narration even if it's configured).

### 4. Compare a later run against the baseline

After another run with `-out candidate.json`:

```
./vegaload compare baseline.json candidate.json
```

Prints deltas (error rate, latency percentiles, totals, overall RPS) and
exits non-zero if error rate or p95 latency got worse. Optional slack:

```
./vegaload compare -error-rate-delta 0.01 -p95-ratio 1.2 \
  baseline.json candidate.json
```

Checked-in fixtures under `examples/scenarios/compare/` show both an ok and
a regressed pair without needing a live target.

### Gate a run on pass/fail thresholds

Add `-threshold` to make a run pass or fail on its own numbers, for example
in CI:

```
./vegaload run -target http://127.0.0.1:8080/widgets -protocol http1 \
  -vus 10 -duration 30s \
  -threshold "p95 < 300ms" \
  -threshold "api stays up: error_rate < 1%"
```

Each threshold is an optional `name:`, a metric, an operator (`<`, `<=`,
`>`, `>=`) and a value. The metrics are `p50`, `p90`, `p95`, `p99`, `mean`,
`min`, `max` (durations such as `300ms`), `error_rate` (a fraction like
`0.01` or a percentage like `1%`), `rps`, `failed` and `total`. Every
threshold is judged once, on the finished run. A run that completed no
requests fails all of them.

The text summary, the JSON output, the HTML report and the audit log all
show each threshold's verdict. The exit code tells a script what happened:

| Exit code | Meaning                                                  |
|-----------|----------------------------------------------------------|
| 0         | The run finished and every threshold passed (or none set) |
| 1         | The run itself failed                                    |
| 2         | Bad usage, such as a threshold that cannot be parsed     |
| 3         | The run finished but broke at least one threshold        |

Thresholds can also come from a file, with `-thresholds gate.json`. The file
is a JSON list of `{"name", "metric", "operator", "value"}`, or the output of
`vegaload diagnose -output json`, so a baseline run can set the bar for the
next ones:

```
./vegaload diagnose -no-llm -output json baseline.json > gate.json
./vegaload run -target http://127.0.0.1:8080/widgets -protocol http1 \
  -vus 10 -duration 30s -thresholds gate.json
```

`diagnose` also prints the same suggestion as ready-to-paste `-threshold`
flags. From an agent, `run_test` takes a `thresholds` list; a breach comes
back as a normal result with `thresholds_passed: false`.

### 5. Hand all of this to an agent

```
./vegaload init
```

writes four things into the current project, each skippable if already
present: a Claude Code skill bundle (`.claude/skills/vegaload`), a Cursor
rules file (`.cursor/rules/vegaload.mdc`), and an MCP server entry merged
into both `.mcp.json` and `.cursor/mcp.json`, pointing at this same compiled
binary running `vegaload mcp serve`. Open the project in Claude Code or
Cursor afterward and the agent has seven tools — `create_scenario`,
`run_test`, `get_results`, `suggest_thresholds`, `diagnose_failure`,
`compare_reports`, `generate_from_spec` — each one calling the exact CLI
command shown above and parsing its `-output json` result; there is no
agent-only path that skips the CLI.

`vegaload mcp eval` runs a versioned, non-LLM suite of {tool call, expected
outcome} cases against those same tools directly — the thing to run in
CI after upgrading, to check the tool layer itself still behaves, independent
of any model's tool-picking behavior:

```
./vegaload mcp eval
```

### 5. Check that everything is wired up

```
./vegaload doctor
```

checks that VegaLoad works from here and says how to fix anything that does
not. It looks at the CLI itself (version, `PATH`, writable folders), at each
agent host it finds (Cursor, Claude Code, and Claude Desktop on macOS), and
optionally at a target. For a host that is installed but has no VegaLoad
MCP entry, that is a warning, not a failure — using the CLI alone is
healthy. When VegaLoad *is* registered, doctor starts the configured
server, performs a real MCP handshake, and expects all seven tools. An
installed editor with no VegaLoad setup does not fail the command.

```
./vegaload doctor -target http://localhost:8080   # also check a target
./vegaload doctor -fix                            # repair what can be repaired safely
./vegaload doctor -fix -dry-run                   # show what -fix would change
./vegaload doctor -output json                    # for CI; exits 1 if any check fails
```

`-fix` only adds missing MCP entries and rules files in the project, and
rewrites a stale server command. It never overwrites a rules file you edited,
and it changes files in your home directory only when you name the host, as in
`-host cursor`. `-smoke` adds a one-user, one-second test against `-target`
(it sends real traffic, so it is off by default). `-harness` is a placeholder
until `--move-to-harness` ships: it makes no network call and cannot verify
credentials.

See [`examples/scenarios/README.md`](./examples/scenarios/README.md) for
this same walkthrough as a standalone, copy-pasteable script.

## Command reference

| Command                 | What it does                                                          |
|--------------------------|------------------------------------------------------------------------|
| `vegaload run`           | Run a load test: a scenario file, or a protocol-direct target         |
| `vegaload new`           | Scaffold a starter scenario file, or a runbook from an OpenAPI spec    |
| `vegaload diagnose`      | Print environment info, or explain a report's results                |
| `vegaload mcp serve`     | Run an MCP server over stdio for agent-native use                     |
| `vegaload mcp eval`      | Run the versioned MCP tool-calling eval suite against this binary     |
| `vegaload init`          | Register the MCP server and skill bundles for the current project     |
| `vegaload doctor`        | Check the CLI, agent hosts, and a target; `-fix` repairs what it can   |

Every command supports `-output text` (default), `json`, or (`run` only)
`jsonl`. Run `vegaload <command> -h` for its full flag list, or `vegaload
help` for the top-level summary.

## Use in GitHub Actions

This repository is also a GitHub Action. It installs a released VegaLoad
(Linux and macOS runners) and runs it, and the step fails when `vegaload`
exits with a non-zero code. With `-threshold`, that means a broken
threshold fails the build:

```yaml
- uses: vegaload/vegaload@v0.3.0
  with:
    args: >-
      run -target https://staging.example.com/health -protocol http1
      -vus 20 -duration 1m -allow-target staging.example.com
      -threshold "p95 < 300ms" -threshold "error_rate < 1%"
```

- `args` is what you would type after `vegaload`, with the same quoting.
  Leave it out to only install, then call `vegaload` in later steps.
- The action installs the version you pin after the `@`. Set `version:` to
  install a different one. A branch name such as `@main` installs the
  latest release.
- The archive is checked against the release's `checksums.txt` before it
  is used. Nothing is sent anywhere except the downloads from the GitHub
  release.
- The step's `version` output is the installed version.

## License

Apache-2.0. See `LICENSE`.
