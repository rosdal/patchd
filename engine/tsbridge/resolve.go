package tsbridge

import (
	"fmt"
	"strings"

	"github.com/microsoft/typescript-go/shim/ast"
	"github.com/microsoft/typescript-go/shim/checker"
)

// location converts a declaration node to a Location. Callers hold p.mu.
func (p *Program) location(decl *ast.Node) Location {
	sf := ast.GetSourceFileOfNode(decl)
	if sf == nil {
		return Location{}
	}
	id := p.fileOf[sf]
	loc := Location{File: id, Path: sf.FileName(), Range: p.rangeOf(decl, sf)}
	if id != 0 {
		f := p.files[id-1]
		loc.Line, loc.Column = f.Position(loc.Range.Pos)
		loc.Package = f.Package
	}
	return loc
}

// Declarations returns where a symbol is declared.
func (p *Program) Declarations(s SymbolID) []Location {
	p.mu.Lock()
	defer p.mu.Unlock()
	defer p.recoverQuery("Declarations")
	sym := p.symbol(s)
	if sym == nil {
		return nil
	}
	return p.locations(sym.Declarations)
}

func (p *Program) locations(decls []*ast.Node) []Location {
	out := make([]Location, 0, len(decls))
	for _, d := range decls {
		out = append(out, p.location(d))
	}
	return out
}

// SymbolName returns a symbol's name as declared ("default" for default
// exports). Compiler-made names are spelled as tsc spells them: "__function",
// "__call", "__index".
func (p *Program) SymbolName(s SymbolID) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if sym := p.symbol(s); sym != nil {
		if isInternalName(sym.Name) {
			return "__" + sym.Name[1:]
		}
		return sym.Name
	}
	return ""
}

// ResolveAlias follows import and export aliases (import { x as y },
// export { x } from, export *, export default, namespace imports, require)
// to the symbol they finally name. A symbol that is not an alias is returned
// unchanged; an alias whose target can't be found returns 0.
func (p *Program) ResolveAlias(s SymbolID) SymbolID {
	p.mu.Lock()
	defer p.mu.Unlock()
	defer p.recoverQuery("ResolveAlias")
	sym, c := p.symbolWithChecker(s)
	if sym == nil || c == nil {
		return 0
	}
	target := resolveAlias(c, sym)
	if target == nil {
		return 0
	}
	return p.internSymbol(c, target)
}

func resolveAlias(c *checker.Checker, s *ast.Symbol) *ast.Symbol {
	if s.Flags&ast.SymbolFlagsAlias == 0 {
		return s
	}
	t := c.GetAliasedSymbol(s)
	if t == nil || c.IsUnknownSymbol(t) {
		return nil
	}
	return t
}

// anyChecker returns a checker for questions that don't depend on a file.
// Callers hold p.mu.
func (p *Program) anyChecker() *checker.Checker {
	if p.closed || len(p.sources) == 0 {
		return nil
	}
	for i, f := range p.files {
		if f.Root {
			return p.checkerFor(FileID(i + 1))
		}
	}
	return p.checkerFor(1)
}

// ResolveCall resolves a call, new expression, tagged template, decorator or
// JSX element to the symbol it calls and that symbol's declarations. Calls
// through interfaces or abstract classes return every declaration the
// checker knows. When ok is false, Callee.Reason says why.
func (p *Program) ResolveCall(call NodeID) (callee Callee, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	defer func() {
		if r := recover(); r != nil {
			p.recordPanic("ResolveCall", r)
			callee, ok = Callee{Reason: ReasonFailed}, false
		}
	}()
	node, _ := p.node(call)
	c := p.checkerFor(call.File())
	if node == nil || c == nil || !kindOf(node.Kind).IsCallLike() {
		return Callee{Reason: ReasonNotACall}, false
	}
	return p.resolveCall(c, node)
}

