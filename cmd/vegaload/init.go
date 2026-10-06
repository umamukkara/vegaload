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

	"github.com/vegaload/vegaload/internal/hosts"
)

// initResult records what cmdInit did for one file, for both its text
// and -output json reporting.
type initResult = hosts.WriteResult

func cmdInit(args []string) int {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	dir := fs.String("dir", ".", "project directory to initialize")
	force := fs.Bool("force", false, "overwrite files that already exist")
	output := fs.String("output", "text", "output mode: text or json")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: vegaload init [flags]")
		fmt.Fprintln(fs.Output(), "Registers VegaLoad's MCP server and skill bundles for this project")
		fmt.Fprintln(fs.Output(), "(Claude Code: .claude/skills/vegaload, .mcp.json; Cursor: .cursor/rules, .cursor/mcp.json).")
		fmt.Fprintln(fs.Output(), "Run \"vegaload doctor\" afterwards to check that everything is wired up.")
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

	// The files and their order come from internal/hosts, the same
	// source `vegaload doctor` checks and repairs.
	for _, a := range hosts.ProjectArtifacts() {
		path := filepath.Join(*dir, a.RelPath)
		var res initResult
		var err error
		if a.Kind == "mcp" {
			res, err = hosts.MergeConfigFile(path, exePath, *force)
		} else {
			res, err = hosts.WriteFile(path, a.Content, *force)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "vegaload init: %s: %v\n", a.RelPath, err)
			failed = true
			continue
		}
		results = append(results, res)
	}

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
