package executor_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/korthane/rrev/pkg/executor"
)

func TestCodexRunReadsMsgEvents(t *testing.T) {
	tool := newFakeTool(t, fakeToolOpts{fixture: "codex_msg_stream.jsonl"})
	var stream strings.Builder

	result, err := (executor.Codex{Command: tool.path}).Run(t.Context(), executor.Request{
		Prompt: "review the branch",
		Stream: &stream,
	})
	if err != nil {
		t.Fatalf("run codex: %v", err)
	}

	if result.Signal != executor.SignalExternalDone {
		t.Errorf("signal = %q, want %q", result.Signal, executor.SignalExternalDone)
	}
	if !strings.Contains(result.Output, "pkg/foo.go:12 skips the validation") {
		t.Errorf("output missing the reported finding:\n%s", result.Output)
	}
	if strings.Contains(result.Output, "Checking each scenario") {
		t.Errorf("reasoning leaked into the collected output:\n%s", result.Output)
	}
	if !strings.Contains(stream.String(), "· thinking") || !strings.Contains(stream.String(), "· command") {
		t.Errorf("stream missing the activity indication:\n%s", stream.String())
	}
}

func TestCodexRunReadsItemEvents(t *testing.T) {
	tool := newFakeTool(t, fakeToolOpts{fixture: "codex_item_stream.jsonl"})

	result, err := (executor.Codex{Command: tool.path}).Run(t.Context(), executor.Request{Prompt: "review"})
	if err != nil {
		t.Fatalf("run codex: %v", err)
	}

	if result.Signal != executor.SignalExternalDone {
		t.Errorf("signal = %q, want %q", result.Signal, executor.SignalExternalDone)
	}
	if !strings.Contains(result.Output, "No issues found.") {
		t.Errorf("output missing the agent message:\n%s", result.Output)
	}
	if strings.Contains(result.Output, "partial") {
		t.Errorf("an unfinished item was collected:\n%s", result.Output)
	}
}

func TestCodexRunInvocation(t *testing.T) {
	tool := newFakeTool(t, fakeToolOpts{stdout: "{}"})

	codex := executor.Codex{Command: tool.path}
	if _, err := codex.Run(t.Context(), executor.Request{Prompt: "check the diff", Model: "gpt-5", Effort: "high"}); err != nil {
		t.Fatalf("run codex: %v", err)
	}

	args := tool.args(t)
	if len(args) == 0 || args[0] != "exec" {
		t.Fatalf("args %v do not start with exec", args)
	}
	if args[len(args)-1] != "-" {
		t.Errorf("args %v do not end with the stdin marker", args)
	}
	for _, want := range [][2]string{
		{"--model", "gpt-5"},
		{"-c", "model_reasoning_effort=high"},
	} {
		if !hasArg(args, want[0], want[1]) {
			t.Errorf("args %v missing %s %s", args, want[0], want[1])
		}
	}
	if slices.Contains(args, "  ") {
		t.Errorf("args %v pass a blank config override", args)
	}
	if got := tool.stdin(t); got != "check the diff" {
		t.Errorf("prompt on stdin = %q", got)
	}
}

func TestCodexRunPlainTextOutput(t *testing.T) {
	tool := newFakeTool(t, fakeToolOpts{stdout: "codex reported no issues\n<<<RREV:EXTERNAL_DONE>>>\n"})

	result, err := (executor.Codex{Command: tool.path}).Run(t.Context(), executor.Request{Prompt: "p"})
	if err != nil {
		t.Fatalf("run codex: %v", err)
	}
	if result.Signal != executor.SignalExternalDone {
		t.Errorf("signal = %q, want %q", result.Signal, executor.SignalExternalDone)
	}
	if !strings.Contains(result.Output, "codex reported no issues") {
		t.Errorf("plain-text output dropped:\n%s", result.Output)
	}
}

func TestCodexRunNonZeroExit(t *testing.T) {
	tool := newFakeTool(t, fakeToolOpts{stderr: "stream error: 429 rate limited", exit: 1})

	_, err := (executor.Codex{Command: tool.path}).Run(t.Context(), executor.Request{Prompt: "p"})

	runErr, ok := errors.AsType[*executor.Error](err)
	if !ok {
		t.Fatalf("error = %v, want *executor.Error", err)
	}
	if runErr.Tool != "codex" || !strings.Contains(runErr.Stderr, "429 rate limited") {
		t.Errorf("error does not carry the tool and its diagnostics: %v", runErr)
	}
}

func TestCodexDefaults(t *testing.T) {
	var codex executor.Codex
	if codex.Bin() != executor.DefaultCodexBin {
		t.Errorf("Bin() = %q, want %q", codex.Bin(), executor.DefaultCodexBin)
	}
	if codex.Name() != "codex" {
		t.Errorf("Name() = %q", codex.Name())
	}
}

func TestCodexRunPassesEffortAsConfigOverride(t *testing.T) {
	tool := newFakeTool(t, fakeToolOpts{stdout: "{}"})

	codex := executor.Codex{Command: tool.path}
	if _, err := codex.Run(t.Context(), executor.Request{Prompt: "p", Effort: "high"}); err != nil {
		t.Fatalf("run codex: %v", err)
	}

	args := tool.args(t)
	if !slices.Contains(args, "model_reasoning_effort=high") {
		t.Errorf("args %v missing the reasoning effort override", args)
	}
}

func TestCodexRunDropsUnsupportedEffort(t *testing.T) {
	tool := newFakeTool(t, fakeToolOpts{stdout: "{}"})
	var stream strings.Builder

	codex := executor.Codex{Command: tool.path}
	if _, err := codex.Run(t.Context(), executor.Request{Prompt: "p", Effort: "max", Stream: &stream}); err != nil {
		t.Fatalf("run codex: %v", err)
	}

	if slices.ContainsFunc(tool.args(t), func(a string) bool { return strings.HasPrefix(a, "model_reasoning_effort=") }) {
		t.Errorf("args %v pass an effort codex does not accept", tool.args(t))
	}
	if !strings.Contains(stream.String(), "max") || !strings.Contains(stream.String(), "codex") {
		t.Errorf("stream does not warn about the dropped effort:\n%s", stream.String())
	}
}
