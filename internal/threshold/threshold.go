// Package threshold implements FR-CLI-11: pass/fail thresholds on a
// run's own results, so a run can gate a CI pipeline.
//
// A threshold is a name, a metric, an operator and a value, for example
//
//	fast-api: p95 < 300ms
//
// It is judged once, against the finished run's numbers — the latency
// percentiles, error rate, request rate and counts VegaLoad itself
// measured. It never looks outside the run, and it produces a verdict
// (pass or fail) and nothing else.
package threshold

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/vegaload/vegaload/internal/report"
)

// Operators a threshold can use, longest first so "<=" is matched before "<".
var operators = []string{"<=", ">=", "<", ">"}

// kind says how a metric's value is written and compared.
type kind int

const (
	kindDuration kind = iota // 300ms, 1.5s
	kindRatio                // 0.01 or 1%
	kindNumber               // 120.5
	kindCount                // 100
)

var metrics = map[string]kind{
	"p50":        kindDuration,
	"p90":        kindDuration,
	"p95":        kindDuration,
	"p99":        kindDuration,
	"mean":       kindDuration,
	"min":        kindDuration,
	"max":        kindDuration,
	"error_rate": kindRatio,
	"rps":        kindNumber,
	"failed":     kindCount,
	"total":      kindCount,
	// check_rate is the share of check() calls that passed (FR-CLI-12).
	"check_rate": kindRatio,
}

// MetricNames lists the metrics a threshold can use, for help text and
// error messages.
func MetricNames() []string {
	return []string{"p50", "p90", "p95", "p99", "mean", "min", "max", "error_rate", "rps", "failed", "total", "check_rate"}
}

// Threshold is one parsed pass/fail threshold.
type Threshold struct {
	Name     string
	Metric   string
	Operator string
	// Value is the canonical text of the limit: a duration such as
	// "300ms", or a plain number such as "0.01".
	Value string

	limit float64 // nanoseconds for durations, else the number itself
}

// Parse parses one threshold expression: an optional "name:" prefix,
// then metric, operator and value, e.g. "p95 < 300ms" or
// "fast-api: error_rate <= 1%".
func Parse(expr string) (Threshold, error) {
	orig := expr
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return Threshold{}, fmt.Errorf("empty threshold")
	}

	name := ""
	if n, rest, ok := strings.Cut(expr, ":"); ok {
		name = strings.TrimSpace(n)
		expr = strings.TrimSpace(rest)
		if name == "" {
			return Threshold{}, fmt.Errorf("threshold %q: the name before ':' is empty", orig)
		}
	}

	idx := strings.IndexAny(expr, "<>")
	if idx < 0 {
		return Threshold{}, fmt.Errorf("threshold %q: want metric, operator and value, e.g. \"p95 < 300ms\" (operators: <, <=, >, >=)", orig)
	}
	metric := strings.ToLower(strings.TrimSpace(expr[:idx]))
	rest := expr[idx:]
	op := ""
	for _, o := range operators {
		if strings.HasPrefix(rest, o) {
			op = o
			break
		}
	}
	valueText := strings.TrimSpace(rest[len(op):])

	k, ok := metrics[metric]
	if !ok {
		return Threshold{}, fmt.Errorf("threshold %q: unknown metric %q (want one of: %s)", orig, metric, strings.Join(MetricNames(), ", "))
	}
	if valueText == "" {
		return Threshold{}, fmt.Errorf("threshold %q: missing value after %q", orig, op)
	}

	limit, canonical, err := parseValue(k, metric, valueText)
	if err != nil {
		return Threshold{}, fmt.Errorf("threshold %q: %w", orig, err)
	}

	t := Threshold{Name: name, Metric: metric, Operator: op, Value: canonical, limit: limit}
	if t.Name == "" {
		t.Name = fmt.Sprintf("%s %s %s", t.Metric, t.Operator, t.Value)
	}
	return t, nil
}

