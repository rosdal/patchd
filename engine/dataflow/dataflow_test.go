package dataflow

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/patchd/patchd/engine/ir"
	"github.com/patchd/patchd/engine/tsbridge"
)

// lower writes src to a one-file project and returns its IR and the function
// with the given name ("<module>" for the module body).
func lower(t *testing.T, src, fn string) (*ir.Module, *ir.Func) {
	t.Helper()
	dir := t.TempDir()
	write := func(name, text string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("tsconfig.json", `{"compilerOptions":{"strict":true,"noEmit":true,"target":"es2024","module":"esnext"},"include":["*.ts"]}`)
	write("a.ts", src)
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
	m, _ := ir.BuildModule(p, ir.SourceFiles(p)[0])
	if err := ir.ValidateModule(m); err != nil {
		t.Fatal(err)
	}
	for _, f := range m.Funcs {
		if f.Name == fn {
			return m, f
		}
	}
	t.Fatalf("no function %s", fn)
	return nil, nil
}

// paramSet is a lattice of parameter-name sets: which parameters a value is
// computed from.
type paramSet []string

type paramLattice struct{}

func (paramLattice) Bottom() paramSet { return nil }
func (paramLattice) Join(a, b paramSet) paramSet {
	out := slices.Concat(a, b)
	slices.Sort(out)
	return slices.Compact(out)
}

func (paramLattice) Leq(a, b paramSet) bool {
	for _, x := range a {
		if !slices.Contains(b, x) {
			return false
		}
	}
	return true
}

// dependsOn runs a sparse "which parameters flow here" analysis: every
// instruction's result is the union of its arguments', and reaching stores
// connect cells and globals.
func dependsOn(f *ir.Func) (*Sparse[paramSet], Result) {
	du := NewDefUse(f)
	rs := Reaching(f, Options{})
	lat := paramLattice{}
	transfer := func(s *Sparse[paramSet], in *ir.Instr) {
		if in.Result == ir.NoValue {
			return
		}
		var fact paramSet
		for _, a := range f.Args(in) {
			fact = lat.Join(fact, s.Get(a))
		}
		for _, st := range rs.StoresFor(in.ID) {
			args := f.Args(f.Instr(st))
			fact = lat.Join(fact, s.Get(args[len(args)-1]))
		}
		s.Add(in.Result, fact)
	}
	extra := func(v ir.ValueID) []ir.InstrID {
		var out []ir.InstrID
		for _, u := range du.Uses(v) {
			if in := f.Instr(u); in.Op == ir.OpStore {
				out = append(out, rs.LoadsOf(u)...)
			}
		}
		return out
	}
	return SolveSparse(f, lat, du, transfer, SparseOptions[paramSet]{
		Init: func(v *ir.Value) (paramSet, bool) {
			if v.Kind == ir.ValueParam {
				return paramSet{v.Name}, true
			}
			return nil, false
		},
		Extra: extra,
	})
}

// returned is the fact of f's returned values, joined.
func returned(f *ir.Func, s *Sparse[paramSet]) paramSet {
	var out paramSet
	for _, b := range RPO(f) {
		if t := f.Terminator(&f.Blocks[b]); t != nil && t.Op == ir.OpReturn {
			for _, a := range f.Args(t) {
				out = paramLattice{}.Join(out, s.Get(a))
			}
		}
	}
	return out
}

func TestSparse(t *testing.T) {
	tests := []struct {
		name, src string
		want      string
	}{
		{"straight line", `export function f(a: string, b: string, c: string) { const x = a + "-"; const y = x.trim(); return y + b; }`, "a,b"},
		{"branches", `export function f(a: string, b: string, c: boolean) { let x = "k"; if (c) { x = a; } else { x = x + "!"; } return x; }`, "a"},
		{"loop", `export function f(a: string, n: number) { let s = ""; for (let i = 0; i < 3; i++) { s = s + a; } return s; }`, "a"},
		{"loop carried", `export function f(a: string, b: string) { let x = "", y = ""; while (x.length < 9) { x = y; y = a; } return x; }`, "a"},
		{"try catch", `declare function g(): void; export function f(a: string, b: string) { let x = "s"; try { x = a; g(); x = b; g(); } catch { return x; } return "none"; }`, "a,b"},
		{"captured cells", `export function f(a: string, b: string) { let x = a; const read = () => x; x = b; return x + read.name; }`, "b"},
		{"cell killed", `export function f(a: string, b: string) { let x = a; const read = () => x; x = "c"; return x; }`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, f := lower(t, tt.src, "f")
			s, res := dependsOn(f)
			if res.Capped {
				t.Fatalf("capped: %+v", res)
			}
			if got := strings.Join(returned(f, s), ","); got != tt.want {
				t.Errorf("return depends on %q, want %q", got, tt.want)
			}
		})
	}
}

