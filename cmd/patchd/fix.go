package main

import "github.com/spf13/cobra"

func newFixCmd() *cobra.Command {
	return stub("fix", "Apply verified patches")
}
