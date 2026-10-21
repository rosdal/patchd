// Command kinddump prints every syntax kind of the pinned typescript-go as JSON.
// tools/scripts/gen-kinds.mjs reads it to generate the compiler-to-Patchd kind
// table; it lives here because only engine/tsbridge may import typescript-go.
package main

import (
	"encoding/json"
	"os"
	"strings"

	"github.com/microsoft/typescript-go/shim/ast"
)

type kind struct {
	Value    int    `json:"value"`
	Name     string `json:"name"`
	Category string `json:"category"`
}

func category(k ast.Kind) string {
	switch {
	case k >= ast.KindFirstJSDocNode && k <= ast.KindLastJSDocNode:
		return "jsdoc"
	case k >= ast.KindFirstTypeNode && k <= ast.KindLastTypeNode:
		return "type"
	case k >= ast.KindFirstKeyword && k <= ast.KindLastKeyword:
		return "keyword"
	case k >= ast.KindFirstTriviaToken && k <= ast.KindLastTriviaToken:
		return "trivia"
	case k >= ast.KindFirstPunctuation && k <= ast.KindLastPunctuation:
		return "punctuation"
	case k >= ast.KindFirstToken && k <= ast.KindLastToken:
		return "token"
	default:
		return "node"
	}
}

func main() {
	kinds := make([]kind, 0, int(ast.KindCount))
	for k := ast.Kind(0); k < ast.KindCount; k++ {
		kinds = append(kinds, kind{Value: int(k), Name: strings.TrimPrefix(k.String(), "Kind"), Category: category(k)})
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(kinds); err != nil {
		os.Exit(1)
	}
}
