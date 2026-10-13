---
title: Moving to VegaLoad
description: How to move a test from k6, JMeter, Locust or Gatling to VegaLoad by hand today, which concepts match, and what is planned.
sidebar_position: 7
---

# Moving to VegaLoad

**Checked on 13 October 2026, against VegaLoad 0.9.0.**

You can move a test to VegaLoad by hand today. There is no automatic import from another load testing tool yet.

:::note Planned
`vegaload import jmeter` and a k6 import are planned. They are not released. When they ship, this page will say so.
:::

## Start from what you have

VegaLoad can make a first scenario from two things you may already have.

**An OpenAPI spec**

```bash
vegaload new -from-openapi openapi.json my-service
```

This writes a runnable scenario that calls every operation in the spec, and a runbook with one command per endpoint. The order of calls is a guess, and the file says so. Check it, then edit it to match your real flow.

**A browser recording (HAR)**

```bash
vegaload import har recording.har
```

This turns a recording from your browser's network tab into a scenario file.

## Then check it works

```bash
vegaload validate my-service.vl.js
```

This runs the scenario once, with one user, and tells you if it works. It makes real network calls, under the same host rules as `vegaload run`.

## What matches

| You wrote this | In VegaLoad |
| --- | --- |
| k6 `vus` and `duration` | `-vus 10 -duration 30s` |
| k6 `thresholds: { http_req_duration: ['p(95)<500'] }` | `-threshold 'p95 < 500ms'` |
| Locust `-u 10 -t 30s --headless` | `-vus 10 -duration 30s` |
| A k6 or Locust test for one endpoint | `vegaload run -target URL -protocol http1`, no file |
| JMeter `-e -o report` HTML dashboard | A self-contained HTML report, written by default |
| A test that runs in CI and fails the build | `-threshold`, and the exit code 3 |
| A JMeter plan or a Gatling simulation with many steps | A scenario file in JavaScript, TypeScript or Python |

## What does not carry over

- **Script code.** k6, Locust, JMeter and Gatling each have their own API. A VegaLoad scenario has its own. You rewrite the steps. You can ask your agent to do it, and then run `vegaload validate`.
- **Extensions and plugins.** VegaLoad has 14 protocols built in. It has no plugin system for new ones.
- **Browser tests.** VegaLoad does not drive a browser.
- **Distributed runs.** VegaLoad has no distributed runner yet. The Helm chart runs one test as a Kubernetes Job.

## One thing to know about exit codes

A run where every call fails still exits 0 unless you set a limit. This is the same for a k6 run without thresholds. Locust is different: it exits 1 if a request failed.

Add a limit on failures to every test that runs in CI:

```bash
vegaload run -target https://staging.example.com/health -protocol http1 \
  -vus 10 -duration 30s \
  -threshold 'error_rate < 0.01' -threshold 'p95 < 500ms' \
  -allow-target staging.example.com
```

A breached limit exits 3.

## Related

- [Compare VegaLoad](./index.md)
- [Install VegaLoad](../get-started/install.md)
