package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/vegaload/vegaload/internal/report"
)

// exitThresholdsBreached is the exit code `vegaload run` uses when a
// run finished but broke one of its -threshold limits (FR-CLI-11). It is
// the same number cmd/vegaload defines; the MCP layer cannot import that
// package, so the value is repeated here.
const exitThresholdsBreached = 3

// NewTools returns FR-MCP-03's tool set, each one a thin wrapper around
// RunCLI(ctx, exePath, ...) — the exact args a human would type at
// their own shell. exePath is resolved once by the caller (normally
// os.Executable(), done in cmd/vegaload/mcp.go) and closed over by every
// handler below.
func NewTools(exePath string) []Tool {
	return []Tool{
		createScenarioTool(exePath),
		runTestTool(exePath),
		getResultsTool(),
		suggestThresholdsTool(exePath),
		diagnoseFailureTool(exePath),
		compareReportsTool(exePath),
		generateFromSpecTool(exePath),
		validateScenarioTool(exePath),
	}
}

// --- create_scenario : wraps `vegaload new` ---

type createScenarioArgs struct {
	Name   string `json:"name,omitempty"`
	Python bool   `json:"python,omitempty"`
	Force  bool   `json:"force,omitempty"`
}

func createScenarioTool(exePath string) Tool {
	return Tool{
		Name:        "create_scenario",
		Description: "Scaffold a new VegaLoad scenario file (JavaScript by default, or Python). Equivalent to running `vegaload new`.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name":   map[string]any{"type": "string", "description": "scenario name or path (default: \"scenario\")"},
				"python": map[string]any{"type": "boolean", "description": "scaffold a Python scenario instead of JavaScript"},
				"force":  map[string]any{"type": "boolean", "description": "overwrite the file if it already exists"},
			},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in createScenarioArgs
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			args := []string{"new"}
			if in.Python {
				args = append(args, "-python")
			}
			if in.Force {
				args = append(args, "-force")
			}
			args = append(args, "-output", "json")
			if in.Name != "" {
				args = append(args, in.Name)
			}
			stdout, err := RunCLI(ctx, exePath, args...)
			if err != nil {
				return nil, err
			}
			var result map[string]any
			if err := json.Unmarshal(stdout, &result); err != nil {
				return nil, fmt.Errorf("parsing `vegaload new` output: %w", err)
			}
			return result, nil
		},
	}
}

// --- run_test : wraps `vegaload run` ---

type runTestArgs struct {
	ScenarioPath  string   `json:"scenario_path,omitempty"`
	Target        string   `json:"target,omitempty"`
	Protocol      string   `json:"protocol,omitempty"`
	Executor      string   `json:"executor,omitempty"`
	VUs           int      `json:"vus,omitempty"`
	Duration      string   `json:"duration,omitempty"`
	Stages        string   `json:"stages,omitempty"`
	Rate          float64  `json:"rate,omitempty"`
	MaxVUs        int      `json:"max_vus,omitempty"`
	AllowTargets  []string `json:"allow_targets,omitempty"`
	Yes           bool     `json:"yes,omitempty"`
	ReportPath    string   `json:"report_path,omitempty"`
	NoReport      bool     `json:"no_report,omitempty"`
	Thresholds    []string `json:"thresholds,omitempty"`
	BaselinePath  string   `json:"baseline_path,omitempty"`
	MaxRegression *float64 `json:"max_regression,omitempty"`
	JUnitPath     string   `json:"junit_path,omitempty"`
}

