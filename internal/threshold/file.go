package threshold

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

// fileEntry is one threshold in a thresholds file.
type fileEntry struct {
	Name     string          `json:"name"`
	Metric   string          `json:"metric"`
	Operator string          `json:"operator"`
	Value    json.RawMessage `json:"value"`
}

// LoadFile reads thresholds from a JSON file (the -thresholds flag).
//
// Three shapes are accepted, so a file can come straight from another
// command's output:
//
//	[{"name": "fast", "metric": "p95", "operator": "<", "value": "300ms"}]
//	{"thresholds": [ ...same list... ]}
//	{"latency_p95": "300ms", "error_rate": 0.015}
//
// The last is what `vegaload diagnose -output json` suggests, under a
// "suggested_thresholds" key, which is also accepted. The suggested
// values are ceilings, so they become "<=" thresholds.
func LoadFile(path string) ([]Threshold, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading thresholds file: %w", err)
	}
	ts, err := ParseFile(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return ts, nil
}

// ParseFile parses the contents of a thresholds file. See LoadFile.
func ParseFile(data []byte) ([]Threshold, error) {
	data = bytes.TrimSpace(data)
	if len(data) == 0 {
		return nil, fmt.Errorf("the thresholds file is empty")
	}

	if data[0] == '[' {
		return fromEntries(data)
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, fmt.Errorf("not valid JSON: %w", err)
	}
	for _, key := range []string{"thresholds", "suggested_thresholds"} {
		raw, ok := obj[key]
		if !ok {
			continue
		}
		if trimmed := bytes.TrimSpace(raw); len(trimmed) > 0 && trimmed[0] == '[' {
			return fromEntries(raw)
		}
		return fromSuggested(raw)
	}
	return fromSuggested(data)
}

func fromEntries(data []byte) ([]Threshold, error) {
	var entries []fileEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, fmt.Errorf("want a list of {name, metric, operator, value}: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("the thresholds file has no thresholds")
	}
	out := make([]Threshold, 0, len(entries))
	for i, e := range entries {
		val, err := rawText(e.Value)
		if err != nil {
			return nil, fmt.Errorf("threshold %d: value: %w", i+1, err)
		}
		expr := e.Metric + " " + e.Operator + " " + val
		if e.Name != "" {
			expr = e.Name + ": " + expr
		}
		t, err := Parse(expr)
		if err != nil {
			return nil, fmt.Errorf("threshold %d: %w", i+1, err)
		}
		out = append(out, t)
	}
	return out, nil
}

// fromSuggested reads the {"latency_p95": ..., "error_rate": ...} shape.
func fromSuggested(data []byte) ([]Threshold, error) {
	var obj struct {
		LatencyP95 json.RawMessage `json:"latency_p95"`
		ErrorRate  json.RawMessage `json:"error_rate"`
		// The suggestion also carries a display copy of the error rate
		// and ready-made flag text; neither is needed to load it.
		ErrorRatePct   json.RawMessage `json:"error_rate_pct"`
		ThresholdFlags json.RawMessage `json:"threshold_flags"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&obj); err != nil {
		return nil, fmt.Errorf("want a list of thresholds, or {\"latency_p95\": \"300ms\", \"error_rate\": 0.01}: %w", err)
	}
	var out []Threshold
	if len(obj.LatencyP95) > 0 {
		v, err := rawText(obj.LatencyP95)
		if err != nil {
			return nil, fmt.Errorf("latency_p95: %w", err)
		}
		t, err := Parse("p95 <= " + v)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if len(obj.ErrorRate) > 0 {
		v, err := rawText(obj.ErrorRate)
		if err != nil {
			return nil, fmt.Errorf("error_rate: %w", err)
		}
		t, err := Parse("error_rate <= " + v)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("the thresholds file has no thresholds")
	}
	return out, nil
}

// rawText turns a JSON string or number into the text Parse expects.
func rawText(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" {
		return "", fmt.Errorf("missing")
	}
	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(raw, &s); err != nil {
			return "", err
		}
		return s, nil
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return "", fmt.Errorf("want a string or a number")
	}
	return strconv.FormatFloat(f, 'g', -1, 64), nil
}
