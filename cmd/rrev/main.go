// Command rrev reviews the current branch against an OpenSpec change.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "rrev:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out, errOut io.Writer) error {
	opts, err := parseArgs(args, errOut)
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	if err != nil {
		return err
	}
	if opts.Version {
		_, err := fmt.Fprintf(out, "rrev %s\n", version)
		return err
	}

	dir, err := os.Getwd()
	if err != nil {
		return err
	}
	start, err := prepare(ctx, opts, dir)
	if err != nil {
		return err
	}

	for _, warning := range start.Warnings {
		// A terminal that cannot be written to is not worth failing a run for.
		_, _ = fmt.Fprintln(errOut, "rrev:", warning)
	}
	_, err = fmt.Fprintf(out, "rrev %s: reviewing %s against %s in %s mode\n",
		version, start.Review.GoalLine(), start.BaseRef, start.Mode)
	return err
}
