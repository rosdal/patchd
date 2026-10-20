// Package buildinfo holds version data injected with -ldflags at build time.
package buildinfo

var (
	// Version is the git describe output, e.g. v0.1.0 or v0.1.0-3-gabc1234-dirty.
	Version = "v0.0.0-dev"
	// Commit is the short commit hash the binary was built from.
	Commit = "none"
	// Date is the build time in RFC 3339 format.
	Date = "unknown"
)
