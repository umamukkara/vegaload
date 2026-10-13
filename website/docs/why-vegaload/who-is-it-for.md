---
title: Who it is for
description: Who gets the most from VegaLoad, and who should use something else.
sidebar_position: 2
---

# Who it is for

## VegaLoad is a good fit if

- You build services, and you use an AI coding agent such as Cursor or Claude Code.
- You want load tests in the same repo and the same pull request as the code.
- You test more than HTTP: databases, queues, files, sockets. VegaLoad has 14 protocols in one binary.
- You want a pass or fail result in CI, with no server to run.
- You want a tool you can read and change. It is Apache 2.0 licensed.

## VegaLoad is not the best fit if

- You need real browsers, with page rendering, at scale.
- Your team has a large suite in JMeter, Gatling, k6 or Locust that works well. Moving it costs time, and the importers for JMeter and k6 are planned, not released.
- You need a cluster of load generators today.

## What each person sees

**A developer** asks the agent: "Write a load test for the checkout API and run it." The test file appears next to the code.

**A reviewer** sees the test in the pull request, with the limits it must meet.

**A platform or SRE engineer** adds a threshold to CI, and gets an exit code and a report on every run.

**A team that grows** moves to Harness Resilience Testing for shared history, distributed load and chaos probes.

Not sure? Read [Compare VegaLoad](/compare), or go to [Install](/get-started/install) and try it. It takes a few minutes.
