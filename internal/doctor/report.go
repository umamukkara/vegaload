package doctor

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// redact shortens the user's home directory to ~ so output is safe to
// paste into an issue. Secrets are never printed in the first place.
func redact(s, home string) string {
	if home == "" || home == "/" {
		return s
	}
	return strings.ReplaceAll(s, home, "~")
}

func redactResult(r Result, home string) Result {
	r.Message = redact(r.Message, home)
	r.Fix = redact(r.Fix, home)
	r.FixPlan = redact(r.FixPlan, home)
	r.FixError = redact(r.FixError, home)
	if len(r.Detail) > 0 {
		d := make([]string, len(r.Detail))
		for i, s := range r.Detail {
			d[i] = redact(s, home)
		}
		r.Detail = d
	}
	return r
}

func symbol(s Status) string {
	switch s {
	case Pass:
		return "✓"
	case Warn:
		return "!"
	case Fail:
		return "✗"
	}
	return "-"
}

// WriteText prints the human-readable report.
func WriteText(w io.Writer, rep Report, home string, verbose bool) {
	fmt.Fprintf(w, "vegaload doctor  v%s  %s/%s\n", rep.Version, rep.OS, rep.Arch)
	fmt.Fprintf(w, "project: %s\n", redact(rep.Dir, home))
	cat := ""
	for _, r := range rep.Results {
		r = redactResult(r, home)
		if r.Category != cat {
			cat = r.Category
			fmt.Fprintf(w, "\n%s\n", cat)
		}
		fmt.Fprintf(w, "  %s %-26s %s\n", symbol(r.Status), r.ID, r.Message)
		if verbose || r.Status == Warn || r.Status == Fail {
			for _, d := range r.Detail {
				fmt.Fprintf(w, "      %s\n", d)
			}
		}
		if r.Fix != "" && (r.Status == Warn || r.Status == Fail) {
			fmt.Fprintf(w, "      fix: %s\n", r.Fix)
		}
		if r.FixPlan != "" {
			fmt.Fprintf(w, "      --fix would: %s\n", r.FixPlan)
		} else if r.Fixable && !r.Fixed && r.Fix != "" && (r.Status == Warn || r.Status == Fail) {
			fmt.Fprintf(w, "      or run with -fix to do this for you\n")
		}
		if r.FixError != "" {
			fmt.Fprintf(w, "      fix failed: %s\n", r.FixError)
		}
	}
	s := rep.Summary
	fmt.Fprintf(w, "\n%d passed, %d warnings, %d failed, %d skipped", s.Pass, s.Warn, s.Fail, s.Skip)
	if s.Fixed > 0 {
		fmt.Fprintf(w, ", %d fixed", s.Fixed)
	}
	fmt.Fprintln(w)
}

// WriteJSON prints the whole report as one JSON document (FR-CLI-05).
func WriteJSON(w io.Writer, rep Report, home string) error {
	out := rep
	out.Dir = redact(rep.Dir, home)
	out.Results = make([]Result, len(rep.Results))
	for i, r := range rep.Results {
		out.Results[i] = redactResult(r, home)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// WriteJSONL prints one JSON object per line: a "check" line per result,
// then one "summary" line (FR-CLI-05's streaming mode).
func WriteJSONL(w io.Writer, rep Report, home string) error {
	enc := json.NewEncoder(w)
	for _, r := range rep.Results {
		line := struct {
			Type string `json:"type"`
			Result
		}{"check", redactResult(r, home)}
		if err := enc.Encode(line); err != nil {
			return err
		}
	}
	return enc.Encode(struct {
		Type    string  `json:"type"`
		Version string  `json:"version"`
		Summary Summary `json:"summary"`
	}{"summary", rep.Version, rep.Summary})
}
