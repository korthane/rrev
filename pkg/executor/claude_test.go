package executor_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/korthane/rrev/pkg/executor"
)

func TestClaudeRunReadsStreamJSON(t *testing.T) {
	tool := newFakeTool(t, fakeToolOpts{fixture: "claude_stream.jsonl"})
	var stream strings.Builder

	result, err := (executor.Claude{Command: tool.path}).Run(t.Context(), executor.Request{
		Prompt: "review the branch",
		Dir:    t.TempDir(),
		Model:  "claude-opus-5",
		Stream: &stream,
	})
	if err != nil {
		t.Fatalf("run claude: %v", err)
	}

	if result.Signal != executor.SignalReviewDone {
		t.Errorf("signal = %q, want %q", result.Signal, executor.SignalReviewDone)
	}
	if !strings.Contains(result.Output, "Reviewing the diff against the requirement checklist.") {
		t.Errorf("output missing the assistant text:\n%s", result.Output)
	}
	// The result event repeats the last assistant message; collecting both
	// would show the user the same paragraph twice.
	if n := strings.Count(result.Output, "No confirmed findings remain."); n != 1 {
		t.Errorf("closing message appears %d times, want once:\n%s", n, result.Output)
	}
	// Tool use is progress, not something the model said: a signal must never
	// come from a file the model happened to write.
	if strings.Contains(result.Output, "Bash") {
		t.Errorf("tool activity leaked into the output:\n%s", result.Output)
	}
	if !strings.Contains(stream.String(), "· tool: Bash") {
		t.Errorf("stream missing the tool activity:\n%s", stream.String())
	}
	// The fixture carries an unknown event type and a plain-text line; neither
	// may abort the run.
	if !strings.Contains(result.Output, "warning: settings file not found") {
		t.Errorf("plain-text line dropped:\n%s", result.Output)
	}
}

func TestClaudeRunInvocation(t *testing.T) {
	tool := newFakeTool(t, fakeToolOpts{stdout: `{"type":"result","subtype":"success","result":"ok"}`})

	claude := executor.Claude{Command: tool.path}
	if _, err := claude.Run(t.Context(), executor.Request{Prompt: "check the diff", Model: "claude-opus-5"}); err != nil {
		t.Fatalf("run claude: %v", err)
	}

	args := tool.args(t)
	for _, want := range [][2]string{{"--output-format", "stream-json"}, {"--model", "claude-opus-5"}} {
		if !hasArg(args, want[0], want[1]) {
			t.Errorf("args %v missing %s %s", args, want[0], want[1])
		}
	}
	if got := tool.stdin(t); got != "check the diff" {
		t.Errorf("prompt on stdin = %q", got)
	}
}

func TestClaudeRunOmitsModelWhenUnset(t *testing.T) {
	tool := newFakeTool(t, fakeToolOpts{stdout: "{}"})
	if _, err := (executor.Claude{Command: tool.path}).Run(t.Context(), executor.Request{Prompt: "p"}); err != nil {
		t.Fatalf("run claude: %v", err)
	}
	for _, arg := range tool.args(t) {
		if arg == "--model" {
			t.Fatalf("args %v pass --model with no model configured", tool.args(t))
		}
	}
}

func TestClaudeRunNonZeroExit(t *testing.T) {
	tool := newFakeTool(t, fakeToolOpts{
		stdout: `{"type":"assistant","message":{"content":[{"type":"text","text":"partial work"}]}}` + "\n",
		stderr: "credit balance too low",
		exit:   2,
	})

	result, err := (executor.Claude{Command: tool.path}).Run(t.Context(), executor.Request{Prompt: "p"})

	runErr, ok := errors.AsType[*executor.Error](err)
	if !ok {
		t.Fatalf("error = %v, want *executor.Error", err)
	}
	if runErr.ExitCode != 2 {
		t.Errorf("exit code = %d, want 2", runErr.ExitCode)
	}
	if !strings.Contains(runErr.Stderr, "credit balance too low") {
		t.Errorf("error drops the tool's diagnostics: %v", runErr)
	}
	if !strings.Contains(runErr.Error(), "claude") {
		t.Errorf("error does not name the tool: %v", runErr)
	}
	// A failed call still reports what it captured, so the phase can log it.
	if !strings.Contains(result.Output, "partial work") {
		t.Errorf("captured output lost on failure: %q", result.Output)
	}
}

