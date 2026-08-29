package executor

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
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

// agentKeys names, per sub-agent-launching tool, the one input field that may
// stand for the agent. That name is the only attribution the stream offers for
// a phase running its reviewers concurrently, and a launch that omits the field
// contributes none: a free-text description used as an agent name would label
// every line of that agent with a sentence.
var agentKeys = map[string]string{"Task": "subagent_type", "Agent": "subagent_type"}

// toolCall is one invocation, held from its launch until its result so both can
// be reported on a single line.
type toolCall struct {
	name  string
	arg   string
	agent string
	// launch marks a sub-agent launch, which is reported as it happens whether
	// or not the stream named the agent: it runs for minutes, and announcing it
	// only once it finishes is the unexplained pause this reporting prevents.
	launch bool
	// parent names the sub-agent that made the call, empty when the stream did
	// not say. It is held on the call because a result can be reported long
	// after the launch that identified it.
	parent string
	// shown marks a call already rendered at launch, so the flush that reports
	// unanswered calls does not print it a second time.
	shown bool
}

// describeToolCall reads the distinguishing argument out of a tool's input.
func describeToolCall(name string, input json.RawMessage) toolCall {
	call := toolCall{name: name, launch: agentKeys[name] != ""}
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
		if key == agentKeys[name] {
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
	first = strings.TrimSpace(sanitize(first))
	if len(first) > toolArgWidth {
		return strings.TrimSpace(cutRunes(first, toolArgWidth)) + truncationMark
	}
	if multiline {
		return first + " " + truncationMark
	}
	return first
}

// sanitize drops the control characters that would escape a bounded line. Both
// the argument and the failure detail carry text rrev did not author - a
// filename, a command, a tool's stderr - where a carriage return undoes the
// line and an escape sequence repaints the terminal.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t':
			return ' '
		case r < 0x20 || r == 0x7f:
			return -1
		default:
			return r
		}
	}, s)
}

// cutRunes shortens s to at most n bytes without splitting a rune, so a
// truncated path or command never emits invalid UTF-8 into the terminal.
func cutRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
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
