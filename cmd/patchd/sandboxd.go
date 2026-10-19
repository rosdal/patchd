package main

import "github.com/spf13/cobra"

func newSandboxdCmd() *cobra.Command {
	return stub("sandboxd", "Run the sandbox manager")
}
