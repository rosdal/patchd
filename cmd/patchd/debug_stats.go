package main

import (
	"maps"
	"slices"
	"sync"
	"time"

	"github.com/spf13/cobra"

	"github.com/patchd/patchd/engine/tsbridge"
)

type statsReport struct {
	Root       string                            `json:"root"`
	Projects   int                               `json:"projects"`
	Failed     int                               `json:"failed"`
	Files      int                               `json:"files"`
	Lines      int                               `json:"lines"`
	Nodes      int                               `json:"nodes"`
	Calls      int                               `json:"calls"`
	Resolved   int                               `json:"resolved"`
	Rate       float64                           `json:"resolution_rate"`
	Unresolved map[tsbridge.UnresolvedReason]int `json:"unresolved"`
	TypeErrors int                               `json:"type_errors"`
	Warnings   map[tsbridge.WarningCode]int      `json:"warnings"`
	DiscoverMS float64                           `json:"discover_ms"`
	ParseMS    float64                           `json:"parse_ms"` // summed over projects
	BindMS     float64                           `json:"bind_ms"`
	CheckMS    float64                           `json:"check_ms"`
	WalkMS     float64                           `json:"walk_ms"`
	WallMS     float64                           `json:"wall_ms"`
	PeakHeap   uint64                            `json:"peak_heap_bytes"`
	PeakRSS    uint64                            `json:"peak_rss_bytes"`
	LoadAll    tsbridge.LoadAllStats             `json:"load_all"`
}

func newDebugStatsCmd(f *debugFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "stats [workspace-root]",
		Short: "Show files, nodes, call resolution rate, memory and timings for a workspace",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := f.validate(); err != nil {
				return err
			}
			start := time.Now()
			root := "."
			if len(args) == 1 {
				root = args[0]
			} else if r, err := f.workspaceRoot("."); err == nil {
				root = r
			}
			if f.root != "" {
				root = f.root
			}
			ctx := cmd.Context()
			ws, err := tsbridge.Discover(ctx, root, tsbridge.DiscoverOptions{})
			if err != nil {
				return err
			}
			opts, err := f.loadAllOptions()
			if err != nil {
				return err
			}
			rep := statsReport{
				Root: ws.Root, Unresolved: map[tsbridge.UnresolvedReason]int{}, Warnings: map[tsbridge.WarningCode]int{},
				DiscoverMS: float64(ws.Elapsed.Microseconds()) / 1000,
			}
			for _, w := range ws.Warnings {
				rep.Warnings[w.Code]++
			}
			var mu sync.Mutex
			rep.LoadAll, err = tsbridge.LoadAll(ctx, ws, opts, func(_ tsbridge.ProjectConfig, p *tsbridge.Program, lerr error) error {
				if lerr != nil {
					return nil
				}
				walkStart := time.Now()
				var nodes, calls, resolved int
				unresolved := map[tsbridge.UnresolvedReason]int{}
				for _, id := range p.RootFiles() {
					nodes += p.NodeCount(id)
					p.Walk(id, func(n tsbridge.NodeID) bool {
						if p.Kind(n).IsCallLike() {
							calls++
							if c, ok := p.ResolveCall(n); ok {
								resolved++
							} else {
								unresolved[c.Reason]++
							}
						}
						return true
					})
				}
				walk := time.Since(walkStart)
				st := p.Stats()
				typeErrors := 0
				for _, d := range p.Diagnostics() {
					if d.Category == tsbridge.DiagnosticError {
						typeErrors++
					}
				}
				warnings := p.Warnings()
				mu.Lock()
				defer mu.Unlock()
				rep.Files += st.RootFiles
				rep.Lines += st.Lines
				rep.Nodes += nodes
				rep.Calls += calls
				rep.Resolved += resolved
				for r, n := range unresolved {
					rep.Unresolved[r] += n
				}
				rep.TypeErrors += typeErrors
				for _, w := range warnings {
					rep.Warnings[w.Code]++
				}
				rep.ParseMS += float64(st.Parse.Microseconds()) / 1000
				rep.BindMS += float64(st.Bind.Microseconds()) / 1000
				rep.CheckMS += float64(st.Check.Microseconds()) / 1000
				rep.WalkMS += float64(walk.Microseconds()) / 1000
				return nil
			})
			if err != nil {
				return err
			}
			rep.Projects, rep.Failed, rep.PeakHeap = rep.LoadAll.Projects, rep.LoadAll.Failed, rep.LoadAll.PeakHeap
			if rep.Calls > 0 {
				rep.Rate = float64(rep.Resolved) / float64(rep.Calls)
			}
			rep.WallMS = float64(time.Since(start).Microseconds()) / 1000
			rep.PeakRSS = tsbridge.ProcessPeakRSS()
			out := cmd.OutOrStdout()
			tw := &textWriter{w: out}
			if f.format == "json" {
				return writeJSON(out, rep)
			}
			tw.printf("root:        %s\n", rep.Root)
			tw.printf("projects:    %d (%d failed)\n", rep.Projects, rep.Failed)
			tw.printf("files:       %d (%d lines, %d nodes)\n", rep.Files, rep.Lines, rep.Nodes)
			tw.printf("calls:       %d, %d resolved (%.1f%%)\n", rep.Calls, rep.Resolved, rep.Rate*100)
			for _, r := range slices.Sorted(maps.Keys(rep.Unresolved)) {
				tw.printf("  %-22s %d\n", r, rep.Unresolved[r])
			}
			tw.printf("type errors: %d\n", rep.TypeErrors)
			for _, c := range slices.Sorted(maps.Keys(rep.Warnings)) {
				tw.printf("warnings %s: %d\n", c, rep.Warnings[c])
			}
			tw.printf("time:        discover %.0f ms, parse %.0f ms, bind %.0f ms, check %.0f ms, walk %.0f ms (summed); wall %.0f ms\n",
				rep.DiscoverMS, rep.ParseMS, rep.BindMS, rep.CheckMS, rep.WalkMS, rep.WallMS)
			tw.printf("peak heap:   %s (process peak RSS %s)\n", formatBytes(rep.PeakHeap), formatBytes(rep.PeakRSS))
			return tw.err
		},
	}
}
