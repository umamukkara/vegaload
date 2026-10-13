---
title: "Fail the build when the API is too slow"
description: "A threshold is a pass or fail rule."
sidebar_position: 5
---

# Fail the build when the API is too slow

A threshold is a pass or fail rule. When one breaks, the run exits with
code 3, so a CI step fails.

```
vegaload run -target http://127.0.0.1:8080/widgets -protocol http1 \
  -vus 10 -duration 30s \
  -threshold "p95 < 300ms" -threshold "error_rate < 1%" \
  -junit results.xml
echo $?
```

`-junit` writes a file that CI systems show natively: each threshold is one
test case. In GitHub Actions the summary is also added to the job page.
