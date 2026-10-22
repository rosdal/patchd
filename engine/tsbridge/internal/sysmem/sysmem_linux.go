package sysmem

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"syscall"
)

// available is MemAvailable, or less when a cgroup (a container) limits
// this process: its limit minus what the cgroup already uses.
func available() uint64 {
	avail := memAvailable()
	if limit, used, ok := cgroupMemory(); ok && limit > used && (avail == 0 || limit-used < avail) {
		return limit - used
	}
	return avail
}

// cgroupMemory reads the memory limit and usage of this process's cgroup
// (v2, then v1). ok is false when there is no limit.
func cgroupMemory() (limit, used uint64, ok bool) {
	read := func(p string) (uint64, bool) {
		b, err := os.ReadFile(p) //nolint:gosec // fixed cgroup paths below, not input
		if err != nil {
			return 0, false
		}
		s := strings.TrimSpace(string(b))
		if s == "max" {
			return 0, false
		}
		n, err := strconv.ParseUint(s, 10, 64)
		return n, err == nil
	}
	if l, ok := read("/sys/fs/cgroup/memory.max"); ok {
		u, _ := read("/sys/fs/cgroup/memory.current")
		return l, u, true
	}
	// cgroup v1 reports "no limit" as a huge number.
	if l, ok := read("/sys/fs/cgroup/memory/memory.limit_in_bytes"); ok && l < 1<<60 {
		u, _ := read("/sys/fs/cgroup/memory/memory.usage_in_bytes")
		return l, u, true
	}
	return 0, 0, false
}

func memAvailable() uint64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }() // read-only: nothing to lose on close
	s := bufio.NewScanner(f)
	for s.Scan() {
		if rest, ok := strings.CutPrefix(s.Text(), "MemAvailable:"); ok {
			kb, err := strconv.ParseUint(strings.TrimSuffix(strings.TrimSpace(rest), " kB"), 10, 64)
			if err != nil {
				return 0
			}
			return kb * 1024
		}
	}
	return 0
}

func peakRSS() uint64 {
	var ru syscall.Rusage
	if syscall.Getrusage(syscall.RUSAGE_SELF, &ru) != nil {
		return 0
	}
	return uint64(ru.Maxrss) * 1024 //nolint:gosec // Maxrss is non-negative, in KiB on Linux
}