func TestClaudeRunResultReportsError(t *testing.T) {
	tool := newFakeTool(t, fakeToolOpts{
		stdout: `{"type":"result","subtype":"error_during_execution","is_error":true,"result":"tool execution failed"}` + "\n",
	})

	_, err := (executor.Claude{Command: tool.path}).Run(t.Context(), executor.Request{Prompt: "p"})

	runErr, ok := errors.AsType[*executor.Error](err)
	if !ok {
		t.Fatalf("error = %v, want *executor.Error", err)
	}
	if !strings.Contains(runErr.Stderr, "tool execution failed") {
		t.Errorf("error drops the reported reason: %v", runErr)
	}
}

func TestClaudeRunMissingBinary(t *testing.T) {
	_, err := (executor.Claude{Command: "rrev-no-such-tool"}).Run(t.Context(), executor.Request{Prompt: "p"})
	if err == nil {
		t.Fatal("running a missing binary succeeded")
	}
	if _, ok := errors.AsType[*executor.Error](err); !ok {
		t.Errorf("error = %v, want *executor.Error", err)
	}
}

func TestClaudeDefaults(t *testing.T) {
	var claude executor.Claude
	if claude.Bin() != executor.DefaultClaudeBin {
		t.Errorf("Bin() = %q, want %q", claude.Bin(), executor.DefaultClaudeBin)
	}
	if claude.Name() != "claude" {
		t.Errorf("Name() = %q", claude.Name())
	}
}

func TestClaudeDebugRecordsCommandAndPrompt(t *testing.T) {
	tool := newFakeTool(t, fakeToolOpts{stdout: "{}"})
	var stream strings.Builder

	claude := executor.Claude{Command: tool.path, Debug: true}
	if _, err := claude.Run(t.Context(), executor.Request{Prompt: "the whole prompt", Stream: &stream}); err != nil {
		t.Fatalf("run claude: %v", err)
	}

	if !strings.Contains(stream.String(), tool.path) || !strings.Contains(stream.String(), "the whole prompt") {
		t.Errorf("debug output missing the command line or the prompt:\n%s", stream.String())
	}
}

func TestClaudeRunCancelledContext(t *testing.T) {
	tool := newFakeTool(t, fakeToolOpts{stdout: "{}"})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := (executor.Claude{Command: tool.path}).Run(ctx, executor.Request{Prompt: "p"}); err == nil {
		t.Fatal("run with a cancelled context succeeded")
	}
}

func TestClaudeRunPassesEffort(t *testing.T) {
	tool := newFakeTool(t, fakeToolOpts{stdout: `{"type":"result","subtype":"success","result":"ok"}`})

	claude := executor.Claude{Command: tool.path}
	req := executor.Request{Prompt: "p", Model: "claude-opus-5", Effort: "xhigh"}
	if _, err := claude.Run(t.Context(), req); err != nil {
		t.Fatalf("run claude: %v", err)
	}

	if !hasArg(tool.args(t), "--effort", "xhigh") {
		t.Errorf("args %v missing --effort xhigh", tool.args(t))
	}
}

func TestClaudeRunDropsUnsupportedEffort(t *testing.T) {
	tool := newFakeTool(t, fakeToolOpts{stdout: `{"type":"result","subtype":"success","result":"ok"}`})
	var stream strings.Builder

	claude := executor.Claude{Command: tool.path}
	req := executor.Request{Prompt: "p", Effort: "turbo", Stream: &stream}
	if _, err := claude.Run(t.Context(), req); err != nil {
		t.Fatalf("run claude: %v", err)
	}

	if slices.Contains(tool.args(t), "--effort") {
		t.Errorf("args %v pass an effort claude does not accept", tool.args(t))
	}
	if !strings.Contains(stream.String(), "turbo") || !strings.Contains(stream.String(), "claude") {
		t.Errorf("stream does not warn about the dropped effort:\n%s", stream.String())
	}
}

