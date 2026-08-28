package executor_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/korthane/rrev/pkg/executor"
)

// The mock must satisfy the same contract as a real tool, since phase tests
// drive the pipeline through it.
var _ executor.Executor = (*executor.Mock)(nil)

func TestMockReplaysResponsesInOrder(t *testing.T) {
	mock := &executor.Mock{Responses: []executor.Response{
		{Output: "fixed one finding"},
		{Output: "nothing left\n<<<RREV:REVIEW_DONE>>>"},
	}}

	first, err := mock.Run(t.Context(), executor.Request{Prompt: "iteration 1", Phase: "comprehensive"})
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	if first.Signal != executor.SignalNone {
		t.Errorf("first signal = %q, want none so the phase iterates again", first.Signal)
	}

	second, err := mock.Run(t.Context(), executor.Request{Prompt: "iteration 2"})
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if second.Signal != executor.SignalReviewDone {
		t.Errorf("second signal = %q, want %q", second.Signal, executor.SignalReviewDone)
	}

	// The last response repeats, so a test that only cares about convergence
	// does not have to script every call.
	third, err := mock.Run(t.Context(), executor.Request{Prompt: "iteration 3"})
	if err != nil || third.Signal != executor.SignalReviewDone {
		t.Errorf("third call = %+v, %v", third, err)
	}

	calls := mock.Calls()
	if len(calls) != 3 || mock.CallCount() != 3 {
		t.Fatalf("recorded %d calls, want 3", len(calls))
	}
	if calls[0].Prompt != "iteration 1" || calls[0].Phase != "comprehensive" {
		t.Errorf("first recorded call = %+v", calls[0])
	}
}

func TestMockStreamsItsOutput(t *testing.T) {
	var stream strings.Builder
	mock := &executor.Mock{Responses: []executor.Response{{Output: "reviewing"}}}

	if _, err := mock.Run(t.Context(), executor.Request{Stream: &stream}); err != nil {
		t.Fatalf("run mock: %v", err)
	}
	if !strings.Contains(stream.String(), "reviewing") {
		t.Errorf("stream = %q", stream.String())
	}
}

func TestMockHandlerOverridesResponses(t *testing.T) {
	want := errors.New("provider unavailable")
	mock := &executor.Mock{
		Tool: "codex",
		Handler: func(_ context.Context, req executor.Request) (executor.Result, error) {
			if req.Phase != "external" {
				return executor.Result{}, nil
			}
			return executor.Result{Output: "boom"}, want
		},
	}

	_, err := mock.Run(t.Context(), executor.Request{Phase: "external"})
	if !errors.Is(err, want) {
		t.Errorf("error = %v, want %v", err, want)
	}
	if mock.Name() != "codex" {
		t.Errorf("Name() = %q", mock.Name())
	}
	if mock.Bin() != "" {
		t.Errorf("Bin() = %q, want empty so preflight checks nothing", mock.Bin())
	}
}

func TestMockWithoutResponses(t *testing.T) {
	mock := &executor.Mock{}
	if _, err := mock.Run(t.Context(), executor.Request{}); err == nil {
		t.Error("unscripted mock call succeeded, want an error naming the call")
	}
}

func TestMockHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	mock := &executor.Mock{Responses: []executor.Response{{Output: "unused"}}}
	if _, err := mock.Run(ctx, executor.Request{}); !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want context.Canceled", err)
	}
	if mock.CallCount() != 0 {
		t.Errorf("cancelled call was recorded")
	}
}
