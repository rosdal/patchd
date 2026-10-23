package engine

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/patchd/patchd/engine/ir"
	"github.com/patchd/patchd/engine/models"
	"github.com/patchd/patchd/engine/rules"
	_ "github.com/patchd/patchd/engine/rules/security" // registers the taint rules
	"github.com/patchd/patchd/engine/taint"
	"github.com/patchd/patchd/engine/tsbridge"
)

// Options tune Analyze. The zero value scans with every enabled rule.
type Options struct {
	Rules []string // rule IDs to run; empty: every rule enabled by default
	// IncludeEnvironment reports process.argv and process.env as sources
	// (low severity).
	IncludeEnvironment bool
	Jobs               int    // functions analysed at once (0: one per CPU); projects load with tsbridge's default
	MaxMemory          uint64 // memory budget for loading projects in bytes (0: tsbridge's default)
	// Discover overrides project discovery (patchd.yaml's typescript block
	// still applies unless NoRepoConfig is set).
	Discover tsbridge.DiscoverOptions
	// Progress, when set, is told when each phase of each project ends.
	Progress func(Progress)
}

// Progress reports one finished phase.
type Progress struct {
	Project string        // the project's config name
	Phase   string        // "load", "lower", "analyse"
	Elapsed time.Duration // how long the phase took
	Detail  string        // "412 files", "3,201 functions"
}

// Result is the outcome of a scan.
type Result struct {
	Root     string        // the scanned root, absolute with forward slashes
	Findings []Finding     // sorted by path, line, column, rule
	Rules    []*rules.Rule // the rules that ran
	Stats    Stats
	Warnings []string // summarised: missing node_modules, unknown syntax, failed projects, capped functions
	// Errors holds projects that failed to load or analyse; the findings
	// of every other project are still in Findings.
	Errors []error
}

// Stats are counters and timings of a scan.
type Stats struct {
	Projects        int           `json:"projects"`
	Files           int           `json:"files"`
	Lines           int           `json:"lines"`
	Functions       int           `json:"functions"`
	Analysed        int           `json:"analysed"` // functions that could contain a source
	Capped          int           `json:"capped"`   // functions whose solver hit the pass cap
	Candidates      int           `json:"candidates"`
	SyntaxNodes     int           `json:"syntax_nodes"`
	UnknownNodes    int           `json:"unknown_nodes"`
	MissingModules  int           `json:"projects_missing_node_modules"`
	Load            time.Duration `json:"load_ns"`
	Lower           time.Duration `json:"lower_ns"`
	Analyse         time.Duration `json:"analyse_ns"`
	Elapsed         time.Duration `json:"elapsed_ns"`
	FailedProjects  int           `json:"failed_projects"`
	PeakHeap        uint64        `json:"peak_heap_bytes"`
	ProjectsSkipped int           `json:"projects_skipped,omitempty"`
}

