package ir

import (
	"cmp"
	"context"
	"fmt"
	"runtime"
	"runtime/debug"
	"slices"
	"sync"

	"github.com/patchd/patchd/engine/tsbridge"
)

// BuildModule lowers one file of a program to IR. It never panics: syntax it
// does not support becomes unknown instructions, and a function whose
// lowering fails becomes a single unknown block, both with a warning.
func BuildModule(prog *tsbridge.Program, file tsbridge.FileID) (*Module, []Warning) {
	tree := prog.Tree(file)
	f := prog.File(file)
	if tree == nil || f == nil {
		return nil, []Warning{{Code: WarnPanic, Message: "no such file"}}
	}
	b := newBuilder(prog, tree)
	m := b.build()
	return m, m.Warnings
}

// BuildOptions tune BuildProgram. The zero value is the default.
type BuildOptions struct {
	Jobs int // files lowered at once (0: one per CPU)
	// Files limits lowering to these files (0: every root file that is not a
	// declaration file).
	Files []tsbridge.FileID
}

// BuildProgram lowers every root source file of a program, in parallel. The
// modules are in the program's file order; nil entries are files that could
// not be lowered at all.
func BuildProgram(ctx context.Context, prog *tsbridge.Program, opts BuildOptions) []*Module {
	files := opts.Files
	if files == nil {
		files = SourceFiles(prog)
	}
	jobs := opts.Jobs
	if jobs <= 0 {
		jobs = runtime.NumCPU()
	}
	out := make([]*Module, len(files))
	var wg sync.WaitGroup
	next := make(chan int)
	for range min(jobs, max(1, len(files))) {
		wg.Go(func() {
			for i := range next {
				out[i], _ = BuildModule(prog, files[i])
			}
		})
	}
	for i := range files {
		if ctx.Err() != nil {
			break
		}
		next <- i
	}
	close(next)
	wg.Wait()
	return out
}

// SourceFiles returns the files BuildProgram lowers by default: the
// program's root files that are not declaration files.
func SourceFiles(prog *tsbridge.Program) []tsbridge.FileID {
	var out []tsbridge.FileID
	for _, id := range prog.RootFiles() {
		if f := prog.File(id); f != nil && !f.Declaration {
			out = append(out, id)
		}
	}
	return out
}

// builder lowers one file.
type builder struct {
	prog *tsbridge.Program
	tree *tsbridge.Tree
	file *tsbridge.File
	mod  *Module
	js   bool

	fns     []*fnInfo               // FuncID-1 -> function being built
	fnOf    map[tsbridge.NodeID]int // function-like node -> index into fns
	classes map[tsbridge.NodeID]*classInfo
	vars    []varInfo
	symVar  map[tsbridge.SymbolID]int32     // declared symbol -> var index
	idVar   map[tsbridge.NodeID]int32       // identifier -> var index+1 it names
	scopes  map[tsbridge.NodeID]*scopeInfo  // block-scope node -> what it declares
	nodeFn  map[tsbridge.NodeID]int         // identifiers, this, super, arguments -> function they are in
	special map[tsbridge.NodeID]FreeVarKind // arguments identifiers that mean the arguments object
	nsOf    map[tsbridge.NodeID]int         // namespace declaration -> its function

	seenNs   map[int32]bool
	marks    []uint8 // per node index: markLowered, markUnknown
	warnings []Warning
	typeReqs []typeReq
}

const (
	markNone uint8 = iota
	markLowered
	markUnknown
)

type typeReq struct {
	fn   *Func
	v    ValueID
	node tsbridge.NodeID
}

func newBuilder(prog *tsbridge.Program, tree *tsbridge.Tree) *builder {
	file := tree.File()
	return &builder{
		prog:    prog,
		tree:    tree,
		file:    file,
		js:      file.JS,
		mod:     &Module{File: file.ID, Path: file.Path},
		fnOf:    map[tsbridge.NodeID]int{},
		classes: map[tsbridge.NodeID]*classInfo{},
		symVar:  map[tsbridge.SymbolID]int32{},
		idVar:   map[tsbridge.NodeID]int32{},
		scopes:  map[tsbridge.NodeID]*scopeInfo{},
		nodeFn:  map[tsbridge.NodeID]int{},
		special: map[tsbridge.NodeID]FreeVarKind{},
		nsOf:    map[tsbridge.NodeID]int{},
		marks:   make([]uint8, tree.Len()),
	}
}

