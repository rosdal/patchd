// Command spike-tsgo is the throwaway typescript-go spike behind ADR 0007:
//
//	spike-tsgo path/to/tsconfig.json > calls.txt
//
// It prints one line per call expression and a stats summary on stderr.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os"

	"github.com/patchd/patchd/engine/tsbridge"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: spike-tsgo path/to/tsconfig.json")
		os.Exit(2)
	}
	out := bufio.NewWriter(os.Stdout)
	stats, err := tsbridge.SpikeCalls(context.Background(), os.Args[1], out)
	if ferr := out.Flush(); err == nil {
		err = ferr
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "%+v\n", *stats)
}
