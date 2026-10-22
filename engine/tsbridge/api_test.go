package tsbridge

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/microsoft/typescript-go/shim/vfs"
)

func TestExportedHelpers(t *testing.T) {
	if got := (TypeString | TypeLiteral).Strings(); !slices.Equal(got, []string{"string", "literal"}) {
		t.Errorf("TypeFlags.Strings = %v", got)
	}
	if KindCallExpression.String() != "CallExpression" || Kind(60000).String() != "Kind(?)" {
		t.Errorf("Kind.String = %q", KindCallExpression.String())
	}
	if !KindArrowFunction.IsFunctionLike() || KindCallExpression.IsFunctionLike() {
		t.Error("IsFunctionLike")
	}
	if !KindJsxSelfClosingElement.IsCallLike() || KindIdentifier.IsCallLike() {
		t.Error("IsCallLike")
	}
	if heap := PeakHeap(func() { _ = make([]byte, 1<<20) }); heap == 0 {
		t.Error("PeakHeap measured nothing")
	}
	_ = ProcessPeakRSS() // 0 on platforms that don't report it; must not fail
}

func TestProgramFileLookupAndDeclarations(t *testing.T) {
	p := loadFixture(t, "resolve", LoadOptions{})
	main := fileNamed(t, p, "main.ts")
	if got := p.FileByPath(main.Path); got != main {
		t.Errorf("FileByPath = %v", got)
	}
	if got := p.FileByPath(strings.ToUpper(main.Path)); got != main {
		t.Error("FileByPath must match case-insensitively (Windows paths)")
	}
	if p.FileByPath("/nope.ts") != nil {
		t.Error("FileByPath of a missing file")
	}
	// An import alias resolves to its target's declarations in another file.
	n := nodeAtText(t, p, "main.ts", "query(\"select 2\")", 0)
	s, ok := p.SymbolOf(n)
	if !ok {
		t.Fatal("no symbol for query")
	}
	target := p.ResolveAlias(s)
	decls := p.Declarations(target)
	if target == 0 || len(decls) != 1 || !strings.HasSuffix(decls[0].Path, "/src/app/db.ts") || decls[0].Line != 3 {
		t.Errorf("query -> %v %+v", target, decls)
	}
	if got := p.Declarations(s); len(got) != 1 || !strings.HasSuffix(got[0].Path, "/src/main.ts") {
		t.Errorf("the alias itself is declared at the import: %+v", got)
	}
	if p.CompilerKind(n) != "Identifier" || p.CompilerKind(0) != "" {
		t.Errorf("CompilerKind = %q", p.CompilerKind(n))
	}
	if p.ResolveAlias(0) != 0 || p.Declarations(0) != nil || p.QualifiedName(0) != "" || p.SymbolName(0) != "" {
		t.Error("SymbolID 0 must answer with zero values")
	}
	// A missing package's import has no target.
	m, _ := p.SymbolOf(nodeAtText(t, p, "main.ts", "missing()", 0))
	if p.ResolveAlias(m) != 0 {
		t.Error("ResolveAlias of an unresolved import must be 0")
	}
}

func TestReadOnlyFS(t *testing.T) {
	fs := newReadOnlyFS()
	for _, err := range []error{
		fs.WriteFile("x", "y"), fs.AppendFile("x", "y"), fs.Remove("x"), fs.Chtimes("x", time.Now(), time.Now()),
	} {
		if !errors.Is(err, vfs.ErrPermission) {
			t.Errorf("write allowed: %v", err)
		}
	}
	fs.set("/virtual/tsconfig.json", "{}")
	if s, ok := fs.ReadFile("/virtual/tsconfig.json"); !ok || s != "{}" || !fs.FileExists("/virtual/tsconfig.json") || fs.Realpath("/virtual/tsconfig.json") != "/virtual/tsconfig.json" {
		t.Error("overlay file not served")
	}
	if fs.FileExists("/virtual/other.json") {
		t.Error("overlay invented a file")
	}
}

func TestIgnoreRules(t *testing.T) {
	var r ignoreRules
	r.add("", "# comment\n*.log\n/build-root\nlogs/\n!keep.log\ndocs/**/draft.md\n\\#hash\nspaced \n")
	r.add("pkg", "local.txt\n/anchored.ts\n")
	tests := []struct {
		path  string
		isDir bool
		want  bool
	}{
		{"a.log", false, true},
		{"deep/b.log", false, true},
		{"keep.log", false, false}, // re-included
		{"build-root", true, true},
		{"sub/build-root", true, false}, // anchored to the root
		{"logs", true, true},
		{"logs", false, false}, // directory-only pattern
		{"docs/a/b/draft.md", false, true},
		{"docs/draft.md", false, true},
		{"#hash", false, true},
		{"spaced", false, true},
		{"pkg/local.txt", false, true},
		{"pkg/deep/local.txt", false, true},
		{"local.txt", false, false}, // only under pkg/
		{"pkg/anchored.ts", false, true},
		{"pkg/deep/anchored.ts", false, false},
	}
	for _, tt := range tests {
		if got := r.ignored(tt.path, tt.isDir); got != tt.want {
			t.Errorf("ignored(%q, dir=%v) = %v, want %v", tt.path, tt.isDir, got, tt.want)
		}
	}
}

func TestPackageNames(t *testing.T) {
	for spec, want := range map[string]string{
		"express": "express", "pg/lib/client": "pg", "@prisma/client": "@prisma/client",
		"@scope/pkg/sub/path": "@scope/pkg",
	} {
		if got := packageName(spec); got != want {
			t.Errorf("packageName(%q) = %q, want %q", spec, got, want)
		}
	}
	for spec, want := range map[string]bool{"fs": true, "node:test": true, "fs/promises": true, "express": false, "fsx": false} {
		if isNodeBuiltin(spec) != want {
			t.Errorf("isNodeBuiltin(%q) != %v", spec, want)
		}
	}
	if ambientPrefix("node:fs", nil) != "node:fs." || ambientPrefix("custom", nil) != "custom:" {
		t.Error("ambientPrefix")
	}
	for in, want := range map[string]string{"lib/*": "./lib/*", "./x": "./x", "../y": "../y", "/abs": "/abs"} {
		if got := relativeSpec(in); got != want {
			t.Errorf("relativeSpec(%q) = %q", in, got)
		}
	}
}

func TestWorkspaceGlobs(t *testing.T) {
	for raw, want := range map[string]string{
		`["a/*","b"]`:             "a/*,b",
		`{"packages":["apps/*"]}`: "apps/*",
		`{"nohoist":["x"]}`:       "",
		`42`:                      "",
	} {
		pj := packageJSON{Workspaces: []byte(raw)}
		if got := strings.Join(pj.workspaceGlobs(), ","); got != want {
			t.Errorf("workspaceGlobs(%s) = %q, want %q", raw, got, want)
		}
	}
}