// A phase runs its reviewers concurrently, and a bare tool name renders them
// identically. The launch has to name the agent it started.
func TestClaudeNamesTheAgentEachTaskLaunches(t *testing.T) {
	stream := runToolFixture(t)

	for _, want := range []string{"· agent: conformance", "· agent: quality"} {
		if !strings.Contains(stream, want) {
			t.Errorf("stream missing %q\n%s", want, stream)
		}
	}
}

// Where the stream says which sub-agent produced a line, that attribution is
// what tells one concurrent reviewer from another.
func TestClaudeAttributesSubAgentText(t *testing.T) {
	stream := runToolFixture(t)

	for _, want := range []string{"[conformance] Scenario 3 is not addressed.", "[quality] Found a nil dereference."} {
		if !strings.Contains(stream, want) {
			t.Errorf("stream missing %q\n%s", want, stream)
		}
	}
}

// A reviewer writes paragraphs, not lines. Attributing only the block's first
// line leaves the rest indistinguishable from the six reviewers beside it.
func TestEveryLineOfASubAgentBlockIsAttributed(t *testing.T) {
	stream := runToolFixture(t)

	if !strings.Contains(stream, "[quality] It is reachable from the parser.") {
		t.Errorf("a later line of a sub-agent's block lost its attribution:\n%s", stream)
	}
}

// The attribution is for the display only: report lines and signals are parsed
// out of the collected text, and a prefix in there would corrupt both.
func TestSubAgentAttributionStaysOutOfTheCollectedOutput(t *testing.T) {
	result := runToolFixtureResult(t)

	if strings.Contains(result.Output, "[conformance]") {
		t.Errorf("display attribution leaked into the parsed output:\n%s", result.Output)
	}
	if !strings.Contains(result.Output, "Scenario 3 is not addressed.") {
		t.Errorf("the sub-agent's text must still be collected:\n%s", result.Output)
	}
}

// A tool call renders the argument that distinguishes it, bounded to one line:
// heredocs and multi-line commands arrive here routinely.
func TestClaudeRendersBoundedToolArguments(t *testing.T) {
	stream := runToolFixture(t)

	if !strings.Contains(stream, "· tool: Bash go test ./... …") {
		t.Errorf("stream missing the bounded command\n%s", stream)
	}
	if strings.Contains(stream, "must never reach the display") {
		t.Errorf("a command's later lines reached the display\n%s", stream)
	}
	if !strings.Contains(stream, "· tool: Read pkg/config/resolve.go") {
		t.Errorf("stream missing the read path\n%s", stream)
	}
}

// The outcome is what a reader needs; the output would flood the display.
func TestClaudeRendersOutcomeWithoutToolOutput(t *testing.T) {
	stream := runToolFixture(t)

	if !strings.Contains(stream, "go test ./... … → ok") {
		t.Errorf("stream missing the success outcome\n%s", stream)
	}
	if !strings.Contains(stream, "→ failed: no such file or directory") {
		t.Errorf("stream missing the failure detail\n%s", stream)
	}
	if strings.Contains(stream, "plenty more output") {
		t.Errorf("tool output was echoed to the display\n%s", stream)
	}
}

// Debug is where the caps come off, and the only place they do.
func TestFullToolArgumentsAndOutputAppearOnlyUnderDebug(t *testing.T) {
	plain := runToolFixture(t)
	if strings.Contains(plain, "plenty more output") || strings.Contains(plain, "must never reach the display") {
		t.Fatalf("full detail leaked without debug\n%s", plain)
	}

	tool := newFakeTool(t, fakeToolOpts{fixture: "claude_tools.jsonl"})
	var stream strings.Builder
	_, err := (executor.Claude{Command: tool.path, Debug: true}).Run(t.Context(), executor.Request{
		Prompt: "review", Dir: t.TempDir(), Stream: &stream,
	})
	if err != nil {
		t.Fatalf("run claude: %v", err)
	}
	for _, want := range []string{"must never reach the display", "plenty more output"} {
		if !strings.Contains(stream.String(), want) {
			t.Errorf("debug output missing %q\n%s", want, stream.String())
		}
	}
}

