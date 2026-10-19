package main

import "github.com/spf13/cobra"

func newScanCmd() *cobra.Command {
	return stub("scan", "Scan the full repository and write SARIF")
}
