// Package tsbridge is the only bridge to typescript-go (parser, binder,
// checker). It discovers the projects in a repo, loads and type-checks them,
// and answers questions about their syntax, symbols, types and calls through
// a small Patchd-owned API (ADR 0008): only Patchd types cross it.
package tsbridge
