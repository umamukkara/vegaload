---
title: "Start from a browser recording"
description: "Save a HAR file from the network tab of your browser, then turn it into a scenario."
sidebar_position: 11
---

# Start from a browser recording

Save a HAR file from the network tab of your browser, then turn it into a
scenario.

```
vegaload import har -o recorded.vl.js recording.har
vegaload validate recorded.vl.js
```

By default it keeps the main site and drops images, fonts, style sheets and
analytics. Add `-include-static` or `-include-third-party` to keep them.
Always run `validate` first. It runs the scenario once with one user.
