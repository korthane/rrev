// Command rrev reviews the current branch against an OpenSpec change.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/korthane/rrev/pkg/status"
)

// version is overridden at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	os.Exit(int(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr)))
}

// run executes one command line and reports the status the process exits with.
func run(ctx context.Context, args []string, out, errOut io.Writer) status.Code {
	code, err := execute(ctx, args, out, errOut)
	if err != nil {
		// A terminal that cannot be written to is not worth reporting on.
		_, _ = fmt.Fprintln(errOut, "rrev:", err)
	}
	return code
}

// execute separates the failures worth printing as one line, which are the ones
// that stop a run before it starts, from a run that reports its own outcome.
func execute(ctx context.Context, args []string, out, errOut io.Writer) (status.Code, error) {
	opts, err := parseArgs(args, errOut)
	if errors.Is(err, flag.ErrHelp) {
		return status.CodeOK, nil
	}
	if err != nil {
		return status.CodeFailed, err
	}
	if opts.Version {
		_, _ = fmt.Fprintf(out, "rrev %s\n", version)
		return status.CodeOK, nil
	}

	dir, err := os.Getwd()
	if err != nil {
		return status.CodeFailed, err
	}
	start, err := prepare(ctx, opts, dir)
	if err != nil {
		return status.CodeFailed, err
	}
	for _, warning := range start.Warnings {
		_, _ = fmt.Fprintln(errOut, "rrev:", warning)
	}
	return launch(ctx, start, out, errOut), nil
}
