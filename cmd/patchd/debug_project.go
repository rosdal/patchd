package main

import (
	"strings"
	"sync"

	"github.com/spf13/cobra"

	"github.com/patchd/patchd/engine/tsbridge"
)

type projectReport struct {
	Name        string      `json:"name"`
	Config      string      `json:"config,omitempty"`
	Synthesised bool        `json:"synthesised,omitempty"`
	Files       []string    `json:"files"`
	References  []string    `json:"references,omitempty"`
	Extends     []string    `json:"extends,omitempty"`
	Load        *loadReport `json:"load,omitempty"`
	Error       string      `json:"error,omitempty"`
}

type loadReport struct {
	tsbridge.Stats
	Errors   int                `json:"errors"`
	Warnings []tsbridge.Warning `json:"warnings,omitempty"`
}

type workspaceReport struct {
	Root       string                 `json:"root"`
	Layouts    []string               `json:"layouts,omitempty"`
	Packages   []tsbridge.Package     `json:"packages,omitempty"`
	DiscoverMS float64                `json:"discover_ms"`
	Projects   []*projectReport       `json:"projects"`
	Uncovered  []string               `json:"uncovered,omitempty"`
	Warnings   []tsbridge.Warning     `json:"warnings,omitempty"`
	LoadAll    *tsbridge.LoadAllStats `json:"load_all,omitempty"`
}

func newDebugProjectCmd(f *debugFlags) *cobra.Command {
	var noLoad bool
	cmd := &cobra.Command{
		Use:   "project [workspace-root]",
		Short: "Show discovered projects, which config covers which files, warnings and time per phase",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := f.validate(); err != nil {
				return err
			}
			// A path names the workspace to discover; without one, the
			// enclosing git repository (or --root).
			root := f.root
			if len(args) == 1 && root == "" {
				root = args[0]
			} else if root == "" {
				r, err := f.workspaceRoot(".")
				if err != nil {
					return err
				}
				root = r
			}
			ctx := cmd.Context()
			ws, err := tsbridge.Discover(ctx, root, tsbridge.DiscoverOptions{})
			if err != nil {
				return err
			}
			rel := func(p string) string { return relTo(ws.Root, p) }
			rep := &workspaceReport{
				Root: ws.Root, Layouts: ws.Layouts, Packages: ws.Packages, Warnings: ws.Warnings,
				DiscoverMS: float64(ws.Elapsed.Microseconds()) / 1000,
			}
			byName := map[string]*projectReport{}
			for _, p := range ws.Projects {
				pr := &projectReport{Name: p.Name, Synthesised: p.Synthesised}
				if p.ConfigPath != "" {
					pr.Config = rel(p.ConfigPath)
				}
				for _, file := range p.Files {
					pr.Files = append(pr.Files, rel(file))
				}
				for _, r := range p.References {
					pr.References = append(pr.References, rel(r))
				}
				for _, e := range p.Extends {
					pr.Extends = append(pr.Extends, rel(e))
				}
				rep.Projects = append(rep.Projects, pr)
				byName[p.Name] = pr
			}
			for _, u := range ws.Uncovered {
				rep.Uncovered = append(rep.Uncovered, rel(u))
			}
			if !noLoad {
				opts, err := f.loadAllOptions()
				if err != nil {
					return err
				}
				var mu sync.Mutex
				stats, err := tsbridge.LoadAll(ctx, ws, opts, func(cfg tsbridge.ProjectConfig, p *tsbridge.Program, lerr error) error {
					mu.Lock()
					defer mu.Unlock()
					pr := byName[cfg.Name]
					if lerr != nil {
						pr.Error = lerr.Error()
						return nil
					}
					lr := &loadReport{Stats: p.Stats(), Warnings: p.Warnings()}
					for _, d := range p.Diagnostics() {
						if d.Category == tsbridge.DiagnosticError {
							lr.Errors++
						}
					}
					pr.Load = lr
					return nil
				})
				if err != nil {
					return err
				}
				rep.LoadAll = &stats
			}
			out := cmd.OutOrStdout()
			if f.format == "json" {
				return writeJSON(out, rep)
			}
			tw := &textWriter{w: out}
			tw.printf("root: %s\n", rep.Root)
			if len(rep.Layouts) > 0 {
				tw.printf("layouts: %s\n", strings.Join(rep.Layouts, ", "))
			}
			for _, pkg := range rep.Packages {
				tw.printf("package %s %s (%s) -> %s\n", pkg.Name, pkg.Version, rel(pkg.Dir), orDash(pkg.Config))
			}
			tw.printf("discover: %.0f ms, %d project(s), %d uncovered file(s)\n\n", rep.DiscoverMS, len(rep.Projects), len(rep.Uncovered))
			for _, pr := range rep.Projects {
				tw.printf("%s: %d file(s)", pr.Name, len(pr.Files))
				if len(pr.Extends) > 0 {
					tw.printf(", extends %s", strings.Join(pr.Extends, " -> "))
				}
				tw.println()
				for i, file := range pr.Files {
					if i == 5 {
						tw.printf("    … %d more\n", len(pr.Files)-5)
						break
					}
					tw.printf("    %s\n", file)
				}
				if pr.Error != "" {
					tw.printf("  load failed: %s\n", pr.Error)
				}
				if l := pr.Load; l != nil {
					tw.printf("  program: %d files (%d root, %d lines); parse %s, bind %s, check %s; %d type error(s)\n",
						l.Files, l.RootFiles, l.Lines, ms(l.Parse), ms(l.Bind), ms(l.Check), l.Errors)
					for _, w := range l.Warnings {
						tw.printf("  warning %s\n", w)
					}
				}
			}
			if len(rep.Warnings) > 0 {
				tw.println("\nworkspace warnings:")
				for _, w := range rep.Warnings {
					tw.printf("  %s\n", w)
				}
			}
			if s := rep.LoadAll; s != nil {
				tw.printf("\nloaded %d project(s) (%d failed), %d files in %s with %d job(s); peak heap %s; shared parse cache %d hits / %d misses\n",
					s.Projects, s.Failed, s.Files, ms(s.Elapsed), s.Jobs, formatBytes(s.PeakHeap), s.CacheHits, s.CacheMisses)
			}
			return tw.err
		},
	}
	cmd.Flags().BoolVar(&noLoad, "no-load", false, "discover only; do not load or type-check")
	return cmd
}

func relTo(root, p string) string {
	if strings.HasPrefix(p, root+"/") {
		return p[len(root)+1:]
	}
	return p
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
