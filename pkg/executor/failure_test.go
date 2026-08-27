package executor_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/korthane/rrev/pkg/executor"
)

func TestRateLimitOnStderrOfFailingTool(t *testing.T) {
	tool := newFakeTool(t, fakeToolOpts{stderr: "ERROR: You've hit your usage limit. Resets at 4pm.", exit: 1})

	_, err := (executor.Codex{Command: tool.path}).Run(t.Context(), executor.Request{Prompt: "p"})

	if !errors.Is(err, executor.ErrRateLimited) {
		t.Fatalf("error = %v, want a rate-limit error", err)
	}
	if !strings.Contains(err.Error(), "codex") {
		t.Errorf("error %v does not name the tool", err)
	}
}

// A throttled call can exit zero with nothing but the refusal in its output;
// treating that as a review that found nothing would converge the phase on a
// call that never reviewed anything.
func TestRateLimitOnSuccessfulExit(t *testing.T) {
	tool := newFakeTool(t, fakeToolOpts{stdout: "Rate limit exceeded, try again in 10 minutes.\n"})

	result, err := (executor.Custom{Command: tool.path}).Run(t.Context(), executor.Request{Prompt: "p"})

	if !errors.Is(err, executor.ErrRateLimited) {
		t.Fatalf("error = %v, want a rate-limit error", err)
	}
	if !strings.Contains(result.Output, "Rate limit exceeded") {
		t.Errorf("output lost: %q", result.Output)
	}
}

func TestRetryableFailureReported(t *testing.T) {
	tool := newFakeTool(t, fakeToolOpts{stderr: "stream error: overloaded_error, please try again", exit: 1})

	_, err := (executor.Custom{Command: tool.path}).Run(t.Context(), executor.Request{Prompt: "p"})

	if !errors.Is(err, executor.ErrRetryable) {
		t.Fatalf("error = %v, want a retryable error", err)
	}
	if errors.Is(err, executor.ErrRateLimited) {
		t.Errorf("error = %v, want it distinguished from a usage limit", err)
	}
}

// A reviewer quoting rate-limit handling from the code under review is a
// finding, not a refusal.
func TestQuotedLimitTextIsNotAFailure(t *testing.T) {
	quoted := strings.Join([]string{
		"The retry helper mishandles throttling:",
		"```go",
		"// rate limit exceeded is retried forever",
		"```",
		"- pkg/api/client.go:42 logs \"usage limit reached\" and retries immediately",
		executor.SignalReviewDone.Marker(),
		"",
	}, "\n")
	tool := newFakeTool(t, fakeToolOpts{stdout: quoted})

	result, err := (executor.Custom{Command: tool.path}).Run(t.Context(), executor.Request{Prompt: "p"})
	if err != nil {
		t.Fatalf("a quoted marker was mistaken for a provider refusal: %v", err)
	}
	if result.Signal != executor.SignalReviewDone {
		t.Errorf("signal = %q, want the review-done signal", result.Signal)
	}
}
