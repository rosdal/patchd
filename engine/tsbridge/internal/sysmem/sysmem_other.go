//go:build !linux && !windows && !darwin

package sysmem

func available() uint64 { return 0 }
