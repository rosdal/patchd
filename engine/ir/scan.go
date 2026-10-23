package ir

import (
	"fmt"
	"strings"

	"github.com/patchd/patchd/engine/tsbridge"
)

// The scan is the builder's pre-pass over a file. It discovers every
// function (one Func per function-like construct, plus synthetic class
// initializers, implicit constructors, namespace bodies and the module
// body), records which function and block scope each declaration and
// reference is in, resolves identifiers to symbols in one tsbridge batch,
// and decides how each variable is stored: a plain SSA value, a cell
// (captured by another function), a global (module level) or a namespace
// member. Lowering then never needs to look ahead.

// fnInfo is a function being built.
type fnInfo struct {
	f        *Func
	node     tsbridge.NodeID // function-like node; the class for ClassInit/StaticInit/implicit constructors; SourceFile; namespace declaration
	parent   int             // index into builder.fns, -1 for the module body
	arrow    bool            // no own this, arguments or new.target
	class    *classInfo      // methods, accessors, constructors and initializers: their class
	caps     []captureKey    // free variables, in FreeVars order
	capIdx   map[captureKey]int
	selfVar  int32 // the name of a named function expression, inside it; -1 if none
	nsVar    int32 // namespace bodies: the namespace object parameter; -1 otherwise
	usesArgs bool
	usesNT   bool
}

type captureKey struct {
	kind FreeVarKind
	v    int32 // the captured variable, for FreeCell
}

func (fi *fnInfo) addCapture(k captureKey) {
	if _, ok := fi.capIdx[k]; ok {
		return
	}
	if fi.capIdx == nil {
		fi.capIdx = map[captureKey]int{}
	}
	fi.capIdx[k] = len(fi.caps)
	fi.caps = append(fi.caps, k)
}

// classInfo is a class being built.
type classInfo struct {
	node     tsbridge.NodeID
	name     string
	ctor     int // index into builder.fns
	init     int // instance field initializers, -1 if none
	static   int // static fields and blocks, -1 if none
	derived  bool
	explicit bool // the constructor is written out
}

// varInfo is a variable: a binding a declaration creates.
type varInfo struct {
	name     string
	owner    int             // index into builder.fns of the function that declares it
	scope    tsbridge.NodeID // block scope it is created in
	kind     declKind
	decl     tsbridge.NodeID // the declaring name, for its type
	global   bool            // module level: load/store global
	captured bool            // referenced from another function: lives in a cell
	nsMember int32           // exported member of a namespace: var index+1 of the namespace object
	exported bool            // the declaration has an export modifier
}

type declKind uint8

const (
	declVar declKind = iota
	declLet
	declConst
	declParam
	declFunction
	declClass
	declCatch
	declImport
	declSelf
	declEnum
	declNamespace
	declNsObject // a namespace body's own parameter
)

// scopeInfo is what a block scope creates when it is entered.
type scopeInfo struct {
	vars  []int32           // variables created here (cells are allocated on entry)
	funcs []tsbridge.NodeID // function declarations hoisted to here
}

type identRec struct {
	node   tsbridge.NodeID
	fn     int
	decl   bool
	kind   declKind
	scope  tsbridge.NodeID
	export bool
	ns     int // for declarations in a namespace body: its fn index; -1 otherwise
}

type scanner struct {
	b      *builder
	t      *tsbridge.Tree
	idents []identRec
}

func (b *builder) scan() {
	s := &scanner{b: b, t: b.tree}
	root := b.tree.Root()
	b.newFn(root, FuncModuleInit, -1, "<module>", nil)
	b.mod.Init = b.fns[0].f
	s.walk(root, 0, root)
	nodes := make([]tsbridge.NodeID, len(s.idents))
	for i, r := range s.idents {
		nodes[i] = r.node
	}
	syms := b.prog.ValueSymbols(nodes)
	s.bind(syms)
}

// newFn creates a function shell.
func (b *builder) newFn(node tsbridge.NodeID, kind FuncKind, parent int, name string, ci *classInfo) int {
	id := FuncID(len(b.fns) + 1) //nolint:gosec // a file has far fewer than 2^32 functions
	f := &Func{ID: id, Name: name, Kind: kind, Source: b.tree.Range(node), Syntax: node}
	if parent >= 0 {
		f.Parent = b.fns[parent].f.ID
	}
	fi := &fnInfo{f: f, node: node, parent: parent, arrow: kind == FuncArrow, class: ci, selfVar: -1, nsVar: -1}
	b.fns = append(b.fns, fi)
	b.mod.Funcs = append(b.mod.Funcs, f)
	return len(b.fns) - 1
}

