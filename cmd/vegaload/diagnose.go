package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/vegaload/vegaload/internal/diagnose"
	"github.com/vegaload/vegaload/internal/llm"
	"github.com/vegaload/vegaload/internal/pyfind"
	"github.com/vegaload/vegaload/internal/report"
)

// diagnosis is cmdDiagnose's structured result for its environment-check
// mode (no report path given) — the same data behind both its
// human-readable text output and its -output json mode (FR-CLI-05).
type diagnosis struct {
	Version   string `json:"version"`
	GoRuntime string `json:"go_runtime"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	CPUs      int    `json:"cpus"`
	Python3   string `json:"python3"`
}

// resultDiagnosis is cmdDiagnose's structured result when given a report
// path — FR-MCP-04's diagnose_failure, exposed on the CLI as `vegaload
// diagnose <report.json>` so the MCP tool of the same name can shell out
// to this exact command (per AGENTS.md's module-boundary rule) rather
// than duplicating the analysis. Narrative is only populated when an LLM
// is configured (internal/llm.ConfigFromEnv) and -no-llm wasn't passed;
// its absence is not an error.
type resultDiagnosis struct {
	ReportPath string              `json:"report_path"`
	Findings   diagnose.Findings   `json:"findings"`
	Thresholds suggestedThresholds `json:"suggested_thresholds"`
	Narrative  string              `json:"narrative,omitempty"`
}

// suggestedThresholds mirrors diagnose.Thresholds but with a
// human-readable ErrorRate string alongside the raw float, since a CLI
// or chat-facing reader reads "1.5%" far more easily than "0.015".
type suggestedThresholds struct {
	LatencyP95   string  `json:"latency_p95"`
	ErrorRate    float64 `json:"error_rate"`
	ErrorRatePct string  `json:"error_rate_pct"`
	// ThresholdFlags are the same suggestion as ready-to-use
	// `vegaload run -threshold` expressions (FR-CLI-11), so a run can be
	// gated on them without retyping the numbers.
	ThresholdFlags []string `json:"threshold_flags"`
}

// cmdDiagnose has two modes, picked by whether a positional report path
// is given:
//
//   - No path: prints environment information useful for a bug report
//     or for checking what a given machine can actually run before a
//     scenario is attempted on it (FR-CLI unrelated to reports at all —
//     this is Phase 0's original behavior, unchanged).
//   - A path to a report's JSON output (from `vegaload run -report
//     ... .json` or report.WriteJSON directly): runs the rule-based
//     diagnose.Analyze heuristics and, if an LLM is configured via
//     VEGALOAD_LLM_* environment variables and -no-llm wasn't passed,
//     asks it to narrate the findings in plain English (FR-MCP-04/05).
func cmdDiagnose(args []string) int {
	fs := flag.NewFlagSet("diagnose", flag.ContinueOnError)
	output := fs.String("output", "text", "output mode: text or json")
	noLLM := fs.Bool("no-llm", false, "skip LLM narration even if VEGALOAD_LLM_PROVIDER is configured")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: vegaload diagnose [flags]              # environment check")
		fmt.Fprintln(fs.Output(), "       vegaload diagnose [flags] <report.json> # explain a run's results")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 2
	}
	if *output != "text" && *output != "json" {
		fmt.Fprintf(os.Stderr, "vegaload diagnose: -output %q: want text or json\n", *output)
		return 2
	}

	if fs.NArg() > 1 {
		fmt.Fprintf(os.Stderr, "vegaload diagnose: want at most one report path, got %d\n", fs.NArg())
		return 2
	}
	if fs.NArg() == 1 {
		return cmdDiagnoseReport(fs.Arg(0), *output, *noLLM)
	}
	return cmdDiagnoseEnvironment(*output)
}

func cmdDiagnoseEnvironment(output string) int {
	d := diagnosis{
		Version:   version,
		GoRuntime: runtime.Version(),
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		CPUs:      runtime.NumCPU(),
		Python3:   pythonDiagnosis(),
	}

	if output == "json" {
		if err := json.NewEncoder(os.Stdout).Encode(d); err != nil {
			fmt.Fprintf(os.Stderr, "vegaload diagnose: %v\n", err)
			return 1
		}
		return 0
	}

	fmt.Printf("vegaload:        %s\n", d.Version)
	fmt.Printf("go runtime:      %s\n", d.GoRuntime)
	fmt.Printf("os/arch:         %s/%s\n", d.OS, d.Arch)
	fmt.Printf("cpus:            %d\n", d.CPUs)
	fmt.Printf("python3:         %s\n", d.Python3)
	return 0
}

