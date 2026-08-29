package executor

import (
	"encoding/json"
	"strings"
)

// toolArgWidth caps a rendered tool argument. Heredocs, multi-line commands and
// whole prompts all arrive as tool arguments, and an unbounded one would break
// the display as thoroughly as echoing the tool's output would.
const toolArgWidth = 100

// truncationMark ends an argument that did not fit, so a reader can tell a
// short command from a shortened one.
const truncationMark = "…"

// toolArgKeys names, per tool, the input field that distinguishes one call from
// another. Without it every call to a tool renders identically, which is what
// made a phase of seven concurrent reviewers read as seven identical lines.
//
// A tool absent from this table renders with no argument rather than a guessed
// one: a wrong argument is worse than none.
var toolArgKeys = map[string][]string{
	"Bash":         {"command"},
	"BashOutput":   {"bash_id"},
	"Read":         {"file_path"},
	"Write":        {"file_path"},
	"Edit":         {"file_path"},
	"NotebookEdit": {"notebook_path"},
	"Glob":         {"pattern"},
	"Grep":         {"pattern"},
	"WebFetch":     {"url"},
	"WebSearch":    {"query"},
	"Skill":        {"skill"},
	"Task":         {"subagent_type", "description"},
	"Agent":        {"subagent_type", "description"},
}

// agentTools launch a sub-agent, so their argument names the agent rather than
// describing a call. That name is the one piece of attribution the stream
// offers for a phase running its reviewers concurrently.
var agentTools = map[string]bool{"Task": true, "Agent": true}

// toolCall is one invocation, held from its launch until its result so both can
// be reported on a single line.
type toolCall struct {
	name  string
	arg   string
	agent string
}

// describeToolCall reads the distinguishing argument out of a tool's input.
func describeToolCall(name string, input json.RawMessage) toolCall {
	call := toolCall{name: name}
	if len(input) == 0 {
		return call
	}
	var fields map[string]any
	if err := json.Unmarshal(input, &fields); err != nil {
		return call
	}
	for _, key := range toolArgKeys[name] {
		text, ok := fields[key].(string)
		if !ok || strings.TrimSpace(text) == "" {
			continue
		}
		call.arg = boundArg(text)
		if agentTools[name] {
			call.agent = call.arg
		}
		break
	}
	return call
}

// boundArg reduces an argument to one bounded line. Only the first line is
// kept: a heredoc's body says far less about the call than its opening does.
func boundArg(text string) string {
	text = strings.TrimSpace(text)
	first, _, multiline := strings.Cut(text, "\n")
	first = strings.TrimSpace(first)
	if len(first) > toolArgWidth {
		return strings.TrimSpace(first[:toolArgWidth]) + truncationMark
	}
	if multiline {
		return first + " " + truncationMark
	}
	return first
}

// line renders the call for the terminal. An agent launch leads with the agent
// it started, since that is what tells one concurrent reviewer from another.
// The tool's own output never appears: a diff or a test run would flood the
// display, and its outcome is what a reader actually needs.
func (c toolCall) line(outcome string) string {
	var b strings.Builder
	if c.agent != "" {
		b.WriteString("agent: " + c.agent)
	} else {
		b.WriteString("tool: " + c.name)
		if c.arg != "" {
			b.WriteString(" " + c.arg)
		}
	}
	if outcome != "" {
		b.WriteString(" → " + outcome)
	}
	return b.String()
}

// failureOutcome reduces a tool's error to one bounded line. The detail is what
// a reader needs to act; the rest of the output is not.
func failureOutcome(detail string) string {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		return "failed"
	}
	return "failed: " + boundArg(detail)
}
