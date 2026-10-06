package report

import (
	"fmt"
	"html"
	"os"
	"strings"
)

// WriteHTML implements FR-RPT-01: one self-contained HTML file for res,
// with no server and nothing fetched at render time — every number is
// inlined, and the two charts are hand-drawn inline SVG (see the
// artifact-diagramming conventions this follows: currentColor strokes so
// the report reads in both light and dark browser themes, a viewBox
// sized to the content, labelled axes instead of a legend). The file
// opens correctly from disk with no network access at all, which is
// what "self-contained" means for FR-RPT-03's offline-renderable
// guarantee.
func WriteHTML(path string, res *Result) error {
	return os.WriteFile(path, []byte(renderHTML(res)), 0o644)
}

func renderHTML(res *Result) string {
	var b strings.Builder
	b.WriteString("<!doctype html>\n<html lang=\"en\"><head><meta charset=\"utf-8\">\n")
	b.WriteString("<title>VegaLoad report — " + html.EscapeString(res.Executor) + "</title>\n")
	b.WriteString(reportStyle)
	b.WriteString("</head><body>\n<main>\n")

	fmt.Fprintf(&b, "<h1>VegaLoad report</h1>\n")
	fmt.Fprintf(&b, "<p class=\"meta\">%s executor &middot; started %s &middot; elapsed %s</p>\n",
		html.EscapeString(res.Executor), res.StartedAt.Format("2006-01-02 15:04:05 MST"), res.Elapsed.Round(1e6))

	errPct := res.ErrorRate * 100
	fmt.Fprintf(&b, "<section class=\"summary\">\n")
	fmt.Fprintf(&b, "<div class=\"stat\"><span class=\"n\">%d</span><span class=\"l\">total</span></div>\n", res.Total)
	fmt.Fprintf(&b, "<div class=\"stat\"><span class=\"n\">%d</span><span class=\"l\">failed</span></div>\n", res.Failed)
	fmt.Fprintf(&b, "<div class=\"stat\"><span class=\"n\">%.2f%%</span><span class=\"l\">error rate</span></div>\n", errPct)
	fmt.Fprintf(&b, "</section>\n")

	b.WriteString(thresholdsSection(res))

	fmt.Fprintf(&b, "<section class=\"latency\">\n<h2>Latency</h2>\n<table>\n<tr><th>min</th><th>p50</th><th>p90</th><th>p95</th><th>p99</th><th>max</th><th>mean</th></tr>\n")
	fmt.Fprintf(&b, "<tr><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td><td>%s</td></tr>\n</table>\n</section>\n",
		res.Latency.Min.Round(1e3), res.Latency.P50.Round(1e3), res.Latency.P90.Round(1e3),
		res.Latency.P95.Round(1e3), res.Latency.P99.Round(1e3), res.Latency.Max.Round(1e3), res.Latency.Mean.Round(1e3))

	if len(res.TimeSeries) > 0 {
		b.WriteString("<section class=\"charts\">\n<h2>Over time</h2>\n")
		b.WriteString(rpsChart(res.TimeSeries))
		b.WriteString(errorRateChart(res.TimeSeries))
		b.WriteString("</section>\n")
	}

	b.WriteString("</main>\n</body></html>\n")
	return b.String()
}

// thresholdsSection renders the pass/fail thresholds table (FR-CLI-11),
// or nothing when the run had none. The verdict is written as the word
// PASS or FAIL, never colour alone, so it reads the same for a reader who
// cannot tell the colours apart and in a printout.
func thresholdsSection(res *Result) string {
	if len(res.Thresholds) == 0 {
		return ""
	}
	var b strings.Builder
	verdict, cls := "All thresholds passed", "pass"
	if res.ThresholdsPassed == nil || !*res.ThresholdsPassed {
		verdict, cls = "Thresholds breached", "fail"
	}
	fmt.Fprintf(&b, "<section class=\"thresholds\">\n<h2>Thresholds <span class=\"badge %s\">%s</span></h2>\n", cls, verdict)
	b.WriteString("<table>\n<tr><th>result</th><th>threshold</th><th>limit</th><th>observed</th></tr>\n")
	for _, t := range res.Thresholds {
		word, c := "PASS", "pass"
		if !t.Passed {
			word, c = "FAIL", "fail"
		}
		fmt.Fprintf(&b, "<tr><td><span class=\"badge %s\">%s</span></td><td>%s</td><td>%s %s</td><td>%s</td></tr>\n",
			c, word, html.EscapeString(t.Name), html.EscapeString(t.Operator), html.EscapeString(t.Value), html.EscapeString(t.Observed))
	}
	b.WriteString("</table>\n</section>\n")
	return b.String()
}

const chartW, chartH = 640.0, 160.0
const chartPad = 28.0

