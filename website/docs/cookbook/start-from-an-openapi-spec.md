---
title: "Start from an OpenAPI spec"
description: "This writes a scenario that calls every operation as a named step, and a runbook with one command per endpoint."
sidebar_position: 12
---

# Start from an OpenAPI spec

```
vegaload new -from-openapi ../sample-app/openapi.json sample-app
vegaload validate sample-app.vl.js
```

This writes a scenario that calls every operation as a named step, and a
runbook with one command per endpoint. The order of calls is a guess:
creates, then reads, updates and deletes. Edit it to match the real flow. Add
`-python` for a Python scenario. Only JSON specs work; export YAML to JSON
first.
