// Intraprocedural taint propagation.

package taint

import (
	"slices"
	"strings"
	"sync"

	"github.com/patchd/patchd/engine/dataflow"
	"github.com/patchd/patchd/engine/ir"
	"github.com/patchd/patchd/engine/tsbridge"
)

// Config tunes an Analyzer.
type Config struct {
	Spec *Spec // sources, sinks, sanitizers and propagators; required
	// IncludeEnvironment turns on environment sources (process.argv,
	// process.env), which are off by default so CLI tools don't flood
	// results.
	IncludeEnvironment bool
	// MaxPasses caps the solver per function (0: dataflow.DefaultMaxPasses).
	MaxPasses int
}

// Analyzer runs intraprocedural taint analysis on the functions of one
// program. Safe for concurrent use: functions are independent in v0.4.
type Analyzer struct {
	prog  *tsbridge.Program
	cfg   Config
	res   *resolver
	cache *dataflow.Cache

	callNames   map[string]bool // last segments of every call matcher: calls with other names are never resolved
	callSources map[string]bool // last segments of call sources
	paramTypes  bool            // the spec has parameter sources

	numMu   sync.Mutex
	numeric map[tsbridge.TypeID]bool
}

// numericConversions are the calls whose result is a number or boolean
// whatever their argument, for code without types (ADR 0010: type pruning).
var numericConversions = map[string]bool{
	"global:Number": true, "global:parseInt": true, "global:parseFloat": true, "global:Boolean": true,
	"global:Number.parseInt": true, "global:Number.parseFloat": true, "global:Math.floor": true,
	"global:Math.round": true, "global:Math.ceil": true, "global:Math.trunc": true, "global:Math.abs": true,
}

// NewAnalyzer returns an analyzer for prog. cfg.Spec is indexed here.
func NewAnalyzer(prog *tsbridge.Program, cfg Config) *Analyzer {
	cfg.Spec.Index()
	a := &Analyzer{
		prog:        prog,
		cfg:         cfg,
		res:         newResolver(prog),
		cache:       dataflow.NewCache(),
		callNames:   map[string]bool{},
		callSources: map[string]bool{},
		numeric:     map[tsbridge.TypeID]bool{},
	}
	add := func(m Matcher) {
		for _, n := range m.Names() {
			a.callNames[lastSegment(n)] = true
		}
	}
	for _, s := range cfg.Spec.Sources {
		switch s.Match.Access {
		case AccessCall:
			add(s.Match)
			for _, n := range s.Match.Names() {
				a.callSources[lastSegment(n)] = true
			}
		case AccessParameter:
			a.paramTypes = true
		}
	}
	for _, s := range cfg.Spec.Sinks {
		add(s.Match)
	}
	for _, s := range cfg.Spec.Sanitizers {
		add(s.Match)
	}
	for _, s := range cfg.Spec.Propagators {
		add(s.Match)
	}
	for n := range numericConversions {
		a.callNames[lastSegment(n)] = true
	}
	return a
}

// Origin is one source occurrence in a function.
type Origin struct {
	Value         ir.ValueID // the value the source produces
	Instr         ir.InstrID // the instruction that produces it; NoInstr for a parameter
	Source        *Source
	QualifiedName string // what the code resolved to
	Kind          Kind   // origin kind: user-input or environment
	Heuristic     bool   // matched through the untyped-handler heuristic (medium confidence)
}

// Candidate is a sink argument that carries a label able to reach the
// sink's kind: a finding before rule refinements.
type Candidate struct {
	Sink  *Sink
	Call  ir.InstrID // the sink call
	Arg   int        // which argument (0-based, receiver excluded)
	Value ir.ValueID // the argument
	Label Label      // the label that reaches it
}

// FuncResult is the taint analysis of one function.
type FuncResult struct {
	Module     *ir.Module
	Func       *ir.Func
	Origins    []Origin
	Facts      []Fact // by ValueID
	Candidates []Candidate
	Solver     dataflow.Result // Capped means the facts may be below the fixed point
	Skipped    bool            // no source could occur in the function: not analysed

	edges []edge
	into  [][]int32 // by ValueID: edges into the value
	calls map[ir.InstrID]string
}

