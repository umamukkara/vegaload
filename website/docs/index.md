---
title: Load testing for agentic developers.
description: Ask your agent for a load test. It writes the file next to your code and runs it with VegaLoad.
slug: /
---

# Load testing for agentic developers.

Ask your agent for a load test. It writes the file next to your code and runs it with VegaLoad.

```bash
brew install vegaload/tap/vegaload
```

[Install](/get-started/install) · [Your first test](/get-started/first-test) · [Use it with your agent](/get-started/cursor)

## The shift

Today, load testing is often spread across three places. There is a separate load tool. The scripts live in another repo. A ticket waits in someone's queue.

With VegaLoad, a test is one file next to your code. Your agent writes it, checks it, and runs it. You review it in the same pull request as the change it tests.

## What you get

**Tests beside the code.** A test is a plain JavaScript, TypeScript or Python file. It lives next to the service it tests. It is reviewed in the same pull request. It runs in CI.

**Setup once, then ask in plain words.** Install VegaLoad. Run `vegaload init` in your project. Your agent then has nine tools and a short guide for them. It can write a scenario, check it, run it and read the report.

```bash
brew install vegaload/tap/vegaload
vegaload init
```

**Built for production.** 14 protocols in one binary. Pass or fail limits that stop a pull request. Baseline compare. One HTML report per run that opens offline. [See all features](/features).

**Safe by default.** Localhost is always allowed. Any other host needs your approval. Every run is written to an audit log on your machine. [Read how the guardrails work](/ai-agents/safety).

**One binary, no runtime.** No JVM. No Node. It runs on macOS, Linux and Windows, in Docker and in Kubernetes.

## Choosing a tool?

VegaLoad is not the only good load testing tool. [Compare VegaLoad](/compare) with k6, JMeter, Locust, Gatling and simple HTTP tools. The pages say where each one is the better choice.

## Open source

VegaLoad is open source under the Apache License 2.0. It is sponsored by Harness. When you need distributed load, shared history and chaos probes across a team, [Harness Resilience Testing](https://www.harness.io/products/resilience-testing) is the next step. A one-command move from VegaLoad to Harness is planned. It is not released yet.
