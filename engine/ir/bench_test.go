package ir

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/patchd/patchd/engine/tsbridge"
)

// Benchmarks run on the reference repos fetched into .patchd/targets by
// `pnpm targets:fetch`, like tsbridge's; repos that aren't fetched are
// skipped and PATCHD_BENCH_REPOS limits them (comma-separated ids). `pnpm
// bench` runs them one repo per process.

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

// BenchmarkLower lowers every source file of every project of a repo. The
// timer runs only while lowering (loading and type-checking are tsbridge's
// benchmarks), so ns/op, B/op and allocs/op are the lowering's. It also
// reports them per 1,000 source lines, the IR's size per source byte, and
// the heap the IR adds on top of the loaded program.
func BenchmarkLower(b *testing.B) {
	for _, root := range benchTargets(b) {
		b.Run(filepath.Base(root), func(b *testing.B) {
			ctx := context.Background()
			ws, err := tsbridge.Discover(ctx, root, tsbridge.DiscoverOptions{})
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			var lines, srcBytes, irBytes int
			var extraHeap uint64
			for b.Loop() {
				b.StopTimer()
				lines, srcBytes, irBytes = 0, 0, 0
				var kept [][]*Module // all of the repo's IR, alive at once
				base := liveHeap()
				// One project at a time, so the timer brackets one lowering.
				_, err := tsbridge.LoadAll(ctx, ws, tsbridge.LoadAllOptions{Jobs: 1}, func(_ tsbridge.ProjectConfig, p *tsbridge.Program, lerr error) error {
					if lerr != nil {
						return nil
					}
					b.StartTimer()
					mods := BuildProgram(ctx, p, BuildOptions{})
					b.StopTimer()
					kept = append(kept, mods)
					lines += p.Stats().Lines
					for _, m := range mods {
						if m == nil {
							continue
						}
						irBytes += m.Bytes()
						if f := p.File(m.File); f != nil {
							srcBytes += len(f.Text)
						}
					}
					return nil
				})
				if err != nil {
					b.Fatal(err)
				}
				after := liveHeap() // the programs are closed; what is left is the IR
				extraHeap = after - min(base, after)
				runtime.KeepAlive(kept)
				b.StartTimer()
			}
			b.StopTimer()
			kl := float64(max(1, lines)) / 1000
			b.ReportMetric(float64(lines), "lines")
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/kl, "ns/kline")
			b.ReportMetric(float64(irBytes)/float64(max(1, srcBytes)), "ir-B/src-B")
			b.ReportMetric(float64(extraHeap)/(1<<20), "extra-heap-MB")
		})
	}
}

// liveHeap returns the live heap after a collection.
func liveHeap() uint64 {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.HeapAlloc
}
