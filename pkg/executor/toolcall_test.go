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
	if !strings.Contains(stream, "· tool: Read pkg/é") || !strings.Contains(stream, "…") {
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
