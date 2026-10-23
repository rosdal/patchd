package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

func main() {
	cmd := newRootCmd()
	cmd.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return &exitError{code: exitUsage, err: err} })
	if err := cmd.Execute(); err != nil {
		code := 1
		var ee *exitError
		if errors.As(err, &ee) {
			code = ee.code
		}
		if ee == nil || ee.err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(code)
	}
}
