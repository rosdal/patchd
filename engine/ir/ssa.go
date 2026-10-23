package ir

import "github.com/patchd/patchd/engine/tsbridge"

// SSA construction follows Braun et al., "Simple and Efficient Construction
// of Static Single Assignment Form" (CC 2013): the builder records the
// current definition of each local variable per block while it lowers, reads
// look backwards through predecessors, blocks are sealed once all their
// predecessors are known, reads in unsealed blocks create operand-less phis
// completed at sealing, and trivial phis are removed when the function is
// finished (removeTrivialPhis). Captured variables are cells and never enter
// SSA themselves; the cell pointer does, so a for (let …) loop that makes a
// fresh cell per iteration merges cells with phis like any other value.

// --- SSA construction (Braun et al. 2013) ---

func defKey(b BlockID, v int32) uint64 { return uint64(b)<<32 | uint64(uint32(v)) } //nolint:gosec // var indexes are non-negative

func (fb *fnBuilder) writeVariable(v int32, b BlockID, val ValueID) {
	fb.defs[defKey(b, v)] = val
}

func (fb *fnBuilder) readVariable(v int32, b BlockID) ValueID {
	if val, ok := fb.defs[defKey(b, v)]; ok {
		return val
	}
	return fb.readVariableRecursive(v, b)
}

func (fb *fnBuilder) readVariableRecursive(v int32, b BlockID) ValueID {
	var val ValueID
	preds := fb.f.Blocks[b].Preds
	switch {
	case !fb.sealed[b]:
		phi := fb.newPhi(b, v)
		fb.pending[b] = append(fb.pending[b], pendingPhi{v: v, phi: phi})
		val = fb.f.Instrs[phi].Result
	case len(preds) == 0:
		val = fb.undef
	case len(preds) == 1:
		val = fb.readVariable(v, preds[0])
	default:
		phi := fb.newPhi(b, v)
		val = fb.f.Instrs[phi].Result
		fb.writeVariable(v, b, val)
		fb.addPhiOperands(v, phi)
	}
	fb.writeVariable(v, b, val)
	return val
}

// newPhi creates an operand-less phi at the start of b.
func (fb *fnBuilder) newPhi(b BlockID, v int32) InstrID {
	id := InstrID(len(fb.f.Instrs)) //nolint:gosec // see newValue
	vi := &fb.b.vars[v]
	in := Instr{ID: id, Op: OpPhi, Block: b, Source: fb.f.Source}
	in.Result = fb.newValue(Value{Kind: ValueInstr, Def: id, Name: vi.name})
	fb.typeOf(in.Result, vi.decl)
	fb.f.Instrs = append(fb.f.Instrs, in)
	fb.blkPhis[b] = append(fb.blkPhis[b], id)
	fb.phiVar[id] = v
	return id
}

func (fb *fnBuilder) addPhiOperands(v int32, phi InstrID) {
	b := fb.f.Instrs[phi].Block
	preds := fb.f.Blocks[b].Preds
	ops := make([]ValueID, len(preds))
	for i, p := range preds {
		ops[i] = fb.readVariable(v, p)
	}
	in := &fb.f.Instrs[phi]
	in.argOff, in.argLen = uint32(len(fb.f.args)), uint32(len(ops)) //nolint:gosec // see newValue
	fb.f.args = append(fb.f.args, ops...)
}

// seal records that all of b's predecessors are known and completes its
// pending phis.
func (fb *fnBuilder) seal(b BlockID) {
	if fb.sealed[b] {
		return
	}
	fb.sealed[b] = true
	for _, p := range fb.pending[b] {
		fb.addPhiOperands(p.v, p.phi)
	}
	delete(fb.pending, b)
}

// phiOf builds a phi for an expression's value at a merge: one operand per
// predecessor of b, in order. b must be sealed and current.
func (fb *fnBuilder) phiOf(n tsbridge.NodeID, ops []ValueID) ValueID {
	b := fb.cur
	id := InstrID(len(fb.f.Instrs))                                                                               //nolint:gosec // see newValue
	in := Instr{ID: id, Op: OpPhi, Block: b, Syntax: n, argOff: uint32(len(fb.f.args)), argLen: uint32(len(ops))} //nolint:gosec // see newValue
	in.Source = fb.t.Range(n)
	fb.f.args = append(fb.f.args, ops...)
	in.Result = fb.newValue(Value{Kind: ValueInstr, Def: id, Syntax: n})
	fb.typeOf(in.Result, n)
	fb.f.Instrs = append(fb.f.Instrs, in)
	fb.blkPhis[b] = append(fb.blkPhis[b], id)
	return in.Result
}
