---
title: "4. Compare a later run against the baseline"
description: "After another run with `-out candidate.json`:"
sidebar_position: 17
---

# 4. Compare a later run against the baseline

After another run with `-out candidate.json`:

```
vegaload compare baseline.json candidate.json
```

Prints deltas (error rate, latency percentiles, totals, overall RPS) and
exits non-zero if error rate or p95 latency got worse. If the scenario makes
`check()` calls, it also lists each check's pass rate in both runs, and a
check whose pass rate fell counts as a regression too. A check that only one
run made is shown as `new` or `removed`, and does not fail the comparison.
The `check_rate` row is the plain average of the shared checks. It is for
reading and never fails the comparison on its own.
Optional slack:

```
vegaload compare -error-rate-delta 0.01 -p95-ratio 1.2 -check-rate-delta 0.02 \
  baseline.json candidate.json
```

Checked-in fixtures under `examples/scenarios/compare/` show an ok and a
regressed pair, with and without checks, without needing a live target.

Or do it in one command, with the baseline as a file you keep:

```
vegaload run -target http://127.0.0.1:8080/widgets -protocol http1 \
  -vus 10 -duration 30s -baseline baseline.json -max-regression 10
```

The run exits 3 if p95 latency, or the error rate, is worse than the
baseline by more than 10 percent. It uses the same comparison as
`vegaload compare`. The error rate may rise by that percent of the
baseline's error rate, so a baseline with no errors allows none. Use a
`-threshold "error_rate < 1%"` for an absolute limit. The `-baseline` gate does not look at checks; use
`-threshold "check_rate >= 99%"` for those. Without
`-max-regression`, any increase fails. VegaLoad keeps no baseline store.
The baseline is a file you supply, from `-out`. The summary, JSON, HTML
report and audit log show the verdict. From an agent, `run_test` takes
`baseline_path` and `max_regression`.
