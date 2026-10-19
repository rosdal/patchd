package main

import "github.com/spf13/cobra"

func newReviewCmd() *cobra.Command {
	return stub("review", "Review a local diff or branch")
}
