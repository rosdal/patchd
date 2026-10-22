package ir

import (
	"github.com/patchd/patchd/engine/tsbridge"
)

// Functions, parameters, variables and closures. A local variable no other
// function refers to is a plain SSA value. A captured one lives in a cell:
// the declaring function allocates it (with a cell instruction where the
// binding is created: function entry, block entry, or each iteration of a
// for (let …) loop), reads and writes it with load/store *cell, and passes
// it to inner functions through their closure instruction, where it becomes
// one of their FreeVars. Module-level variables are globals.

// lowerBody lowers the whole function.
func (fb *fnBuilder) lowerBody() {
	f := fb.f
	entry := fb.newBlock("entry")
	fb.seal(entry)
	fb.cur = entry
	fb.setupEntry()
	t := fb.t
	switch f.Kind {
	case FuncModuleInit:
		fb.mark(fb.info.node)
		fb.lowerModuleBody()
	case FuncClassInit:
		fb.lowerClassInit()
	case FuncStaticInit:
		fb.lowerStaticInit()
	case FuncNamespace:
		fb.lowerNamespaceBody()
	default:
		if f.Flags&FuncAbstract != 0 {
			fb.mark(fb.info.node)
			fb.terminate(OpUnreachable, fb.info.node, nil)
			return
		}
		if f.Flags&FuncImplicit != 0 {
			fb.lowerImplicitConstructor()
			break
		}
		fb.mark(fb.info.node)
		fb.lowerParams()
		if f.Kind == FuncConstructor && f.Flags&FuncDerived == 0 {
			fb.constructorPrologue(fb.info.node)
		}
		body := fb.bodyOf(fb.info.node)
		switch {
		case body == 0:
		case t.Kind(body) == tsbridge.KindBlock:
			fb.lowerBlock(body)
		default: // an arrow's expression body
			v := fb.lowerExpr(body)
			fb.exitRegions(0)
			if fb.reachable() {
				fb.terminate(OpReturn, body, []ValueID{v})
			}
		}
	}
	if fb.reachable() {
		fb.terminate(OpReturn, 0, []ValueID{fb.undef})
	}
}

// bodyOf returns a function-like node's body: its Block, or an arrow's
// expression.
func (fb *fnBuilder) bodyOf(n tsbridge.NodeID) tsbridge.NodeID {
	t := fb.t
	for _, c := range t.Children(n) {
		if t.Kind(c) == tsbridge.KindBlock {
			return c
		}
	}
	if t.Kind(n) == tsbridge.KindArrowFunction {
		kids := t.Children(n)
		for i := len(kids) - 1; i >= 0; i-- {
			if isExpressionKind(t.Kind(kids[i])) {
				return kids[i]
			}
		}
	}
	return 0
}

// setupEntry creates the function-level values: free variables, this,
// arguments, new.target and the function's own name, then enters the
// function scope.
func (fb *fnBuilder) setupEntry() {
	fi, f := fb.info, fb.f
	for _, k := range fi.caps {
		name := freeVarKindNames[k.kind]
		if k.kind == FreeCell {
			name = fb.b.vars[k.v].name
		}
		v := fb.newValue(Value{Kind: ValueFreeVar, Name: name})
		if k.kind == FreeCell {
			fb.typeOf(v, fb.b.vars[k.v].decl)
		}
		f.FreeVars = append(f.FreeVars, FreeVar{Name: name, Kind: k.kind, Value: v})
		fb.free[k] = v
	}
	if !fi.arrow {
		f.This = fb.newValue(Value{Kind: ValueThis, Name: "this"})
		fb.this = f.This
		if ci := fi.class; ci != nil && f.Flags&FuncStatic == 0 && f.Kind != FuncStaticInit {
			fb.typeOf(f.This, ci.node)
		}
	} else {
		fb.this = fb.free[captureKey{kind: FreeThis}]
	}
	if fi.usesArgs {
		fb.args = fb.newValue(Value{Kind: ValueArguments, Name: "arguments"})
	}
	if fi.usesNT {
		fb.newTgt = fb.newValue(Value{Kind: ValueNewTarget, Name: "new.target"})
	}
	switch f.Kind {
	case FuncDeclaration, FuncArrow, FuncMethod, FuncGetter, FuncSetter, FuncConstructor:
		fb.b.typeReqs = append(fb.b.typeReqs, typeReq{fn: f, v: NoValue, node: fi.node})
	}
	fb.enterScope(fi.node)
	if fi.selfVar >= 0 {
		fb.self = fb.newValue(Value{Kind: ValueSelf, Name: fb.b.vars[fi.selfVar].name})
		fb.writeVar(fi.selfVar, fb.self, fi.node)
	}
	if fi.nsVar >= 0 {
		p := fb.newValue(Value{Kind: ValueParam, Name: fb.b.vars[fi.nsVar].name, Syntax: fi.node})
		f.Params = append(f.Params, p)
		fb.writeVar(fi.nsVar, p, fi.node)
	}
}

