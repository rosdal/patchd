package tsbridge

import (
	"github.com/microsoft/typescript-go/shim/ast"
	"github.com/microsoft/typescript-go/shim/scanner"
)

// tree indexes one file's syntax in source (pre-)order. Node i's children are
// kids[childStart[i]:childEnd[i]], so Children returns a sub-slice without
// allocating, and Walk is a loop over nodes.
type tree struct {
	nodes      []*ast.Node
	parent     []uint32 // index+1 of the parent; 0 for the root
	childStart []uint32
	childEnd   []uint32
	kids       []NodeID
}

// nodeID packs a file and a preorder index; a file has far fewer than 2^32 nodes.
func nodeID(f FileID, index int) NodeID {
	return NodeID(uint64(f)<<32 | uint64(index+1)) //nolint:gosec // index is non-negative and below 2^32
}

func (n NodeID) index() int { return int(n&0xffffffff) - 1 }

func buildTree(f FileID, sf *ast.SourceFile) *tree {
	t := &tree{}
	// First pass: preorder numbering with parent links.
	var visit func(n *ast.Node, parent uint32)
	visit = func(n *ast.Node, parent uint32) {
		t.nodes = append(t.nodes, n)
		t.parent = append(t.parent, parent)
		self := uint32(len(t.nodes)) //nolint:gosec // see nodeID
		n.ForEachChild(func(c *ast.Node) bool {
			visit(c, self)
			return false
		})
	}
	visit(sf.AsNode(), 0)
	// Second pass: child lists, in the order children were visited.
	counts := make([]uint32, len(t.nodes))
	for i := 1; i < len(t.nodes); i++ {
		counts[t.parent[i]-1]++
	}
	t.childStart = make([]uint32, len(t.nodes))
	t.childEnd = make([]uint32, len(t.nodes))
	var off uint32
	for i, c := range counts {
		t.childStart[i], t.childEnd[i] = off, off
		off += c
	}
	t.kids = make([]NodeID, off)
	for i := 1; i < len(t.nodes); i++ {
		p := t.parent[i] - 1
		t.kids[t.childEnd[p]] = nodeID(f, i)
		t.childEnd[p]++
	}
	return t
}

// treeOf returns the syntax index of a file, building it on first use.
// Callers hold p.mu.
func (p *Program) treeOf(f FileID) *tree {
	if p.closed || f == 0 || int(f) > len(p.trees) {
		return nil
	}
	if p.trees[f-1] == nil {
		p.trees[f-1] = buildTree(f, p.sources[f-1])
	}
	return p.trees[f-1]
}

// node returns the compiler node for n. Callers hold p.mu.
func (p *Program) node(n NodeID) (*ast.Node, *tree) {
	t := p.treeOf(n.File())
	if t == nil {
		return nil, nil
	}
	i := n.index()
	if i < 0 || i >= len(t.nodes) {
		return nil, nil
	}
	return t.nodes[i], t
}

// Root returns the SourceFile node of a file.
func (p *Program) Root(f FileID) NodeID {
	p.mu.Lock()
	defer p.mu.Unlock()
	defer p.recoverQuery("Root")
	if p.treeOf(f) == nil {
		return 0
	}
	return nodeID(f, 0)
}

// Kind returns the Patchd kind of a node; KindInvalid for a bad NodeID.
func (p *Program) Kind(n NodeID) Kind {
	p.mu.Lock()
	defer p.mu.Unlock()
	defer p.recoverQuery("Kind")
	node, _ := p.node(n)
	if node == nil {
		return KindInvalid
	}
	return kindOf(node.Kind)
}

// CompilerKind returns the compiler's name for a node's syntax kind, for
// debug output (e.g. "CallExpression"). It is not stable across upgrades.
func (p *Program) CompilerKind(n NodeID) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	defer p.recoverQuery("CompilerKind")
	node, _ := p.node(n)
	if node == nil {
		return ""
	}
	return node.Kind.String()[len("Kind"):]
}

// Children returns a node's children in source order. The slice is shared:
// callers must not modify it.
func (p *Program) Children(n NodeID) []NodeID {
	p.mu.Lock()
	defer p.mu.Unlock()
	defer p.recoverQuery("Children")
	node, t := p.node(n)
	if node == nil {
		return nil
	}
	i := n.index()
	return t.kids[t.childStart[i]:t.childEnd[i]:t.childEnd[i]]
}

// Parent returns a node's parent; 0 for a SourceFile or a bad NodeID.
func (p *Program) Parent(n NodeID) NodeID {
	p.mu.Lock()
	defer p.mu.Unlock()
	defer p.recoverQuery("Parent")
	node, t := p.node(n)
	if node == nil || t.parent[n.index()] == 0 {
		return 0
	}
	return nodeID(n.File(), int(t.parent[n.index()]-1))
}

// Range returns a node's byte range, starting at its first token: leading
// comments and whitespace are not part of it.
func (p *Program) Range(n NodeID) Range {
	p.mu.Lock()
	defer p.mu.Unlock()
	defer p.recoverQuery("Range")
	node, _ := p.node(n)
	if node == nil {
		return Range{}
	}
	return p.rangeOf(node, p.sources[n.File()-1])
}

// rangeOf is Range for a compiler node. Callers hold p.mu.
func (p *Program) rangeOf(node *ast.Node, sf *ast.SourceFile) Range {
	if node.Kind == ast.KindSourceFile {
		return Range{Pos: 0, End: Pos(len(sf.Text()))}
	}
	start := node.Pos()
	if node.Pos() < node.End() {
		start = scanner.GetTokenPosOfNode(node, sf, false)
	}
	return Range{Pos: Pos(start), End: Pos(max(start, node.End()))}
}

// Text returns the source text of a node's range.
func (p *Program) Text(n NodeID) string {
	r := p.Range(n)
	f := p.File(n.File())
	if f == nil || int(r.End) > len(f.Text) {
		return ""
	}
	return f.Text[r.Pos:r.End]
}

// NodeAt returns the innermost node whose range contains pos, or the
// SourceFile when pos is in trivia between top-level statements.
func (p *Program) NodeAt(f FileID, pos Pos) NodeID {
	p.mu.Lock()
	defer p.mu.Unlock()
	defer p.recoverQuery("NodeAt")
	t := p.treeOf(f)
	if t == nil {
		return 0
	}
	sf := p.sources[f-1]
	cur := 0
	for {
		next := -1
		// A linear scan, not a binary search: reparsed JSDoc nodes in JS
		// files can sit out of source order among their siblings.
		for _, k := range t.kids[t.childStart[cur]:t.childEnd[cur]] {
			c := k.index()
			if n := t.nodes[c]; n.Pos() <= int(pos) && int(pos) < n.End() && p.rangeOf(n, sf).Contains(pos) {
				next = c
			}
		}
		if next < 0 {
			return nodeID(f, cur)
		}
		cur = next
	}
}

// Walk visits every node of a file in source order, parents before children.
// It stops early when visit returns false.
func (p *Program) Walk(f FileID, visit func(NodeID) bool) {
	p.mu.Lock()
	t := p.treeOf(f)
	p.mu.Unlock()
	if t == nil {
		return
	}
	for i := range t.nodes {
		if !visit(nodeID(f, i)) {
			return
		}
	}
}

// NodeCount returns the number of syntax nodes in a file.
func (p *Program) NodeCount(f FileID) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if t := p.treeOf(f); t != nil {
		return len(t.nodes)
	}
	return 0
}
