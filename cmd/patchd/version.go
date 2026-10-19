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
		Run: func(cmd *cobra.Command, _ []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "patchd %s (commit %s, built %s, %s/%s)\n",
				buildinfo.Version, buildinfo.Commit, buildinfo.Date, runtime.GOOS, runtime.GOARCH)
		},
	}
}
