---
title: "Time each stage of a flow"
description: "A real user does several things in order."
sidebar_position: 8
---

# Time each stage of a flow

A real user does several things in order. Wrap each one in `step(name, fn)`
and the summary shows latency and errors for each stage on its own.
[`crud-flow.vl.js`](https://github.com/vegaload/vegaload/blob/main/examples/scenarios/crud-flow.vl.js) creates a widget, reads it
back, and lists all widgets:

```
vegaload run -vus 5 -duration 10s -threshold 'p95{step="list"} < 300ms' crud-flow.vl.js
```

The sample app fails about 3 percent of creates on purpose, so the `create`
stage shows a few failures. The threshold looks at the `list` stage only. If you do not know which stage
is slow, run without a threshold and read the per-step table first.
