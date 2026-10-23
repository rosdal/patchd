package ir

import (
	"errors"
	"fmt"
)

// Validate checks a function's invariants and returns every violation
// found (nil if none):
//
//   - every block ends in exactly one terminator, and only phis come before
//     the first non-phi instruction;
//   - terminators have the right successors and arguments;
//   - predecessors and successors agree in both directions (exceptional
//     edges included), and the entry block has no predecessors;
//   - a block has a handler only if it has an instruction that can throw;
//   - each phi has one operand per predecessor, and each operand is
//     available at the end of that predecessor (for an exceptional edge,
//     not the result of the throwing instruction itself);
//   - every value is defined before every use along every path: its
//     definition dominates the use;
//   - instructions only refer to the function's own values (free variables
//     arrive through FreeVars);
//   - every instruction's source range lies inside the function's.
func Validate(f *Func) error {
	v := &validator{f: f}
	v.run()
	return errors.Join(v.errs...)
}

// ValidateModule validates every function of a module, and that every
// closure passes as many values as its function has free variables.
func ValidateModule(m *Module) error {
	var errs []error
	for _, f := range m.Funcs {
		if err := Validate(f); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", f.Name, err))
		}
	}
	if err := ValidateClosures(m); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// ValidateClosures checks that every closure instruction of a module creates
// one of its functions and passes one value per free variable.
func ValidateClosures(m *Module) error {
	var errs []error
	for _, f := range m.Funcs {
		for i := 1; i < len(f.Instrs); i++ {
			in := &f.Instrs[i]
			if in.Op != OpClosure {
				continue
			}
			g := m.Func(in.Aux.Func)
			if g == nil {
				errs = append(errs, fmt.Errorf("%s: v%d: closure of unknown function f%d", f.Name, in.Result, in.Aux.Func))
				continue
			}
			if int(in.argLen) != len(g.FreeVars) {
				errs = append(errs, fmt.Errorf("%s: v%d: closure of %s passes %d values for %d free variables",
					f.Name, in.Result, g.Name, in.argLen, len(g.FreeVars)))
			}
		}
	}
	return errors.Join(errs...)
}

type validator struct {
	f    *Func
	errs []error
	dom  *DomTree
	pos  []int // instruction index within its block
}

func (v *validator) errorf(format string, args ...any) {
	if len(v.errs) < 50 {
		v.errs = append(v.errs, fmt.Errorf(format, args...))
	}
}

func (v *validator) run() {
	f := v.f
	if len(f.Blocks) < 2 {
		v.errorf("no blocks")
		return
	}
	if f.Entry != 1 {
		v.errorf("entry is b%d, not b1", f.Entry)
	}
	if len(f.Blocks[f.Entry].Preds) != 0 {
		v.errorf("entry b%d has predecessors %v", f.Entry, f.Blocks[f.Entry].Preds)
	}
	v.pos = make([]int, len(f.Instrs))
	seen := make([]bool, len(f.Instrs))
	for b := 1; b < len(f.Blocks); b++ {
		v.block(BlockID(b), seen) //nolint:gosec // block counts fit
	}
	for i := 1; i < len(f.Instrs); i++ {
		if !seen[i] {
			v.errorf("i%d (%s) is in no block", i, f.Instrs[i].Op)
		}
	}
	v.edges()
	v.values()
	if len(v.errs) > 0 {
		return // dominance needs a well-formed CFG
	}
	v.dom = Dominators(f)
	v.uses()
}