// edge moves taint from one value to another at an instruction.
type edge struct {
	from, to ir.ValueID
	instr    ir.InstrID
	clear    KindSet // kinds a sanitizer removes
	numeric  bool    // the result is a number or boolean whatever its type says
}

// CallName returns the qualified name a call of the function resolved to,
// for calls the analysis resolved (those whose name a model could match).
func (r *FuncResult) CallName(call ir.InstrID) string { return r.calls[call] }

// Origin returns the origin of a label.
func (r *FuncResult) Origin(l Label) *Origin {
	if l.Origin < 0 || int(l.Origin) >= len(r.Origins) {
		return nil
	}
	return &r.Origins[l.Origin]
}

// MayHaveSource reports whether a source could occur in f: a read of a
// property some property source ends in, a call some call source ends in,
// or a parameter (when the spec has parameter sources). Functions without
// one can't produce a finding in v0.4 and are skipped.
func (a *Analyzer) MayHaveSource(f *ir.Func) bool {
	if a.paramTypes && len(f.Params) > 0 {
		return true
	}
	for i := 1; i < len(f.Instrs); i++ {
		in := &f.Instrs[i]
		switch in.Op {
		case ir.OpLoad:
			if in.Aux.Flags&(ir.AuxGlobal|ir.AuxCell|ir.AuxDynamic) == 0 && a.cfg.Spec.IsSourceMember(in.Aux.Name.String()) {
				return true
			}
		case ir.OpCall, ir.OpNew:
			if a.callSources[in.Aux.Name.String()] {
				return true
			}
		}
	}
	return false
}

// Func analyses one function of module m.
func (a *Analyzer) Func(m *ir.Module, f *ir.Func) *FuncResult {
	r := &FuncResult{Module: m, Func: f, calls: map[ir.InstrID]string{}}
	if !a.MayHaveSource(f) {
		r.Skipped = true
		return r
	}
	b := &builder{a: a, r: r, f: f, names: a.res.forFunc(m, f), init: map[ir.ValueID]Fact{}}
	b.build()
	r.into = make([][]int32, len(f.Values))
	byInstr := map[ir.InstrID][]int32{}
	for i, e := range r.edges {
		r.into[e.to] = append(r.into[e.to], int32(i)) //nolint:gosec // edges per function fit
		byInstr[e.instr] = append(byInstr[e.instr], int32(i))
	}
	lat := Lattice{}
	transfer := func(s *dataflow.Sparse[Fact], in *ir.Instr) {
		for _, ei := range byInstr[in.ID] {
			e := &r.edges[ei]
			if fact := a.apply(f, e, s.Get(e.from)); len(fact) > 0 {
				s.Add(e.to, fact)
			}
		}
	}
	du := a.cache.DefUse(f)
	sparse, res := dataflow.SolveSparse(f, lat, du, transfer, dataflow.SparseOptions[Fact]{
		Options: dataflow.Options{MaxPasses: a.cfg.MaxPasses},
		Init: func(v *ir.Value) (Fact, bool) {
			fact, ok := b.init[v.ID]
			return fact, ok
		},
		Extra: func(v ir.ValueID) []ir.InstrID { return b.extra[v] },
	})
	r.Facts = sparse.Facts()
	r.Solver = res
	for _, s := range b.sinks {
		for _, i := range s.sink.Args {
			if i >= len(s.args) {
				continue
			}
			v := s.args[i]
			if val := f.Value(v); val == nil || isSpread(f, v) {
				continue
			}
			for _, l := range r.Facts[v] {
				if l.Kinds.Has(s.sink.Kind) {
					r.Candidates = append(r.Candidates, Candidate{Sink: s.sink, Call: s.call, Arg: i, Value: v, Label: l})
				}
			}
		}
	}
	a.cache.Forget(f)
	return r
}

