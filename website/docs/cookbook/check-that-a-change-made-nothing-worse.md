---
title: "Check that a change made nothing worse"
description: "Keep a good run as the baseline, then compare after your change."
sidebar_position: 7
---

# Check that a change made nothing worse

Keep a good run as the baseline, then compare after your change.

```
vegaload run -target http://127.0.0.1:8080/widgets -protocol http1 \
  -vus 10 -duration 30s -out baseline.json

# ... change your code, restart the service ...

vegaload run -target http://127.0.0.1:8080/widgets -protocol http1 \
  -vus 10 -duration 30s -baseline baseline.json -max-regression 10
```

The run exits with code 3 if p95 or the error rate is more than 10 percent
worse. To compare two files you already have:
`vegaload compare baseline.json candidate.json`.
