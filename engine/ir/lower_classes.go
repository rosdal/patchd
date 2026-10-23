package ir

import (
	"strconv"

	"github.com/patchd/patchd/engine/tsbridge"
)

// Objects, classes, decorators, enums and namespaces.
//
// A class lowers, where it is defined, to its constructor's closure (flagged
// class), stores that attach methods and accessors to the prototype and
// static members to the constructor (so dataflow sees them), a call of its
// StaticInit with this = the constructor, and its decorators as calls.
// Instance field initializers run in ClassInit, which the constructor calls
// at entry (base classes) or right after super() (derived classes), after
// the parameter properties.

// lowerObject lowers an object literal: keys, computed keys, spreads,
// methods, accessors and __proto__ are kept apart in the part list.
func (fb *fnBuilder) lowerObject(n tsbridge.NodeID) ValueID {
	t := fb.t
	var parts []Part
	var args []ValueID
	for _, m := range t.Children(n) {
		fb.mark(m)
		switch t.Kind(m) {
		case tsbridge.KindPropertyAssignment:
			e := fb.elems(m)
			if len(e) < 2 {
				continue
			}
			key, kv, computed := fb.memberKey(e[0])
			v := fb.lowerExpr(e[1])
			switch {
			case computed:
				parts = append(parts, Part{Kind: PartComputed})
				args = append(args, kv, v)
			case key == "__proto__" && t.Kind(e[0]) != tsbridge.KindComputedPropertyName:
				parts = append(parts, Part{Kind: PartProto, Name: fb.name(key)})
				args = append(args, v)
			default:
				parts = append(parts, Part{Kind: PartKey, Name: fb.name(key)})
				args = append(args, v)
			}
		case tsbridge.KindShorthandPropertyAssignment:
			e := fb.elems(m)
			if len(e) == 0 {
				continue
			}
			fb.mark(e[0])
			parts = append(parts, Part{Kind: PartKey, Name: fb.name(t.Value(e[0]))})
			args = append(args, fb.readIdent(e[0]))
		case tsbridge.KindSpreadAssignment:
			parts = append(parts, Part{Kind: PartSpread})
			args = append(args, fb.lowerExpr(fb.first(m)))
		case tsbridge.KindMethodDeclaration, tsbridge.KindGetAccessor, tsbridge.KindSetAccessor:
			nameNode := fb.memberName(m)
			key, kv, computed := fb.memberKey(nameNode)
			clo := fb.closureOf(m)
			kind := PartMethod
			switch t.Kind(m) {
			case tsbridge.KindGetAccessor:
				kind = PartGetter
			case tsbridge.KindSetAccessor:
				kind = PartSetter
			}
			if computed {
				parts = append(parts, Part{Kind: PartComputed})
				args = append(args, kv, clo)
			} else {
				parts = append(parts, Part{Kind: kind, Name: fb.name(key)})
				args = append(args, clo)
			}
		}
	}
	return fb.emitTypedArgs(OpObject, n, fb.withParts(Aux{}, parts), args)
}

// memberName returns a member's name node.
func (fb *fnBuilder) memberName(m tsbridge.NodeID) tsbridge.NodeID {
	t := fb.t
	for _, c := range t.Children(m) {
		if k := t.Kind(c); isNameKind(k) || k == tsbridge.KindComputedPropertyName {
			return c
		}
	}
	return 0
}

// memberKey returns a member name's static text, or for a computed name its
// value.
func (fb *fnBuilder) memberKey(n tsbridge.NodeID) (key string, v ValueID, computed bool) {
	t := fb.t
	if n == 0 {
		return "", NoValue, false
	}
	fb.mark(n)
	if t.Kind(n) == tsbridge.KindComputedPropertyName {
		inner := fb.first(n)
		if s, ok := fb.literalString(inner); ok {
			fb.mark(inner)
			return s, NoValue, false
		}
		return "", fb.lowerExpr(inner), true
	}
	return t.Value(n), NoValue, false
}