// enterScope allocates the cells of the captured variables a scope creates
// and instantiates the function declarations hoisted to it.
func (fb *fnBuilder) enterScope(n tsbridge.NodeID) {
	si := fb.b.scopes[n]
	if si == nil {
		return
	}
	me := fb.info
	for _, v := range si.vars {
		vi := &fb.b.vars[v]
		if !vi.captured || vi.global || fb.b.fns[vi.owner] != me {
			continue
		}
		fb.newCell(v, vi.decl)
	}
	for _, fn := range si.funcs {
		clo := fb.closureOf(fn)
		if name := fb.nameOf(fn); name != 0 {
			fb.mark(name)
			if v := fb.b.idVar[name]; v > 0 {
				fb.writeVar(v-1, clo, fn)
				fb.exportDecl(v-1, clo)
			}
		} else {
			fb.exportAnonymousDefault(fn, clo)
		}
	}
}

// newCell allocates a fresh cell for v.
func (fb *fnBuilder) newCell(v int32, n tsbridge.NodeID) ValueID {
	c := fb.emit(OpCell, n, Aux{Name: Intern(fb.b.vars[v].name)})
	fb.f.cells = append(fb.f.cells, cellVar{value: c, name: fb.b.vars[v].name})
	fb.writeVariable(v, fb.cur, c)
	return c
}

// nameOf returns the name identifier of a declaration, or 0.
func (fb *fnBuilder) nameOf(n tsbridge.NodeID) tsbridge.NodeID {
	for _, c := range fb.t.Children(n) {
		switch fb.t.Kind(c) {
		case tsbridge.KindIdentifier:
			return c
		case tsbridge.KindParameter, tsbridge.KindBlock:
			return 0
		}
	}
	return 0
}

// cellOf returns the cell holding captured variable v in this function.
func (fb *fnBuilder) cellOf(v int32, n tsbridge.NodeID) ValueID {
	vi := &fb.b.vars[v]
	if fb.b.fns[vi.owner] == fb.info {
		fb.ensureBlock()
		c := fb.readVariable(v, fb.cur)
		if c == fb.undef {
			c = fb.newCell(v, n) // the scope that creates it was not lowered
		}
		return c
	}
	if c, ok := fb.free[captureKey{kind: FreeCell, v: v}]; ok {
		return c
	}
	return fb.newCell(v, n)
}

// readVar reads variable v.
func (fb *fnBuilder) readVar(v int32, n tsbridge.NodeID) ValueID {
	vi := &fb.b.vars[v]
	switch {
	case vi.global:
		return fb.loadGlobal(n, vi.name)
	case vi.nsMember > 0:
		ns := fb.readVar(vi.nsMember-1, n)
		r := fb.emitTyped(OpLoad, n, Aux{Name: Intern(vi.name)}, ns)
		fb.nameValue(r, vi.name)
		return r
	case vi.captured:
		c := fb.cellOf(v, n)
		r := fb.emitTyped(OpLoad, n, Aux{Flags: AuxCell}, c)
		fb.nameValue(r, vi.name)
		return r
	}
	fb.ensureBlock()
	return fb.readVariable(v, fb.cur)
}

// writeVar writes variable v.
func (fb *fnBuilder) writeVar(v int32, val ValueID, n tsbridge.NodeID) {
	vi := &fb.b.vars[v]
	switch {
	case vi.global:
		fb.storeGlobal(n, vi.name, val)
	case vi.nsMember > 0:
		ns := fb.readVar(vi.nsMember-1, n)
		fb.emit(OpStore, n, Aux{Name: Intern(vi.name)}, ns, val)
	case vi.captured:
		c := fb.cellOf(v, n)
		fb.emit(OpStore, n, Aux{Flags: AuxCell}, c, val)
	default:
		fb.ensureBlock()
		fb.writeVariable(v, fb.cur, fb.named(val, vi.name, n))
	}
}