// skipped reports syntax with no runtime code: types, JSDoc, type-only and
// ambient declarations. Lowering ignores it and coverage doesn't count it.
func (b *builder) skipped(n tsbridge.NodeID) bool {
	switch b.tree.Kind(n) {
	case tsbridge.KindTypeNode, tsbridge.KindJSDoc, tsbridge.KindTypeParameter, tsbridge.KindInterfaceDeclaration,
		tsbridge.KindTypeAliasDeclaration, tsbridge.KindToken, tsbridge.KindModifier, tsbridge.KindPropertySignature,
		tsbridge.KindMethodSignature, tsbridge.KindNamespaceExportDeclaration:
		return true
	case tsbridge.KindModuleDeclaration:
		return b.tree.Flags(n)&tsbridge.FlagAmbient != 0 || !b.instantiated(n)
	case tsbridge.KindSourceFile:
		return false
	case tsbridge.KindOther:
		switch b.tree.CompilerKind(n) {
		case "JSTypeAliasDeclaration", "JSImportDeclaration", "NotEmittedStatement", "NotEmittedTypeElement":
			return true // JSDoc @typedef and @import, reparsed into the tree of a JS file
		}
		return false
	case tsbridge.KindPropertyDeclaration:
		if b.tree.Flags(n)&tsbridge.FlagAbstract != 0 {
			return true
		}
	case tsbridge.KindHeritageClause:
		return b.tree.Operator(n) == "implements"
	case tsbridge.KindImportDeclaration, tsbridge.KindImportEqualsDeclaration, tsbridge.KindExportDeclaration:
		return false // type-only imports are recorded, not lowered
	}
	f := b.tree.Flags(n)
	if f&tsbridge.FlagDeclare != 0 {
		return true
	}
	if f&tsbridge.FlagAmbient != 0 {
		return true
	}
	return false
}

// instantiated reports a namespace with runtime content: anything but types
// and non-instantiated namespaces.
func (b *builder) instantiated(n tsbridge.NodeID) bool {
	for _, c := range b.tree.Children(n) {
		switch b.tree.Kind(c) {
		case tsbridge.KindModuleBlock:
			for _, st := range b.tree.Children(c) {
				switch b.tree.Kind(st) {
				case tsbridge.KindInterfaceDeclaration, tsbridge.KindTypeAliasDeclaration, tsbridge.KindJSDoc:
				case tsbridge.KindModuleDeclaration:
					if b.tree.Flags(st)&tsbridge.FlagAmbient == 0 && b.instantiated(st) {
						return true
					}
				case tsbridge.KindImportEqualsDeclaration, tsbridge.KindImportDeclaration, tsbridge.KindExportDeclaration:
					if b.tree.Flags(st)&tsbridge.FlagTypeOnly == 0 {
						return true
					}
				default:
					if b.tree.Flags(st)&(tsbridge.FlagAmbient|tsbridge.FlagDeclare) == 0 {
						return true
					}
				}
			}
		case tsbridge.KindModuleDeclaration:
			return b.instantiated(c)
		}
	}
	return false
}

func (s *scanner) ident(n tsbridge.NodeID, fn int) {
	if s.t.Kind(n) == tsbridge.KindIdentifier {
		s.idents = append(s.idents, identRec{node: n, fn: fn, ns: -1})
	}
}

func (s *scanner) declare(n tsbridge.NodeID, fn int, kind declKind, scope tsbridge.NodeID, export bool) {
	switch s.t.Kind(n) {
	case tsbridge.KindIdentifier:
		ns := -1
		if s.b.fns[fn].f.Kind == FuncNamespace && export {
			ns = fn
		}
		s.idents = append(s.idents, identRec{node: n, fn: fn, decl: true, kind: kind, scope: scope, export: export, ns: ns})
	case tsbridge.KindObjectBindingPattern, tsbridge.KindArrayBindingPattern:
		for _, el := range s.t.Children(n) {
			if s.t.Kind(el) != tsbridge.KindBindingElement {
				continue
			}
			for _, c := range s.t.Children(el) {
				switch s.t.Role(c) {
				case tsbridge.RoleName:
					s.declare(c, fn, kind, scope, export)
				case tsbridge.RolePropertyName:
					if s.t.Kind(c) == tsbridge.KindComputedPropertyName {
						s.walk(c, fn, scope)
					}
				case tsbridge.RoleInitializer:
					s.walk(c, fn, scope)
				}
			}
		}
	}
}

// varScope returns the function-level scope node of fn: where var declarations live.
func (s *scanner) varScope(fn int, scope tsbridge.NodeID) tsbridge.NodeID {
	fi := s.b.fns[fn]
	switch fi.f.Kind {
	case FuncClassInit, FuncStaticInit:
		return scope // static blocks: their own block
	}
	return fi.node
}

