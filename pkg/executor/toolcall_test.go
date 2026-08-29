package executor_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/korthane/rrev/pkg/executor"
)

func runToolEdgeFixture(t *testing.T) string {
	t.Helper()
	tool := newFakeTool(t, fakeToolOpts{fixture: "claude_tool_edges.jsonl"})
	var stream strings.Builder
	if _, err := (executor.Claude{Command: tool.path}).Run(t.Context(), executor.Request{
		Prompt: "review", Dir: t.TempDir(), Stream: &stream,
	}); err != nil {
		t.Fatalf("run claude: %v", err)
	}
	return stream.String()
}

// The width cap exists to stop a long argument breaking the display, so cutting
// one mid-rune would produce the garbled line it was meant to prevent.
func TestOverWidthToolArgumentIsCutOnARuneBoundary(t *testing.T) {
	stream := runToolEdgeFixture(t)

	if !utf8.ValidString(stream) {
		t.Errorf("a truncated argument emitted invalid UTF-8:\n%q", stream)
	}
	if !strings.Contains(stream, "· tool: Read pkgs/é") || !strings.Contains(stream, "…") {
		t.Errorf("stream missing the truncated read path:\n%s", stream)
	}
}

// A tool call the stream never gives an id for used to render nothing at all,
// leaving the user with the unexplained pause this reporting exists to prevent.
func TestToolCallWithoutAnIdIsStillReported(t *testing.T) {
	stream := runToolEdgeFixture(t)

	if !strings.Contains(stream, "· tool: Bash ls -la") {
		t.Errorf("a tool call carrying no id went unreported:\n%s", stream)
	}
}

// A sub-agent launch is rendered when it starts; the flush that reports calls
// the stream never answered must not print it a second time.
func TestUnansweredAgentLaunchIsReportedOnce(t *testing.T) {
	stream := runToolEdgeFixture(t)

	if n := strings.Count(stream, "agent: conformance"); n != 1 {
		t.Errorf("agent launch rendered %d times, want 1:\n%s", n, stream)
	}
}

// A launch that does not name its agent must not borrow the description for
// the name: every line of that agent would then be labelled with a sentence.
func TestUnnamedAgentLaunchIsReportedWithoutAnAgentName(t *testing.T) {
	stream := runToolEdgeFixture(t)

	if !strings.Contains(stream, "· tool: Task look for defects") {
		t.Errorf("an unnamed launch went unreported:\n%s", stream)
	}
	if strings.Contains(stream, "agent: look for defects") {
		t.Errorf("a description was used as an agent name:\n%s", stream)
	}
}

// A tool absent from the argument table renders with no argument rather than a
// guessed one, and its failure still reports as a failure.
func TestUnlistedToolRendersWithoutAnArgument(t *testing.T) {
	stream := runToolEdgeFixture(t)

	if !strings.Contains(stream, "· tool: Mystery → failed") {
		t.Errorf("stream missing the unlisted tool's outcome:\n%s", stream)
	}
	if strings.Contains(stream, "unlisted") {
		t.Errorf("an argument was guessed for a tool not in the table:\n%s", stream)
	}
}

// A result answering nothing rrev is holding is dropped: it cannot be paired,
// and printing its body would echo the tool output the display never carries.
func TestResultForAnUnknownCallIsDropped(t *testing.T) {
	stream := runToolEdgeFixture(t)

	if strings.Contains(stream, "orphan result body") {
		t.Errorf("an unmatched tool result reached the display:\n%s", stream)
	}
}

// Arguments and failure detail carry text rrev did not author. An escape
// sequence in one repaints the terminal and a carriage return unwinds the line
// the width cap exists to keep whole.
func TestControlCharactersAreStrippedFromToolArguments(t *testing.T) {
	stream := runToolEdgeFixture(t)

	if !strings.Contains(stream, "· tool: Bash echo [31mred[0mrewritten") {
		t.Errorf("stream missing the sanitized command:\n%s", stream)
	}
	if strings.ContainsAny(stream, "\x1b\r") {
		t.Errorf("a control character reached the display:\n%q", stream)
	}
}
