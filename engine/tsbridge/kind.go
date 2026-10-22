package tsbridge

import "github.com/microsoft/typescript-go/shim/ast"

// Kind is a Patchd syntax kind: the syntax v0.3 lowers to IR (declarations,
// statements, expressions, JSX), with type-level syntax kept as opaque
// KindTypeNode nodes. The constants are generated into kind_gen.go by
// tools/scripts/gen-kinds.mjs, which also maps every compiler kind to one of
// them; a compiler kind Patchd does not model becomes KindOther, never a panic.
type Kind uint16

func (k Kind) String() string {
	if int(k) < len(kindNames) {
		return kindNames[k]
	}
	return "Kind(?)"
}

// kindOf maps a compiler kind to a Patchd kind.
func kindOf(k ast.Kind) Kind {
	if k < 0 || int(k) >= len(kindTable) {
		return KindOther
	}
	return kindTable[k]
}

// IsFunctionLike reports whether nodes of this kind have a body of their own.
func (k Kind) IsFunctionLike() bool {
	switch k {
	case KindFunctionDeclaration, KindFunctionExpression, KindArrowFunction, KindMethodDeclaration,
		KindConstructor, KindGetAccessor, KindSetAccessor, KindClassStaticBlockDeclaration:
		return true
	}
	return false
}

// IsCallLike reports whether nodes of this kind call something ResolveCall can resolve.
func (k Kind) IsCallLike() bool {
	switch k {
	case KindCallExpression, KindNewExpression, KindTaggedTemplateExpression, KindDecorator,
		KindJsxOpeningElement, KindJsxSelfClosingElement:
		return true
	}
	return false
}
