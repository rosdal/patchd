// Paths from a source to a sink argument.

package taint

import "github.com/patchd/patchd/engine/ir"

// PathStep is one value on a taint path and the instruction that gave it the
// taint: the source instruction for the first step, then the instruction of
// each propagation edge.
type PathStep struct {
	Value ir.ValueID
	Instr ir.InstrID // NoInstr for a parameter source
}

// Path returns the shortest chain of values from a candidate's origin to the
// sink argument, along edges whose source value carries the origin's label.
// It is never empty for a candidate the analysis produced: the first step is
// the origin, the last the sink argument.
func (r *FuncResult) Path(c Candidate) []PathStep {
	origin := r.Origin(c.Label)
	if origin == nil {
		return nil
	}
	// Breadth-first search backwards from the sink argument.
	type visit struct {
		next ir.ValueID // the value one step closer to the sink
		via  ir.InstrID // the instruction of the edge from this value to next
	}
	seen := map[ir.ValueID]visit{c.Value: {}}
	queue := []ir.ValueID{c.Value}
	for len(queue) > 0 {
		v := queue[0]
		queue = queue[1:]
		if v == origin.Value {
			break
		}
		if int(v) >= len(r.into) {
			continue
		}
		for _, ei := range r.into[v] {
			e := r.edges[ei]
			if _, ok := seen[e.from]; ok {
				continue
			}
			if _, ok := r.Facts[e.from].Label(c.Label.Origin); !ok {
				continue
			}
			seen[e.from] = visit{next: v, via: e.instr}
			queue = append(queue, e.from)
		}
	}
	if _, ok := seen[origin.Value]; !ok {
		return []PathStep{{Value: origin.Value, Instr: origin.Instr}, {Value: c.Value, Instr: defOf(r.Func, c.Value)}}
	}
	path := []PathStep{{Value: origin.Value, Instr: origin.Instr}}
	for v := origin.Value; v != c.Value; {
		step := seen[v]
		path = append(path, PathStep{Value: step.next, Instr: step.via})
		v = step.next
	}
	return path
}

func defOf(f *ir.Func, v ir.ValueID) ir.InstrID {
	if val := f.Value(v); val != nil && val.Kind == ir.ValueInstr {
		return val.Def
	}
	return ir.NoInstr
}
