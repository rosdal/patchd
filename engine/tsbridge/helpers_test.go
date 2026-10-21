package tsbridge

import (
	"context"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// loadFixture discovers testdata/<dir> and loads its first project.
func loadFixture(t *testing.T, dir string, opts LoadOptions) *Program {
	t.Helper()
	ctx := context.Background()
	ws, err := Discover(ctx, filepath.Join("testdata", dir), DiscoverOptions{NoRepoConfig: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(ws.Projects) == 0 {
		t.Fatalf("%s: no projects", dir)
	}
	p, err := Load(ctx, ws.Projects[0], opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

// fileNamed returns the program file whose path ends in /name.
func fileNamed(t *testing.T, p *Program, name string) *File {
	t.Helper()
	for _, id := range p.Files() {
		if f := p.File(id); path.Base(f.Path) == name {
			return f
		}
	}
	t.Fatalf("no file %s in the program", name)
	return nil
}

// offsetOf returns the byte offset of the nth (0-based) occurrence of needle.
func offsetOf(t *testing.T, f *File, needle string, nth int) Pos {
	t.Helper()
	off := -1
	for i := 0; i <= nth; i++ {
		j := strings.Index(f.Text[off+1:], needle)
		if j < 0 {
			t.Fatalf("%s: occurrence %d of %q not found", f.Path, nth, needle)
		}
		off += 1 + j
	}
	return Pos(off)
}