// rpsChart draws requests-per-second as an inline SVG line, following
// the artifact-diagramming skill's inline-SVG conventions: a viewBox
// sized to the content, currentColor stroke so it reads in both themes,
// axis labels on the mark itself rather than a separate legend, and a
// figcaption stating the one claim the figure makes.
func rpsChart(series []Point) string {
	maxRPS := 0.0
	for _, p := range series {
		if p.RPS > maxRPS {
			maxRPS = p.RPS
		}
	}
	if maxRPS == 0 {
		maxRPS = 1
	}
	points := polylinePoints(series, maxRPS, func(p Point) float64 { return p.RPS })
	return fmt.Sprintf(`<figure>
<svg viewBox="0 0 %.0f %.0f" role="img" aria-label="Requests per second over the run, peaking at %.1f">
<polyline points="%s" fill="none" stroke="currentColor" stroke-width="2"/>
<text x="%.0f" y="14" font-size="12">%.1f req/s peak</text>
<text x="%.0f" y="%.0f" font-size="12">0</text>
</svg>
<figcaption>Requests per second, one point per second of the run.</figcaption>
</figure>
`, chartW, chartH, maxRPS, points, chartPad, maxRPS, chartPad, chartH-6)
}

// errorRateChart draws error rate (0-100%) the same way rpsChart draws
// RPS, scaled to a fixed 0-100% axis rather than the observed max, since
// a flat 0% line (the common case) should visibly read as "no errors",
// not be stretched to fill the chart.
func errorRateChart(series []Point) string {
	points := polylinePoints(series, 1.0, func(p Point) float64 { return p.ErrorRate })
	maxErr := 0.0
	for _, p := range series {
		if p.ErrorRate*100 > maxErr {
			maxErr = p.ErrorRate * 100
		}
	}
	return fmt.Sprintf(`<figure>
<svg viewBox="0 0 %.0f %.0f" role="img" aria-label="Error rate over the run, peaking at %.1f percent">
<polyline points="%s" fill="none" stroke="currentColor" stroke-width="2"/>
<text x="%.0f" y="14" font-size="12">100%%</text>
<text x="%.0f" y="%.0f" font-size="12">0%%</text>
</svg>
<figcaption>Error rate, one point per second of the run (peak %.1f%%).</figcaption>
</figure>
`, chartW, chartH, maxErr, points, chartPad, chartPad, chartH-6, maxErr)
}

// polylinePoints maps series onto an SVG polyline's points attribute,
// scaling x across chartW and y (via value, inverted since SVG y grows
// downward) across chartH, within a fixed left/bottom margin for axis
// labels.
func polylinePoints(series []Point, max float64, value func(Point) float64) string {
	if len(series) == 0 || max == 0 {
		return ""
	}
	innerW := chartW - chartPad
	innerH := chartH - chartPad
	var sb strings.Builder
	for i, p := range series {
		x := chartPad + innerW*float64(i)/float64(maxInt(len(series)-1, 1))
		y := chartPad + innerH*(1-clamp01(value(p)/max))
		if i > 0 {
			sb.WriteByte(' ')
		}
		fmt.Fprintf(&sb, "%.1f,%.1f", x, y)
	}
	return sb.String()
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// reportStyle keeps the report's visual baseline in one place: a
// readable system-font stack, explicit light/dark backgrounds (no
// external stylesheet — the file must render with no network access),
// and currentColor-friendly contrast for the inline SVG charts above.
const reportStyle = `<style>
:root{--bg:#ffffff;--fg:#1d2433;--muted:#5a6477;--line:#d6dae3;--pass:#1a6b3a;--fail:#b3261e}
@media (prefers-color-scheme:dark){:root{--bg:#161a22;--fg:#e6e8ee;--muted:#9aa3b5;--line:#30384a;--pass:#5fd08a;--fail:#ff8a80}}
body{background:var(--bg);color:var(--fg);font-family:-apple-system,"Segoe UI",system-ui,sans-serif;margin:0;padding:24px}
main{max-width:720px;margin:0 auto}
h1{font-size:1.4rem;margin:0 0 4px}
.meta{color:var(--muted);font-size:.85rem;margin:0 0 20px}
.summary{display:flex;gap:24px;margin:0 0 24px}
.stat{display:flex;flex-direction:column}
.stat .n{font-size:1.6rem;font-weight:600}
.stat .l{color:var(--muted);font-size:.8rem}
table{border-collapse:collapse;width:100%;margin:0 0 20px;font-size:.9rem}
th,td{border:1px solid var(--line);padding:6px 10px;text-align:left}
figure{margin:0 0 20px}
figcaption{color:var(--muted);font-size:.8rem;margin-top:4px}
svg{width:100%;height:auto;color:var(--fg)}
svg text{fill:currentColor}
h2{font-size:1.05rem;margin:0 0 10px}
.badge{display:inline-block;border:1px solid currentColor;border-radius:4px;padding:0 6px;font-size:.78rem;font-weight:600}
.badge.pass{color:var(--pass)}
.badge.fail{color:var(--fail)}
</style>
`
