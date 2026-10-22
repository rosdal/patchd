package ir

import (
	"strconv"

	"github.com/patchd/patchd/engine/tsbridge"
)

// Destructuring lowers to a sequence of loads, with an explicit default
// branch per default value. Object patterns load each key; array patterns
// iterate the value (a spread, unless it is an array literal) and load by
// index, with the rest as load v[i:]. Every extracted value records the path
// it came from (PathLink: v1 + ".body.user.name"), which field-sensitive
// taint needs.

// bindTarget binds a declaration name (identifier or binding pattern) to v.
// base and path say where v came from, for path links.
func (fb *fnBuilder) bindTarget(name tsbridge.NodeID, v, base ValueID, path string) {
	t := fb.t
	fb.mark(name)
	switch t.Kind(name) {
	case tsbridge.KindIdentifier:
		if vi := fb.b.idVar[name]; vi > 0 {
			fb.writeVar(vi-1, v, name)
			fb.exportDecl(vi-1, v)
		} else {
			fb.storeGlobal(name, t.Value(name), v)
		}
	case tsbridge.KindObjectBindingPattern:
		fb.bindObjectPattern(name, v, base, path)
	case tsbridge.KindArrayBindingPattern:
		fb.bindArrayPattern(name, v, base, path)
	}
}

// bindingElement splits a binding element into its parts.
func (fb *fnBuilder) bindingElement(el tsbridge.NodeID) (prop, name, init tsbridge.NodeID, rest bool) {
	t := fb.t
	for _, c := range t.Children(el) {
		switch t.Role(c) {
		case tsbridge.RolePropertyName:
			prop = c
		case tsbridge.RoleName:
			name = c
		case tsbridge.RoleInitializer:
			init = c
		default:
			if t.Kind(c) == tsbridge.KindToken && t.Operator(c) == "..." {
				rest = true
			}
		}
	}
	return
}

func (fb *fnBuilder) bindObjectPattern(pat tsbridge.NodeID, v, base ValueID, path string) {
	t := fb.t
	var seen []Part
	for _, el := range t.Children(pat) {
		if t.Kind(el) != tsbridge.KindBindingElement {
			continue
		}
		fb.mark(el)
		prop, name, init, rest := fb.bindingElement(el)
		if rest {
			parts := append([]Part{{Kind: PartSpread}}, seen...)
			r := fb.emitTyped(OpObject, el, fb.withParts(Aux{}, parts), v)
			fb.pathLink(r, base, path+".<rest>")
			fb.bindTarget(name, r, base, path)
			continue
		}
		keyNode := prop
		if keyNode == 0 {
			keyNode = name
		}
		val, keyText := fb.loadKey(el, keyNode, v)
		if keyText != "" {
			seen = append(seen, Part{Kind: PartExclude, Name: Intern(keyText)})
		}
		sub := path + accessPath(keyText)
		fb.pathLink(val, base, sub)
		if init != 0 {
			val = fb.withDefault(val, init, el)
		}
		if t.Kind(name) == tsbridge.KindIdentifier {
			fb.typeOf(val, name)
		}
		fb.bindTarget(name, val, base, sub)
	}
}

// loadKey loads the property a pattern key names. It returns the static key
// text ("" for a computed key).
func (fb *fnBuilder) loadKey(at, key tsbridge.NodeID, obj ValueID) (ValueID, string) {
	t := fb.t
	fb.mark(key)
	switch t.Kind(key) {
	case tsbridge.KindComputedPropertyName:
		k := fb.lowerExpr(fb.first(key))
		return fb.emit(OpLoad, at, Aux{Flags: AuxDynamic}, obj, k), ""
	case tsbridge.KindIdentifier, tsbridge.KindStringLiteral, tsbridge.KindNumericLiteral,
		tsbridge.KindPrivateIdentifier, tsbridge.KindNoSubstitutionTemplateLiteral, tsbridge.KindBigIntLiteral:
		name := t.Value(key)
		return fb.emit(OpLoad, at, Aux{Name: Intern(name)}, obj), name
	}
	return fb.emitUnknown(key), ""
}

// accessPath renders a key as a path step: .name, or ["odd key"].
func accessPath(key string) string {
	if key == "" {
		return "[?]"
	}
	if isIdentName(key) {
		return "." + key
	}
	return "[" + strconv.Quote(key) + "]"
}

func isIdentName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == '_' || r == '$' || r == '#' && i == 0 || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r > 127 || i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

// iterated returns what array destructuring reads elements from: the value
// itself for an array literal, else a spread of it (the iteration protocol).
func (fb *fnBuilder) iterated(at tsbridge.NodeID, v ValueID) ValueID {
	if d := fb.f.Value(v); d != nil && d.Kind == ValueInstr && fb.f.Instrs[d.Def].Op == OpArray {
		return v
	}
	return fb.emit(OpSpread, at, Aux{}, v)
}

