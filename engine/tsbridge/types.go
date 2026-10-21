package tsbridge

import (
	"github.com/microsoft/typescript-go/shim/ast"
	"github.com/microsoft/typescript-go/shim/checker"
)

// Queries use the checker typescript-go associated with the node's file: it
// is the one that type-checked the file, so its answers are already computed.
// Types from different checkers can't be mixed; a TypeID remembers its
// checker, and IsAssignable answers false for types from two checkers.

// checkerFor returns the checker for a file. Callers hold p.mu.
func (p *Program) checkerFor(f FileID) *checker.Checker {
	if p.closed || f == 0 || int(f) > len(p.sources) {
		return nil
	}
	c, done := p.prog.GetTypeCheckerForFile(p.ctx, p.sources[f-1])
	done() // non-exclusive in the compiler's pool; p.mu serialises our queries
	return c
}

func (p *Program) internSymbol(s *ast.Symbol) SymbolID {
	if s == nil {
		return 0
	}
	if id, ok := p.symbolIDs[s]; ok {
		return id
	}
	p.symbols = append(p.symbols, s)
	id := SymbolID(len(p.symbols)) //nolint:gosec // a program has far fewer than 2^32 symbols
	p.symbolIDs[s] = id
	return id
}

func (p *Program) symbol(id SymbolID) *ast.Symbol {
	if p.closed || id == 0 || int(id) > len(p.symbols) {
		return nil
	}
	return p.symbols[id-1]
}

func (p *Program) internType(c *checker.Checker, t *checker.Type) TypeID {
	if t == nil || c == nil {
		return 0
	}
	e := typeEntry{t: t, c: c}
	if id, ok := p.typeIDs[e]; ok {
		return id
	}
	p.types = append(p.types, e)
	id := TypeID(len(p.types)) //nolint:gosec // a program has far fewer than 2^32 types
	p.typeIDs[e] = id
	return id
}

func (p *Program) typeEntry(id TypeID) (typeEntry, bool) {
	if p.closed || id == 0 || int(id) > len(p.types) {
		return typeEntry{}, false
	}
	return p.types[id-1], true
}

// SymbolOf returns the symbol a node refers to or declares: identifiers,
// property and element accesses with literal keys, this, super, import
// specifiers, JSX tag names, and named declarations.
func (p *Program) SymbolOf(n NodeID) (SymbolID, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	defer p.recoverQuery("SymbolOf")
	return p.symbolOfLocked(n)
}

func (p *Program) symbolOfLocked(n NodeID) (SymbolID, bool) {
	p.stats.SymbolOfCalls++
	if id, ok := p.symbolOf[n]; ok {
		p.stats.SymbolOfHits++
		return id, id != 0
	}
	node, _ := p.node(n)
	c := p.checkerFor(n.File())
	if node == nil || c == nil {
		return 0, false
	}
	s := symbolAtNode(c, node)
	id := p.internSymbol(s)
	p.symbolOf[n] = id
	return id, id != 0
}

func symbolAtNode(c *checker.Checker, node *ast.Node) *ast.Symbol {
	if s := c.GetSymbolAtLocation(node); s != nil && !c.IsUnknownSymbol(s) {
		return s
	}
	switch node.Kind {
	case ast.KindPropertyAccessExpression:
		return symbolAtNode(c, node.Name())
	case ast.KindElementAccessExpression:
		if arg := node.AsElementAccessExpression().ArgumentExpression; arg != nil && ast.IsStringOrNumericLiteralLike(arg) {
			return c.GetSymbolAtLocation(arg)
		}
	case ast.KindCallExpression, ast.KindNewExpression, ast.KindTaggedTemplateExpression, ast.KindDecorator:
		if sig := c.GetResolvedSignature(node); sig != nil && sig.Declaration() != nil {
			return declarationSymbol(c, sig.Declaration())
		}
	case ast.KindJsxOpeningElement, ast.KindJsxSelfClosingElement, ast.KindJsxClosingElement:
		return c.GetSymbolAtLocation(node.TagName())
	}
	if ast.IsDeclaration(node) {
		if name := node.Name(); name != nil {
			if s := c.GetSymbolAtLocation(name); s != nil {
				return s
			}
		}
		return node.Symbol()
	}
	return nil
}

