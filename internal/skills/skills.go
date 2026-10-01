// Package skills implements FR-MCP-04's agent skill bundles and the
// `vegaload init` command built on them: the actual Markdown content
// Claude Code and Cursor read to know when and how to call VegaLoad's
// MCP tools, embedded into the compiled binary at build time (via
// go:embed) so `vegaload init` can write them out without needing the
// vegaload source tree to be present on the machine it's installed on.
package skills

import (
	_ "embed"
	"encoding/json"
	"fmt"
)

// ClaudeCode is the Claude Code skill bundle's SKILL.md content,
// written by Init to .claude/skills/vegaload/SKILL.md.
//
//go:embed bundles/claude-code/SKILL.md
var ClaudeCode string

// Cursor is the Cursor rules bundle's content, written by Init to
// .cursor/rules/vegaload.mdc.
//
//go:embed bundles/cursor/vegaload.mdc
var Cursor string

// mcpServerEntry is one entry under a Claude Code or Cursor MCP config
// file's "mcpServers" key — both tools share this schema.
type mcpServerEntry struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

// MergeMCPServerConfig takes an existing MCP config file's raw JSON
// (nil or empty for "no existing file") and returns the JSON it should
// be replaced with, with a "vegaload" entry added or updated under
// "mcpServers" pointing at exePath — preserving every other key and
// every other configured server untouched. It returns ok=false without
// modifying anything when a "vegaload" entry already exists and force
// is false, so a second `vegaload init` doesn't clobber a hand-edited
// config by default.
func MergeMCPServerConfig(existing []byte, exePath string, force bool) (merged []byte, changed bool, err error) {
	doc := map[string]any{}
	if len(existing) > 0 {
		if err := json.Unmarshal(existing, &doc); err != nil {
			return nil, false, fmt.Errorf("existing config is not valid JSON: %w", err)
		}
	}

	servers, _ := doc["mcpServers"].(map[string]any)
	if servers == nil {
		servers = map[string]any{}
	}
	if _, exists := servers["vegaload"]; exists && !force {
		return existing, false, nil
	}

	servers["vegaload"] = mcpServerEntry{Command: exePath, Args: []string{"mcp", "serve"}}
	doc["mcpServers"] = servers

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, false, fmt.Errorf("encoding merged config: %w", err)
	}
	return append(out, '\n'), true, nil
}
