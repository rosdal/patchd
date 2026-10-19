package main

import "github.com/spf13/cobra"

func newServeCmd() *cobra.Command {
	return stub("serve", "Run the API, webhooks and static web app")
}
