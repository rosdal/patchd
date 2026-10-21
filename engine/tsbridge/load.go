package tsbridge

import (
	"context"
	"fmt"
	"log/slog"
	"path"
	"runtime/debug"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/microsoft/typescript-go/shim/ast"
	"github.com/microsoft/typescript-go/shim/bundled"
	"github.com/microsoft/typescript-go/shim/checker"
	"github.com/microsoft/typescript-go/shim/compiler"
	"github.com/microsoft/typescript-go/shim/core"
	"github.com/microsoft/typescript-go/shim/tsoptions"
	"github.com/microsoft/typescript-go/shim/vfs/cachedvfs"
)

// Program is one loaded, bound and type-checked project. Its methods are safe
// for concurrent use; queries are serialised on the program. Handles it
// returns (FileID, NodeID, SymbolID, TypeID) are valid only for it.
type Program struct {
	mu       sync.Mutex
	ctx      context.Context
	cancel   context.CancelFunc
	cfg      ProjectConfig
	prog     *compiler.Program
	closed   bool
	panics   int
	warnings []Warning
	diags    []Diagnostic
	stats    Stats

	files     []*File           // FileID-1 -> file
	sources   []*ast.SourceFile // FileID-1 -> compiler file
	fileOf    map[*ast.SourceFile]FileID
	trees     []*tree             // FileID-1 -> syntax index, built on first use
	pkgs      map[string]*Package // directory -> package, for files under node_modules
	installed map[string]bool     // dir + package -> installed at or above dir

	symbols   []*ast.Symbol
	symbolIDs map[*ast.Symbol]SymbolID
	types     []typeEntry
	typeIDs   map[typeEntry]TypeID
	typeOf    map[NodeID]TypeID
	symbolOf  map[NodeID]SymbolID
}

type typeEntry struct {
	t *checker.Type
	c *checker.Checker // types from different checkers can't be mixed
}

// testHookAfterParse lets tests inject a compiler panic.
var testHookAfterParse func()

// Load parses, binds and type-checks one project. It returns an error only
// when nothing usable could be loaded; problems that leave a usable program
// (type errors, syntax errors, missing packages, timeouts) are Warnings.
func Load(ctx context.Context, cfg ProjectConfig, opts LoadOptions) (p *Program, err error) {
	defer recoverPanic(&err, "load "+cfg.Name)
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	p = &Program{
		cfg:       cfg,
		fileOf:    map[*ast.SourceFile]FileID{},
		pkgs:      map[string]*Package{},
		installed: map[string]bool{},
		symbolIDs: map[*ast.Symbol]SymbolID{},
		typeIDs:   map[typeEntry]TypeID{},
		typeOf:    map[NodeID]TypeID{},
		symbolOf:  map[NodeID]SymbolID{},
	}
	p.ctx, p.cancel = context.WithCancel(context.WithoutCancel(ctx))

	start := time.Now()
	fs := newReadOnlyFS()
	configPath := cfg.ConfigPath
	if cfg.Synthesised {
		configPath = path.Join(cfg.Dir, synthesisedConfigName)
		fs.set(configPath, synthesisedConfig(cfg.Files))
	}
	host := compiler.NewCompilerHost(cfg.Dir, bundled.WrapFS(cachedvfs.From(fs)), bundled.LibPath(), nil, nil)
	if opts.cache != nil {
		host = &cachingHost{CompilerHost: host, cache: opts.cache}
	}
	parsed, warnings := parseConfig(host, configPath, cfg.Root)
	p.warnings = append(p.warnings, warnings...)
	if parsed == nil {
		return nil, fmt.Errorf("tsbridge: %s: config cannot be parsed", cfg.Name)
	}
	p.configure(parsed, opts)

	// Parsing and module resolution can't be cancelled inside the compiler, so
	// they run on their own goroutine and a timeout abandons them.
	type result struct {
		prog *compiler.Program
		err  error
	}
	done := make(chan result, 1)
	go func() {
		var r result
		defer func() {
			if rec := recover(); rec != nil {
				r.err = &PanicError{Op: "parse " + cfg.Name, Value: rec, Stack: debug.Stack()}
				slog.Debug("tsbridge: recovered compiler panic", "op", r.err.(*PanicError).Op, "panic", rec, "stack", string(r.err.(*PanicError).Stack))
			}
			done <- r
		}()
		r.prog = compiler.NewProgram(compiler.ProgramOptions{Config: parsed, Host: host})
		if testHookAfterParse != nil {
			testHookAfterParse()
		}
	}()
	select {
	case r := <-done:
		if r.err != nil {
			return nil, r.err
		}
		p.prog = r.prog
	case <-ctx.Done():
		return nil, fmt.Errorf("tsbridge: %s: %w while parsing (TSB006)", cfg.Name, ctx.Err())
	}
	p.stats.Parse = time.Since(start)

	start = time.Now()
	p.prog.BindSourceFiles()
	p.stats.Bind = time.Since(start)

	p.indexFiles(cfg, opts)
	p.collectSyntaxDiagnostics()
	p.reportUnresolvedImports()

	if !opts.NoCheck {
		start = time.Now()
		p.check(ctx)
		p.stats.Check = time.Since(start)
	}
	return p, nil
}

