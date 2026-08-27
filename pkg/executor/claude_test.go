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

	claude := executor.Claude{Command: tool.path, ExtraArgs: []string{"--add-dir", "/tmp"}}
	if _, err := claude.Run(t.Context(), executor.Request{Prompt: "check the diff", Model: "claude-opus-5"}); err != nil {
		t.Fatalf("run claude: %v", err)
	}

	args := tool.args(t)
	for _, want := range [][2]string{{"--output-format", "stream-json"}, {"--model", "claude-opus-5"}, {"--add-dir", "/tmp"}} {
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
