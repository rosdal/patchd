// Package report writes scan results: a readable terminal report, SARIF
// 2.1.0 for code scanning, and Patchd's own JSON.
package report

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/patchd/patchd/engine"
	"github.com/patchd/patchd/engine/rules"
)

// PrettyOptions tune the terminal report.
type PrettyOptions struct {
	Color   bool // ANSI colours (off for NO_COLOR, pipes and files)
	Verbose bool // also print fingerprints and confidence
}

const (
	reset  = "\x1b[0m"
	bold   = "\x1b[1m"
	dim    = "\x1b[2m"
	red    = "\x1b[31m"
	yellow = "\x1b[33m"
	blue   = "\x1b[34m"
	cyan   = "\x1b[36m"
)

var severityColor = map[rules.Severity]string{
	rules.Critical: bold + red,
	rules.High:     red,
	rules.Medium:   yellow,
	rules.Low:      blue,
}

// Pretty writes findings grouped by file, with each trace as numbered steps
// and their code.
func Pretty(w io.Writer, res *engine.Result, opts PrettyOptions) error {
	paint := func(code, s string) string {
		if !opts.Color || code == "" {
			return s
		}
		return code + s + reset
	}
	pw := &errWriter{w: w}
	names := map[string]string{}
	for _, r := range res.Rules {
		names[r.ID] = r.Name
	}
	file := ""
	for _, f := range res.Findings {
		if f.Location.Path != file {
			if file != "" {
				pw.printf("\n")
			}
			file = f.Location.Path
			pw.printf("%s\n", paint(bold+cyan, file))
		}
		pw.printf("\n  %s %s %s  %s\n",
			paint(severityColor[f.Severity], strings.ToUpper(string(f.Severity))),
			paint(bold, f.RuleID),
			names[f.RuleID],
			paint(dim, fmt.Sprintf("%d:%d", f.Location.StartLine, f.Location.StartColumn)))
		pw.printf("  %s\n", f.Message)
		if f.OtherSources > 0 {
			pw.printf("  %s\n", paint(dim, fmt.Sprintf("(%d more source(s) reach the same call)", f.OtherSources)))
		}
		for i, st := range f.Trace {
			pw.printf("    %s %s  %s\n", paint(bold, fmt.Sprintf("%d.", i+1)), paint(dim, fmt.Sprintf("%d:%d", st.Location.StartLine, st.Location.StartColumn)), st.Note)
			if st.Snippet != "" {
				pw.printf("       %s %s\n", paint(dim, "│"), st.Snippet)
			}
		}
		if opts.Verbose {
			pw.printf("    %s\n", paint(dim, fmt.Sprintf("confidence %s · fingerprint %s · %s", f.Confidence, f.Fingerprint, strings.Join(f.Tags, ", "))))
		}
	}
	if len(res.Findings) > 0 {
		pw.printf("\n")
	}
	pw.printf("%s\n", Summary(res))
	for _, warn := range res.Warnings {
		pw.printf("%s %s\n", paint(yellow, "warning:"), warn)
	}
	return pw.err
}

// Summary is the one-line result: "2 findings (1 critical, 1 high) in 412
// files, 3.2 s".
func Summary(res *engine.Result) string {
	counts := map[rules.Severity]int{}
	for _, f := range res.Findings {
		counts[f.Severity]++
	}
	var parts []string
	for _, s := range []rules.Severity{rules.Critical, rules.High, rules.Medium, rules.Low} {
		if counts[s] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", counts[s], s))
		}
	}
	noun := "findings"
	if len(res.Findings) == 1 {
		noun = "finding"
	}
	head := fmt.Sprintf("%d %s", len(res.Findings), noun)
	if len(parts) > 0 {
		head += " (" + strings.Join(parts, ", ") + ")"
	}
	return fmt.Sprintf("%s in %d files, %s", head, res.Stats.Files, res.Stats.Elapsed.Round(100*time.Millisecond))
}

type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) printf(format string, args ...any) {
	if e.err == nil {
		_, e.err = fmt.Fprintf(e.w, format, args...)
	}
}