func (s *scanner) walk(n tsbridge.NodeID, fn int, scope tsbridge.NodeID) {
	t := s.t
	if s.b.skipped(n) {
		return
	}
	switch k := t.Kind(n); k {
	case tsbridge.KindIdentifier:
		s.ident(n, fn)
		if t.Value(n) == "arguments" {
			s.b.nodeFn[n] = fn
		}
	case tsbridge.KindThis, tsbridge.KindSuper:
		s.b.nodeFn[n] = fn
		s.useThis(fn)
	case tsbridge.KindMetaProperty:
		if t.Value(n) == "new.target" {
			s.b.nodeFn[n] = fn
			s.useNewTarget(fn)
		}
	case tsbridge.KindFunctionDeclaration, tsbridge.KindFunctionExpression, tsbridge.KindArrowFunction:
		s.walkFunction(n, fn, scope, nil)
	case tsbridge.KindClassDeclaration, tsbridge.KindClassExpression:
		s.walkClass(n, fn, scope)
	case tsbridge.KindModuleDeclaration:
		s.walkNamespace(n, fn, scope)
	case tsbridge.KindEnumDeclaration:
		for _, c := range t.Children(n) {
			switch t.Kind(c) {
			case tsbridge.KindIdentifier:
				s.declare(c, fn, declEnum, scope, t.Flags(n)&tsbridge.FlagExport != 0)
			case tsbridge.KindEnumMember:
				for _, mc := range t.Children(c) {
					if !isNameKind(t.Kind(mc)) {
						s.walk(mc, fn, scope)
					}
				}
			}
		}
	case tsbridge.KindVariableStatement:
		export := t.Flags(n)&tsbridge.FlagExport != 0
		for _, c := range t.Children(n) {
			if t.Kind(c) == tsbridge.KindVariableDeclarationList {
				s.walkDeclList(c, fn, scope, export)
			}
		}
	case tsbridge.KindVariableDeclarationList:
		s.walkDeclList(n, fn, scope, false)
	case tsbridge.KindCatchClause:
		for _, c := range t.Children(n) {
			if t.Kind(c) == tsbridge.KindVariableDeclaration {
				kids := t.Children(c)
				if len(kids) > 0 {
					s.declare(kids[0], fn, declCatch, n, false)
				}
				for _, cc := range kids[1:] {
					s.walk(cc, fn, n)
				}
			} else {
				s.walk(c, fn, n)
			}
		}
	case tsbridge.KindBlock, tsbridge.KindCaseBlock, tsbridge.KindModuleBlock,
		tsbridge.KindForStatement, tsbridge.KindForInStatement, tsbridge.KindForOfStatement:
		for _, c := range t.Children(n) {
			s.walk(c, fn, n)
		}
	case tsbridge.KindPropertyAccessExpression:
		kids := t.Children(n)
		if len(kids) > 0 {
			s.walk(kids[0], fn, scope)
		}
	case tsbridge.KindQualifiedName:
		kids := t.Children(n)
		if len(kids) > 0 {
			s.walk(kids[0], fn, scope)
		}
	case tsbridge.KindLabeledStatement, tsbridge.KindBreakStatement, tsbridge.KindContinueStatement:
		for _, c := range t.Children(n) {
			if t.Kind(c) != tsbridge.KindIdentifier {
				s.walk(c, fn, scope)
			}
		}
	case tsbridge.KindPropertyAssignment, tsbridge.KindJsxAttribute, tsbridge.KindEnumMember:
		for i, c := range t.Children(n) {
			if i == 0 && isNameKind(t.Kind(c)) {
				continue
			}
			s.walk(c, fn, scope)
		}
	case tsbridge.KindMethodDeclaration, tsbridge.KindGetAccessor, tsbridge.KindSetAccessor, tsbridge.KindConstructor:
		s.walkFunction(n, fn, scope, nil) // object literal methods
	case tsbridge.KindJsxOpeningElement, tsbridge.KindJsxSelfClosingElement, tsbridge.KindJsxClosingElement:
		for i, c := range t.Children(n) {
			if i == 0 && k != tsbridge.KindJsxClosingElement && !jsxIntrinsic(t, c) {
				s.walk(c, fn, scope)
			} else if i > 0 {
				s.walk(c, fn, scope)
			}
		}
	case tsbridge.KindImportDeclaration:
		s.walkImport(n, fn, scope)
	case tsbridge.KindImportEqualsDeclaration:
		kids := t.Children(n)
		for _, c := range kids {
			switch t.Kind(c) {
			case tsbridge.KindIdentifier:
				if t.Flags(n)&tsbridge.FlagTypeOnly == 0 {
					s.declare(c, fn, declImport, scope, t.Flags(n)&tsbridge.FlagExport != 0)
				}
			case tsbridge.KindQualifiedName:
				if t.Flags(n)&tsbridge.FlagTypeOnly == 0 {
					s.walk(c, fn, scope)
				}
			}
		}
	case tsbridge.KindExportDeclaration:
		if t.Flags(n)&tsbridge.FlagTypeOnly != 0 {
			return
		}
		local := true
		for _, c := range t.Children(n) {
			if t.Kind(c) == tsbridge.KindStringLiteral {
				local = false
			}
		}
		if !local {
			return
		}
		for _, c := range t.Children(n) {
			if t.Kind(c) != tsbridge.KindNamedExports {
				continue
			}
			for _, spec := range t.Children(c) {
				if t.Flags(spec)&tsbridge.FlagTypeOnly != 0 {
					continue
				}
				if l := specLocal(t, spec); l != 0 {
					s.ident(l, fn)
				}
			}
		}
	case tsbridge.KindNamespaceExport, tsbridge.KindExportSpecifier:
	default:
		for _, c := range t.Children(n) {
			s.walk(c, fn, scope)
		}
	}
}

