package executor_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

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

// The error shapes here are the ones the executors actually return: a timeout
// is a *TimeoutError wrapped by the process failure, a cancellation is the
// process failure wrapping context.Canceled, and a refusal that exited zero is
// a *LimitError with nothing underneath.
func TestDescribeNamesTheClassification(t *testing.T) {
	base := &executor.Error{Tool: "claude", ExitCode: 1, Stderr: "rate limit exceeded"}
	timeout := &executor.TimeoutError{Tool: "claude", Limit: time.Minute, Idle: true}
	for _, tc := range []struct {
		err  error
		want string
	}{
		{&executor.LimitError{Tool: "claude", Reason: "rate limit exceeded", Err: base}, "claude: usage limit (exit 1)"},
		{&executor.LimitError{Tool: "claude", Reason: "overloaded", Retryable: true, Err: base}, "claude: transient failure (exit 1)"},
		{&executor.LimitError{Tool: "claude", Reason: "hit your usage limit"}, "claude: usage limit"},
		{fmt.Errorf("claude: %w", &executor.Error{Tool: "claude", ExitCode: -1, Err: timeout}), "claude: timeout"},
		{timeout, "claude: timeout"},
		{fmt.Errorf("claude: %w", &executor.Error{Tool: "claude", ExitCode: -1, Err: context.Canceled}), "claude: cancelled"},
		{fmt.Errorf("claude: %w", context.Canceled), "cancelled"},
	} {
		if got := executor.Describe(tc.err).Summary(); got != tc.want {
			t.Errorf("Describe(%v).Summary() = %q, want %q", tc.err, got, tc.want)
		}
	}
}

// A refusal that exited zero has no stderr and no exit status: the matched
// line is the whole diagnosis, and dropping it leaves "usage limit" alone.
func TestDescribeKeepsTheRefusalLineOfAnExitZeroRefusal(t *testing.T) {
	c := executor.Describe(&executor.LimitError{Tool: "claude", Reason: "You've hit your usage limit. Try again at 3pm."})

	if c.Detail() != "You've hit your usage limit. Try again at 3pm." {
		t.Errorf("detail = %q, want the refusal line", c.Detail())
	}
}

// The matched line may sit above the tail's bound; it must still be shown,
// and shown once when it is already in the tail.
func TestDescribeLeadsWithTheReasonUnlessTheTailHoldsIt(t *testing.T) {
	inTail := &executor.LimitError{Tool: "claude", Reason: "rate limit exceeded",
		Err: &executor.Error{Tool: "claude", ExitCode: 1, Stderr: "rate limit exceeded\nexiting"}}
	if got := executor.Describe(inTail).Detail(); got != "rate limit exceeded\nexiting" {
		t.Errorf("detail = %q, want the reason once", got)
	}

	var lines []string
	for i := range 40 {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	aboveBound := &executor.LimitError{Tool: "claude", Reason: "overloaded_error", Retryable: true,
		Err: &executor.Error{Tool: "claude", ExitCode: 1, Output: "overloaded_error\n" + strings.Join(lines, "\n")}}
	got := executor.Describe(aboveBound).Detail()
	if !strings.HasPrefix(got, "overloaded_error\n[earlier lines omitted]\n") || !strings.HasSuffix(got, "line 39") {
		t.Errorf("detail = %q, want the reason, the cut marker, then the tail", got)
	}
}

// A timeout wrapped by the process failure keeps what the tool said before the
// bound cut it, and which bound that was.
func TestDescribeTimeoutKeepsTheBoundAndTheTail(t *testing.T) {
	timeout := &executor.TimeoutError{Tool: "claude", Limit: time.Minute, Idle: true}
	err := &executor.Error{Tool: "claude", ExitCode: -1, Output: "Reviewing.\nRunning the suite", Err: timeout}

	c := executor.Describe(err)
	if want := "claude produced no output for 1m0s\nReviewing.\nRunning the suite"; c.Detail() != want {
		t.Errorf("detail = %q, want %q", c.Detail(), want)
	}
	if c.Summary() != "claude: timeout" {
		t.Errorf("summary = %q", c.Summary())
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

// A byte bound lands mid-line; the fragment before the first newline is not a
// line the tool wrote, and rendering it would put half a word in the log.
func TestStdoutTailStartsOnALineBoundary(t *testing.T) {
	var b strings.Builder
	for i := range 2000 {
		fmt.Fprintf(&b, "line %04d — still reviewing\n", i)
	}
	tool := newFakeTool(t, fakeToolOpts{stdout: b.String(), exit: 1})

	_, err := (executor.Claude{Command: tool.path}).Run(t.Context(), executor.Request{Prompt: "review", Dir: t.TempDir()})
	failure, ok := errors.AsType[*executor.Error](err)
	if !ok {
		t.Fatalf("err = %v, want *executor.Error", err)
	}
	first, _, _ := strings.Cut(failure.Output, "\n")
	if !strings.HasPrefix(first, "line ") || !strings.HasSuffix(first, "still reviewing") {
		t.Errorf("tail opens with a fragment %q, want a whole line", first)
	}
	if !strings.HasSuffix(failure.Output, "line 1999 — still reviewing") {
		t.Errorf("tail lost the last line: %q", failure.Output[max(0, len(failure.Output)-80):])
	}
}
