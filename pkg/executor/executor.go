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
	// Model and Effort are the already-resolved model selection; either empty
	// leaves the tool's own default in place.
	Model  string
	Effort string
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
	// Output is the tail of what the tool wrote to stdout before failing,
	// which is the only diagnostic a tool that exits silently on stderr
	// leaves. An executor with a channel of its own — claude, in its result
	// event — has that message appended to Stderr instead.
	Output string
	Err    error
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
	// touch reports rendered output to the watchdog, which is set while a
	// command runs; nothing else renders, so it is nil until then.
	touch func()
	// debug turns on the unabridged rendering. Normal output is deliberately
	// bounded — a diff or a test run would flood the display — so the full
	// arguments and output of a tool call are only ever shown here.
	debug bool
}

func (c *collector) say(text string) { c.sayAs("", text) }

// sayAs collects the model's text verbatim while rendering it under the
// sub-agent that produced it. The attribution is display-only on purpose: the
// collected text is what signals and report lines are parsed from, and a
// prefix in there would corrupt both.
func (c *collector) sayAs(agent, text string) {
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return
	}
	c.text.WriteString(text)
	c.text.WriteString("\n")
	if agent == "" {
		c.render(text)
		return
	}
	// Every line carries the attribution, not just the first: the printer
	// prefixes each line it splits out with the phase alone, so a paragraph
	// rendered as one block would leave all but its opening line
	// indistinguishable from the other reviewers running beside it.
	for line := range strings.SplitSeq(text, "\n") {
		c.render("[" + agent + "] " + line)
	}
}

// line records raw output verbatim, blank lines included, for a tool whose
// stdout is the finding text itself rather than a structured event stream.
func (c *collector) line(text string) {
	c.text.WriteString(text)
	c.text.WriteString("\n")
	c.render(text)
}

func (c *collector) activity(note string) { c.render("· " + note) }

// activityAs attributes a note to the sub-agent that produced it, where the
// stream said which one that was.
func (c *collector) activityAs(agent, note string) {
	if agent == "" {
		c.activity(note)
		return
	}
	c.render("· [" + agent + "] " + note)
}

// detail renders only under debug, where the caps that keep normal output
// readable are lifted.
func (c *collector) detail(label, text string) {
	if !c.debug || strings.TrimSpace(text) == "" {
		return
	}
	c.render("· " + label + ": " + strings.TrimRight(text, "\n"))
}

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
	if c.touch != nil {
		c.touch()
	}
	// A terminal that cannot be written to is not worth failing the review for.
	_, _ = fmt.Fprintln(c.stream, line)
}

func (c *collector) result() Result {
	output := c.text.String()
	return Result{Output: output, Signal: Detect(output)}
}
