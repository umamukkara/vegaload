---
title: "Check that an API works"
description: "A smoke test: a few users, a few seconds."
sidebar_position: 2
---

# Check that an API works

A smoke test: a few users, a few seconds. It tells you the API answers and
does not fail.

```
vegaload run -target http://127.0.0.1:8080/widgets -protocol http1 -vus 5 -duration 10s
```

Look at `failed` (it should be 0) and `p95` (the time that 95 out of 100
requests beat). Run this before any bigger test.
