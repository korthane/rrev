// Command rrev reviews the current branch against an OpenSpec change.
package main

import (
	"fmt"
	"io"
	"os"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "rrev:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	if len(args) > 0 && (args[0] == "-version" || args[0] == "--version") {
		_, err := fmt.Fprintf(out, "rrev %s\n", version)
		return err
	}
	_, err := fmt.Fprintf(out, "rrev %s: not implemented yet\n", version)
	return err
}