func runTestTool(exePath string) Tool {
	return Tool{
		Name: "run_test",
		Description: "Run a load test: either a scenario file (scenario_path) or a protocol-direct target " +
			"(target + protocol). Equivalent to `vegaload run`. Returns the run's report.Result and, unless " +
			"no_report is set, the path to a self-contained HTML report. A non-localhost target that isn't in " +
			"allow_targets is refused unless yes is set — the same FR-CLI-06 safety gate `vegaload run` enforces " +
			"from a terminal, since this tool has no interactive terminal of its own to prompt on. Optional thresholds " +
			"(each like \"p95 < 300ms\" or \"error_rate < 1%\") make the run pass or fail: the result then carries " +
			"thresholds and thresholds_passed. A breached threshold is a normal result with thresholds_passed false, " +
			"not a tool error — the same run exits 3 from a terminal. With baseline_path the run is also compared with an earlier " +
			"report, and the result carries baseline with passed true or false; a worse run is a normal result too.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"scenario_path":  map[string]any{"type": "string", "description": "path to a scenario file (mutually exclusive with target/protocol)"},
				"target":         map[string]any{"type": "string", "description": "target URL or host:port (protocol-direct mode)"},
				"protocol":       map[string]any{"type": "string", "description": "http1, http2, grpc, or websocket (protocol-direct mode)"},
				"executor":       map[string]any{"type": "string", "description": "fixed-vus (default), ramp, step, or constant-arrival-rate"},
				"vus":            map[string]any{"type": "integer", "description": "virtual users (fixed-vus)"},
				"duration":       map[string]any{"type": "string", "description": "run duration, e.g. \"30s\" (fixed-vus, constant-arrival-rate)"},
				"stages":         map[string]any{"type": "string", "description": "comma-separated target:duration stages for ramp/step, e.g. \"10:30s,0:10s\""},
				"rate":           map[string]any{"type": "number", "description": "iterations per second (constant-arrival-rate)"},
				"max_vus":        map[string]any{"type": "integer", "description": "max concurrent VUs (constant-arrival-rate)"},
				"allow_targets":  map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "additional hosts allowed without confirmation"},
				"yes":            map[string]any{"type": "boolean", "description": "skip the confirmation gate for a non-allowlisted target"},
				"report_path":    map[string]any{"type": "string", "description": "where to write the self-contained HTML report (default: a generated name)"},
				"no_report":      map[string]any{"type": "boolean", "description": "skip writing the HTML report"},
				"thresholds":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "pass/fail thresholds, each \"[name:] metric operator value\", e.g. \"p95 < 300ms\" or \"error_rate < 1%\". Metrics: p50, p90, p95, p99, mean, min, max, error_rate, rps, failed, total, check_rate (share of check() calls that passed)"},
				"baseline_path":  map[string]any{"type": "string", "description": "path to a JSON report of an earlier run (from `-out`) to compare this run with. If p95 or the error rate is worse by more than max_regression, the result has baseline.passed false"},
				"junit_path":     map[string]any{"type": "string", "description": "also write a JUnit XML file here: each threshold, check and the baseline gate is one test case"},
				"max_regression": map[string]any{"type": "number", "description": "with baseline_path: how many percent worse than the baseline p95 and error rate may be (default 0: any increase fails)"},
			},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in runTestArgs
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			if in.ScenarioPath == "" && (in.Target == "" || in.Protocol == "") {
				return nil, fmt.Errorf("provide scenario_path, or both target and protocol")
			}
			if in.ScenarioPath != "" && (in.Target != "" || in.Protocol != "") {
				return nil, fmt.Errorf("scenario_path and target/protocol are two different modes; use one, not both")
			}

			reportPath := in.ReportPath
			if reportPath == "" && !in.NoReport {
				reportPath = fmt.Sprintf("vegaload-mcp-report-%s.html", time.Now().Format("20060102-150405.000"))
			}

			args := []string{"run", "-output", "json", "-trigger", "mcp", "-no-open"}
			if in.Executor != "" {
				args = append(args, "-executor", in.Executor)
			}
			if in.VUs > 0 {
				args = append(args, "-vus", strconv.Itoa(in.VUs))
			}
			if in.Duration != "" {
				args = append(args, "-duration", in.Duration)
			}
			if in.Stages != "" {
				args = append(args, "-stages", in.Stages)
			}
			if in.Rate > 0 {
				args = append(args, "-rate", strconv.FormatFloat(in.Rate, 'f', -1, 64))
			}
			if in.MaxVUs > 0 {
				args = append(args, "-max-vus", strconv.Itoa(in.MaxVUs))
			}
			if in.Target != "" {
				args = append(args, "-target", in.Target)
			}
			if in.Protocol != "" {
				args = append(args, "-protocol", in.Protocol)
			}
			for _, t := range in.AllowTargets {
				args = append(args, "-allow-target", t)
			}
			for _, t := range in.Thresholds {
				args = append(args, "-threshold", t)
			}
			if in.Yes {
				args = append(args, "-yes")
			}
			if in.BaselinePath != "" {
				args = append(args, "-baseline", in.BaselinePath)
			}
			if in.MaxRegression != nil {
				args = append(args, "-max-regression", strconv.FormatFloat(*in.MaxRegression, 'f', -1, 64))
			}
			if in.JUnitPath != "" {
				args = append(args, "-junit", in.JUnitPath)
			}
			if in.NoReport {
				args = append(args, "-no-report")
			} else if reportPath != "" {
				args = append(args, "-report", reportPath)
			}
			if in.ScenarioPath != "" {
				args = append(args, in.ScenarioPath) // positional: must come last, flag.Parse stops at the first non-flag argument
			}

			stdout, err := RunCLI(ctx, exePath, args...)
			// Exit 3 means the run finished but broke a threshold. That
			// is a result the caller asked for, not a tool failure, so
			// the structured result is returned with thresholds_passed
			// false. Any other non-zero exit is still an error.
			var exitErr *exec.ExitError
			breached := errors.As(err, &exitErr) && exitErr.ExitCode() == exitThresholdsBreached
			if err != nil && !breached {
				return nil, err
			}
			var result report.Result
			if err := json.Unmarshal(stdout, &result); err != nil {
				return nil, fmt.Errorf("parsing `vegaload run` output: %w", err)
			}
			out := map[string]any{"result": result}
			if reportPath != "" {
				out["report_path"] = reportPath
			}
			return out, nil
		},
	}
}