func (fb *fnBuilder) bindArrayPattern(pat tsbridge.NodeID, v, base ValueID, path string) {
	t := fb.t
	src := NoValue
	i := 0
	for _, el := range t.Children(pat) {
		switch t.Kind(el) {
		case tsbridge.KindOmittedExpression:
			fb.mark(el)
			i++
			continue
		case tsbridge.KindBindingElement:
		default:
			continue
		}
		if src == NoValue {
			src = fb.iterated(pat, v)
		}
		fb.mark(el)
		_, name, init, rest := fb.bindingElement(el)
		idx := strconv.Itoa(i)
		var val ValueID
		var sub string
		if rest {
			val = fb.emit(OpLoad, el, Aux{Name: Intern(idx), Flags: AuxRest}, src)
			sub = path + "[" + idx + ":]"
		} else {
			val = fb.emit(OpLoad, el, Aux{Name: Intern(idx)}, src)
			sub = path + "[" + idx + "]"
		}
		fb.pathLink(val, base, sub)
		if init != 0 {
			val = fb.withDefault(val, init, el)
		}
		if t.Kind(name) == tsbridge.KindIdentifier {
			fb.typeOf(val, name)
		}
		fb.bindTarget(name, val, base, sub)
		i++
	}
}

func (fb *fnBuilder) pathLink(v, base ValueID, path string) {
	if v == NoValue || base == NoValue || v == base {
		return
	}
	fb.f.paths = append(fb.f.paths, PathLink{Value: v, Base: base, Path: path})
}

// assignTarget assigns v to an expression target: a name, property, element,
// or an object or array literal used as a pattern.
func (fb *fnBuilder) assignTarget(target tsbridge.NodeID, v, base ValueID, path string) {
	t := fb.t
	target = fb.unwrap(target)
	fb.mark(target)
	switch t.Kind(target) {
	case tsbridge.KindIdentifier:
		fb.writeIdent(target, v, target)
	case tsbridge.KindObjectLiteralExpression:
		fb.assignObjectPattern(target, v, base, path)
	case tsbridge.KindArrayLiteralExpression:
		fb.assignArrayPattern(target, v, base, path)
	case tsbridge.KindVariableDeclarationList:
		for _, d := range t.Children(target) {
			if e := fb.elems(d); len(e) > 0 {
				fb.mark(d)
				fb.bindTarget(e[0], v, base, path)
			}
		}
	default:
		fb.reference(target).write(v, target)
	}
}

// splitDefault splits `target = default` in a pattern.
func (fb *fnBuilder) splitDefault(n tsbridge.NodeID) (target, def tsbridge.NodeID) {
	t := fb.t
	if t.Kind(n) == tsbridge.KindBinaryExpression && t.Operator(n) == "=" {
		if e := fb.elems(n); len(e) >= 2 {
			fb.mark(n)
			return e[0], e[len(e)-1]
		}
	}
	return n, 0
}

func (fb *fnBuilder) assignObjectPattern(pat tsbridge.NodeID, v, base ValueID, path string) {
	t := fb.t
	var seen []Part
	for _, m := range t.Children(pat) {
		fb.mark(m)
		switch t.Kind(m) {
		case tsbridge.KindPropertyAssignment:
			e := fb.elems(m)
			if len(e) < 2 {
				continue
			}
			val, keyText := fb.loadKey(m, e[0], v)
			if keyText != "" {
				seen = append(seen, Part{Kind: PartExclude, Name: Intern(keyText)})
			}
			sub := path + accessPath(keyText)
			fb.pathLink(val, base, sub)
			target, def := fb.splitDefault(e[1])
			if def != 0 {
				val = fb.withDefault(val, def, m)
			}
			fb.assignTarget(target, val, base, sub)
		case tsbridge.KindShorthandPropertyAssignment:
			e := fb.elems(m)
			if len(e) == 0 {
				continue
			}
			name := e[0]
			val, keyText := fb.loadKey(m, name, v)
			seen = append(seen, Part{Kind: PartExclude, Name: Intern(keyText)})
			sub := path + accessPath(keyText)
			fb.pathLink(val, base, sub)
			if len(e) > 1 {
				val = fb.withDefault(val, e[len(e)-1], m)
			}
			fb.writeIdent(name, val, m)
		case tsbridge.KindSpreadAssignment:
			parts := append([]Part{{Kind: PartSpread}}, seen...)
			r := fb.emit(OpObject, m, fb.withParts(Aux{}, parts), v)
			fb.pathLink(r, base, path+".<rest>")
			fb.assignTarget(fb.first(m), r, base, path)
		}
	}
}

func (fb *fnBuilder) assignArrayPattern(pat tsbridge.NodeID, v, base ValueID, path string) {
	t := fb.t
	src := NoValue
	for i, el := range fb.elems(pat) {
		fb.mark(el)
		if t.Kind(el) == tsbridge.KindOmittedExpression {
			continue
		}
		if src == NoValue {
			src = fb.iterated(pat, v)
		}
		idx := strconv.Itoa(i)
		if t.Kind(el) == tsbridge.KindSpreadElement {
			val := fb.emit(OpLoad, el, Aux{Name: Intern(idx), Flags: AuxRest}, src)
			fb.pathLink(val, base, path+"["+idx+":]")
			fb.assignTarget(fb.first(el), val, base, path+"["+idx+":]")
			continue
		}
		val := fb.emit(OpLoad, el, Aux{Name: Intern(idx)}, src)
		sub := path + "[" + idx + "]"
		fb.pathLink(val, base, sub)
		target, def := fb.splitDefault(el)
		if def != 0 {
			val = fb.withDefault(val, def, el)
		}
		fb.assignTarget(target, val, base, sub)
	}
}