// named gives val a variable's name for traces: an unnamed instruction
// result takes the name; a value already named something else is copied
// with an assign so both names show up.
func (fb *fnBuilder) named(val ValueID, name string, n tsbridge.NodeID) ValueID {
	v := fb.f.Value(val)
	if v == nil || name == "" {
		return val
	}
	switch v.Kind {
	case ValueInstr:
		if v.Name == "" {
			v.Name = name
			return val
		}
	case ValueParam, ValueThis, ValueFreeVar, ValueArguments, ValueSelf:
	default:
		return val
	}
	if v.Name == name {
		return val
	}
	a := fb.emit(OpAssign, n, Aux{}, val)
	fb.f.Values[a].Name = name
	fb.f.Values[a].Type = v.Type
	if v.Kind == ValueInstr {
		if r := fb.lastTypeReq(val); r != 0 {
			fb.typeOf(a, r)
		}
	}
	return a
}

// lastTypeReq returns the node a value's type will come from, if any.
func (fb *fnBuilder) lastTypeReq(v ValueID) tsbridge.NodeID {
	if val := fb.f.Value(v); val != nil {
		return val.Syntax
	}
	return 0
}

// thisValue returns this in this function: its own, or the captured one.
func (fb *fnBuilder) thisValue() ValueID {
	if fb.this != NoValue {
		return fb.this
	}
	return fb.undef
}

func (fb *fnBuilder) argumentsValue() ValueID {
	if fb.args != NoValue {
		return fb.args
	}
	if v, ok := fb.free[captureKey{kind: FreeArguments}]; ok {
		return v
	}
	return fb.undef
}

func (fb *fnBuilder) newTargetValue() ValueID {
	if fb.newTgt != NoValue {
		return fb.newTgt
	}
	if v, ok := fb.free[captureKey{kind: FreeNewTarget}]; ok {
		return v
	}
	return fb.undef
}

// captureValue is this function's value for something an inner function captures.
func (fb *fnBuilder) captureValue(k captureKey, n tsbridge.NodeID) ValueID {
	switch k.kind {
	case FreeCell:
		return fb.cellOf(k.v, n)
	case FreeThis:
		return fb.thisValue()
	case FreeArguments:
		return fb.argumentsValue()
	case FreeNewTarget:
		return fb.newTargetValue()
	}
	return fb.undef
}

// closureOf creates the function value of a function-like node.
func (fb *fnBuilder) closureOf(n tsbridge.NodeID) ValueID {
	idx, ok := fb.b.fnOf[n]
	if !ok {
		return fb.emitUnknown(n)
	}
	return fb.closureOfFn(idx, n, 0)
}

// closureOfFn creates the function value of fns[idx].
func (fb *fnBuilder) closureOfFn(idx int, n tsbridge.NodeID, flags AuxFlags) ValueID {
	fi := fb.b.fns[idx]
	args := make([]ValueID, len(fi.caps))
	for i, k := range fi.caps {
		args[i] = fb.captureValue(k, n)
	}
	switch fi.f.Kind {
	case FuncGetter:
		flags |= AuxGetter
	case FuncSetter:
		flags |= AuxSetter
	}
	fb.markFunc(n)
	v := fb.emitArgs(OpClosure, n, Aux{Func: fi.f.ID, Flags: flags}, args)
	if fi.f.Kind != FuncClassInit && fi.f.Kind != FuncStaticInit && fi.f.Kind != FuncNamespace && flags&AuxClass == 0 {
		fb.typeOf(v, n)
	}
	return v
}

