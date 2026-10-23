package main

import (
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/patchd/patchd/engine/rules"
	_ "github.com/patchd/patchd/engine/rules/security" // registers the taint rules
)

func newRulesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "rules",
		Short: "List and describe the rules patchd scan runs",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List every rule with its severity and CWE",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			out := &textWriter{w: w}
			out.println("ID\tNAME\tSEVERITY\tCWE\tDEFAULT")
			for _, r := range rules.All() {
				state := "on"
				if r.Disabled {
					state = "off"
				}
				out.printf("%s\t%s\t%s\t%s\t%s\n", r.ID, r.Name, r.Severity, strings.Join(r.CWE, ","), state)
			}
			if out.err != nil {
				return out.err
			}
			return w.Flush()
		},
	})
	return cmd
}
