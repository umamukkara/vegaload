---
title: Cookbook
description: Short recipes for common jobs.
sidebar_position: 1
---

# VegaLoad cookbook

Short recipes for common jobs. Each one has a goal, a command, and what to
look for. They run against [`../sample-app`](https://github.com/vegaload/vegaload/tree/main/examples/sample-app), so you can try
every one:

```
cd ../sample-app && go run .
```

Then, in another terminal, run the commands from the `examples/scenarios`
folder (so that the file names below are found), with `vegaload` built
(`go build ./cmd/vegaload` from the repository root).

A released binary does not include the [examples folder](https://github.com/vegaload/vegaload/tree/main/examples).

Flags always go before the scenario file: `vegaload run -vus 5 file.vl.js`.

1. [Check that an API works](/cookbook/check-an-api-works)
2. [Find out how many users it can take](/cookbook/find-how-many-users)
3. [Send a fixed number of requests per second](/cookbook/send-a-fixed-number-of-requests-per-second)
4. [Fail the build when the API is too slow](/cookbook/fail-the-build-when-the-api-is-too-slow)
5. [Stop a long run early when it is going badly](/cookbook/stop-a-long-run-early-when-it-is-going-badly)
6. [Check that a change made nothing worse](/cookbook/check-that-a-change-made-nothing-worse)
7. [Time each stage of a flow](/cookbook/time-each-stage-of-a-flow)
8. [Give every user different data](/cookbook/give-every-user-different-data)
9. [Keep a token out of the reports](/cookbook/keep-a-token-out-of-the-reports)
10. [Start from a browser recording](/cookbook/start-from-a-browser-recording)
11. [Start from an OpenAPI spec](/cookbook/start-from-an-openapi-spec)
12. [Understand a bad run](/cookbook/understand-a-bad-run)
13. [Let an AI agent run the tests](/cookbook/let-an-ai-agent-run-the-tests)
14. [Check your setup](/cookbook/check-your-setup)
15. [Block a slow pull request in GitHub Actions](/cookbook/block-a-slow-pull-request-in-github-actions)
