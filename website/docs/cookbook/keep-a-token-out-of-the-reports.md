---
title: "Keep a token out of the reports"
description: "Scripts can only read variables you name."
sidebar_position: 10
---

# Keep a token out of the reports

Scripts can only read variables you name. Use `-secret-env` for tokens and
passwords. The value is removed from the summary, the JSON file, the audit
log and the console. Use `-env` for settings that are not secret.

```
API_KEY=demo-key REGION=eu vegaload run -vus 2 -duration 5s -secret-env API_KEY -env REGION data-env.vl.js
```

In the script you read them as `env.API_KEY` and `env.REGION`.
A variable you name must be set, or the run stops with an error.
