package main

import (
	"fmt"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/patchd/patchd/internal/buildinfo"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		RunE: func(cmd *cobra.Command, _ []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "patchd %s (commit %s, built %s, %s/%s)\n",
				buildinfo.Version, buildinfo.Commit, buildinfo.Date, runtime.GOOS, runtime.GOARCH)
			return err
		},
	}
}
