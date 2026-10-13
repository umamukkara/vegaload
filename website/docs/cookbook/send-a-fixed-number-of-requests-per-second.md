---
title: "Send a fixed number of requests per second"
description: "Real traffic does not slow down when your server does."
sidebar_position: 4
---

# Send a fixed number of requests per second

Real traffic does not slow down when your server does. The
`constant-arrival-rate` executor keeps the rate fixed, so a slow server shows
up as a growing delay, not as fewer requests.

```
vegaload run -target http://127.0.0.1:8080/widgets -protocol http1 \
  -executor constant-arrival-rate -rate 50 -max-vus 100 -duration 30s
```

`-rate` is iterations per second. `-max-vus` is the most users VegaLoad may
use to keep that rate.
