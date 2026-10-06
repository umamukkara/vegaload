// Package doctor implements `vegaload doctor`: a set of small, independent
// checks that answer "will VegaLoad work from here?" for the CLI, for each
// agent host (Cursor, Claude Code, Claude Desktop), and for a target.
//
// Every check returns pass, warn, fail, or skip with a one-line message
// and, for problems, a fix. Checks that can be repaired safely carry a fix
// function that `--fix` runs. Nothing changes on disk unless the caller
// asks for fixes, and nothing is sent to Harness unless it asks for the
// harness checks (NFR-05).
//
// The package knows nothing about the engine, protocols, or reporting.
// Host-specific file locations come from internal/hosts, the same source
// `vegaload init` writes from.
package doctor

import (
	"context"
	"strings"
	"sync"
	"time"
)

// Status is the outcome of one check.
type Status string

// The four outcomes. Pass and skip never affect the exit code, warn only
// does under strict mode, and fail always does.
const (
	Pass Status = "pass"
	Warn Status = "warn"
	Fail Status = "fail"
	Skip Status = "skip"
)

// FixFunc repairs one problem. With dryRun set it changes nothing and
// returns what it would do. Otherwise it returns what it did.
type FixFunc func(ctx context.Context, dryRun bool) (string, error)

// Result is one check's outcome. The JSON form is the stable output
// contract of `vegaload doctor -output json` (FR-CLI-05).
type Result struct {
	ID         string   `json:"id"`
	Category   string   `json:"category"`
	Name       string   `json:"name"`
	Status     Status   `json:"status"`
	Message    string   `json:"message"`
	Detail     []string `json:"detail,omitempty"`
	Fix        string   `json:"fix,omitempty"`
	Fixable    bool     `json:"fixable,omitempty"`
	FixPlan    string   `json:"fix_plan,omitempty"`  // set by --fix -dry-run
	Fixed      bool     `json:"fixed,omitempty"`     // set when --fix repaired it
	FixError   string   `json:"fix_error,omitempty"` // set when --fix failed
	DurationMS int64    `json:"duration_ms"`

	fix FixFunc
}

// Check is one named, independent check.
type Check struct {
	ID       string
	Category string
	Name     string
	Run      func(ctx context.Context) Result
}

// Options selects and tunes the checks.
type Options struct {
	Dir          string   // project directory; empty means Env.Dir
	Hosts        []string // "auto" (default), "all", "none", or host IDs
	MCPConfig    string   // an extra MCP config file to check (host "custom")
	Target       string   // target URL or host:port to check
	AllowTargets []string // extra allowlisted hosts, as for `run -allow-target`
	Smoke        bool     // run a one-user, one-second test against Target
	Harness      bool     // verify Harness RT access (the only network call to Harness)
	Fix          bool     // repair what can be repaired safely
	DryRun       bool     // with Fix: report planned repairs, change nothing
	Only         []string // run only these check IDs or prefixes
	Skip         []string // skip these check IDs or prefixes
	Timeout      time.Duration
}

// Summary counts results by status.
type Summary struct {
	Pass  int `json:"pass"`
	Warn  int `json:"warn"`
	Fail  int `json:"fail"`
	Skip  int `json:"skip"`
	Fixed int `json:"fixed"`
}

// Report is a complete doctor run.
type Report struct {
	Version string    `json:"version"`
	OS      string    `json:"os"`
	Arch    string    `json:"arch"`
	Dir     string    `json:"dir"`
	Summary Summary   `json:"summary"`
	Results []Result  `json:"results"`
	Started time.Time `json:"-"`
}

// ExitCode maps a report to the process exit code: 1 when any check
// failed (or warned, under strict), else 0.
func (r Report) ExitCode(strict bool) int {
	if r.Summary.Fail > 0 || (strict && r.Summary.Warn > 0) {
		return 1
	}
	return 0
}

