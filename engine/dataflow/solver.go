// Worklist solvers.

package dataflow

import "github.com/patchd/patchd/engine/ir"

// DefaultMaxPasses bounds the work a solver does on one function: it stops
// after visiting every block (or instruction) this many times on average.
const DefaultMaxPasses = 50

// Options tune a solver. The zero value is the default.
type Options struct {
	// MaxPasses caps the work per function as a number of passes over it
	// (0: DefaultMaxPasses). A solver that hits the cap stops with the
	// facts it has, which may be below the fixed point, and reports Capped.
	MaxPasses int
}

func (o Options) maxPasses() int {
	if o.MaxPasses <= 0 {
		return DefaultMaxPasses
	}
	return o.MaxPasses
}

// Result says how a solver run went.
type Result struct {
	Visits int  // blocks (dense) or instructions (sparse) transferred
	Passes int  // Visits divided by the function's size, rounded up
	Capped bool // stopped at MaxPasses before reaching the fixed point
}

// RPO returns the blocks reachable from f's entry, through normal and
// exceptional edges, in reverse postorder: every block comes before its
// successors except along back edges. Dead blocks are left out.
func RPO(f *ir.Func) []ir.BlockID {
	n := len(f.Blocks)
	if n <= 1 || f.Entry == ir.NoBlock {
		return nil
	}
	seen := make([]bool, n)
	post := make([]ir.BlockID, 0, n-1)
	type frame struct {
		b    ir.BlockID
		next int
	}
	succ := func(b ir.BlockID, i int) (ir.BlockID, bool) {
		blk := &f.Blocks[b]
		if i < len(blk.Succs) {
			return blk.Succs[i], true
		}
		if i == len(blk.Succs) && blk.Handler != ir.NoBlock {
			return blk.Handler, true
		}
		return ir.NoBlock, false
	}
	stack := []frame{{b: f.Entry}}
	seen[f.Entry] = true
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		s, ok := succ(top.b, top.next)
		if !ok {
			post = append(post, top.b)
			stack = stack[:len(stack)-1]
			continue
		}
		top.next++
		if !seen[s] {
			seen[s] = true
			stack = append(stack, frame{b: s})
		}
	}
	for i, j := 0, len(post)-1; i < j; i, j = i+1, j-1 {
		post[i], post[j] = post[j], post[i]
	}
	return post
}

// Forward runs a dense forward analysis over f's live blocks: in[b] is the
// join of out[p] over b's predecessors (normal and exceptional, so a handler
// sees the state at every throwing block), out[b] = transfer(b, in[b]), and
// the entry block starts from entry. Blocks are visited from a worklist in
// reverse postorder until nothing changes. Dead blocks keep Bottom.
//
// transfer must not modify in; it returns the block's out fact.
func Forward[T any](f *ir.Func, lat Lattice[T], entry T, transfer func(b ir.BlockID, in T) T, opts Options) (in, out []T, res Result) {
	n := len(f.Blocks)
	in, out = make([]T, n), make([]T, n)
	for i := range in {
		in[i], out[i] = lat.Bottom(), lat.Bottom()
	}
	rpo := RPO(f)
	if len(rpo) == 0 {
		return in, out, res
	}
	pos := make([]int, n)
	for i := range pos {
		pos[i] = -1
	}
	for i, b := range rpo {
		pos[b] = i
	}
	updates := make([]int, n)
	queued := make([]bool, n)
	visited := make([]bool, n)
	// The worklist is processed in RPO order, pass after pass: pending
	// blocks with a higher position than the current one run in this pass,
	// the rest in the next.
	pending := len(rpo)
	for _, b := range rpo {
		queued[b] = true
	}
	limit := opts.maxPasses() * len(rpo)
	for pending > 0 {
		for _, b := range rpo {
			if !queued[b] {
				continue
			}
			if res.Visits >= limit {
				res.Capped = true
				res.Passes = ceilDiv(res.Visits, len(rpo))
				return in, out, res
			}
			queued[b] = false
			pending--
			res.Visits++
			blk := &f.Blocks[b]
			fact := lat.Bottom()
			if b == f.Entry {
				fact = entry
			}
			for _, p := range blk.Preds {
				if pos[p] >= 0 {
					fact = lat.Join(fact, out[p])
				}
			}
			in[b] = fact
			next := transfer(b, fact)
			if visited[b] && lat.Leq(next, out[b]) {
				continue
			}
			visited[b] = true
			updates[b]++
			out[b] = combine(lat, out[b], next, updates[b])
			enqueue := func(s ir.BlockID) {
				if pos[s] >= 0 && !queued[s] {
					queued[s] = true
					pending++
				}
			}
			for _, s := range blk.Succs {
				enqueue(s)
			}
			if blk.Handler != ir.NoBlock {
				enqueue(blk.Handler)
			}
		}
	}
	res.Passes = ceilDiv(res.Visits, len(rpo))
	return in, out, res
}

