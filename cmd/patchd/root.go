package main

import (
	"errors"

	"github.com/spf13/cobra"
)

var errNotImplemented = errors.New("not implemented yet")

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "patchd",
		Short:         "Patchd Code: find it, prove it, patch it",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		newVersionCmd(),
		newServeCmd(),
		newWorkerCmd(),
		newSandboxdCmd(),
		newReviewCmd(),
		newScanCmd(),
		newFixCmd(),
		newLSPCmd(),
		newMCPCmd(),
	)
	return root
}

// stub returns a placeholder subcommand that fails with errNotImplemented.
func stub(use, short string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short + " (not implemented yet)",
		RunE:  func(*cobra.Command, []string) error { return errNotImplemented },
	}
}
