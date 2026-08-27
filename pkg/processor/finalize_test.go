package processor

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/korthane/rrev/pkg/config"
	"github.com/korthane/rrev/pkg/executor"
	"github.com/korthane/rrev/pkg/processor/phase"
)

func TestFinalizeDisabledByDefault(t *testing.T) {
	primary := mock("claude", reviewDone)
	external := mock("codex", externalDone)
	env, _ := newEnv(t, primary, external, nil)

	res := (&Runner{Env: env, Mode: ModeFull}).Run(context.Background())

	if res.Finalize == nil || !res.Finalize.Skipped {
		t.Fatalf("finalize = %+v, want it skipped", res.Finalize)
	}
	for _, call := range primary.Calls() {
		if call.Phase == phase.NameFinalize {
			t.Fatal("finalize ran without being enabled")
		}
	}
}

func TestFinalizeRunsOnceWhenEnabled(t *testing.T) {
	primary := mock("claude", reviewDone)
	external := mock("codex", externalDone)
	env, repo := newEnv(t, primary, external, func(c *config.Config) { c.Finalize = true })
	primary.Handler = func(_ context.Context, req executor.Request) (executor.Result, error) {
		if req.Phase == phase.NameComprehensive {
			repo.commit("head1")
		}
		return executor.Result{Output: reviewDone, Signal: executor.SignalReviewDone}, nil
	}

	res := (&Runner{Env: env, Mode: ModeFull}).Run(context.Background())

	if res.Finalize == nil || res.Finalize.Skipped {
		t.Fatalf("finalize = %+v, want it to run", res.Finalize)
	}
	if res.Finalize.Iterations != 1 {
		t.Errorf("finalize iterations = %d, want 1", res.Finalize.Iterations)
	}
	var finalizeCalls int
	for _, call := range primary.Calls() {
		if call.Phase != phase.NameFinalize {
			continue
		}
		finalizeCalls++
		if !strings.Contains(call.Prompt, "Finalize step for") {
			t.Error("the finalize call does not use the finalize prompt")
		}
	}
	if finalizeCalls != 1 {
		t.Errorf("finalize calls = %d, want exactly 1", finalizeCalls)
	}
}

func TestFinalizeSkippedOnNonConvergence(t *testing.T) {
	primary := mock("claude", "still fixing")
	external := mock("codex", externalDone)
	env, repo := newEnv(t, primary, external, func(c *config.Config) {
		c.Finalize = true
		c.MaxIterations = 2
		c.FinalMaxIterations = 1
	})
	primary.Handler = func(_ context.Context, _ executor.Request) (executor.Result, error) {
		repo.commit("head" + strings.Repeat("x", primary.CallCount()))
		return executor.Result{Output: "still fixing"}, nil
	}

	res := (&Runner{Env: env, Mode: ModeFull}).Run(context.Background())

	if res.Converged {
		t.Fatal("the run did not converge, so it must not be reported as converged")
	}
	if res.Finalize == nil || !res.Finalize.Skipped {
		t.Fatalf("finalize = %+v, want it skipped", res.Finalize)
	}
	if !strings.Contains(res.Finalize.SkipReason, "without converging") {
		t.Errorf("skip reason = %q, want it to name non-convergence", res.Finalize.SkipReason)
	}
}

func TestFinalizeFailureDoesNotChangeOutcome(t *testing.T) {
	boom := errors.New("rebase died")
	primary := mock("claude", reviewDone)
	external := mock("codex", externalDone)
	env, repo := newEnv(t, primary, external, func(c *config.Config) { c.Finalize = true })
	primary.Handler = func(_ context.Context, req executor.Request) (executor.Result, error) {
		if req.Phase == phase.NameFinalize {
			return executor.Result{Output: "could not rebase"}, boom
		}
		if req.Phase == phase.NameComprehensive {
			repo.commit("head1")
		}
		return executor.Result{Output: reviewDone, Signal: executor.SignalReviewDone}, nil
	}

	res := (&Runner{Env: env, Mode: ModeFull}).Run(context.Background())

	if !res.Converged {
		t.Error("a finalize failure must not turn a converged run into a failed one")
	}
	if res.Err != nil {
		t.Errorf("run err = %v, want none: finalize is best-effort", res.Err)
	}
	if res.Finalize == nil || !errors.Is(res.Finalize.Err, boom) {
		t.Fatalf("finalize = %+v, want it to carry the failure", res.Finalize)
	}
	if out, ok := env.Out.(interface{ String() string }); ok && !strings.Contains(out.String(), "outcome is unchanged") {
		t.Error("the finalize failure was not reported as leaving the outcome unchanged")
	}
}

func TestFinalizeNotReachedByFirstPhaseOnly(t *testing.T) {
	primary := mock("claude", reviewDone)
	env, _ := newEnv(t, primary, nil, func(c *config.Config) { c.Finalize = true })

	res := (&Runner{Env: env, Mode: ModePhase1Only}).Run(context.Background())

	if res.Finalize != nil {
		t.Fatalf("finalize = %+v, want it never reached by a first-phase-only run", res.Finalize)
	}
	if primary.CallCount() != 1 {
		t.Errorf("primary calls = %d, want the single comprehensive pass", primary.CallCount())
	}
}
