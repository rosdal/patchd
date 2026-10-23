package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/patchd/patchd/engine"
	"github.com/patchd/patchd/engine/report"
	"github.com/patchd/patchd/engine/rules"
	"github.com/patchd/patchd/internal/buildinfo"
)

// Exit codes of patchd scan.
const (
	exitClean    = 0 // no findings at or above --fail-on
	exitFindings = 1 // findings at or above --fail-on
	exitUsage    = 2 // bad flags or arguments
	exitFailed   = 3 // analysis failed; partial results are still written
)

// exitError carries a process exit code out of a command.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string {
	if e.err == nil {
		return fmt.Sprintf("exit status %d", e.code)
	}
	return e.err.Error()
}

func (e *exitError) Unwrap() error { return e.err }

func usageError(format string, args ...any) error {
	return &exitError{code: exitUsage, err: fmt.Errorf(format, args...)}
}

type scanFlags struct {
	format             string
	output             string
	rules              string
	severityThreshold  string
	failOn             string
	jobs               int
	maxMemory          string
	timeout            time.Duration
	verbose            bool
	includeEnvironment bool
}

func newScanCmd() *cobra.Command {
	f := &scanFlags{}
	cmd := &cobra.Command{
		Use:   "scan [path]",
		Short: "Scan a repository for injection vulnerabilities and report them (pretty, SARIF or JSON)",
		Long: `Scan the TypeScript and JavaScript projects under path (default: the current
directory) for command injection, path traversal and SQL injection.

Findings go to stdout or --output; progress and warnings go to stderr.

Exit codes: 0 no findings at or above --fail-on, 1 findings found, 2 usage
error, 3 analysis failed (partial results are still written).`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := "."
			if len(args) == 1 {
				path = args[0]
			}
			return runScan(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), path, f)
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&f.format, "format", "pretty", "output format: pretty, sarif or json")
	fl.StringVarP(&f.output, "output", "o", "", "write findings to this file instead of stdout")
	fl.StringVar(&f.rules, "rules", "", "comma-separated rule IDs to run (default: every enabled rule; see patchd rules list)")
	fl.StringVar(&f.severityThreshold, "severity-threshold", "low", "report only findings at or above this severity")
	fl.StringVar(&f.failOn, "fail-on", "low", "exit 1 when a reported finding is at or above this severity")
	fl.IntVar(&f.jobs, "jobs", 0, "functions analysed at once (default: one per CPU)")
	fl.StringVar(&f.maxMemory, "max-memory", "", "memory budget for loading projects, e.g. 8GB (default: 75% of available memory)")
	fl.DurationVar(&f.timeout, "timeout", 0, "stop the scan after this long, e.g. 10m (default: none)")
	fl.BoolVarP(&f.verbose, "verbose", "v", false, "print per-project progress, fingerprints and confidence")
	fl.BoolVar(&f.includeEnvironment, "include-environment", false, "also treat process.argv and process.env as sources (low severity)")
	return cmd
}

func runScan(ctx context.Context, stdout, stderr io.Writer, path string, f *scanFlags) error {
	if ctx == nil {
		ctx = context.Background()
	}
	switch f.format {
	case "pretty", "sarif", "json":
	default:
		return usageError("--format must be pretty, sarif or json, got %q", f.format)
	}
	threshold, err := rules.ParseSeverity(f.severityThreshold)
	if err != nil {
		return usageError("--severity-threshold: %v", err)
	}
	failOn, err := rules.ParseSeverity(f.failOn)
	if err != nil {
		return usageError("--fail-on: %v", err)
	}
	opts := engine.Options{Jobs: f.jobs, IncludeEnvironment: f.includeEnvironment}
	if f.rules != "" {
		opts.Rules = strings.Split(f.rules, ",")
		if _, err := rules.Select(opts.Rules); err != nil {
			return usageError("--rules: %v", err)
		}
	}
	if f.maxMemory != "" {
		if opts.MaxMemory, err = parseBytes(f.maxMemory); err != nil {
			return usageError("--max-memory: %v", err)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return usageError("%v", err)
	}
	if !info.IsDir() {
		return usageError("%s is not a directory", path)
	}
	root, err := filepath.Abs(path)
	if err != nil {
		return usageError("%v", err)
	}
	if f.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, f.timeout)
		defer cancel()
	}
	// Progress and warnings are best effort: a failing stderr doesn't stop
	// the scan.
	log := &textWriter{w: stderr}
	if f.verbose {
		opts.Progress = func(p engine.Progress) {
			log.printf("%-8s %-40s %8s  %s\n", p.Phase, p.Project, p.Elapsed.Round(time.Millisecond), p.Detail)
		}
	}
	log.printf("patchd scan %s\n", root)

	res, scanErr := engine.Analyze(ctx, root, opts)
	if res == nil {
		return &exitError{code: exitFailed, err: scanErr}
	}
	s := res.Stats
	log.printf("load %s, lower %s, analyse %s (%d projects, %d files, %d of %d functions analysed)\n",
		s.Load.Round(time.Millisecond), s.Lower.Round(time.Millisecond), s.Analyse.Round(time.Millisecond),
		s.Projects, s.Files, s.Analysed, s.Functions)

	kept := res.Findings[:0:0]
	for _, fd := range res.Findings {
		if fd.Severity.Rank() >= threshold.Rank() {
			kept = append(kept, fd)
		}
	}
	res.Findings = kept

	out := stdout
	var file *os.File
	if f.output != "" {
		file, err = os.Create(f.output)
		if err != nil {
			return &exitError{code: exitFailed, err: err}
		}
		defer func() { _ = file.Close() }() // closed and checked below; this covers early returns
		out = file
	}
	switch f.format {
	case "sarif":
		err = report.SARIF(out, res, buildinfo.Version)
	case "json":
		err = report.JSON(out, res, buildinfo.Version)
	default:
		err = report.Pretty(out, res, report.PrettyOptions{Color: file == nil && useColor(stdout), Verbose: f.verbose})
	}
	if err == nil && file != nil {
		err = file.Close()
	}
	if err != nil {
		return &exitError{code: exitFailed, err: err}
	}
	if f.format != "pretty" || file != nil {
		log.println(report.Summary(res))
		for _, w := range res.Warnings {
			log.println("warning:", w)
		}
	}

	if scanErr != nil {
		if errors.Is(scanErr, context.DeadlineExceeded) {
			scanErr = fmt.Errorf("scan timed out after %s; results are partial", f.timeout)
		}
		return &exitError{code: exitFailed, err: scanErr}
	}
	if len(res.Errors) > 0 && res.Stats.Projects == 0 {
		return &exitError{code: exitFailed, err: fmt.Errorf("no project could be analysed: %w", errors.Join(res.Errors...))}
	}
	for _, fd := range res.Findings {
		if fd.Severity.Rank() >= failOn.Rank() {
			return &exitError{code: exitFindings}
		}
	}
	return nil
}

// useColor reports whether w is a terminal and NO_COLOR is unset.
func useColor(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}
