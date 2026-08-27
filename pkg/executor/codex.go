package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// DefaultCodexBin is the codex executable rrev looks for on PATH.
const DefaultCodexBin = "codex"

// Codex runs the codex CLI in non-interactive exec mode, reading its JSON event
// stream.
type Codex struct {
	// Command overrides the executable name or path.
	Command string
	// Limits bound one call; every bound is disabled at zero.
	Limits Limits
	// Debug records the resolved command line and the full prompt.
	Debug bool
}

// Name identifies the tool.
func (c Codex) Name() string { return "codex" }

// Bin is the executable preflight checks for.
func (c Codex) Bin() string {
	if c.Command != "" {
		return c.Command
	}
	return DefaultCodexBin
}

// Run executes the prompt through the codex CLI. The prompt arrives on stdin,
// which the trailing `-` argument selects.
func (c Codex) Run(ctx context.Context, req Request) (Result, error) {
	out := newSyncWriter(req.Stream)
	spec, warning := Spec{Model: req.Model, Effort: req.Effort}.For(c.Name())
	if warning != "" {
		_, _ = fmt.Fprintln(out, "· "+warning)
	}

	args := []string{"exec", "--json", "--dangerously-bypass-approvals-and-sandbox", "--skip-git-repo-check"}
	if spec.Model != "" {
		args = append(args, "--model", spec.Model)
	}
	if spec.Effort != "" {
		args = append(args, "-c", "model_reasoning_effort="+spec.Effort)
	}
	args = append(args, "-")

	cmd := command{tool: c.Name(), bin: c.Bin(), args: args, dir: req.Dir, prompt: req.Prompt, limits: c.Limits, debug: c.Debug}
	col := &collector{stream: out}
	err := cmd.run(ctx, out, func(line string) error { codexLine(col, line); return nil })
	result := col.result()
	return result, classify(c.Name(), result, err)
}

// codexEvent is one line of codex's JSON stream. Two shapes are accepted: the
// `msg`-wrapped events of older releases and the `item` events of newer ones.
type codexEvent struct {
	Type string `json:"type"`
	Msg  *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
		Text    string `json:"text"`
	} `json:"msg"`
	Item *struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"item"`
}

func codexLine(col *collector, line string) {
	line = strings.TrimSpace(line)
	if line == "" {
		return
	}
	var event codexEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		// codex prints plain text when it is not speaking JSON, and a wrapper
		// script may print anything at all.
		col.say(line)
		return
	}

	switch {
	case event.Msg != nil:
		codexPart(col, event.Msg.Type, firstNonEmpty(event.Msg.Message, event.Msg.Text))
	// Only completed items are rendered; a started item repeats as a completed
	// one, and rendering both would duplicate the model's text.
	case event.Item != nil && event.Type == "item.completed":
		codexPart(col, event.Item.Type, event.Item.Text)
	}
}

// codexPart renders one event part. Unknown types are ignored so a codex
// release that adds events does not break the stream.
func codexPart(col *collector, kind, text string) {
	switch kind {
	case "agent_message":
		col.say(text)
	case "error":
		col.say(text)
	case "agent_reasoning", "reasoning":
		col.activity("thinking")
	case "exec_command_begin", "command_execution":
		col.activity("command")
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
