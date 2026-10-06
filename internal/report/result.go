package report

import (
	"encoding/json"
	"os"
	"time"
)

// Result is a run's structured outcome: everything the HTML report
// renders, and the same shape FR-RPT-04 says the MCP get_results tool
// (Phase 2) must return, so an agent host can render its own view from
// exactly the same data a human reads in the HTML report.
type Result struct {
	Executor  string        `json:"executor"`
	StartedAt time.Time     `json:"started_at"`
	Elapsed   time.Duration `json:"elapsed_ns"`
	Total     int64         `json:"total"`
	Failed    int64         `json:"failed"`
	ErrorRate float64       `json:"error_rate"`
	Latency   Latency       `json:"latency"`
	// TimeSeries is RPS and error rate bucketed by one-second intervals
	// from the run's start (FR-RPT-01's "RPS over time" and "error rate
	// over time"). It stays small regardless of request volume — one
	// point per second of run duration, not one per request — which is
	// what lets the HTML report embed it directly even for a
	// 1,000,000-request run (NFR-03).
	TimeSeries []Point `json:"time_series"`

	// Thresholds is the verdict of each pass/fail threshold the run was
	// given (FR-CLI-11), in the order they were given. It is omitted when
	// the run had none, so reports from before thresholds existed, and
	// runs that do not use them, look exactly as before. ThresholdsPassed
	// is true only when every threshold passed.
	Thresholds       []ThresholdResult `json:"thresholds,omitempty"`
	ThresholdsPassed *bool             `json:"thresholds_passed,omitempty"`

	// Baseline is the verdict of the baseline gate (FR-CLI-14): this run
	// compared with a baseline report the user supplied. It is omitted
	// when the run had no baseline.
	Baseline *BaselineResult `json:"baseline,omitempty"`

	// Checks is the pass and fail count of each named check the scenario
	// made with check() (FR-CLI-12), in the order each name was first
	// seen. It is omitted when the scenario made no checks. A failed
	// check does not fail an iteration or count as a failed request: it
	// is its own measure, which a threshold can judge through the
	// check_rate metric.
	Checks []CheckResult `json:"checks,omitempty"`
}

// CheckResult is one named check's totals across the whole run.
type CheckResult struct {
	Name   string `json:"name"`
	Passes int64  `json:"passes"`
	Fails  int64  `json:"fails"`
}

// CheckRate is the share of all checks that passed, from 0 to 1. The
// second result is false when the run made no checks, since a rate of
// nothing is not a rate of 100%.
func (r *Result) CheckRate() (float64, bool) {
	var pass, total int64
	for _, c := range r.Checks {
		pass += c.Passes
		total += c.Passes + c.Fails
	}
	if total == 0 {
		return 0, false
	}
	return float64(pass) / float64(total), true
}

// ThresholdResult is one threshold's verdict: what was asked (a name,
// a metric, an operator and a value) and what the run observed. The
// shape is deliberately plain — a name, a metric, an operator, a value —
// so anything that reads a saved report can read it without knowing how
// VegaLoad evaluated it. Value and Observed are strings so durations
// ("300ms") and plain numbers share one field.
type ThresholdResult struct {
	Name     string `json:"name"`
	Metric   string `json:"metric"`
	Operator string `json:"operator"`
	Value    string `json:"value"`
	Observed string `json:"observed"`
	Passed   bool   `json:"passed"`
}

// BaselineResult is the verdict of a run's baseline gate (FR-CLI-14). It
// only covers the two gated metrics, p95 latency and error rate; the full
// comparison is what `vegaload compare` prints.
type BaselineResult struct {
	Path string `json:"path"`
	// MaxRegressionPercent is how much worse than the baseline each gated
	// metric may be before the gate fails.
	MaxRegressionPercent float64          `json:"max_regression_percent"`
	Passed               bool             `json:"passed"`
	Metrics              []BaselineMetric `json:"metrics"`
	Notes                []string         `json:"notes,omitempty"`
}

// BaselineMetric is one gated metric: its baseline and this run's value,
// in the unit given ("ns" for p95, "ratio" for the error rate).
type BaselineMetric struct {
	Name      string  `json:"name"`
	Baseline  float64 `json:"baseline"`
	Candidate float64 `json:"candidate"`
	Unit      string  `json:"unit"`
	Regressed bool    `json:"regressed"`
}

// Latency holds the percentiles and summary stats FR-RPT-01 asks for.
type Latency struct {
	Min  time.Duration `json:"min_ns"`
	Mean time.Duration `json:"mean_ns"`
	Max  time.Duration `json:"max_ns"`
	P50  time.Duration `json:"p50_ns"`
	P90  time.Duration `json:"p90_ns"`
	P95  time.Duration `json:"p95_ns"`
	P99  time.Duration `json:"p99_ns"`
}

// Point is one bucket of Result.TimeSeries.
type Point struct {
	Offset    time.Duration `json:"offset_ns"`
	Requests  int64         `json:"requests"`
	Failed    int64         `json:"failed"`
	RPS       float64       `json:"rps"`
	ErrorRate float64       `json:"error_rate"`
}

// WriteJSON implements FR-CLI-05's structured JSON output mode for a
// finished run's result: res marshalled as indented JSON to path.
func WriteJSON(path string, res *Result) error {
	data, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
