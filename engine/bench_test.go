package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patchd/patchd/engine/ir"
	"github.com/patchd/patchd/engine/models"
	"github.com/patchd/patchd/engine/rules"
	"github.com/patchd/patchd/engine/taint"
	"github.com/patchd/patchd/engine/tsbridge"
)

// Benchmarks run on the reference repos fetched into .patchd/targets by
// `pnpm targets:fetch`, like tsbridge's and the IR's; PATCHD_BENCH_REPOS
// limits them (comma-separated ids). `pnpm bench` runs them one repo per
// process.

func benchTargets(b *testing.B) []string {
	b.Helper()
	dir := os.Getenv("PATCHD_TARGETS")
	if dir == "" {
		dir = filepath.Join("..", ".patchd", "targets")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		b.Skipf("no reference repos in %s (pnpm targets:fetch)", dir)
	}
	only := map[string]bool{}
	for _, id := range strings.Split(os.Getenv("PATCHD_BENCH_REPOS"), ",") {
		if id = strings.TrimSpace(id); id != "" {
			only[id] = true
		}
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && (len(only) == 0 || only[e.Name()]) {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	if len(out) == 0 {
		b.Skip("no reference repos selected")
	}
	return out
}

// BenchmarkTaint times taint analysis and the rules on every function of a
// repo (every rule, including those off by default). Loading, type-checking
// and lowering run outside the timer: this is what v0.4 adds on top of v0.3
// (target: under 30 s on a 500k-line repo).
func BenchmarkTaint(b *testing.B) {
	spec := models.Builtin()
	if err := spec.Validate(rules.Exists); err != nil {
		b.Fatal(err)
	}
	all, _ := rules.Select(rules.IDs())
	byKind := map[taint.Kind][]*rules.Rule{}
	for _, r := range all {
		byKind[r.Kind] = append(byKind[r.Kind], r)
	}
	for _, root := range benchTargets(b) {
		b.Run(filepath.Base(root), func(b *testing.B) {
			ctx := context.Background()
			ws, err := tsbridge.Discover(ctx, root, tsbridge.DiscoverOptions{})
			if err != nil {
				b.Fatal(err)
			}
			var lines, analysed int
			for b.Loop() {
				b.StopTimer()
				lines, analysed = 0, 0
				_, err := tsbridge.LoadAll(ctx, ws, tsbridge.LoadAllOptions{Jobs: 1}, func(_ tsbridge.ProjectConfig, prog *tsbridge.Program, lerr error) error {
					if lerr != nil {
						return nil
					}
					var jobs []funcJob
					for _, m := range ir.BuildProgram(ctx, prog, ir.BuildOptions{}) {
						if m == nil {
							continue
						}
						for _, f := range m.Funcs {
							jobs = append(jobs, funcJob{m: m, f: f})
						}
					}
					p := &projectScan{prog: prog, ws: ws, spec: spec, byKind: byKind}
					b.StartTimer()
					_, st := p.analyse(ctx, jobs, Stats{})
					b.StopTimer()
					lines += prog.Stats().Lines
					analysed += st.Analysed
					return nil
				})
				if err != nil {
					b.Fatal(err)
				}
				b.StartTimer()
			}
			b.StopTimer()
			b.ReportMetric(float64(lines), "lines")
			b.ReportMetric(float64(analysed), "functions-analysed")
		})
	}
}

// BenchmarkScan times a whole patchd scan of a repo (discover, load, check,
// lower, taint, rules) and reports the process's peak RSS.
func BenchmarkScan(b *testing.B) {
	for _, root := range benchTargets(b) {
		b.Run(filepath.Base(root), func(b *testing.B) {
			var res *Result
			for b.Loop() {
				var err error
				res, err = Analyze(context.Background(), root, Options{Rules: rules.IDs()})
				if err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(res.Stats.Lines), "lines")
			b.ReportMetric(float64(len(res.Findings)), "findings")
			b.ReportMetric(float64(tsbridge.ProcessPeakRSS())/(1<<20), "peak-rss-MB")
		})
	}
}
