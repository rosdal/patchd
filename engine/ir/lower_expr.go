package ir

import (
	"strings"

	"github.com/patchd/patchd/engine/tsbridge"
)

// Expressions produce values. Anything with short-circuit behaviour (&&, ||,
// ??, ?:, ?., logical assignment, default values) becomes control flow with
// a phi, so dataflow sees both paths.

func (fb *fnBuilder) lowerExpr(n tsbridge.NodeID) ValueID {
	t := fb.t
	if n == 0 {
		return fb.undef
	}
	fb.mark(n)
	switch k := t.Kind(n); k {
	case tsbridge.KindIdentifier:
		return fb.readIdent(n)
	case tsbridge.KindPrivateIdentifier:
		return fb.constant(n, LitString, t.Value(n))
	case tsbridge.KindNumericLiteral:
		return fb.constant(n, LitNumber, t.Value(n))
	case tsbridge.KindBigIntLiteral:
		return fb.constant(n, LitBigInt, t.Value(n))
	case tsbridge.KindStringLiteral, tsbridge.KindNoSubstitutionTemplateLiteral:
		return fb.constant(n, LitString, t.Value(n))
	case tsbridge.KindRegularExpressionLiteral:
		return fb.constant(n, LitRegex, t.Value(n))
	case tsbridge.KindTrue:
		return fb.constant(n, LitBool, "true")
	case tsbridge.KindFalse:
		return fb.constant(n, LitBool, "false")
	case tsbridge.KindNull:
		return fb.constant(n, LitNull, "null")
	case tsbridge.KindThis:
		return fb.thisValue()
	case tsbridge.KindSuper:
		return fb.thisValue()
	case tsbridge.KindTemplateExpression:
		return fb.lowerTemplate(n)
	case tsbridge.KindTaggedTemplateExpression:
		if fb.chainRoot(n) {
			return fb.lowerChain(n)
		}
		return fb.lowerTagged(n)
	case tsbridge.KindArrayLiteralExpression:
		return fb.lowerArray(n)
	case tsbridge.KindObjectLiteralExpression:
		return fb.lowerObject(n)
	case tsbridge.KindPropertyAccessExpression, tsbridge.KindElementAccessExpression, tsbridge.KindCallExpression:
		if fb.chainRoot(n) {
			return fb.lowerChain(n)
		}
		return fb.lowerLink(n)
	case tsbridge.KindNonNullExpression:
		if fb.chainRoot(n) {
			return fb.lowerChain(n)
		}
		return fb.lowerExpr(fb.first(n))
	case tsbridge.KindNewExpression:
		return fb.lowerNew(n)
	case tsbridge.KindParenthesizedExpression, tsbridge.KindAsExpression, tsbridge.KindSatisfiesExpression,
		tsbridge.KindTypeAssertionExpression, tsbridge.KindExpressionWithTypeArguments:
		return fb.lowerExpr(fb.first(n))
	case tsbridge.KindFunctionExpression, tsbridge.KindArrowFunction:
		return fb.closureOf(n)
	case tsbridge.KindClassExpression:
		return fb.lowerClass(n)
	case tsbridge.KindDeleteExpression:
		return fb.lowerDelete(n)
	case tsbridge.KindTypeOfExpression:
		return fb.emitTyped(OpUnOp, n, Aux{Name: Intern("typeof")}, fb.lowerExpr(fb.first(n)))
	case tsbridge.KindVoidExpression:
		return fb.emitTyped(OpUnOp, n, Aux{Name: Intern("void")}, fb.lowerExpr(fb.first(n)))
	case tsbridge.KindAwaitExpression:
		return fb.emitTyped(OpAwait, n, Aux{}, fb.lowerExpr(fb.first(n)))
	case tsbridge.KindPrefixUnaryExpression:
		op := t.Operator(n)
		if op == "++" || op == "--" {
			return fb.lowerUpdate(n, fb.first(n), op, true)
		}
		return fb.emitTyped(OpUnOp, n, Aux{Name: Intern(op)}, fb.lowerExpr(fb.first(n)))
	case tsbridge.KindPostfixUnaryExpression:
		return fb.lowerUpdate(n, fb.first(n), t.Operator(n), false)
	case tsbridge.KindBinaryExpression:
		return fb.lowerBinary(n)
	case tsbridge.KindConditionalExpression:
		return fb.lowerConditional(n)
	case tsbridge.KindYieldExpression:
		v := fb.undef
		if e := fb.elems(n); len(e) > 0 {
			v = fb.lowerExpr(e[0])
		}
		var flags AuxFlags
		if t.Flags(n)&tsbridge.FlagGenerator != 0 {
			flags = AuxDelegate
		}
		return fb.emitTyped(OpYield, n, Aux{Flags: flags}, v)
	case tsbridge.KindSpreadElement:
		return fb.emitTyped(OpSpread, n, Aux{}, fb.lowerExpr(fb.first(n)))
	case tsbridge.KindOmittedExpression:
		return fb.undef
	case tsbridge.KindMetaProperty:
		if t.Value(n) == "new.target" {
			return fb.newTargetValue()
		}
		return fb.intrinsic("%" + t.Value(n))
	case tsbridge.KindImportKeyword:
		return fb.intrinsic("%import")
	case tsbridge.KindJsxElement, tsbridge.KindJsxSelfClosingElement, tsbridge.KindJsxFragment:
		return fb.lowerJSX(n)
	case tsbridge.KindJsxExpression:
		if e := fb.elems(n); len(e) > 0 {
			return fb.lowerExpr(e[0])
		}
		return fb.undef
	case tsbridge.KindJsxText:
		return fb.constant(n, LitString, t.Value(n))
	case tsbridge.KindOther:
		if t.CompilerKind(n) == "PartiallyEmittedExpression" {
			return fb.lowerExpr(fb.first(n))
		}
	}
	return fb.emitUnknown(n)
}