// configure applies LoadOptions to the parsed config: root files, MaxFiles,
// tests, lib checking and checker count.
func (p *Program) configure(parsed *tsoptions.ParsedCommandLine, opts LoadOptions) {
	o := parsed.CompilerOptions()
	o.NoEmit = core.TSTrue
	if o.Types == nil {
		// TypeScript 6 stopped including every @types package by default;
		// code written for TypeScript 5 relies on it (@types/node above all).
		// Without "types" in the config, include them all, as tsc 5 did.
		o.Types = []string{"*"}
	}
	if opts.SkipLibCheck == nil || *opts.SkipLibCheck {
		o.SkipLibCheck = core.TSTrue
	}
	if opts.Checkers > 0 {
		n := opts.Checkers
		o.Checkers = &n
	}
	p.stats.Checkers = 4
	if o.Checkers != nil {
		p.stats.Checkers = *o.Checkers
	}

	roots := parsed.FileNames()
	if !p.cfg.Synthesised && len(p.cfg.Files) > 0 {
		// Analyse only the files this project owns, plus every declaration
		// file the config includes: those carry global types the owned files
		// may depend on.
		owned := make(map[string]bool, len(p.cfg.Files))
		for _, f := range p.cfg.Files {
			owned[f] = true
		}
		roots = slices.DeleteFunc(slices.Clone(roots), func(f string) bool { return !owned[f] && !isDeclarationFile(f) })
	}
	if opts.IncludeTests != nil && !*opts.IncludeTests {
		roots = slices.DeleteFunc(roots, func(f string) bool { return isTestFile(f) && !isDeclarationFile(f) })
	}
	if opts.MaxFiles > 0 {
		var nonDecl int
		for _, f := range roots {
			if !isDeclarationFile(f) {
				nonDecl++
			}
		}
		if nonDecl > opts.MaxFiles {
			roots = prioritise(roots, opts.Priority, opts.MaxFiles)
			p.warnings = append(p.warnings, Warning{
				Code: WarnMaxFiles, File: relPath(p.cfg.Root, p.cfg.ConfigPath),
				Message: fmt.Sprintf("%d files, analysing the first %d by priority (changed files, then non-test source); %d skipped",
					nonDecl, opts.MaxFiles, nonDecl-opts.MaxFiles),
			})
		}
	}
	po := *parsed.ParsedConfig
	po.FileNames = roots
	parsed.SetParsedOptions(&po)
}