func declarationSymbol(c *checker.Checker, decl *ast.Node) *ast.Symbol {
	if s := decl.Symbol(); s != nil {
		return s
	}
	if name := decl.Name(); name != nil {
		return c.GetSymbolAtLocation(name)
	}
	return nil
}

// TypeOf returns the type of an expression, including the contextual types
// of callback parameters, or of the entity a declaration or type node names.
func (p *Program) TypeOf(n NodeID) TypeID {
	p.mu.Lock()
	defer p.mu.Unlock()
	defer p.recoverQuery("TypeOf")
	return p.typeOfLocked(n)
}

func (p *Program) typeOfLocked(n NodeID) TypeID {
	p.stats.TypeOfCalls++
	if id, ok := p.typeOf[n]; ok {
		p.stats.TypeOfHits++
		return id
	}
	node, _ := p.node(n)
	c := p.checkerFor(n.File())
	if node == nil || c == nil {
		return 0
	}
	var t *checker.Type
	switch {
	case ast.IsDeclaration(node) && node.Name() != nil && node.Kind != ast.KindSourceFile:
		t = c.GetTypeAtLocation(node.Name())
	case isTypeName(node):
		// A name in a type position (Props in `p: Props<T>`) is the declared
		// type of what it names, as in tsc; the checker would say any.
		if s := c.GetSymbolAtLocation(node); s != nil {
			if r := resolveAlias(c, s); r != nil {
				s = r
			}
			t = c.GetDeclaredTypeOfSymbol(s)
		}
	}
	if t == nil {
		t = c.GetTypeAtLocation(node)
	}
	id := p.internType(c, t)
	p.typeOf[n] = id
	return id
}

// isTypeName reports an identifier naming a type in a type reference, an
// implements/extends clause of an interface, or a qualified type name.
func isTypeName(node *ast.Node) bool {
	if node.Kind != ast.KindIdentifier || node.Parent == nil {
		return false
	}
	parent := node.Parent
	for parent.Kind == ast.KindQualifiedName && parent.AsQualifiedName().Right == node {
		node, parent = parent, parent.Parent
	}
	switch parent.Kind {
	case ast.KindTypeReference:
		return parent.AsTypeReferenceNode().TypeName == node
	case ast.KindExpressionWithTypeArguments:
		return ast.IsPartOfTypeNode(parent)
	}
	return false
}

// TypeString returns a type as the compiler displays it.
func (p *Program) TypeString(t TypeID) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	defer p.recoverQuery("TypeString")
	e, ok := p.typeEntry(t)
	if !ok {
		return ""
	}
	return e.c.TypeToString(e.t)
}

// TypeFlags classifies a type.
func (p *Program) TypeFlags(t TypeID) TypeFlags {
	p.mu.Lock()
	defer p.mu.Unlock()
	defer p.recoverQuery("TypeFlags")
	e, ok := p.typeEntry(t)
	if !ok {
		return 0
	}
	return typeFlags(e.c, e.t)
}

func typeFlags(c *checker.Checker, t *checker.Type) TypeFlags {
	f := t.Flags()
	var out TypeFlags
	set := func(cond bool, flag TypeFlags) {
		if cond {
			out |= flag
		}
	}
	set(f&checker.TypeFlagsStringLike != 0, TypeString)
	set(f&checker.TypeFlagsNumberLike != 0 && f&checker.TypeFlagsEnum == 0, TypeNumber)
	set(f&checker.TypeFlagsBooleanLike != 0, TypeBoolean)
	set(f&checker.TypeFlagsBigIntLike != 0, TypeBigInt)
	set(f&checker.TypeFlagsNull != 0, TypeNull)
	set(f&checker.TypeFlagsUndefined != 0, TypeUndefined)
	set(f&checker.TypeFlagsVoid != 0, TypeVoid)
	set(f&checker.TypeFlagsNever != 0, TypeNever)
	set(f&checker.TypeFlagsAny != 0, TypeAny)
	set(f&checker.TypeFlagsUnknown != 0, TypeUnknown)
	set(f&checker.TypeFlagsLiteral != 0 || f&checker.TypeFlagsBooleanLiteral != 0 || f&checker.TypeFlagsEnumLiteral != 0 && f&checker.TypeFlagsUnion == 0, TypeLiteral)
	set(f&checker.TypeFlagsUnion != 0, TypeUnion)
	set(f&checker.TypeFlagsIntersection != 0, TypeIntersection)
	set(f&checker.TypeFlagsObject != 0, TypeObject)
	set(f&checker.TypeFlagsTypeParameter != 0, TypeTypeParameter)
	set(f&checker.TypeFlagsEnumLike != 0, TypeEnum)
	set(f&checker.TypeFlagsESSymbolLike != 0, TypeESSymbol)
	if f&checker.TypeFlagsAny != 0 && t.AsIntrinsicType().IntrinsicName() == "error" {
		out |= TypeError
	}
	if f&checker.TypeFlagsObject != 0 && (len(c.GetCallSignatures(t)) > 0 || len(c.GetConstructSignatures(t)) > 0) {
		out |= TypeFunction
	}
	return out
}

