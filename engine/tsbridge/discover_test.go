package tsbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

// goldenWorkspace is a Workspace with paths relative to its root and no timings.
type goldenWorkspace struct {
	Layouts   []string        `json:"layouts,omitempty"`
	Packages  []goldenPackage `json:"packages,omitempty"`
	Projects  []goldenProject `json:"projects"`
	Uncovered []string        `json:"uncovered,omitempty"`
	Warnings  []string        `json:"warnings,omitempty"`
}

type goldenPackage struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	Dir     string `json:"dir"`
	Config  string `json:"config,omitempty"`
}

type goldenProject struct {
	Name        string   `json:"name"`
	Synthesised bool     `json:"synthesised,omitempty"`
	Files       []string `json:"files"`
	References  []string `json:"references,omitempty"`
	Extends     []string `json:"extends,omitempty"`
}

func toGolden(ws *Workspace) goldenWorkspace {
	rel := func(p string) string { return relPath(ws.Root, p) }
	g := goldenWorkspace{Layouts: ws.Layouts, Projects: []goldenProject{}}
	for _, p := range ws.Packages {
		g.Packages = append(g.Packages, goldenPackage{p.Name, p.Version, rel(p.Dir), p.Config})
	}
	for _, p := range ws.Projects {
		gp := goldenProject{Name: p.Name, Synthesised: p.Synthesised}
		for _, f := range p.Files {
			gp.Files = append(gp.Files, rel(f))
		}
		for _, r := range p.References {
			gp.References = append(gp.References, rel(r))
		}
		for _, e := range p.Extends {
			gp.Extends = append(gp.Extends, rel(e))
		}
		g.Projects = append(g.Projects, gp)
	}
	for _, u := range ws.Uncovered {
		g.Uncovered = append(g.Uncovered, rel(u))
	}
	for _, w := range ws.Warnings {
		// Messages may embed absolute paths; keep them stable.
		g.Warnings = append(g.Warnings, strings.ReplaceAll(w.String(), ws.Root+"/", ""))
	}
	return g
}

// checkGolden compares got with testdata/<name>, or rewrites it with -update.
func checkGolden(t *testing.T, name string, got any) {
	t.Helper()
	b, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, b, 0o600); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path) //nolint:gosec // a golden file under testdata
	if err != nil {
		t.Fatalf("%v (run go test ./engine/tsbridge/... -update)", err)
	}
	if !bytes.Equal(bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")), b) {
		t.Errorf("%s differs from the golden file (run with -update and review the diff):\n%s", name, b)
	}
}

func TestDiscoverGolden(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("testdata", "workspaces"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		t.Run(e.Name(), func(t *testing.T) {
			ws, err := Discover(context.Background(), filepath.Join("testdata", "workspaces", e.Name()), DiscoverOptions{})
			if err != nil {
				t.Fatal(err)
			}
			checkGolden(t, filepath.Join("workspaces", e.Name()+".discover.golden.json"), toGolden(ws))
		})
	}
}

func TestDiscoverOptionsOverrideRepoConfig(t *testing.T) {
	ws, err := Discover(context.Background(), filepath.Join("testdata", "workspaces", "override"), DiscoverOptions{
		Projects: []string{"tsconfig.json"}, NoRepoConfig: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(ws.Projects) != 2 || ws.Projects[0].Name != "tsconfig.json" {
		t.Fatalf("projects = %+v, want only tsconfig.json", toGolden(ws).Projects)
	}
	// Without allowJs, "**/*" includes only the TypeScript files.
	g := toGolden(ws)
	if got := g.Projects[0].Files; strings.Join(got, ",") != "app/main.ts,lib.ts" {
		t.Errorf("root tsconfig owns %v, want [app/main.ts lib.ts]", got)
	}
	if strings.Join(g.Uncovered, ",") != "legacy/old.js" {
		t.Errorf("uncovered = %v, want [legacy/old.js] (patchd.yaml's ignore is off)", g.Uncovered)
	}
}

func TestDiscoverMissingRoot(t *testing.T) {
	if _, err := Discover(context.Background(), filepath.Join("testdata", "does-not-exist"), DiscoverOptions{}); err == nil {
		t.Fatal("want an error for a missing root")
	}
}

func TestDiscoverCancelled(*testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// A tiny workspace finishes before the walk checks the context; it must
	// not panic or hang either way.
	_, _ = Discover(ctx, filepath.Join("testdata", "workspaces", "single"), DiscoverOptions{})
}
