package executor_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/korthane/rrev/pkg/executor"
)

func TestCustomRunTreatsStdoutAsFindings(t *testing.T) {
	findings := "Findings:\n\n1. pkg/foo.go:12 - missing validation\n<<<RREV:EXTERNAL_DONE>>>\n"
	tool := newFakeTool(t, fakeToolOpts{stdout: findings})

	custom := executor.Custom{Command: tool.path + " --format text"}
	result, err := custom.Run(t.Context(), executor.Request{Prompt: "review the branch", Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("run custom: %v", err)
	}

	if result.Output != findings {
		t.Errorf("output = %q, want the script's stdout verbatim", result.Output)
	}
	if result.Signal != executor.SignalExternalDone {
		t.Errorf("signal = %q, want %q", result.Signal, executor.SignalExternalDone)
	}
	if got := tool.args(t); len(got) != 2 || got[0] != "--format" || got[1] != "text" {
		t.Errorf("args = %v, want the configured arguments", got)
	}
	if got := tool.stdin(t); got != "review the branch" {
		t.Errorf("prompt on stdin = %q", got)
	}
}

func TestCustomRunWithoutCommand(t *testing.T) {
	_, err := (executor.Custom{Command: "   "}).Run(t.Context(), executor.Request{Prompt: "p"})
	if !errors.Is(err, executor.ErrNoCommand) {
		t.Errorf("error = %v, want ErrNoCommand", err)
	}
	if got := (executor.Custom{}).Bin(); got != "" {
		t.Errorf("Bin() = %q, want empty for an unconfigured script", got)
	}
}

func TestCustomRunScriptFails(t *testing.T) {
	tool := newFakeTool(t, fakeToolOpts{stdout: "partial findings\n", stderr: "review script crashed", exit: 3})

	result, err := (executor.Custom{Command: tool.path}).Run(t.Context(), executor.Request{Prompt: "p"})

	runErr, ok := errors.AsType[*executor.Error](err)
	if !ok {
		t.Fatalf("error = %v, want *executor.Error", err)
	}
	if runErr.ExitCode != 3 || !strings.Contains(runErr.Stderr, "review script crashed") {
		t.Errorf("error = %v, want exit 3 with the script's diagnostics", runErr)
	}
	if !strings.Contains(result.Output, "partial findings") {
		t.Errorf("captured findings lost on failure: %q", result.Output)
	}
}

func TestCustomBinIsTheScript(t *testing.T) {
	custom := executor.Custom{Command: "./scripts/review.sh --json"}
	if got := custom.Bin(); got != "./scripts/review.sh" {
		t.Errorf("Bin() = %q", got)
	}
	if custom.Name() != "custom" {
		t.Errorf("Name() = %q", custom.Name())
	}
}