func (v *validator) block(b BlockID, seen []bool) {
	f := v.f
	blk := &f.Blocks[b]
	if blk.ID != b {
		v.errorf("b%d has ID b%d", b, blk.ID)
	}
	if len(blk.Instrs) == 0 {
		v.errorf("b%d is empty", b)
		return
	}
	phis := true
	throws := false
	for i, id := range blk.Instrs {
		in := f.Instr(id)
		if in == nil {
			v.errorf("b%d: bad instruction i%d", b, id)
			continue
		}
		if seen[id] {
			v.errorf("i%d is in two blocks", id)
		}
		seen[id] = true
		v.pos[id] = i
		if in.Block != b {
			v.errorf("i%d says block b%d, is in b%d", id, in.Block, b)
		}
		if in.ID != id {
			v.errorf("i%d has ID i%d", id, in.ID)
		}
		last := i == len(blk.Instrs)-1
		switch {
		case in.Op.IsTerminator() && !last:
			v.errorf("b%d: terminator %s at position %d of %d", b, in.Op, i, len(blk.Instrs))
		case !in.Op.IsTerminator() && last:
			v.errorf("b%d does not end in a terminator (ends in %s)", b, in.Op)
		}
		if in.Op == OpPhi {
			if !phis {
				v.errorf("b%d: phi v%d after a non-phi instruction", b, in.Result)
			}
		} else {
			phis = false
		}
		if in.Op.CanThrow() {
			throws = true
		}
		if in.Op.HasResult() != (in.Result != NoValue) {
			v.errorf("i%d (%s): result v%d", id, in.Op, in.Result)
		}
		if f.Source.End > f.Source.Pos && (in.Source.Pos < f.Source.Pos || in.Source.End > f.Source.End) {
			v.errorf("i%d (%s): source %d-%d outside the function's %d-%d", id, in.Op, in.Source.Pos, in.Source.End, f.Source.Pos, f.Source.End)
		}
	}
	if blk.Handler != NoBlock && !throws {
		v.errorf("b%d has handler b%d but nothing in it can throw", b, blk.Handler)
	}
	term := f.Terminator(blk)
	if term == nil {
		return
	}
	args := f.Args(term)
	want := map[Op][2]int{OpJump: {1, 0}, OpReturn: {0, 1}, OpThrow: {0, 1}, OpUnreachable: {0, 0}}
	switch term.Op {
	case OpBranch:
		if len(blk.Succs) != 2 || len(args) > 1 {
			v.errorf("b%d: branch with %d successors and %d arguments", b, len(blk.Succs), len(args))
		}
	default:
		w := want[term.Op]
		if len(blk.Succs) != w[0] || len(args) != w[1] {
			v.errorf("b%d: %s with %d successors and %d arguments", b, term.Op, len(blk.Succs), len(args))
		}
	}
}

// edges checks that predecessor and successor lists agree, counting
// duplicate edges (a branch with both arms to one block).
func (v *validator) edges() {
	f := v.f
	type edge struct{ from, to BlockID }
	out := map[edge]int{}
	in := map[edge]int{}
	for b := 1; b < len(f.Blocks); b++ {
		blk := &f.Blocks[b]
		for _, s := range blk.Succs {
			if f.Block(s) == nil {
				v.errorf("b%d: bad successor b%d", b, s)
				continue
			}
			out[edge{BlockID(b), s}]++ //nolint:gosec // block counts fit
		}
		if blk.Handler != NoBlock {
			if f.Block(blk.Handler) == nil {
				v.errorf("b%d: bad handler b%d", b, blk.Handler)
			} else {
				out[edge{BlockID(b), blk.Handler}]++ //nolint:gosec // block counts fit
			}
		}
		for _, p := range blk.Preds {
			if f.Block(p) == nil {
				v.errorf("b%d: bad predecessor b%d", b, p)
				continue
			}
			in[edge{p, BlockID(b)}]++ //nolint:gosec // block counts fit
		}
	}
	for e, n := range out {
		if in[e] != n {
			v.errorf("edge b%d→b%d: %d in the successors, %d in the predecessors", e.from, e.to, n, in[e])
		}
	}
	for e, n := range in {
		if out[e] != n {
			v.errorf("edge b%d→b%d: %d in the predecessors, %d in the successors", e.from, e.to, n, out[e])
		}
	}
}

