package report

import (
	"strings"
	"testing"
	"time"
)

func TestShowProtocolsAndViews(t *testing.T) {
	plain := &Result{Executor: "fixed-vus", Protocols: []ProtocolResult{{
		Name: "http", Total: 3, Operations: []OperationResult{{Name: "GET", Total: 3}},
	}}}
	if ShowProtocols(plain) {
		t.Fatal("a plain HTTP run should keep today's layout")
	}
	html := renderHTML(plain)
	md := MarkdownSummary(plain)
	if strings.Contains(html, "Protocols") || strings.Contains(md, "### Protocols") {
		t.Fatal("plain HTTP views include a protocol section")
	}

	none := &Result{Executor: "fixed-vus"}
	if renderHTML(none) != renderHTML(&Result{Executor: "fixed-vus"}) {
		t.Fatal("a report with no calls changed the HTML")
	}

	mixed := &Result{Executor: "fixed-vus", Elapsed: time.Second, Protocols: []ProtocolResult{
		{
			Name: "http", Total: 1, Latency: Latency{P50: time.Millisecond, P95: 2 * time.Millisecond, Max: 3 * time.Millisecond},
			Operations: []OperationResult{{Name: "GET", Total: 1, Latency: Latency{P95: 2 * time.Millisecond}}},
		},
		{
			Name: "<ftp>", Total: 2, Failed: 1, ErrorRate: 0.5,
			Latency:       Latency{P50: time.Millisecond, P95: 4 * time.Millisecond, Max: 5 * time.Millisecond},
			BytesSent:     2048,
			BytesReceived: 4096,
			Operations: []OperationResult{{
				Name: "roundtrip", Total: 2, Failed: 1, ErrorRate: 0.5,
				Latency: Latency{P95: 4 * time.Millisecond}, BytesSent: 2048, BytesReceived: 4096,
			}},
			Measures: []MeasureResult{{Name: "upload_time", Count: 2, Latency: Latency{P50: time.Millisecond, P95: 2 * time.Millisecond, Max: 2 * time.Millisecond}}},
		},
	}}
	if !ShowProtocols(mixed) {
		t.Fatal("two protocols should be shown")
	}
	page := renderHTML(mixed)
	if !strings.Contains(page, "<h2>Protocols</h2>") || !strings.Contains(page, "&lt;ftp&gt;") || strings.Contains(page, "<ftp>") {
		t.Fatalf("html did not escape the protocol name:\n%s", page)
	}
	if !strings.Contains(page, "&nbsp;&nbsp;roundtrip") || !strings.Contains(page, "upload_time") || !strings.Contains(page, "2.0 KiB") {
		t.Fatalf("html missing the operation, measure, or bytes:\n%s", page)
	}
	text := MarkdownSummary(mixed)
	if !strings.Contains(text, "### Protocols") || !strings.Contains(text, "<ftp>") || !strings.Contains(text, "upload_time") || !strings.Contains(text, "2.0 KiB") {
		t.Fatalf("markdown:\n%s", text)
	}

	xmlBytes, err := JUnitXML(mixed)
	if err != nil {
		t.Fatal(err)
	}
	xmlText := string(xmlBytes)
	for _, prop := range []string{
		`name="protocol.http.calls" value="1"`,
		`name="protocol.&lt;ftp&gt;.failed" value="1"`,
		`name="protocol.&lt;ftp&gt;.p95_ms" value="4"`,
		`name="protocol.&lt;ftp&gt;.bytes_sent" value="2048"`,
		`name="protocol.&lt;ftp&gt;.bytes_received" value="4096"`,
	} {
		if !strings.Contains(xmlText, prop) {
			t.Errorf("junit missing %s\n%s", prop, xmlText)
		}
	}
	if strings.Count(xmlText, "<testcase") != 0 {
		t.Fatalf("protocol properties became test cases:\n%s", xmlText)
	}

	onlyFTP := &Result{Protocols: []ProtocolResult{{Name: "ftp"}}}
	if !ShowProtocols(onlyFTP) {
		t.Fatal("one non-HTTP protocol should be shown")
	}
	if ShowProtocols(&Result{Protocols: []ProtocolResult{{Name: "http2"}}}) {
		t.Fatal("http2 alone should stay hidden")
	}
}

func TestFormatByteCount(t *testing.T) {
	if got := FormatByteCount(512); got != "512 B" {
		t.Errorf("512 = %s", got)
	}
	if got := FormatByteCount(1536); got != "1.5 KiB" {
		t.Errorf("1536 = %s", got)
	}
	if got := FormatByteCount(1024 * 1024 * 5 / 4); got != "1.2 MiB" {
		t.Errorf("1.25MiB = %s", got)
	}
}