// first returns n's first runtime child.
func (fb *fnBuilder) first(n tsbridge.NodeID) tsbridge.NodeID {
	if e := fb.elems(n); len(e) > 0 {
		return e[0]
	}
	return 0
}

// unwrap strips syntax with no runtime effect: parentheses, as, satisfies,
// non-null !, <T>x.
func (fb *fnBuilder) unwrap(n tsbridge.NodeID) tsbridge.NodeID {
	for {
		switch fb.t.Kind(n) {
		case tsbridge.KindParenthesizedExpression, tsbridge.KindAsExpression, tsbridge.KindSatisfiesExpression,
			tsbridge.KindTypeAssertionExpression, tsbridge.KindNonNullExpression:
			if fb.t.Flags(n)&tsbridge.FlagOptionalChain != 0 {
				return n
			}
			fb.mark(n)
			inner := fb.first(n)
			if inner == 0 {
				return n
			}
			n = inner
		default:
			return n
		}
	}
}

// --- names ---

func (fb *fnBuilder) readIdent(n tsbridge.NodeID) ValueID {
	if v := fb.b.idVar[n]; v > 0 {
		return fb.readVar(v-1, n)
	}
	if fb.b.special[n] == FreeArguments {
		return fb.argumentsValue()
	}
	name := fb.t.Value(n)
	if v, ok := fb.enumMember(name); ok {
		return v
	}
	if name == "undefined" {
		return fb.constant(n, LitUndefined, "undefined")
	}
	return fb.loadGlobal(n, name)
}

// writeIdent assigns to a name.
func (fb *fnBuilder) writeIdent(n tsbridge.NodeID, v ValueID, at tsbridge.NodeID) {
	if vi := fb.b.idVar[n]; vi > 0 {
		fb.writeVar(vi-1, v, at)
		return
	}
	fb.storeGlobal(at, fb.t.Value(n), v)
}

// --- templates ---

func (fb *fnBuilder) lowerTemplate(n tsbridge.NodeID) ValueID {
	parts, args := fb.templateParts(n)
	return fb.emitTyped(OpTemplate, n, fb.withParts(Aux{}, parts), args...)
}

// templateParts returns a template literal's static strings (one more than
// its expressions) and the values of its expressions.
func (fb *fnBuilder) templateParts(n tsbridge.NodeID) ([]Part, []ValueID) {
	t := fb.t
	var parts []Part
	var args []ValueID
	if t.Kind(n) == tsbridge.KindNoSubstitutionTemplateLiteral {
		fb.mark(n)
		return []Part{{Kind: PartString, Name: Intern(t.Value(n))}}, nil
	}
	for _, c := range t.Children(n) {
		switch t.Kind(c) {
		case tsbridge.KindTemplateHead:
			fb.mark(c)
			parts = append(parts, Part{Kind: PartString, Name: Intern(t.Value(c))})
		case tsbridge.KindTemplateSpan:
			fb.mark(c)
			for _, sc := range t.Children(c) {
				switch t.Kind(sc) {
				case tsbridge.KindTemplateMiddle, tsbridge.KindTemplateTail:
					fb.mark(sc)
					parts = append(parts, Part{Kind: PartString, Name: Intern(t.Value(sc))})
				default:
					args = append(args, fb.lowerExpr(sc))
				}
			}
		}
	}
	for len(parts) < len(args)+1 {
		parts = append(parts, Part{Kind: PartString})
	}
	return parts, args
}

