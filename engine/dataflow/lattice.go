// Lattices.

package dataflow

// Lattice is a join semilattice of facts of type T, the domain a dataflow
// analysis computes in. Implementations must be monotone and of finite height
// (or provide a Widener) for the solvers to terminate.
type Lattice[T any] interface {
	// Bottom is the least fact: "nothing known yet", the start of every value
	// and block.
	Bottom() T
	// Join is the least upper bound of a and b. It must not modify either.
	Join(a, b T) T
	// Leq reports a ⊑ b: b already holds everything a does.
	Leq(a, b T) bool
}

// Widener is implemented by lattices of infinite height. After a block or
// value has been updated WidenAfter times, the solvers combine its old and
// new facts with Widen instead of Join, which must reach a fixed point in a
// bounded number of steps.
type Widener[T any] interface {
	Widen(old, next T) T
}

// WidenAfter is how many updates of one block or value the solvers allow
// before widening.
const WidenAfter = 8

// combine joins next into old, widening once a fact has changed often.
func combine[T any](lat Lattice[T], old, next T, updates int) T {
	if w, ok := lat.(Widener[T]); ok && updates >= WidenAfter {
		return w.Widen(old, next)
	}
	return lat.Join(old, next)
}