// apply moves a fact along an edge: sanitizers clear their kinds, and a
// numeric result loses the kinds a number can't inject into.
func (a *Analyzer) apply(f *ir.Func, e *edge, fact Fact) Fact {
	if len(fact) == 0 {
		return nil
	}
	numeric := e.numeric || a.isNumeric(f, e.to)
	if e.clear == 0 && !numeric {
		return fact
	}
	return fact.Map(func(l Label) (Label, bool) {
		l.Kinds &^= e.clear
		if numeric {
			l.Kinds &^= NumericPruned
			l.Numeric = true
		}
		return l, l.Kinds != 0
	})
}

// isNumeric reports a value typed number, boolean or bigint, or a union of
// only those (and null/undefined).
func (a *Analyzer) isNumeric(f *ir.Func, v ir.ValueID) bool {
	val := f.Value(v)
	if val == nil || val.Type == 0 {
		return false
	}
	a.numMu.Lock()
	n, ok := a.numeric[val.Type]
	a.numMu.Unlock()
	if ok {
		return n
	}
	n = a.numericType(val.Type)
	a.numMu.Lock()
	a.numeric[val.Type] = n
	a.numMu.Unlock()
	return n
}

func (a *Analyzer) numericType(t tsbridge.TypeID) bool {
	const num = tsbridge.TypeNumber | tsbridge.TypeBoolean | tsbridge.TypeBigInt
	const nullish = tsbridge.TypeNull | tsbridge.TypeUndefined | tsbridge.TypeVoid
	flags := a.prog.TypeFlags(t)
	if flags&(tsbridge.TypeAny|tsbridge.TypeUnknown|tsbridge.TypeString|tsbridge.TypeObject|tsbridge.TypeTypeParameter) != 0 {
		return false
	}
	if flags&tsbridge.TypeUnion == 0 {
		return flags&num != 0
	}
	sawNum := false
	for _, m := range a.prog.UnionMembers(t) {
		mf := a.prog.TypeFlags(m)
		switch {
		case mf&num != 0 && mf&(tsbridge.TypeString|tsbridge.TypeObject) == 0:
			sawNum = true
		case mf&nullish != 0 && mf&^(nullish|tsbridge.TypeLiteral) == 0:
		default:
			return false
		}
	}
	return sawNum
}

func isSpread(f *ir.Func, v ir.ValueID) bool {
	val := f.Value(v)
	if val == nil || val.Kind != ir.ValueInstr {
		return false
	}
	in := f.Instr(val.Def)
	return in != nil && in.Op == ir.OpSpread
}

// builder turns one function's instructions into taint edges, origins and
// sink sites.
type builder struct {
	a     *Analyzer
	r     *FuncResult
	f     *ir.Func
	names *funcNames
	init  map[ir.ValueID]Fact
	extra map[ir.ValueID][]ir.InstrID
	sinks []sinkSite
}

type sinkSite struct {
	sink *Sink
	call ir.InstrID
	args []ir.ValueID // arguments, receiver excluded
}

func (b *builder) edge(from, to ir.ValueID, instr ir.InstrID) {
	if from == ir.NoValue || to == ir.NoValue || from == to {
		return
	}
	b.r.edges = append(b.r.edges, edge{from: from, to: to, instr: instr})
}

// gen records a source occurrence producing v.
func (b *builder) gen(v ir.ValueID, instr ir.InstrID, src *Source, qn string, heuristic bool) {
	kind := src.Origin()
	if kind == KindEnvironment && !b.a.cfg.IncludeEnvironment {
		return
	}
	for _, o := range b.r.Origins {
		if o.Value == v && o.Source == src {
			return
		}
	}
	idx := int32(len(b.r.Origins)) //nolint:gosec // origins per function fit
	b.r.Origins = append(b.r.Origins, Origin{Value: v, Instr: instr, Source: src, QualifiedName: qn, Kind: kind, Heuristic: heuristic})
	l := Label{Origin: idx, Kinds: AllSinkKinds}
	if b.a.isNumeric(b.f, v) {
		l.Kinds &^= NumericPruned
		l.Numeric = true
	}
	b.init[v] = Lattice{}.Join(b.init[v], Fact{l})
}

