package threshold

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/vegaload/vegaload/internal/report"
)

func TestParse_ProtocolSelectors(t *testing.T) {
	cases := []struct {
		expr     string
		protocol string
		op       string
		step     string
	}{
		{`p95{protocol="ftp"} < 500ms`, "ftp", "", ""},
		{`p95{protocol='ftp'} < 500ms`, "ftp", "", ""},
		{`p95{protocol=ftp} < 500ms`, "ftp", "", ""},
		{`error_rate{protocol="ftp",op="roundtrip"} < 1%`, "ftp", "roundtrip", ""},
		{`error_rate{op="roundtrip",protocol="ftp"} < 1%`, "ftp", "roundtrip", ""},
		{`p95{step="login"} < 300ms`, "", "", "login"},
		{`fast: p95{protocol="FTP"} < 500ms`, "FTP", "", ""},
	}
	for _, tc := range cases {
		th, err := Parse(tc.expr)
		if err != nil {
			t.Errorf("%s: %v", tc.expr, err)
			continue
		}
		if th.Protocol != tc.protocol || th.Op != tc.op || th.Step != tc.step {
			t.Errorf("%s: protocol %q op %q step %q", tc.expr, th.Protocol, th.Op, th.Step)
		}
	}
}

func TestParse_ProtocolSelectorErrors(t *testing.T) {
	bad := []string{
		`p95{step="login",protocol="ftp"} < 1s`,
		`p95{protocol="ftp",step="login"} < 1s`,
		`error_rate{op="roundtrip"} < 1%`,
		`p95{foo="ftp"} < 1s`,
		`p95{protocol=""} < 1s`,
		`p95{protocol="ftp",op=""} < 1s`,
		`check_rate{protocol="ftp"} > 0.9`,
		`check_rate{protocol="ftp",op="roundtrip"} > 0.9`,
		`p95{Protocol="ftp"} < 1s`,
	}
	for _, expr := range bad {
		if _, err := Parse(expr); err == nil {
			t.Errorf("%s: want an error", expr)
		}
	}
	_, err := Parse(`p95{foo="x"} < 1s`)
	if err == nil || !strings.Contains(err.Error(), `{step=NAME}`) || !strings.Contains(err.Error(), `{protocol=NAME}`) || !strings.Contains(err.Error(), `{protocol=NAME,op=NAME}`) {
		t.Fatalf("unknown key: %v", err)
	}
}

func TestExpression_ProtocolRoundTrip(t *testing.T) {
	for _, expr := range []string{
		`p95{step="login"} < 300ms`,
		`p95{protocol="ftp"} < 500ms`,
		`error_rate{protocol="ftp",op="roundtrip"} < 1%`,
		`ftp-fast: p95{protocol="ftp"} < 500ms`,
		`rt-ok: error_rate{protocol="ftp",op="roundtrip"} < 1%`,
	} {
		first, err := Parse(expr)
		if err != nil {
			t.Fatal(err)
		}
		second, err := Parse(first.Expression())
		if err != nil {
			t.Fatal(err)
		}
		if first != second {
			t.Fatalf("%s -> %s -> %+v, want %+v", expr, first.Expression(), second, first)
		}
	}
}

func ftpResult() *report.Result {
	slow := report.Latency{P95: 40 * time.Millisecond}
	fast := report.Latency{P95: 5 * time.Millisecond}
	return &report.Result{
		Total: 100, Failed: 1, ErrorRate: 0.01, Elapsed: time.Second, Latency: slow,
		Protocols: []report.ProtocolResult{{
			Name: "ftp", Total: 10, Failed: 1, ErrorRate: 0.1, Latency: slow,
			Operations: []report.OperationResult{{
				Name: "roundtrip", Total: 4, Failed: 0, ErrorRate: 0, Latency: fast,
			}},
		}},
	}
}

func TestEvaluate_Protocol(t *testing.T) {
	res := ftpResult()
	cases := []struct {
		expr     string
		pass     bool
		observed string
	}{
		{`p95{protocol="ftp"} < 1s`, true, "40ms"},
		{`p95{protocol="ftp"} < 1ms`, false, "40ms"},
		{`error_rate{protocol="ftp"} < 5%`, false, "0.1"},
		{`error_rate{protocol="ftp",op="roundtrip"} < 1%`, true, "0"},
		{`p95{protocol="ftp",op="roundtrip"} < 10ms`, true, "5ms"},
		{`rps{protocol="ftp"} >= 10`, true, "10.00"},
		{`p95{protocol="mqtt"} < 1s`, false, `no calls to protocol "mqtt" ran`},
		{`p95{protocol="ftp",op="download"} < 1s`, false, `no calls to protocol "ftp" op "download" ran`},
		{`p95{protocol="FTP"} < 1s`, false, `no calls to protocol "FTP" ran`},
	}
	for _, tc := range cases {
		got := Evaluate([]Threshold{mustParse(t, tc.expr)}, res)[0]
		if got.Passed != tc.pass || got.Observed != tc.observed {
			t.Errorf("%s: passed %v observed %q, want passed %v observed %q", tc.expr, got.Passed, got.Observed, tc.pass, tc.observed)
		}
	}
	// A protocol threshold with no calls does not use the whole-run numbers.
	empty := &report.Result{Total: 10, Latency: report.Latency{P95: time.Millisecond}}
	got := Evaluate([]Threshold{mustParse(t, `p95{protocol="ftp"} < 1s`)}, empty)[0]
	if got.Passed || got.Observed != `no calls to protocol "ftp" ran` {
		t.Fatalf("empty run: %+v", got)
	}
}

func TestParseFile_Protocol(t *testing.T) {
	ts, err := ParseFile([]byte(`[
		{"name": "ftp-fast", "metric": "p95", "protocol": "ftp", "operator": "<", "value": "500ms"},
		{"metric": "error_rate", "protocol": "ftp", "op": "roundtrip", "operator": "<", "value": "1%"}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	if len(ts) != 2 || ts[0].Name != "ftp-fast" || ts[0].Protocol != "ftp" || ts[0].Op != "" {
		t.Fatalf("first %+v", ts)
	}
	if ts[1].Protocol != "ftp" || ts[1].Op != "roundtrip" {
		t.Fatalf("second %+v", ts[1])
	}
	if _, err := ParseFile([]byte(`[{"metric":"p95","step":"login","protocol":"ftp","operator":"<","value":"1s"}]`)); err == nil {
		t.Fatal("step and protocol together was accepted")
	}
	if _, err := ParseFile([]byte(`[{"metric":"p95","op":"roundtrip","operator":"<","value":"1s"}]`)); err == nil {
		t.Fatal("op without a protocol was accepted")
	}
}

func TestThresholdJSON_OmitsProtocolWhenUnset(t *testing.T) {
	b, err := json.Marshal(report.ThresholdResult{
		Name: "p95 < 300ms", Metric: "p95", Operator: "<", Value: "300ms", Observed: "1ms", Passed: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "protocol") || strings.Contains(string(b), `"op"`) || strings.Contains(string(b), "step") {
		t.Fatalf("whole-run threshold gained a selector field: %s", b)
	}
}