// values checks value definitions and argument references.
func (v *validator) values() {
	f := v.f
	for i := 1; i < len(f.Values); i++ {
		val := &f.Values[i]
		if val.ID != ValueID(i) { //nolint:gosec // value counts fit
			v.errorf("v%d has ID v%d", i, val.ID)
		}
		if val.Kind == ValueInstr {
			in := f.Instr(val.Def)
			if in == nil || in.Result != val.ID {
				v.errorf("v%d: defining instruction i%d does not produce it", i, val.Def)
			}
		}
	}
	for i := 1; i < len(f.Instrs); i++ {
		in := &f.Instrs[i]
		for _, a := range f.Args(in) {
			if a == NoValue {
				if in.Op != OpArray {
					v.errorf("i%d (%s): missing argument", i, in.Op)
				}
				continue
			}
			if f.Value(a) == nil {
				v.errorf("i%d (%s): argument v%d is not a value of this function", i, in.Op, a)
			}
		}
	}
	for _, fv := range f.FreeVars {
		if val := f.Value(fv.Value); val == nil || val.Kind != ValueFreeVar {
			v.errorf("free variable %s: v%d is not a free variable value", fv.Name, fv.Value)
		}
	}
	for _, p := range f.Params {
		if val := f.Value(p); val == nil || val.Kind != ValueParam {
			v.errorf("parameter v%d is not a parameter value", p)
		}
	}
}

// uses checks that every use is dominated by its definition.
func (v *validator) uses() {
	f := v.f
	for i := 1; i < len(f.Instrs); i++ {
		in := &f.Instrs[i]
		args := f.Args(in)
		if in.Op == OpPhi {
			preds := f.Blocks[in.Block].Preds
			if len(args) != len(preds) {
				v.errorf("phi v%d in b%d: %d operands for %d predecessors", in.Result, in.Block, len(args), len(preds))
				continue
			}
			for j, a := range args {
				p := preds[j]
				if v.dom.Live(in.Block) && !v.dom.Live(p) {
					continue // an edge from dead code: the operand is never used
				}
				if !v.availableAtEnd(a, p) {
					v.errorf("phi v%d in b%d: operand v%d is not available at the end of b%d", in.Result, in.Block, a, p)
				}
				pb := &f.Blocks[p]
				if pb.Handler == in.Block && !containsBlock(pb.Succs, in.Block) {
					if t := v.throwing(p); t != nil && t.Result == a && a != NoValue {
						v.errorf("phi v%d in b%d: operand v%d is the result of the instruction that throws in b%d", in.Result, in.Block, a, p)
					}
				}
			}
			continue
		}
		for _, a := range args {
			if a == NoValue {
				continue
			}
			val := f.Value(a)
			if val == nil || val.Kind != ValueInstr {
				continue
			}
			def := &f.Instrs[val.Def]
			if def.Block == in.Block {
				if v.pos[def.ID] >= v.pos[in.ID] {
					v.errorf("i%d (%s) uses v%d before its definition in b%d", i, in.Op, a, in.Block)
				}
				continue
			}
			if !v.dom.Dominates(def.Block, in.Block) {
				v.errorf("i%d (%s) in b%d uses v%d from b%d, which does not dominate it", i, in.Op, in.Block, a, def.Block)
			}
		}
	}
}

// availableAtEnd reports whether value a is defined on every path to the end of block b.
func (v *validator) availableAtEnd(a ValueID, b BlockID) bool {
	val := v.f.Value(a)
	if val == nil {
		return false
	}
	if val.Kind != ValueInstr {
		return true
	}
	return v.dom.Dominates(v.f.Instrs[val.Def].Block, b)
}

// throwing returns the instruction of b that can throw, if any.
func (v *validator) throwing(b BlockID) *Instr {
	blk := &v.f.Blocks[b]
	for i := len(blk.Instrs) - 1; i >= 0; i-- {
		if in := v.f.Instr(blk.Instrs[i]); in.Op.CanThrow() {
			return in
		}
	}
	return nil
}
