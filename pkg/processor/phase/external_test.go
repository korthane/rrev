package phase

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/korthane/rrev/pkg/config"
	"github.com/korthane/rrev/pkg/executor"
	"github.com/korthane/rrev/pkg/progress"
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
	env.Break = func() <-chan struct{} { return brk }

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

// A break the user sent while another phase was running was meant for whatever
// was running then. The loop must therefore arm the break when it starts rather
// than watch a channel a break from an earlier phase already latched.
func TestExternalArmsTheBreakWhenTheLoopStarts(t *testing.T) {
	external := mock("codex", externalDone)
	primary := mock("claude", "evaluated")
	env, _ := newEnv(t, primary, external, nil)
	armed := 0
	env.Break = func() <-chan struct{} {
		armed++
		return make(chan struct{})
	}

	res := External(context.Background(), env)

	if armed != 1 {
		t.Errorf("the break was armed %d times, want once, when the loop started", armed)
	}
	if res.Reason != ReasonConverged {
		t.Fatalf("reason = %q, want %q: the break belonged to an earlier phase", res.Reason, ReasonConverged)
	}
	if external.CallCount() == 0 {
		t.Error("the external tool was never invoked")
	}
}

// An iteration re-run after a transient failure is still one round: recording
// it twice would show the next round the same report under the same number,
// which is exactly the memory meant to stop the tool repeating itself.
func TestExternalRetriedIterationRecordsOneRound(t *testing.T) {
	external := mock("codex", "FINDING: major | pkg/a.go:10 | external | - | the loop bound is off by one")
	primary := mock("claude", "")
	env, repo := newEnv(t, primary, external, func(c *config.Config) { c.ExternalMaxIterations = 2 })
	log, err := progress.Open(t.TempDir(), "add-user-auth", progress.Options{})
	if err != nil {
		t.Fatalf("open progress log: %v", err)
	}
	env.Log = log
	evals := 0
	primary.Handler = func(_ context.Context, _ executor.Request) (executor.Result, error) {
		evals++
		if evals == 1 {
			return executor.Result{}, &executor.LimitError{Tool: "claude", Reason: "overloaded_error", Retryable: true}
		}
		repo.commit("head-eval")
		return executor.Result{Output: "REJECTED: pkg/a.go:10 | external | the bound is inclusive by design"}, nil
	}

	External(context.Background(), env)

	calls := external.Calls()
	if len(calls) < 2 {
		t.Fatalf("external calls = %d, want the loop to reach its second round", len(calls))
	}
	if got := strings.Count(calls[len(calls)-1].Prompt, "Round 1"); got != 1 {
		t.Errorf("the last round's prompt reports round 1 %d times, want once", got)
	}
}

// A transient failure re-runs the whole iteration. Recording the attempt it
// superseded opens a second ledger entry for a finding the retry reports again,
// so the same argument stands twice in every prompt built from the ledger.
func TestRetriedIterationRecordsOneCopyOfItsFindings(t *testing.T) {
	external := mock("codex", "FINDING: major | pkg/a.go:10 | external | - | the loop bound is off by one")
	primary := mock("claude", "")
	env, repo := newEnv(t, primary, external, func(c *config.Config) { c.ExternalMaxIterations = 1 })
	log, err := progress.Open(t.TempDir(), "add-user-auth", progress.Options{})
	if err != nil {
		t.Fatalf("open progress log: %v", err)
	}
	env.Log = log
	evals := 0
	primary.Handler = func(_ context.Context, _ executor.Request) (executor.Result, error) {
		evals++
		if evals == 1 {
			return executor.Result{}, &executor.LimitError{Tool: "claude", Reason: "overloaded_error", Retryable: true}
		}
		repo.commit("head-eval")
		return executor.Result{Output: "REJECTED: pkg/a.go:10 | external | the bound is inclusive by design"}, nil
	}

	External(context.Background(), env)

	got := readFile(t, log.Path())
	if evals < 2 {
		t.Fatalf("the iteration was never retried, so nothing was superseded:\n%s", got)
	}
	if n := strings.Count(got, "the loop bound is off by one"); n != 1 {
		t.Errorf("the tool's finding is recorded %d times over one iteration, want once:\n%s", n, got)
	}
	if n := strings.Count(got, "the bound is inclusive by design"); n != 2 {
		t.Errorf("the rejection appears %d times, want its record and its one ledger row:\n%s", n, got)
	}
}