// specLocal returns the local side of an import or export specifier: the
// property name of `a as b` for exports, the name for imports.
func specLocal(t *tsbridge.Tree, spec tsbridge.NodeID) tsbridge.NodeID {
	var prop, name tsbridge.NodeID
	for _, c := range t.Children(spec) {
		switch t.Role(c) {
		case tsbridge.RolePropertyName:
			prop = c
		case tsbridge.RoleName:
			name = c
		}
	}
	if t.Kind(spec) == tsbridge.KindExportSpecifier && prop != 0 {
		return prop
	}
	return name
}

func isNameKind(k tsbridge.Kind) bool {
	switch k {
	case tsbridge.KindIdentifier, tsbridge.KindPrivateIdentifier, tsbridge.KindStringLiteral, tsbridge.KindNumericLiteral,
		tsbridge.KindNoSubstitutionTemplateLiteral, tsbridge.KindBigIntLiteral, tsbridge.KindJsxNamespacedName:
		return true
	}
	return false
}

// jsxIntrinsic reports a JSX tag name that is an HTML element (div), not a component.
func jsxIntrinsic(t *tsbridge.Tree, tag tsbridge.NodeID) bool {
	switch t.Kind(tag) {
	case tsbridge.KindIdentifier:
		v := t.Value(tag)
		return v != "" && (v[0] >= 'a' && v[0] <= 'z' || strings.Contains(v, "-"))
	case tsbridge.KindJsxNamespacedName:
		return true
	}
	return false
}

func (s *scanner) walkDeclList(n tsbridge.NodeID, fn int, scope tsbridge.NodeID, export bool) {
	t := s.t
	kind := declVar
	f := t.Flags(n)
	switch {
	case f&tsbridge.FlagConst != 0, f&(tsbridge.FlagUsing|tsbridge.FlagAwaitUsing) != 0:
		kind = declConst
	case f&tsbridge.FlagLet != 0:
		kind = declLet
	}
	declScope := scope
	if kind == declVar {
		declScope = s.varScope(fn, scope)
	}
	for _, d := range t.Children(n) {
		if t.Kind(d) != tsbridge.KindVariableDeclaration {
			continue
		}
		kids := t.Children(d)
		if len(kids) == 0 {
			continue
		}
		s.declare(kids[0], fn, kind, declScope, export)
		for _, c := range kids[1:] {
			s.walk(c, fn, scope)
		}
	}
}

func (s *scanner) walkImport(n tsbridge.NodeID, fn int, scope tsbridge.NodeID) {
	t := s.t
	for _, c := range t.Children(n) {
		if t.Kind(c) != tsbridge.KindImportClause || t.Flags(c)&tsbridge.FlagTypeOnly != 0 {
			continue
		}
		for _, cc := range t.Children(c) {
			switch t.Kind(cc) {
			case tsbridge.KindIdentifier:
				s.declare(cc, fn, declImport, scope, false)
			case tsbridge.KindNamespaceImport:
				for _, id := range t.Children(cc) {
					s.declare(id, fn, declImport, scope, false)
				}
			case tsbridge.KindNamedImports:
				for _, spec := range t.Children(cc) {
					if t.Flags(spec)&tsbridge.FlagTypeOnly != 0 {
						continue
					}
					if l := specLocal(t, spec); l != 0 {
						s.declare(l, fn, declImport, scope, false)
					}
				}
			}
		}
	}
}