// lowerTagged lowers tag`…`: a call of the tag with the strings array and
// the values, in method-call shape when the tag is a property (sql.raw`…`).
func (fb *fnBuilder) lowerTagged(n tsbridge.NodeID) ValueID {
	e := fb.elems(n)
	if len(e) < 2 {
		return fb.emitUnknown(n)
	}
	tag, tmpl := e[0], e[len(e)-1]
	callee, aux, lead := fb.lowerCallee(tag)
	parts, vals := fb.templateParts(tmpl)
	fb.mark(tmpl)
	strs := fb.emit(OpConst, tmpl, fb.withParts(Aux{Flags: LitStrings}, parts))
	aux.Flags |= AuxTagged
	args := append([]ValueID{callee}, lead...)
	args = append(args, strs)
	args = append(args, vals...)
	return fb.emitTypedArgs(OpCall, n, aux, args)
}

func (fb *fnBuilder) emitTypedArgs(op Op, n tsbridge.NodeID, aux Aux, args []ValueID) ValueID {
	v := fb.emitArgs(op, n, aux, args)
	fb.typeOf(v, n)
	return v
}

// --- arrays ---

func (fb *fnBuilder) lowerArray(n tsbridge.NodeID) ValueID {
	t := fb.t
	var args []ValueID
	for _, c := range fb.elems(n) {
		switch t.Kind(c) {
		case tsbridge.KindOmittedExpression:
			fb.mark(c)
			args = append(args, NoValue)
		default:
			args = append(args, fb.lowerExpr(c))
		}
	}
	return fb.emitTypedArgs(OpArray, n, Aux{}, args)
}

// --- property access and calls, with optional chains ---

// chainRoot reports the outermost link of an optional chain: a?.b.c is one
// chain whose root is the .c access; a short-circuit anywhere in it skips
// the rest.
func (fb *fnBuilder) chainRoot(n tsbridge.NodeID) bool {
	t := fb.t
	if t.Flags(n)&tsbridge.FlagOptionalChain == 0 {
		return false
	}
	p := t.Parent(n)
	if t.Flags(p)&tsbridge.FlagOptionalChain == 0 {
		return true
	}
	switch t.Kind(p) {
	case tsbridge.KindPropertyAccessExpression, tsbridge.KindElementAccessExpression, tsbridge.KindCallExpression,
		tsbridge.KindNonNullExpression, tsbridge.KindTaggedTemplateExpression:
		return fb.first(p) != n
	}
	return true
}

// lowerChain lowers an optional chain: each ?. link branches to one
// short-circuit block on null or undefined, and the chain's value is a phi
// of its result and undefined.
func (fb *fnBuilder) lowerChain(n tsbridge.NodeID) ValueID {
	short := fb.newBlock("opt.short")
	fb.chains = append(fb.chains, short)
	var v ValueID
	switch fb.t.Kind(n) {
	case tsbridge.KindNonNullExpression:
		v = fb.lowerExpr(fb.first(n))
	case tsbridge.KindTaggedTemplateExpression:
		v = fb.lowerTagged(n)
	default:
		v = fb.lowerLink(n)
	}
	fb.chains = fb.chains[:len(fb.chains)-1]
	done := fb.newBlock("opt.done")
	end := fb.cur
	fb.jump(done)
	fb.seal(short)
	fb.cur = short
	if len(fb.f.Blocks[short].Preds) == 0 {
		fb.f.Blocks[short].Dead = true
	}
	fb.jump(done)
	fb.seal(done)
	fb.cur = done
	return fb.merge(n, done, func(p BlockID) ValueID {
		if p == end && p != short {
			return v
		}
		return fb.undef
	})
}

// nullCheck branches to the current chain's short-circuit block when v is
// null or undefined.
func (fb *fnBuilder) nullCheck(v ValueID, n tsbridge.NodeID) {
	if len(fb.chains) == 0 {
		return
	}
	short := fb.chains[len(fb.chains)-1]
	isNull := fb.emit(OpBinOp, n, Aux{Name: Intern("==")}, v, fb.undef)
	cont := fb.newBlock("opt.cont")
	fb.branch(n, isNull, short, cont)
	fb.seal(cont)
	fb.cur = cont
}

// lowerLink lowers a property access, element access or call.
func (fb *fnBuilder) lowerLink(n tsbridge.NodeID) ValueID {
	t := fb.t
	switch t.Kind(n) {
	case tsbridge.KindPropertyAccessExpression:
		obj, name, super := fb.accessBase(n)
		if super {
			return fb.emitTyped(OpLoad, n, Aux{Name: Intern(name), Flags: AuxSuper}, obj)
		}
		return fb.emitTyped(OpLoad, n, Aux{Name: Intern(name)}, obj)
	case tsbridge.KindElementAccessExpression:
		e := fb.elems(n)
		if len(e) < 2 {
			return fb.emitUnknown(n)
		}
		obj := fb.lowerExpr(e[0])
		if t.Flags(n)&tsbridge.FlagQuestionDot != 0 {
			fb.nullCheck(obj, n)
		}
		if name, ok := fb.staticKey(e[1]); ok {
			return fb.emitTyped(OpLoad, n, Aux{Name: Intern(name)}, obj)
		}
		key := fb.lowerExpr(e[1])
		return fb.emitTyped(OpLoad, n, Aux{Flags: AuxDynamic}, obj, key)
	case tsbridge.KindCallExpression:
		return fb.lowerCall(n)
	}
	return fb.emitUnknown(n)
}