func (b *builder) build() *Module {
	b.scan()
	for _, fi := range b.fns {
		b.lowerFunc(fi)
	}
	b.resolveTypes()
	b.mod.Coverage = b.coverage()
	b.mod.Warnings = append(b.mod.Warnings, b.warnings...)
	return b.mod
}

// lowerFunc lowers one function, recovering a panic into an unknown body.
func (b *builder) lowerFunc(fi *fnInfo) {
	defer func() {
		if r := recover(); r != nil {
			b.warn(WarnPanic, fi.node, fmt.Sprintf("lowering %s panicked: %v", fi.f.Name, r))
			_ = debug.Stack
			b.panicBody(fi)
		}
	}()
	fb := newFnBuilder(b, fi)
	fb.lowerBody()
	fb.finish()
}

// panicBody replaces a function's body with one unknown block.
func (b *builder) panicBody(fi *fnInfo) {
	fb := newFnBuilder(b, fi)
	fb.f.Flags |= FuncPanicked
	fb.markUnknown(fi.node)
	v := fb.emit(OpUnknown, fi.node, Aux{Name: Intern("panic")})
	fb.terminate(OpReturn, fi.node, []ValueID{v})
	fb.finish()
}

func (b *builder) warn(code string, n tsbridge.NodeID, msg string) {
	if len(b.warnings) >= 200 {
		return
	}
	b.warnings = append(b.warnings, Warning{Code: code, Message: msg, Path: b.file.Path, Range: b.tree.Range(n)})
}

// resolveTypes asks tsbridge for the type of every value with a syntax node,
// in one batch.
func (b *builder) resolveTypes() {
	nodes := make([]tsbridge.NodeID, len(b.typeReqs))
	for i, r := range b.typeReqs {
		nodes[i] = r.node
	}
	types := b.prog.TypesOf(nodes)
	for i, r := range b.typeReqs {
		if r.v == NoValue {
			r.fn.Type = types[i]
		} else if v := r.fn.Value(r.v); v != nil {
			v.Type = types[i]
		}
	}
	b.typeReqs = nil
}

// fnBuilder holds the state of one function under construction.
type fnBuilder struct {
	b    *builder
	t    *tsbridge.Tree
	info *fnInfo
	f    *Func

	cur      BlockID
	blkInstr [][]InstrID // per block: non-phi instructions, in order
	blkPhis  [][]InstrID // per block: phis
	sealed   []bool
	defs     map[uint64]ValueID // (block, var) -> current definition
	pending  map[BlockID][]pendingPhi
	phiVar   map[InstrID]int32

	targets []jumpTarget
	regions []region
	chains  []BlockID // short-circuit blocks of the optional chains being lowered

	undef   ValueID
	this    ValueID
	args    ValueID
	newTgt  ValueID
	self    ValueID
	free    map[captureKey]ValueID
	globals map[string]ValueID
	intrins map[string]ValueID
	exports map[int32]bool // module-level vars exported by declaration (Init only)
	enums   []map[string]ValueID
}

type pendingPhi struct {
	v   int32
	phi InstrID
}

// jumpTarget is a break or continue target.
type jumpTarget struct {
	labels  []string
	brk     BlockID
	cont    BlockID // NoBlock: continue can't target it (switch, labelled block)
	loop    bool    // a loop or switch: unlabelled break targets it
	regions int     // len(regions) when the target was pushed
}

// region is an enclosing try: where its exceptions go and what finally body
// runs on the way out.
type region struct {
	handler BlockID
	finally tsbridge.NodeID
	targets int // len(targets) when the region was pushed
}