// prioritise keeps max non-declaration files: priority files first, then
// non-test source, then tests, each in path order. Declaration files are kept.
func prioritise(roots, priority []string, maxFiles int) []string {
	prio := make(map[string]bool, len(priority))
	for _, f := range priority {
		prio[toSlash(f)] = true
	}
	rank := func(f string) int {
		switch {
		case prio[f]:
			return 0
		case !isTestFile(f):
			return 1
		default:
			return 2
		}
	}
	var decls, rest []string
	for _, f := range roots {
		if isDeclarationFile(f) {
			decls = append(decls, f)
		} else {
			rest = append(rest, f)
		}
	}
	sort.SliceStable(rest, func(i, j int) bool {
		if ri, rj := rank(rest[i]), rank(rest[j]); ri != rj {
			return ri < rj
		}
		return rest[i] < rest[j]
	})
	return append(decls, rest[:maxFiles]...)
}

// isTestFile is a path heuristic: *.test.*, *.spec.*, and files under
// __tests__, test or tests directories.
func isTestFile(f string) bool {
	base := path.Base(f)
	if strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") {
		return true
	}
	for _, dir := range []string{"/__tests__/", "/test/", "/tests/", "/__mocks__/"} {
		if strings.Contains(f, dir) {
			return true
		}
	}
	return false
}

func (p *Program) indexFiles(cfg ProjectConfig, opts LoadOptions) {
	// Root files: the files this project owns, which for an entry-point
	// config include files reached through imports; for a synthesised
	// project, its listed files.
	roots := make(map[string]bool)
	for _, f := range p.prog.CommandLine().FileNames() {
		roots[f] = true
	}
	for _, f := range cfg.Files {
		roots[f] = true
	}
	if opts.IncludeTests != nil && !*opts.IncludeTests {
		for f := range roots {
			if isTestFile(f) {
				delete(roots, f)
			}
		}
	}
	srcs := p.prog.GetSourceFiles()
	p.sources = srcs
	p.files = make([]*File, len(srcs))
	p.trees = make([]*tree, len(srcs))
	for i, sf := range srcs {
		id := FileID(i + 1)
		name := sf.FileName()
		f := &File{
			ID:          id,
			Path:        name,
			Text:        sf.Text(),
			Root:        roots[name] && !sf.IsDeclarationFile,
			Declaration: sf.IsDeclarationFile,
			JS:          ast.IsSourceFileJS(sf),
			Lib:         strings.HasPrefix(name, "bundled:///"),
		}
		if !f.Lib {
			f.Package = p.packageOf(name)
		}
		p.files[i] = f
		p.fileOf[sf] = id
		if f.Root {
			p.stats.RootFiles++
			p.stats.Lines += f.LineCount()
		}
	}
	p.stats.Files = len(srcs)
}

// packageOf returns the npm package a node_modules file belongs to.
func (p *Program) packageOf(file string) *Package {
	i := strings.LastIndex(file, "/node_modules/")
	if i < 0 {
		return nil
	}
	for dir := path.Dir(file); len(dir) > i+len("/node_modules"); dir = path.Dir(dir) {
		if pkg, ok := p.pkgs[dir]; ok {
			return pkg
		}
		if pj, ok := readPackageJSON(path.Join(dir, "package.json")); ok && pj.Name != "" {
			pkg := &Package{Name: pj.Name, Version: pj.Version, Dir: dir}
			p.pkgs[dir] = pkg
			return pkg
		}
	}
	return nil
}

func (p *Program) collectSyntaxDiagnostics() {
	for _, d := range p.prog.GetConfigFileParsingDiagnostics() {
		p.addDiag(d)
	}
	for i, sf := range p.sources {
		if !p.files[i].Root {
			continue
		}
		diags := p.prog.GetSyntacticDiagnostics(p.ctx, sf)
		for _, d := range diags {
			p.addDiag(d)
		}
		if n := countErrors(diags); n > 0 {
			first := diags[0]
			p.warnings = append(p.warnings, Warning{
				Code: WarnSyntaxErrors, File: relPath(p.cfg.Root, sf.FileName()),
				Range:   &Range{Pos: Pos(first.Pos()), End: Pos(first.End())},
				Message: fmt.Sprintf("%d syntax error(s), first: %s; analysing the partial tree", n, first.String()),
			})
		}
	}
}

