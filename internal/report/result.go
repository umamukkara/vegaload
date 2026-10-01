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
