// Package sysmem reports how much memory the machine has available, for the
// tsbridge memory budget.
package sysmem

// Available returns the bytes of memory available to new allocations, or 0
// when it can't be determined.
func Available() uint64 { return available() }
