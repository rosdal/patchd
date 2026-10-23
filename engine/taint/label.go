// Taint labels and the taint lattice.

package taint

import (
	"fmt"
	"strings"
)

// Label is one strand of taint on a value (ADR 0010): which source
// occurrence it came from, which sink kinds it can still reach, and whether
// type pruning removed kinds on the way.
type Label struct {
	Origin  int32   // index into FuncResult.Origins
	Kinds   KindSet // sink kinds still reachable
	Numeric bool    // passed through a number or boolean: SQL, command, path and URL kinds were dropped
}

// Fact is a value's taint: at most one label per origin, sorted by origin.
// Facts are immutable once built.
type Fact []Label

func (f Fact) String() string {
	parts := make([]string, len(f))
	for i, l := range f {
		parts[i] = fmt.Sprintf("o%d%s", l.Origin, l.Kinds)
		if l.Numeric {
			parts[i] += "#"
		}
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// Label returns the label of an origin, if the fact has one.
func (f Fact) Label(origin int32) (Label, bool) {
	for _, l := range f {
		if l.Origin == origin {
			return l, true
		}
	}
	return Label{}, false
}

// Map returns the fact with fn applied to each label; labels fn drops (ok
// false) are left out.
func (f Fact) Map(fn func(Label) (Label, bool)) Fact {
	if len(f) == 0 {
		return nil
	}
	out := make(Fact, 0, len(f))
	for _, l := range f {
		if m, ok := fn(l); ok {
			out = append(out, m)
		}
	}
	return out
}

// Lattice is the taint lattice: join is set union, merging labels of one
// origin by uniting their kinds; bottom is the empty fact.
type Lattice struct{}

// Bottom is the empty fact.
func (Lattice) Bottom() Fact { return nil }

// Join unites two facts.
func (Lattice) Join(a, b Fact) Fact {
	if len(a) == 0 {
		return b
	}
	if len(b) == 0 {
		return a
	}
	out := make(Fact, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i].Origin < b[j].Origin:
			out = append(out, a[i])
			i++
		case a[i].Origin > b[j].Origin:
			out = append(out, b[j])
			j++
		default:
			out = append(out, Label{Origin: a[i].Origin, Kinds: a[i].Kinds | b[j].Kinds, Numeric: a[i].Numeric && b[j].Numeric})
			i++
			j++
		}
	}
	out = append(out, a[i:]...)
	return append(out, b[j:]...)
}

// Leq reports whether b holds every label and kind of a. A numeric label is
// below its non-numeric counterpart (the join of the two is non-numeric).
func (Lattice) Leq(a, b Fact) bool {
	j := 0
	for _, l := range a {
		for j < len(b) && b[j].Origin < l.Origin {
			j++
		}
		if j == len(b) || b[j].Origin != l.Origin || l.Kinds&^b[j].Kinds != 0 || (!l.Numeric && b[j].Numeric) {
			return false
		}
	}
	return true
}
