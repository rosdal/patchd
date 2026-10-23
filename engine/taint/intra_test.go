package taint

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patchd/patchd/engine/ir"
	"github.com/patchd/patchd/engine/tsbridge"
)

// The unit tests use a tiny spec over global declarations, so they need no
// node_modules: source() and Req.query are sources, sink() and exec() sinks
// of different kinds, clean() clears sql, push propagates to its receiver.
const globalsDTS = `
interface Req { query: string; body: any; id: number }
declare function source(): string;
declare function sinkSQL(q: string, values?: unknown[]): void;
declare function sinkCmd(c: string): void;
declare function sinkPath(p: string): void;
declare function cleanSQL(s: string): string;
declare function envSource(): string;
`

func unitSpec() *Spec {
	s := &Spec{}
	call := func(qn string) Matcher { return Matcher{QualifiedName: qn, Access: AccessCall} }
	s.AddSource(
		Source{ID: "test.source", Match: call("global:source"), Kinds: []Kind{KindUserInput}},
		Source{ID: "test.query", Match: Matcher{QualifiedName: "global:Req.query", Access: AccessProperty}, Kinds: []Kind{KindUserInput}},
		Source{ID: "test.body", Match: Matcher{QualifiedName: "global:Req.body", Access: AccessProperty}, Kinds: []Kind{KindUserInput}},
		Source{ID: "test.id", Match: Matcher{QualifiedName: "global:Req.id", Access: AccessProperty}, Kinds: []Kind{KindUserInput}},
		Source{ID: "test.env", Match: call("global:envSource"), Kinds: []Kind{KindEnvironment}},
	)
	s.AddSink(
		Sink{ID: "test.sql", Match: call("global:sinkSQL"), Args: []int{0}, Kind: KindSQL, Rule: "t/sql"},
		Sink{ID: "test.cmd", Match: call("global:sinkCmd"), Args: []int{0}, Kind: KindCommand, Rule: "t/cmd"},
		Sink{ID: "test.path", Match: call("global:sinkPath"), Args: []int{0}, Kind: KindPath, Rule: "t/path"},
	)
	s.AddSanitizer(Sanitizer{ID: "test.clean", Match: call("global:cleanSQL"), Clears: []Kind{KindSQL}})
	s.AddPropagator(Propagator{ID: "test.push", Match: call("global:Array.push"), From: []Slot{SlotArgs}, To: []Slot{SlotReceiver}})
	if err := s.Validate(nil); err != nil {
		panic(err)
	}
	return s
}

// unitCases are function bodies (with a parameter req: Req) and the number
// of findings each must produce. One row of the propagation table each, at
// least.
var unitCases = []struct {
	name string
	body string
	want int
}{
	{"const", `sinkSQL("select 1");`, 0},
	{"source to sink", `sinkSQL(source());`, 1},
	{"assign and phi", `let q = "a"; if (req.id > 1) { q = source(); } sinkSQL(q);`, 1},
	{"concatenation", `sinkSQL("select " + req.query);`, 1},
	{"compound concatenation", `let q = "select "; q += req.query; sinkSQL(q);`, 1},
	{"comparison", `const b = req.query === "x"; sinkSQL(String(b) as string);`, 0},
	{"in instanceof", `const b = "x" in req.body; const c = req.body instanceof Error; sinkSQL("" + b + c);`, 0},
	{"arithmetic", `const n = (req.query as any) * 2; sinkSQL("x" + n);`, 0},
	{"unop", `const t = typeof req.query; const n = -req.query; const v = void req.query; sinkCmd(t + n + v);`, 0},
	{"template", "sinkSQL(`select ${req.query}`);", 1},
	{"load property", `const b = req.body; sinkCmd(b.cmd.name);`, 1},
	{"dynamic key", `const o: Record<string, string> = { a: "x" }; sinkCmd(o[req.query]);`, 1},
	{"store weak update", `const o: any = {}; o.x = req.query; sinkCmd(o.y);`, 1},
	{"object and array", `const o = { a: req.query }; const xs = ["a", ...[req.query]]; sinkCmd(o.a); sinkPath(xs.join("/"));`, 2},
	{"sanitizer", `sinkSQL(cleanSQL(req.query));`, 0},
	{"sanitizer is kind specific", `sinkCmd(cleanSQL(req.query));`, 1},
	{"propagator", `const parts: string[] = []; parts.push(req.query); sinkCmd(parts.join(" "));`, 1},
	{"unknown call default", `sinkCmd(req.query.trim().toLowerCase().slice(1));`, 1},
	{"new default", `const u = new URL(req.query); sinkPath(u.pathname);`, 1},
	{"await", `async function inner() { const v = await Promise.resolve(req.query); sinkCmd(v); } inner();`, 1},
	{"closure", `const f = () => 1; sinkCmd(String(f));`, 0},
	{"catchval", `try { JSON.parse(req.query); } catch (e) { sinkCmd(String(e)); }`, 0},
	{"numeric type pruning", `sinkSQL("select " + req.id);`, 0},
	{"numeric conversion", `const n = parseInt(req.query); sinkSQL("select " + n);`, 0},
	{"number conversion", `sinkSQL("select " + Number(req.query));`, 0},
	{"numeric keeps html kinds", `sinkSQL("a" + Boolean(req.query));`, 0},
	{"loops", `let q = ""; for (const p of [1, 2]) { q = q + req.query; } sinkSQL(q);`, 1},
	{"try catch flow", `let q = "x"; try { q = req.query; JSON.parse(q); } catch { sinkSQL(q); }`, 1},
	{"captured cell", `let q = req.query; const g = () => q; sinkSQL(q);`, 1},
	{"captured cell overwritten", `let q = req.query; const g = () => q; q = "safe"; sinkSQL(q);`, 0},
	{"destructuring", `const { query } = req; sinkSQL(query);`, 1},
	{"environment off", `sinkCmd(envSource());`, 0},
	{"never invents", `const a = "x"; sinkCmd(a.trim() + String(1));`, 0},
	{"several sources one sink", `sinkCmd(req.query + source());`, 2},
}