func TestReachingStores(t *testing.T) {
	_, f := lower(t, `
declare function g(): void;
export function f(a: string, b: string) {
  let x = a;
  const read = () => x;
  if (b) { x = b; }
  return x;
}`, "f")
	rs := Reaching(f, Options{})
	var loads []ir.InstrID
	for i := 1; i < len(f.Instrs); i++ {
		if in := &f.Instrs[i]; in.Op == ir.OpLoad && in.Aux.Flags.Has(ir.AuxCell) {
			loads = append(loads, in.ID)
		}
	}
	if len(loads) != 1 {
		t.Fatalf("%d cell loads", len(loads))
	}
	// Both stores (x = a on entry, x = b in the branch) reach the return.
	if got := rs.StoresFor(loads[0]); len(got) != 2 {
		t.Errorf("stores reaching the load: %v, want 2", got)
	}
	for _, st := range rs.StoresFor(loads[0]) {
		if !slices.Contains(rs.LoadsOf(st), loads[0]) {
			t.Errorf("store %d: LoadsOf misses the load", st)
		}
	}
}

func TestReachingGlobals(t *testing.T) {
	_, f := lower(t, `
let config = "a";
config = "b";
export const out = config;
`, "<module>")
	rs := Reaching(f, Options{})
	for i := 1; i < len(f.Instrs); i++ {
		in := &f.Instrs[i]
		if in.Op == ir.OpLoad && in.Aux.Flags.Has(ir.AuxGlobal) && in.Aux.Name.String() == "config" {
			stores := rs.StoresFor(in.ID)
			if len(stores) != 1 {
				t.Fatalf("stores reaching config: %v, want only the second", stores)
			}
			args := f.Args(f.Instr(stores[0]))
			v := f.Value(args[len(args)-1])
			if c := f.Instr(v.Def); c == nil || c.Aux.Name.String() != "b" {
				t.Errorf("reaching store writes %v", c)
			}
			return
		}
	}
	t.Fatal("no load of config")
}

// counter is an infinite-height lattice: every transfer adds one.
type counter struct{}

func (counter) Bottom() int       { return 0 }
func (counter) Join(a, b int) int { return max(a, b) }
func (counter) Leq(a, b int) bool { return a <= b }

type widening struct{ counter }

func (widening) Widen(_, _ int) int { return 1 << 30 }

func TestForwardCapAndWidening(t *testing.T) {
	_, f := lower(t, `export function f(n: number) { let s = 0; while (s < n) { s++; } return s; }`, "f")
	inc := func(_ ir.BlockID, in int) int {
		if in >= 1<<30 {
			return in
		}
		return in + 1
	}
	_, _, res := Forward[int](f, counter{}, 0, inc, Options{MaxPasses: 5})
	if !res.Capped || res.Passes > 5 {
		t.Errorf("without widening: %+v, want capped within 5 passes", res)
	}
	_, out, res := Forward[int](f, widening{}, 0, inc, Options{})
	if res.Capped {
		t.Errorf("with widening: %+v, want a fixed point", res)
	}
	if out[f.Entry] != 1 {
		t.Errorf("entry out = %d", out[f.Entry])
	}
}

func TestForwardExceptionalEdges(t *testing.T) {
	_, f := lower(t, `declare function g(): void; export function f() { try { g(); } catch { return 1; } return 2; }`, "f")
	// Count the blocks on the longest path: the catch block must be reached
	// through the exceptional edge.
	in, _, res := Forward[int](f, counter{}, 1, func(_ ir.BlockID, in int) int { return in + 1 }, Options{})
	if res.Capped {
		t.Fatal(res)
	}
	for b := 1; b < len(f.Blocks); b++ {
		if f.Blocks[b].Comment == "catch" && in[b] == 0 {
			t.Errorf("catch block never reached")
		}
	}
}

func TestCache(t *testing.T) {
	_, f := lower(t, `export function f(a: string) { return a; }`, "f")
	c := NewCache()
	du, rs := c.DefUse(f), c.Reaching(f)
	if c.DefUse(f) != du || c.Reaching(f) != rs {
		t.Error("cache rebuilt the same function")
	}
	c.Forget(f)
	if c.DefUse(f) == nil {
		t.Error("nil after Forget")
	}
}
