package executor_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/korthane/rrev/pkg/executor"
)

// claude reports its own error on stdout and exits silently on stderr. Without
// the stdout tail such a failure is an exit status and nothing else.
func TestFailingToolSilentOnStderrKeepsItsStdoutTail(t *testing.T) {
	tool := newFakeTool(t, fakeToolOpts{stdout: "Reviewing.\nError: context window exceeded\n", exit: 1})

	_, err := (executor.Claude{Command: tool.path}).Run(t.Context(), executor.Request{Prompt: "review", Dir: t.TempDir()})
	failure, ok := errors.AsType[*executor.Error](err)
	if !ok {
		t.Fatalf("err = %v, want *executor.Error", err)
	}
	if failure.Stderr != "" {
		t.Errorf("stderr = %q, want empty", failure.Stderr)
	}
	if !strings.Contains(failure.Output, "context window exceeded") {
		t.Errorf("output tail = %q, want the tool's own error line", failure.Output)
	}
	if failure.ExitCode != 1 {
		t.Errorf("exit = %d, want 1", failure.ExitCode)
	}
}

func TestDescribePrefersStderrOverStdout(t *testing.T) {
	err := &executor.Error{Tool: "claude", ExitCode: 2, Stderr: "fatal: no api key", Output: "some review prose"}

	c := executor.Describe(err)
	if c.Summary() != "claude: failure (exit 2)" {
		t.Errorf("summary = %q", c.Summary())
	}
	if c.Detail() != "fatal: no api key" {
		t.Errorf("detail = %q, want the stderr tail alone", c.Detail())
	}
}

func TestDescribeFallsBackToStdoutWhenStderrIsEmpty(t *testing.T) {
	err := &executor.Error{Tool: "claude", ExitCode: 1, Output: "line one\n\nError: context window exceeded"}

	c := executor.Describe(err)
	if want := "line one\nError: context window exceeded"; c.Detail() != want {
		t.Errorf("detail = %q, want %q", c.Detail(), want)
	}
}

func TestDescribeNamesTheClassification(t *testing.T) {
	base := &executor.Error{Tool: "claude", ExitCode: 1, Stderr: "rate limit exceeded"}
	for _, tc := range []struct {
		err  error
		want string
	}{
		{&executor.LimitError{Tool: "claude", Reason: "rate limit exceeded", Err: base}, "claude: usage limit (exit 1)"},
		{&executor.LimitError{Tool: "claude", Reason: "overloaded", Retryable: true, Err: base}, "claude: transient failure (exit 1)"},
		{fmt.Errorf("claude: %w", executor.ErrTimeout), "timeout"},
		{fmt.Errorf("claude: %w", context.Canceled), "cancelled"},
	} {
		if got := executor.Describe(tc.err).Summary(); got != tc.want {
			t.Errorf("Describe(%v).Summary() = %q, want %q", tc.err, got, tc.want)
		}
	}
}

func TestDescribeBoundsTheTailAndSaysSo(t *testing.T) {
	var lines []string
	for i := range 60 {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	err := &executor.Error{Tool: "codex", ExitCode: 1, Stderr: strings.Join(lines, "\n")}

	c := executor.Describe(err)
	if !c.Truncated || !strings.HasPrefix(c.Detail(), "[earlier lines omitted]") {
		t.Errorf("a cut tail must say it was cut: %q", c.Detail())
	}
	if !strings.HasSuffix(c.Detail(), "line 59") || strings.Contains(c.Detail(), "line 0\n") {
		t.Errorf("the tail must keep the last lines, not the first: %q", c.Detail())
	}
}
