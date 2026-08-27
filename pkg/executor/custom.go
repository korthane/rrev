package executor

import (
	"context"
	"errors"
	"strings"
)

// ErrNoCommand reports a custom external reviewer with nothing configured to
// run.
var ErrNoCommand = errors.New("no external review command configured")

// Custom runs a user-supplied external review script: the prompt goes in on
// stdin and everything the script writes to stdout is its findings.
type Custom struct {
	// Command is the script and its fixed arguments, split on whitespace. It is
	// executed directly rather than through a shell, so quoting and shell
	// operators are not interpreted and preflight can check the executable.
	Command string
	// Limits bound one call; every bound is disabled at zero.
	Limits Limits
	// Debug records the resolved command line and the full prompt.
	Debug bool
}

// Name identifies the tool.
func (c Custom) Name() string { return "custom" }

// Bin is the executable preflight checks for.
func (c Custom) Bin() string {
	fields := strings.Fields(c.Command)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// Run executes the review prompt through the configured script.
func (c Custom) Run(ctx context.Context, req Request) (Result, error) {
	fields := strings.Fields(c.Command)
	if len(fields) == 0 {
		return Result{}, ErrNoCommand
	}

	out := newSyncWriter(req.Stream)
	cmd := command{tool: c.Name(), bin: fields[0], args: fields[1:], dir: req.Dir, prompt: req.Prompt, limits: c.Limits, debug: c.Debug}
	col := &collector{stream: out}
	err := cmd.run(ctx, col, func(line string) error { col.line(line); return nil })
	result := col.result()
	return result, classify(c.Name(), result, err)
}
