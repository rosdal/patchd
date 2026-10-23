package tsbridge

import (
	"github.com/microsoft/typescript-go/shim/ast"
	"github.com/microsoft/typescript-go/shim/scanner"
)

// Tree is a read-only view of one file's syntax for code that walks every
// node, such as the IR builder. It answers the same questions as the
// Program's syntax methods, plus the literal values, operators, flags and
// field roles a lowering pass needs, without taking the Program's lock: what
// it reads is computed when the Tree is made or never changes after parsing,
// so builders on many goroutines don't contend on the program. A Tree is
// valid until the Program is closed.
type Tree struct {
	file  *File
	sf    *ast.SourceFile
	t     *tree
	kinds []Kind
	flags []SyntaxFlags
	roles []Role
}

// SyntaxFlags are facts about a node that its kind and children don't show.
type SyntaxFlags uint32

// Syntax flags.
const (
	FlagLet           SyntaxFlags = 1 << iota // a let declaration list
	FlagConst                                 // a const declaration list
	FlagUsing                                 // a using declaration list
	FlagAwaitUsing                            // an await using declaration list
	FlagOptionalChain                         // part of an optional chain (a?.b.c: both accesses)
	FlagQuestionDot                           // the link itself is written with ?. (a?.b, a?.[k], f?.())
	FlagAmbient                               // inside a declare context or a .d.ts file: no runtime code
	FlagAsync                                 // async modifier
	FlagGenerator                             // function*, or yield* on a yield expression
	FlagStatic                                // static modifier
	FlagAbstract                              // abstract modifier
	FlagExport                                // export modifier
	FlagDefault                               // default modifier
	FlagDeclare                               // declare modifier
	FlagAccessor                              // accessor modifier (auto-accessor field)
	FlagReadonly                              // readonly modifier
	FlagAccessibility                         // public, private or protected modifier (parameter properties)
	FlagOverride                              // override modifier
	FlagTypeOnly                              // import type / export type, or a type-only specifier
	FlagExportEquals                          // export = e
	FlagAwait                                 // for await (… of …)
	FlagConstEnum                             // const enum
)

// Role is the field of its parent a node fills, for parents whose children
// can't be told apart by kind and position alone. Other nodes are RoleNone.
type Role uint8

// Roles.
const (
	RoleNone         Role = iota
	RoleInitializer       // ForStatement initializer, BindingElement default value
	RoleCondition         // ForStatement condition
	RoleIncrementor       // ForStatement incrementor
	RoleBody              // ForStatement body
	RolePropertyName      // BindingElement, ImportSpecifier, ExportSpecifier property name: the `a` of `a: b` / `a as b`
	RoleName              // BindingElement, ImportSpecifier, ExportSpecifier local name
)

// Tree returns the syntax view of a file; nil for a bad FileID or after Close.
func (p *Program) Tree(f FileID) *Tree {
	p.mu.Lock()
	defer p.mu.Unlock()
	defer p.recoverQuery("Tree")
	t := p.treeOf(f)
	if t == nil {
		return nil
	}
	tr := &Tree{
		file:  p.files[f-1],
		sf:    p.sources[f-1],
		t:     t,
		kinds: make([]Kind, len(t.nodes)),
		flags: make([]SyntaxFlags, len(t.nodes)),
		roles: make([]Role, len(t.nodes)),
	}
	for i, n := range t.nodes {
		tr.kinds[i] = kindOf(n.Kind)
		tr.flags[i] = syntaxFlags(n)
		if t.parent[i] != 0 {
			tr.roles[i] = roleOf(t.nodes[t.parent[i]-1], n)
		}
	}
	return tr
}