func (b *builder) build() {
	f := b.f
	b.extra = map[ir.ValueID][]ir.InstrID{}
	rs := b.a.cache.Reaching(f)
	if b.a.paramTypes {
		for _, p := range f.Params {
			if v := f.Value(p); v != nil && v.Type != 0 {
				if qn := b.a.prog.TypeQualifiedName(v.Type); qn != "" {
					for _, src := range b.a.cfg.Spec.SourcesFor(qn, AccessParameter) {
						b.gen(p, ir.NoInstr, src, qn, false)
					}
				}
			}
		}
	}
	for _, blk := range dataflow.RPO(f) {
		for _, id := range f.Blocks[blk].Instrs {
			b.instr(f.Instr(id), rs)
		}
	}
}

func (b *builder) instr(in *ir.Instr, rs *dataflow.ReachingStores) {
	f := b.f
	args := f.Args(in)
	res := in.Result
	switch in.Op {
	case ir.OpAssign, ir.OpSpread, ir.OpAwait:
		b.edge(args[0], res, in.ID)
	case ir.OpPhi, ir.OpTemplate, ir.OpObject, ir.OpArray, ir.OpUnknown:
		for _, a := range args {
			b.edge(a, res, in.ID)
		}
	case ir.OpBinOp:
		if in.Aux.Name.String() == "+" {
			for _, a := range args {
				b.edge(a, res, in.ID)
			}
		}
	case ir.OpLoad:
		b.load(in, args, rs)
	case ir.OpStore:
		if in.Aux.Flags&(ir.AuxGlobal|ir.AuxCell|ir.AuxProto|ir.AuxMethod|ir.AuxGetter|ir.AuxSetter|ir.AuxSuper) == 0 && len(args) >= 2 {
			// Field-insensitive weak update: the object takes the stored
			// value's taint.
			b.edge(args[len(args)-1], args[0], in.ID)
		}
	case ir.OpCall, ir.OpNew:
		b.call(in)
	}
}

func (b *builder) load(in *ir.Instr, args []ir.ValueID, rs *dataflow.ReachingStores) {
	f, res := b.f, in.Result
	switch {
	case in.Aux.Flags.Has(ir.AuxGlobal) || in.Aux.Flags.Has(ir.AuxCell):
		for _, st := range rs.StoresFor(in.ID) {
			sargs := f.Args(f.Instr(st))
			val := sargs[len(sargs)-1]
			b.edge(val, res, in.ID)
			b.extra[val] = append(b.extra[val], in.ID)
		}
	case in.Aux.Flags.Has(ir.AuxSuper):
	case in.Aux.Flags.Has(ir.AuxDynamic):
		// Only the object's taint: a tainted key chooses among the object's
		// values but doesn't write them, so COMMANDS[req.query.op] is as
		// trusted as COMMANDS (ADR 0010).
		if len(args) > 0 {
			b.edge(args[0], res, in.ID)
		}
	default:
		if len(args) > 0 {
			b.edge(args[0], res, in.ID)
		}
		if in.Aux.Flags&(ir.AuxIterKey|ir.AuxIterValue|ir.AuxRest) == 0 && b.a.cfg.Spec.IsSourceMember(in.Aux.Name.String()) {
			if n, ok := b.names.property(in); ok {
				for _, src := range b.a.cfg.Spec.SourcesFor(n.qn, AccessProperty) {
					b.gen(res, in.ID, src, n.qn, n.heuristic)
				}
			}
		}
	}
}

// callLayout splits a call's arguments into the receiver (or NoValue) and
// the arguments proper.
func callLayout(f *ir.Func, in *ir.Instr) (recv ir.ValueID, args []ir.ValueID) {
	all := f.Args(in)
	fl := in.Aux.Flags
	switch {
	case len(all) == 0:
		return ir.NoValue, nil
	case in.Op == ir.OpNew:
		return ir.NoValue, all[1:]
	case fl.Has(ir.AuxMethod) && fl.Has(ir.AuxDynamic):
		if len(all) < 2 {
			return all[0], nil
		}
		return all[0], all[2:]
	case fl.Has(ir.AuxMethod), fl.Has(ir.AuxSuperCall):
		return all[0], all[1:]
	case fl.Has(ir.AuxWithThis):
		if len(all) < 2 {
			return ir.NoValue, nil
		}
		return all[1], all[2:]
	default:
		return ir.NoValue, all[1:]
	}
}

