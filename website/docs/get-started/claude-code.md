---
title: Use it with Claude Code
description: Set up VegaLoad in a Claude Code project and ask for a load test.
sidebar_position: 5
---

# Use it with Claude Code

You need VegaLoad installed. See [Install](/get-started/install).

## 1. Set up the project

Open a terminal in your project folder. Run:

```bash
vegaload init -editor claude-code
```

It writes three files:

```
created      .claude/skills/vegaload/SKILL.md
created      .claude/skills/vegaload-smoke/SKILL.md
created      .mcp.json
```

The two skills tell Claude Code how to use VegaLoad. One is for real load tests. One is for a quick ten-second check. `.mcp.json` registers the VegaLoad server. It runs `vegaload mcp serve`.

Run `vegaload init` with no `-editor` to set up Claude Code and Cursor together.

:::note
`.mcp.json` holds the full path to the `vegaload` binary on your machine. If you commit it, a teammate with the binary in another place should run `vegaload init -force` once.
:::

## 2. Check that it works

```bash
vegaload doctor
```

For Claude Code you should see:

```
✓ host.claude-code.config      vegaload is registered in .../.mcp.json
✓ host.claude-code.rules       agent rules are present and match this version
✓ host.claude-code.handshake   handshake ok: 9 tools (...)
```

A warning about Cursor on the same list is fine if you only set up Claude Code.

Start Claude Code in the project. Claude Code may ask you to approve the project's MCP server the first time. Run `/mcp` to see its status.

## 3. Ask for a test

> Write a load test for the checkout API and run it against localhost:8080.

Claude writes a scenario, checks it, runs it, and reads the result. Localhost is always allowed. For any other host, see [Safety](/ai-agents/safety). More prompts are in [Prompts that work](/ai-agents/prompts).

## Use the command line instead

You do not need the MCP server to use Claude Code with VegaLoad. Claude Code can run `vegaload run ...` in a terminal like any command. The MCP server adds structured results and the safety gate for agents.
