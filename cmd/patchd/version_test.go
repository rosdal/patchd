package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	cmd := newRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"version"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.String(), "patchd ") {
		t.Fatalf("unexpected output: %q", out.String())
	}
}
