package ir

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/patchd/patchd/engine/tsbridge"
)

// fn builds a function by hand for tests that need IR the builder would
// never produce.
type fn struct {
	f *Func
}

func newTestFunc() *fn {
	f := &Func{ID: 1, Name: "t", Blocks: []Block{{}}, Instrs: []Instr{{}}, Values: []Value{{}}, Entry: 1}
	return &fn{f: f}
}

func (t *fn) block(comment string) BlockID {
	id := BlockID(len(t.f.Blocks)) //nolint:gosec // test sizes
	t.f.Blocks = append(t.f.Blocks, Block{ID: id, Comment: comment})
	return id
}

func (t *fn) value(kind ValueKind) ValueID {
	id := ValueID(len(t.f.Values)) //nolint:gosec // test sizes
	t.f.Values = append(t.f.Values, Value{ID: id, Kind: kind})
	return id
}

func (t *fn) instr(b BlockID, op Op, aux Aux, args ...ValueID) ValueID {
	id := InstrID(len(t.f.Instrs))                                                                            //nolint:gosec // test sizes
	in := Instr{ID: id, Op: op, Block: b, Aux: aux, argOff: uint32(len(t.f.args)), argLen: uint32(len(args))} //nolint:gosec // test sizes
	t.f.args = append(t.f.args, args...)
	if op.HasResult() {
		in.Result = t.value(ValueInstr)
		t.f.Values[in.Result].Def = id
	}
	t.f.Instrs = append(t.f.Instrs, in)
	t.f.Blocks[b].Instrs = append(t.f.Blocks[b].Instrs, id)
	return in.Result
}

func (t *fn) edge(from, to BlockID) {
	t.f.Blocks[from].Succs = append(t.f.Blocks[from].Succs, to)
	t.f.Blocks[to].Preds = append(t.f.Blocks[to].Preds, from)
}

func TestOps(t *testing.T) {
	m := &Module{}
	for _, op := range Ops() {
		info := opTable[op]
		if info.name == "" {
			t.Errorf("op %d has no entry in the properties table", op)
			continue
		}
		if op.IsTerminator() && op.HasResult() {
			t.Errorf("%s: a terminator with a result", op)
		}
		// Every op has a printer case.
		tf := newTestFunc()
		b := tf.block("entry")
		var args []ValueID
		switch op {
		case OpPhi, OpJump, OpUnreachable:
		case OpBranch:
			args = []ValueID{tf.value(ValueUndefined)}
		default:
			args = []ValueID{tf.value(ValueUndefined)}
		}
		tf.instr(b, op, Aux{Name: Intern("x")}, args...)
		var sb strings.Builder
		m.Funcs = []*Func{tf.f}
		if err := PrintFunc(&sb, m, tf.f, PrintOptions{}); err != nil {
			t.Fatal(err)
		}
		if out := sb.String(); strings.Contains(out, noPrinter) || !strings.Contains(out, op.String()) && op != OpAssign {
			t.Errorf("%s has no printer case:\n%s", op, out)
		}
	}
}