func newFnBuilder(b *builder, fi *fnInfo) *fnBuilder {
	f := fi.f
	f.Blocks = []Block{{}}
	f.Instrs = []Instr{{}}
	f.Values = []Value{{}}
	f.args, f.parts, f.paths, f.cells = nil, nil, nil, nil
	f.Params, f.FreeVars = nil, nil
	f.This = NoValue
	f.byName = map[string]ValueID{}
	fb := &fnBuilder{
		b:        b,
		t:        b.tree,
		info:     fi,
		f:        f,
		blkInstr: [][]InstrID{nil},
		blkPhis:  [][]InstrID{nil},
		sealed:   []bool{false},
		defs:     map[uint64]ValueID{},
		pending:  map[BlockID][]pendingPhi{},
		phiVar:   map[InstrID]int32{},
		free:     map[captureKey]ValueID{},
		globals:  map[string]ValueID{},
		intrins:  map[string]ValueID{},
	}
	fb.undef = fb.newValue(Value{Kind: ValueUndefined})
	return fb
}

// newValue appends a value and returns its ID.
func (fb *fnBuilder) newValue(v Value) ValueID {
	v.ID = ValueID(len(fb.f.Values)) //nolint:gosec // a function has far fewer than 2^32 values
	fb.f.Values = append(fb.f.Values, v)
	return v.ID
}

// newBlock creates an unsealed, empty block.
func (fb *fnBuilder) newBlock(comment string) BlockID {
	id := BlockID(len(fb.f.Blocks)) //nolint:gosec // see newValue
	fb.f.Blocks = append(fb.f.Blocks, Block{ID: id, Comment: comment})
	fb.blkInstr = append(fb.blkInstr, nil)
	fb.blkPhis = append(fb.blkPhis, nil)
	fb.sealed = append(fb.sealed, false)
	return id
}

// addEdge records a normal edge from → to.
func (fb *fnBuilder) addEdge(from, to BlockID) {
	fb.f.Blocks[from].Succs = append(fb.f.Blocks[from].Succs, to)
	fb.f.Blocks[to].Preds = append(fb.f.Blocks[to].Preds, from)
}

// ensureBlock gives code after a terminator a block: a dead one, sealed
// with no predecessors, so the code is still lowered (bugs live there too).
func (fb *fnBuilder) ensureBlock() {
	if fb.cur != NoBlock {
		return
	}
	b := fb.newBlock("unreachable")
	fb.f.Blocks[b].Dead = true
	fb.seal(b)
	fb.cur = b
}

// reachable reports whether code at this point can run: false after a
// terminator, until something jumps to a new block.
func (fb *fnBuilder) reachable() bool { return fb.cur != NoBlock }

// emitArgs appends an instruction with arguments to the current block and
// returns its result (NoValue for ops without one). Inside a try, a can-throw
// instruction ends its block, with an exceptional edge to the handler, so
// each throwing point has its own variable state for the handler's phis.
func (fb *fnBuilder) emitArgs(op Op, n tsbridge.NodeID, aux Aux, args []ValueID) ValueID {
	fb.ensureBlock()
	id := InstrID(len(fb.f.Instrs))                                                                                            //nolint:gosec // see newValue
	in := Instr{ID: id, Op: op, Block: fb.cur, Aux: aux, Syntax: n, argOff: uint32(len(fb.f.args)), argLen: uint32(len(args))} //nolint:gosec // see newValue
	if n != 0 {
		in.Source = fb.t.Range(n)
	} else {
		in.Source = fb.f.Source
	}
	fb.f.args = append(fb.f.args, args...)
	if op.HasResult() {
		in.Result = fb.newValue(Value{Kind: ValueInstr, Def: id, Syntax: n})
	}
	fb.f.Instrs = append(fb.f.Instrs, in)
	fb.blkInstr[fb.cur] = append(fb.blkInstr[fb.cur], id)
	if op.CanThrow() && !op.IsTerminator() {
		fb.afterThrowing()
	}
	return in.Result
}

