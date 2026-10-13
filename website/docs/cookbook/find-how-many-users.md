---
title: "Find out how many users it can take"
description: "Raise the number of users in stages and watch where latency or errors start to climb."
sidebar_position: 3
---

# Find out how many users it can take

Raise the number of users in stages and watch where latency or errors start
to climb. `-stages` takes `users:time` pairs: the first number is how many
users to reach, the second is how long the stage lasts. The users go up
smoothly during each stage.

```
vegaload run -target http://127.0.0.1:8080/widgets -protocol http1 \
  -executor ramp -stages 10:20s,25:20s,50:20s,0:10s
```

This run lasts as long as its stages, 70 seconds here. `-duration` is not
used with `-stages`, even though the start line of the run prints the default
10 seconds. Open the HTML report and look at the time series: the
point where p95 bends up is your limit. Use `-executor step` for jumps
instead of smooth climbs.
