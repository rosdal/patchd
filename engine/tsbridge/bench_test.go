package tsbridge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Benchmarks run on the reference repos (eval/targets/reference-repos.yaml)
// fetched into .patchd/targets by `pnpm targets:fetch`; repos that aren't
// fetched are skipped. PATCHD_BENCH_REPOS limits them (comma-separated ids).
// tools/scripts/bench.mjs (`pnpm bench`) runs each repo in its own process so
// the reported peak RSS belongs to that repo.

func benchTargets(b *testing.B) []string {
	b.Helper()
	dir := os.Getenv("PATCHD_TARGETS")
	if dir == "" {
		dir = filepath.Join("..", "..", ".patchd", "targets")
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

func benchDiscover(b *testing.B, root string) *Workspace {
	b.Helper()
	ws, err := Discover(context.Background(), root, DiscoverOptions{})
	if err != nil {
		b.Fatal(err)
	}
	return ws
}

func reportMemory(b *testing.B, peakHeap uint64) {
	b.ReportMetric(float64(peakHeap)/(1<<20), "peak-heap-MB")
	b.ReportMetric(float64(ProcessPeakRSS())/(1<<20), "peak-rss-MB")
}

func BenchmarkDiscover(b *testing.B) {
	for _, root := range benchTargets(b) {
		b.Run(filepath.Base(root), func(b *testing.B) {
			for b.Loop() {
				benchDiscover(b, root)
			}
		})
	}
}

// BenchmarkLoad parses and binds every project, without type-checking.
func BenchmarkLoad(b *testing.B) {
	benchLoadAll(b, true)
}

// BenchmarkCheck is the full load and type-check of every project: the
// number the v0.2 targets are about.
func BenchmarkCheck(b *testing.B) {
	benchLoadAll(b, false)
}

func benchLoadAll(b *testing.B, noCheck bool) {
	for _, root := range benchTargets(b) {
		b.Run(filepath.Base(root), func(b *testing.B) {
			ws := benchDiscover(b, root)
			var stats LoadAllStats
			for b.Loop() {
				opts := LoadAllOptions{}
				opts.NoCheck = noCheck
				var err error
				stats, err = LoadAll(context.Background(), ws, opts, func(ProjectConfig, *Program, error) error { return nil })
				if err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(stats.Files), "files")
			reportMemory(b, stats.PeakHeap)
		})
	}
}

// BenchmarkTypeOf measures 10,000 first-time TypeOf calls on identifiers of
// each repo's largest project.
func BenchmarkTypeOf(b *testing.B) {
	for _, root := range benchTargets(b) {
		b.Run(filepath.Base(root), func(b *testing.B) {
			ws := benchDiscover(b, root)
			var largest ProjectConfig
			for _, p := range ws.Projects {
				if len(p.Files) > len(largest.Files) {
					largest = p
				}
			}
			for b.Loop() {
				b.StopTimer()
				p, err := Load(context.Background(), largest, LoadOptions{NoCheck: true})
				if err != nil {
					b.Fatal(err)
				}
				var ids []NodeID
				for _, f := range p.RootFiles() {
					p.Walk(f, func(n NodeID) bool {
						if p.Kind(n) == KindIdentifier {
							ids = append(ids, n)
						}
						return len(ids) < 10_000
					})
					if len(ids) >= 10_000 {
						break
					}
				}
				b.StartTimer()
				for _, n := range ids {
					p.TypeOf(n)
				}
				b.StopTimer()
				b.ReportMetric(float64(len(ids)), "typeof-calls")
				p.Close()
				b.StartTimer()
			}
		})
	}
}
