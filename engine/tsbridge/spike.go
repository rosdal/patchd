package tsbridge

// Spike code for ADR 0007: lives on the spike/tsgo branch only.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/microsoft/typescript-go/shim/ast"
	"github.com/microsoft/typescript-go/shim/bundled"
	"github.com/microsoft/typescript-go/shim/checker"
	"github.com/microsoft/typescript-go/shim/compiler"
	"github.com/microsoft/typescript-go/shim/core"
	"github.com/microsoft/typescript-go/shim/scanner"
	"github.com/microsoft/typescript-go/shim/tsoptions"
	"github.com/microsoft/typescript-go/shim/tspath"
	"github.com/microsoft/typescript-go/shim/vfs/cachedvfs"
	"github.com/microsoft/typescript-go/shim/vfs/osvfs"
)

// SpikeStats is what the spike measures for one project.
type SpikeStats struct {
	Files           int // source files in the program, including lib and node_modules .d.ts
	ProjectFiles    int // files outside node_modules and the default lib
	ConfigErrors    int
	SemanticErrors  int
	Calls           int
	ResolvedCalls   int
	LoadTime        time.Duration // parse tsconfig, create program, bind
	CheckTime       time.Duration // semantic diagnostics for project files
	WalkTime        time.Duration // call resolution over project files
	HeapPeakApproxB uint64        // Go runtime Sys after the run
}

// SpikeCalls loads the project at tsconfigPath, type-checks it, and writes one
// line per call expression in project files to out:
//
//	file:line callee-symbol → declaration-file:line | first-arg-type
func SpikeCalls(ctx context.Context, tsconfigPath string, out io.Writer) (*SpikeStats, error) {
	stats := &SpikeStats{}
	abs, err := filepath.Abs(tsconfigPath)
	if err != nil {
		return nil, err
	}
	configPath := tspath.NormalizePath(filepath.ToSlash(abs))
	root := tspath.GetDirectoryPath(configPath)

	start := time.Now()
	fs := bundled.WrapFS(cachedvfs.From(osvfs.FS()))
	host := compiler.NewCompilerHost(root, fs, bundled.LibPath(), nil, nil)
	config, diags := tsoptions.GetParsedCommandLineOfConfigFile(configPath, &core.CompilerOptions{}, nil, host, nil)
	if config == nil {
		return nil, fmt.Errorf("parse %s: %d diagnostics", configPath, len(diags))
	}
	stats.ConfigErrors = len(diags) + len(config.Errors)
	program := compiler.NewProgram(compiler.ProgramOptions{
		Config:         config,
		Host:           host,
		SingleThreaded: core.TSFalse,
	})
	if program == nil {
		return nil, errors.New("couldn't create program")
	}
	program.BindSourceFiles()
	stats.LoadTime = time.Since(start)

	var files []*ast.SourceFile
	for _, f := range program.GetSourceFiles() {
		stats.Files++
		if program.IsSourceFileDefaultLibrary(f.Path()) || program.IsSourceFileFromExternalLibrary(f) ||
			strings.Contains(f.FileName(), "/node_modules/") {
			continue
		}
		files = append(files, f)
	}
	stats.ProjectFiles = len(files)

	start = time.Now()
	var semErrors atomic.Int64
	work := make(chan *ast.SourceFile)
	var wg sync.WaitGroup
	for range runtime.GOMAXPROCS(0) {
		wg.Go(func() {
			for f := range work {
				diags := program.GetSemanticDiagnostics(ctx, f)
				semErrors.Add(int64(len(diags)))
				if os.Getenv("SPIKE_DIAG") != "" {
					for _, d := range diags {
						fmt.Fprintf(os.Stderr, "diag %s: TS%d %s\n", f.FileName(), d.Code(), d.String())
					}
				}
			}
		})
	}
	for _, f := range files {
		work <- f
	}
	close(work)
	wg.Wait()
	stats.SemanticErrors = int(semErrors.Load())
	stats.CheckTime = time.Since(start)

	start = time.Now()
	if os.Getenv("SPIKE_NO_WALK") != "" {
		files = nil
	}
	for _, f := range files {
		c, done := program.GetTypeCheckerForFile(ctx, f)
		walkCalls(c, f, root, out, stats)
		done()
	}
	stats.WalkTime = time.Since(start)

	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	stats.HeapPeakApproxB = ms.Sys
	return stats, nil
}

func walkCalls(c *checker.Checker, f *ast.SourceFile, root string, out io.Writer, stats *SpikeStats) {
	var visit ast.Visitor
	visit = func(n *ast.Node) bool {
		if n.Kind == ast.KindCallExpression {
			stats.Calls++
			if writeCall(c, f, n, root, out) {
				stats.ResolvedCalls++
			}
		}
		n.ForEachChild(visit)
		return false
	}
	f.AsNode().ForEachChild(visit)
}

func writeCall(c *checker.Checker, f *ast.SourceFile, call *ast.Node, root string, out io.Writer) bool {
	callee := call.Expression()
	nameNode := callee
	if callee.Kind == ast.KindPropertyAccessExpression {
		nameNode = callee.Name()
	}
	name := "?"
	if sym := c.GetSymbolAtLocation(nameNode); sym != nil {
		if sym.Flags&ast.SymbolFlagsAlias != 0 {
			sym = c.GetAliasedSymbol(sym)
		}
		name = sym.Name
	}

	target, resolved := "unresolved", false
	if sig := c.GetResolvedSignature(call); sig != nil {
		if decl := sig.Declaration(); decl != nil {
			if df := ast.GetSourceFileOfNode(decl); df != nil {
				target = position(df, decl, root)
				resolved = true
			}
		}
	}

	argType := "-"
	if args := call.Arguments(); len(args) > 0 {
		argType = c.TypeToString(c.GetTypeAtLocation(args[0]))
	}
	_, _ = fmt.Fprintf(out, "%s %s → %s | %s\n", position(f, call, root), name, target, argType)
	return resolved
}

func position(f *ast.SourceFile, n *ast.Node, root string) string {
	pos := scanner.SkipTrivia(f.Text(), n.Pos())
	line := scanner.GetECMALineOfPosition(f, pos) + 1
	name := f.FileName()
	if rel, ok := strings.CutPrefix(name, root+"/"); ok {
		name = rel
	}
	return fmt.Sprintf("%s:%d", name, line)
}