func (p *Program) resolveCall(c *checker.Checker, node *ast.Node) (Callee, bool) {
	target := calleeExpression(node)
	if target == nil {
		return Callee{Reason: ReasonNoSymbol}, false
	}
	// obj[expr] with a key the checker can't read.
	if target.Kind == ast.KindElementAccessExpression {
		arg := target.AsElementAccessExpression().ArgumentExpression
		if arg == nil {
			return Callee{Reason: ReasonDynamicProperty}, false // obj[] in a file with syntax errors
		}
		if !ast.IsStringOrNumericLiteralLike(arg) {
			if t := c.GetTypeAtLocation(arg); t == nil || t.Flags()&checker.TypeFlagsStringOrNumberLiteralOrUnique == 0 {
				return Callee{Reason: ReasonDynamicProperty}, false
			}
		}
	}
	// An any-typed receiver or callee: nothing to resolve against.
	if receiver := receiverOf(target); receiver != nil {
		if t := c.GetTypeAtLocation(receiver); t != nil && t.Flags()&checker.TypeFlagsAny != 0 {
			return p.unresolvedAny(c, receiver, t), false
		}
	}

	sym := symbolAtNode(c, target)
	if sym != nil && sym.Flags&ast.SymbolFlagsAlias != 0 {
		resolved := resolveAlias(c, sym)
		if resolved == nil {
			reason := p.importReason(sym)
			if reason == "" {
				reason = ReasonPackageNotInstalled
			}
			return Callee{Name: sym.Name, Reason: reason}, false
		}
		sym = resolved
	}
	sym = followRequire(c, sym)
	sig := c.GetResolvedSignature(node)
	if sym == nil && sig != nil && sig.Declaration() != nil {
		sym = declarationSymbol(c, sig.Declaration())
	}
	if sym == nil {
		if t := c.GetTypeAtLocation(target); t != nil && t.Flags()&checker.TypeFlagsAny != 0 {
			return p.unresolvedAny(c, target, t), false
		}
		return Callee{Reason: ReasonNoSymbol}, false
	}

	// Union and intersection members: a synthetic property stands for the
	// real ones.
	var decls []*ast.Node
	for _, root := range c.GetRootSymbols(sym) {
		decls = append(decls, root.Declarations...)
	}
	if len(decls) == 0 {
		decls = sym.Declarations
	}
	callee := Callee{
		Symbol:        p.internSymbol(c, sym),
		Name:          sym.Name,
		QualifiedName: p.qualifiedName(c, sym, receiverOf(target)),
		Declarations:  p.locations(decls),
	}
	if len(decls) == 0 {
		callee.Reason = ReasonNoDeclaration
		return callee, false
	}
	return callee, true
}

// unresolvedAny explains an any-typed callee: an import from a package that is
// not installed, or plain any.
func (p *Program) unresolvedAny(c *checker.Checker, expr *ast.Node, t *checker.Type) Callee {
	for e := expr; e != nil; e = receiverOf(e) {
		if e.Kind != ast.KindIdentifier {
			continue
		}
		if s := c.GetSymbolAtLocation(e); s != nil {
			if reason := p.importReason(s); reason != "" && reason != ReasonNoDeclaration {
				return Callee{Name: s.Name, Reason: reason}
			}
		}
	}
	if t.Flags()&checker.TypeFlagsAny != 0 && t.AsIntrinsicType().IntrinsicName() == "error" {
		return Callee{Reason: ReasonUnresolvedName}
	}
	return Callee{Reason: ReasonAnyReceiver}
}

// calleeExpression returns the expression that names what a call-like node calls.
func calleeExpression(node *ast.Node) *ast.Node {
	var e *ast.Node
	switch node.Kind {
	case ast.KindCallExpression:
		e = node.AsCallExpression().Expression
	case ast.KindNewExpression:
		e = node.AsNewExpression().Expression
	case ast.KindTaggedTemplateExpression:
		e = node.AsTaggedTemplateExpression().Tag
	case ast.KindDecorator:
		e = node.AsDecorator().Expression
		if e != nil && e.Kind == ast.KindCallExpression {
			e = e.AsCallExpression().Expression
		}
	case ast.KindJsxOpeningElement, ast.KindJsxSelfClosingElement:
		e = node.TagName()
	}
	return ast.SkipOuterExpressions(e, ast.OEKAll)
}

// receiverOf returns the object of a property or element access, or nil.
func receiverOf(e *ast.Node) *ast.Node {
	if e == nil {
		return nil
	}
	switch e.Kind {
	case ast.KindPropertyAccessExpression:
		return ast.SkipOuterExpressions(e.AsPropertyAccessExpression().Expression, ast.OEKAll)
	case ast.KindElementAccessExpression:
		return ast.SkipOuterExpressions(e.AsElementAccessExpression().Expression, ast.OEKAll)
	}
	return nil
}

