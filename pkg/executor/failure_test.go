package executor_test

import (
	"errors"
	"strings"
	"testing"
	"time"

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

// A review that ends on its done signal reviewed something; limit wording it
// quotes from the code under review must not abort the run.
func TestReportedLimitWordingIsNotARefusal(t *testing.T) {
	report := strings.Join([]string{
		"FINDING: major | pkg/api/handler.go:12 | quality | - | returns Internal Server Error instead of 400 on a nil body",
		"REJECTED: pkg/api/retry.go:8 | quality | the quota exceeded branch is already covered",
		executor.SignalReviewDone.Marker(),
		"",
	}, "\n")
	tool := newFakeTool(t, fakeToolOpts{stdout: report})

	result, err := (executor.Custom{Command: tool.path}).Run(t.Context(), executor.Request{Prompt: "p"})
	if err != nil {
		t.Fatalf("a reported finding was mistaken for a provider refusal: %v", err)
	}
	if result.Signal != executor.SignalReviewDone {
		t.Errorf("signal = %q, want the review-done signal", result.Signal)
	}
}

// A finding reported without a done signal is still a review, not a refusal:
// an unconverged iteration is the common case.
func TestReportLineWithoutSignalIsNotARefusal(t *testing.T) {
	tool := newFakeTool(t, fakeToolOpts{
		stdout: "FINDING: minor | pkg/api/client.go:42 | quality | - | retries on rate limit exceeded forever\n",
	})

	if _, err := (executor.Custom{Command: tool.path}).Run(t.Context(), executor.Request{Prompt: "p"}); err != nil {
		t.Fatalf("a reported finding was mistaken for a provider refusal: %v", err)
	}
}

// The prompts ask for a paragraph of prose under every finding, and an
// unconverged iteration carries no signal. Wording the reviewer used to describe
// the code must not abort the run as a provider refusal.
func TestProseAboutAnErrorIsNotARefusal(t *testing.T) {
	report := strings.Join([]string{
		"FINDING: major | pkg/api/handler.go:12 | quality | - | the nil body is not rejected",
		"The handler returns an internal server error when the token is missing,",
		"and tells the caller to please try again, which hides the real cause.",
		"",
	}, "\n")
	tool := newFakeTool(t, fakeToolOpts{stdout: report})

	if _, err := (executor.Custom{Command: tool.path}).Run(t.Context(), executor.Request{Prompt: "p"}); err != nil {
		t.Fatalf("a reviewer's prose was mistaken for a provider refusal: %v", err)
	}
}

// An iteration that says nothing rrev recognises as a report is still a review
// when it says a lot: a refusal is a line or two, so long prose that happens to
// quote limit wording must not end the run.
func TestLongProseWithoutAReportIsNotARefusal(t *testing.T) {
	prose := strings.Join([]string{
		"I read the diff and the surrounding handlers.",
		"The retry helper now backs off once the provider answers 429 Too Many Requests,",
		"which is what the earlier revision got wrong.",
		"I applied the fix and committed it on the branch.",
		"Nothing else in the diff touches throttling.",
		"The tests pass.",
		"",
	}, "\n")
	tool := newFakeTool(t, fakeToolOpts{stdout: prose})

	if _, err := (executor.Custom{Command: tool.path}).Run(t.Context(), executor.Request{Prompt: "p"}); err != nil {
		t.Fatalf("a reviewer's prose was mistaken for a provider refusal: %v", err)
	}
}

// A bound rrev enforced itself must stay recognisable as a timeout: reported as
// a transient provider failure it would be retried, spending the budget again
// on a call that timed out for a reason retrying cannot fix.
func TestTimeoutIsNotReclassifiedAsAProviderFailure(t *testing.T) {
	tool := newScript(t, "echo the handler returns an internal server error\nsleep 30\n")
	custom := executor.Custom{Command: tool, Limits: executor.Limits{Session: 500 * time.Millisecond}}

	_, err := custom.Run(t.Context(), executor.Request{Prompt: "p"})

	if !errors.Is(err, executor.ErrTimeout) {
		t.Fatalf("error = %v, want it to stay a timeout", err)
	}
	if errors.Is(err, executor.ErrRetryable) || errors.Is(err, executor.ErrRateLimited) {
		t.Errorf("error = %v, want it distinguished from a provider failure", err)
	}
}

// The classification must not swallow what it was made from: the exit status
// and the tool's stderr are what a reader needs to tell one refusal apart from
// the next.
func TestLimitErrorKeepsTheUnderlyingFailure(t *testing.T) {
	tool := newFakeTool(t, fakeToolOpts{stderr: "ERROR: rate limit exceeded", exit: 7})

	_, err := (executor.Custom{Command: tool.path}).Run(t.Context(), executor.Request{Prompt: "p"})

	if !errors.Is(err, executor.ErrRateLimited) {
		t.Fatalf("error = %v, want a rate-limit error", err)
	}
	failure, ok := errors.AsType[*executor.Error](err)
	if !ok {
		t.Fatalf("error %v does not carry the invocation it was classified from", err)
	}
	if failure.ExitCode != 7 {
		t.Errorf("exit code = %d, want 7", failure.ExitCode)
	}
}
