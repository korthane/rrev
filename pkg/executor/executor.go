package executor

import (
	"context"
	"fmt"
	"io"
	"strings"
)

// Request is one executor call: the prompt to run and where to run it.
type Request struct {
	// Prompt is the fully expanded phase prompt handed to the tool.
	Prompt string
	// Dir is the repository the tool runs in.
	Dir string
	// Phase names the pipeline phase, so a caller can attribute output.
	Phase string
	// Model is the already-resolved model name; empty leaves the tool's own
	// default in place.
	Model string
	// Stream receives the tool's activity as it arrives. A caller that wants
	// phase attribution wraps it before passing it; nil discards the activity.
	Stream io.Writer
}

// Result is what an executor call produced: the tool's own output and the
// termination signal that output carried, if any.
type Result struct {
	Output string
	Signal Signal
}

// Executor is the single contract every AI tool rrev drives is used through, so
// claude, codex, and a user-supplied script are interchangeable to a phase.
type Executor interface {
	// Name identifies the tool in messages and in the progress log.
	Name() string
	// Bin is the executable startup preflight looks for on PATH. An empty
	// string means there is nothing to check.
	Bin() string
	// Run executes the prompt and returns the tool's output with any signal it
	// contained. The result carries whatever was captured even when the error
	// is non-nil, so a cancelled or failed call is still reportable.
	Run(ctx context.Context, req Request) (Result, error)
}

// Error reports an executor invocation that did not complete, carrying the
// tool's own diagnostics so the calling phase can decide to retry or abort.
type Error struct {
	Tool     string
	Args     []string
	ExitCode int
	Stderr   string
	Err      error
}

func (e *Error) Error() string {
	msg := e.Tool
	if len(e.Args) > 0 {
		msg += " " + strings.Join(e.Args, " ")
	}
	if e.Err != nil {
		msg += ": " + e.Err.Error()
	}
	if e.Stderr != "" {
		msg += ": " + e.Stderr
	}
	return msg
}

func (e *Error) Unwrap() error { return e.Err }

// collector accumulates the model's own text while rendering everything worth
// showing to the stream. Tool activity is rendered but not collected: a signal
// must come from what the model said, not from a file it happened to write.
type collector struct {
	stream io.Writer
	text   strings.Builder
}

func (c *collector) say(text string) {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return
	}
	c.line(text)
}

// line records raw output verbatim, blank lines included, for a tool whose
// stdout is the finding text itself rather than a structured event stream.
func (c *collector) line(text string) {
	c.text.WriteString(text)
	c.text.WriteString("\n")
	c.render(text)
}

func (c *collector) activity(note string) { c.render("· " + note) }

// final records a tool's closing summary, which usually repeats the last
// message it already streamed.
func (c *collector) final(text string) {
	if strings.TrimSpace(text) == "" || strings.Contains(c.text.String(), strings.TrimSpace(text)) {
		return
	}
	c.say(text)
}

func (c *collector) render(line string) {
	if c.stream == nil {
		return
	}
	// A terminal that cannot be written to is not worth failing the review for.
	_, _ = fmt.Fprintln(c.stream, line)
}

func (c *collector) result() Result {
	output := c.text.String()
	return Result{Output: output, Signal: Detect(output)}
}