func TestValidateCatchesBrokenIR(t *testing.T) {
	cases := []struct {
		name  string
		build func(*fn)
		want  string
	}{
		{"no terminator", func(t *fn) {
			b := t.block("entry")
			t.instr(b, OpConst, Aux{Name: Intern("1"), Flags: LitNumber})
		}, "does not end in a terminator"},
		{"terminator in the middle", func(t *fn) {
			b := t.block("entry")
			u := t.value(ValueUndefined)
			t.instr(b, OpReturn, Aux{}, u)
			t.instr(b, OpReturn, Aux{}, u)
		}, "terminator return at position 0"},
		{"one-sided edge", func(t *fn) {
			a, b := t.block("entry"), t.block("next")
			t.instr(a, OpJump, Aux{})
			t.f.Blocks[a].Succs = []BlockID{b}
			t.instr(b, OpReturn, Aux{}, t.value(ValueUndefined))
		}, "edge b1→b2"},
		{"use before definition", func(t *fn) {
			a, b, c := t.block("entry"), t.block("then"), t.block("done")
			cond := t.value(ValueParam)
			t.f.Params = []ValueID{cond}
			t.instr(a, OpBranch, Aux{}, cond)
			t.edge(a, b)
			t.edge(a, c)
			v := t.instr(b, OpConst, Aux{Name: Intern("1"), Flags: LitNumber})
			t.instr(b, OpJump, Aux{})
			t.edge(b, c)
			t.instr(c, OpReturn, Aux{}, v)
		}, "does not dominate"},
		{"phi operand count", func(t *fn) {
			a, b := t.block("entry"), t.block("loop")
			t.instr(a, OpJump, Aux{})
			t.edge(a, b)
			p := t.instr(b, OpPhi, Aux{})
			t.instr(b, OpReturn, Aux{}, p)
		}, "0 operands for 1 predecessors"},
		{"phi after an instruction", func(t *fn) {
			a := t.block("entry")
			u := t.value(ValueUndefined)
			t.instr(a, OpConst, Aux{Flags: LitNull})
			t.instr(a, OpPhi, Aux{})
			t.instr(a, OpReturn, Aux{}, u)
		}, "after a non-phi"},
		{"handler without a throwing instruction", func(t *fn) {
			a, h := t.block("entry"), t.block("catch")
			u := t.value(ValueUndefined)
			t.instr(a, OpReturn, Aux{}, u)
			t.f.Blocks[a].Handler = h
			t.f.Blocks[h].Preds = []BlockID{a}
			t.instr(h, OpReturn, Aux{}, u)
		}, "nothing in it can throw"},
		{"exceptional phi operand is the throwing result", func(t *fn) {
			a, b, h := t.block("entry"), t.block("next"), t.block("catch")
			g := t.value(ValueGlobal)
			call := t.instr(a, OpCall, Aux{}, g)
			t.instr(a, OpJump, Aux{})
			t.edge(a, b)
			t.f.Blocks[a].Handler = h
			t.f.Blocks[h].Preds = append(t.f.Blocks[h].Preds, a)
			t.instr(b, OpReturn, Aux{}, call)
			p := t.instr(h, OpPhi, Aux{})
			t.f.Instrs[t.f.Values[p].Def].argOff, t.f.Instrs[t.f.Values[p].Def].argLen = uint32(len(t.f.args)), 1 //nolint:gosec // test sizes
			t.f.args = append(t.f.args, call)
			t.instr(h, OpReturn, Aux{}, p)
		}, "result of the instruction that throws"},
		{"foreign value", func(t *fn) {
			a := t.block("entry")
			t.instr(a, OpReturn, Aux{}, 99)
		}, "not a value of this function"},
		{"source outside the function", func(t *fn) {
			t.f.Source = tsbridge.Range{Pos: 10, End: 20}
			a := t.block("entry")
			id := InstrID(len(t.f.Instrs)) //nolint:gosec // test sizes
			t.instr(a, OpReturn, Aux{}, t.value(ValueUndefined))
			t.f.Instrs[id].Source = tsbridge.Range{Pos: 0, End: 5}
		}, "outside the function"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tf := newTestFunc()
			c.build(tf)
			err := Validate(tf.f)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Validate = %v, want an error containing %q", err, c.want)
			}
		})
	}
}

func TestDominators(t *testing.T) {
	// b1 → b2 → {b3, b4} → b5 → b2 (loop), b2 → b6; b7 is dead code that
	// jumps into b5.
	tf := newTestFunc()
	for _, c := range []string{"entry", "head", "then", "else", "join", "exit", "dead"} {
		tf.block(c)
	}
	for _, e := range [][2]BlockID{{1, 2}, {2, 3}, {2, 4}, {3, 5}, {4, 5}, {5, 2}, {2, 6}, {7, 5}} {
		tf.edge(e[0], e[1])
	}
	d := Dominators(tf.f)
	idoms := map[BlockID]BlockID{2: 1, 3: 2, 4: 2, 5: 2, 6: 2}
	for b, want := range idoms {
		if got := d.Idom(b); got != want {
			t.Errorf("idom(b%d) = b%d, want b%d", b, got, want)
		}
	}
	if !d.Dominates(1, 5) || d.Dominates(3, 5) || !d.Dominates(2, 6) {
		t.Error("Dominates is wrong")
	}
	if !d.Live(5) || d.Live(7) {
		t.Error("Live is wrong")
	}
	if d.Dominates(7, 5) {
		t.Error("dead code dominates live code")
	}
	fr := d.Frontier(3)
	if len(fr) != 1 || fr[0] != 5 {
		t.Errorf("frontier(b3) = %v, want [b5]", fr)
	}
	if fr := d.Frontier(5); len(fr) != 1 || fr[0] != 2 {
		t.Errorf("frontier(b5) = %v, want [b2]", fr)
	}
}

// loadSource writes files into a temporary project and loads it.
func loadSource(t *testing.T, files map[string]string) (*tsbridge.Program, map[string]tsbridge.FileID) {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		writeFile(t, dir, name, text)
	}
	progs := loadDir(t, dir)
	ids := map[string]tsbridge.FileID{}
	for _, p := range progs {
		for _, id := range SourceFiles(p) {
			f := p.File(id)
			ids[f.Path[strings.LastIndex(f.Path, "/")+1:]] = id
		}
	}
	if len(progs) != 1 {
		t.Fatalf("%d programs", len(progs))
	}
	return progs[0], ids
}

