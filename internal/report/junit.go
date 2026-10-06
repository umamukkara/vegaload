package report

import (
	"encoding/xml"
	"fmt"
	"os"
	"strconv"
	"time"
)

// JUnit XML (FR-RPT-05) lets a CI system show a run's verdicts natively.
// Each threshold (FR-CLI-11), each check (FR-CLI-12) and the baseline
// gate (FR-CLI-14) is one test case, so what the CI shows is what the
// text summary shows. A run with none of these has an empty suite.
//
// A failed check is a failed test case here, even though a failed check
// does not change the exit code of `vegaload run`: the report says what
// happened, and the exit code is the gate.

type junitSuites struct {
	XMLName  xml.Name     `xml:"testsuites"`
	Name     string       `xml:"name,attr"`
	Tests    int          `xml:"tests,attr"`
	Failures int          `xml:"failures,attr"`
	Time     string       `xml:"time,attr"`
	Suites   []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	Name       string          `xml:"name,attr"`
	Tests      int             `xml:"tests,attr"`
	Failures   int             `xml:"failures,attr"`
	Errors     int             `xml:"errors,attr"`
	Skipped    int             `xml:"skipped,attr"`
	Time       string          `xml:"time,attr"`
	Timestamp  string          `xml:"timestamp,attr,omitempty"`
	Properties []junitProperty `xml:"properties>property"`
	Cases      []junitCase     `xml:"testcase"`
}

type junitProperty struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type junitCase struct {
	ClassName string        `xml:"classname,attr"`
	Name      string        `xml:"name,attr"`
	Time      string        `xml:"time,attr"`
	Failure   *junitFailure `xml:"failure,omitempty"`
}

type junitFailure struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Body    string `xml:",chardata"`
}

// JUnitXML renders res as JUnit XML.
func JUnitXML(res *Result) ([]byte, error) {
	suite := junitSuite{
		Name:    "vegaload",
		Time:    seconds(res.Elapsed),
		Skipped: 0,
		Properties: []junitProperty{
			{"executor", res.Executor},
			{"total_requests", strconv.FormatInt(res.Total, 10)},
			{"failed_requests", strconv.FormatInt(res.Failed, 10)},
			{"error_rate", fmt.Sprintf("%.4f", res.ErrorRate)},
			{"p95", res.Latency.P95.String()},
		},
	}
	if !res.StartedAt.IsZero() {
		suite.Timestamp = res.StartedAt.UTC().Format("2006-01-02T15:04:05")
	}

	for _, t := range res.Thresholds {
		c := junitCase{ClassName: "vegaload.thresholds", Name: t.Name, Time: "0"}
		if !t.Passed {
			msg := fmt.Sprintf("%s %s %s: observed %s", t.Metric, t.Operator, t.Value, t.Observed)
			c.Failure = &junitFailure{Message: msg, Type: "threshold", Body: msg}
		}
		suite.Cases = append(suite.Cases, c)
	}
	for _, ch := range res.Checks {
		c := junitCase{ClassName: "vegaload.checks", Name: ch.Name, Time: "0"}
		if ch.Fails > 0 {
			all := ch.Passes + ch.Fails
			msg := fmt.Sprintf("%d of %d failed (%.2f%% passed)", ch.Fails, all, 100*float64(ch.Passes)/float64(all))
			c.Failure = &junitFailure{Message: msg, Type: "check", Body: msg}
		}
		suite.Cases = append(suite.Cases, c)
	}
	if b := res.Baseline; b != nil {
		c := junitCase{ClassName: "vegaload.baseline", Name: "within baseline " + b.Path, Time: "0"}
		if !b.Passed {
			msg := fmt.Sprintf("worse than the baseline by more than %g%%", b.MaxRegressionPercent)
			body := msg
			for _, n := range b.Notes {
				body += "\n" + n
			}
			c.Failure = &junitFailure{Message: msg, Type: "baseline", Body: body}
		}
		suite.Cases = append(suite.Cases, c)
	}

	suite.Tests = len(suite.Cases)
	for _, c := range suite.Cases {
		if c.Failure != nil {
			suite.Failures++
		}
	}
	out := junitSuites{Name: "vegaload", Tests: suite.Tests, Failures: suite.Failures, Time: suite.Time, Suites: []junitSuite{suite}}
	body, err := xml.MarshalIndent(out, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), append(body, '\n')...), nil
}

// WriteJUnit writes res as JUnit XML to path.
func WriteJUnit(path string, res *Result) error {
	data, err := JUnitXML(res)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func seconds(d time.Duration) string {
	return strconv.FormatFloat(d.Seconds(), 'f', 3, 64)
}