// useThis records that fn reads this (or super): arrows capture it from the
// nearest function with its own this.
func (s *scanner) useThis(fn int) {
	for f := fn; f >= 0 && s.b.fns[f].arrow; f = s.b.fns[f].parent {
		s.b.fns[f].addCapture(captureKey{kind: FreeThis})
	}
}

func (s *scanner) useNewTarget(fn int) {
	f := fn
	for ; f >= 0 && s.b.fns[f].arrow; f = s.b.fns[f].parent {
		s.b.fns[f].addCapture(captureKey{kind: FreeNewTarget})
	}
	if f >= 0 {
		s.b.fns[f].usesNT = true
	}
}

// walkFunction discovers a function-like node and walks its parameters and
// body in its own function. Decorators and computed names run in the
// enclosing function.
func (s *scanner) walkFunction(n tsbridge.NodeID, fn int, scope tsbridge.NodeID, ci *classInfo) int {
	t := s.t
	k := t.Kind(n)
	body := tsbridge.NodeID(0)
	for _, c := range t.Children(n) {
		switch t.Kind(c) {
		case tsbridge.KindBlock:
			body = c
		default:
			if k == tsbridge.KindArrowFunction && isExpressionKind(t.Kind(c)) {
				body = c
			}
		}
	}
	abstract := t.Flags(n)&tsbridge.FlagAbstract != 0
	if body == 0 && !abstract && k != tsbridge.KindArrowFunction {
		s.b.marks[t.Index(n)] = markLowered // an overload signature: no runtime code
		return -1
	}
	kind := FuncDeclaration
	switch k {
	case tsbridge.KindArrowFunction:
		kind = FuncArrow
	case tsbridge.KindMethodDeclaration:
		kind = FuncMethod
	case tsbridge.KindGetAccessor:
		kind = FuncGetter
	case tsbridge.KindSetAccessor:
		kind = FuncSetter
	case tsbridge.KindConstructor:
		kind = FuncConstructor
	}
	var idx int
	if existing, ok := s.b.fnOf[n]; ok {
		idx = existing // a constructor created by walkClass
	} else {
		idx = s.b.newFn(n, kind, fn, s.b.funcName(n, fn, ci), ci)
		s.b.fnOf[n] = idx
	}
	fi := s.b.fns[idx]
	if t.Flags(n)&tsbridge.FlagAsync != 0 {
		fi.f.Flags |= FuncAsync
	}
	if t.Flags(n)&tsbridge.FlagGenerator != 0 {
		fi.f.Flags |= FuncGenerator
	}
	if t.Flags(n)&tsbridge.FlagStatic != 0 {
		fi.f.Flags |= FuncStatic
	}
	if abstract && body == 0 {
		fi.f.Flags |= FuncAbstract
	}
	if k == tsbridge.KindFunctionDeclaration && s.b.nameOfDecl(n) == 0 {
		si := s.b.scope(scope) // export default function () {}: hoisted, no binding
		si.funcs = append(si.funcs, n)
	}
	for _, c := range t.Children(n) {
		if c == body {
			s.walk(c, idx, n)
			continue
		}
		switch t.Kind(c) {
		case tsbridge.KindDecorator:
			s.walk(c, fn, scope)
		case tsbridge.KindIdentifier:
			switch k {
			case tsbridge.KindFunctionDeclaration:
				owner, sc := fn, scope
				s.declare(c, owner, declFunction, sc, t.Flags(n)&tsbridge.FlagExport != 0)
				si := s.b.scope(sc)
				si.funcs = append(si.funcs, n)
			case tsbridge.KindFunctionExpression:
				s.declare(c, idx, declSelf, n, false)
			}
		case tsbridge.KindComputedPropertyName:
			s.walk(c, fn, scope)
		case tsbridge.KindParameter:
			for _, pc := range t.Children(c) {
				switch t.Kind(pc) {
				case tsbridge.KindDecorator:
					s.walk(pc, fn, scope)
				case tsbridge.KindIdentifier, tsbridge.KindObjectBindingPattern, tsbridge.KindArrayBindingPattern:
					s.declare(pc, idx, declParam, n, false)
				default:
					s.walk(pc, idx, n)
				}
			}
		}
	}
	return idx
}

