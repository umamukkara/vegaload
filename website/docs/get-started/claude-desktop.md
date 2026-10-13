---
title: Use it with Claude Desktop
description: Add the VegaLoad MCP server to Claude Desktop on macOS or Windows.
draft: true
sidebar_position: 6
---

# Use it with Claude Desktop

:::caution
Draft. The steps below have not been tested on the Claude Desktop app yet. Do not publish until they are.
:::

`vegaload init` sets up Cursor and Claude Code. It does not write Claude Desktop's settings. Claude Desktop is set up by hand, or with `vegaload doctor`.

Claude Desktop reads one file:

| System | File |
| --- | --- |
| macOS | `~/Library/Application Support/Claude/claude_desktop_config.json` |
| Windows | `%APPDATA%\Claude\claude_desktop_config.json` |

## Option A: let doctor add it

```bash
vegaload doctor -host claude-desktop -fix -dry-run
vegaload doctor -host claude-desktop -fix
```

The first command shows what would change. The second makes the change. VegaLoad changes files in your home folder only when you name the host like this.

## Option B: add it by hand

Open the file. Add VegaLoad under `mcpServers`. Use the full path to the binary. Run `which vegaload` on macOS, or `where vegaload` on Windows, to find it.

```json
{
  "mcpServers": {
    "vegaload": {
      "command": "/full/path/to/vegaload",
      "args": ["mcp", "serve"]
    }
  }
}
```

If the file already has other servers, add `vegaload` next to them. Do not remove them.

## Check it

Quit Claude Desktop completely and open it again. Then run:

```bash
vegaload doctor -host claude-desktop
```

It starts the server and expects all nine tools. In Claude Desktop, the tools list should show VegaLoad.

## What is different from Cursor and Claude Code

Claude Desktop gets the nine tools. It does not get the rules or skill files, because those are project files. The tool descriptions still say what each tool does.

Claude Desktop does not work in your project folder. Ask for scenario files with a full path, or work in Cursor or Claude Code when the test belongs next to your code.
