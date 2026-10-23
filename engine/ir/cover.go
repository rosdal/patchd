package ir

import (
	"fmt"
	"sort"

	"github.com/patchd/patchd/engine/tsbridge"
)

// Coverage counts how much of a file's syntax lowered to real instructions.
// It counts nodes with runtime meaning: types, type-only declarations,
// ambient code, JSDoc, tokens and modifiers are left out. A node is lowered
// when the builder handled it, unknown when it became (or sits under) an
// unknown instruction, and skipped when the builder never reached it (a
// builder bug: syntax silently dropped). Names (identifiers, literals used
// as keys) count with the node that consumed them.
type Coverage struct {
	Nodes   int                      `json:"nodes"`
	Lowered int                      `json:"lowered"`
	Unknown int                      `json:"unknown"`
	Skipped int                      `json:"skipped"`
	Kinds   map[string]*KindCoverage `json:"kinds,omitempty"`
}

// KindCoverage is Coverage for one compiler syntax kind.
type KindCoverage struct {
	Lowered  int      `json:"lowered"`
	Unknown  int      `json:"unknown"`
	Skipped  int      `json:"skipped"`
	Examples []string `json:"examples,omitempty"` // up to 3 "path:line:col" of unknown or skipped nodes
}

// Percent returns the share of counted nodes that lowered, 0–100.
func (c Coverage) Percent() float64 {
	if c.Nodes == 0 {
		return 100
	}
	return 100 * float64(c.Lowered) / float64(c.Nodes)
}

// Add merges o into c.
func (c *Coverage) Add(o Coverage) {
	c.Nodes += o.Nodes
	c.Lowered += o.Lowered
	c.Unknown += o.Unknown
	c.Skipped += o.Skipped
	if c.Kinds == nil {
		c.Kinds = map[string]*KindCoverage{}
	}
	for k, kc := range o.Kinds {
		dst := c.Kinds[k]
		if dst == nil {
			dst = &KindCoverage{}
			c.Kinds[k] = dst
		}
		dst.Lowered += kc.Lowered
		dst.Unknown += kc.Unknown
		dst.Skipped += kc.Skipped
		for _, ex := range kc.Examples {
			if len(dst.Examples) < 3 {
				dst.Examples = append(dst.Examples, ex)
			}
		}
	}
}

// Missing returns the kinds with unknown or skipped nodes, most first.
func (c Coverage) Missing() []string {
	var out []string
	for k, kc := range c.Kinds {
		if kc.Unknown+kc.Skipped > 0 {
			out = append(out, k)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := c.Kinds[out[i]], c.Kinds[out[j]]
		if x, y := a.Unknown+a.Skipped, b.Unknown+b.Skipped; x != y {
			return x > y
		}
		return out[i] < out[j]
	})
	return out
}

// signatureOnly reports a function-like declaration without a body: an
// overload signature or an abstract member. Neither has runtime code.
func (b *builder) signatureOnly(n tsbridge.NodeID) bool {
	switch b.tree.Kind(n) {
	case tsbridge.KindFunctionDeclaration, tsbridge.KindMethodDeclaration, tsbridge.KindConstructor,
		tsbridge.KindGetAccessor, tsbridge.KindSetAccessor:
		for _, c := range b.tree.Children(n) {
			if b.tree.Kind(c) == tsbridge.KindBlock {
				return false
			}
		}
		return true
	}
	return false
}

func (b *builder) coverage() Coverage {
	c := Coverage{Kinds: map[string]*KindCoverage{}}
	t := b.tree
	var visit func(n tsbridge.NodeID, parentMark uint8)
	visit = func(n tsbridge.NodeID, parentMark uint8) {
		if b.skipped(n) || b.signatureOnly(n) {
			return
		}
		i := t.Index(n)
		if i < 0 {
			return
		}
		m := b.marks[i]
		k := t.Kind(n)
		if k == tsbridge.KindToken || k == tsbridge.KindModifier {
			return
		}
		if m == markNone && parentMark != markNone && isNameKind(k) {
			m = parentMark
		}
		if m == markNone && parentMark == markUnknown {
			m = markUnknown
		}
		name := t.CompilerKind(n)
		kc := c.Kinds[name]
		if kc == nil {
			kc = &KindCoverage{}
			c.Kinds[name] = kc
		}
		c.Nodes++
		switch m {
		case markLowered:
			c.Lowered++
			kc.Lowered++
		case markUnknown:
			c.Unknown++
			kc.Unknown++
		default:
			c.Skipped++
			kc.Skipped++
		}
		if m != markLowered && len(kc.Examples) < 3 {
			line, col := b.file.Position(t.Range(n).Pos)
			kc.Examples = append(kc.Examples, fmt.Sprintf("%s:%d:%d", b.file.Path, line, col))
		}
		for _, ch := range t.Children(n) {
			visit(ch, m)
		}
	}
	visit(t.Root(), markLowered)
	return c
}
