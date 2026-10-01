// init.go implements `vegaload init`: FR-MCP-04's one-command setup for
// an agent-native project. It writes four things, each independently
// skippable if already present (so a second `vegaload init` is a no-op
// unless -force is given):
//
//   - .claude/skills/vegaload/SKILL.md — the Claude Code skill bundle
//   - .cursor/rules/vegaload.mdc        — the Cursor rules bundle
//   - .mcp.json                         — Claude Code's MCP server config
//   - .cursor/mcp.json                  — Cursor's MCP server config
//
// Both MCP config files point at this same compiled binary (via
// os.Executable()) running `mcp serve`, merged into whatever config
// already exists there rather than overwriting it — see
// internal/skills.MergeMCPServerConfig.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/vegaload/vegaload/internal/skills"
)

// initResult records what cmdInit did for one file, for both its text
// and -output json reporting.
type initResult struct {
	Path   string `json:"path"`
	Status string `json:"status"` // "created", "updated", or "skipped (exists)"
}

func cmdInit(args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory to initialize")
	force := fs.Bool("force", false, "overwrite files that already exist")
	output := fs.String("output", "text", "output mode: text or json")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: vegaload init [flags]")
		fmt.Fprintln(fs.Output(), "Registers VegaLoad's MCP server and skill bundles for this project")
		fmt.Fprintln(fs.Output(), "(Claude Code: .claude/skills/vegaload, .mcp.json; Cursor: .cursor/rules, .cursor/mcp.json).")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if *output != "text" && *output != "json" {
		fmt.Fprintf(os.Stderr, "vegaload init: -output %q: want text or json\n", *output)
		return 2
	}

	exePath, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "vegaload init: locating the vegaload binary: %v\n", err)
		return 1
	}

	var results []initResult
	failed := false

	writeFile := func(relPath, content string) {
		path := filepath.Join(*dir, relPath)
		res, err := writeSkillFile(path, content, *force)
		if err != nil {
			fmt.Fprintf(os.Stderr, "vegaload init: %s: %v\n", relPath, err)
			failed = true
			return
		}
		results = append(results, res)
	}
	writeMCPConfig := func(relPath string) {
		path := filepath.Join(*dir, relPath)
		res, err := mergeMCPConfigFile(path, exePath, *force)
		if err != nil {
			fmt.Fprintf(os.Stderr, "vegaload init: %s: %v\n", relPath, err)
			failed = true
			return
		}
		results = append(results, res)
	}

	writeFile(filepath.Join(".claude", "skills", "vegaload", "SKILL.md"), skills.ClaudeCode)
	writeFile(filepath.Join(".cursor", "rules", "vegaload.mdc"), skills.Cursor)
	writeMCPConfig(".mcp.json")
	writeMCPConfig(filepath.Join(".cursor", "mcp.json"))

	if *output == "json" {
		if err := json.NewEncoder(os.Stdout).Encode(results); err != nil {
			fmt.Fprintf(os.Stderr, "vegaload init: %v\n", err)
			return 1
		}
	} else {
		for _, r := range results {
			fmt.Printf("%-12s %s\n", r.Status, r.Path)
		}
	}
	if failed {
		return 1
	}
	return 0
}

// writeSkillFile writes content to path, creating any missing parent
// directories, unless path already exists and force is false, in which
// case it reports "skipped (exists)" without touching the file.
func writeSkillFile(path, content string, force bool) (initResult, error) {
	if !force {
		if _, err := os.Stat(path); err == nil {
			return initResult{Path: path, Status: "skipped (exists)"}, nil
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return initResult{}, err
	}
	status := "created"
	if _, err := os.Stat(path); err == nil {
		status = "updated"
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return initResult{}, err
	}
	return initResult{Path: path, Status: status}, nil
}

// mergeMCPConfigFile reads path (if it exists), merges in a "vegaload"
// MCP server entry via internal/skills.MergeMCPServerConfig, and writes
// the result back — creating the file and its parent directory if
// neither existed yet.
func mergeMCPConfigFile(path, exePath string, force bool) (initResult, error) {
	var existing []byte
	existed := false
	if data, err := os.ReadFile(path); err == nil {
		existing = data
		existed = true
	} else if !os.IsNotExist(err) {
		return initResult{}, err
	}

	merged, changed, err := skills.MergeMCPServerConfig(existing, exePath, force)
	if err != nil {
		return initResult{}, err
	}
	if !changed {
		return initResult{Path: path, Status: "skipped (exists)"}, nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return initResult{}, err
	}
	if err := os.WriteFile(path, merged, 0o644); err != nil {
		return initResult{}, err
	}
	status := "created"
	if existed {
		status = "updated"
	}
	return initResult{Path: path, Status: status}, nil
}