// TestExternalBreakCancelsTheCallInFlight covers the break arriving mid-call: a
// loop the user ended must not keep spending an executor on the iteration it is
// in, and the cancelled call must read as a break rather than as a failure.
func TestExternalBreakCancelsTheCallInFlight(t *testing.T) {
	brk := make(chan struct{})
	external := mock("codex", "")
	primary := mock("claude", "evaluated")
	env, _ := newEnv(t, primary, external, func(c *config.Config) { c.ExternalMaxIterations = 5 })
	external.Handler = func(ctx context.Context, _ executor.Request) (executor.Result, error) {
		close(brk)
		<-ctx.Done()
		return executor.Result{}, ctx.Err()
	}
	env.Break = func() <-chan struct{} { return brk }

	res := External(context.Background(), env)

	if res.Reason != ReasonUserBreak {
		t.Fatalf("reason = %q, want %q", res.Reason, ReasonUserBreak)
	}
	if res.Err != nil {
		t.Errorf("a user break is not a failure: %v", res.Err)
	}
	if primary.CallCount() != 0 {
		t.Errorf("the evaluation ran %d times after the break; want 0", primary.CallCount())
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

// The external tool's report is the primary executor's input. When the round
// ends before that evaluation happens, its claims must not become findings of
// the phase: nothing checked them against the code.
func TestExternalConvergedRoundReportsNoUnverifiedFindings(t *testing.T) {
	unverified := "FINDING: critical | a.go:1 | external | - | never verified\n" + externalDone
	primary, external := mock("claude", reviewDone), mock("codex", unverified)
	env, _ := newEnv(t, primary, external, nil)

	res := External(context.Background(), env)

	if res.Reason != ReasonConverged {
		t.Fatalf("reason = %q, want %q", res.Reason, ReasonConverged)
	}
	if len(res.Findings) != 0 {
		t.Errorf("findings = %+v, want none: the primary executor never evaluated them", res.Findings)
	}
	if primary.CallCount() != 0 {
		t.Errorf("primary calls = %d, want 0", primary.CallCount())
	}
}

func TestExternalFailedRoundReportsNoUnverifiedFindings(t *testing.T) {
	primary, external := mock("claude", reviewDone), mock("codex", "FINDING: critical | a.go:1 | external | - | never verified")
	external.Responses[0].Err = errors.New("codex died")
	env, _ := newEnv(t, primary, external, nil)

	res := External(context.Background(), env)

	if res.Reason != ReasonFailure {
		t.Fatalf("reason = %q, want %q", res.Reason, ReasonFailure)
	}
	if len(res.Findings) != 0 {
		t.Errorf("findings = %+v, want none: the primary executor never evaluated them", res.Findings)
	}
}

// TestExternalEvaluationUsesTheReviewModel guards the model each side of the
// loop runs with: external_model names a model of the external tool, so passing
// it to the primary executor's evaluation would fail the call outright.
func TestExternalEvaluationUsesTheReviewModel(t *testing.T) {
	external := mock("codex", "FINDING: major | pkg/a.go:10 | external | - | the mode flags are not exclusive", externalDone)
	primary := mock("claude", "FINDING: major | pkg/a.go:10 | external | - | fixed the missing exclusion")
	env, repo := newEnv(t, primary, external, func(c *config.Config) {
		c.ReviewModel = "opus"
		c.ExternalModel = "gpt-5-codex"
	})
	primary.Handler = changingHandler(primary, repo)

	External(context.Background(), env)

	calls := primary.Calls()
	if len(calls) == 0 {
		t.Fatal("the primary executor never evaluated the external findings")
	}
	if calls[0].Model != "opus" {
		t.Errorf("evaluation model = %q, want %q: the external tool's model must not reach the primary executor", calls[0].Model, "opus")
	}
	if got := external.Calls()[0].Model; got != "gpt-5-codex" {
		t.Errorf("external review model = %q, want %q", got, "gpt-5-codex")
	}
}