// walkClass discovers a class's constructor (written or implicit), its
// initializer functions and its members.
func (s *scanner) walkClass(n tsbridge.NodeID, fn int, scope tsbridge.NodeID) {
	t := s.t
	ci := &classInfo{node: n, init: -1, static: -1}
	ci.name = s.b.className(n)
	s.b.classes[n] = ci
	var ctorNode tsbridge.NodeID
	hasInit, hasStatic := false, false
	for _, c := range t.Children(n) {
		switch t.Kind(c) {
		case tsbridge.KindConstructor:
			if s.hasBody(c) {
				ctorNode = c
			}
		case tsbridge.KindPropertyDeclaration:
			if s.b.skipped(c) {
				continue
			}
			if t.Flags(c)&tsbridge.FlagStatic != 0 {
				hasStatic = true
			} else {
				hasInit = true
			}
		case tsbridge.KindClassStaticBlockDeclaration:
			hasStatic = true
		case tsbridge.KindHeritageClause:
			if t.Operator(c) == "extends" {
				ci.derived = true
			}
		}
	}
	prefix := s.b.prefix(fn)
	if ctorNode != 0 {
		ci.explicit = true
		ci.ctor = s.b.newFn(ctorNode, FuncConstructor, fn, prefix+ci.name+".constructor", ci)
		s.b.fnOf[ctorNode] = ci.ctor
	} else {
		ci.ctor = s.b.newFn(n, FuncConstructor, fn, prefix+ci.name+".constructor", ci)
		s.b.fns[ci.ctor].f.Flags |= FuncImplicit
	}
	if ci.derived {
		s.b.fns[ci.ctor].f.Flags |= FuncDerived
	}
	if hasInit {
		ci.init = s.b.newFn(n, FuncClassInit, ci.ctor, prefix+ci.name+".<init>", ci)
	}
	if hasStatic {
		ci.static = s.b.newFn(n, FuncStaticInit, fn, prefix+ci.name+".<static>", ci)
	}
	for _, c := range t.Children(n) {
		if s.b.skipped(c) {
			continue
		}
		switch t.Kind(c) {
		case tsbridge.KindIdentifier:
			kind := declClass
			if t.Kind(n) == tsbridge.KindClassExpression {
				s.declare(c, fn, declLet, n, false)
				continue
			}
			s.declare(c, fn, kind, scope, t.Flags(n)&tsbridge.FlagExport != 0)
		case tsbridge.KindDecorator:
			s.walk(c, fn, scope)
		case tsbridge.KindHeritageClause:
			for _, e := range t.Children(c) {
				if kids := t.Children(e); len(kids) > 0 {
					s.walk(kids[0], fn, scope)
				}
			}
		case tsbridge.KindConstructor:
			if c == ctorNode {
				s.walkFunction(c, fn, scope, ci)
			} else {
				s.b.marks[t.Index(c)] = markLowered
			}
		case tsbridge.KindMethodDeclaration, tsbridge.KindGetAccessor, tsbridge.KindSetAccessor:
			s.walkFunction(c, fn, scope, ci)
		case tsbridge.KindPropertyDeclaration:
			target := ci.init
			if t.Flags(c)&tsbridge.FlagStatic != 0 {
				target = ci.static
			}
			for i, pc := range t.Children(c) {
				switch {
				case t.Kind(pc) == tsbridge.KindDecorator:
					s.walk(pc, fn, scope)
				case i == 0 && isNameKind(t.Kind(pc)), t.Kind(pc) == tsbridge.KindPrivateIdentifier:
				default:
					s.walk(pc, target, n)
				}
			}
		case tsbridge.KindClassStaticBlockDeclaration:
			for _, bc := range t.Children(c) {
				s.walk(bc, ci.static, bc)
			}
		}
	}
}

func (s *scanner) hasBody(n tsbridge.NodeID) bool {
	for _, c := range s.t.Children(n) {
		if s.t.Kind(c) == tsbridge.KindBlock {
			return true
		}
	}
	return false
}

// walkNamespace discovers a namespace body as a function called with the
// namespace object.
func (s *scanner) walkNamespace(n tsbridge.NodeID, fn int, scope tsbridge.NodeID) {
	t := s.t
	var name, body tsbridge.NodeID
	for _, c := range t.Children(n) {
		switch t.Kind(c) {
		case tsbridge.KindIdentifier:
			name = c
		case tsbridge.KindModuleBlock, tsbridge.KindModuleDeclaration:
			body = c
		}
	}
	if name == 0 {
		return
	}
	nested := t.Kind(t.Parent(n)) == tsbridge.KindModuleDeclaration
	export := nested || t.Flags(n)&tsbridge.FlagExport != 0
	s.declare(name, fn, declNamespace, scope, export)
	idx := s.b.newFn(n, FuncNamespace, fn, s.b.prefix(fn)+t.Value(name), nil)
	s.b.nsOf[n] = idx
	fi := s.b.fns[idx]
	v := s.b.addVar(varInfo{name: t.Value(name), owner: idx, scope: n, kind: declNsObject, decl: name})
	fi.nsVar = v
	if body == 0 {
		return
	}
	if t.Kind(body) == tsbridge.KindModuleDeclaration {
		s.walkNamespace(body, idx, n)
		return
	}
	for _, c := range t.Children(body) {
		s.walk(c, idx, body)
	}
}

