---
title: Why VegaLoad
description: Why VegaLoad exists. Load tests become a file beside your code, and your agent can write and run them.
sidebar_position: 1
---

# Why VegaLoad

## The problem

Most teams know they should load test. Few do it often. The reasons are simple.

- The load tool is separate from the code. It has its own project, its own scripts, and its own owner.
- A load test is a ticket in someone's queue. It runs after the release, not before.
- The tests drift. The API changes. The script does not.
- Reading the results needs a person who knows the tool.

## The idea

A load test should be a file in the same repo as the service it tests. It should change in the same pull request. It should run in CI. And your coding agent should be able to write and run it for you.

VegaLoad is built for that idea. It is one binary. It has a command line, a small scenario format, and a server that agents can talk to.

## Three things you can check

**A file beside the code.** A scenario is a JavaScript, TypeScript or Python file. Put `checkout.load.js` next to `checkout.js`. It is reviewed like any other file. See [Your first test](/get-started/first-test).

**An agent that can run it, safely.** `vegaload init` gives Cursor and Claude Code nine tools. The agent can create a scenario, check it, run it, and read the report. Every tool calls the same command line that you can run by hand. A host that is not localhost needs approval first. See [How it works](/ai-agents/how-it-works) and [Safety](/ai-agents/safety).

**A pass or fail gate.** Add `-threshold 'p95 < 500ms'`. If the run is slower, the command exits 3. CI stops the pull request. Add `-baseline` to catch a run that is worse than a good one you saved.

## What a run looks like

```bash
vegaload run \
  -target https://staging.example.com/health -protocol http1 \
  -vus 10 -duration 30s \
  -threshold 'p95 < 500ms' \
  -allow-target staging.example.com
```

This needs no scenario file. VegaLoad prints a summary, and writes one HTML report that opens offline.

## Where VegaLoad is not the best choice

Be honest about the fit. VegaLoad is probably not the right tool if:

- You need browser tests at scale. k6 has a browser module.
- You have a large JMeter or Gatling suite that works well, and nobody wants to move it.
- You need a cluster of load generators today. Look at Harness Resilience Testing, or a tool that already has a hosted service.

The [Compare](/compare) pages go through each tool in detail.

## What comes next

VegaLoad works on your laptop and in CI today. For distributed load and shared history, Harness Resilience Testing is the next step. A command to move a test there is planned. It is not released.