func syntaxFlags(n *ast.Node) SyntaxFlags {
	var f SyntaxFlags
	nf := n.Flags
	if n.Kind == ast.KindVariableDeclarationList {
		switch {
		case nf&ast.NodeFlagsAwaitUsing == ast.NodeFlagsAwaitUsing:
			f |= FlagAwaitUsing
		case nf&ast.NodeFlagsUsing != 0:
			f |= FlagUsing
		case nf&ast.NodeFlagsConst != 0:
			f |= FlagConst
		case nf&ast.NodeFlagsLet != 0:
			f |= FlagLet
		}
	}
	if nf&ast.NodeFlagsOptionalChain != 0 {
		f |= FlagOptionalChain
	}
	if nf&ast.NodeFlagsAmbient != 0 {
		f |= FlagAmbient
	}
	switch n.Kind {
	case ast.KindPropertyAccessExpression:
		if n.AsPropertyAccessExpression().QuestionDotToken != nil {
			f |= FlagQuestionDot
		}
	case ast.KindElementAccessExpression:
		if n.AsElementAccessExpression().QuestionDotToken != nil {
			f |= FlagQuestionDot
		}
	case ast.KindCallExpression:
		if n.AsCallExpression().QuestionDotToken != nil {
			f |= FlagQuestionDot
		}
	case ast.KindFunctionDeclaration:
		if n.AsFunctionDeclaration().AsteriskToken != nil {
			f |= FlagGenerator
		}
	case ast.KindFunctionExpression:
		if n.AsFunctionExpression().AsteriskToken != nil {
			f |= FlagGenerator
		}
	case ast.KindMethodDeclaration:
		if n.AsMethodDeclaration().AsteriskToken != nil {
			f |= FlagGenerator
		}
	case ast.KindYieldExpression:
		if n.AsYieldExpression().AsteriskToken != nil {
			f |= FlagGenerator
		}
	case ast.KindForOfStatement:
		if n.AsForInOrOfStatement().AwaitModifier != nil {
			f |= FlagAwait
		}
	case ast.KindExportAssignment:
		if n.AsExportAssignment().IsExportEquals {
			f |= FlagExportEquals
		}
	case ast.KindImportClause, ast.KindImportSpecifier, ast.KindExportDeclaration, ast.KindExportSpecifier,
		ast.KindImportEqualsDeclaration:
		if n.IsTypeOnly() {
			f |= FlagTypeOnly
		}
	}
	mf := n.ModifierFlags()
	for _, m := range [...]struct {
		mod  ast.ModifierFlags
		flag SyntaxFlags
	}{
		{ast.ModifierFlagsAsync, FlagAsync},
		{ast.ModifierFlagsStatic, FlagStatic},
		{ast.ModifierFlagsAbstract, FlagAbstract},
		{ast.ModifierFlagsExport, FlagExport},
		{ast.ModifierFlagsDefault, FlagDefault},
		{ast.ModifierFlagsAmbient, FlagDeclare | FlagAmbient},
		{ast.ModifierFlagsAccessor, FlagAccessor},
		{ast.ModifierFlagsReadonly, FlagReadonly},
		{ast.ModifierFlagsAccessibilityModifier, FlagAccessibility},
		{ast.ModifierFlagsOverride, FlagOverride},
	} {
		if mf&m.mod != 0 {
			f |= m.flag
		}
	}
	if n.Kind == ast.KindEnumDeclaration && mf&ast.ModifierFlagsConst != 0 {
		f |= FlagConstEnum
	}
	return f
}

func roleOf(parent, n *ast.Node) Role {
	switch parent.Kind {
	case ast.KindForStatement:
		s := parent.AsForStatement()
		switch n {
		case s.Initializer:
			return RoleInitializer
		case s.Condition:
			return RoleCondition
		case s.Incrementor:
			return RoleIncrementor
		case s.Statement:
			return RoleBody
		}
	case ast.KindBindingElement:
		b := parent.AsBindingElement()
		switch n {
		case b.PropertyName:
			return RolePropertyName
		case b.Name():
			return RoleName
		case b.Initializer:
			return RoleInitializer
		}
	case ast.KindImportSpecifier:
		s := parent.AsImportSpecifier()
		switch n {
		case s.PropertyName:
			return RolePropertyName
		case s.Name():
			return RoleName
		}
	case ast.KindExportSpecifier:
		s := parent.AsExportSpecifier()
		switch n {
		case s.PropertyName:
			return RolePropertyName
		case s.Name():
			return RoleName
		}
	}
	return RoleNone
}

// File returns the file the tree belongs to.
func (t *Tree) File() *File { return t.file }

// Root returns the SourceFile node.
func (t *Tree) Root() NodeID { return nodeID(t.file.ID, 0) }

// Len returns the number of nodes in the file.
func (t *Tree) Len() int { return len(t.t.nodes) }

// Index returns a node's preorder index in [0, Len), or -1 for a node of
// another file. Use it to index per-node slices.
func (t *Tree) Index(n NodeID) int {
	i := n.index()
	if n.File() != t.file.ID || i < 0 || i >= len(t.t.nodes) {
		return -1
	}
	return i
}

// Node returns the node with a preorder index.
func (t *Tree) Node(index int) NodeID { return nodeID(t.file.ID, index) }

// Kind returns a node's Patchd kind; KindInvalid for a bad NodeID.
func (t *Tree) Kind(n NodeID) Kind {
	if i := t.Index(n); i >= 0 {
		return t.kinds[i]
	}
	return KindInvalid
}

