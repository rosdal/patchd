// Package dataflow is the generic dataflow framework every analysis reuses:
// lattices, a sparse solver over SSA def-use chains with a dense per-block
// solver for memory, and reaching stores for cells and module globals.
// Function summaries follow in v0.5.
package dataflow
