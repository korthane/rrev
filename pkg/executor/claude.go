package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// DefaultClaudeBin is the claude executable rrev looks for on PATH.
const DefaultClaudeBin = "claude"

// Claude runs the claude CLI headless, reading its stream-json output so the
// terminal shows the model's text and tool use as they happen.
type Claude struct {
	// Command overrides the executable name or path.
	Command string
	// Limits bound one call; every bound is disabled at zero.
	Limits Limits
	// Debug records the resolved command line and the full prompt.
	Debug bool
}

// Name identifies the tool.
func (c Claude) Name() string { return "claude" }

// Bin is the executable preflight checks for.
func (c Claude) Bin() string {
	if c.Command != "" {
		return c.Command
	}
	return DefaultClaudeBin
}

// Run executes the prompt through the claude CLI.
func (c Claude) Run(ctx context.Context, req Request) (Result, error) {
	out := newSyncWriter(req.Stream)
	spec := c.spec(req, out)

	args := []string{"--print", "--output-format", "stream-json", "--verbose", "--dangerously-skip-permissions"}
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	if spec.Effort != "" {
		args = append(args, "--effort", spec.Effort)
	}

	cmd := command{tool: c.Name(), bin: c.Bin(), args: args, dir: req.Dir, prompt: req.Prompt, limits: c.Limits, debug: c.Debug}
	col := &collector{stream: out}
	err := cmd.run(ctx, out, func(line string) error { return claudeLine(col, line) })
	result := col.result()
	if reported, ok := errors.AsType[*claudeResultError](err); ok {
		err = &Error{Tool: c.Name(), Args: args, ExitCode: -1, Stderr: reported.msg, Err: err}
	}
	return result, classify(c.Name(), result, err)
}

// spec drops an effort level claude does not accept, reporting it rather than
// passing it through to a flag value the CLI would reject.
func (c Claude) spec(req Request, out io.Writer) Spec {
	spec, warning := Spec{Model: req.Model, Effort: req.Effort}.For(c.Name())
	if warning != "" {
		_, _ = fmt.Fprintln(out, "· "+warning)
	}
	return spec
}

// claudeEvent is one stream-json line. Only the fields rrev renders are
// declared; unknown event types and unknown fields are ignored, since the
// stream format gains events over time.
type claudeEvent struct {
	Type    string          `json:"type"`
	Message json.RawMessage `json:"message"`
	Result  string          `json:"result"`
	IsError bool            `json:"is_error"`
}

type claudeMessage struct {
	Content []claudeBlock `json:"content"`
}

type claudeBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
	Name string `json:"name"`
}

// claudeResultError reports a run claude itself declared failed, which it can
// do while still exiting zero.
type claudeResultError struct{ msg string }

func (e *claudeResultError) Error() string { return "claude reported an error: " + e.msg }

func claudeLine(col *collector, line string) error {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil
	}
	var event claudeEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		// A wrapper script or a crash message prints plain text on this stream;
		// showing it beats dropping it.
		col.say(line)
		return nil
	}

	switch event.Type {
	case "assistant":
		for _, block := range claudeBlocks(event.Message) {
			switch block.Type {
			case "text":
				col.say(block.Text)
			case "tool_use":
				col.activity("tool: " + block.Name)
			}
		}
	case "result":
		if event.IsError {
			return &claudeResultError{msg: claudeErrorText(event)}
		}
		col.final(event.Result)
	}
	return nil
}

// claudeBlocks decodes a message's content blocks, tolerating the plain-string
// form other event types use for the same field.
func claudeBlocks(raw json.RawMessage) []claudeBlock {
	if len(raw) == 0 {
		return nil
	}
	var msg claudeMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return nil
	}
	return msg.Content
}

func claudeErrorText(event claudeEvent) string {
	if text := strings.TrimSpace(event.Result); text != "" {
		return text
	}
	return fmt.Sprintf("%s event reported is_error", event.Type)
}
