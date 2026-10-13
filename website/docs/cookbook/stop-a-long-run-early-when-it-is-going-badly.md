---
title: "Stop a long run early when it is going badly"
description: "For a ten-minute run there is no reason to wait if the API fell over in the first minute."
sidebar_position: 6
---

# Stop a long run early when it is going badly

For a ten-minute run there is no reason to wait if the API fell over in the
first minute.

```
vegaload run -target http://127.0.0.1:8080/widgets -protocol http1 \
  -vus 10 -duration 10m \
  -threshold "error_rate < 5%" -abort-on-breach
```

Statistics such as `p95` and `error_rate` must stay broken for three
seconds, after a short warm-up, so one slow second does not stop the run.
The run exits with code 3 and the result says it was aborted.
