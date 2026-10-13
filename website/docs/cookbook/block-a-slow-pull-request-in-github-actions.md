---
title: "Block a slow pull request in GitHub Actions"
description: "Run a short load test on every pull request."
sidebar_position: 16
---

# Block a slow pull request in GitHub Actions

Run a short load test on every pull request. When a limit breaks, the check
fails, and the pull request cannot be merged until it is fixed.

Add `.github/workflows/load-gate.yml` to your repository. Replace the start
command and the URL with your own:

```yaml
name: Load gate

on: pull_request

jobs:
  load:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Start the service
        run: |
          nohup ./start-service.sh > service.log 2>&1 &
          for i in $(seq 1 30); do
            curl -sf http://127.0.0.1:8080/health && exit 0
            sleep 1
          done
          echo "The service did not start"; cat service.log; exit 1

      - uses: vegaload/vegaload@v0.9.0
        with:
          args: >-
            run -target http://127.0.0.1:8080/widgets -protocol http1
            -vus 10 -duration 30s
            -threshold "p95 < 300ms" -threshold "error_rate < 1%"
            -junit vegaload-junit.xml

      - uses: actions/upload-artifact@v4
        if: always()
        with:
          name: vegaload-results
          path: |
            vegaload-junit.xml
            vegaload-report-*.html
```

What happens:

1. The job starts your service on the runner and waits until it answers.
2. VegaLoad runs for 30 seconds. If p95 is above 300 ms, or more than 1 in
   100 requests fail, it exits with code 3 and the step turns red.
3. The job page shows a table with the verdict of each limit. VegaLoad adds it
   on its own inside GitHub Actions. The HTML report is saved as an artifact.

To make it a real gate, tell GitHub that this check is required. In the
repository, open Settings, then Branches (or Rules), and add a rule for your
main branch that requires the status check `load`. Without this rule, a red
check can still be merged.

Tips:

- The service runs on the runner itself, so the target is localhost and needs
  no approval. If you test a shared staging URL instead, add
  `-allow-target` with its host name to `args`, because nobody can answer the
  question in a job.
- Shared runners are not all the same speed. Start with a loose limit, for
  example twice your usual p95. Run the job a few times, look at the spread,
  and tighten the limit later. A limit that fails by chance makes people stop
  trusting the check.
- To also catch a change that makes things slower than before, add `-baseline`
  and `-max-regression` as in [recipe 6](/cookbook/check-that-a-change-made-nothing-worse).
  VegaLoad keeps no baseline for you, so the job must have the baseline file,
  for example one you keep in the repository.
- The action runs on Linux and macOS runners.
