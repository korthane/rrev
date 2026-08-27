package phase

import (
	"context"
	"strings"
	"testing"

	"github.com/korthane/rrev/pkg/config"
	"github.com/korthane/rrev/pkg/executor"
)

func TestExternalSkippedWhenDisabled(t *testing.T) {
	primary := mock("claude", reviewDone)
	env, _ := newEnv(t, primary, nil, nil)

	res := External(context.Background(), env)

	if !res.Skipped || res.Reason != ReasonSkipped {
		t.Fatalf("result = %+v, want a skipped phase", res)
	}
	if !strings.Contains(res.SkipReason, "disabled") {
		t.Errorf("skip reason = %q, want it to say external review is disabled", res.SkipReason)
	}
	if !res.OK() {
		t.Error("a skipped phase must not count as non-convergence")
	}
	if primary.CallCount() != 0 {
		t.Errorf("executor calls = %d, want 0", primary.CallCount())
	}
}

func TestExternalSkippedForSameModel(t *testing.T) {
	primary, external := mock("codex", reviewDone), mock("codex", externalDone)
	env, _ := newEnv(t, primary, external, func(c *config.Config) {
		c.Executor = config.ExecutorCodex
		c.ExternalReviewTool = config.ExternalToolCodex
	})

	res := External(context.Background(), env)

	if !res.Skipped {
		t.Fatalf("result = %+v, want a skipped phase", res)
	}
	if !strings.Contains(res.SkipReason, "codex") || !strings.Contains(res.SkipReason, "own work") {
		t.Errorf("skip reason = %q, want it to name the shared tool and why it is skipped", res.SkipReason)
	}
	if external.CallCount() != 0 || primary.CallCount() != 0 {
		t.Error("a skipped phase must invoke no executor")
	}
}

func TestExternalRunsWhenModelsDiffer(t *testing.T) {
	primary, external := mock("claude", externalDone), mock("codex", externalDone)
	env, _ := newEnv(t, primary, external, nil)

	res := External(context.Background(), env)

	if res.Skipped {
		t.Fatalf("result = %+v, want the phase to run", res)
	}
	if res.Reason != ReasonConverged {
		t.Errorf("reason = %q, want %q", res.Reason, ReasonConverged)
	}
}

func TestExternalSameToolDifferentModelRuns(t *testing.T) {
	primary, external := mock("codex", "FINDING: minor | a.go:1 | external | - | naming"), mock("codex", externalDone)
	env, _ := newEnv(t, primary, external, func(c *config.Config) {
		c.Executor = config.ExecutorCodex
		c.ReviewModel = "gpt-5-codex:low"
		c.ExternalModel = "gpt-5-codex:high"
	})

	res := External(context.Background(), env)

	if res.Skipped {
		t.Fatalf("result = %+v, want the phase to run: the two sides review at different efforts", res)
	}
	if external.CallCount() == 0 {
		t.Error("the external tool was never invoked")
	}
}

func TestExternalConvergesWhenToolReportsNothing(t *testing.T) {
	primary, external := mock("claude", externalDone), mock("codex", "reviewed the branch\n"+externalDone)
	env, _ := newEnv(t, primary, external, nil)

	res := External(context.Background(), env)

	if res.Reason != ReasonConverged {
		t.Fatalf("reason = %q, want %q", res.Reason, ReasonConverged)
	}
	if res.Iterations != 1 {
		t.Errorf("iterations = %d, want 1", res.Iterations)
	}
	if primary.CallCount() != 0 {
		t.Errorf("primary calls = %d, want 0: there is nothing to evaluate", primary.CallCount())
	}
}