// emit is emitArgs with variadic arguments.
func (fb *fnBuilder) emit(op Op, n tsbridge.NodeID, aux Aux, args ...ValueID) ValueID {
	return fb.emitArgs(op, n, aux, args)
}

// emitTyped emits an instruction whose result is the value of expression n:
// the result gets n's type.
func (fb *fnBuilder) emitTyped(op Op, n tsbridge.NodeID, aux Aux, args ...ValueID) ValueID {
	v := fb.emitArgs(op, n, aux, args)
	fb.typeOf(v, n)
	return v
}

// typeOf asks for v's type to be n's.
func (fb *fnBuilder) typeOf(v ValueID, n tsbridge.NodeID) {
	if v != NoValue && n != 0 {
		fb.b.typeReqs = append(fb.b.typeReqs, typeReq{fn: fb.f, v: v, node: n})
	}
}

// withParts attaches a part list to an Aux.
func (fb *fnBuilder) withParts(aux Aux, parts []Part) Aux {
	aux.partOff = uint32(len(fb.f.parts)) //nolint:gosec // see newValue
	aux.partLen = uint32(len(parts))      //nolint:gosec // see newValue
	fb.f.parts = append(fb.f.parts, parts...)
	return aux
}

// afterThrowing ends the current block after a can-throw instruction when a
// handler is active.
func (fb *fnBuilder) afterThrowing() {
	h := fb.handler()
	if h == NoBlock {
		return
	}
	from := fb.cur
	fb.f.Blocks[from].Handler = h
	fb.f.Blocks[h].Preds = append(fb.f.Blocks[h].Preds, from)
	next := fb.newBlock(fb.f.Blocks[from].Comment)
	fb.terminate(OpJump, 0, nil, next)
	fb.seal(next)
	fb.cur = next
}

// handler returns the innermost active exception handler.
func (fb *fnBuilder) handler() BlockID {
	for i := len(fb.regions) - 1; i >= 0; i-- {
		if fb.regions[i].handler != NoBlock {
			return fb.regions[i].handler
		}
	}
	return NoBlock
}

// terminate ends the current block. succs are the normal successors; a
// throw also gets an exceptional edge to the active handler.
func (fb *fnBuilder) terminate(op Op, n tsbridge.NodeID, args []ValueID, succs ...BlockID) {
	fb.ensureBlock()
	from := fb.cur
	id := InstrID(len(fb.f.Instrs))                                                                                //nolint:gosec // see newValue
	in := Instr{ID: id, Op: op, Block: from, Syntax: n, argOff: uint32(len(fb.f.args)), argLen: uint32(len(args))} //nolint:gosec // see newValue
	if n != 0 {
		in.Source = fb.t.Range(n)
	} else {
		in.Source = fb.f.Source
	}
	fb.f.args = append(fb.f.args, args...)
	fb.f.Instrs = append(fb.f.Instrs, in)
	fb.blkInstr[from] = append(fb.blkInstr[from], id)
	for _, s := range succs {
		fb.addEdge(from, s)
	}
	if op == OpThrow {
		if h := fb.handler(); h != NoBlock {
			fb.f.Blocks[from].Handler = h
			fb.f.Blocks[h].Preds = append(fb.f.Blocks[h].Preds, from)
		}
	}
	fb.cur = NoBlock
}

// jump ends the current block with a jump to b, if code here is reachable.
func (fb *fnBuilder) jump(b BlockID) {
	if fb.reachable() {
		fb.terminate(OpJump, 0, nil, b)
	}
}

// branch ends the current block with a conditional jump.
func (fb *fnBuilder) branch(n tsbridge.NodeID, cond ValueID, t, f BlockID) {
	fb.terminate(OpBranch, n, []ValueID{cond}, t, f)
}

// constant emits a const.
func (fb *fnBuilder) constant(n tsbridge.NodeID, lit AuxFlags, text string) ValueID {
	return fb.emitTyped(OpConst, n, Aux{Name: Intern(text), Flags: lit})
}

