package tsbridge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

func discoverBroken(t *testing.T, name string) *Workspace {
	t.Helper()
	ws, err := Discover(context.Background(), filepath.Join("testdata", "broken", name), DiscoverOptions{NoRepoConfig: true})
	if err != nil {
		t.Fatal(err)
	}
	return ws
}

func loadBroken(t *testing.T, name string, opts LoadOptions) (*Workspace, *Program) {
	t.Helper()
	ws := discoverBroken(t, name)
	p, err := Load(context.Background(), ws.Projects[0], opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return ws, p
}

func hasWarning(ws []Warning, code WarningCode, substr string) bool {
	for _, w := range ws {
		if w.Code == code && strings.Contains(w.Message, substr) {
			return true
		}
	}
	return false
}

func TestMissingNodeModules(t *testing.T) { // TSB001
	ws, p := loadBroken(t, "no-node-modules", LoadOptions{})
	if !hasWarning(ws.Warnings, WarnPackageNotInstalled, "node_modules is missing") {
		t.Errorf("discover warnings = %v", ws.Warnings)
	}
	if !hasWarning(p.Warnings(), WarnPackageNotInstalled, "express") {
		t.Errorf("load warnings = %v", p.Warnings())
	}
	f := fileNamed(t, p, "app.ts")
	if got := p.TypeString(p.TypeOf(nodeAtText(t, p, "app.ts", "app =", 0))); got != "any" {
		t.Errorf("type of app = %q, want any", got)
	}
	if c, ok := p.ResolveCall(callAt(t, p, "app.ts", "express()", 0)); ok || c.Reason != ReasonPackageNotInstalled {
		t.Errorf("express() = %+v %v, want package-not-installed", c, ok)
	}
	// Local code still resolves.
	if c, ok := p.ResolveCall(callAt(t, p, "app.ts", "local(2)", 0)); !ok || c.QualifiedName != "local:src/app.ts#local" {
		t.Errorf("local(2) = %+v %v", c, ok)
	}
	_ = f
}

func TestInvalidConfigFallsBack(t *testing.T) { // TSB002
	ws, p := loadBroken(t, "invalid-json", LoadOptions{})
	if !hasWarning(ws.Warnings, WarnInvalidConfig, "invalid JSON") {
		t.Errorf("warnings = %v", ws.Warnings)
	}
	if !p.Config().Synthesised || len(p.RootFiles()) != 1 {
		t.Errorf("project %s with %d root files, want a synthesised project", p.Config().Name, len(p.RootFiles()))
	}
}

func TestMissingExtends(t *testing.T) { // TSB003
	_, p := loadBroken(t, "missing-extends", LoadOptions{})
	if !hasWarning(p.Warnings(), WarnExtendsNotFound, "does-not-exist") {
		t.Errorf("warnings = %v", p.Warnings())
	}
	if got := p.TypeString(p.TypeOf(nodeAtText(t, p, "index.ts", "value", 0))); got != "number" {
		t.Errorf("type = %q", got)
	}
}

func TestSyntaxErrorsKeepPartialTree(t *testing.T) { // TSB004
	_, p := loadBroken(t, "syntax-error", LoadOptions{})
	if !hasWarning(p.Warnings(), WarnSyntaxErrors, "syntax error") {
		t.Errorf("warnings = %v", p.Warnings())
	}
	// The function after the error still parsed, with types.
	n := nodeAtText(t, p, "broken.ts", "toUpperCase", 0)
	if p.Kind(n) != KindIdentifier {
		t.Fatalf("NodeAt(toUpperCase) = %v", p.Kind(n))
	}
	if got := p.TypeString(p.TypeOf(nodeAtText(t, p, "broken.ts", "b.toUpperCase", 0))); got != "string" {
		t.Errorf("type of b after the error = %q, want string", got)
	}
	// o[]() has no key at all: unresolved, not a compiler panic.
	if c, ok := p.ResolveCall(callAt(t, p, "broken.ts", "o[]()", 0)); ok || c.Reason != ReasonDynamicProperty {
		t.Errorf("o[]() = %+v %v, want dynamic-property", c, ok)
	}
	if hasWarning(p.Warnings(), WarnCompilerPanic, "") {
		t.Errorf("unexpected TSB007: %v", p.Warnings())
	}
}

func TestMaxFiles(t *testing.T) { // TSB005
	ws := discoverBroken(t, "many-files")
	priority := filepath.ToSlash(filepath.Join(ws.Root, "src", "f4.ts"))
	p, err := Load(context.Background(), ws.Projects[0], LoadOptions{MaxFiles: 2, Priority: []string{priority}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if !hasWarning(p.Warnings(), WarnMaxFiles, "4 skipped") {
		t.Errorf("warnings = %v", p.Warnings())
	}
	var roots []string
	for _, id := range p.RootFiles() {
		roots = append(roots, filepath.Base(p.File(id).Path))
	}
	// The priority file, then non-test source in path order; the test file is cut.
	slices.Sort(roots)
	if strings.Join(roots, ",") != "f0.ts,f4.ts" {
		t.Errorf("analysed %v, want [f0.ts f4.ts]", roots)
	}
	// f0.ts imports f1-f3: they are in the program, but not analysed.
	if st := p.Stats(); st.RootFiles != 2 || st.Files < 5 {
		t.Errorf("stats %+v: want 2 root files among all program files", st)
	}
}

func TestIncludeTestsOff(t *testing.T) {
	ws := discoverBroken(t, "many-files")
	off := false
	p, err := Load(context.Background(), ws.Projects[0], LoadOptions{IncludeTests: &off})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for _, id := range p.RootFiles() {
		if isTestFile(p.File(id).Path) {
			t.Errorf("test file %s analysed with IncludeTests off", p.File(id).Path)
		}
	}
	if len(p.RootFiles()) != 5 {
		t.Errorf("%d root files, want 5", len(p.RootFiles()))
	}
}

func TestTimeout(t *testing.T) { // TSB006
	// Its own fixture: the parse it abandons keeps running and must not
	// meet other tests' hooks.
	ws := discoverBroken(t, "missing-extends")
	p, err := Load(context.Background(), ws.Projects[0], LoadOptions{Timeout: time.Nanosecond})
	switch {
	case err != nil:
		if !strings.Contains(err.Error(), "TSB006") {
			t.Errorf("err = %v, want a TSB006 timeout", err)
		}
	default:
		defer p.Close()
		if !hasWarning(p.Warnings(), WarnTimeout, "stopped") {
			t.Errorf("warnings = %v, want TSB006", p.Warnings())
		}
	}
}

func TestCompilerPanicIsRecovered(t *testing.T) { // TSB007
	ws := discoverBroken(t, "many-files")
	// Hooks match the project by directory: a timed-out load from another
	// test may still be parsing the same fixture on an abandoned goroutine.
	target := ws.Projects[0].Dir
	boom := func(cfg ProjectConfig) {
		if cfg.Dir == target {
			panic("boom")
		}
	}
	testHookAfterParse.Store(&boom)
	defer testHookAfterParse.Store(nil)
	_, err := Load(context.Background(), ws.Projects[0], LoadOptions{})
	var pe *PanicError
	if !errors.As(err, &pe) || !strings.Contains(pe.Error(), "boom") || len(pe.Stack) == 0 {
		t.Fatalf("err = %v, want a *PanicError with a stack", err)
	}

	// LoadAll keeps going: one project panics, the others load.
	two := discoverBroken(t, "no-node-modules")
	two.Projects = append(two.Projects, ws.Projects...)
	var once sync.Once
	onlyOnce := func(cfg ProjectConfig) {
		if cfg.Dir == target {
			once.Do(func() { panic("only once") })
		}
	}
	testHookAfterParse.Store(&onlyOnce)
	var mu sync.Mutex
	var failed, loaded int
	_, err = LoadAll(context.Background(), two, LoadAllOptions{Jobs: 1}, func(_ ProjectConfig, _ *Program, err error) error {
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			failed++
		} else {
			loaded++
		}
		return nil
	})
	if err != nil || failed != 1 || loaded != 1 {
		t.Errorf("LoadAll: err %v, %d failed, %d loaded; want 1 and 1", err, failed, loaded)
	}
}

func TestQueryPanicIsRecovered(t *testing.T) {
	p := loadFixture(t, "types", LoadOptions{})
	n := nodeAtText(t, p, "generics.ts", "n = identity", 0)
	p.mu.Lock()
	p.sources[n.File()-1] = nil // a corrupted program: the compiler will dereference nil
	p.trees[n.File()-1] = nil
	p.mu.Unlock()
	if got := p.Kind(n); got != KindInvalid {
		t.Errorf("Kind on a corrupted file = %v", got)
	}
	if !hasWarning(p.Warnings(), WarnCompilerPanic, "compiler panic") {
		t.Errorf("warnings = %v, want TSB007", p.Warnings())
	}
}

func TestFrameworkFilesSkipped(t *testing.T) { // TSB008
	ws := discoverBroken(t, "framework-files")
	for _, ext := range []string{".vue", ".svelte", ".astro"} {
		if !hasWarning(ws.Warnings, WarnUnsupportedFile, ext) {
			t.Errorf("no TSB008 for %s: %v", ext, ws.Warnings)
		}
	}
	if len(ws.Projects) != 1 || len(ws.Projects[0].Files) != 1 {
		t.Errorf("projects = %+v", ws.Projects)
	}
}

func TestRemovedOptionsTranslated(t *testing.T) { // TSB009
	_, p := loadBroken(t, "removed-options", LoadOptions{})
	if !hasWarning(p.Warnings(), WarnRemovedOption, "baseUrl") || !hasWarning(p.Warnings(), WarnRemovedOption, "moduleResolution") {
		t.Errorf("warnings = %v", p.Warnings())
	}
	// baseUrl-relative and paths imports resolve as they did in TypeScript 5.
	for _, call := range []string{"shout(helper())", "helper()"} {
		if c, ok := p.ResolveCall(callAt(t, p, "main.ts", call, 0)); !ok {
			t.Errorf("%s did not resolve: %+v", call, c)
		}
	}
	for _, d := range p.Diagnostics() {
		if d.Category == DiagnosticError && strings.Contains(d.Message, "Cannot find module") {
			t.Errorf("unresolved import: %s", d.Message)
		}
	}
}

func TestWarningString(t *testing.T) {
	w := Warning{Code: WarnTimeout, File: "a.ts", Message: "slow"}
	if w.String() != "TSB006 a.ts: slow" || (Warning{Code: WarnTimeout, Message: "slow"}).String() != "TSB006 slow" {
		t.Errorf("Warning.String = %q", w.String())
	}
}

// FuzzLoad feeds random tsconfig JSON and file contents through Discover and
// Load. Errors are fine; a panic escaping tsbridge is not. Run it for at
// least an hour before a release:
//
//	go test ./engine/tsbridge -run '^$' -fuzz FuzzLoad -fuzztime 1h
func FuzzLoad(f *testing.F) {
	f.Add(`{"compilerOptions":{"strict":true}}`, "export const x: number = 1;")
	f.Add(`{"extends":"./missing.json","include":["**/*"]}`, "function (")
	f.Add(`{"compilerOptions":{"baseUrl":".","paths":{"*":["*"]}}}`, "import x from 'y'; x();")
	f.Add(`{`, "<div>{</div>")
	f.Add(`{"files":["index.ts"],"references":[{"path":"."}]}`, "class A extends A {}")
	f.Add(`{"compilerOptions":{"jsx":"preserve","allowJs":true,"checkJs":true}}`, "/** @type {Foo<} */ var a;")
	f.Fuzz(func(t *testing.T, config, source string) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "tsconfig.json"), []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "index.ts"), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "other.js"), []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		ws, err := Discover(ctx, dir, DiscoverOptions{NoRepoConfig: true})
		if err != nil {
			return
		}
		for _, cfg := range ws.Projects {
			p, err := Load(ctx, cfg, LoadOptions{})
			if err != nil {
				continue
			}
			for _, id := range p.RootFiles() {
				p.Walk(id, func(n NodeID) bool {
					p.TypeOf(n)
					p.SymbolOf(n)
					if p.Kind(n).IsCallLike() {
						p.ResolveCall(n)
						p.Signature(n)
					}
					return true
				})
			}
			p.Close()
		}
	})
}
