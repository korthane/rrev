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

// The flush reports only the calls the stream never answered. Re-reporting an
// answered one renders it from an entry already taken, producing a bare
// "tool:" line per call - the display flooding this reporting exists to avoid.
func TestFlushRendersNoEmptyToolLine(t *testing.T) {
	for name, stream := range map[string]string{"edges": runToolEdgeFixture(t), "tools": runToolFixture(t)} {
		for line := range strings.SplitSeq(stream, "\n") {
			if strings.HasSuffix(strings.TrimRight(line, " "), "tool:") {
				t.Errorf("%s fixture rendered a tool line with no call behind it:\n%s", name, stream)
			}
		}
	}
}

// subagent_type names a registered agent type, and rrev passes its reviewers
// as ad-hoc definitions, so it reads the same for all seven. Borrowing it would
// label every reviewer alike, which looks like attribution that worked.
func TestSubAgentTypeIsNotBorrowedAsAnAgentName(t *testing.T) {
	stream := runToolEdgeFixture(t)

	if !strings.Contains(stream, "· tool: Task typed-only") {
		t.Errorf("a launch naming only its type went unreported:\n%s", stream)
	}
	if strings.Contains(stream, "agent: typed-only") {
		t.Errorf("a subagent_type was used as an agent name:\n%s", stream)
	}
}

// The bound is what keeps a description from labelling every one of an agent's
// lines with a sentence; a long unbroken one passes the no-spaces test. It is
// still the argument that distinguishes the launch, just not an agent name.
func TestOverLongDescriptionIsNotUsedAsAnAgentName(t *testing.T) {
	stream := runToolEdgeFixture(t)

	if !strings.Contains(stream, "· tool: Task review-the-authentication-middleware-changes-in-detail") {
		t.Errorf("a launch with an over-long description went unreported:\n%s", stream)
	}
	if strings.Contains(stream, "agent: review-the-authentication") {
		t.Errorf("a description past the bound was used as an agent name:\n%s", stream)
	}
}

// The description is read ahead of subagent_type for the rendered argument too,
// not only for attribution: rrev's reviewers share one subagent_type, so
// reading it first renders seven concurrent launches as seven identical lines.
func TestProseDescriptionStillDistinguishesALaunch(t *testing.T) {
	stream := runToolEdgeFixture(t)

	if !strings.Contains(stream, "· tool: Task review the tests") {
		t.Errorf("a launch described in prose was not distinguished:\n%s", stream)
	}
	if strings.Contains(stream, "Task general-purpose") {
		t.Errorf("the shared subagent_type was rendered as the distinguishing argument:\n%s", stream)
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

// rrev's reviewers are passed to claude as ad-hoc definitions, not registered
// agent types, so a real run sends the same generic subagent_type for all seven
// and names the reviewer in the description the prompt asks for. Reading only
// subagent_type would label every concurrent reviewer alike, which is the
// pause-and-guess this attribution exists to end.
func TestAgentIsNamedFromTheDescriptionWhenTheTypeIsGeneric(t *testing.T) {
	stream := runToolFixture(t)

	if !strings.Contains(stream, "· agent: testing") {
		t.Errorf("the launch was not attributed to the reviewer it named:\n%s", stream)
	}
	if strings.Contains(stream, "agent: general-purpose") {
		t.Errorf("the generic agent type was used as the reviewer's name:\n%s", stream)
	}
}

// A result event need not repeat the attribution its launch carried, so the
// outcome is attributed from the call rrev held rather than left bare.
func TestToolOutcomeKeepsItsLaunchesAttributionWhenTheResultDropsIt(t *testing.T) {
	stream := runToolFixture(t)

	if !strings.Contains(stream, "· [testing] tool: Glob *_test.go → ok") {
		t.Errorf("an outcome lost the sub-agent its launch identified:\n%s", stream)
	}
}

// A tab is folded to a space rather than dropped: it separates words in a
// command, and removing it would run two arguments together.
func TestTabsInAToolArgumentBecomeSpaces(t *testing.T) {
	stream := runToolEdgeFixture(t)

	if !strings.Contains(stream, "· tool: Bash go test ./... → ok") {
		t.Errorf("a tabbed command did not render as spaced words:\n%s", stream)
	}
	if strings.Contains(stream, "\t") {
		t.Errorf("a tab reached the display:\n%q", stream)
	}
}