// global returns the value standing for a global name.
func (fb *fnBuilder) global(name string) ValueID {
	if v, ok := fb.globals[name]; ok {
		return v
	}
	v := fb.newValue(Value{Kind: ValueGlobal, Name: name})
	fb.globals[name] = v
	fb.f.byName[name] = v
	return v
}

// intrinsic returns the value of a synthetic function: %jsx, %import, %Fragment.
func (fb *fnBuilder) intrinsic(name string) ValueID {
	if v, ok := fb.intrins[name]; ok {
		return v
	}
	v := fb.newValue(Value{Kind: ValueIntrinsic, Name: name})
	fb.intrins[name] = v
	return v
}

// loadGlobal reads a global by name.
func (fb *fnBuilder) loadGlobal(n tsbridge.NodeID, name string) ValueID {
	v := fb.emitTyped(OpLoad, n, Aux{Name: Intern(name), Flags: AuxGlobal}, fb.global(name))
	fb.nameValue(v, name)
	return v
}

// storeGlobal writes a global by name.
func (fb *fnBuilder) storeGlobal(n tsbridge.NodeID, name string, v ValueID) {
	fb.emit(OpStore, n, Aux{Name: Intern(name), Flags: AuxGlobal}, fb.global(name), v)
}

// nameValue gives an unnamed instruction result a source name.
func (fb *fnBuilder) nameValue(v ValueID, name string) {
	if val := fb.f.Value(v); val != nil && val.Name == "" && val.Kind == ValueInstr {
		val.Name = name
	}
}

// finish seals every block, removes trivial phis, compacts the function and
// lays its slices out contiguously.
func (fb *fnBuilder) finish() {
	f := fb.f
	for b := 1; b < len(f.Blocks); b++ {
		fb.seal(BlockID(b)) //nolint:gosec // see newValue
	}
	fb.ignoreDeadOperands()
	repl := fb.removeTrivialPhis()
	fb.compact(repl)
}

// ignoreDeadOperands makes the operands that phis in live blocks receive
// from dead code (lowered after a return, throw or break, and falling back
// into live code) the phi itself, so they don't count: code that never runs
// contributes no values, and trivial-phi removal ignores self operands.
func (fb *fnBuilder) ignoreDeadOperands() {
	f := fb.f
	live := make([]bool, len(f.Blocks))
	stack := []BlockID{1}
	live[1] = true
	for len(stack) > 0 {
		b := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		next := f.Blocks[b].Succs
		if h := f.Blocks[b].Handler; h != NoBlock {
			next = append(next[:len(next):len(next)], h)
		}
		for _, s := range next {
			if !live[s] {
				live[s] = true
				stack = append(stack, s)
			}
		}
	}
	for b := 1; b < len(f.Blocks); b++ {
		if !live[b] {
			f.Blocks[b].Dead = true
			continue
		}
		preds := f.Blocks[b].Preds
		for _, id := range fb.blkPhis[b] {
			in := &f.Instrs[id]
			args := f.args[in.argOff : in.argOff+in.argLen]
			for i := range args {
				if i < len(preds) && !live[preds[i]] {
					args[i] = in.Result
				}
			}
		}
	}
}

// removeTrivialPhis finds phis whose operands are all one value (or the phi
// itself) and maps them to that value, repeating until nothing changes.
// Returns the replacement of each value (NoValue: kept as is).
func (fb *fnBuilder) removeTrivialPhis() []ValueID {
	f := fb.f
	repl := make([]ValueID, len(f.Values))
	find := func(v ValueID) ValueID {
		for repl[v] != NoValue {
			if r := repl[repl[v]]; r != NoValue {
				repl[v] = r
			}
			v = repl[v]
		}
		return v
	}
	for changed := true; changed; {
		changed = false
		for b := 1; b < len(f.Blocks); b++ {
			for _, id := range fb.blkPhis[b] {
				in := &f.Instrs[id]
				if repl[in.Result] != NoValue {
					continue
				}
				same := NoValue
				trivial := true
				for _, op := range f.Args(in) {
					op = find(op)
					if op == same || op == in.Result {
						continue
					}
					if same != NoValue {
						trivial = false
						break
					}
					same = op
				}
				if !trivial {
					continue
				}
				if same == NoValue {
					same = fb.undef
				}
				repl[in.Result] = same
				changed = true
			}
		}
	}
	for v := range repl {
		if repl[v] != NoValue {
			repl[v] = find(ValueID(v)) //nolint:gosec // see newValue
		}
	}
	return repl
}