// UnionMembers returns the members of a union type, or nil.
func (p *Program) UnionMembers(t TypeID) []TypeID {
	return p.members(t, checker.TypeFlagsUnion, "UnionMembers")
}

// IntersectionMembers returns the members of an intersection type, or nil.
func (p *Program) IntersectionMembers(t TypeID) []TypeID {
	return p.members(t, checker.TypeFlagsIntersection, "IntersectionMembers")
}

func (p *Program) members(t TypeID, flag checker.TypeFlags, op string) []TypeID {
	p.mu.Lock()
	defer p.mu.Unlock()
	defer p.recoverQuery(op)
	e, ok := p.typeEntry(t)
	if !ok || e.t.Flags()&flag == 0 {
		return nil
	}
	var out []TypeID
	for _, m := range e.t.Types() {
		out = append(out, p.internType(e.c, m))
	}
	return out
}

// Properties returns a type's properties, including inherited members,
// members of the apparent type (String for string, …) and the symbols of its
// index signatures.
func (p *Program) Properties(t TypeID) []SymbolID {
	p.mu.Lock()
	defer p.mu.Unlock()
	defer p.recoverQuery("Properties")
	e, ok := p.typeEntry(t)
	if !ok {
		return nil
	}
	apparent := e.c.GetApparentType(e.t)
	var out []SymbolID
	for _, s := range e.c.GetPropertiesOfType(apparent) {
		out = append(out, p.internSymbol(s))
	}
	for _, info := range e.c.GetIndexInfosOfType(apparent) {
		if decl := info.Declaration(); decl != nil && decl.Symbol() != nil {
			out = append(out, p.internSymbol(decl.Symbol()))
		}
	}
	return out
}

// IsAssignable reports whether a value of type from can be assigned to type
// to, using the checker's own assignability relation. Types from two
// different checkers are never assignable.
func (p *Program) IsAssignable(from, to TypeID) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	defer p.recoverQuery("IsAssignable")
	a, ok1 := p.typeEntry(from)
	b, ok2 := p.typeEntry(to)
	if !ok1 || !ok2 || a.c != b.c {
		return false
	}
	return a.c.IsTypeAssignableTo(a.t, b.t)
}

// Signature returns the signature a call, new expression, tagged template,
// decorator or JSX element resolved to: the chosen overload, parameter types
// after generic instantiation, and the return type.
func (p *Program) Signature(call NodeID) (Signature, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	defer p.recoverQuery("Signature")
	node, _ := p.node(call)
	c := p.checkerFor(call.File())
	if node == nil || c == nil || !kindOf(node.Kind).IsCallLike() {
		return Signature{}, false
	}
	sig := c.GetResolvedSignature(node)
	if sig == nil {
		return Signature{}, false
	}
	out := Signature{
		Text:   c.SignatureToStringEx(sig, nil, 0, nil),
		Return: p.internType(c, c.GetReturnTypeOfSignature(sig)),
	}
	params := sig.Parameters()
	for i, ps := range params {
		out.Parameters = append(out.Parameters, Parameter{
			Name:     ps.Name,
			Type:     p.internType(c, c.GetTypeOfSymbol(ps)),
			Optional: i >= sig.MinArgumentCount() && (!sig.HasRestParameter() || i != len(params)-1),
			Rest:     sig.HasRestParameter() && i == len(params)-1,
		})
	}
	if decl := sig.Declaration(); decl != nil {
		loc := p.location(decl)
		out.Declaration = &loc
	}
	return out, true
}