// lowerClass lowers a class declaration or expression and returns the
// constructor value.
func (fb *fnBuilder) lowerClass(n tsbridge.NodeID) ValueID {
	t := fb.t
	ci := fb.b.classes[n]
	fb.mark(n)
	if ci == nil {
		return fb.emitUnknown(n)
	}
	fb.enterScope(n)
	var base ValueID
	var nameNode tsbridge.NodeID
	for _, c := range t.Children(n) {
		switch t.Kind(c) {
		case tsbridge.KindHeritageClause:
			if fb.b.skipped(c) {
				continue
			}
			fb.mark(c)
			for _, e := range t.Children(c) {
				fb.mark(e)
				if x := fb.first(e); x != 0 && base == NoValue {
					base = fb.lowerExpr(x)
				}
			}
		case tsbridge.KindIdentifier:
			nameNode = c
		}
	}
	flags := AuxClass
	if ci.derived {
		flags |= AuxDerived
	}
	ctorSyntax := n
	if ci.explicit {
		ctorSyntax = fb.b.fns[ci.ctor].node
	}
	ctor := fb.closureOfFn(ci.ctor, ctorSyntax, flags)
	if base != NoValue {
		fb.emit(OpStore, n, Aux{Name: fb.name("__proto__"), Flags: AuxProto}, ctor, base)
	}
	if nameNode != 0 {
		fb.mark(nameNode)
		if v := fb.b.idVar[nameNode]; v > 0 {
			fb.writeVar(v-1, ctor, nameNode)
			fb.exportDecl(v-1, ctor)
		}
	} else if t.Kind(n) == tsbridge.KindClassDeclaration {
		fb.exportAnonymousDefault(n, ctor)
	}
	proto := fb.emit(OpLoad, n, Aux{Name: fb.name("prototype")}, ctor)
	if base != NoValue {
		bp := fb.emit(OpLoad, n, Aux{Name: fb.name("prototype")}, base)
		fb.emit(OpStore, n, Aux{Name: fb.name("__proto__"), Flags: AuxProto}, proto, bp)
	}
	for _, m := range t.Children(n) {
		if fb.b.skipped(m) {
			continue
		}
		switch t.Kind(m) {
		case tsbridge.KindMethodDeclaration, tsbridge.KindGetAccessor, tsbridge.KindSetAccessor:
			fb.mark(m)
			idx, ok := fb.b.fnOf[m]
			if !ok || fb.b.fns[idx].f.Flags&FuncAbstract != 0 {
				continue
			}
			static := t.Flags(m)&tsbridge.FlagStatic != 0
			target := proto
			sflags := AuxFlags(0)
			if static {
				target, sflags = ctor, AuxStatic
			}
			key, kv, computed := fb.memberKey(fb.memberName(m))
			clo := fb.closureOf(m)
			switch t.Kind(m) {
			case tsbridge.KindGetAccessor:
				sflags |= AuxGetter
			case tsbridge.KindSetAccessor:
				sflags |= AuxSetter
			default:
				sflags |= AuxMethod
			}
			if computed {
				fb.emit(OpStore, m, Aux{Flags: sflags | AuxDynamic}, target, kv, clo)
			} else {
				fb.emit(OpStore, m, Aux{Name: fb.name(key), Flags: sflags}, target, clo)
			}
		case tsbridge.KindConstructor, tsbridge.KindPropertyDeclaration, tsbridge.KindClassStaticBlockDeclaration,
			tsbridge.KindSemicolonClassElement:
			fb.mark(m)
		}
	}
	if ci.static >= 0 {
		s := fb.closureOfFn(ci.static, n, 0)
		fb.emit(OpCall, n, Aux{Name: fb.name("<static>"), Flags: AuxWithThis}, s, ctor)
	}
	fb.lowerDecorators(n, ctor, proto)
	return ctor
}

// lowerClassInit lowers instance field initializers, in source order, as
// stores on this.
func (fb *fnBuilder) lowerClassInit() {
	fb.lowerFields(false)
}

// lowerStaticInit lowers static fields and static blocks, in source order;
// this is the constructor.
func (fb *fnBuilder) lowerStaticInit() {
	fb.lowerFields(true)
}