func TestCoverageCountsUnknownAndSkipsTypes(t *testing.T) {
	p, ids := loadSource(t, map[string]string{
		"a.js": "function f(o) { with (o) { return x; } }\n",
		"b.ts": "interface I { a: string }\ntype T = I;\nexport function g(x: I): T { return x; }\n",
	})
	ma, _ := BuildModule(p, ids["a.js"])
	if kc := ma.Coverage.Kinds["WithStatement"]; kc == nil || kc.Unknown != 1 {
		t.Errorf("with statement not counted unknown: %+v", ma.Coverage.Kinds["WithStatement"])
	}
	if ma.Coverage.Lowered == 0 || ma.Coverage.Unknown != 1 {
		t.Errorf("a.js coverage = %+v", ma.Coverage)
	}
	mb, _ := BuildModule(p, ids["b.ts"])
	if _, ok := mb.Coverage.Kinds["InterfaceDeclaration"]; ok {
		t.Error("an interface is counted")
	}
	if mb.Coverage.Percent() != 100 {
		t.Errorf("b.ts coverage %.1f%%: %+v", mb.Coverage.Percent(), mb.Coverage.Missing())
	}
}

func TestBuildNeverPanics(t *testing.T) {
	broken := map[string]string{
		"syntax.ts":      "function f( { if (x ] return; class { constructor( } }\nconst = ;\nfor (let of) {}\n",
		"garbage.ts":     "))) {{{ ??. => ``${ \n<div <span> </ \n@@ export default default\n",
		"half-class.ts":  "class A extends { m() { super. } get } \n",
		"jsx-broken.tsx": "const x = <div><span>{</div>;\nconst y = <>{...}</>;\n",
		"deep.ts":        "const v = " + strings.Repeat("(", 300) + "1" + strings.Repeat(")", 300) + ";\n" + "if (a) " + strings.Repeat("if (b) ", 300) + "c();\n",
		"labels.ts":      "a: a: for(;;) { break b; continue a; }\nbreak;\n",
		"empty.ts":       "",
	}
	p, ids := loadSource(t, broken)
	for name, id := range ids {
		m, _ := BuildModule(p, id)
		if m == nil {
			t.Errorf("%s: no module", name)
			continue
		}
		if err := ValidateModule(m); err != nil {
			t.Errorf("%s: invalid IR:\n%v", name, err)
		}
	}
}

func TestBuildProgram(t *testing.T) {
	p, ids := loadSource(t, map[string]string{
		"a.ts":   "export const a = 1;\n",
		"b.ts":   "import { a } from './a';\nexport function b() { return a + 1; }\n",
		"c.d.ts": "export declare const c: number;\n",
	})
	mods := BuildProgram(context.Background(), p, BuildOptions{Jobs: 2})
	if len(mods) != 2 {
		t.Fatalf("%d modules, want 2 (declaration files are not lowered)", len(mods))
	}
	for _, m := range mods {
		if m == nil || m.Init == nil || m.Init != m.Funcs[0] {
			t.Fatalf("bad module %+v", m)
		}
	}
	if _, ok := ids["b.ts"]; !ok {
		t.Fatal("no b.ts")
	}
}

func TestUsesAndGlobals(t *testing.T) {
	p, ids := loadSource(t, map[string]string{
		"u.ts": "export function f(a: number) { const b = a + a; console.log(b); return b; }\n",
	})
	m, _ := BuildModule(p, ids["u.ts"])
	f := m.Funcs[1]
	a := f.Params[0]
	uses := f.Uses()
	if len(uses[a]) != 2 {
		t.Errorf("a has %d uses, want 2 (a + a)", len(uses[a]))
	}
	if _, ok := f.Global("console"); !ok {
		t.Error("console is not a global of f")
	}
}

func writeFile(t *testing.T, dir, name, text string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil { //nolint:gosec // test files
		t.Fatal(err)
	}
}

// TestParallelMatchesSequential lowers a whole testdata area with many
// workers and checks the output equals lowering file by file: lowering is
// deterministic and files don't interfere (run under -race in CI).
func TestParallelMatchesSequential(t *testing.T) {
	for _, area := range []string{"expressions", "classes"} {
		for _, p := range loadDir(t, filepath.Join("testdata", area)) {
			par := BuildProgram(context.Background(), p, BuildOptions{Jobs: 8})
			for i, id := range SourceFiles(p) {
				seq, _ := BuildModule(p, id)
				if got, want := par[i].String(), seq.String(); got != want {
					t.Errorf("%s: parallel and sequential lowering differ:\n%s", seq.Path, lineDiff(want, got))
				}
			}
		}
	}
}
