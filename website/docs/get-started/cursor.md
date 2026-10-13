---
title: Use it with Cursor
description: Set up VegaLoad in a Cursor project, check that Cursor sees the nine tools, and ask for your first load test.
sidebar_position: 4
---

# Use it with Cursor

This takes about five minutes. You need VegaLoad installed. See [Install](/get-started/install).

## 1. Set up the project

Open a terminal in your project folder. Run:

```bash
vegaload init -editor cursor
```

It writes three files:

```
created      .cursor/rules/vegaload.mdc
created      .cursor/rules/vegaload-smoke.mdc
created      .cursor/mcp.json
```

The two rules files tell Cursor's agent how to use VegaLoad. One is for real load tests. One is for a quick ten-second check. `.cursor/mcp.json` registers the VegaLoad server. It runs `vegaload mcp serve`.

Run `vegaload init` with no `-editor` to set up Cursor and Claude Code together.

:::note
`.cursor/mcp.json` holds the full path to the `vegaload` binary on your machine. If you commit it, a teammate with the binary in another place should run `vegaload init -force` once. `vegaload doctor` tells you when the path does not match.
:::

## 2. Check that it works

```bash
vegaload doctor
```

For Cursor you should see lines like these:

```
✓ host.cursor.config      vegaload is registered in .../.cursor/mcp.json
✓ host.cursor.rules       agent rules are present and match this version
✓ host.cursor.handshake   handshake ok: 9 tools (...)
```

Doctor starts the server, does a real handshake, and expects all nine tools. If a line shows `!` or `✗`, it says how to fix it. A warning about Claude Code on the same list is fine if you only set up Cursor. It means Claude Code is installed and has no VegaLoad entry in this project. `vegaload doctor -fix` repairs what it safely can.

In Cursor, open the settings and find the MCP section. `vegaload` should be listed, with its nine tools. If Cursor was open before you ran `init`, reload the window.

## 3. Ask for a test

Open the agent chat. Ask in plain words:

> Write a load test for the checkout API and run it against localhost:8080.

The agent can:

1. Write a scenario file next to your code.
2. Check it with one user and one iteration (`validate_scenario`).
3. Run it (`run_test`).
4. Read the result and tell you what happened.

Localhost is always allowed. For any other host, see [Safety](/ai-agents/safety). More prompts are in [Prompts that work](/ai-agents/prompts).

## 4. Read the result

A run writes a self-contained HTML report. The agent gives you its path. Open it in a browser. It works offline.

To keep the result as a baseline, ask: "Save this report as the baseline." Later, ask: "Run it again and compare with the baseline."

## If something is wrong

- **Cursor does not list vegaload.** Run `vegaload init -status`. It shows each file as `ok`, `missing` or `outdated`. Run `vegaload init -force` to fix what it reports.
- **The tools list is empty.** Run `vegaload doctor`. It starts the server the same way Cursor does.
- **The agent does not use VegaLoad.** Name it in your prompt: "Use VegaLoad to ...".
