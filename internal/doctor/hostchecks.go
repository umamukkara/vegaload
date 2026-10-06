package doctor

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/vegaload/vegaload/internal/hosts"
	"github.com/vegaload/vegaload/internal/mcp"
	"github.com/vegaload/vegaload/internal/mcpprobe"
)

type chosenHost struct {
	host     hosts.Host
	explicit bool // named by the user, so user-scope files may be repaired
}

// chooseHosts turns -host values into the hosts to check. "auto" picks
// every host that is installed or already has VegaLoad files in the project.
func chooseHosts(env Env, opt Options) (chosen []chosenHost, unsupported []hosts.Host, none bool, err error) {
	henv := env.hostEnv()
	mode := opt.Hosts
	if len(mode) == 0 {
		mode = []string{"auto"}
	}
	seen := map[string]bool{}
	add := func(h hosts.Host, explicit bool) {
		if seen[h.ID] {
			return
		}
		seen[h.ID] = true
		chosen = append(chosen, chosenHost{h, explicit})
	}
	for _, m := range mode {
		switch m {
		case "none":
			none = true
		case "auto":
			for _, h := range hosts.All() {
				if h.SupportedOn(env.OS) && hostActive(h, henv) {
					add(h, false)
				}
			}
		case "all":
			for _, h := range hosts.All() {
				if h.SupportedOn(env.OS) {
					add(h, false)
				}
			}
		default:
			h, ok := hosts.Get(m)
			if !ok {
				return nil, nil, false, fmt.Errorf("unknown host %q (want auto, all, none, cursor, claude-code, or claude-desktop)", m)
			}
			if !h.SupportedOn(env.OS) {
				unsupported = append(unsupported, h)
				continue
			}
			add(h, true)
		}
	}
	if opt.MCPConfig != "" {
		add(hosts.Custom(opt.MCPConfig), true)
	}
	return chosen, unsupported, none, nil
}

func hostActive(h hosts.Host, henv hosts.Env) bool {
	if ok, _ := h.Detect(henv); ok {
		return true
	}
	return hasProjectFiles(h, henv)
}

// hasProjectFiles reports whether the project already holds files for h.
func hasProjectFiles(h hosts.Host, henv hosts.Env) bool {
	for _, c := range h.Configs(henv) {
		if c.Scope == hosts.ScopeProject && hosts.Inspect(c).Exists {
			return true
		}
	}
	if h.Rules != nil {
		for _, r := range h.Rules(henv) {
			if _, err := os.Stat(r.Path); err == nil {
				return true
			}
		}
	}
	return false
}

func hostChecks(env Env, opt Options) ([]Check, error) {
	chosen, unsupported, none, err := chooseHosts(env, opt)
	if err != nil {
		return nil, err
	}
	var checks []Check
	for _, h := range unsupported {
		h := h
		checks = append(checks, Check{ID: "host." + h.ID + ".detected", Category: "host", Name: h.Name + " installed",
			Run: func(context.Context) Result {
				return result(Skip, fmt.Sprintf("%s is not available on %s", h.Name, env.OS))
			}})
	}
	if len(chosen) == 0 && len(unsupported) == 0 {
		if !none {
			checks = append(checks, Check{ID: "host.none", Category: "host", Name: "Agent hosts", Run: func(context.Context) Result {
				return result(Pass, "no agent host found. Using VegaLoad from the CLI alone is fully supported. Run `vegaload init` to set up Cursor or Claude Code.")
			}})
		}
		return checks, nil
	}
	for _, c := range chosen {
		checks = append(checks, hostCheckSet(env, c)...)
	}
	return checks, nil
}

// configStates inspects every MCP config the host may read.
func configStates(h hosts.Host, henv hosts.Env) []hosts.ConfigState {
	var out []hosts.ConfigState
	for _, c := range h.Configs(henv) {
		out = append(out, hosts.Inspect(c))
	}
	return out
}

func firstEntry(states []hosts.ConfigState) *hosts.ConfigState {
	for i := range states {
		if states[i].Entry != nil {
			return &states[i]
		}
	}
	return nil
}

// repairable reports whether doctor may rewrite c: a file VegaLoad owns
// the structure of, in the project, or in the user's home when the user
// named this host.
func repairable(c hosts.MCPConfig, explicit bool) bool {
	if c.ReadOnly || len(c.Key) > 0 {
		return false
	}
	return c.Scope == hosts.ScopeProject || explicit
}

func mergeFix(path, exe string, force bool) FixFunc {
	return func(ctx context.Context, dry bool) (string, error) {
		if dry {
			verb := "add"
			if force {
				verb = "replace"
			}
			return fmt.Sprintf("%s the vegaload entry in %s so it runs `%s mcp serve`", verb, path, exe), nil
		}
		res, err := hosts.MergeConfigFile(path, exe, force)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s %s", res.Status, res.Path), nil
	}
}