func (fb *fnBuilder) lowerFields(static bool) {
	t := fb.t
	n := fb.info.node
	for _, m := range t.Children(n) {
		if fb.b.skipped(m) {
			continue
		}
		isStatic := t.Flags(m)&tsbridge.FlagStatic != 0
		switch t.Kind(m) {
		case tsbridge.KindPropertyDeclaration:
			if isStatic != static {
				continue
			}
			fb.mark(m)
			e := fb.elems(m)
			if len(e) == 0 {
				continue
			}
			key, kv, computed := fb.memberKey(e[0])
			if len(e) < 2 {
				continue // a field without an initializer
			}
			v := fb.lowerExpr(e[len(e)-1])
			if computed {
				fb.emit(OpStore, m, Aux{Flags: AuxDynamic}, fb.thisValue(), kv, v)
			} else {
				fb.emit(OpStore, m, Aux{Name: fb.name(key)}, fb.thisValue(), v)
			}
		case tsbridge.KindClassStaticBlockDeclaration:
			if !static {
				continue
			}
			fb.mark(m)
			for _, c := range t.Children(m) {
				if t.Kind(c) == tsbridge.KindBlock {
					fb.lowerBlock(c)
				}
			}
		}
	}
}

// decorators returns the decorators written on a node, in source order.
func (fb *fnBuilder) decorators(n tsbridge.NodeID) []tsbridge.NodeID {
	var out []tsbridge.NodeID
	for _, c := range fb.t.Children(n) {
		if fb.t.Kind(c) == tsbridge.KindDecorator {
			out = append(out, c)
		}
	}
	return out
}

// lowerDecorators applies a class's decorators the way TypeScript's
// (experimental) decorators do: for each member in source order, its
// parameters' decorators then its own; then the constructor's parameter
// decorators and the class decorators. Each decorator expression is
// evaluated in source order and applied last to first. The application is a
// call flagged decorator whose Name is the decorator's name (Body for
// @Body()) and whose part names the target ("param create#0").
func (fb *fnBuilder) lowerDecorators(n tsbridge.NodeID, ctor, proto ValueID) {
	t := fb.t
	apply := func(decs []tsbridge.NodeID, target ValueID, what string) {
		if len(decs) == 0 {
			return
		}
		vals := make([]ValueID, len(decs))
		for i, d := range decs {
			fb.mark(d)
			vals[i] = fb.lowerExpr(fb.first(d))
		}
		for i := len(decs) - 1; i >= 0; i-- {
			aux := fb.withParts(Aux{Name: fb.name(fb.decoratorName(decs[i])), Flags: AuxDecorator}, []Part{{Kind: PartTarget, Name: fb.name(what)}})
			fb.emit(OpCall, decs[i], aux, vals[i], target)
		}
	}
	params := func(m tsbridge.NodeID, target ValueID, member string) {
		i := 0
		for _, p := range t.Children(m) {
			if t.Kind(p) != tsbridge.KindParameter {
				continue
			}
			apply(fb.decorators(p), target, "param "+member+"#"+strconv.Itoa(i))
			i++
		}
	}
	var ctorNode tsbridge.NodeID
	for _, m := range t.Children(n) {
		if fb.b.skipped(m) {
			continue
		}
		target := proto
		if t.Flags(m)&tsbridge.FlagStatic != 0 {
			target = ctor
		}
		name := ""
		if nn := fb.memberName(m); nn != 0 {
			name = t.Text(nn)
		}
		switch t.Kind(m) {
		case tsbridge.KindMethodDeclaration:
			params(m, target, name)
			apply(fb.decorators(m), target, "method "+name)
		case tsbridge.KindGetAccessor:
			params(m, target, name)
			apply(fb.decorators(m), target, "get "+name)
		case tsbridge.KindSetAccessor:
			params(m, target, name)
			apply(fb.decorators(m), target, "set "+name)
		case tsbridge.KindPropertyDeclaration:
			apply(fb.decorators(m), target, "field "+name)
		case tsbridge.KindConstructor:
			if ctorNode == 0 && fb.hasBody(m) {
				ctorNode = m
			}
		}
	}
	if ctorNode != 0 {
		params(ctorNode, ctor, "constructor")
	}
	apply(fb.decorators(n), ctor, "class")
}

func (fb *fnBuilder) hasBody(n tsbridge.NodeID) bool {
	for _, c := range fb.t.Children(n) {
		if fb.t.Kind(c) == tsbridge.KindBlock {
			return true
		}
	}
	return false
}

// decoratorName is a decorator's name without its arguments: Body for
// @Body(), Validate.IsString for @Validate.IsString().
func (fb *fnBuilder) decoratorName(d tsbridge.NodeID) string {
	t := fb.t
	e := fb.unwrap(fb.first(d))
	if t.Kind(e) == tsbridge.KindCallExpression {
		e = fb.unwrap(fb.first(e))
	}
	return t.Text(e)
}