// --- get_results : reads a previously-written report.Result JSON file ---

type getResultsArgs struct {
	ReportPath string `json:"report_path"`
}

// getResultsTool does not shell out to vegaload at all: a report's JSON
// is a static file already sitting on disk (written by run_test above,
// or by `vegaload run -out ...` / report.WriteJSON directly), so reading
// it is exactly what a human would do with `cat` — no engine or
// scenario logic runs, which is why this is still compliant with
// AGENTS.md's module-boundary rule despite not calling RunCLI.
func getResultsTool() Tool {
	return Tool{
		Name:        "get_results",
		Description: "Read a previously-written VegaLoad JSON report (from run_test's report_path, or `vegaload run -out`) and return its full report.Result.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"report_path": map[string]any{"type": "string", "description": "path to a vegaload JSON report"}},
			"required":   []string{"report_path"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in getResultsArgs
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			if in.ReportPath == "" {
				return nil, fmt.Errorf("report_path is required")
			}
			data, err := os.ReadFile(in.ReportPath)
			if err != nil {
				return nil, fmt.Errorf("reading %s: %w", in.ReportPath, err)
			}
			var result report.Result
			if err := json.Unmarshal(data, &result); err != nil {
				return nil, fmt.Errorf("%s does not look like a vegaload JSON report: %w", in.ReportPath, err)
			}
			return result, nil
		},
	}
}

// --- suggest_thresholds : wraps `vegaload diagnose <report> -no-llm` ---

type suggestThresholdsArgs struct {
	ReportPath string `json:"report_path"`
}

func suggestThresholdsTool(exePath string) Tool {
	return Tool{
		Name:        "suggest_thresholds",
		Description: "Suggest pass/fail thresholds (p95 latency, error rate) derived from a baseline run's report. Equivalent to `vegaload diagnose <report.json> -no-llm`.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"report_path": map[string]any{"type": "string", "description": "path to a vegaload JSON report"}},
			"required":   []string{"report_path"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in suggestThresholdsArgs
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			if in.ReportPath == "" {
				return nil, fmt.Errorf("report_path is required")
			}
			stdout, err := RunCLI(ctx, exePath, "diagnose", "-output", "json", "-no-llm", in.ReportPath)
			if err != nil {
				return nil, err
			}
			var parsed struct {
				SuggestedThresholds json.RawMessage `json:"suggested_thresholds"`
			}
			if err := json.Unmarshal(stdout, &parsed); err != nil {
				return nil, fmt.Errorf("parsing `vegaload diagnose` output: %w", err)
			}
			var thresholds map[string]any
			if err := json.Unmarshal(parsed.SuggestedThresholds, &thresholds); err != nil {
				return nil, fmt.Errorf("parsing suggested_thresholds: %w", err)
			}
			return thresholds, nil
		},
	}
}

// --- diagnose_failure : wraps `vegaload diagnose <report>` with BYO-LLM narration ---

type diagnoseFailureArgs struct {
	ReportPath string `json:"report_path"`
	NoLLM      bool   `json:"no_llm,omitempty"`
}

