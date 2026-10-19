package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var errNotImplemented = errors.New("not implemented yet")

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

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

func stub(use, short string) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		RunE:  func(*cobra.Command, []string) error { return errNotImplemented },
	}
}