func ceilDiv(a, b int) int {
	if b == 0 {
		return 0
	}
	return (a + b - 1) / b
}

// SparseOptions tune Sparse.
type SparseOptions[T any] struct {
	Options
	// Init seeds values that no instruction defines (parameters, this,
	// free variables, globals). Values it leaves out start at Bottom.
	Init func(v *ir.Value) (T, bool)
	// Extra returns instructions to re-run when v's fact changes beyond
	// v's users in the def-use chains: memory edges such as a store's
	// value reaching a load of the same cell.
	Extra func(v ir.ValueID) []ir.InstrID
}

// Sparse is the state of a sparse analysis: one fact per SSA value,
// propagated along def-use chains. Transfer functions read facts with Get
// and grow them with Add, which re-queues the value's users.
type Sparse[T any] struct {
	f       *ir.Func
	lat     Lattice[T]
	du      *DefUse
	extra   func(ir.ValueID) []ir.InstrID
	facts   []T
	updates []int
	live    []bool // by block
	queue   []ir.InstrID
	queued  []bool // by instruction
}

// Get returns v's fact.
func (s *Sparse[T]) Get(v ir.ValueID) T {
	if v == ir.NoValue || int(v) >= len(s.facts) {
		return s.lat.Bottom()
	}
	return s.facts[v]
}

// Facts returns every value's fact, indexed by ValueID. The slice is shared.
func (s *Sparse[T]) Facts() []T { return s.facts }

// Live reports whether a block is reachable from the entry; the solver never
// runs instructions of dead blocks.
func (s *Sparse[T]) Live(b ir.BlockID) bool { return int(b) < len(s.live) && s.live[b] }

// Add joins fact into v's fact. If that changes it, the instructions that use
// v (and v's extra dependants) run again. It reports whether v changed.
func (s *Sparse[T]) Add(v ir.ValueID, fact T) bool {
	if v == ir.NoValue || int(v) >= len(s.facts) || s.lat.Leq(fact, s.facts[v]) {
		return false
	}
	s.updates[v]++
	s.facts[v] = combine(s.lat, s.facts[v], fact, s.updates[v])
	for _, u := range s.du.Uses(v) {
		s.push(u)
	}
	if s.extra != nil {
		for _, u := range s.extra(v) {
			s.push(u)
		}
	}
	return true
}

func (s *Sparse[T]) push(id ir.InstrID) {
	in := s.f.Instr(id)
	if in == nil || s.queued[id] || !s.Live(in.Block) {
		return
	}
	s.queued[id] = true
	s.queue = append(s.queue, id)
}

// SolveSparse runs a sparse analysis over f: every live instruction runs once
// in reverse postorder, and again whenever a value it uses (or depends on
// through opts.Extra) changes, until nothing changes. Facts only grow, so
// with a finite-height lattice it terminates; MaxPasses bounds it anyway,
// counted in instructions.
func SolveSparse[T any](f *ir.Func, lat Lattice[T], du *DefUse, transfer func(s *Sparse[T], in *ir.Instr), opts SparseOptions[T]) (*Sparse[T], Result) {
	if du == nil {
		du = NewDefUse(f)
	}
	s := &Sparse[T]{
		f:       f,
		lat:     lat,
		du:      du,
		extra:   opts.Extra,
		facts:   make([]T, len(f.Values)),
		updates: make([]int, len(f.Values)),
		live:    make([]bool, len(f.Blocks)),
		queued:  make([]bool, len(f.Instrs)),
	}
	for i := range s.facts {
		s.facts[i] = lat.Bottom()
	}
	if opts.Init != nil {
		for i := 1; i < len(f.Values); i++ {
			if fact, ok := opts.Init(&f.Values[i]); ok {
				s.facts[i] = fact
			}
		}
	}
	rpo := RPO(f)
	n := 0
	for _, b := range rpo {
		s.live[b] = true
		n += len(f.Blocks[b].Instrs)
	}
	for _, b := range rpo {
		for _, id := range f.Blocks[b].Instrs {
			s.push(id)
		}
	}
	var res Result
	limit := opts.maxPasses() * max(n, 1)
	for head := 0; head < len(s.queue); head++ {
		if res.Visits >= limit {
			res.Capped = true
			break
		}
		id := s.queue[head]
		s.queued[id] = false
		res.Visits++
		transfer(s, f.Instr(id))
		// Reclaim the processed prefix now and then so long runs don't grow
		// the queue without bound.
		if head > 1024 && head*2 > len(s.queue) {
			s.queue = append(s.queue[:0], s.queue[head+1:]...)
			head = -1
		}
	}
	res.Passes = ceilDiv(res.Visits, max(n, 1))
	return s, res
}
