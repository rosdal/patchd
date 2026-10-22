package main

import (
	"github.com/spf13/cobra"

	"github.com/patchd/patchd/engine/tsbridge"
)

type callAnswer struct {
	Line          int                       `json:"line"`
	Column        int                       `json:"column"`
	Kind          string                    `json:"kind"`
	Text          string                    `json:"text"`
	QualifiedName string                    `json:"qualified_name,omitempty"`
	Declarations  []tsbridge.Location       `json:"declarations,omitempty"`
	Reason        tsbridge.UnresolvedReason `json:"reason,omitempty"`
}

func newDebugResolveCmd(f *debugFlags) *cobra.Command {
	return &cobra.Command{
		Use:   "resolve <file>",
		Short: "List every call in a file with its resolved qualified name, or the reason it did not resolve",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := f.validate(); err != nil {
				return err
			}
			p, sf, err := f.loadFile(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			defer p.Close()
			calls := resolveCalls(p, sf)
			out := cmd.OutOrStdout()
			tw := &textWriter{w: out}
			if f.format == "json" {
				return writeJSON(out, calls)
			}
			resolved := 0
			for _, c := range calls {
				if c.Reason == "" {
					resolved++
					tw.printf("%d:%d %s -> %s\n", c.Line, c.Column, c.Text, c.QualifiedName)
				} else {
					tw.printf("%d:%d %s -> unresolved (%s)\n", c.Line, c.Column, c.Text, c.Reason)
				}
			}
			tw.printf("%d call(s), %d resolved\n", len(calls), resolved)
			return tw.err
		},
	}
}

func resolveCalls(p *tsbridge.Program, sf *tsbridge.File) []callAnswer {
	var calls []callAnswer
	p.Walk(sf.ID, func(n tsbridge.NodeID) bool {
		k := p.Kind(n)
		if !k.IsCallLike() {
			return true
		}
		line, col := sf.Position(p.Range(n).Pos)
		c := callAnswer{Line: line, Column: col, Kind: k.String(), Text: truncate(p.Text(n), 60)}
		callee, ok := p.ResolveCall(n)
		if ok {
			c.QualifiedName, c.Declarations = callee.QualifiedName, callee.Declarations
		} else {
			c.Reason = callee.Reason
		}
		calls = append(calls, c)
		return true
	})
	return calls
}