func diagnoseFailureTool(exePath string) Tool {
	return Tool{
		Name: "diagnose_failure",
		Description: "Explain a run's results: rule-based findings (failure rate, latency long tail, whether " +
			"failures were transient or spread out) plus, if VEGALOAD_LLM_PROVIDER is configured in this server's " +
			"environment, a plain-English narrative. Equivalent to `vegaload diagnose <report.json>`.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"report_path": map[string]any{"type": "string", "description": "path to a vegaload JSON report"},
				"no_llm":      map[string]any{"type": "boolean", "description": "skip LLM narration even if configured"},
			},
			"required": []string{"report_path"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in diagnoseFailureArgs
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			if in.ReportPath == "" {
				return nil, fmt.Errorf("report_path is required")
			}
			args := []string{"diagnose", "-output", "json"}
			if in.NoLLM {
				args = append(args, "-no-llm")
			}
			args = append(args, in.ReportPath)
			stdout, err := RunCLI(ctx, exePath, args...)
			if err != nil {
				return nil, err
			}
			var result map[string]any
			if err := json.Unmarshal(stdout, &result); err != nil {
				return nil, fmt.Errorf("parsing `vegaload diagnose` output: %w", err)
			}
			return result, nil
		},
	}
}

// --- compare_reports : wraps `vegaload compare <baseline> <candidate>` ---

type compareReportsArgs struct {
	BaselinePath   string   `json:"baseline_path"`
	CandidatePath  string   `json:"candidate_path"`
	ErrorRateDelta *float64 `json:"error_rate_delta,omitempty"`
	P95Ratio       *float64 `json:"p95_ratio,omitempty"`
}

func compareReportsTool(exePath string) Tool {
	return Tool{
		Name: "compare_reports",
		Description: "Diff a candidate JSON report against a baseline JSON report (both from " +
			"`vegaload run -out` / run_test). Returns metric deltas and whether error rate or p95 " +
			"latency regressed. Equivalent to `vegaload compare <baseline.json> <candidate.json>`.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"baseline_path":    map[string]any{"type": "string", "description": "path to the baseline vegaload JSON report"},
				"candidate_path":   map[string]any{"type": "string", "description": "path to the candidate vegaload JSON report"},
				"error_rate_delta": map[string]any{"type": "number", "description": "absolute error-rate slack before counting as a regression (0.01 = one percentage point)"},
				"p95_ratio":        map[string]any{"type": "number", "description": "max allowed candidate/baseline p95 ratio (1.2 allows 20% headroom)"},
			},
			"required": []string{"baseline_path", "candidate_path"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in compareReportsArgs
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			if in.BaselinePath == "" || in.CandidatePath == "" {
				return nil, fmt.Errorf("baseline_path and candidate_path are required")
			}
			args := []string{"compare", "-output", "json"}
			if in.ErrorRateDelta != nil {
				args = append(args, "-error-rate-delta", strconv.FormatFloat(*in.ErrorRateDelta, 'f', -1, 64))
			}
			if in.P95Ratio != nil {
				args = append(args, "-p95-ratio", strconv.FormatFloat(*in.P95Ratio, 'f', -1, 64))
			}
			args = append(args, in.BaselinePath, in.CandidatePath)
			stdout, err := RunCLI(ctx, exePath, args...)
			// Exit 1 with a parseable compare Result means "regressed",
			// not a tool failure — return the structured result either way.
			var result map[string]any
			if uerr := json.Unmarshal(stdout, &result); uerr == nil {
				if _, ok := result["regressed"]; ok {
					return result, nil
				}
			}
			if err != nil {
				return nil, err
			}
			return result, nil
		},
	}
}

// --- generate_from_spec : wraps `vegaload new -from-openapi <spec>` ---

type generateFromSpecArgs struct {
	SpecPath string `json:"spec_path"`
	Name     string `json:"name,omitempty"`
	Force    bool   `json:"force,omitempty"`
}