// lowerEnum lowers an enum to an object: one store per member, plus the
// reverse mapping for numeric members. Members without an initializer count
// up from the previous numeric one.
func (fb *fnBuilder) lowerEnum(n tsbridge.NodeID) {
	t := fb.t
	obj := fb.emitTyped(OpObject, n, Aux{})
	var nameNode tsbridge.NodeID
	next, numeric := 0, true
	members := map[string]ValueID{}
	fb.enums = append(fb.enums, members)
	for _, c := range t.Children(n) {
		switch t.Kind(c) {
		case tsbridge.KindIdentifier:
			nameNode = c
			fb.mark(c)
			fb.nameValue(obj, t.Value(c))
		case tsbridge.KindEnumMember:
			fb.mark(c)
			e := fb.elems(c)
			if len(e) == 0 {
				continue
			}
			fb.mark(e[0])
			name := t.Value(e[0])
			var v ValueID
			isNum := false
			if len(e) > 1 {
				init := e[len(e)-1]
				if t.Kind(init) == tsbridge.KindNumericLiteral {
					if x, err := strconv.Atoi(t.Value(init)); err == nil {
						next, numeric, isNum = x+1, true, true
					}
				} else {
					numeric = false
				}
				v = fb.lowerExpr(init)
			} else {
				v = fb.constant(c, LitNumber, strconv.Itoa(next))
				isNum = numeric
				next++
			}
			fb.emit(OpStore, c, Aux{Name: fb.name(name)}, obj, v)
			if isNum {
				fb.emit(OpStore, c, Aux{Flags: AuxDynamic}, obj, v, fb.constant(e[0], LitString, name))
			}
			members[name] = v
		}
	}
	fb.enums = fb.enums[:len(fb.enums)-1]
	if nameNode != 0 {
		if v := fb.b.idVar[nameNode]; v > 0 {
			fb.writeVar(v-1, obj, nameNode)
			fb.exportDecl(v-1, obj)
		}
	}
}

// enumMember resolves a bare member name inside an enum's initializers.
func (fb *fnBuilder) enumMember(name string) (ValueID, bool) {
	for i := len(fb.enums) - 1; i >= 0; i-- {
		if v, ok := fb.enums[i][name]; ok {
			return v, true
		}
	}
	return NoValue, false
}

// lowerNamespace lowers a namespace declaration: the namespace object (new,
// or the existing binding when the declaration merges with an earlier one),
// then a call of the namespace body with it.
func (fb *fnBuilder) lowerNamespace(n tsbridge.NodeID) {
	t := fb.t
	idx, ok := fb.b.nsOf[n]
	if !ok {
		return
	}
	var nameNode tsbridge.NodeID
	for _, c := range t.Children(n) {
		if t.Kind(c) == tsbridge.KindIdentifier {
			nameNode = c
		}
	}
	fb.mark(nameNode)
	var obj ValueID
	v := fb.b.idVar[nameNode] - 1
	if v >= 0 && fb.b.declared(v) {
		obj = fb.readVar(v, nameNode)
	} else {
		obj = fb.emitTyped(OpObject, n, Aux{})
		fb.nameValue(obj, t.Value(nameNode))
		if v >= 0 {
			fb.writeVar(v, obj, nameNode)
			fb.exportDecl(v, obj)
		}
	}
	clo := fb.closureOfFn(idx, n, 0)
	fb.emit(OpCall, n, Aux{Name: fb.name(t.Value(nameNode))}, clo, obj)
}

// declared records that v's binding was created and reports whether it had
// been before (namespace merging).
func (b *builder) declared(v int32) bool {
	if b.seenNs == nil {
		b.seenNs = map[int32]bool{}
	}
	seen := b.seenNs[v]
	b.seenNs[v] = true
	return seen
}

// lowerNamespaceBody lowers a namespace body like a block. Exported members
// are stores on the namespace object (its parameter).
func (fb *fnBuilder) lowerNamespaceBody() {
	t := fb.t
	n := fb.info.node
	fb.mark(n)
	for _, c := range t.Children(n) {
		switch t.Kind(c) {
		case tsbridge.KindModuleBlock:
			fb.lowerBlock(c)
		case tsbridge.KindModuleDeclaration:
			fb.mark(c)
			fb.lowerNamespace(c)
		case tsbridge.KindIdentifier:
			fb.mark(c)
		}
	}
}