// accessBase lowers the object of a property access (null-checking it for
// ?.) and returns it with the property name. For super.x the object is this.
func (fb *fnBuilder) accessBase(n tsbridge.NodeID) (obj ValueID, name string, super bool) {
	t := fb.t
	e := fb.elems(n)
	if len(e) < 2 {
		return fb.emitUnknown(n), "", false
	}
	nameNode := e[len(e)-1]
	fb.mark(nameNode)
	name = t.Value(nameNode)
	if t.Kind(e[0]) == tsbridge.KindSuper {
		fb.mark(e[0])
		return fb.thisValue(), name, true
	}
	obj = fb.lowerExpr(e[0])
	if t.Flags(n)&tsbridge.FlagQuestionDot != 0 {
		fb.nullCheck(obj, n)
	}
	return obj, name, false
}

// staticKey returns the name of a literal element key: o["x"], o[0].
func (fb *fnBuilder) staticKey(n tsbridge.NodeID) (string, bool) {
	switch fb.t.Kind(n) {
	case tsbridge.KindStringLiteral, tsbridge.KindNumericLiteral, tsbridge.KindNoSubstitutionTemplateLiteral:
		fb.mark(n)
		return fb.t.Value(n), true
	}
	return "", false
}

// lowerCallee lowers what a call calls. A property access callee keeps the
// call in method-call shape: the receiver is the first argument and Aux
// holds the method name (call v1.m(…) this=v1). lead holds extra leading
// arguments: the key of a dynamic method call, the this of a super call.
func (fb *fnBuilder) lowerCallee(callee tsbridge.NodeID) (fn ValueID, aux Aux, lead []ValueID) {
	t := fb.t
	c := fb.unwrap(callee)
	switch t.Kind(c) {
	case tsbridge.KindPropertyAccessExpression:
		if t.Flags(c)&tsbridge.FlagOptionalChain != 0 && fb.chainRoot(c) {
			break
		}
		fb.mark(c)
		recv, name, super := fb.accessBase(c)
		flags := AuxMethod
		if super {
			flags |= AuxSuper
		}
		return recv, Aux{Name: Intern(name), Flags: flags}, nil
	case tsbridge.KindElementAccessExpression:
		if t.Flags(c)&tsbridge.FlagOptionalChain != 0 && fb.chainRoot(c) {
			break
		}
		e := fb.elems(c)
		if len(e) < 2 {
			break
		}
		fb.mark(c)
		recv := fb.lowerExpr(e[0])
		if t.Flags(c)&tsbridge.FlagQuestionDot != 0 {
			fb.nullCheck(recv, c)
		}
		if name, ok := fb.staticKey(e[1]); ok {
			return recv, Aux{Name: Intern(name), Flags: AuxMethod}, nil
		}
		key := fb.lowerExpr(e[1])
		return recv, Aux{Flags: AuxMethod | AuxDynamic}, []ValueID{key}
	case tsbridge.KindIdentifier:
		v := fb.lowerExpr(c)
		return v, Aux{Name: Intern(t.Value(c))}, nil
	case tsbridge.KindFunctionExpression, tsbridge.KindArrowFunction:
		return fb.lowerExpr(c), Aux{Flags: AuxIIFE}, nil
	}
	return fb.lowerExpr(callee), Aux{}, nil
}

func (fb *fnBuilder) lowerArgs(nodes []tsbridge.NodeID) []ValueID {
	out := make([]ValueID, 0, len(nodes))
	for _, a := range nodes {
		out = append(out, fb.lowerExpr(a))
	}
	return out
}

