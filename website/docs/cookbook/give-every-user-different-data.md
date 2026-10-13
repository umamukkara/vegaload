---
title: "Give every user different data"
description: "[`data-env.vl.js`](https://github.com/vegaload/vegaload/blob/main/examples/scenarios/data-env.vl.js) reads one row of `data/widgets.csv` for each iteration, so users do not all send the same request."
sidebar_position: 9
---

# Give every user different data

[`data-env.vl.js`](https://github.com/vegaload/vegaload/blob/main/examples/scenarios/data-env.vl.js) reads one row of
`data/widgets.csv` for each iteration, so users do not all send the same
request.

```
API_KEY=demo-key vegaload run -vus 5 -duration 10s -data data/widgets.csv -secret-env API_KEY data-env.vl.js
```

The sample app fails about 3 percent of creates on purpose, so a few checks
fail. That is expected. In the script, `data.widgets.next()` is the next row, and
`data.widgets.random()` is any row.
