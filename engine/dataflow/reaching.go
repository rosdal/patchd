// Reaching stores for cells and module globals.

package dataflow

import (
	"slices"

	"github.com/patchd/patchd/engine/ir"
)

// MemKey names a variable that lives in memory rather than in SSA: a
// captured variable's cell (by the cell value) or a module global (by name).
type MemKey struct {
	Cell   ir.ValueID // the cell, or NoValue for a global
	Global ir.Name
}

// ReachingStores tells, for each load of a cell (load *c) or global
// (load global x) in one function, which stores of the same function may
// have written the value it reads. A store to the same variable kills the
// stores before it; a cell reached through a phi of several cells sees the
// stores of each. Stores in other functions (a closure writing a captured
// variable) are not seen: v0.4's analyses are intraprocedural.
type ReachingStores struct {
	loads  map[ir.InstrID][]ir.InstrID // load -> stores reaching it, ascending
	stores map[ir.InstrID][]ir.InstrID // store -> loads it reaches, ascending
	result Result
}

// StoresFor returns the stores that may reach a load. The slice is shared.
func (r *ReachingStores) StoresFor(load ir.InstrID) []ir.InstrID { return r.loads[load] }

// LoadsOf returns the loads a store may reach. The slice is shared.
func (r *ReachingStores) LoadsOf(store ir.InstrID) []ir.InstrID { return r.stores[store] }

// Result reports the solver run.
func (r *ReachingStores) Result() Result { return r.result }

// storeFact is a set of (variable, store) pairs, sorted. Facts are never
// modified once built: transfer functions make new ones.
type storeFact []storeEntry

type storeEntry struct {
	key   MemKey
	store ir.InstrID
}

func entryLess(a, b storeEntry) int {
	switch {
	case a.key.Cell != b.key.Cell:
		return int(a.key.Cell) - int(b.key.Cell)
	case a.key.Global != b.key.Global:
		if a.key.Global.String() < b.key.Global.String() {
			return -1
		}
		return 1
	}
	return int(a.store) - int(b.store)
}

type storeLattice struct{}

func (storeLattice) Bottom() storeFact { return nil }

func (storeLattice) Join(a, b storeFact) storeFact {
	if len(a) == 0 {
		return b
	}
	if len(b) == 0 {
		return a
	}
	out := make(storeFact, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch c := entryLess(a[i], b[j]); {
		case c < 0:
			out = append(out, a[i])
			i++
		case c > 0:
			out = append(out, b[j])
			j++
		default:
			out = append(out, a[i])
			i++
			j++
		}
	}
	out = append(out, a[i:]...)
	return append(out, b[j:]...)
}

func (storeLattice) Leq(a, b storeFact) bool {
	j := 0
	for _, e := range a {
		for j < len(b) && entryLess(b[j], e) < 0 {
			j++
		}
		if j == len(b) || entryLess(b[j], e) != 0 {
			return false
		}
	}
	return true
}

// Reaching computes f's reaching stores with a dense forward analysis over
// its blocks.
func Reaching(f *ir.Func, opts Options) *ReachingStores {
	r := &ReachingStores{loads: map[ir.InstrID][]ir.InstrID{}, stores: map[ir.InstrID][]ir.InstrID{}}
	roots := cellRoots(f)
	keysOf := func(in *ir.Instr) []MemKey {
		args := f.Args(in)
		switch {
		case in.Aux.Flags.Has(ir.AuxGlobal):
			return []MemKey{{Global: in.Aux.Name}}
		case in.Aux.Flags.Has(ir.AuxCell) && len(args) > 0:
			rs := roots(args[0])
			keys := make([]MemKey, len(rs))
			for i, c := range rs {
				keys[i] = MemKey{Cell: c}
			}
			return keys
		}
		return nil
	}
	hasMemory := false
	for i := 1; i < len(f.Instrs) && !hasMemory; i++ {
		in := &f.Instrs[i]
		hasMemory = (in.Op == ir.OpLoad || in.Op == ir.OpStore) && (in.Aux.Flags.Has(ir.AuxGlobal) || in.Aux.Flags.Has(ir.AuxCell))
	}
	if !hasMemory {
		return r
	}
	// step applies one instruction to a fact; for a load it reports the
	// stores reaching it.
	step := func(fact storeFact, in *ir.Instr, onLoad func(ir.InstrID, []ir.InstrID)) storeFact {
		if in.Op != ir.OpLoad && in.Op != ir.OpStore {
			return fact
		}
		keys := keysOf(in)
		if len(keys) == 0 {
			return fact
		}
		if in.Op == ir.OpLoad {
			if onLoad != nil {
				var reach []ir.InstrID
				for _, e := range fact {
					if slices.Contains(keys, e.key) {
						reach = append(reach, e.store)
					}
				}
				slices.Sort(reach)
				onLoad(in.ID, slices.Compact(reach))
			}
			return fact
		}
		// A store through a single known variable replaces its earlier
		// stores (strong update); through a phi of cells it may write any of
		// them, so it only adds (weak update).
		out := make(storeFact, 0, len(fact)+len(keys))
		strong := len(keys) == 1
		for _, e := range fact {
			if strong && e.key == keys[0] {
				continue
			}
			out = append(out, e)
		}
		for _, k := range keys {
			out = append(out, storeEntry{key: k, store: in.ID})
		}
		slices.SortFunc(out, entryLess)
		return out
	}
	transfer := func(b ir.BlockID, fact storeFact) storeFact {
		for _, id := range f.Blocks[b].Instrs {
			fact = step(fact, f.Instr(id), nil)
		}
		return fact
	}
	in, _, res := Forward[storeFact](f, storeLattice{}, nil, transfer, opts)
	r.result = res
	for _, b := range RPO(f) {
		fact := in[b]
		for _, id := range f.Blocks[b].Instrs {
			fact = step(fact, f.Instr(id), func(load ir.InstrID, stores []ir.InstrID) {
				if len(stores) == 0 {
					return
				}
				r.loads[load] = stores
				for _, s := range stores {
					r.stores[s] = append(r.stores[s], load)
				}
			})
		}
	}
	for s, loads := range r.stores {
		slices.Sort(loads)
		r.stores[s] = slices.Compact(loads)
	}
	return r
}

// cellRoots returns a function that resolves a cell pointer to the cells it
// may be: itself, or for a phi (per-iteration cells of a for-let loop) or a
// copy, the cells of its operands.
func cellRoots(f *ir.Func) func(ir.ValueID) []ir.ValueID {
	memo := map[ir.ValueID][]ir.ValueID{}
	var resolve func(v ir.ValueID, visiting map[ir.ValueID]bool) []ir.ValueID
	resolve = func(v ir.ValueID, visiting map[ir.ValueID]bool) []ir.ValueID {
		if r, ok := memo[v]; ok {
			return r
		}
		val := f.Value(v)
		if val == nil || val.Kind != ir.ValueInstr {
			return []ir.ValueID{v}
		}
		in := f.Instr(val.Def)
		if in == nil || (in.Op != ir.OpPhi && in.Op != ir.OpAssign) {
			return []ir.ValueID{v}
		}
		if visiting[v] {
			return nil
		}
		visiting[v] = true
		var out []ir.ValueID
		for _, a := range f.Args(in) {
			if a != v {
				out = append(out, resolve(a, visiting)...)
			}
		}
		delete(visiting, v)
		slices.Sort(out)
		out = slices.Compact(out)
		if len(out) == 0 {
			out = []ir.ValueID{v}
		}
		memo[v] = out
		return out
	}
	return func(v ir.ValueID) []ir.ValueID { return resolve(v, map[ir.ValueID]bool{}) }
}