func generateFromSpecTool(exePath string) Tool {
	return Tool{
		Name: "generate_from_spec",
		Description: "Generate a load-test runbook from a JSON OpenAPI spec: one ready-to-run `vegaload run` " +
			"command per endpoint the spec declares. Equivalent to `vegaload new -from-openapi <spec>`. YAML specs " +
			"are not supported — convert to JSON first. This does not produce a runnable scenario script, since " +
			"a spec describes each endpoint on its own, not how their responses should chain together; it " +
			"produces the exact command to run each endpoint protocol-direct instead, which you can also use " +
			"as a checklist for a hand-written scenario if the endpoints should be chained.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"spec_path": map[string]any{"type": "string", "description": "path to a JSON OpenAPI document"},
				"name":      map[string]any{"type": "string", "description": "base name for the generated runbook file (default: \"scenario\")"},
				"force":     map[string]any{"type": "boolean", "description": "overwrite the runbook file if it already exists"},
			},
			"required": []string{"spec_path"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in generateFromSpecArgs
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			if in.SpecPath == "" {
				return nil, fmt.Errorf("spec_path is required")
			}
			args := []string{"new", "-from-openapi", in.SpecPath, "-output", "json"}
			if in.Force {
				args = append(args, "-force")
			}
			if in.Name != "" {
				args = append(args, in.Name)
			}
			stdout, err := RunCLI(ctx, exePath, args...)
			if err != nil {
				return nil, err
			}
			var result map[string]any
			if err := json.Unmarshal(stdout, &result); err != nil {
				return nil, fmt.Errorf("parsing `vegaload new -from-openapi` output: %w", err)
			}
			return result, nil
		},
	}
}

// CoreToolNames are the tools every VegaLoad MCP server must expose
// (FR-MCP-03). `vegaload doctor` checks a host's live server against
// this list; a test keeps it in step with NewTools.
var CoreToolNames = []string{
	"create_scenario",
	"run_test",
	"get_results",
	"suggest_thresholds",
	"diagnose_failure",
	"compare_reports",
	"generate_from_spec",
	"validate_scenario",
}

// --- validate_scenario : wraps `vegaload validate` (FR-MCP-07) ---

type validateScenarioArgs struct {
	ScenarioPath string   `json:"scenario_path"`
	AllowTargets []string `json:"allow_targets,omitempty"`
	Yes          bool     `json:"yes,omitempty"`
	Timeout      string   `json:"timeout,omitempty"`
}

func validateScenarioTool(exePath string) Tool {
	return Tool{
		Name: "validate_scenario",
		Description: "Check a scenario before a real load test: run it once, with one user and one iteration, and " +
			"report whether it works. Equivalent to `vegaload validate`. This makes real network calls, under the same " +
			"host rules as run_test (a non-localhost host that isn't in allow_targets is refused unless yes is set). " +
			"Returns valid, and when it is not valid, stage (\"load\" if the script could not be read or started, " +
			"\"iteration\" if the iteration returned an error) and error. A scenario that is not valid is a normal " +
			"result, not a tool error, so read valid before running run_test. Any check() results are listed too; " +
			"a failed check does not make the scenario invalid.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"scenario_path": map[string]any{"type": "string", "description": "path to the scenario file (.vl.js or .py)"},
				"allow_targets": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "additional hosts allowed without confirmation"},
				"yes":           map[string]any{"type": "boolean", "description": "skip the confirmation gate for a non-allowlisted host"},
				"timeout":       map[string]any{"type": "string", "description": "time limit for the one iteration, e.g. \"30s\" (default 30s)"},
			},
			"required": []string{"scenario_path"},
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (any, error) {
			var in validateScenarioArgs
			if err := json.Unmarshal(raw, &in); err != nil {
				return nil, fmt.Errorf("invalid arguments: %w", err)
			}
			if in.ScenarioPath == "" {
				return nil, fmt.Errorf("scenario_path is required")
			}
			args := []string{"validate", "-output", "json", "-trigger", "mcp"}
			if in.Timeout != "" {
				args = append(args, "-timeout", in.Timeout)
			}
			for _, t := range in.AllowTargets {
				args = append(args, "-allow-target", t)
			}
			if in.Yes {
				args = append(args, "-yes")
			}
			args = append(args, in.ScenarioPath) // positional: must come last

			stdout, err := RunCLI(ctx, exePath, args...)
			// Exit 1 with a JSON body means the scenario is not valid.
			// That is the answer the caller asked for, not a failure of
			// the tool, so the result is returned as-is. No JSON body
			// means validate itself failed (for example a usage error).
			var result map[string]any
			if jerr := json.Unmarshal(stdout, &result); jerr != nil {
				if err != nil {
					return nil, err
				}
				return nil, fmt.Errorf("parsing `vegaload validate` output: %w", jerr)
			}
			return result, nil
		},
	}
}