func hostCheckSet(env Env, ch chosenHost) []Check {
	h := ch.host
	henv := env.hostEnv()
	id := func(s string) string { return "host." + h.ID + "." + s }
	var checks []Check

	checks = append(checks, Check{ID: id("detected"), Category: "host", Name: h.Name + " installed", Run: func(context.Context) Result {
		if ok, evidence := h.Detect(henv); ok {
			return result(Pass, fmt.Sprintf("%s found (%s)", h.Name, evidence))
		}
		if hasProjectFiles(h, henv) {
			return result(Pass, "no "+h.Name+" install found, but this project has "+h.Name+" files")
		}
		if ch.explicit {
			return result(Warn, h.Name+" does not look installed on this machine. Checking its files anyway.")
		}
		return result(Skip, h.Name+" not found on this machine")
	}})

	checks = append(checks, Check{ID: id("config"), Category: "host", Name: h.Name + " MCP config", Run: func(context.Context) Result {
		states := configStates(h, henv)
		var detail []string
		var invalid []string
		var fixCfg *hosts.MCPConfig
		for i := range states {
			s := states[i]
			where := fmt.Sprintf("%s: %s", s.Config.Scope, s.Config.Path)
			if len(s.Config.Key) > 0 {
				where += " (entry for this project)"
			}
			switch {
			case s.Err != nil:
				detail = append(detail, where+" (unreadable: "+s.Err.Error()+")")
				invalid = append(invalid, s.Config.Path)
			case !s.Exists:
				detail = append(detail, where+" (no file)")
			case s.Entry != nil:
				detail = append(detail, where+" (vegaload registered)")
			default:
				detail = append(detail, where+" (no vegaload entry)")
			}
			if fixCfg == nil && repairable(s.Config, ch.explicit) && s.Err == nil {
				c := s.Config
				fixCfg = &c
			}
		}
		if st := firstEntry(states); st != nil {
			r := result(Pass, fmt.Sprintf("vegaload is registered in %s", st.Config.Path))
			r.Detail = detail
			return r
		}
		// Auto-detected hosts (Cursor is installed, ~/.cursor exists) with
		// no VegaLoad files must not fail the run: CLI-only is healthy.
		// Named hosts (-host cursor) and projects that already have host
		// files still Fail so init/doctor stay in agreement.
		if !ch.explicit && !hasProjectFiles(h, henv) {
			r := result(Warn, fmt.Sprintf("%s is installed but VegaLoad is not registered. Using the CLI alone is fully supported.", h.Name))
			r.Detail = detail
			r.Fix = "Run `vegaload init` in this project if you want the agent path."
			if fixCfg != nil {
				r.fix = mergeFix(fixCfg.Path, env.Exe, false)
			}
			return r
		}
		r := result(Fail, fmt.Sprintf("%s has no vegaload MCP server registered", h.Name))
		r.Detail = detail
		r.Fix = "Run `vegaload init` in this project."
		if len(invalid) > 0 {
			r.Fix = "Fix the JSON in " + strings.Join(invalid, ", ") + " first, then run `vegaload init`."
			return r
		}
		if fixCfg != nil {
			r.fix = mergeFix(fixCfg.Path, env.Exe, false)
		}
		return r
	}})

	checks = append(checks, Check{ID: id("command"), Category: "host", Name: h.Name + " server command", Run: func(ctx context.Context) Result {
		st := firstEntry(configStates(h, henv))
		if st == nil {
			return result(Skip, "no vegaload entry to check")
		}
		return checkCommand(ctx, env, st, ch.explicit)
	}})

	if h.Rules != nil {
		checks = append(checks, Check{ID: id("rules"), Category: "host", Name: h.Name + " rules", Run: func(context.Context) Result {
			return checkRules(h.Rules(henv))
		}})
	}

	checks = append(checks, Check{ID: id("handshake"), Category: "host", Name: h.Name + " MCP handshake", Run: func(ctx context.Context) Result {
		st := firstEntry(configStates(h, henv))
		if st == nil {
			return result(Skip, "no vegaload entry to start")
		}
		return checkHandshake(ctx, env, st)
	}})
	return checks
}

func hasMCPServe(args []string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "mcp" && args[i+1] == "serve" {
			return true
		}
	}
	return false
}