// lowerParams binds the parameters: defaults become an explicit
// `p === undefined` branch at entry, rest parameters are flagged, and
// destructured parameters are unpacked.
func (fb *fnBuilder) lowerParams() {
	t := fb.t
	for _, p := range t.Children(fb.info.node) {
		if t.Kind(p) != tsbridge.KindParameter {
			continue
		}
		fb.mark(p)
		var name, init tsbridge.NodeID
		rest := false
		for _, c := range t.Children(p) {
			switch k := t.Kind(c); {
			case k == tsbridge.KindToken && t.Operator(c) == "...":
				rest = true
			case k == tsbridge.KindIdentifier || k == tsbridge.KindObjectBindingPattern || k == tsbridge.KindArrayBindingPattern:
				if name == 0 {
					name = c
				}
			case k == tsbridge.KindDecorator, k == tsbridge.KindToken, k == tsbridge.KindModifier, k == tsbridge.KindTypeNode, k == tsbridge.KindJSDoc:
			default:
				init = c
			}
		}
		pname := ""
		if t.Kind(name) == tsbridge.KindIdentifier {
			pname = t.Value(name)
			if pname == "this" {
				continue // a this-parameter: a type annotation only
			}
		}
		pv := fb.newValue(Value{Kind: ValueParam, Name: pname, Syntax: p})
		fb.typeOf(pv, p)
		fb.f.Params = append(fb.f.Params, pv)
		if rest {
			fb.f.Flags |= FuncRest
		}
		val := pv
		if init != 0 {
			val = fb.withDefault(pv, init, p)
		}
		fb.bindTarget(name, val, pv, "")
	}
}

// withDefault returns v, or the value of init when v is undefined: an
// explicit branch, so dataflow sees both.
func (fb *fnBuilder) withDefault(v ValueID, init, n tsbridge.NodeID) ValueID {
	isUndef := fb.emit(OpBinOp, n, Aux{Name: Intern("===")}, v, fb.undef)
	from := fb.cur
	def := fb.newBlock("default")
	join := fb.newBlock("default.done")
	fb.branch(n, isUndef, def, join)
	fb.seal(def)
	fb.cur = def
	dv := fb.lowerExpr(init)
	defEnd := fb.cur
	fb.jump(join)
	fb.seal(join)
	fb.cur = join
	return fb.merge(n, join, func(p BlockID) ValueID {
		if p == defEnd && p != from {
			return dv
		}
		return v
	})
}

// merge builds a phi for an expression value at join from each predecessor.
// With one predecessor no phi is needed.
func (fb *fnBuilder) merge(n tsbridge.NodeID, join BlockID, value func(pred BlockID) ValueID) ValueID {
	preds := fb.f.Blocks[join].Preds
	switch len(preds) {
	case 0:
		return fb.undef
	case 1:
		return value(preds[0])
	}
	ops := make([]ValueID, len(preds))
	same := true
	for i, p := range preds {
		ops[i] = value(p)
		if ops[i] != ops[0] {
			same = false
		}
	}
	if same {
		return ops[0]
	}
	return fb.phiOf(n, ops)
}

// constructorPrologue runs at the start of a base class's constructor, and
// after super() in a derived one: parameter properties, then field
// initializers.
func (fb *fnBuilder) constructorPrologue(at tsbridge.NodeID) {
	ci := fb.info.class
	if ci == nil {
		return
	}
	t := fb.t
	if fb.f.Flags&FuncImplicit == 0 {
		i := 0
		for _, p := range t.Children(fb.info.node) {
			if t.Kind(p) != tsbridge.KindParameter {
				continue
			}
			pv := NoValue
			if i < len(fb.f.Params) {
				pv = fb.f.Params[i]
			}
			i++
			if t.Flags(p)&(tsbridge.FlagAccessibility|tsbridge.FlagReadonly|tsbridge.FlagOverride) == 0 || pv == NoValue {
				continue
			}
			name := fb.nameOf(p)
			if name == 0 {
				continue
			}
			fb.emit(OpStore, p, Aux{Name: Intern(t.Value(name))}, fb.thisValue(), pv)
		}
	}
	if ci.init >= 0 {
		init := fb.closureOfFn(ci.init, at, 0)
		fb.emit(OpCall, at, Aux{Name: Intern("<init>"), Flags: AuxWithThis}, init, fb.thisValue())
	}
}

// lowerImplicitConstructor lowers the constructor of a class that doesn't
// write one: super(...args) for a derived class, then field initializers.
func (fb *fnBuilder) lowerImplicitConstructor() {
	n := fb.info.node
	if fb.f.Flags&FuncDerived != 0 {
		args := fb.newValue(Value{Kind: ValueParam, Name: "args"})
		fb.f.Params = append(fb.f.Params, args)
		fb.f.Flags |= FuncRest
		s := fb.emit(OpSpread, n, Aux{}, args)
		fb.emit(OpCall, n, Aux{Flags: AuxSuperCall}, fb.thisValue(), s)
	}
	fb.constructorPrologue(n)
}
