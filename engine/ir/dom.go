package ir

// DomTree is a function's dominator tree, computed with the iterative
// algorithm of Cooper, Harvey and Kennedy ("A Simple, Fast Dominance
// Algorithm", 2001). Edges are normal successors plus exceptional edges to
// handlers. Dead code (blocks the entry can't reach, lowered anyway) forms
// trees of its own, rooted at blocks without predecessors; edges from dead
// code into live code are ignored, so it never weakens dominance in the live
// part.
type DomTree struct {
	f        *Func
	idom     []BlockID // NoBlock for roots
	rpo      []BlockID // reverse postorder from the virtual root
	pos      []int     // rpo position
	region   []int     // 1: reachable from the entry; others: dead code regions
	children [][]BlockID
	pre      []int // DFS numbering of the dominator tree, for O(1) Dominates
	post     []int
	frontier [][]BlockID
}

// successors returns a block's normal and exceptional successors.
func successors(f *Func, b BlockID, out []BlockID) []BlockID {
	blk := &f.Blocks[b]
	out = append(out[:0], blk.Succs...)
	if blk.Handler != NoBlock {
		out = append(out, blk.Handler)
	}
	return out
}

// Dominators computes f's dominator tree.
func Dominators(f *Func) *DomTree {
	n := len(f.Blocks)
	d := &DomTree{f: f, idom: make([]BlockID, n), pos: make([]int, n)}
	for i := range d.pos {
		d.pos[i] = -1
	}
	// Regions: 1 is everything reachable from the entry; each block with no
	// predecessors outside it roots a dead region (2, 3, …) of the blocks
	// only it reaches. Edges between regions (dead code falling back into
	// live code) are ignored, so live blocks are dominated by live blocks
	// only.
	region := make([]int, n)
	var roots []BlockID
	if n > 1 {
		roots = append(roots, f.Entry)
	}
	for b := 1; b < n; b++ {
		if BlockID(b) != f.Entry && len(f.Blocks[b].Preds) == 0 { //nolint:gosec // block counts fit
			roots = append(roots, BlockID(b)) //nolint:gosec // block counts fit
		}
	}
	type frame struct {
		b    BlockID
		next int
		succ []BlockID
	}
	var post []BlockID
	for ri, r := range roots {
		if region[r] != 0 {
			continue
		}
		region[r] = ri + 1
		stack := []frame{{b: r, succ: successors(f, r, nil)}}
		for len(stack) > 0 {
			top := &stack[len(stack)-1]
			if top.next < len(top.succ) {
				s := top.succ[top.next]
				top.next++
				if region[s] == 0 {
					region[s] = ri + 1
					stack = append(stack, frame{b: s, succ: successors(f, s, nil)})
				}
				continue
			}
			post = append(post, top.b)
			stack = stack[:len(stack)-1]
		}
	}
	// Blocks in a cycle no root reaches get regions of their own.
	for b := 1; b < n; b++ {
		if region[b] == 0 {
			region[b] = len(roots) + b
			post = append(post, BlockID(b)) //nolint:gosec // block counts fit
		}
	}
	d.region = region
	d.rpo = make([]BlockID, len(post))
	for i, b := range post {
		d.rpo[len(post)-1-i] = b
	}
	for i, b := range d.rpo {
		d.pos[b] = i
	}
	isRoot := make([]bool, n)
	for _, r := range roots {
		isRoot[r] = true
	}
	processed := make([]bool, n)
	for _, r := range roots {
		processed[r] = true
	}
	intersect := func(a, b BlockID) BlockID {
		for a != b {
			for a != NoBlock && b != NoBlock && d.pos[a] > d.pos[b] {
				a = d.idom[a]
			}
			for a != NoBlock && b != NoBlock && d.pos[b] > d.pos[a] {
				b = d.idom[b]
			}
			if a == NoBlock || b == NoBlock {
				return NoBlock
			}
		}
		return a
	}
	for changed := true; changed; {
		changed = false
		for _, b := range d.rpo {
			if isRoot[b] {
				continue
			}
			newIdom := NoBlock
			first := true
			for _, p := range f.Blocks[b].Preds {
				if !processed[p] || region[p] != region[b] {
					continue
				}
				if first {
					newIdom, first = p, false
					continue
				}
				newIdom = intersect(p, newIdom)
				if newIdom == NoBlock {
					break
				}
			}
			if first {
				continue
			}
			if !processed[b] || d.idom[b] != newIdom {
				d.idom[b] = newIdom
				processed[b] = true
				changed = true
			}
		}
	}
	d.children = make([][]BlockID, n)
	var top []BlockID
	for _, b := range d.rpo {
		if p := d.idom[b]; p != NoBlock {
			d.children[p] = append(d.children[p], b)
		} else {
			top = append(top, b)
		}
	}
	d.pre, d.post = make([]int, n), make([]int, n)
	clock := 0
	// Iterative DFS numbering of the dominator tree (it can be deep).
	type nframe struct {
		b    BlockID
		next int
	}
	for _, r := range top {
		clock++
		d.pre[r] = clock
		stack := []nframe{{b: r}}
		for len(stack) > 0 {
			fr := &stack[len(stack)-1]
			if fr.next < len(d.children[fr.b]) {
				c := d.children[fr.b][fr.next]
				fr.next++
				clock++
				d.pre[c] = clock
				stack = append(stack, nframe{b: c})
				continue
			}
			clock++
			d.post[fr.b] = clock
			stack = stack[:len(stack)-1]
		}
	}
	return d
}

// Live reports whether b is reachable from the entry.
func (d *DomTree) Live(b BlockID) bool { return d.region[b] == 1 }

// Idom returns b's immediate dominator; NoBlock for the entry and other roots.
func (d *DomTree) Idom(b BlockID) BlockID { return d.idom[b] }

// Children returns the blocks b immediately dominates.
func (d *DomTree) Children(b BlockID) []BlockID { return d.children[b] }

// RPO returns the blocks in reverse postorder (every block reachable from a
// root; dead blocks after the live ones).
func (d *DomTree) RPO() []BlockID { return d.rpo }

// Dominates reports whether a dominates b (every block dominates itself).
func (d *DomTree) Dominates(a, b BlockID) bool {
	if a == b {
		return true
	}
	if d.pre[a] == 0 || d.pre[b] == 0 {
		return false
	}
	return d.pre[a] < d.pre[b] && d.post[b] < d.post[a]
}

// Frontier returns b's dominance frontier: the blocks where b's dominance
// ends (Cytron et al.), computed on first use for all blocks.
func (d *DomTree) Frontier(b BlockID) []BlockID {
	if d.frontier == nil {
		f := d.f
		d.frontier = make([][]BlockID, len(f.Blocks))
		for x := 1; x < len(f.Blocks); x++ {
			preds := f.Blocks[x].Preds
			if len(preds) < 2 {
				continue
			}
			for _, p := range preds {
				if d.region[p] != d.region[x] {
					continue
				}
				for r := p; r != NoBlock && r != d.idom[x]; r = d.idom[r] {
					if !containsBlock(d.frontier[r], BlockID(x)) { //nolint:gosec // block counts fit
						d.frontier[r] = append(d.frontier[r], BlockID(x)) //nolint:gosec // block counts fit
					}
				}
			}
		}
	}
	return d.frontier[b]
}

func containsBlock(s []BlockID, b BlockID) bool {
	for _, x := range s {
		if x == b {
			return true
		}
	}
	return false
}
