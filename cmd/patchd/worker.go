package main

import "github.com/spf13/cobra"

func newWorkerCmd() *cobra.Command {
	return stub("worker", "Run review pipeline workers")
}