// cmdDiagnoseReport loads a report.Result from path (JSON, as written
// by report.WriteJSON / `vegaload run -report`), runs the rule-based
// heuristics, and optionally narrates them via a configured LLM.
func cmdDiagnoseReport(path string, output string, noLLM bool) int {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "vegaload diagnose: reading %s: %v\n", path, err)
		return 1
	}
	var res report.Result
	if err := json.Unmarshal(data, &res); err != nil {
		fmt.Fprintf(os.Stderr, "vegaload diagnose: %s does not look like a vegaload JSON report: %v\n", path, err)
		return 1
	}

	findings := diagnose.Analyze(&res)
	th := diagnose.SuggestThresholds(&res)
	rd := resultDiagnosis{
		ReportPath: path,
		Findings:   findings,
		Thresholds: suggestedThresholds{
			LatencyP95:   th.LatencyP95,
			ErrorRate:    th.ErrorRate,
			ErrorRatePct: formatPercent(th.ErrorRate),
			ThresholdFlags: []string{
				"p95 <= " + th.LatencyP95,
				"error_rate <= " + strconv.FormatFloat(th.ErrorRate, 'g', -1, 64),
			},
		},
	}

	if !noLLM {
		cfg := llm.ConfigFromEnv()
		if cfg.Enabled() {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			narrative, err := llm.Narrate(ctx, cfg, narratePrompt(path, findings))
			if err != nil {
				// A configured-but-failing LLM should not fail the whole
				// command — the rule-based findings above are already a
				// complete, useful answer on their own.
				fmt.Fprintf(os.Stderr, "vegaload diagnose: LLM narration skipped: %v\n", err)
			} else {
				rd.Narrative = narrative
			}
		}
	}

	if output == "json" {
		if err := json.NewEncoder(os.Stdout).Encode(rd); err != nil {
			fmt.Fprintf(os.Stderr, "vegaload diagnose: %v\n", err)
			return 1
		}
		return 0
	}

	printResultDiagnosis(os.Stdout, rd)
	return 0
}

// narratePrompt composes the text sent to an LLM for narration. It
// stays here (not in internal/llm) so that package remains agnostic to
// what it's narrating.
func narratePrompt(path string, f diagnose.Findings) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are summarizing the results of a load test (report: %s) for an engineer. ", path)
	b.WriteString("Be concise (3-5 sentences), plain English, no bullet points. Here is what was observed:\n")
	for _, n := range f.Notes {
		fmt.Fprintf(&b, "- %s\n", n)
	}
	b.WriteString("Explain what likely happened and, if relevant, what to check next.")
	return b.String()
}

func printResultDiagnosis(w *os.File, rd resultDiagnosis) {
	fmt.Fprintf(w, "report:          %s\n", rd.ReportPath)
	if rd.Findings.ZeroIterations {
		fmt.Fprintln(w, "zero iterations: the run recorded no requests at all")
	} else {
		fmt.Fprintf(w, "failure rate:    %.1f%%\n", rd.Findings.FailurePercent)
		if rd.Findings.LatencyLongTail {
			fmt.Fprintln(w, "latency:         long tail (p99 > 5x p50)")
		}
		if rd.Findings.WorstBucket != nil {
			shape := "spread across the run"
			if rd.Findings.Transient {
				shape = "concentrated in one period"
			}
			fmt.Fprintf(w, "worst second:    +%.0fs, %.1f%% errors (%s)\n",
				rd.Findings.WorstBucket.OffsetSeconds, rd.Findings.WorstBucket.ErrorRate*100, shape)
		}
	}
	fmt.Fprintln(w, "notes:")
	for _, n := range rd.Findings.Notes {
		fmt.Fprintf(w, "  - %s\n", n)
	}
	fmt.Fprintf(w, "suggested thresholds: p95 <= %s, error rate <= %s\n", rd.Thresholds.LatencyP95, rd.Thresholds.ErrorRatePct)
	if len(rd.Thresholds.ThresholdFlags) > 0 {
		var flags []string
		for _, f := range rd.Thresholds.ThresholdFlags {
			flags = append(flags, fmt.Sprintf("-threshold %q", f))
		}
		fmt.Fprintf(w, "gate a run on them:   vegaload run ... %s\n", strings.Join(flags, " "))
	}
	if rd.Narrative != "" {
		fmt.Fprintf(w, "\n%s\n", rd.Narrative)
	}
}

func formatPercent(rate float64) string {
	return fmt.Sprintf("%.1f%%", rate*100)
}

func pythonDiagnosis() string {
	interp, ok := pyfind.FindOnThisMachine()
	if !ok {
		return "not found on PATH (only needed for Python scenarios; JavaScript/TypeScript scenarios don't need it)"
	}
	args := append(append([]string{}, interp.Args...), "--version")
	out, err := exec.Command(interp.Path, args...).Output() //nolint:gosec // fixed interpreter lookup, no user input
	if err != nil {
		return fmt.Sprintf("found at %s, but running it failed: %v", interp.Path, err)
	}
	return fmt.Sprintf("%s (%s)", strings.TrimSpace(string(out)), interp.Path)
}