// renumberBlocks orders blocks by when their code was emitted, which
// follows the source: a loop's head, body, the blocks inside it, its update,
// then what comes after. Blocks are created ahead of their code (a loop
// creates its exit block before its body), so creation order reads badly.
func (fb *fnBuilder) renumberBlocks() {
	f := fb.f
	n := len(f.Blocks)
	first := make([]InstrID, n)
	for b := 1; b < n; b++ {
		first[b] = InstrID(len(f.Instrs)) //nolint:gosec // see newValue
		if l := fb.blkInstr[b]; len(l) > 0 {
			first[b] = l[0]
		}
	}
	order := make([]BlockID, 0, n-1)
	for b := 1; b < n; b++ {
		order = append(order, BlockID(b)) //nolint:gosec // see newValue
	}
	slices.SortStableFunc(order, func(a, b BlockID) int {
		switch { // the entry stays first
		case a == 1:
			return -1
		case b == 1:
			return 1
		}
		return cmp.Compare(first[a], first[b])
	})
	newID := make([]BlockID, n)
	for i, b := range order {
		newID[b] = BlockID(i + 1) //nolint:gosec // see newValue
	}
	blocks := make([]Block, n)
	instrLists := make([][]InstrID, n)
	phiLists := make([][]InstrID, n)
	for _, old := range order {
		nb := newID[old]
		blk := f.Blocks[old]
		blk.ID = nb
		for i, s := range blk.Succs {
			blk.Succs[i] = newID[s]
		}
		for i, p := range blk.Preds {
			blk.Preds[i] = newID[p]
		}
		if blk.Handler != NoBlock {
			blk.Handler = newID[blk.Handler]
		}
		blocks[nb] = blk
		instrLists[nb] = fb.blkInstr[old]
		phiLists[nb] = fb.blkPhis[old]
	}
	for i := 1; i < len(f.Instrs); i++ {
		f.Instrs[i].Block = newID[f.Instrs[i].Block]
	}
	f.Blocks, fb.blkInstr, fb.blkPhis = blocks, instrLists, phiLists
}

