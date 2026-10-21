package tsbridge

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoadAllWorkspace(t *testing.T) {
	ws, err := Discover(context.Background(), filepath.Join("testdata", "workspaces", "pnpm-workspace"), DiscoverOptions{NoRepoConfig: true})
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	names := map[string]bool{}
	stats, err := LoadAll(context.Background(), ws, LoadAllOptions{Jobs: 4}, func(cfg ProjectConfig, p *Program, err error) error {
		if err != nil {
			t.Errorf("%s: %v", cfg.Name, err)
			return nil
		}
		if p.Closed() || len(p.RootFiles()) == 0 {
			t.Errorf("%s: program closed or empty inside visit", cfg.Name)
		}
		mu.Lock()
		names[cfg.Name] = true
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != len(ws.Projects) || stats.Projects != len(ws.Projects) || stats.Failed != 0 || stats.Files != 2 {
		t.Errorf("visited %v; stats %+v", names, stats)
	}
	if stats.CacheHits == 0 {
		t.Errorf("no shared parse cache hits across %d programs: %+v", stats.Projects, stats)
	}
	if stats.PeakHeap == 0 || stats.Budget == 0 {
		t.Errorf("stats missing memory numbers: %+v", stats)
	}
}

func TestLoadAllStopsOnVisitError(t *testing.T) {
	ws, err := Discover(context.Background(), filepath.Join("testdata", "workspaces", "pnpm-workspace"), DiscoverOptions{NoRepoConfig: true})
	if err != nil {
		t.Fatal(err)
	}
	stop := errors.New("stop")
	var visits atomic.Int32
	_, err = LoadAll(context.Background(), ws, LoadAllOptions{Jobs: 1}, func(ProjectConfig, *Program, error) error {
		visits.Add(1)
		return stop
	})
	if !errors.Is(err, stop) || visits.Load() != 1 {
		t.Errorf("err %v after %d visits, want stop after 1", err, visits.Load())
	}
}

func TestLoadAllCancelled(t *testing.T) {
	ws, err := Discover(context.Background(), filepath.Join("testdata", "workspaces", "pnpm-workspace"), DiscoverOptions{NoRepoConfig: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := LoadAll(ctx, ws, LoadAllOptions{}, func(ProjectConfig, *Program, error) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestBudgetBlocksUntilReleased(t *testing.T) {
	b := newBudget(100)
	ctx := context.Background()
	if !b.acquire(ctx, 80) {
		t.Fatal("first acquire failed")
	}
	got := make(chan bool)
	go func() { got <- b.acquire(ctx, 50) }()
	select {
	case <-got:
		t.Fatal("acquire went over the budget")
	case <-time.After(50 * time.Millisecond):
	}
	b.release(80)
	if !<-got {
		t.Fatal("acquire failed after release")
	}
	// A cost larger than the budget still runs when nothing else does.
	b.release(50)
	if !b.acquire(ctx, 500) {
		t.Fatal("an oversized job must run alone")
	}
	// Cancellation wakes waiters.
	cctx, cancel := context.WithCancel(ctx)
	go func() { got <- b.acquire(cctx, 10) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	if <-got {
		t.Fatal("acquire succeeded after cancel")
	}
}

// TestConcurrentQueries hammers one program from several goroutines; run
// with -race (CI does) to check the locking.
func TestConcurrentQueries(t *testing.T) {
	p := loadFixture(t, "resolve", LoadOptions{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for _, id := range p.RootFiles() {
				p.Walk(id, func(n NodeID) bool {
					p.TypeString(p.TypeOf(n))
					if s, ok := p.SymbolOf(n); ok {
						p.QualifiedName(s)
					}
					if p.Kind(n).IsCallLike() {
						p.ResolveCall(n)
					}
					return true
				})
			}
		})
	}
	wg.Wait()
	if st := p.Stats(); st.TypeOfHits == 0 {
		t.Errorf("no TypeOf cache hits across goroutines: %+v", st)
	}
}

func TestEstimateCost(t *testing.T) {
	small := estimateCost(ProjectConfig{})
	big := estimateCost(ProjectConfig{Files: []string{filepath.ToSlash(mustAbs(t, "load.go"))}})
	if small == 0 || big <= small {
		t.Errorf("estimateCost: empty %d, one file %d", small, big)
	}
}

func mustAbs(t *testing.T, p string) string {
	t.Helper()
	abs, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return abs
}
