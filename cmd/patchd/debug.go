package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/patchd/patchd/engine/tsbridge"
)

// The debug commands make engine layers inspectable from the terminal. They
// use only the public tsbridge API, which proves that API is enough. Each
// later engine layer adds its own `patchd debug <layer>` subcommand here.

type debugFlags struct {
	format    string
	jobs      int
	maxMemory string
	timeout   time.Duration
	maxFiles  int
	noCheck   bool
	root      string
	verbose   bool
	checkers  int
	noCache   bool
}

func newDebugCmd() *cobra.Command {
	f := &debugFlags{}
	cmd := &cobra.Command{
		Use:    "debug",
		Short:  "Inspect engine internals (for Patchd developers)",
		Hidden: true,
		PersistentPreRun: func(*cobra.Command, []string) {
			if f.verbose {
				slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
			}
		},
	}
	pf := cmd.PersistentFlags()
	pf.StringVar(&f.format, "format", "text", "output format: text or json")
	pf.IntVar(&f.jobs, "jobs", 0, "projects to load at once (default: a quarter of the CPU cores)")
	pf.StringVar(&f.maxMemory, "max-memory", "", "memory budget, e.g. 8GB (default: 75% of available memory)")
	pf.DurationVar(&f.timeout, "timeout", 0, "per-project load and check timeout (default: none)")
	pf.IntVar(&f.maxFiles, "max-files", 0, "analyse at most this many files per project (default: no limit)")
	pf.BoolVar(&f.noCheck, "no-check", false, "parse and bind only; skip type-checking")
	pf.StringVar(&f.root, "root", "", "workspace root (default: the enclosing git repository)")
	pf.IntVar(&f.checkers, "checkers", 0, "typescript-go checkers per project (default: 8 for projects of 2,000+ files, else 4)")
	pf.BoolVar(&f.noCache, "no-shared-cache", false, "do not share parsed declaration files between projects")
	pf.BoolVarP(&f.verbose, "verbose", "v", false, "log debug output, including recovered compiler panics, to stderr")
	cmd.AddCommand(newDebugProjectCmd(f), newDebugTypesCmd(f), newDebugResolveCmd(f), newDebugStatsCmd(f),
		newDebugIRCmd(f), newDebugIRCoverageCmd(f))
	return cmd
}

func (f *debugFlags) validate() error {
	if f.format != "text" && f.format != "json" {
		return fmt.Errorf("--format must be text or json, got %q", f.format)
	}
	return nil
}

func (f *debugFlags) loadAllOptions() (tsbridge.LoadAllOptions, error) {
	opts := tsbridge.LoadAllOptions{Jobs: f.jobs, NoSharedCache: f.noCache}
	opts.Timeout, opts.MaxFiles, opts.NoCheck, opts.Checkers = f.timeout, f.maxFiles, f.noCheck, f.checkers
	if f.maxMemory != "" {
		n, err := parseBytes(f.maxMemory)
		if err != nil {
			return opts, err
		}
		opts.MaxMemory = n
	}
	return opts, nil
}

// parseBytes reads sizes like 512MB, 8GB or a plain byte count.
func parseBytes(s string) (uint64, error) {
	units := []struct {
		suffix string
		mult   uint64
	}{{"TB", 1 << 40}, {"GB", 1 << 30}, {"MB", 1 << 20}, {"KB", 1 << 10}, {"B", 1}}
	u := strings.ToUpper(strings.TrimSpace(s))
	for _, unit := range units {
		if num, ok := strings.CutSuffix(u, unit.suffix); ok {
			var n float64
			if _, err := fmt.Sscanf(strings.TrimSpace(num), "%g", &n); err != nil || n < 0 {
				return 0, fmt.Errorf("invalid size %q", s)
			}
			return uint64(n * float64(unit.mult)), nil
		}
	}
	var n uint64
	if _, err := fmt.Sscanf(u, "%d", &n); err != nil {
		return 0, fmt.Errorf("invalid size %q", s)
	}
	return n, nil
}

// workspaceRoot returns --root, else the git repository enclosing p, else p
// (or p's directory for a file).
func (f *debugFlags) workspaceRoot(p string) (string, error) {
	if f.root != "" {
		return filepath.Abs(f.root)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	start := abs
	if st, err := os.Stat(abs); err == nil && !st.IsDir() {
		start = filepath.Dir(abs)
	}
	for dir := start; ; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir, nil
		}
		if filepath.Dir(dir) == dir {
			return start, nil
		}
	}
}

// projectFor returns the project that analyses file.
func projectFor(ws *tsbridge.Workspace, file string) (tsbridge.ProjectConfig, bool) {
	file = filepath.ToSlash(file)
	for _, p := range ws.Projects {
		for _, f := range p.Files {
			if f == file || strings.EqualFold(f, file) {
				return p, true
			}
		}
	}
	return tsbridge.ProjectConfig{}, false
}

// loadFile discovers the workspace around file and loads the project that analyses it.
func (f *debugFlags) loadFile(ctx context.Context, file string) (*tsbridge.Program, *tsbridge.File, error) {
	abs, err := filepath.Abs(file)
	if err != nil {
		return nil, nil, err
	}
	root, err := f.workspaceRoot(abs)
	if err != nil {
		return nil, nil, err
	}
	ws, err := tsbridge.Discover(ctx, root, tsbridge.DiscoverOptions{})
	if err != nil {
		return nil, nil, err
	}
	cfg, ok := projectFor(ws, abs)
	if !ok {
		return nil, nil, fmt.Errorf("%s is not a source file of any project under %s", file, root)
	}
	opts, err := f.loadAllOptions()
	if err != nil {
		return nil, nil, err
	}
	p, err := tsbridge.Load(ctx, cfg, opts.LoadOptions)
	if err != nil {
		return nil, nil, err
	}
	sf := p.FileByPath(abs)
	if sf == nil {
		p.Close()
		return nil, nil, fmt.Errorf("%s is not in the loaded program", file)
	}
	return p, sf, nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// parsePosition parses <file>:<line>:<col>; the file may contain colons (C:\…).
func parsePosition(s string) (file string, line, col int, err error) {
	i := strings.LastIndex(s, ":")
	j := strings.LastIndex(s[:max(i, 0)], ":")
	if i < 0 || j < 0 {
		return "", 0, 0, errors.New("want <file>:<line>:<col>")
	}
	if _, err := fmt.Sscanf(s[j+1:i], "%d", &line); err != nil {
		return "", 0, 0, fmt.Errorf("bad line in %q", s)
	}
	if _, err := fmt.Sscanf(s[i+1:], "%d", &col); err != nil {
		return "", 0, 0, fmt.Errorf("bad column in %q", s)
	}
	return s[:j], line, col, nil
}

func formatBytes(n uint64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f MB", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%d KB", n>>10)
	}
}

func ms(d time.Duration) string { return fmt.Sprintf("%.0f ms", float64(d)/float64(time.Millisecond)) }

// textWriter prints text output and keeps the first write error.
type textWriter struct {
	w   io.Writer
	err error
}

func (t *textWriter) printf(format string, args ...any) {
	if t.err == nil {
		_, t.err = fmt.Fprintf(t.w, format, args...)
	}
}

func (t *textWriter) println(args ...any) {
	if t.err == nil {
		_, t.err = fmt.Fprintln(t.w, args...)
	}
}
