// Package engine is the public entry point of the analysis engine:
// Analyze(ctx, root, opts) discovers and loads the projects under root,
// lowers them to IR, runs taint analysis and the selected rules, and returns
// Findings with traces and stable fingerprints.
package engine