// nameOfDecl returns the name identifier of a function or class declaration.
func (b *builder) nameOfDecl(n tsbridge.NodeID) tsbridge.NodeID {
	for _, c := range b.tree.Children(n) {
		switch b.tree.Kind(c) {
		case tsbridge.KindIdentifier:
			return c
		case tsbridge.KindParameter, tsbridge.KindBlock:
			return 0
		}
	}
	return 0
}

func (b *builder) scope(n tsbridge.NodeID) *scopeInfo {
	si := b.scopes[n]
	if si == nil {
		si = &scopeInfo{}
		b.scopes[n] = si
	}
	return si
}

func (b *builder) addVar(v varInfo) int32 {
	b.vars = append(b.vars, v)
	idx := int32(len(b.vars) - 1) //nolint:gosec // a file has far fewer than 2^31 variables
	b.scope(v.scope).vars = append(b.scope(v.scope).vars, idx)
	return idx
}

// bind turns the scanned identifiers and their symbols into variables and
// captures.
func (s *scanner) bind(syms []tsbridge.SymbolID) {
	b := s.b
	declOf := map[tsbridge.NodeID]int32{}
	for i, r := range s.idents {
		if !r.decl {
			continue
		}
		sym := syms[i]
		if v, ok := b.symVar[sym]; ok && sym != 0 {
			b.idVar[r.node] = v + 1
			declOf[r.node] = v
			if r.export {
				b.vars[v].exported = true
			}
			continue
		}
		vi := varInfo{name: s.t.Value(r.node), owner: r.fn, scope: r.scope, kind: r.kind, decl: r.node, exported: r.export}
		if r.fn == 0 && (r.scope == s.t.Root() || r.kind == declVar || r.kind == declImport || r.kind == declFunction && r.scope == s.t.Root()) {
			vi.global = true
		}
		if r.kind == declImport {
			vi.global = true
		}
		if r.ns >= 0 {
			vi.nsMember = b.fns[r.ns].nsVar + 1
		}
		v := b.addVar(vi)
		if sym != 0 {
			b.symVar[sym] = v
		}
		b.idVar[r.node] = v + 1
		declOf[r.node] = v
		if r.kind == declSelf {
			b.fns[r.fn].selfVar = v
		}
	}
	for i, r := range s.idents {
		if r.decl {
			if v, ok := declOf[r.node]; ok {
				s.capture(r.fn, v, r.node)
			}
			continue
		}
		v, ok := b.symVar[syms[i]]
		if !ok || syms[i] == 0 {
			if s.t.Value(r.node) == "arguments" {
				s.useArguments(r.fn, r.node)
			}
			continue
		}
		if !s.capture(r.fn, v, r.node) {
			continue
		}
		b.idVar[r.node] = v + 1
	}
}

// capture records a use of variable v in function fn, making it a cell and
// adding it to the free variables of every function between fn and its
// owner. It returns false when v is not visible from fn (the use is then a
// global by name).
func (s *scanner) capture(fn int, v int32, n tsbridge.NodeID) bool {
	b := s.b
	vi := &b.vars[v]
	if vi.global {
		return true
	}
	target := v
	owner := vi.owner
	if vi.nsMember > 0 {
		target = vi.nsMember - 1
		owner = b.vars[target].owner
	}
	if fn == owner {
		return true
	}
	for f := fn; f != owner; f = b.fns[f].parent {
		if f < 0 {
			return false
		}
	}
	b.vars[target].captured = true
	for f := fn; f != owner; f = b.fns[f].parent {
		b.fns[f].addCapture(captureKey{kind: FreeCell, v: target})
	}
	_ = n
	return true
}

func (s *scanner) useArguments(fn int, n tsbridge.NodeID) {
	f := fn
	for ; f >= 0 && s.b.fns[f].arrow; f = s.b.fns[f].parent {
		s.b.fns[f].addCapture(captureKey{kind: FreeArguments})
	}
	if f > 0 {
		s.b.fns[f].usesArgs = true
		s.b.special[n] = FreeArguments
	}
}

// --- names ---

// prefix returns the name prefix for functions nested in fn: "outer." for a
// named enclosing function, "" at module level.
func (b *builder) prefix(fn int) string {
	if fn <= 0 {
		return ""
	}
	return b.fns[fn].f.Name + "."
}