// lowerCall lowers a call expression.
func (fb *fnBuilder) lowerCall(n tsbridge.NodeID) ValueID {
	t := fb.t
	e := fb.elems(n)
	if len(e) == 0 {
		return fb.emitUnknown(n)
	}
	callee := fb.unwrap(e[0])
	args := e[1:]
	switch t.Kind(callee) {
	case tsbridge.KindSuper:
		fb.mark(callee)
		argv := fb.lowerArgs(args)
		v := fb.emitTypedArgs(OpCall, n, Aux{Flags: AuxSuperCall}, append([]ValueID{fb.thisValue()}, argv...))
		if fb.f.Kind == FuncConstructor {
			fb.constructorPrologue(n)
		}
		return v
	case tsbridge.KindImportKeyword:
		fb.mark(callee)
		argv := fb.lowerArgs(args)
		if len(args) > 0 {
			if spec, ok := fb.literalString(args[0]); ok {
				fb.addImport(Import{Specifier: spec, Imported: "*", Kind: ImportDynamic}, args[0], n)
			}
		}
		return fb.emitTypedArgs(OpCall, n, Aux{Flags: AuxImport}, append([]ValueID{fb.intrinsic("%import")}, argv...))
	}
	fn, aux, lead := fb.lowerCallee(e[0])
	optional := t.Flags(n)&tsbridge.FlagQuestionDot != 0
	if optional && aux.Flags&AuxMethod != 0 {
		// recv.m?.(): load the method to check it, then call it with this.
		var m ValueID
		if aux.Flags&AuxDynamic != 0 {
			m = fb.emit(OpLoad, e[0], Aux{Flags: AuxDynamic}, fn, lead[0])
		} else {
			flags := AuxFlags(0)
			if aux.Flags&AuxSuper != 0 {
				flags = AuxSuper
			}
			m = fb.emit(OpLoad, e[0], Aux{Name: aux.Name, Flags: flags}, fn)
		}
		fb.nullCheck(m, n)
		argv := fb.lowerArgs(args)
		return fb.emitTypedArgs(OpCall, n, Aux{Name: aux.Name, Flags: AuxWithThis | AuxOptional}, append([]ValueID{m, fn}, argv...))
	}
	if optional {
		fb.nullCheck(fn, n)
		aux.Flags |= AuxOptional
	}
	if t.Kind(callee) == tsbridge.KindIdentifier && fb.b.idVar[callee] == 0 {
		switch t.Value(callee) {
		case "require":
			if len(args) == 1 {
				if spec, ok := fb.literalString(args[0]); ok {
					aux.Flags |= AuxRequire
					fb.addImport(Import{Specifier: spec, Imported: "default", Local: fb.requireBinding(n), Kind: ImportRequire}, args[0], n)
				}
			}
		case "eval":
			aux.Flags |= AuxEval
			fb.b.warn(WarnEval, n, "direct eval can read and write the function's variables")
		}
	}
	argv := fb.lowerArgs(args)
	all := make([]ValueID, 0, 1+len(lead)+len(argv))
	all = append(all, fn)
	all = append(all, lead...)
	all = append(all, argv...)
	return fb.emitTypedArgs(OpCall, n, aux, all)
}

// literalString returns the value of a string literal argument.
func (fb *fnBuilder) literalString(n tsbridge.NodeID) (string, bool) {
	switch fb.t.Kind(n) {
	case tsbridge.KindStringLiteral, tsbridge.KindNoSubstitutionTemplateLiteral:
		return fb.t.Value(n), true
	}
	return "", false
}

func (fb *fnBuilder) lowerNew(n tsbridge.NodeID) ValueID {
	t := fb.t
	e := fb.elems(n)
	if len(e) == 0 {
		return fb.emitUnknown(n)
	}
	callee := fb.lowerExpr(e[0])
	hint := ""
	switch c := fb.unwrap(e[0]); t.Kind(c) {
	case tsbridge.KindIdentifier:
		hint = t.Value(c)
	case tsbridge.KindPropertyAccessExpression:
		hint = t.Text(c)
	}
	argv := fb.lowerArgs(e[1:])
	return fb.emitTypedArgs(OpNew, n, Aux{Name: Intern(hint)}, append([]ValueID{callee}, argv...))
}

func (fb *fnBuilder) lowerDelete(n tsbridge.NodeID) ValueID {
	t := fb.t
	target := fb.unwrap(fb.first(n))
	if t.Flags(target)&tsbridge.FlagOptionalChain != 0 {
		short := fb.newBlock("opt.short")
		fb.chains = append(fb.chains, short)
		v := fb.lowerDeleteTarget(n, target)
		fb.chains = fb.chains[:len(fb.chains)-1]
		done := fb.newBlock("opt.done")
		end := fb.cur
		fb.jump(done)
		fb.seal(short)
		fb.cur = short
		yes := fb.constant(n, LitBool, "true")
		fb.jump(done)
		fb.seal(done)
		fb.cur = done
		return fb.merge(n, done, func(p BlockID) ValueID {
			if p == end && p != short {
				return v
			}
			return yes
		})
	}
	return fb.lowerDeleteTarget(n, target)
}