// loadUnit lowers the unit cases into one project and returns its module.
func loadUnit(t *testing.T) (*tsbridge.Program, *ir.Module) {
	t.Helper()
	dir := t.TempDir()
	var src strings.Builder
	src.WriteString("export {};\n")
	for i, c := range unitCases {
		fmt.Fprintf(&src, "export function case%d(req: Req) {\n  %s\n}\n", i, c.body)
	}
	for name, text := range map[string]string{
		"tsconfig.json": `{"compilerOptions":{"strict":true,"noEmit":true,"target":"es2024","module":"esnext","lib":["es2024","dom"]},"include":["*.ts"]}`,
		"globals.d.ts":  globalsDTS,
		"cases.ts":      src.String(),
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	ws, err := tsbridge.Discover(ctx, dir, tsbridge.DiscoverOptions{NoRepoConfig: true})
	if err != nil {
		t.Fatal(err)
	}
	p, err := tsbridge.Load(ctx, ws.Projects[0], tsbridge.LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	for _, id := range ir.SourceFiles(p) {
		if strings.HasSuffix(p.File(id).Path, "cases.ts") {
			m, _ := ir.BuildModule(p, id)
			return p, m
		}
	}
	t.Fatal("cases.ts not lowered")
	return nil, nil
}

func TestPropagation(t *testing.T) {
	p, m := loadUnit(t)
	a := NewAnalyzer(p, Config{Spec: unitSpec()})
	for i, c := range unitCases {
		t.Run(c.name, func(t *testing.T) {
			got := 0
			name := fmt.Sprintf("case%d", i)
			for _, f := range m.Funcs {
				if f.Name != name && !strings.HasPrefix(f.Name, name+".") {
					continue
				}
				r := a.Func(m, f)
				if r.Solver.Capped {
					t.Errorf("%s: solver capped", f.Name)
				}
				got += len(r.Candidates)
				for _, cand := range r.Candidates {
					path := r.Path(cand)
					if len(path) == 0 || path[0].Value != r.Origin(cand.Label).Value || path[len(path)-1].Value != cand.Value {
						t.Errorf("%s: bad path %v for %+v", f.Name, path, cand)
					}
				}
			}
			if got != c.want {
				t.Errorf("%d findings, want %d", got, c.want)
			}
		})
	}
}

func TestEnvironmentSources(t *testing.T) {
	p, m := loadUnit(t)
	a := NewAnalyzer(p, Config{Spec: unitSpec(), IncludeEnvironment: true})
	for i, c := range unitCases {
		if c.name != "environment off" {
			continue
		}
		for _, f := range m.Funcs {
			if f.Name == fmt.Sprintf("case%d", i) {
				r := a.Func(m, f)
				if len(r.Candidates) != 1 || r.Origin(r.Candidates[0].Label).Kind != KindEnvironment {
					t.Errorf("with IncludeEnvironment: %+v", r.Candidates)
				}
				return
			}
		}
	}
	t.Fatal("case not found")
}

func TestSkipsFunctionsWithoutSources(t *testing.T) {
	p, m := loadUnit(t)
	a := NewAnalyzer(p, Config{Spec: unitSpec()})
	skipped := 0
	for _, f := range m.Funcs {
		if a.Func(m, f).Skipped {
			skipped++
		}
	}
	if skipped == 0 {
		t.Error("no function skipped (the module body has no source)")
	}
}

func TestLattice(t *testing.T) {
	lat := Lattice{}
	a := Fact{{Origin: 0, Kinds: KindSetOf(KindSQL)}, {Origin: 2, Kinds: KindSetOf(KindPath), Numeric: true}}
	b := Fact{{Origin: 1, Kinds: KindSetOf(KindCommand)}, {Origin: 2, Kinds: KindSetOf(KindHTML)}}
	j := lat.Join(a, b)
	if len(j) != 3 || j[2].Kinds != KindSetOf(KindPath, KindHTML) || j[2].Numeric {
		t.Fatalf("Join = %v", j)
	}
	if !lat.Leq(a, j) || !lat.Leq(b, j) || lat.Leq(j, a) || !lat.Leq(nil, a) {
		t.Error("Leq")
	}
	if lat.Leq(Fact{{Origin: 2, Kinds: KindSetOf(KindPath)}}, a) {
		t.Error("a non-numeric label is not below a numeric one")
	}
}
