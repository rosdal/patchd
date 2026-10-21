package tsbridge

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"slices"
	"sync"
	"time"

	"github.com/patchd/patchd/engine/tsbridge/internal/sysmem"
)

// LoadAllOptions tune LoadAll. The zero value is the default.
type LoadAllOptions struct {
	LoadOptions
	// Jobs is how many projects load at once (0: the number of CPU cores).
	Jobs int
	// MaxMemory is the memory budget in bytes (0: 75% of available memory).
	// A project starts only when its estimated cost fits in what is left.
	MaxMemory uint64
	// NoSharedCache turns off sharing parsed node_modules and lib files.
	NoSharedCache bool
}

// LoadAllStats summarise a LoadAll run.
type LoadAllStats struct {
	Projects    int           `json:"projects"`
	Failed      int           `json:"failed"`
	Files       int           `json:"files"`
	Elapsed     time.Duration `json:"elapsed_ns"`
	PeakHeap    uint64        `json:"peak_heap_bytes"`
	Budget      uint64        `json:"budget_bytes"`
	Jobs        int           `json:"jobs"`
	CacheHits   int64         `json:"cache_hits"`
	CacheMisses int64         `json:"cache_misses"`
}

// LoadAll loads every project of a workspace with a worker pool inside a
// memory budget, and calls visit for each one as it finishes: with the
// program, or with the error that kept it from loading (a *PanicError for a
// compiler panic, TSB007). The program is closed when visit returns, so
// memory is freed before more projects load. visit may run concurrently. An
// error from visit stops LoadAll and is returned.
func LoadAll(ctx context.Context, ws *Workspace, opts LoadAllOptions, visit func(ProjectConfig, *Program, error) error) (LoadAllStats, error) {
	start := time.Now()
	stats := LoadAllStats{Jobs: opts.Jobs, Budget: opts.MaxMemory}
	if stats.Jobs <= 0 {
		stats.Jobs = runtime.NumCPU()
	}
	if stats.Budget == 0 {
		stats.Budget = sysmem.Available() / 4 * 3
		if stats.Budget == 0 {
			stats.Budget = 4 << 30
		}
	}
	if !opts.NoSharedCache {
		opts.cache = newParseCache()
	}

	type job struct {
		cfg  ProjectConfig
		cost uint64
	}
	jobs := make([]job, len(ws.Projects))
	for i, cfg := range ws.Projects {
		jobs[i] = job{cfg: cfg, cost: min(estimateCost(cfg), stats.Budget)}
	}
	// Largest first: they set the critical path, and small ones fill the gaps.
	slices.SortStableFunc(jobs, func(a, b job) int {
		switch {
		case a.cost > b.cost:
			return -1
		case a.cost < b.cost:
			return 1
		}
		return 0
	})

	parent := ctx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	budget := newBudget(stats.Budget)
	sampler := startHeapSampler()

	var (
		mu       sync.Mutex
		firstErr error
		wg       sync.WaitGroup
	)
	work := make(chan job)
	for range min(stats.Jobs, max(len(jobs), 1)) {
		wg.Go(func() {
			for j := range work {
				if !budget.acquire(ctx, j.cost) {
					continue
				}
				p, err := Load(ctx, j.cfg, opts.LoadOptions)
				verr := visit(j.cfg, p, err)
				mu.Lock()
				stats.Projects++
				if err != nil {
					stats.Failed++
				} else {
					stats.Files += p.Stats().RootFiles
				}
				if verr != nil && firstErr == nil {
					firstErr = verr
					cancel()
				}
				mu.Unlock()
				if p != nil {
					p.Close()
				}
				budget.release(j.cost)
			}
		})
	}
feed:
	for _, j := range jobs {
		select {
		case work <- j:
		case <-ctx.Done():
			break feed
		}
	}
	close(work)
	wg.Wait()
	stats.PeakHeap = sampler.stop()
	stats.Elapsed = time.Since(start)
	if opts.cache != nil {
		stats.CacheHits, stats.CacheMisses = opts.cache.hits.Load(), opts.cache.misses.Load()
	}
	if firstErr == nil {
		firstErr = parent.Err()
	}
	return stats, firstErr
}

// estimateCost guesses a project's peak memory from its source size. The
// constants come from the v0.1 spike: about 45 bytes of checker memory per
// byte of source on the largest repo, plus a base for lib and node_modules
// declarations (~270 MB for a 65-file Next.js app).
func estimateCost(cfg ProjectConfig) uint64 {
	const (
		base    = 200 << 20
		perByte = 45
	)
	var size uint64
	for _, f := range cfg.Files {
		if st, err := os.Stat(filepath.FromSlash(f)); err == nil {
			size += uint64(max(st.Size(), 0))
		}
	}
	return base + size*perByte
}

// budget is a counting semaphore over bytes.
type budget struct {
	mu    sync.Mutex
	cond  *sync.Cond
	total uint64
	used  uint64
}

func newBudget(total uint64) *budget {
	b := &budget{total: total}
	b.cond = sync.NewCond(&b.mu)
	return b
}

// acquire waits until cost fits; it returns false when ctx is done.
func (b *budget) acquire(ctx context.Context, cost uint64) bool {
	stop := context.AfterFunc(ctx, func() {
		b.mu.Lock()
		b.cond.Broadcast()
		b.mu.Unlock()
	})
	defer stop()
	b.mu.Lock()
	defer b.mu.Unlock()
	for b.used+cost > b.total && b.used > 0 {
		if ctx.Err() != nil {
			return false
		}
		b.cond.Wait()
	}
	if ctx.Err() != nil {
		return false
	}
	b.used += cost
	return true
}

func (b *budget) release(cost uint64) {
	b.mu.Lock()
	b.used -= cost
	b.cond.Broadcast()
	b.mu.Unlock()
}

// heapSampler records the peak live heap every 100 ms.
type heapSampler struct {
	done chan struct{}
	peak chan uint64
}

func startHeapSampler() *heapSampler {
	s := &heapSampler{done: make(chan struct{}), peak: make(chan uint64, 1)}
	go func() {
		sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
		var peak uint64
		read := func() {
			metrics.Read(sample)
			if sample[0].Value.Kind() == metrics.KindUint64 {
				peak = max(peak, sample[0].Value.Uint64())
			}
		}
		t := time.NewTicker(100 * time.Millisecond)
		defer t.Stop()
		for {
			read()
			select {
			case <-t.C:
			case <-s.done:
				read()
				s.peak <- peak
				return
			}
		}
	}()
	return s
}

func (s *heapSampler) stop() uint64 {
	close(s.done)
	return <-s.peak
}

// PeakHeap measures the peak live heap while f runs, for benchmarks and
// debug output.
func PeakHeap(f func()) uint64 {
	s := startHeapSampler()
	f()
	return s.stop()
}

// ProcessPeakRSS returns this process's peak resident memory in bytes (0
// when the platform doesn't say), for benchmarks and debug output.
func ProcessPeakRSS() uint64 { return sysmem.PeakRSS() }
