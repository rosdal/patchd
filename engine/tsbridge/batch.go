package tsbridge

import (
	"github.com/microsoft/typescript-go/shim/ast"
	"github.com/microsoft/typescript-go/shim/checker"
)

// Batch queries answer one question for many nodes under one lock, for
// builders that ask about every expression of a file. Unlike TypeOf and
// SymbolOf they don't memoise per node: a builder asks once per node, and a
// per-node cache over a whole repo would cost more memory than the IR. A
// panic on one node leaves that node's answer zero and the rest intact.

// TypesOf returns TypeOf for each node, in order.
func (p *Program) TypesOf(nodes []NodeID) []TypeID {
	out := make([]TypeID, len(nodes))
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, n := range nodes {
		out[i] = p.typeOfUncached(n)
	}
	return out
}

func (p *Program) typeOfUncached(n NodeID) (id TypeID) {
	defer p.recoverQuery("TypesOf")
	if id, ok := p.typeOf[n]; ok {
		return id
	}
	node, _ := p.node(n)
	c := p.checkerFor(n.File())
	if node == nil || c == nil {
		return 0
	}
	var t *checker.Type
	if ast.IsDeclaration(node) && node.Name() != nil && node.Kind != ast.KindSourceFile {
		t = c.GetTypeAtLocation(node.Name())
	} else {
		t = c.GetTypeAtLocation(node)
	}
	return p.internType(c, t)
}

// ValueSymbols returns, for each identifier, the symbol of the binding it
// names as a value: the local variable, parameter, function, class, enum,
// namespace or import alias (not resolved through the import). Where
// SymbolOf answers with a property, ValueSymbols answers with the variable:
// the `a` of a shorthand property `{a}` is the variable a, and the `a` of
// `export {a}` is the local a. Other nodes, and names that resolve to
// nothing (undeclared globals), are 0.
func (p *Program) ValueSymbols(nodes []NodeID) []SymbolID {
	out := make([]SymbolID, len(nodes))
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, n := range nodes {
		out[i] = p.valueSymbol(n)
	}
	return out
}

func (p *Program) valueSymbol(n NodeID) SymbolID {
	defer p.recoverQuery("ValueSymbols")
	node, _ := p.node(n)
	c := p.checkerFor(n.File())
	if node == nil || c == nil || node.Kind != ast.KindIdentifier {
		return 0
	}
	var s *ast.Symbol
	switch parent := node.Parent; {
	case parent != nil && parent.Kind == ast.KindShorthandPropertyAssignment && parent.Name() == node:
		s = c.GetShorthandAssignmentValueSymbol(parent)
	case parent != nil && parent.Kind == ast.KindExportSpecifier && exportSpecifierIsLocal(parent):
		s = c.GetExportSpecifierLocalTargetSymbol(parent)
	default:
		s = c.GetSymbolAtLocation(node)
	}
	if s == nil || c.IsUnknownSymbol(s) {
		return 0
	}
	return p.internSymbol(c, s)
}

// exportSpecifierIsLocal reports an export specifier of `export {a}` with no
// module specifier: it exports a local binding, not a re-export.
func exportSpecifierIsLocal(spec *ast.Node) bool {
	decl := spec.Parent
	for decl != nil && decl.Kind != ast.KindExportDeclaration {
		decl = decl.Parent
	}
	return decl != nil && decl.AsExportDeclaration().ModuleSpecifier == nil
}

// ModuleTarget is what an import specifier resolved to.
type ModuleTarget struct {
	File    FileID   `json:"-"`                 // the resolved file, when it is part of the program
	Path    string   `json:"path,omitempty"`    // its path
	Package *Package `json:"package,omitempty"` // the npm package it belongs to, if any
	Ambient string   `json:"ambient,omitempty"` // the name of a `declare module "m"` it resolved to
}

// ResolveModule returns the module a module specifier (the string literal of
// an import, export, require or dynamic import) resolves to.
func (p *Program) ResolveModule(specifier NodeID) (target ModuleTarget, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	defer p.recoverQuery("ResolveModule")
	node, _ := p.node(specifier)
	c := p.checkerFor(specifier.File())
	if node == nil || c == nil || !ast.IsStringLiteralLike(node) {
		return ModuleTarget{}, false
	}
	s := c.GetSymbolAtLocation(node)
	if s == nil || c.IsUnknownSymbol(s) {
		return ModuleTarget{}, false
	}
	for _, d := range s.Declarations {
		if d.Kind == ast.KindSourceFile {
			loc := p.location(d)
			return ModuleTarget{File: loc.File, Path: loc.Path, Package: loc.Package}, true
		}
		if d.Kind == ast.KindModuleDeclaration {
			if name := d.Name(); name != nil && ast.IsStringLiteral(name) {
				return ModuleTarget{Ambient: name.Text()}, true
			}
		}
	}
	return ModuleTarget{}, false
}
