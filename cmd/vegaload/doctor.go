// doctor.go implements `vegaload doctor`: it checks that VegaLoad is set
// up and working on this machine, for the CLI alone and for each agent
// host (Cursor, Claude Code, Claude Desktop). The checks live in
// internal/doctor; host file locations come from internal/hosts, the same
// source `vegaload init` writes from.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/vegaload/vegaload/internal/doctor"
)

func cmdDoctor(args []string) int {
	return runDoctor(args, os.Stdout, os.Stderr, nil)
}

// runDoctor is cmdDoctor's body. A test passes a non-nil mutate to swap
// parts of the environment (the network, the MCP probe) for fakes.
func runDoctor(args []string, stdout, stderr *os.File, mutate func(*doctor.Env)) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var allowTargets repeatedFlags
	output := fs.String("output", "text", "output mode: text, json, or jsonl")
	dir := fs.String("dir", "", "project directory to check (default: the current directory)")
	host := fs.String("host", "auto", "agent hosts to check: auto, all, none, or a comma list of cursor, claude-code, claude-desktop")
	mcpConfig := fs.String("mcp-config", "", "also check this MCP config file (any MCP-capable tool)")
	tgt := fs.String("target", "", "check this target: a URL or host:port")
	fs.Var(&allowTargets, "allow-target", "extra host (or host:port) allowed without confirmation, as for `run` (repeatable)")
	smoke := fs.Bool("smoke", false, "also run a one-user, one-second test against -target (generates real traffic)")
	harness := fs.Bool("harness", false, "also verify Harness RT access (the only check that contacts Harness)")
	fix := fs.Bool("fix", false, "repair what can be repaired safely: missing MCP entries and rules files, stale commands")
	dryRun := fs.Bool("dry-run", false, "with -fix, show what would change and change nothing")
	strict := fs.Bool("strict", false, "exit 1 on warnings as well as failures")
	only := fs.String("only", "", "run only these checks: a comma list of check IDs or groups, e.g. host,target.connect")
	skip := fs.String("skip", "", "skip these checks: a comma list of check IDs or groups")
	timeout := fs.Duration("timeout", 10*time.Second, "time limit for each check")
	verbose := fs.Bool("v", false, "show detail lines for passing checks too")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: vegaload doctor [flags]")
		fmt.Fprintln(fs.Output(), "Checks that VegaLoad is installed and wired up: the CLI, each agent host")
		fmt.Fprintln(fs.Output(), "(a live MCP handshake included), and optionally a target and Harness RT.")
		fmt.Fprintln(fs.Output(), "Exits 1 if any check fails. It changes nothing unless you pass -fix.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if *output != "text" && *output != "json" && *output != "jsonl" {
		fmt.Fprintf(stderr, "vegaload doctor: -output %q: want text, json, or jsonl\n", *output)
		return 2
	}
	if *dryRun && !*fix {
		fmt.Fprintln(stderr, "vegaload doctor: -dry-run only makes sense with -fix")
		return 2
	}
	if *smoke && *tgt == "" {
		fmt.Fprintln(stderr, "vegaload doctor: -smoke needs -target")
		return 2
	}

	env, err := doctor.DefaultEnv(version)
	if err != nil {
		fmt.Fprintf(stderr, "vegaload doctor: %v\n", err)
		return 1
	}
	if mutate != nil {
		mutate(&env)
	}
	if *dir != "" {
		env.Dir = *dir
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	rep, err := doctor.Run(ctx, env, doctor.Options{
		Hosts:        splitList(*host),
		MCPConfig:    *mcpConfig,
		Target:       *tgt,
		AllowTargets: allowTargets,
		Smoke:        *smoke,
		Harness:      *harness,
		Fix:          *fix,
		DryRun:       *dryRun,
		Only:         splitList(*only),
		Skip:         splitList(*skip),
		Timeout:      *timeout,
	})
	if err != nil {
		fmt.Fprintf(stderr, "vegaload doctor: %v\n", err)
		return 2
	}

	switch *output {
	case "json":
		err = doctor.WriteJSON(stdout, rep, env.Home)
	case "jsonl":
		err = doctor.WriteJSONL(stdout, rep, env.Home)
	default:
		doctor.WriteText(stdout, rep, env.Home, *verbose)
	}
	if err != nil {
		fmt.Fprintf(stderr, "vegaload doctor: %v\n", err)
		return 1
	}
	return rep.ExitCode(*strict)
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