// compact drops removed phis, renumbers values and instructions densely in
// block order, rewrites arguments, and gives each block's instruction and
// edge lists one shared backing array.
func (fb *fnBuilder) compact(repl []ValueID) {
	f := fb.f
	resolve := func(v ValueID) ValueID {
		if v != NoValue && repl[v] != NoValue {
			return repl[v]
		}
		return v
	}
	// New value numbering: params, this, free variables and the other
	// function-level values first, then instruction results in block order,
	// then globals, intrinsics and undefined (which print by name).
	newVal := make([]ValueID, len(f.Values))
	values := []Value{{}}
	keep := func(v ValueID) {
		if v == NoValue || newVal[v] != NoValue || repl[v] != NoValue {
			return
		}
		val := f.Values[v]
		val.ID = ValueID(len(values)) //nolint:gosec // see newValue
		newVal[v] = val.ID
		values = append(values, val)
	}
	for _, v := range f.Params {
		keep(v)
	}
	keepKinds := func(kinds ...ValueKind) {
		for _, kind := range kinds {
			for v := 1; v < len(f.Values); v++ {
				if f.Values[v].Kind == kind {
					keep(ValueID(v)) //nolint:gosec // see newValue
				}
			}
		}
	}
	keepKinds(ValueThis, ValueFreeVar, ValueArguments, ValueNewTarget, ValueSelf, ValueParam)
	fb.renumberBlocks()
	newInstr := make([]InstrID, len(f.Instrs))
	instrs := []Instr{{}}
	order := make([]InstrID, 0, len(f.Instrs))
	args := make([]ValueID, 0, len(f.args))
	blockStart := make([]int, len(f.Blocks)+1)
	for b := 1; b < len(f.Blocks); b++ {
		blockStart[b] = len(order)
		for _, list := range [2][]InstrID{fb.blkPhis[b], fb.blkInstr[b]} {
			for _, id := range list {
				in := f.Instrs[id]
				if in.Op == OpPhi && repl[in.Result] != NoValue {
					continue
				}
				keep(in.Result)
				in.ID = InstrID(len(instrs)) //nolint:gosec // see newValue
				newInstr[id] = in.ID
				order = append(order, in.ID)
				instrs = append(instrs, in)
			}
		}
	}
	blockStart[len(f.Blocks)] = len(order)
	keepKinds(ValueGlobal, ValueIntrinsic, ValueUndefined)
	for i := 1; i < len(instrs); i++ {
		in := &instrs[i]
		old := f.args[in.argOff : in.argOff+in.argLen]
		in.argOff = uint32(len(args)) //nolint:gosec // see newValue
		for _, a := range old {
			if a = resolve(a); a != NoValue {
				a = newVal[a]
			}
			args = append(args, a)
		}
		if in.Result != NoValue {
			in.Result = newVal[in.Result]
		}
	}
	for i := 1; i < len(values); i++ {
		if values[i].Kind == ValueInstr {
			values[i].Def = newInstr[values[i].Def]
		}
	}
	// Blocks: instruction lists and edges in one backing array each.
	edgeCount := 0
	for b := 1; b < len(f.Blocks); b++ {
		edgeCount += len(f.Blocks[b].Preds) + len(f.Blocks[b].Succs)
	}
	edges := make([]BlockID, 0, edgeCount)
	for b := 1; b < len(f.Blocks); b++ {
		blk := &f.Blocks[b]
		blk.Instrs = order[blockStart[b]:blockStart[b+1]:blockStart[b+1]]
		start := len(edges)
		edges = append(edges, blk.Preds...)
		blk.Preds = edges[start:len(edges):len(edges)]
		start = len(edges)
		edges = append(edges, blk.Succs...)
		blk.Succs = edges[start:len(edges):len(edges)]
	}
	remap := func(v ValueID) ValueID {
		if v = resolve(v); v != NoValue {
			return newVal[v]
		}
		return NoValue
	}
	for i := range f.Params {
		f.Params[i] = remap(f.Params[i])
	}
	f.This = remap(f.This)
	for i := range f.FreeVars {
		f.FreeVars[i].Value = remap(f.FreeVars[i].Value)
	}
	paths := f.paths[:0]
	for _, p := range f.paths {
		p.Value, p.Base = remap(p.Value), remap(p.Base)
		if p.Value != NoValue {
			paths = append(paths, p)
		}
	}
	slices.SortStableFunc(paths, func(a, b PathLink) int { return cmp.Compare(a.Value, b.Value) })
	f.paths = slices.CompactFunc(paths, func(a, b PathLink) bool { return a.Value == b.Value })
	for i := range f.cells {
		f.cells[i].value = remap(f.cells[i].value)
	}
	for name, v := range f.byName {
		f.byName[name] = remap(v)
	}
	if f == fb.b.mod.Init {
		for i := range fb.b.mod.Exports {
			fb.b.mod.Exports[i].Value = remap(fb.b.mod.Exports[i].Value)
		}
	}
	reqs := fb.b.typeReqs[:0]
	for _, r := range fb.b.typeReqs {
		if r.fn == f && r.v != NoValue {
			if repl[r.v] != NoValue {
				continue // a removed phi: its replacement has its own type
			}
			r.v = newVal[r.v]
		}
		reqs = append(reqs, r)
	}
	fb.b.typeReqs = reqs
	f.Values, f.Instrs, f.args, f.order, f.edges = values, instrs, args, order, edges
	f.Entry = 1
	fb.blkInstr, fb.blkPhis, fb.defs, fb.pending, fb.phiVar = nil, nil, nil, nil, nil
}
