package main

import (
	"errors"
	"io"
	"testing"
)

func TestStubsReturnNotImplemented(t *testing.T) {
	for _, name := range []string{"serve", "worker", "sandboxd", "scan", "review", "fix", "lsp", "mcp"} {
		t.Run(name, func(t *testing.T) {
			cmd := newRootCmd()
			cmd.SetOut(io.Discard)
			cmd.SetArgs([]string{name})
			if err := cmd.Execute(); !errors.Is(err, errNotImplemented) {
				t.Fatalf("got %v, want errNotImplemented", err)
			}
		})
	}
}