func (fb *fnBuilder) lowerDeleteTarget(n, target tsbridge.NodeID) ValueID {
	t := fb.t
	switch t.Kind(target) {
	case tsbridge.KindPropertyAccessExpression:
		fb.mark(target)
		obj, name, _ := fb.accessBase(target)
		return fb.emitTyped(OpDelete, n, Aux{Name: Intern(name)}, obj)
	case tsbridge.KindElementAccessExpression:
		fb.mark(target)
		e := fb.elems(target)
		if len(e) < 2 {
			return fb.emitUnknown(n)
		}
		obj := fb.lowerExpr(e[0])
		if t.Flags(target)&tsbridge.FlagQuestionDot != 0 {
			fb.nullCheck(obj, target)
		}
		if name, ok := fb.staticKey(e[1]); ok {
			return fb.emitTyped(OpDelete, n, Aux{Name: Intern(name)}, obj)
		}
		key := fb.lowerExpr(e[1])
		return fb.emitTyped(OpDelete, n, Aux{Flags: AuxDynamic}, obj, key)
	}
	fb.lowerExpr(target)
	return fb.constant(n, LitBool, "true")
}

// --- operators ---

// lowerUpdate lowers ++ and --: read, add or subtract one, write back.
func (fb *fnBuilder) lowerUpdate(n, operand tsbridge.NodeID, op string, prefix bool) ValueID {
	ref := fb.reference(operand)
	old := ref.read()
	one := fb.constant(n, LitNumber, "1")
	bop := "+"
	if op == "--" {
		bop = "-"
	}
	nv := fb.emitTyped(OpBinOp, n, Aux{Name: Intern(bop)}, old, one)
	ref.write(nv, n)
	if prefix {
		return nv
	}
	return old
}

// ref is an assignable place evaluated once: a name, a property or an element.
type ref struct {
	fb    *fnBuilder
	kind  tsbridge.Kind
	node  tsbridge.NodeID
	obj   ValueID
	key   ValueID
	name  string
	super bool
}

func (fb *fnBuilder) reference(target tsbridge.NodeID) *ref {
	t := fb.t
	target = fb.unwrap(target)
	r := &ref{fb: fb, kind: t.Kind(target), node: target}
	fb.mark(target)
	switch r.kind {
	case tsbridge.KindPropertyAccessExpression:
		r.obj, r.name, r.super = fb.accessBase(target)
	case tsbridge.KindElementAccessExpression:
		e := fb.elems(target)
		if len(e) < 2 {
			r.kind = tsbridge.KindInvalid
			return r
		}
		r.obj = fb.lowerExpr(e[0])
		if name, ok := fb.staticKey(e[1]); ok {
			r.name = name
		} else {
			r.key = fb.lowerExpr(e[1])
		}
	case tsbridge.KindIdentifier:
	default:
		r.kind = tsbridge.KindInvalid
	}
	return r
}

func (r *ref) read() ValueID {
	fb := r.fb
	switch r.kind {
	case tsbridge.KindIdentifier:
		return fb.readIdent(r.node)
	case tsbridge.KindPropertyAccessExpression, tsbridge.KindElementAccessExpression:
		if r.key != NoValue {
			return fb.emitTyped(OpLoad, r.node, Aux{Flags: AuxDynamic}, r.obj, r.key)
		}
		flags := AuxFlags(0)
		if r.super {
			flags = AuxSuper
		}
		return fb.emitTyped(OpLoad, r.node, Aux{Name: Intern(r.name), Flags: flags}, r.obj)
	}
	return fb.emitUnknown(r.node)
}

func (r *ref) write(v ValueID, at tsbridge.NodeID) {
	fb := r.fb
	switch r.kind {
	case tsbridge.KindIdentifier:
		fb.writeIdent(r.node, v, at)
	case tsbridge.KindPropertyAccessExpression, tsbridge.KindElementAccessExpression:
		if r.key != NoValue {
			fb.emit(OpStore, at, Aux{Flags: AuxDynamic}, r.obj, r.key, v)
			return
		}
		flags := AuxFlags(0)
		if r.super {
			flags = AuxSuper
		}
		fb.emit(OpStore, at, Aux{Name: Intern(r.name), Flags: flags}, r.obj, v)
		fb.commonJSExport(r, v, at)
	default:
		fb.emitArgs(OpUnknown, r.node, Aux{Name: Intern(fb.t.CompilerKind(r.node))}, []ValueID{v})
		fb.markUnknown(r.node)
	}
}

var compoundOps = map[string]string{
	"+=": "+", "-=": "-", "*=": "*", "/=": "/", "%=": "%", "**=": "**", "<<=": "<<", ">>=": ">>",
	">>>=": ">>>", "&=": "&", "|=": "|", "^=": "^",
}