// CompilerKind returns the compiler's name for a node's kind, for debug
// output and coverage reports ("CallExpression"). Not stable across upgrades.
func (t *Tree) CompilerKind(n NodeID) string {
	if i := t.Index(n); i >= 0 {
		return t.t.nodes[i].Kind.String()[len("Kind"):]
	}
	return ""
}

// Children returns a node's children in source order. The slice is shared:
// callers must not modify it.
func (t *Tree) Children(n NodeID) []NodeID {
	i := t.Index(n)
	if i < 0 {
		return nil
	}
	return t.t.kids[t.t.childStart[i]:t.t.childEnd[i]:t.t.childEnd[i]]
}

// Parent returns a node's parent; 0 for the SourceFile.
func (t *Tree) Parent(n NodeID) NodeID {
	i := t.Index(n)
	if i < 0 || t.t.parent[i] == 0 {
		return 0
	}
	return nodeID(t.file.ID, int(t.t.parent[i]-1))
}

// Range returns a node's byte range, starting at its first token.
func (t *Tree) Range(n NodeID) Range {
	i := t.Index(n)
	if i < 0 {
		return Range{}
	}
	node := t.t.nodes[i]
	if node.Kind == ast.KindSourceFile {
		return Range{Pos: 0, End: Pos(len(t.sf.Text()))}
	}
	start := node.Pos()
	if node.Pos() < node.End() {
		start = scanner.GetTokenPosOfNode(node, t.sf, false)
	}
	return Range{Pos: Pos(start), End: Pos(max(start, node.End()))}
}

// Text returns the source text of a node's range.
func (t *Tree) Text(n NodeID) string {
	r := t.Range(n)
	if int(r.End) > len(t.file.Text) {
		return ""
	}
	return t.file.Text[r.Pos:r.End]
}

// Value returns what a name or literal denotes, with escapes resolved: the
// name of an identifier or #private name, the cooked text of a string,
// template part or JSX text, a number's or bigint's text as written, a
// regular expression's source (/re/g), and the keyword of a MetaProperty
// (new.target, import.meta). Other nodes return "".
func (t *Tree) Value(n NodeID) string {
	i := t.Index(n)
	if i < 0 {
		return ""
	}
	node := t.t.nodes[i]
	switch node.Kind {
	case ast.KindJsxText:
		return node.AsJsxText().Text
	case ast.KindMetaProperty:
		return scanner.TokenToString(node.AsMetaProperty().KeywordToken) + "." + node.Text()
	case ast.KindIdentifier, ast.KindPrivateIdentifier, ast.KindStringLiteral, ast.KindNumericLiteral,
		ast.KindBigIntLiteral, ast.KindNoSubstitutionTemplateLiteral, ast.KindTemplateHead,
		ast.KindTemplateMiddle, ast.KindTemplateTail, ast.KindRegularExpressionLiteral, ast.KindJsxNamespacedName:
		return node.Text()
	}
	return ""
}

// Operator returns the operator of a binary, prefix or postfix expression
// ("+", "===", "??=", "++", "typeof" is a TypeOfExpression, not an
// operator), the keyword of a token, modifier or heritage clause ("?.",
// "async", "extends"), and "" for other nodes.
func (t *Tree) Operator(n NodeID) string {
	i := t.Index(n)
	if i < 0 {
		return ""
	}
	node := t.t.nodes[i]
	switch node.Kind {
	case ast.KindBinaryExpression:
		return scanner.TokenToString(node.AsBinaryExpression().OperatorToken.Kind)
	case ast.KindPrefixUnaryExpression:
		return scanner.TokenToString(node.AsPrefixUnaryExpression().Operator)
	case ast.KindPostfixUnaryExpression:
		return scanner.TokenToString(node.AsPostfixUnaryExpression().Operator)
	case ast.KindHeritageClause:
		return scanner.TokenToString(node.AsHeritageClause().Token)
	}
	if k := t.kinds[i]; k == KindToken || k == KindModifier {
		return scanner.TokenToString(node.Kind)
	}
	return ""
}

// Flags returns a node's syntax flags.
func (t *Tree) Flags(n NodeID) SyntaxFlags {
	if i := t.Index(n); i >= 0 {
		return t.flags[i]
	}
	return 0
}

// Role returns the field of its parent a node fills; see Role.
func (t *Tree) Role(n NodeID) Role {
	if i := t.Index(n); i >= 0 {
		return t.roles[i]
	}
	return RoleNone
}