func checkCommand(ctx context.Context, env Env, st *hosts.ConfigState, explicit bool) Result {
	entry := st.Entry.Expand(env.hostEnv(), env.Getenv)
	var fix FixFunc
	if repairable(st.Config, explicit) {
		fix = mergeFix(st.Config.Path, env.Exe, true)
	}
	bad := func(s Status, msg, how string) Result {
		r := result(s, msg)
		r.Detail = []string{"config: " + st.Config.Path, "command: " + entry.Command + " " + strings.Join(entry.Args, " ")}
		r.Fix = how
		r.fix = fix
		return r
	}

	resolved := entry.Command
	if entry.Command == "" {
		return bad(Fail, "the vegaload entry has no command", "Run `vegaload init -force` to rewrite it.")
	}
	if strings.Contains(entry.Command, "/") {
		fi, err := os.Stat(entry.Command)
		switch {
		case err != nil:
			return bad(Fail, "the configured command does not exist: "+entry.Command, "Run `vegaload init -force` to point it at the binary you are running now.")
		case fi.IsDir() || fi.Mode()&0o111 == 0:
			return bad(Fail, "the configured command is not an executable file: "+entry.Command, "Run `vegaload init -force` to rewrite it.")
		}
	} else {
		p, err := env.LookPath(entry.Command)
		if err != nil {
			return bad(Fail, fmt.Sprintf("the configured command %q is not on the PATH the host will use", entry.Command), "Use an absolute path: run `vegaload init -force`.")
		}
		resolved = p
	}
	if !hasMCPServe(entry.Args) {
		return bad(Fail, "the configured arguments do not start the MCP server (expected `mcp serve`)", "Run `vegaload init -force` to rewrite the entry.")
	}

	out, err := env.RunCmd(ctx, resolved, "version")
	if err != nil {
		return bad(Warn, "could not run `"+resolved+" version` to compare versions", "Check that the binary runs from a terminal.")
	}
	got := strings.TrimSpace(strings.TrimPrefix(out, "vegaload"))
	if got != env.Version {
		return bad(Warn, fmt.Sprintf("the host runs vegaload %s but this is %s", got, env.Version),
			"If that is not intended, run `vegaload init -force` to point the host at this binary.")
	}
	r := result(Pass, resolved+" runs and matches this version")
	r.Detail = []string{"config: " + st.Config.Path}
	return r
}

func checkRules(files []hosts.RulesFile) Result {
	var missing, stale []hosts.RulesFile
	for _, f := range files {
		data, err := os.ReadFile(f.Path)
		switch {
		case err != nil:
			missing = append(missing, f)
		case string(data) != f.Content:
			stale = append(stale, f)
		}
	}
	switch {
	case len(missing) > 0:
		r := result(Warn, "the agent rules file is missing, so the agent has less guidance on using VegaLoad")
		for _, f := range missing {
			r.Detail = append(r.Detail, "missing: "+f.Path)
		}
		r.Fix = "Run `vegaload init` in this project."
		r.fix = func(ctx context.Context, dry bool) (string, error) {
			var done []string
			for _, f := range missing {
				if dry {
					done = append(done, "create "+f.Path)
					continue
				}
				res, err := hosts.WriteFile(f.Path, f.Content, false)
				if err != nil {
					return "", err
				}
				done = append(done, res.Status+" "+res.Path)
			}
			return strings.Join(done, "; "), nil
		}
		return r
	case len(stale) > 0:
		r := result(Warn, "the agent rules file differs from the one this version ships. It may be edited or out of date.")
		for _, f := range stale {
			r.Detail = append(r.Detail, "differs: "+f.Path)
		}
		r.Fix = "To replace it with this version's file, run `vegaload init -force`. This also rewrites the MCP entries."
		return r
	}
	r := result(Pass, "agent rules are present and match this version")
	for _, f := range files {
		r.Detail = append(r.Detail, f.Path)
	}
	return r
}

func checkHandshake(ctx context.Context, env Env, st *hosts.ConfigState) Result {
	entry := st.Entry.Expand(env.hostEnv(), env.Getenv)
	cmdPath := entry.Command
	if !strings.Contains(cmdPath, "/") {
		if p, err := env.LookPath(cmdPath); err == nil {
			cmdPath = p
		}
	}
	if strings.Contains(cmdPath, "/") {
		if _, err := os.Stat(cmdPath); err != nil {
			return result(Skip, "the configured command does not exist, see the command check")
		}
	}
	info, err := env.Probe(ctx, mcpprobe.Command{Path: cmdPath, Args: entry.Args, Env: entry.Env, Dir: env.Dir})
	manual := "Run `" + cmdPath + " " + strings.Join(entry.Args, " ") + "` in a terminal to see why it does not start."
	if err != nil {
		r := result(Fail, "the configured server did not complete an MCP handshake: "+err.Error())
		r.Detail = []string{"config: " + st.Config.Path}
		r.Fix = manual
		return r
	}
	if missing := info.MissingTools(mcp.CoreToolNames); len(missing) > 0 {
		r := result(Fail, "the server started but is missing tools: "+strings.Join(missing, ", "))
		r.Detail = []string{"tools found: " + strings.Join(info.Tools, ", ")}
		r.Fix = "The server may be an older vegaload. Update it, then run `vegaload init -force`."
		return r
	}
	r := result(Pass, fmt.Sprintf("handshake ok: %d tools (%s)", len(info.Tools), strings.Join(info.Tools, ", ")))
	r.Detail = []string{"config: " + st.Config.Path}
	return r
}