func summarize(rs []Result) Summary {
	var s Summary
	for _, r := range rs {
		switch r.Status {
		case Pass:
			s.Pass++
		case Warn:
			s.Warn++
		case Fail:
			s.Fail++
		case Skip:
			s.Skip++
		}
		if r.Fixed {
			s.Fixed++
		}
	}
	return s
}

// matches reports whether id equals pattern or sits under it, so "host"
// selects every "host.*" check and "host.cursor" every Cursor check.
func matches(id, pattern string) bool {
	return id == pattern || strings.HasPrefix(id, pattern+".")
}

func selected(id string, opt Options) bool {
	if len(opt.Only) > 0 {
		ok := false
		for _, p := range opt.Only {
			if matches(id, p) {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	for _, p := range opt.Skip {
		if matches(id, p) {
			return false
		}
	}
	return true
}

// Run builds the plan, runs the checks, and, when asked, applies fixes
// and re-checks what it fixed.
func Run(ctx context.Context, env Env, opt Options) (Report, error) {
	if opt.Dir != "" {
		env.Dir = opt.Dir
	}
	if opt.Timeout <= 0 {
		opt.Timeout = 10 * time.Second
	}
	checks, err := plan(env, opt)
	if err != nil {
		return Report{}, err
	}
	var kept []Check
	for _, c := range checks {
		if selected(c.ID, opt) {
			kept = append(kept, c)
		}
	}

	results := runAll(ctx, kept, opt.Timeout)

	if opt.Fix {
		results = applyFixes(ctx, kept, results, opt)
	}

	rep := Report{
		Version: env.Version, OS: env.OS, Arch: env.Arch, Dir: env.Dir,
		Results: results, Summary: summarize(results), Started: time.Now(),
	}
	return rep, nil
}

func runOne(ctx context.Context, c Check, timeout time.Duration) Result {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	res := c.Run(cctx)
	res.ID, res.Category, res.Name = c.ID, c.Category, c.Name
	res.DurationMS = time.Since(start).Milliseconds()
	if res.Fix != "" || res.fix != nil {
		res.Fixable = res.fix != nil
	}
	return res
}

func runAll(ctx context.Context, checks []Check, timeout time.Duration) []Result {
	results := make([]Result, len(checks))
	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i, c := range checks {
		wg.Add(1)
		go func(i int, c Check) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = runOne(ctx, c, timeout)
		}(i, c)
	}
	wg.Wait()
	return results
}

// applyFixes runs each fixable warn or fail result's fix in order, then
// re-runs every check, since one repair can change others (a fixed config
// entry lets the handshake check run). A fixed result is marked Fixed
// only if it now passes.
func applyFixes(ctx context.Context, checks []Check, results []Result, opt Options) []Result {
	applied := map[string]bool{}
	for i := range results {
		r := &results[i]
		if r.fix == nil || (r.Status != Fail && r.Status != Warn) {
			continue
		}
		if opt.DryRun {
			plan, err := r.fix(ctx, true)
			if err != nil {
				r.FixError = err.Error()
			} else {
				r.FixPlan = plan
			}
			continue
		}
		if _, err := r.fix(ctx, false); err != nil {
			r.FixError = err.Error()
			continue
		}
		applied[r.ID] = true
	}
	if len(applied) == 0 {
		return results
	}
	again := runAll(ctx, checks, opt.Timeout)
	for i := range again {
		if !applied[again[i].ID] {
			continue
		}
		if again[i].Status == Pass {
			again[i].Fixed = true
			again[i].Message += " (fixed)"
		} else {
			again[i].FixError = "the fix ran but the check still does not pass"
		}
	}
	// Keep the original error for a fix that failed outright.
	for i := range results {
		if results[i].FixError != "" && !applied[results[i].ID] {
			again[i].FixError = results[i].FixError
		}
	}
	return again
}

func result(s Status, msg string) Result { return Result{Status: s, Message: msg} }