// className names a class: its declared name, the variable or property it is
// assigned to, or its position.
func (b *builder) className(n tsbridge.NodeID) string {
	for _, c := range b.tree.Children(n) {
		if b.tree.Kind(c) == tsbridge.KindIdentifier {
			return b.tree.Value(c)
		}
	}
	if name := b.inferredName(n); name != "" {
		return name
	}
	if b.tree.Flags(n)&tsbridge.FlagDefault != 0 {
		return "default"
	}
	return b.anon("class", n)
}

func (b *builder) anon(what string, n tsbridge.NodeID) string {
	line, col := b.file.Position(b.tree.Range(n).Pos)
	if what == "" {
		return fmt.Sprintf("<anon@%d:%d>", line, col)
	}
	return fmt.Sprintf("<%s@%d:%d>", what, line, col)
}

// funcName names a function for printing and traces.
func (b *builder) funcName(n tsbridge.NodeID, fn int, ci *classInfo) string {
	t := b.tree
	k := t.Kind(n)
	prefix := b.prefix(fn)
	member := ""
	for _, c := range t.Children(n) {
		if k == tsbridge.KindArrowFunction {
			break
		}
		ck := t.Kind(c)
		if ck == tsbridge.KindComputedPropertyName {
			member = t.Text(c)
			break
		}
		if isNameKind(ck) {
			member = t.Value(c)
			break
		}
		if ck == tsbridge.KindParameter || ck == tsbridge.KindBlock {
			break
		}
	}
	switch k {
	case tsbridge.KindMethodDeclaration, tsbridge.KindGetAccessor, tsbridge.KindSetAccessor:
		switch k {
		case tsbridge.KindGetAccessor:
			member = "get " + member
		case tsbridge.KindSetAccessor:
			member = "set " + member
		}
		if ci != nil {
			return prefix + ci.name + "." + member
		}
		if obj := b.objectName(t.Parent(n)); obj != "" {
			return prefix + obj + "." + member
		}
		return prefix + member
	}
	if member != "" {
		return prefix + member
	}
	if name := b.inferredName(n); name != "" {
		return prefix + name
	}
	if t.Flags(n)&tsbridge.FlagDefault != 0 {
		return "default"
	}
	return prefix + b.anon("", n)
}

// inferredName is the name JavaScript gives an anonymous function or class
// from where it is assigned: const f = …, { f: … }, x.f = …, export default.
func (b *builder) inferredName(n tsbridge.NodeID) string {
	t := b.tree
	p := t.Parent(n)
	for t.Kind(p) == tsbridge.KindParenthesizedExpression || t.Kind(p) == tsbridge.KindAsExpression ||
		t.Kind(p) == tsbridge.KindSatisfiesExpression {
		n, p = p, t.Parent(p)
	}
	switch t.Kind(p) {
	case tsbridge.KindVariableDeclaration, tsbridge.KindPropertyDeclaration, tsbridge.KindParameter, tsbridge.KindBindingElement:
		kids := t.Children(p)
		if len(kids) > 0 && kids[0] != n && isNameKind(t.Kind(kids[0])) {
			return t.Value(kids[0])
		}
		if len(kids) > 0 && kids[0] != n && t.Role(kids[0]) == tsbridge.RoleName && t.Kind(kids[0]) == tsbridge.KindIdentifier {
			return t.Value(kids[0])
		}
	case tsbridge.KindPropertyAssignment:
		kids := t.Children(p)
		if len(kids) > 0 && isNameKind(t.Kind(kids[0])) {
			if obj := b.objectName(t.Parent(p)); obj != "" {
				return obj + "." + t.Value(kids[0])
			}
			return t.Value(kids[0])
		}
	case tsbridge.KindBinaryExpression:
		kids := t.Children(p)
		if len(kids) == 3 && kids[2] == n && t.Operator(p) == "=" {
			switch t.Kind(kids[0]) {
			case tsbridge.KindIdentifier, tsbridge.KindPropertyAccessExpression:
				return strings.TrimPrefix(t.Text(kids[0]), "this.")
			}
		}
	case tsbridge.KindExportAssignment:
		return "default"
	}
	return ""
}

// objectName is the variable an object literal is assigned to, if any.
func (b *builder) objectName(obj tsbridge.NodeID) string {
	t := b.tree
	if t.Kind(obj) != tsbridge.KindObjectLiteralExpression {
		return ""
	}
	return b.inferredName(obj)
}

func isExpressionKind(k tsbridge.Kind) bool {
	switch k {
	case tsbridge.KindToken, tsbridge.KindModifier, tsbridge.KindTypeNode, tsbridge.KindJSDoc, tsbridge.KindTypeParameter,
		tsbridge.KindParameter, tsbridge.KindDecorator, tsbridge.KindBlock, tsbridge.KindInvalid:
		return false
	}
	return true
}