func countErrors(diags []*ast.Diagnostic) int {
	n := 0
	for _, d := range diags {
		if d.Category() == 1 { // diagnostics.CategoryError
			n++
		}
	}
	return n
}

// reportUnresolvedImports records TSB001 for packages root files import that
// are not installed, and TSB011 for installed packages that ship no types.
// Imports inside node_modules don't count, nor do ambient modules (Node
// built-ins declared by @types/node).
func (p *Program) reportUnresolvedImports() {
	resolved := p.prog.GetResolvedModules()
	c := p.anyChecker()
	var missing, untyped []string
	seen := map[string]bool{}
	for i, sf := range p.sources {
		if !p.files[i].Root {
			continue
		}
		for key, res := range resolved[sf.Path()] {
			name := key.Name
			if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "/") || seen[name] {
				continue
			}
			if res.IsResolved() && hasExtension(res.ResolvedFileName, typedExtensions) {
				continue
			}
			if c != nil && c.TryFindAmbientModule(name) != nil {
				continue
			}
			seen[name] = true
			pkg := packageName(name)
			dir := path.Dir(sf.FileName())
			if isNodeBuiltin(name) {
				pkg = "@types/node"
				if p.packageInstalled(dir, pkg) {
					pkg = name + " (not visible: check the config's \"types\")"
				}
			}
			if seen["pkg:"+pkg] {
				continue
			}
			seen["pkg:"+pkg] = true
			if !isNodeBuiltin(name) && (res.IsResolved() || p.packageInstalled(dir, pkg)) {
				untyped = append(untyped, pkg)
			} else {
				missing = append(missing, pkg)
			}
		}
	}
	if len(missing) > 0 {
		p.warnings = append(p.warnings, Warning{
			Code: WarnPackageNotInstalled, File: relPath(p.cfg.Root, p.cfg.ConfigPath),
			Message: fmt.Sprintf("%d package(s) not installed: %s; their imports are unknown", len(missing), listSome(missing)),
		})
	}
	if len(untyped) > 0 {
		p.warnings = append(p.warnings, Warning{
			Code: WarnUntypedPackages, File: relPath(p.cfg.Root, p.cfg.ConfigPath),
			Message: fmt.Sprintf("%d installed package(s) without types: %s; their imports are any", len(untyped), listSome(untyped)),
		})
	}
}

// typedExtensions are module files that carry types.
var typedExtensions = []string{".ts", ".tsx", ".mts", ".cts", ".json"}

func listSome(names []string) string {
	slices.Sort(names)
	list := strings.Join(names[:min(len(names), 10)], ", ")
	if len(names) > 10 {
		list += fmt.Sprintf(" and %d more", len(names)-10)
	}
	return list
}

// packageName returns the package part of a bare module specifier.
func packageName(spec string) string {
	parts := strings.SplitN(spec, "/", 3)
	if strings.HasPrefix(spec, "@") && len(parts) >= 2 {
		return parts[0] + "/" + parts[1]
	}
	return parts[0]
}

