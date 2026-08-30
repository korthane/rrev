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
	limits Limits
	debug  bool
}

// run starts the command and calls onLine for every stdout line as it arrives,
// so a phase reports progress rather than an unexplained pause. An error from
// onLine is remembered but does not stop the scan: the tool keeps running and
// its remaining output stays worth capturing. The collector is passed in whole
// so the watchdog learns which output actually reached the terminal.
func (c command) run(ctx context.Context, col *collector, onLine func(string) error) error {
	stream := col.stream
	col.debug = c.debug
	if c.debug && stream != nil {
		_, _ = fmt.Fprintf(stream, "· exec: %s %s\n· prompt:\n%s\n", c.bin, strings.Join(c.args, " "), c.prompt)
	}

	// The tool is started outside the caller's context so cancellation goes
	// through the process group, which reaches the sub-agents exec would leave
	// running.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	guard := newWatchdog(c.limits, stream)
	// Only what the collector renders is a sign of life the user can see, so
	// that is what holds off the progress note.
	col.touch = guard.touched

	cmd := exec.Command(c.bin, c.args...)
	cmd.Dir = c.dir
	cmd.Stdin = strings.NewReader(c.prompt)
	stderr := &tailWriter{limit: stderrTailBytes}
	// Diagnostics on stderr are kept back for a failure report, so a tool
	// reporting its progress there only proves it is alive: enough to hold off
	// the idle bound, not enough to stand in for the progress note.
	cmd.Stderr = &activityWriter{to: stderr, touch: guard.stirred}
	group, err := newProcessGroup(cmd)
	if err != nil {
		// Without a process group a cancelled run would orphan the tool's
		// sub-agents, so the phase fails before anything is started.
		return c.fail(stderr, col, err)
	}
	defer group.close()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return c.fail(stderr, col, err)
	}
	if err := cmd.Start(); err != nil {
		return c.fail(stderr, col, err)
	}
	if err := group.started(); err != nil {
		// The tool is running but not under our control, so it is stopped here
		// rather than left to finish a review nothing can cancel.
		group.kill()
		_ = cmd.Wait()
		return c.fail(stderr, col, err)
	}

	go guard.watch(cancel)
	stopKill := killOnCancel(runCtx, group)

	var lineErr error
	// Raw bytes arriving prove the tool is alive, not that it said anything:
	// a long line still in the scanner, or an event rrev does not render, puts
	// nothing on the terminal, so it holds off the idle bound only.
	scanner := bufio.NewScanner(&activityReader{from: stdout, touch: guard.stirred})
	scanner.Buffer(make([]byte, 0, bufio.MaxScanTokenSize), maxLineBytes)
	for scanner.Scan() {
		if err := onLine(scanner.Text()); err != nil && lineErr == nil {
			lineErr = err
		}
	}
	scanErr := scanner.Err()
	if scanErr != nil {
		// The scanner stops for good on a line over maxLineBytes; without a
		// drain the tool blocks writing into a pipe nobody reads and Wait
		// never returns.
		_, _ = io.Copy(io.Discard, stdout)
	}

	waitErr := cmd.Wait()
	guard.stop()
	stopKill()

	if timeout := guard.timeout(c.tool); timeout != nil {
		// Wrapped like any other failure so the tool's last words survive the
		// bound that cut it short; the timeout sentinels still match through it.
		return c.fail(stderr, col, timeout)
	}
	switch {
	case waitErr != nil && ctx.Err() != nil:
		return c.fail(stderr, col, ctx.Err())
	case waitErr != nil:
		return c.fail(stderr, col, waitErr)
	case lineErr != nil:
		return lineErr
	case scanErr != nil:
		return c.fail(stderr, col, fmt.Errorf("read output: %w", scanErr))
	default:
		return nil
	}
}

// killOnCancel terminates the tool's whole process group when ctx is cancelled,
// so a cancelled review leaves no sub-agent behind. The returned function ends
// the watch once the tool has exited, and waits for a kill already in flight:
// releasing the group while it is being terminated would race on its handles.
func killOnCancel(ctx context.Context, group *processGroup) func() {
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-ctx.Done():
			group.kill()
		case <-done:
		}
	}()
	return func() {
		close(done)
		<-stopped
	}
}

func (c command) fail(stderr *tailWriter, col *collector, err error) error {
	// The stdout tail goes through the same writer as stderr, so both are
	// cut at the same bound and tidied the same way.
	stdout := &tailWriter{limit: stderrTailBytes}
	_, _ = stdout.Write([]byte(col.text.String()))
	failure := &Error{
		Tool:     c.tool,
		Args:     c.args,
		ExitCode: -1,
		Stderr:   stderr.String(),
		Output:   stdout.String(),
		Err:      err,
	}
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		failure.ExitCode = exitErr.ExitCode()
	}
	return failure
}

// activityWriter reports every write to the watchdog before passing it on.
type activityWriter struct {
	to    io.Writer
	touch func()
}

func (w *activityWriter) Write(p []byte) (int, error) {
	w.touch()
	return w.to.Write(p)
}

// activityReader reports every non-empty read to the watchdog.
type activityReader struct {
	from  io.Reader
	touch func()
}

func (r *activityReader) Read(p []byte) (int, error) {
	n, err := r.from.Read(p)
	if n > 0 {
		r.touch()
	}
	return n, err
}

// tailWriter keeps only the last limit bytes written to it.
type tailWriter struct {
	limit int
	buf   []byte
	cut   bool
}

func (w *tailWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	if len(w.buf) > w.limit {
		w.buf = w.buf[len(w.buf)-w.limit:]
		w.cut = true
	}
	return len(p), nil
}

func (w *tailWriter) String() string {
	text := string(w.buf)
	if w.cut {
		text = afterCut(text)
	}
	return strings.TrimSpace(text)
}

// afterCut tidies text a byte cut opened mid-line: what precedes the first
// newline is a fragment the tool never wrote as a line. A last line longer
// than the bound has no such newline and keeps its end, minus any bytes the
// cut left mid-rune, rather than leaving nothing.
func afterCut(text string) string {
	text = strings.TrimRight(text, "\n")
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[i+1:]
	}
	return strings.ToValidUTF8(text, "")
}
