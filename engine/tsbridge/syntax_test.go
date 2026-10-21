package tsbridge

import (
	"slices"
	"testing"
)

func TestWalkChildrenParentAgree(t *testing.T) {
	p := loadFixture(t, "positions", LoadOptions{})
	for _, id := range p.RootFiles() {
		root := p.Root(id)
		if p.Kind(root) != KindSourceFile || p.Parent(root) != 0 {
			t.Fatalf("root of %s: kind %v, parent %v", p.File(id).Path, p.Kind(root), p.Parent(root))
		}
		var order []NodeID
		p.Walk(id, func(n NodeID) bool {
			order = append(order, n)
			return true
		})
		if len(order) != p.NodeCount(id) || order[0] != root {
			t.Fatalf("walk visited %d nodes, NodeCount %d", len(order), p.NodeCount(id))
		}
		// Preorder: every node follows its parent, children point back at
		// their parent, and children are in source order.
		seen := map[NodeID]bool{}
		for _, n := range order {
			if n != root && !seen[p.Parent(n)] {
				t.Fatalf("node %v visited before its parent", n)
			}
			seen[n] = true
			kids := p.Children(n)
			for i, c := range kids {
				if p.Parent(c) != n {
					t.Fatalf("child %v of %v has parent %v", c, n, p.Parent(c))
				}
				if i > 0 && p.Range(c).Pos < p.Range(kids[i-1]).Pos {
					t.Fatalf("children of %v out of source order", n)
				}
			}
		}
	}
}

func TestWalkStopsEarly(t *testing.T) {
	p := loadFixture(t, "positions", LoadOptions{})
	f := fileNamed(t, p, "tabs.ts")
	n := 0
	p.Walk(f.ID, func(NodeID) bool {
		n++
		return n < 3
	})
	if n != 3 {
		t.Errorf("walk visited %d nodes after asking to stop at 3", n)
	}
}

func TestRangeSkipsLeadingTrivia(t *testing.T) {
	p := loadFixture(t, "positions", LoadOptions{})
	f := fileNamed(t, p, "trivia.ts")
	n := p.NodeAt(f.ID, offsetOf(t, f, "target", 0))
	if p.Kind(n) != KindIdentifier || p.Text(n) != "target" {
		t.Fatalf("NodeAt(target) = %v %q", p.Kind(n), p.Text(n))
	}
	fn := p.Parent(n)
	if p.Kind(fn) != KindFunctionDeclaration {
		t.Fatalf("parent = %v", p.Kind(fn))
	}
	line, col := f.Position(p.Range(fn).Pos)
	if line != 3 || col != 1 {
		t.Errorf("function starts at %d:%d, want 3:1 (after its comments)", line, col)
	}
	// In leading trivia, NodeAt returns the enclosing node, not the function.
	if got := p.NodeAt(f.ID, offsetOf(t, f, "leading", 0)); p.Kind(got) != KindSourceFile {
		t.Errorf("NodeAt in a comment = %v, want SourceFile", p.Kind(got))
	}
}

func TestJSXNodes(t *testing.T) {
	p := loadFixture(t, "positions", LoadOptions{})
	f := fileNamed(t, p, "jsx.tsx")
	var kinds []Kind
	p.Walk(f.ID, func(n NodeID) bool {
		kinds = append(kinds, p.Kind(n))
		return true
	})
	for _, want := range []Kind{
		KindJsxElement, KindJsxOpeningElement, KindJsxAttributes, KindJsxAttribute,
		KindJsxExpression, KindJsxSelfClosingElement, KindJsxClosingElement,
	} {
		if !slices.Contains(kinds, want) {
			t.Errorf("no %v node in jsx.tsx", want)
		}
	}
	at := p.NodeAt(f.ID, offsetOf(t, f, "onClick", 0))
	if p.Kind(at) != KindIdentifier || p.Kind(p.Parent(at)) != KindJsxAttribute {
		t.Errorf("NodeAt(onClick) = %v in %v", p.Kind(at), p.Kind(p.Parent(at)))
	}
}

func TestSyntaxBadHandles(t *testing.T) {
	p := loadFixture(t, "positions", LoadOptions{})
	for _, n := range []NodeID{0, NodeID(1<<32 | 1<<31), NodeID(9999 << 32)} {
		if p.Kind(n) != KindInvalid || p.Children(n) != nil || p.Parent(n) != 0 || p.Range(n) != (Range{}) || p.Text(n) != "" {
			t.Errorf("bad NodeID %v did not return zero values", n)
		}
	}
	if p.Root(0) != 0 || p.NodeAt(9999, 0) != 0 || p.File(0) != nil || p.File(9999) != nil {
		t.Error("bad FileID did not return zero values")
	}
	p.Walk(9999, func(NodeID) bool { t.Error("walked a bad file"); return false })
}

func TestClosedProgramReturnsZeroValues(t *testing.T) {
	p := loadFixture(t, "positions", LoadOptions{})
	f := fileNamed(t, p, "tabs.ts")
	n := p.NodeAt(f.ID, offsetOf(t, f, "return", 0))
	p.Close()
	p.Close() // idempotent
	if !p.Closed() || p.Kind(n) != KindInvalid || p.TypeOf(n) != 0 || p.Root(f.ID) != 0 {
		t.Error("a closed program must answer with zero values")
	}
	if _, ok := p.SymbolOf(n); ok {
		t.Error("SymbolOf on a closed program")
	}
}
