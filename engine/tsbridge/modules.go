package tsbridge

import (
	"path"
	"strings"

	"github.com/microsoft/typescript-go/shim/ast"
	"github.com/microsoft/typescript-go/shim/checker"
)

// nodeBuiltins are Node's built-in modules. Their types come from @types/node.
var nodeBuiltins = map[string]bool{}

func init() {
	for _, m := range strings.Fields(`assert async_hooks buffer child_process cluster console constants crypto
		dgram diagnostics_channel dns domain events fs http http2 https inspector module net os path
		perf_hooks process punycode querystring readline repl stream string_decoder sys timers tls
		trace_events tty url util v8 vm wasi worker_threads zlib test sqlite sea`) {
		nodeBuiltins[m] = true
	}
}

// isNodeBuiltin reports whether a module specifier names a Node built-in
// (fs, node:fs, fs/promises).
func isNodeBuiltin(spec string) bool {
	if strings.HasPrefix(spec, "node:") {
		return true
	}
	base, _, _ := strings.Cut(spec, "/")
	return nodeBuiltins[base]
}

// packageInstalled reports whether pkg is installed in a node_modules
// directory at or above dir. Callers hold p.mu.
func (p *Program) packageInstalled(dir, pkg string) bool {
	key := dir + "\x00" + pkg
	if v, ok := p.installed[key]; ok {
		return v
	}
	found := false
	for d := dir; ; d = path.Dir(d) {
		if fileExists(path.Join(d, "node_modules", pkg, "package.json")) {
			found = true
			break
		}
		if path.Dir(d) == d {
			break
		}
	}
	p.installed[key] = found
	return found
}

// importReason explains an import whose target the checker can't see: the
// package (or @types/node, for a built-in) is not installed, or it is
// installed but ships no types. "" when sym is not an import of a package.
// Callers hold p.mu.
func (p *Program) importReason(sym *ast.Symbol) UnresolvedReason {
	if sym == nil || len(sym.Declarations) == 0 {
		return ""
	}
	decl := sym.Declarations[0]
	switch decl.Kind {
	case ast.KindVariableDeclaration, ast.KindBindingElement:
		if !ast.IsVariableDeclarationInitializedToRequire(decl) {
			return ""
		}
	case ast.KindImportEqualsDeclaration, ast.KindImportClause, ast.KindNamespaceExport,
		ast.KindNamespaceImport, ast.KindExportSpecifier, ast.KindImportSpecifier:
	default:
		return "" // the compiler helper below asserts on any other kind
	}
	specNode := checker.TryGetModuleSpecifierFromDeclaration(decl)
	if specNode == nil || !ast.IsStringLiteralLike(specNode) {
		return ""
	}
	spec := specNode.Text()
	if strings.HasPrefix(spec, ".") || strings.HasPrefix(spec, "/") {
		return ReasonNoDeclaration
	}
	dir := "/"
	if sf := ast.GetSourceFileOfNode(decl); sf != nil {
		dir = path.Dir(sf.FileName())
	}
	if isNodeBuiltin(spec) {
		if p.packageInstalled(dir, "@types/node") {
			return ""
		}
		return ReasonPackageNotInstalled
	}
	if !p.packageInstalled(dir, packageName(spec)) {
		return ReasonPackageNotInstalled
	}
	return ReasonUntypedPackage
}

// followRequire follows `const x = require("m")` and
// `const x = require("m").y` in JavaScript to the symbol required, which the
// checker binds as a plain variable when a property is accessed on the call.
func followRequire(c *checker.Checker, sym *ast.Symbol) *ast.Symbol {
	for range 8 {
		if sym == nil || sym.ValueDeclaration == nil || sym.ValueDeclaration.Kind != ast.KindVariableDeclaration {
			return sym
		}
		init := sym.ValueDeclaration.Initializer()
		if init == nil {
			return sym
		}
		init = ast.SkipOuterExpressions(init, ast.OEKAll)
		base := init
		for base.Kind == ast.KindPropertyAccessExpression {
			base = ast.SkipOuterExpressions(base.AsPropertyAccessExpression().Expression, ast.OEKAll)
		}
		if !ast.IsRequireCall(base, true) || base == init {
			return sym
		}
		next := c.GetSymbolAtLocation(init.Name())
		if next == nil || next == sym || c.IsUnknownSymbol(next) {
			return sym
		}
		if t := resolveAlias(c, next); t != nil {
			next = t
		}
		sym = next
	}
	return sym
}
