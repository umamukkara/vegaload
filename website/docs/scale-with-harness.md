---
title: Scale with Harness
description: How VegaLoad grows from your laptop to CI and CD, and then to Harness Resilience Testing. What works today and what is planned.
sidebar_position: 5
---

# Scale with Harness

VegaLoad is open source and sponsored by Harness. It is built to start small and grow in three steps.

## The ladder

**1. Your laptop and your agent.** You install one binary. You ask your agent for a test, or you write one. It runs on your machine and writes a report. This works today. See [Get started](/get-started/install).

**2. CI and CD.** The same test runs in your pipeline, in the same repo. A threshold that fails exits with code 3, so the pull request or the rollout stops. This works today.

**3. Harness Resilience Testing.** When one machine is not enough, or when a team needs one place for results, you move to Harness Resilience Testing. It adds distributed load, shared history and chaos probes. VegaLoad is a way to start. The connection between the two is planned. It is not released.

## What works today

| What | Status |
| --- | --- |
| Run a test on your machine, with or without an agent | Available |
| 14 protocols in one binary | Available |
| Pass or fail limits, with exit code 3 | Available |
| Compare with a saved baseline | Available |
| JUnit XML and a GitHub Actions job summary | Available |
| A container image for Linux amd64 and arm64 | Available |
| A Helm chart that runs a test as a Kubernetes Job | Available |
| A GitHub Action | Available |
| Run in any pipeline that can run a command or a container | Available |

VegaLoad is one binary with plain exit codes. Any CI or CD system that can run a command can use it. See [Read the report](/get-started/read-the-report) for the exit codes and files.

## What is planned

These are not released. Do not build on them yet.

| What | Status |
| --- | --- |
| `vegaload --move-to-harness`: move a test to Harness in one command | Planned |
| Upload a run's results to Harness | Planned |
| Import a JMeter plan or a k6 script into a VegaLoad scenario | Planned |

`vegaload doctor -harness` is a placeholder today. It makes no network call and cannot check any credentials.

## When to move up

Stay on your laptop and in CI while one machine can make the load you need, and while a report per run is enough.

Look at Harness Resilience Testing when:

- You need load from many machines at once.
- A team needs one place to see results over time.
- You want to test how a service behaves under load and under failure together, with chaos probes.

[Harness Resilience Testing](https://www.harness.io/products/resilience-testing) is a separate product. [Its load testing](https://www.harness.io/products/resilience-testing/load-testing) page says what it offers today.

## Keep your tests

Your tests stay plain files in your repo. They do not depend on Harness. If you never move, nothing changes. This page will say more when the connection ships.