func TestExternalAlternatesToolAndExecutor(t *testing.T) {
	external := mock("codex",
		"FINDING: major | pkg/a.go:10 | external | Run modes | the mode flags are not mutually exclusive",
		externalDone)
	primary := mock("claude",
		"FINDING: major | pkg/a.go:10 | external | Run modes | fixed the missing exclusion\n"+
			"REJECTED: pkg/b.go:3 | external | the tool misread the loop bound")
	env, repo := newEnv(t, primary, external, nil)
	primary.Handler = changingHandler(primary, repo)

	res := External(context.Background(), env)

	if res.Reason != ReasonConverged {
		t.Fatalf("reason = %q, want %q", res.Reason, ReasonConverged)
	}
	if res.Iterations != 2 {
		t.Errorf("iterations = %d, want 2", res.Iterations)
	}
	if external.CallCount() != 2 || primary.CallCount() != 1 {
		t.Errorf("external calls = %d, primary calls = %d, want 2 and 1", external.CallCount(), primary.CallCount())
	}
	if len(res.Findings) != 1 || res.Findings[0].Summary != "fixed the missing exclusion" {
		t.Errorf("findings = %+v, want only what the executor confirmed", res.Findings)
	}
	if !res.Changed {
		t.Error("an iteration that committed a fix must be reported as changing the branch")
	}

	evalPrompt := primary.Calls()[0].Prompt
	if !strings.Contains(evalPrompt, "the mode flags are not mutually exclusive") {
		t.Error("the evaluation prompt does not carry the external tool's report")
	}
}

func TestExternalCarriesPriorRoundsForward(t *testing.T) {
	external := mock("codex",
		"FINDING: major | pkg/a.go:10 | external | - | the loop bound is off by one",
		externalDone)
	primary := mock("claude", "REJECTED: pkg/a.go:10 | external | the bound is inclusive by design, see design.md")
	env, repo := newEnv(t, primary, external, nil)
	primary.Handler = changingHandler(primary, repo)

	External(context.Background(), env)

	calls := external.Calls()
	if len(calls) != 2 {
		t.Fatalf("external calls = %d, want 2", len(calls))
	}
	if strings.Contains(calls[0].Prompt, "Round 1") {
		t.Error("the first round must not claim prior rounds exist")
	}
	second := calls[1].Prompt
	for _, want := range []string{
		"Round 1",
		"the loop bound is off by one",
		"the bound is inclusive by design",
	} {
		if !strings.Contains(second, want) {
			t.Errorf("the second round's prompt is missing %q", want)
		}
	}
}

func TestExternalUserBreakEndsLoop(t *testing.T) {
	brk := make(chan struct{})
	external := mock("codex", "FINDING: minor | a.go:1 | external | - | naming")
	primary := mock("claude", "evaluated")
	env, repo := newEnv(t, primary, external, func(c *config.Config) { c.ExternalMaxIterations = 5 })
	primary.Handler = func(_ context.Context, _ executor.Request) (executor.Result, error) {
		repo.commit("head-eval")
		close(brk)
		return executor.Result{Output: "evaluated"}, nil
	}
	env.Break = brk

	res := External(context.Background(), env)

	if res.Reason != ReasonUserBreak {
		t.Fatalf("reason = %q, want %q", res.Reason, ReasonUserBreak)
	}
	if res.Iterations != 1 {
		t.Errorf("iterations = %d, want 1", res.Iterations)
	}
	if res.OK() {
		t.Error("a broken loop must not report OK")
	}
}

func TestExternalIterationLimit(t *testing.T) {
	external := mock("codex", "FINDING: minor | a.go:1 | external | - | naming")
	primary := mock("claude", "evaluated")
	env, repo := newEnv(t, primary, external, func(c *config.Config) { c.ExternalMaxIterations = 2 })
	primary.Handler = func(_ context.Context, _ executor.Request) (executor.Result, error) {
		repo.commit("head" + strings.Repeat("x", primary.CallCount()))
		return executor.Result{Output: "evaluated"}, nil
	}

	res := External(context.Background(), env)

	if res.Reason != ReasonIterationLimit {
		t.Fatalf("reason = %q, want %q", res.Reason, ReasonIterationLimit)
	}
	if external.CallCount() != 2 || primary.CallCount() != 2 {
		t.Errorf("external calls = %d, primary calls = %d, want 2 and 2", external.CallCount(), primary.CallCount())
	}
}

func TestExternalReportsToolFailure(t *testing.T) {
	external := mock("codex", "cannot obtain the diff\n"+taskFailed)
	primary := mock("claude", externalDone)
	env, _ := newEnv(t, primary, external, nil)

	res := External(context.Background(), env)

	if res.Reason != ReasonFailure {
		t.Fatalf("reason = %q, want %q", res.Reason, ReasonFailure)
	}
	if primary.CallCount() != 0 {
		t.Error("a failed external review must not be handed to the executor")
	}
}
