package tsbridge

import (
	"testing"

	"github.com/microsoft/typescript-go/shim/ast"
)

// TestKindTableCoversCompiler fails when the pinned typescript-go has a syntax
// kind the generated table does not list, so a compiler upgrade surfaces new
// syntax explicitly. Fix it with `pnpm gen`, then decide how the new kind maps.
func TestKindTableCoversCompiler(t *testing.T) {
	if int(ast.KindCount) != compilerKindCount {
		t.Fatalf("typescript-go has %d syntax kinds, kind_gen.go was generated for %d: run pnpm gen", ast.KindCount, compilerKindCount)
	}
	for k := ast.Kind(0); k < ast.KindCount; k++ {
		if kindTable[k] == KindInvalid {
			t.Errorf("compiler kind %v has no Patchd kind: run pnpm gen", k)
		}
	}
}

func TestKindOfNeverPanics(t *testing.T) {
	for _, k := range []ast.Kind{-1, ast.KindCount, ast.KindCount + 100} {
		if got := kindOf(k); got != KindOther {
			t.Errorf("kindOf(%d) = %v, want Other", k, got)
		}
	}
	if got := kindOf(ast.KindIdentifier); got != KindIdentifier {
		t.Errorf("kindOf(Identifier) = %v", got)
	}
	if got := kindOf(ast.KindThisKeyword); got != KindThis {
		t.Errorf("kindOf(ThisKeyword) = %v", got)
	}
	if got := kindOf(ast.KindUnionType); got != KindTypeNode {
		t.Errorf("kindOf(UnionType) = %v", got)
	}
}
