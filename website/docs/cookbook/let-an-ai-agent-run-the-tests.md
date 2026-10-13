---
title: "Let an AI agent run the tests"
description: "`init` registers VegaLoad as an MCP server for Claude Code and Cursor in this project, and adds two skills: `vegaload-smoke` for quick checks and `vegaload` for real load."
sidebar_position: 14
---

# Let an AI agent run the tests

```
vegaload init
vegaload init -status
```

`init` registers VegaLoad as an MCP server for Claude Code and Cursor in this
project, and adds two skills: `vegaload-smoke` for quick checks and `vegaload`
for real load. Use `-editor cursor` to set up only one editor. `-status`
shows what is in place and changes nothing.