func parseValue(k kind, metric, text string) (limit float64, canonical string, err error) {
	switch k {
	case kindDuration:
		d, derr := time.ParseDuration(text)
		if derr != nil {
			return 0, "", fmt.Errorf("%s needs a duration with a unit, like 300ms or 1.5s, not %q", metric, text)
		}
		if d < 0 {
			return 0, "", fmt.Errorf("%s cannot be negative", metric)
		}
		return float64(d.Nanoseconds()), d.String(), nil
	case kindRatio:
		if p, ok := strings.CutSuffix(text, "%"); ok {
			f, perr := strconv.ParseFloat(strings.TrimSpace(p), 64)
			if perr != nil || f < 0 || f > 100 {
				return 0, "", fmt.Errorf("%s %q: want a percentage from 0%% to 100%%", metric, text)
			}
			f /= 100
			return f, formatNumber(f), nil
		}
		f, perr := strconv.ParseFloat(text, 64)
		if perr != nil || f < 0 || f > 1 {
			return 0, "", fmt.Errorf("%s %q: want a fraction from 0 to 1 (like 0.01), or a percentage (like 1%%)", metric, text)
		}
		return f, formatNumber(f), nil
	case kindCount:
		f, perr := strconv.ParseFloat(text, 64)
		if perr != nil || f < 0 || f != float64(int64(f)) {
			return 0, "", fmt.Errorf("%s %q: want a whole number", metric, text)
		}
		return f, formatNumber(f), nil
	default:
		f, perr := strconv.ParseFloat(text, 64)
		if perr != nil || f < 0 {
			return 0, "", fmt.Errorf("%s %q: want a number", metric, text)
		}
		return f, formatNumber(f), nil
	}
}

func formatNumber(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// ParseAll parses several expressions, failing on the first bad one.
func ParseAll(exprs []string) ([]Threshold, error) {
	out := make([]Threshold, 0, len(exprs))
	for _, e := range exprs {
		t, err := Parse(e)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, nil
}

// Expression returns the threshold as text that Parse accepts again,
// including its name, e.g. "fast-api: p95 < 300ms". It is what other
// commands print as a ready-to-paste -threshold flag.
func (t Threshold) Expression() string {
	core := fmt.Sprintf("%s %s %s", t.Metric, t.Operator, t.Value)
	if t.Name == core {
		return core
	}
	return t.Name + ": " + core
}

// Evaluate judges every threshold against a finished run's result, in
// order. A run that completed no requests fails every threshold: with
// nothing measured, "p95 < 300ms" would otherwise pass trivially.
func Evaluate(ts []Threshold, res *report.Result) []report.ThresholdResult {
	out := make([]report.ThresholdResult, 0, len(ts))
	for _, t := range ts {
		r := report.ThresholdResult{Name: t.Name, Metric: t.Metric, Operator: t.Operator, Value: t.Value}
		if t.Metric == "check_rate" {
			rate, any := res.CheckRate()
			if !any {
				r.Observed = "no checks were made"
				out = append(out, r)
				continue
			}
			r.Observed = formatObserved(t.Metric, rate)
			r.Passed = compare(rate, t.Operator, t.limit)
			out = append(out, r)
			continue
		}
		if res.Total == 0 {
			r.Observed = "no requests completed"
			out = append(out, r)
			continue
		}
		observed := observe(t.Metric, res)
		r.Observed = formatObserved(t.Metric, observed)
		r.Passed = compare(observed, t.Operator, t.limit)
		out = append(out, r)
	}
	return out
}

// AllPassed reports whether every result passed. It is true for an
// empty list, since a run with no thresholds has nothing to breach.
func AllPassed(rs []report.ThresholdResult) bool {
	for _, r := range rs {
		if !r.Passed {
			return false
		}
	}
	return true
}

func observe(metric string, res *report.Result) float64 {
	switch metric {
	case "p50":
		return float64(res.Latency.P50.Nanoseconds())
	case "p90":
		return float64(res.Latency.P90.Nanoseconds())
	case "p95":
		return float64(res.Latency.P95.Nanoseconds())
	case "p99":
		return float64(res.Latency.P99.Nanoseconds())
	case "mean":
		return float64(res.Latency.Mean.Nanoseconds())
	case "min":
		return float64(res.Latency.Min.Nanoseconds())
	case "max":
		return float64(res.Latency.Max.Nanoseconds())
	case "error_rate":
		return res.ErrorRate
	case "rps":
		if res.Elapsed <= 0 {
			return 0
		}
		return float64(res.Total) / res.Elapsed.Seconds()
	case "failed":
		return float64(res.Failed)
	case "total":
		return float64(res.Total)
	}
	return 0
}

func formatObserved(metric string, v float64) string {
	switch metrics[metric] {
	case kindDuration:
		return time.Duration(int64(v)).Round(10 * time.Microsecond).String()
	case kindRatio:
		return strconv.FormatFloat(v, 'g', 4, 64)
	case kindNumber:
		return strconv.FormatFloat(v, 'f', 2, 64)
	default:
		return strconv.FormatFloat(v, 'f', 0, 64)
	}
}

func compare(observed float64, op string, limit float64) bool {
	switch op {
	case "<":
		return observed < limit
	case "<=":
		return observed <= limit
	case ">":
		return observed > limit
	case ">=":
		return observed >= limit
	}
	return false
}
