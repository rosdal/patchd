package sysmem

import "golang.org/x/sys/unix"

// available on macOS is total physical memory: the kernel reclaims cached
// pages on demand, so "free" memory understates what a process can use.
func available() uint64 {
	n, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return 0
	}
	return n
}