// Analyze scans the TypeScript and JavaScript projects under root with the
// taint rules: discover, load, lower to IR, analyse each function, apply
// rules, then deduplicate and fingerprint the findings. A cancelled ctx
// stops it; what was found so far is returned with ctx's error.
func Analyze(ctx context.Context, root string, opts Options) (*Result, error) {
	start := time.Now()
	selected, err := rules.Select(opts.Rules)
	if err != nil {
		return nil, err
	}
	spec := models.Builtin()
	if err := spec.Validate(rules.Exists); err != nil {
		return nil, fmt.Errorf("built-in models: %w", err)
	}
	ws, err := tsbridge.Discover(ctx, root, opts.Discover)
	if err != nil {
		return nil, err
	}
	res := &Result{Root: ws.Root, Rules: selected}
	byKind := map[taint.Kind][]*rules.Rule{}
	for _, r := range selected {
		byKind[r.Kind] = append(byKind[r.Kind], r)
	}
	var mu sync.Mutex
	loadStart := time.Now()
	stats, loadErr := tsbridge.LoadAll(ctx, ws, tsbridge.LoadAllOptions{MaxMemory: opts.MaxMemory}, func(cfg tsbridge.ProjectConfig, prog *tsbridge.Program, err error) error {
		if err != nil {
			mu.Lock()
			res.Errors = append(res.Errors, fmt.Errorf("%s: %w", cfg.Name, err))
			mu.Unlock()
			return nil
		}
		ps := prog.Stats()
		report(opts, cfg.Name, "load", time.Since(loadStart), fmt.Sprintf("%d files", ps.RootFiles))
		p := &projectScan{prog: prog, ws: ws, spec: spec, byKind: byKind, opts: opts}
		findings, st := p.run(ctx)
		mu.Lock()
		defer mu.Unlock()
		res.Findings = append(res.Findings, findings...)
		res.Stats.add(st)
		res.Stats.Projects++
		res.Stats.Files += ps.RootFiles
		res.Stats.Lines += ps.Lines
		for _, w := range prog.Warnings() {
			if w.Code == tsbridge.WarnPackageNotInstalled {
				res.Stats.MissingModules++
				break
			}
		}
		return nil
	})
	res.Stats.Load = stats.Elapsed - res.Stats.Lower - res.Stats.Analyse
	res.Stats.FailedProjects = len(res.Errors)
	res.Stats.PeakHeap = stats.PeakHeap
	finish(res)
	res.Stats.Elapsed = time.Since(start)
	res.Warnings = summarise(res)
	if loadErr != nil {
		return res, loadErr
	}
	return res, ctx.Err()
}

func report(opts Options, project, phase string, d time.Duration, detail string) {
	if opts.Progress != nil {
		opts.Progress(Progress{Project: project, Phase: phase, Elapsed: d, Detail: detail})
	}
}

func (s *Stats) add(o Stats) {
	s.Functions += o.Functions
	s.Analysed += o.Analysed
	s.Capped += o.Capped
	s.Candidates += o.Candidates
	s.SyntaxNodes += o.SyntaxNodes
	s.UnknownNodes += o.UnknownNodes
	s.Lower += o.Lower
	s.Analyse += o.Analyse
}

// projectScan analyses one loaded program.
type projectScan struct {
	prog   *tsbridge.Program
	ws     *tsbridge.Workspace
	spec   *taint.Spec
	byKind map[taint.Kind][]*rules.Rule
	opts   Options
}

type funcJob struct {
	m *ir.Module
	f *ir.Func
}

