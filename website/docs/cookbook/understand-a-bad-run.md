---
title: "Understand a bad run"
description: "`diagnose` lists findings in plain words: how many requests failed, whether latency has a long tail, and whether the failures were one burst or spread over the whole run."
sidebar_position: 13
---

# Understand a bad run

```
vegaload run -target http://127.0.0.1:8080/widgets -protocol http1 \
  -vus 10 -duration 20s -method POST -body '{"name":"x"}' -out bad.json
vegaload diagnose bad.json
```

`diagnose` lists findings in plain words: how many requests failed, whether
latency has a long tail, and whether the failures were one burst or spread
over the whole run. To get ready-made thresholds from a healthy run, use
`vegaload diagnose -output json baseline.json > gate.json`, then
`-thresholds gate.json` on later runs.
