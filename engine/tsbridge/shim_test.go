package tsbridge

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/microsoft/typescript-go/shim/ast"
	"github.com/microsoft/typescript-go/shim/bundled"
	"github.com/microsoft/typescript-go/shim/compiler"
	"github.com/microsoft/typescript-go/shim/core"
	"github.com/microsoft/typescript-go/shim/tsoptions"
	"github.com/microsoft/typescript-go/shim/tspath"
	"github.com/microsoft/typescript-go/shim/vfs/cachedvfs"
	"github.com/microsoft/typescript-go/shim/vfs/osvfs"
)

// TestShimTypeChecksProject guards the shim mechanism (ADR 0007): it loads a
// tsconfig through the go:linkname shims, type-checks it, and resolves a call.
// It fails when a typescript-go upgrade breaks the shims.
func TestShimTypeChecksProject(t *testing.T) {
	ctx := context.Background()
	abs, err := filepath.Abs("testdata/smoke/tsconfig.json")
	if err != nil {
		t.Fatal(err)
	}
	configPath := tspath.NormalizePath(filepath.ToSlash(abs))
	root := tspath.GetDirectoryPath(configPath)

	fs := bundled.WrapFS(cachedvfs.From(osvfs.FS()))
	host := compiler.NewCompilerHost(root, fs, bundled.LibPath(), nil, nil)
	config, diags := tsoptions.GetParsedCommandLineOfConfigFile(configPath, &core.CompilerOptions{}, nil, host, nil)
	if config == nil || len(diags)+len(config.Errors) > 0 {
		t.Fatalf("parse tsconfig: %d diagnostics", len(diags))
	}
	program := compiler.NewProgram(compiler.ProgramOptions{Config: config, Host: host})
	program.BindSourceFiles()

	var file *ast.SourceFile
	for _, f := range program.GetSourceFiles() {
		if strings.HasSuffix(f.FileName(), "/index.ts") {
			file = f
		}
	}
	if file == nil {
		t.Fatal("index.ts not in program")
	}
	if d := program.GetSemanticDiagnostics(ctx, file); len(d) > 0 {
		t.Fatalf("unexpected diagnostics: %s", d[0].String())
	}

	c, done := program.GetTypeCheckerForFile(ctx, file)
	defer done()
	var resolved string
	var visit ast.Visitor
	visit = func(n *ast.Node) bool {
		if n.Kind == ast.KindCallExpression {
			if decl := c.GetResolvedSignature(n).Declaration(); decl != nil {
				resolved = decl.Name().Text()
			}
			return true
		}
		return n.ForEachChild(visit)
	}
	file.AsNode().ForEachChild(visit)
	if resolved != "greet" {
		t.Fatalf("call resolved to %q, want greet", resolved)
	}
}