// QualifiedName returns the stable name framework models match on (ADR
// 0008): "<package>:<export path>.<member>", for example
// "express:Response.redirect", "node:child_process.exec",
// "global:JSON.parse" or "local:src/db.ts#runQuery".
func (p *Program) QualifiedName(s SymbolID) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	defer p.recoverQuery("QualifiedName")
	sym, c := p.symbolWithChecker(s)
	if sym == nil || c == nil {
		return ""
	}
	return p.qualifiedName(c, sym, nil)
}

// qualifiedName names sym; for a member reached through a receiver (a
// method call), the receiver's type names the owner, so a method declared on
// an internal base interface is named after the type the code actually
// uses (express's Response, not express-serve-static-core's).
func (p *Program) qualifiedName(c *checker.Checker, sym *ast.Symbol, receiver *ast.Node) string {
	if t := resolveAlias(c, sym); t != nil {
		sym = t
	}
	if roots := c.GetRootSymbols(sym); len(roots) > 0 && roots[0] != sym {
		sym = roots[0]
	}
	if receiver != nil && isMember(sym) {
		// A static or global receiver names the value: Date.now is
		// global:Date.now, not the DateConstructor interface's member.
		if rs := c.GetSymbolAtLocation(receiver); rs != nil {
			if t := resolveAlias(c, rs); t != nil {
				rs = t
			}
			if rs.Flags&(ast.SymbolFlagsClass|ast.SymbolFlagsEnum|ast.SymbolFlagsNamespaceModule|ast.SymbolFlagsValueModule) != 0 || isGlobalAmbientVariable(rs) {
				if prefix := p.qualifiedName(c, rs, nil); prefix != "" {
					return prefix + "." + sym.Name
				}
			}
		}
		if t := c.GetTypeAtLocation(receiver); t != nil {
			owner := t.Symbol()
			if a := t.Alias(); a != nil && a.Symbol() != nil {
				owner = a.Symbol()
			}
			if owner == nil {
				apparent := c.GetApparentType(t)
				owner = apparent.Symbol()
			}
			if owner != nil && owner.Name != ast.InternalSymbolNameType && owner.Name != ast.InternalSymbolNameObject && !strings.HasPrefix(owner.Name, "__") {
				if prefix := p.qualifiedName(c, owner, nil); prefix != "" {
					return prefix + "." + sym.Name
				}
			}
		}
	}

	// Member path: walk parents up to the module or global scope.
	names := []string{exportName(sym)}
	top := sym // the outermost symbol below the module
	var module *ast.Symbol
	for s := sym.Parent; s != nil; s = s.Parent {
		if isModuleSymbol(s) {
			module = s
			break
		}
		if !isInternalName(s.Name) {
			names = append(names, exportName(s))
		}
		top = s
	}
	reverse(names)

	decl := sym.ValueDeclaration
	if decl == nil && len(sym.Declarations) > 0 {
		decl = sym.Declarations[0]
	}
	var file *File
	if decl != nil {
		if sf := ast.GetSourceFileOfNode(decl); sf != nil {
			if id := p.fileOf[sf]; id != 0 {
				file = p.files[id-1]
			}
			// Top-level declarations a module doesn't export with the export
			// keyword (the namespace behind `export = e`) have no parent
			// symbol; their module is the file.
			if module == nil && ast.IsExternalModule(sf) && !inGlobalAugmentation(decl) {
				module = sf.AsNode().Symbol()
			}
		}
	}

	// Anonymous functions and classes have compiler-made names; name them by
	// where they are, which is unique and readable: "(anonymous 12:5)".
	if isInternalName(names[len(names)-1]) && decl != nil && file != nil {
		line, col := file.Position(p.rangeOf(decl, ast.GetSourceFileOfNode(decl)).Pos)
		names[len(names)-1] = fmt.Sprintf("(anonymous %d:%d)", line, col)
	}

	if module != nil {
		// export = e: the module is e, so e's members are the module's.
		if exp := module.Exports[ast.InternalSymbolNameExportEquals]; exp != nil {
			target := exp
			if t := resolveAlias(c, exp); t != nil {
				target = t
			}
			if c.GetMergedSymbol(target) == c.GetMergedSymbol(top) {
				names = names[1:]
				if len(names) == 0 {
					names = []string{"default"}
				}
			}
		}
		if ambient := ambientModuleName(module); ambient != "" {
			return ambientPrefix(ambient, file) + strings.Join(names, ".")
		}
	}
	if file == nil || file.Lib {
		return "global:" + strings.Join(names, ".")
	}
	if module == nil && (file.Declaration || inGlobalAugmentation(decl)) {
		return "global:" + strings.Join(names, ".")
	}
	if file.Package != nil {
		return publicPackageName(file.Package.Name) + ":" + strings.Join(names, ".")
	}
	return "local:" + relPath(p.cfg.Root, file.Path) + "#" + strings.Join(names, ".")
}

