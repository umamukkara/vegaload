---
title: Prompts that work
description: Plain-words prompts to give your agent for common load testing jobs with VegaLoad.
sidebar_position: 4
---

# Prompts that work

You do not need special words. Say what you want to test, and where. These prompts are examples. Change the names to yours.

Name VegaLoad if the agent is not using it. "Use VegaLoad to ..." works.

## Check an endpoint quickly

> Smoke test http://localhost:8080/health with VegaLoad.

The agent uses the `vegaload-smoke` guide: at most 5 users and 10 seconds.

## Write a test next to the code

> Write a load test for the checkout API and run it against localhost:8080. Put the file next to checkout.js.

## Start from an OpenAPI file

> Generate a load test from openapi.json. Validate it, fix what fails, and run it for 30 seconds with 10 users.

The spec must be JSON. For YAML, ask the agent to convert it first.

## Add a pass or fail limit

> Run the checkout test and fail it if p95 goes above 300 ms or errors go above 1%.

The agent sets `thresholds`. A breach gives `thresholds_passed: false`, and exit code 3 from the command line.

## Choose limits from a good run

> Run the checkout test once. Suggest thresholds from that report.

The agent calls `suggest_thresholds` on the report.

## Compare with a baseline

> Run the checkout test again and compare it with the baseline report in reports/baseline.json. Did anything get worse?

## Explain a bad run

> The last run had errors. Diagnose it and tell me what to check first.

The agent calls `diagnose_failure`. It reads rule-based findings: failure rate, a long latency tail, and whether failures came in a burst or were spread out.

## Test something that is not HTTP

> List the protocols VegaLoad supports. Then run a 20-second test against the Redis server on localhost:6379.

## Test a host that is not localhost

> Run a 10-second test against https://staging.example.com/health. Ask me before you send anything.

The agent should stop and ask you. Approve with a host in `allow_targets`, not a blanket `yes`.

## Put it in CI

> Add a GitHub Action step that runs the checkout load test with the p95 threshold and uploads the report.

For the Action itself, see the [GitHub Actions section of the README](https://github.com/vegaload/vegaload#use-in-github-actions).
