// Package sysmem reports how much memory the machine has available, for the
// tsbridge memory budget.
package sysmem

// Available returns the bytes of memory available to new allocations, or 0
// when it can't be determined.
func Available() uint64 { return available() }

// PeakRSS returns the process's peak resident set size in bytes, or 0 when
// it can't be determined.
func PeakRSS() uint64 { return peakRSS() }
