---
title: FAQ
description: Short answers to common questions about VegaLoad.
sidebar_position: 2
---

# FAQ

## What is the license?

Apache 2.0. The source is at [github.com/vegaload/vegaload](https://github.com/vegaload/vegaload).

## Do I need an AI agent to use VegaLoad?

No. VegaLoad is one binary with a normal command line. The agent path is optional. See [Your first test](../get-started/first-test.md).

## Which systems does it run on?

Linux, macOS and Windows. See [Install](../get-started/install.md) for the archives, Homebrew and Docker.

## Which languages can scenarios use?

JavaScript, TypeScript and Python. Python scenarios need Python 3 on the machine. Most protocol tests need no script at all: you give `-target` and `-protocol`. See [Scenarios](../guides/scenarios.md).

## What leaves my machine?

By default, nothing. Reports and the audit log (`~/.vegaload/audit.log`) are written locally. Two things can send data out:

- `VEGALOAD_LLM_PROVIDER` is optional. If you set it, `diagnose` sends the report data to the provider you chose.
- The GitHub Action downloads a VegaLoad release.

The load itself goes to the target you name. See [Safety](../ai-agents/safety.md).

## Can an agent run a test against any host?

Not without a check. A run against a host that is not localhost or allow-listed is refused unless it is allowed with `-allow-target` or `-yes`. Read [Safety](../ai-agents/safety.md) for what an agent can and cannot change.

## Can I run it in Docker or Kubernetes?

Docker, yes. A small image is published at `ghcr.io/vegaload/vegaload`. The image holds only the binary, so Python scenarios need a different base image. See [Install](../get-started/install.md). For Kubernetes, the repository has a Helm chart that runs a test as a Job.

## How does it compare with k6, JMeter, Locust and Gatling?

See [Compare](../compare/index.md). We say where each tool is the better choice.

## How do I scale a test across many machines?

See [Scale with Harness](../scale-with-harness.md).

## A run failed and I do not know why

Start with [Troubleshooting](./troubleshooting.md). Then try `vegaload diagnose`, or see [Explain a run](../guides/explain-a-run.md).