func (fb *fnBuilder) lowerBinary(n tsbridge.NodeID) ValueID {
	t := fb.t
	e := fb.elems(n)
	if len(e) < 2 {
		return fb.emitUnknown(n)
	}
	l, r := e[0], e[len(e)-1]
	op := t.Operator(n)
	switch op {
	case "=":
		return fb.lowerAssign(n, l, r)
	case "&&", "||", "??":
		return fb.lowerShortCircuit(n, l, r, op)
	case "&&=", "||=", "??=":
		return fb.lowerLogicalAssign(n, l, r, op[:len(op)-1])
	case ",":
		fb.lowerExpr(l)
		return fb.lowerExpr(r)
	}
	if bop, ok := compoundOps[op]; ok {
		ref := fb.reference(l)
		old := ref.read()
		rv := fb.lowerExpr(r)
		nv := fb.emitTyped(OpBinOp, n, Aux{Name: Intern(bop)}, old, rv)
		ref.write(nv, n)
		return nv
	}
	lv := fb.lowerExpr(l)
	rv := fb.lowerExpr(r)
	return fb.emitTyped(OpBinOp, n, Aux{Name: Intern(op)}, lv, rv)
}

// lowerAssign lowers target = value; a pattern target destructures.
func (fb *fnBuilder) lowerAssign(n, l, r tsbridge.NodeID) ValueID {
	t := fb.t
	target := fb.unwrap(l)
	switch t.Kind(target) {
	case tsbridge.KindObjectLiteralExpression, tsbridge.KindArrayLiteralExpression:
		v := fb.lowerExpr(r)
		fb.assignTarget(target, v, v, "")
		return v
	case tsbridge.KindIdentifier:
		fb.mark(target)
		v := fb.lowerExpr(r)
		fb.writeIdent(target, v, n)
		return v
	}
	ref := fb.reference(target)
	v := fb.lowerExpr(r)
	ref.write(v, n)
	return v
}

// lowerShortCircuit lowers a && b, a || b and a ?? b as a branch on a and
// a phi of a and b.
func (fb *fnBuilder) lowerShortCircuit(n, l, r tsbridge.NodeID, op string) ValueID {
	lv := fb.lowerExpr(l)
	return fb.shortCircuit(n, lv, op, func() ValueID { return fb.lowerExpr(r) })
}

func (fb *fnBuilder) shortCircuit(n tsbridge.NodeID, lv ValueID, op string, rhs func() ValueID) ValueID {
	cond := lv
	if op == "??" {
		cond = fb.emit(OpBinOp, n, Aux{Name: Intern("==")}, lv, fb.undef)
	}
	check := fb.cur
	right := fb.newBlock(map[string]string{"&&": "and.rhs", "||": "or.rhs", "??": "nullish.rhs"}[op])
	done := fb.newBlock(map[string]string{"&&": "and.done", "||": "or.done", "??": "nullish.done"}[op])
	if op == "||" {
		fb.branch(n, cond, done, right)
	} else {
		fb.branch(n, cond, right, done)
	}
	fb.seal(right)
	fb.cur = right
	rv := rhs()
	end := fb.cur
	fb.jump(done)
	fb.seal(done)
	fb.cur = done
	return fb.merge(n, done, func(p BlockID) ValueID {
		if p == check {
			return lv
		}
		if p == end {
			return rv
		}
		return fb.undef
	})
}

// lowerLogicalAssign lowers a &&= b, a ||= b, a ??= b: the write happens
// only on the path that evaluates b.
func (fb *fnBuilder) lowerLogicalAssign(n, l, r tsbridge.NodeID, op string) ValueID {
	ref := fb.reference(l)
	old := ref.read()
	return fb.shortCircuit(n, old, op, func() ValueID {
		v := fb.lowerExpr(r)
		ref.write(v, n)
		return v
	})
}

func (fb *fnBuilder) lowerConditional(n tsbridge.NodeID) ValueID {
	e := fb.elems(n)
	if len(e) < 3 {
		return fb.emitUnknown(n)
	}
	cond := fb.lowerExpr(e[0])
	tb := fb.newBlock("cond.true")
	fbk := fb.newBlock("cond.false")
	done := fb.newBlock("cond.done")
	fb.branch(n, cond, tb, fbk)
	fb.seal(tb)
	fb.seal(fbk)
	fb.cur = tb
	tv := fb.lowerExpr(e[1])
	tEnd := fb.cur
	fb.jump(done)
	fb.cur = fbk
	fv := fb.lowerExpr(e[2])
	fEnd := fb.cur
	fb.jump(done)
	fb.seal(done)
	fb.cur = done
	return fb.merge(n, done, func(p BlockID) ValueID {
		switch p {
		case tEnd:
			return tv
		case fEnd:
			return fv
		}
		return fb.undef
	})
}

// --- JSX ---

