package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// DefaultClaudeBin is the claude executable rrev looks for on PATH.
const DefaultClaudeBin = "claude"

// Claude runs the claude CLI headless, reading its stream-json output so the
// terminal shows the model's text and tool use as they happen.
type Claude struct {
	// Command overrides the executable name or path.
	Command string
	// ExtraArgs are appended to the invocation for a project that needs to pass
	// further claude flags.
	ExtraArgs []string
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
	args := []string{"--print", "--output-format", "stream-json", "--verbose", "--dangerously-skip-permissions"}
	if req.Model != "" {
		args = append(args, "--model", req.Model)
	}
	args = append(args, c.ExtraArgs...)

	cmd := command{tool: c.Name(), bin: c.Bin(), args: args, dir: req.Dir, prompt: req.Prompt, debug: c.Debug}
	col := &collector{stream: req.Stream}
	err := cmd.run(ctx, req.Stream, func(line string) error { return claudeLine(col, line) })
	result := col.result()
	if err != nil {
		if reported, ok := errors.AsType[*claudeResultError](err); ok {
			return result, &Error{Tool: c.Name(), Args: args, ExitCode: -1, Stderr: reported.msg, Err: err}
		}
		return result, err
	}
	return result, nil
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