// isInternalName reports compiler-made names such as the object literal of
// module.exports ("þobject"); they are not part of a qualified name.
func isInternalName(name string) bool { return strings.HasPrefix(name, "\xfe") }

func exportName(s *ast.Symbol) string {
	if s.ExportSymbol != nil {
		return s.ExportSymbol.Name
	}
	return s.Name
}

func isMember(s *ast.Symbol) bool {
	return s.Flags&(ast.SymbolFlagsMethod|ast.SymbolFlagsProperty|ast.SymbolFlagsGetAccessor|ast.SymbolFlagsSetAccessor) != 0
}

func isModuleSymbol(s *ast.Symbol) bool {
	if s == nil {
		return false
	}
	if s.Flags&ast.SymbolFlagsValueModule != 0 && strings.HasPrefix(s.Name, `"`) {
		return true
	}
	for _, d := range s.Declarations {
		if d.Kind == ast.KindSourceFile {
			return true
		}
	}
	return false
}

// ambientModuleName returns M for `declare module "M"`, or "".
func ambientModuleName(module *ast.Symbol) string {
	for _, d := range module.Declarations {
		if d.Kind == ast.KindModuleDeclaration && strings.HasPrefix(module.Name, `"`) {
			return strings.Trim(module.Name, `"`)
		}
	}
	return ""
}

// ambientPrefix names an ambient module: Node built-ins (declared in
// @types/node, or named node:*) become "node:<module>.", others "<module>:".
func ambientPrefix(name string, file *File) string {
	if bare, ok := strings.CutPrefix(name, "node:"); ok {
		return "node:" + bare + "."
	}
	if file != nil && file.Package != nil && file.Package.Name == "@types/node" {
		return "node:" + name + "."
	}
	return name + ":"
}

// publicPackageName maps a types package to the package it types:
// @types/express -> express, @types/babel__core -> @babel/core. Packages
// that only hold another package's internal types take that package's name.
func publicPackageName(name string) string {
	if bare, ok := strings.CutPrefix(name, "@types/"); ok {
		name = bare
		if scope, pkg, ok := strings.Cut(bare, "__"); ok {
			name = "@" + scope + "/" + pkg
		}
	}
	if public, ok := internalTypesPackages[name]; ok {
		return public
	}
	return name
}

// internalTypesPackages are types packages users never import directly; their
// declarations are named after the package that exposes them (ADR 0008).
var internalTypesPackages = map[string]string{
	"express-serve-static-core": "express",
}

// isGlobalAmbientVariable reports a variable declared in global scope by a
// declaration file: Date, JSON, console, process, Buffer.
func isGlobalAmbientVariable(s *ast.Symbol) bool {
	if s.Flags&ast.SymbolFlagsVariable == 0 || s.ValueDeclaration == nil {
		return false
	}
	sf := ast.GetSourceFileOfNode(s.ValueDeclaration)
	if sf == nil || !sf.IsDeclarationFile {
		return false
	}
	if inGlobalAugmentation(s.ValueDeclaration) {
		return true
	}
	return s.Parent == nil && !ast.IsExternalModule(sf)
}

// inGlobalAugmentation reports whether decl sits inside `declare global { }`.
func inGlobalAugmentation(decl *ast.Node) bool {
	for n := decl; n != nil; n = n.Parent {
		if n.Kind == ast.KindModuleDeclaration && ast.IsGlobalScopeAugmentation(n) {
			return true
		}
	}
	return false
}

func reverse(s []string) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}