// check type-checks the root files in parallel, one goroutine per checker.
func (p *Program) check(ctx context.Context) {
	var roots []*ast.SourceFile
	for i, sf := range p.sources {
		if p.files[i].Root {
			roots = append(roots, sf)
		}
	}
	results := make([][]*ast.Diagnostic, len(roots))
	var wg sync.WaitGroup
	var panicked sync.Once
	work := make(chan int)
	for range max(p.stats.Checkers, 1) {
		wg.Go(func() {
			for i := range work {
				func() {
					defer func() {
						if r := recover(); r != nil {
							panicked.Do(func() {
								p.warnings = append(p.warnings, Warning{
									Code: WarnCompilerPanic, File: relPath(p.cfg.Root, roots[i].FileName()),
									Message: fmt.Sprintf("compiler panic while checking: %v; diagnostics are incomplete", r),
								})
							})
						}
					}()
					results[i] = p.prog.GetSemanticDiagnostics(ctx, roots[i])
				}()
			}
		})
	}
	checked := 0
feed:
	for i := range roots {
		select {
		case work <- i:
			checked++
		case <-ctx.Done():
			break feed
		}
	}
	close(work)
	wg.Wait()
	if ctx.Err() != nil {
		p.warnings = append(p.warnings, Warning{
			Code: WarnTimeout, File: relPath(p.cfg.Root, p.cfg.ConfigPath),
			Message: fmt.Sprintf("type-checking stopped after %d of %d files (%v); diagnostics are partial, types are still answered on demand",
				checked, len(roots), ctx.Err()),
		})
	}
	for _, diags := range results {
		for _, d := range diags {
			p.addDiag(d)
		}
	}
	for _, d := range p.prog.GetGlobalDiagnostics(ctx) {
		p.addDiag(d)
	}
}

func (p *Program) addDiag(d *ast.Diagnostic) {
	out := Diagnostic{
		Code:     int(d.Code()),
		Category: diagCategory(int(d.Category())),
		Message:  d.String(),
		Range:    Range{Pos: Pos(d.Pos()), End: Pos(d.End())},
	}
	if sf := d.File(); sf != nil {
		out.Path = sf.FileName()
		if id, ok := p.fileOf[sf]; ok {
			out.File = id
			out.Line, out.Column = p.files[id-1].Position(out.Range.Pos)
		}
	}
	p.diags = append(p.diags, out)
}

func diagCategory(c int) DiagnosticCategory {
	switch c {
	case 0:
		return DiagnosticWarning
	case 1:
		return DiagnosticError
	case 2:
		return DiagnosticSuggestion
	default:
		return DiagnosticMessage
	}
}

// Config returns the project this program was loaded from.
func (p *Program) Config() ProjectConfig { return p.cfg }

// Files returns every file in the program, root files and dependencies.
func (p *Program) Files() []FileID {
	p.mu.Lock()
	defer p.mu.Unlock()
	ids := make([]FileID, len(p.files))
	for i := range p.files {
		ids[i] = FileID(i + 1)
	}
	return ids
}

// RootFiles returns the files the project analyses.
func (p *Program) RootFiles() []FileID {
	p.mu.Lock()
	defer p.mu.Unlock()
	var ids []FileID
	for i, f := range p.files {
		if f.Root {
			ids = append(ids, FileID(i+1))
		}
	}
	return ids
}

// File returns the file with the given ID, or nil.
func (p *Program) File(id FileID) *File {
	p.mu.Lock()
	defer p.mu.Unlock()
	if id == 0 || int(id) > len(p.files) {
		return nil
	}
	return p.files[id-1]
}

// FileByPath returns the file with the given absolute path, or nil.
func (p *Program) FileByPath(name string) *File {
	name = toSlash(name)
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, f := range p.files {
		if f.Path == name || strings.EqualFold(f.Path, name) {
			return f
		}
	}
	return nil
}

// Diagnostics returns config, syntax and type diagnostics for the root files.
func (p *Program) Diagnostics() []Diagnostic {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.diags)
}

// Warnings returns what the program skipped or fell back on.
func (p *Program) Warnings() []Warning {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.warnings)
}

// Stats returns counters and timings.
func (p *Program) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stats
}

// Close frees the checker and syntax memory. Later calls return zero values.
func (p *Program) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
	p.cancel()
	p.prog = nil
	p.sources = nil
	p.trees = nil
	p.fileOf = nil
	p.symbols, p.symbolIDs = nil, nil
	p.types, p.typeIDs = nil, nil
	p.typeOf, p.symbolOf = nil, nil
	for _, f := range p.files {
		f.Text = ""
	}
}

// Closed reports whether Close was called.
func (p *Program) Closed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}