func (p *projectScan) run(ctx context.Context) ([]Finding, Stats) {
	var st Stats
	t0 := time.Now()
	mods := ir.BuildProgram(ctx, p.prog, ir.BuildOptions{Jobs: p.opts.Jobs})
	st.Lower = time.Since(t0)
	var jobs []funcJob
	for _, m := range mods {
		if m == nil {
			continue
		}
		st.SyntaxNodes += m.Coverage.Nodes
		st.UnknownNodes += m.Coverage.Unknown
		for _, f := range m.Funcs {
			jobs = append(jobs, funcJob{m: m, f: f})
		}
	}
	st.Functions = len(jobs)
	report(p.opts, p.prog.Config().Name, "lower", st.Lower, fmt.Sprintf("%d functions", len(jobs)))

	t1 := time.Now()
	analyzer := taint.NewAnalyzer(p.prog, taint.Config{Spec: p.spec, IncludeEnvironment: p.opts.IncludeEnvironment})
	workers := p.opts.Jobs
	if workers <= 0 {
		workers = runtime.NumCPU()
	}
	var (
		mu       sync.Mutex
		findings []Finding
		wg       sync.WaitGroup
		next     int
	)
	take := func() (funcJob, bool) {
		mu.Lock()
		defer mu.Unlock()
		if next >= len(jobs) || ctx.Err() != nil {
			return funcJob{}, false
		}
		j := jobs[next]
		next++
		return j, true
	}
	for range min(workers, max(len(jobs), 1)) {
		wg.Go(func() {
			for {
				j, ok := take()
				if !ok {
					return
				}
				r := analyzer.Func(j.m, j.f)
				fs := p.findings(r)
				mu.Lock()
				if !r.Skipped {
					st.Analysed++
				}
				if r.Solver.Capped {
					st.Capped++
				}
				st.Candidates += len(r.Candidates)
				findings = append(findings, fs...)
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	st.Analyse = time.Since(t1)
	report(p.opts, p.prog.Config().Name, "analyse", st.Analyse, fmt.Sprintf("%d of %d functions", st.Analysed, st.Functions))
	return findings, st
}

// findings applies the rules to one function's candidates and builds the
// findings: one per rule and sink call, with the most direct trace.
func (p *projectScan) findings(r *taint.FuncResult) []Finding {
	if len(r.Candidates) == 0 {
		return nil
	}
	type key struct {
		rule string
		call ir.InstrID
	}
	type group struct {
		rule    *rules.Rule
		best    *rules.Candidate
		origins map[int32]bool
	}
	groups := map[key]*group{}
	var order []key
	for _, c := range r.Candidates {
		for _, rule := range p.byKind[c.Sink.Kind] {
			if c.Sink.Rule != rule.ID {
				continue
			}
			cand := &rules.Candidate{Prog: p.prog, Result: r, Taint: c, Path: r.Path(c)}
			if rule.Keep != nil && !rule.Keep(cand) {
				continue
			}
			k := key{rule.ID, c.Call}
			g, ok := groups[k]
			if !ok {
				g = &group{rule: rule, origins: map[int32]bool{}}
				groups[k] = g
				order = append(order, k)
			}
			g.origins[c.Label.Origin] = true
			if g.best == nil || better(r, cand, g.best) {
				g.best = cand
			}
		}
	}
	out := make([]Finding, 0, len(order))
	for _, k := range order {
		g := groups[k]
		f := p.build(g.rule, g.best)
		f.OtherSources = len(g.origins) - 1
		out = append(out, f)
	}
	assignFingerprints(out, r)
	return out
}

// better prefers the more direct trace: fewer steps, then a high-confidence
// origin, then the earlier source.
func better(r *taint.FuncResult, a, b *rules.Candidate) bool {
	if len(a.Path) != len(b.Path) {
		return len(a.Path) < len(b.Path)
	}
	oa, ob := r.Origin(a.Taint.Label), r.Origin(b.Taint.Label)
	if oa.Heuristic != ob.Heuristic {
		return !oa.Heuristic
	}
	return a.Taint.Label.Origin < b.Taint.Label.Origin
}

// finish sorts the findings of all projects.
func finish(res *Result) {
	slices.SortFunc(res.Findings, func(a, b Finding) int {
		if c := strings.Compare(a.Location.Path, b.Location.Path); c != 0 {
			return c
		}
		if a.Location.StartLine != b.Location.StartLine {
			return a.Location.StartLine - b.Location.StartLine
		}
		if a.Location.StartColumn != b.Location.StartColumn {
			return a.Location.StartColumn - b.Location.StartColumn
		}
		return strings.Compare(a.RuleID, b.RuleID)
	})
}

// summarise turns stats and errors into the warnings printed at the end.
func summarise(res *Result) []string {
	var out []string
	s := res.Stats
	if s.MissingModules > 0 {
		out = append(out, fmt.Sprintf("%d of %d projects had missing node_modules: calls into uninstalled packages are not resolved (run your package manager's install)", s.MissingModules, s.Projects))
	}
	if s.SyntaxNodes > 0 && s.UnknownNodes > 0 {
		out = append(out, fmt.Sprintf("%.1f%% of syntax lowered as unknown", 100*float64(s.UnknownNodes)/float64(s.SyntaxNodes)))
	}
	if s.Capped > 0 {
		out = append(out, fmt.Sprintf("%d functions hit the analysis pass cap; their results may be incomplete", s.Capped))
	}
	for _, err := range res.Errors {
		var pe *tsbridge.PanicError
		if errors.As(err, &pe) {
			out = append(out, "project failed with a compiler panic: "+err.Error())
		} else {
			out = append(out, "project failed: "+err.Error())
		}
	}
	return out
}