// lowerJSX lowers an element or fragment to %jsx(tag, props, children): an
// intrinsic tag is a string, a component its value, a fragment %Fragment;
// props is an object (dangerouslySetInnerHTML stays an ordinary prop).
func (fb *fnBuilder) lowerJSX(n tsbridge.NodeID) ValueID {
	t := fb.t
	var opening tsbridge.NodeID
	var children []tsbridge.NodeID
	switch t.Kind(n) {
	case tsbridge.KindJsxSelfClosingElement:
		opening = n
	case tsbridge.KindJsxElement, tsbridge.KindJsxFragment:
		for _, c := range t.Children(n) {
			switch t.Kind(c) {
			case tsbridge.KindJsxOpeningElement, tsbridge.KindJsxOpeningFragment:
				opening = c
			case tsbridge.KindJsxClosingElement, tsbridge.KindJsxClosingFragment:
				fb.mark(c)
				for _, cc := range t.Children(c) {
					fb.mark(cc)
				}
			default:
				children = append(children, c)
			}
		}
	}
	fb.mark(opening)
	var tag ValueID
	tagName := ""
	var attrs tsbridge.NodeID
	if t.Kind(opening) == tsbridge.KindJsxOpeningFragment {
		tag = fb.intrinsic("%Fragment")
		tagName = "Fragment"
	} else {
		e := fb.elems(opening)
		if len(e) > 0 {
			tagNode := e[0]
			tagName = t.Text(tagNode)
			if jsxIntrinsic(t, tagNode) {
				fb.mark(tagNode)
				tag = fb.constant(tagNode, LitString, tagName)
			} else {
				tag = fb.lowerExpr(tagNode)
			}
			for _, c := range e[1:] {
				if t.Kind(c) == tsbridge.KindJsxAttributes {
					attrs = c
				}
			}
		}
	}
	props := fb.lowerJSXAttributes(attrs, opening)
	var kids []ValueID
	for _, c := range children {
		switch t.Kind(c) {
		case tsbridge.KindJsxText:
			fb.mark(c)
			if txt := jsxText(t.Value(c)); txt != "" {
				kids = append(kids, fb.constant(c, LitString, txt))
			}
		case tsbridge.KindJsxExpression:
			fb.mark(c)
			if e := fb.elems(c); len(e) > 0 {
				v := fb.lowerExpr(e[0])
				if t.Text(c) != "" && strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(t.Text(c), "{")), "...") {
					v = fb.emit(OpSpread, c, Aux{}, v)
				}
				kids = append(kids, v)
			}
		default:
			kids = append(kids, fb.lowerExpr(c))
		}
	}
	arr := fb.emitArgs(OpArray, n, Aux{}, kids)
	return fb.emitTypedArgs(OpCall, n, Aux{Name: Intern(tagName), Flags: AuxJSX}, []ValueID{fb.intrinsic("%jsx"), tag, props, arr})
}

func (fb *fnBuilder) lowerJSXAttributes(attrs, at tsbridge.NodeID) ValueID {
	t := fb.t
	var parts []Part
	var args []ValueID
	if attrs != 0 {
		fb.mark(attrs)
		for _, a := range t.Children(attrs) {
			fb.mark(a)
			switch t.Kind(a) {
			case tsbridge.KindJsxAttribute:
				e := fb.elems(a)
				if len(e) == 0 {
					continue
				}
				fb.mark(e[0])
				for _, nc := range t.Children(e[0]) {
					fb.mark(nc)
				}
				name := t.Text(e[0])
				var v ValueID
				switch {
				case len(e) < 2:
					v = fb.constant(a, LitBool, "true")
				case t.Kind(e[1]) == tsbridge.KindJsxExpression:
					fb.mark(e[1])
					if inner := fb.elems(e[1]); len(inner) > 0 {
						v = fb.lowerExpr(inner[0])
					} else {
						v = fb.undef
					}
				default:
					v = fb.lowerExpr(e[1])
				}
				parts = append(parts, Part{Kind: PartKey, Name: Intern(name)})
				args = append(args, v)
			case tsbridge.KindJsxSpreadAttribute:
				parts = append(parts, Part{Kind: PartSpread})
				args = append(args, fb.lowerExpr(fb.first(a)))
			}
		}
	} else {
		attrs = at
	}
	return fb.emitArgs(OpObject, attrs, fb.withParts(Aux{}, parts), args)
}

// jsxText applies JSX's whitespace rule to text between tags: lines are
// trimmed (the first only at its end, the last only at its start), empty
// lines dropped, and the rest joined with spaces.
func jsxText(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	var out []string
	for i, l := range lines {
		if i > 0 {
			l = strings.TrimLeft(l, " \t")
		}
		if i < len(lines)-1 {
			l = strings.TrimRight(l, " \t")
		}
		if l != "" {
			out = append(out, l)
		}
	}
	return strings.Join(out, " ")
}
