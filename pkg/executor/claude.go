package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
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
	tools := newClaudeTools()
	err := cmd.run(ctx, col, func(line string) error { return claudeLine(col, tools, line) })
	tools.flush(col)
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
	Type string `json:"type"`
	// ParentToolUseID names the tool call an event belongs to. On a sub-agent's
	// own events it is the Task call that launched it, which is the one place
	// the stream says which of several concurrent reviewers is speaking.
	ParentToolUseID string          `json:"parent_tool_use_id"`
	Message         json.RawMessage `json:"message"`
	Result          string          `json:"result"`
	IsError         bool            `json:"is_error"`
}

type claudeMessage struct {
	Content []claudeBlock `json:"content"`
}

type claudeBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Name      string          `json:"name"`
	ID        string          `json:"id"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
	Content   json.RawMessage `json:"content"`
}

// claudeResultError reports a run claude itself declared failed, which it can
// do while still exiting zero.
type claudeResultError struct{ msg string }

func (e *claudeResultError) Error() string { return "claude reported an error: " + e.msg }

// claudeTools pairs each tool_use with the tool_result that answers it, so one
// line can carry the call and its outcome. Holding the launch costs a little
// latency in the display and buys a reader the outcome they would otherwise
// have to infer from silence.
type claudeTools struct {
	pending map[string]toolCall
	order   []string
	// agents outlives pending: a sub-agent keeps emitting under its launch id
	// after that launch has been reported, and its lines still need naming.
	agents map[string]string
}

func newClaudeTools() *claudeTools {
	return &claudeTools{pending: map[string]toolCall{}, agents: map[string]string{}}
}

// agentFor names the sub-agent an event belongs to, empty when the stream does
// not say. Attribution is never guessed: a phase whose format offers nothing
// falls back to the phase alone.
func (t *claudeTools) agentFor(parentID string) string {
	if parentID == "" {
		return ""
	}
	return t.agents[parentID]
}

func (t *claudeTools) start(col *collector, parentID string, block claudeBlock) {
	call := describeToolCall(block.Name, block.Input)
	// The parent is kept on the call so the flush that reports unanswered ones
	// attributes them as accurately as the launch did.
	call.parent = t.agentFor(parentID)
	// A sub-agent runs for minutes. Announcing it only once it finishes is the
	// unexplained pause this reporting exists to prevent, so its launch is
	// rendered straight away and its outcome follows later.
	if call.launch {
		col.activityAs(call.parent, call.line(""))
	}
	if block.ID == "" {
		// No id to match a result against, so report it now rather than hold a
		// line that nothing will ever release. A launch was already rendered
		// above; only a launch ever is.
		if !call.launch {
			col.activityAs(call.parent, call.line(""))
		}
		return
	}
	t.pending[block.ID] = call
	t.order = append(t.order, block.ID)
	if call.agent != "" {
		t.agents[block.ID] = call.agent
	}
}

// finish releases the call a result answers, returning the line to render and
// the sub-agent the launch attributed it to. The parent comes back because a
// result event need not repeat the attribution its launch carried.
func (t *claudeTools) finish(block claudeBlock) (rendered, parent string, ok bool) {
	call, ok := t.pending[block.ToolUseID]
	if !ok {
		return "", "", false
	}
	delete(t.pending, block.ToolUseID)
	t.order = slices.DeleteFunc(t.order, func(id string) bool { return id == block.ToolUseID })
	if block.IsError {
		return call.line(failureOutcome(claudeResultText(block.Content))), call.parent, true
	}
	return call.line("ok"), call.parent, true
}

// flush reports calls the stream never answered, so a run cut short still shows
// what it was doing when it stopped.
func (t *claudeTools) flush(col *collector) {
	for _, id := range t.order {
		if call := t.pending[id]; !call.launch {
			col.activityAs(call.parent, call.line(""))
		}
	}
	t.order, t.pending = nil, map[string]toolCall{}
}

// claudeResultText pulls readable text out of a tool result, which claude sends
// either as a string or as a list of content blocks.
func claudeResultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var blocks []claudeBlock
	if err := json.Unmarshal(raw, &blocks); err != nil {
		return ""
	}
	var texts []string
	for _, b := range blocks {
		if b.Text != "" {
			texts = append(texts, b.Text)
		}
	}
	// Every block, not the first: debug records a tool's full output, and the
	// displayed failure detail is bounded to one line after this returns.
	return strings.Join(texts, "\n")
}

func claudeLine(col *collector, tools *claudeTools, line string) error {
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
				col.sayAs(tools.agentFor(event.ParentToolUseID), block.Text)
			case "tool_use":
				tools.start(col, event.ParentToolUseID, block)
				col.detail("tool input", string(block.Input))
			}
		}
	case "user":
		for _, block := range claudeBlocks(event.Message) {
			if block.Type != "tool_result" {
				continue
			}
			if rendered, parent, ok := tools.finish(block); ok {
				col.activityAs(parent, rendered)
				// Decoded only under debug: the result carries the tool's whole
				// output, and re-decoding megabytes to discard them is what
				// every non-debug run would otherwise pay per tool call.
				if col.debug {
					col.detail("tool output", claudeResultText(block.Content))
				}
			}
		}
	case "result":
		tools.flush(col)
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
