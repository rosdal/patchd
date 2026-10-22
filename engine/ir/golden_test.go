package ir

import (
	"bytes"
	"context"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/patchd/patchd/engine/tsbridge"
)

var update = flag.Bool("update", false, "rewrite the .ir.golden files")

// TestGolden lowers every source file under testdata/<area>/ and compares
// its printed IR with the .ir.golden file next to it. Each area is one
// project. Run with -update to rewrite the goldens; review the diff.
func TestGolden(t *testing.T) {
	areas, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range areas {
		if !a.IsDir() {
			continue
		}
		t.Run(a.Name(), func(t *testing.T) {
			t.Parallel()
			dir := filepath.Join("testdata", a.Name())
			progs := loadDir(t, dir)
			abs, _ := filepath.Abs(dir)
			for _, prog := range progs {
				for _, id := range SourceFiles(prog) {
					f := prog.File(id)
					rel := strings.TrimPrefix(filepath.ToSlash(f.Path), filepath.ToSlash(abs)+"/")
					t.Run(rel, func(t *testing.T) {
						checkGolden(t, prog, id, abs, a.Name())
					})
				}
			}
		})
	}
}

func checkGolden(t *testing.T, prog *tsbridge.Program, id tsbridge.FileID, root, area string) {
	t.Helper()
	m, _ := BuildModule(prog, id)
	if m == nil {
		t.Fatal("no module")
	}
	if err := ValidateModule(m); err != nil {
		t.Errorf("invalid IR:\n%v", err)
	}
	var buf bytes.Buffer
	// Most areas print bare IR; types prints TypeScript types and
	// destructuring prints variable names and destructuring paths.
	opts := PrintOptions{Root: root}
	switch area {
	case "types":
		opts.Types, opts.Prog = true, prog
	case "destructuring":
		opts.Names = true
	}
	if err := Print(&buf, m, opts); err != nil {
		t.Fatal(err)
	}
	got := buf.String()
	path := prog.File(id).Path
	golden := strings.TrimSuffix(path, filepath.Ext(path)) + ".ir.golden"
	if *update {
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil { //nolint:gosec // test output
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden) //nolint:gosec // a testdata path
	if err != nil {
		t.Fatalf("%v (run go test ./engine/ir -run TestGolden -update)", err)
	}
	if string(want) != got {
		t.Errorf("IR differs from %s (run with -update and review the diff):\n%s", filepath.Base(golden), lineDiff(string(want), got))
	}
}

// loadDir discovers and loads every project under dir.
func loadDir(t *testing.T, dir string) []*tsbridge.Program {
	t.Helper()
	ctx := context.Background()
	ws, err := tsbridge.Discover(ctx, dir, tsbridge.DiscoverOptions{NoRepoConfig: true})
	if err != nil {
		t.Fatal(err)
	}
	var progs []*tsbridge.Program
	for _, cfg := range ws.Projects {
		p, err := tsbridge.Load(ctx, cfg, tsbridge.LoadOptions{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		progs = append(progs, p)
	}
	slices.SortFunc(progs, func(a, b *tsbridge.Program) int { return strings.Compare(a.Config().Name, b.Config().Name) })
	return progs
}

// lineDiff shows the first lines where two texts differ.
func lineDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	var out []string
	for i := 0; i < max(len(w), len(g)) && len(out) < 40; i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			out = append(out, "-"+wl, "+"+gl)
		}
	}
	return strings.Join(out, "\n")
}
