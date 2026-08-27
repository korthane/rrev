package executor

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

// maxLineBytes bounds one line of tool output. A stream-json event carrying a
// large tool result is a single line, so the scanner needs far more than its
// default 64KB.
const maxLineBytes = 8 << 20

// stderrTailBytes caps the diagnostics kept from a failing tool: the tail is
// where the reason lives, and a tool that spews must not exhaust memory.
const stderrTailBytes = 8 << 10

// command is one external tool invocation. The prompt goes in on stdin so a
// long prompt cannot hit the platform's argument-length limit.
type command struct {
	tool   string
	bin    string
	args   []string
	dir    string
	prompt string
	debug  bool
}

// run starts the command and calls onLine for every stdout line as it arrives,
// so a phase reports progress rather than an unexplained pause. An error from
// onLine is remembered but does not stop the scan: the tool keeps running and
// its remaining output stays worth capturing.
func (c command) run(ctx context.Context, stream io.Writer, onLine func(string) error) error {
	if c.debug && stream != nil {
		_, _ = fmt.Fprintf(stream, "· exec: %s %s\n· prompt:\n%s\n", c.bin, strings.Join(c.args, " "), c.prompt)
	}

	cmd := exec.CommandContext(ctx, c.bin, c.args...)
	cmd.Dir = c.dir
	cmd.Stdin = strings.NewReader(c.prompt)
	stderr := &tailWriter{limit: stderrTailBytes}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return c.fail(stderr, err)
	}
	if err := cmd.Start(); err != nil {
		return c.fail(stderr, err)
	}

	var lineErr error
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, bufio.MaxScanTokenSize), maxLineBytes)
	for scanner.Scan() {
		if err := onLine(scanner.Text()); err != nil && lineErr == nil {
			lineErr = err
		}
	}
	scanErr := scanner.Err()

	if err := cmd.Wait(); err != nil {
		return c.fail(stderr, err)
	}
	if lineErr != nil {
		return lineErr
	}
	if scanErr != nil {
		return c.fail(stderr, fmt.Errorf("read output: %w", scanErr))
	}
	return nil
}

func (c command) fail(stderr *tailWriter, err error) error {
	failure := &Error{Tool: c.tool, Args: c.args, ExitCode: -1, Stderr: stderr.String(), Err: err}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		failure.ExitCode = exitErr.ExitCode()
	}
	return failure
}

// tailWriter keeps only the last limit bytes written to it.
type tailWriter struct {
	limit int
	buf   []byte
}

func (w *tailWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	if len(w.buf) > w.limit {
		w.buf = w.buf[len(w.buf)-w.limit:]
	}
	return len(p), nil
}

func (w *tailWriter) String() string { return strings.TrimSpace(string(w.buf)) }