func runToolFixture(t *testing.T) string {
	t.Helper()
	tool := newFakeTool(t, fakeToolOpts{fixture: "claude_tools.jsonl"})
	var stream strings.Builder
	if _, err := (executor.Claude{Command: tool.path}).Run(t.Context(), executor.Request{
		Prompt: "review", Dir: t.TempDir(), Stream: &stream,
	}); err != nil {
		t.Fatalf("run claude: %v", err)
	}
	return stream.String()
}

func runToolFixtureResult(t *testing.T) executor.Result {
	t.Helper()
	tool := newFakeTool(t, fakeToolOpts{fixture: "claude_tools.jsonl"})
	var stream strings.Builder
	result, err := (executor.Claude{Command: tool.path}).Run(t.Context(), executor.Request{
		Prompt: "review", Dir: t.TempDir(), Stream: &stream,
	})
	if err != nil {
		t.Fatalf("run claude: %v", err)
	}
	return result
}

// Nearly every tool call in a review happens inside one of the concurrent
// reviewers, so the outcome line needs the same attribution its text gets.
func TestClaudeAttributesToolOutcomesToTheSubAgent(t *testing.T) {
	stream := runToolFixture(t)

	if !strings.Contains(stream, "· [conformance] tool: Grep func Open → failed: no matches under pkg") {
		t.Errorf("stream missing the attributed tool outcome:\n%s", stream)
	}
	// The stream says nothing about who ran this one, so it stays with the phase.
	if !strings.Contains(stream, "· tool: Read pkg/config/resolve.go → failed") {
		t.Errorf("an unattributed call must still render:\n%s", stream)
	}
}

// A killed or truncated stream ends with no result event, so the in-stream
// flush never runs. The flush after the command is what stops a call held for
// its outcome from vanishing with it, leaving the unexplained pause this
// reporting exists to prevent.
func TestClaudeFlushesHeldCallsWhenTheStreamEndsWithoutAResult(t *testing.T) {
	tool := newFakeTool(t, fakeToolOpts{fixture: "claude_no_result.jsonl"})
	var stream strings.Builder
	if _, err := (executor.Claude{Command: tool.path}).Run(t.Context(), executor.Request{
		Prompt: "review", Dir: t.TempDir(), Stream: &stream,
	}); err != nil {
		t.Fatalf("run claude: %v", err)
	}

	if !strings.Contains(stream.String(), "· tool: Bash go build ./...") {
		t.Errorf("a call held for its outcome was lost when the stream ended:\n%s", stream.String())
	}
}

// A sub-agent launch is announced when it starts and again when it ends, and
// the ending is the only line that says whether the reviewer actually
// succeeded. A failed launch's result is that agent's whole report, so it is
// bounded like any other failure detail rather than echoed.
func TestClaudeReportsSubAgentLaunchCompletion(t *testing.T) {
	stream := runToolFixture(t)

	if !strings.Contains(stream, "· agent: testing → ok") {
		t.Errorf("a completed sub-agent launch renders no outcome:\n%s", stream)
	}
	if !strings.Contains(stream, "· agent: conformance → failed: agent failed: budget exhausted") {
		t.Errorf("a failed sub-agent launch renders no cause:\n%s", stream)
	}
	for _, unwanted := range []string{"which must not be echoed", "must stay off the display"} {
		if strings.Contains(stream, unwanted) {
			t.Errorf("a sub-agent's report body reached the display (%q):\n%s", unwanted, stream)
		}
	}
}

// A result arrives either as a string or as content blocks. Reading only the
// first block would drop detail from the failure line and from the debug record
// that promises the tool's full output.
func TestClaudeReadsEveryBlockOfAToolResult(t *testing.T) {
	if plain := runToolFixture(t); strings.Contains(plain, "a second block") {
		t.Fatalf("full detail leaked without debug\n%s", plain)
	}

	tool := newFakeTool(t, fakeToolOpts{fixture: "claude_tools.jsonl"})
	var stream strings.Builder
	if _, err := (executor.Claude{Command: tool.path, Debug: true}).Run(t.Context(), executor.Request{
		Prompt: "review", Dir: t.TempDir(), Stream: &stream,
	}); err != nil {
		t.Fatalf("run claude: %v", err)
	}
	if !strings.Contains(stream.String(), "a second block the debug record must keep") {
		t.Errorf("debug output kept only the first content block:\n%s", stream.String())
	}
}
