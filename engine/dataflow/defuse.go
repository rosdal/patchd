// Def-use chains.

package dataflow

import (
	"sync"

	"github.com/patchd/patchd/engine/ir"
)

// DefUse holds a function's def-use chains: for every value, the
// instructions that use it as an argument. The definition side is in the IR
// already (Value.Def).
type DefUse struct {
	f    *ir.Func
	uses [][]ir.InstrID
}

// NewDefUse builds the def-use chains of f. An instruction that uses a value
// twice is listed once.
func NewDefUse(f *ir.Func) *DefUse {
	uses := f.Uses()
	for v, list := range uses {
		uses[v] = dedupSorted(list)
	}
	return &DefUse{f: f, uses: uses}
}

// Func returns the function the chains belong to.
func (d *DefUse) Func() *ir.Func { return d.f }

// Uses returns the instructions that use v, in instruction order. The slice
// is shared: callers must not modify it.
func (d *DefUse) Uses(v ir.ValueID) []ir.InstrID {
	if int(v) >= len(d.uses) {
		return nil
	}
	return d.uses[v]
}

// dedupSorted removes adjacent duplicates from an ascending list.
func dedupSorted(list []ir.InstrID) []ir.InstrID {
	if len(list) < 2 {
		return list
	}
	out := list[:1]
	for _, id := range list[1:] {
		if id != out[len(out)-1] {
			out = append(out, id)
		}
	}
	return out
}

// Cache builds def-use chains (and reaching stores) once per function and
// hands out the same ones to every analysis that asks. Safe for concurrent
// use; drop it with the IR it describes.
type Cache struct {
	mu       sync.Mutex
	defuse   map[*ir.Func]*DefUse
	reaching map[*ir.Func]*ReachingStores
}

// NewCache returns an empty cache.
func NewCache() *Cache {
	return &Cache{defuse: map[*ir.Func]*DefUse{}, reaching: map[*ir.Func]*ReachingStores{}}
}

// DefUse returns f's def-use chains, building them on first use.
func (c *Cache) DefUse(f *ir.Func) *DefUse {
	c.mu.Lock()
	d, ok := c.defuse[f]
	c.mu.Unlock()
	if ok {
		return d
	}
	d = NewDefUse(f)
	c.mu.Lock()
	if old, ok := c.defuse[f]; ok {
		d = old
	} else {
		c.defuse[f] = d
	}
	c.mu.Unlock()
	return d
}

// Reaching returns f's reaching stores, computing them on first use.
func (c *Cache) Reaching(f *ir.Func) *ReachingStores {
	c.mu.Lock()
	r, ok := c.reaching[f]
	c.mu.Unlock()
	if ok {
		return r
	}
	r = Reaching(f, Options{})
	c.mu.Lock()
	if old, ok := c.reaching[f]; ok {
		r = old
	} else {
		c.reaching[f] = r
	}
	c.mu.Unlock()
	return r
}

// Forget drops what the cache holds for f.
func (c *Cache) Forget(f *ir.Func) {
	c.mu.Lock()
	delete(c.defuse, f)
	delete(c.reaching, f)
	c.mu.Unlock()
}
