package main

import "github.com/spf13/cobra"

func newLSPCmd() *cobra.Command {
	return stub("lsp", "Run the IDE language server")
}
