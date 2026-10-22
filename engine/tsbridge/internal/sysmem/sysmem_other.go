//go:build !linux && !windows && !darwin

package sysmem

func available() uint64 { return 0 }

func peakRSS() uint64 { return 0 }