func (b *builder) call(in *ir.Instr) {
	f, res := b.f, in.Result
	recv, args := callLayout(f, in)
	var qn string
	heuristic := false
	if !in.Aux.Flags.Has(ir.AuxRequire) && !in.Aux.Flags.Has(ir.AuxImport) && b.a.callNames[in.Aux.Name.String()] {
		if n, ok := b.names.call(in); ok {
			qn, heuristic = n.qn, n.heuristic
			b.r.calls[in.ID] = qn
		}
	}
	spec := b.a.cfg.Spec
	if qn != "" {
		for _, src := range spec.SourcesFor(qn, AccessCall) {
			b.gen(res, in.ID, src, qn, heuristic)
		}
		for _, sk := range spec.SinksFor(qn) {
			b.sinks = append(b.sinks, sinkSite{sink: sk, call: in.ID, args: args})
		}
		if props := spec.PropagatorsFor(qn); len(props) > 0 {
			for _, p := range props {
				for _, from := range p.From {
					for _, fv := range slotValues(from, recv, args, res) {
						for _, to := range p.To {
							for _, tv := range slotValues(to, recv, args, res) {
								b.edge(fv, tv, in.ID)
							}
						}
					}
				}
			}
			return
		}
		if sans := spec.SanitizersFor(qn); len(sans) > 0 {
			var clears KindSet
			for _, s := range sans {
				clears |= KindSetOf(s.Clears...)
			}
			for _, a := range append([]ir.ValueID{recv}, args...) {
				if a != ir.NoValue {
					b.r.edges = append(b.r.edges, edge{from: a, to: res, instr: in.ID, clear: clears})
				}
			}
			return
		}
		if numericConversions[qn] {
			for _, a := range args {
				b.r.edges = append(b.r.edges, edge{from: a, to: res, instr: in.ID, numeric: true})
			}
			return
		}
	}
	// Code in the repo is left to v0.5's function summaries: passing a value
	// through a local helper is a flow across functions, outside v0.4's
	// scope, and the helper may well validate it (ADR 0010).
	if qn == "" && b.a.isRepoCall(in) {
		return
	}
	// Default: the result carries the receiver's and the arguments' taint.
	b.edge(recv, res, in.ID)
	for _, a := range args {
		b.edge(a, res, in.ID)
	}
}

// isRepoCall reports a call that resolves to a function declared in the
// analysed repo (a local: qualified name).
func (a *Analyzer) isRepoCall(in *ir.Instr) bool {
	if in.Aux.Flags&(ir.AuxRequire|ir.AuxImport|ir.AuxJSX|ir.AuxDecorator|ir.AuxSuperCall) != 0 {
		return false
	}
	c, ok := a.prog.ResolveCall(in.Syntax)
	return ok && strings.HasPrefix(c.QualifiedName, "local:")
}

// slotValues returns the values a slot names at one call.
func slotValues(s Slot, recv ir.ValueID, args []ir.ValueID, res ir.ValueID) []ir.ValueID {
	switch s {
	case SlotReturn:
		return []ir.ValueID{res}
	case SlotReceiver:
		if recv != ir.NoValue {
			return []ir.ValueID{recv}
		}
		return nil
	case SlotArgs:
		return slices.DeleteFunc(slices.Clone(args), func(v ir.ValueID) bool { return v == ir.NoValue })
	}
	if i, ok := s.ArgIndex(); ok && i < len(args) {
		return []ir.ValueID{args[i]}
	}
	return nil
}

// Layout returns a call's receiver and arguments, receiver excluded: what
// a Sink's Args index into.
func Layout(f *ir.Func, call *ir.Instr) (recv ir.ValueID, args []ir.ValueID) {
	return callLayout(f, call)
}

// IsHTTPRequestHandler reports whether a function looks like an untyped
// (req, res) handler, the shape the untyped-handler heuristic accepts.
func IsHTTPRequestHandler(prog *tsbridge.Program, f *ir.Func) bool {
	return len(f.Params) >= 2 && untypedHandlerParam(prog, f, f.Params[0]) != ""
}
