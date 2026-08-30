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
	// With logging disabled no ids were assigned, so the parsed findings are
	// shown bare rather than under an empty token.
	if !strings.Contains(evalPrompt, "FINDING: major | pkg/a.go:10 | external | Run modes | the mode flags are not mutually exclusive") ||
		strings.Contains(evalPrompt, "FINDING[R1]:") || strings.Contains(evalPrompt, "FINDING[]:") {
		t.Errorf("the evaluation prompt must list the tool's findings without ids when none were assigned:\n%s", evalPrompt)
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
	log, err := progress.Open(t.TempDir(), "add-user-auth", progress.Options{})
	if err != nil {
		t.Fatalf("open progress log: %v", err)
	}
	env.Log = log

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
	// The user ending the loop is an outcome, not a failure to diagnose.
	if got := readFile(t, log.Path()); strings.Contains(got, "**failed**") {
		t.Errorf("a user break must not leave a failure record\n--- log ---\n%s", got)
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

// An evaluation that keeps failing transiently exhausts the retry budget and
// ends the phase. Every retry answers the report the tool already gave: the
// tool is invoked once, its findings are recorded once, and every attempt
// leaves its own failure record.
func TestPersistentEvaluationFailureKeepsTheToolsOneReport(t *testing.T) {
	external := mock("codex", "FINDING: major | pkg/a.go:10 | external | - | the loop bound is off by one")
	primary := &executor.Mock{Tool: "claude", Responses: []executor.Response{
		{Err: &executor.LimitError{Tool: "claude", Reason: "overloaded_error", Retryable: true}},
	}}
	env, _ := newEnv(t, primary, external, func(c *config.Config) { c.ExternalMaxIterations = 3 })
	log, err := progress.Open(t.TempDir(), "add-user-auth", progress.Options{})
	if err != nil {
		t.Fatalf("open progress log: %v", err)
	}
	env.Log = log

	res := External(context.Background(), env)

	if res.Reason != ReasonFailure || res.Err == nil {
		t.Fatalf("result = %+v, want the phase to fail", res)
	}
	if primary.CallCount() != retryBudget+1 {
		t.Errorf("evaluator calls = %d, want %d", primary.CallCount(), retryBudget+1)
	}
	if external.CallCount() != 1 {
		t.Errorf("external calls = %d, want 1: every retry must reuse the tool's report", external.CallCount())
	}
	got := readFile(t, log.Path())
	if n := strings.Count(got, "the loop bound is off by one"); n != 1 {
		t.Errorf("the tool's finding is recorded %d times, want once:\n%s", n, got)
	}
	want := "- **failed** claude: transient failure — external iteration 1"
	if strings.Count(got, want) != retryBudget+1 {
		t.Errorf("failure records = %d, want one per attempt (%d):\n%s", strings.Count(got, want), retryBudget+1, got)
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
	var console strings.Builder
	env.Out = &console
	evals := 0
	var retryPrompt string
	primary.Handler = func(_ context.Context, req executor.Request) (executor.Result, error) {
		evals++
		if evals == 1 {
			return executor.Result{}, &executor.LimitError{Tool: "claude", Reason: "overloaded_error", Retryable: true}
		}
		retryPrompt = req.Prompt
		repo.commit("head-eval")
		return executor.Result{Output: "REJECTED: pkg/a.go:10 | external | the bound is inclusive by design"}, nil
	}

	External(context.Background(), env)

	got := readFile(t, log.Path())
	if evals < 2 {
		t.Fatalf("the iteration was never retried, so nothing was superseded:\n%s", got)
	}
	// The retry answers the report the tool already gave, under the ids it
	// was recorded with, rather than invoking the tool a second time.
	if external.CallCount() != 1 {
		t.Errorf("external calls = %d, want 1: the retry must reuse the tool's report", external.CallCount())
	}
	if !strings.Contains(retryPrompt, "FINDING[R1]: major | pkg/a.go:10 | external | - | the loop bound is off by one") {
		t.Errorf("the retried evaluation was not shown the finding under its recorded id:\n%s", retryPrompt)
	}
	if n := strings.Count(got, "the loop bound is off by one"); n != 1 {
		t.Errorf("the tool's finding is recorded %d times over one iteration, want once:\n%s", n, got)
	}
	if n := strings.Count(got, "the bound is inclusive by design"); n != 2 {
		t.Errorf("the rejection appears %d times, want its record and its one ledger row:\n%s", n, got)
	}
	// The superseded attempt still failed, and its cause is recorded like any
	// other rather than left as a one-line note.
	for _, want := range []string{"- **failed** claude: transient failure — external iteration 1", "  overloaded_error"} {
		if !strings.Contains(got, want) {
			t.Errorf("log missing %q for the retried attempt:\n%s", want, got)
		}
	}
	// The console names the attempt, not the phase: the phase went on to succeed.
	if !strings.Contains(console.String(), "external review iteration 1 attempt failed: claude: transient failure") {
		t.Errorf("console does not announce the retried attempt:\n%s", console.String())
	}
	// The recorded cause says what went wrong; the note says what rrev did
	// about it, which is the only account of why a second attempt appeared.
	if !strings.Contains(console.String(), "external review iteration 1: retrying") {
		t.Errorf("console does not say the iteration is being retried:\n%s", console.String())
	}
	if strings.Contains(console.String(), "external review failed:") {
		t.Errorf("console announces the phase as failed for an attempt that was retried:\n%s", console.String())
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
	log, err := progress.Open(t.TempDir(), "add-user-auth", progress.Options{})
	if err != nil {
		t.Fatalf("open progress log: %v", err)
	}
	env.Log = log
	var console strings.Builder
	env.Out = &console

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
	// The call really was cancelled mid-flight, so this is the path where a
	// failure record would be written if the break were not downgrading the
	// cancellation to an outcome. The user ending the loop is not a failure
	// to diagnose, in the log or on the console.
	if got := readFile(t, log.Path()); strings.Contains(got, "**failed**") {
		t.Errorf("a cancelled call under a user break left a failure record\n--- log ---\n%s", got)
	}
	if strings.Contains(console.String(), "failed:") {
		t.Errorf("console announces a failure for a user break:\n%s", console.String())
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

// The external round returns before the evaluation on both a failure and an
// early convergence, and each carries the tool's own report back so it still
// reaches the log. A round that drops it loses every finding the tool reported
// from the log, the ledger, and so from every later reviewer's prompt.
func TestExternalRoundEndingEarlyStillRecordsWhatTheToolReported(t *testing.T) {
	const reported = "FINDING: major | pkg/a.go:10 | external | - | the handle outlives the request"
	for name, response := range map[string]executor.Response{
		"failed":    {Output: reported, Err: errors.New("codex exited with status 1")},
		"converged": {Output: reported + "\n" + externalDone},
	} {
		t.Run(name, func(t *testing.T) {
			primary := mock("claude", reviewDone)
			external := &executor.Mock{Tool: "codex", Responses: []executor.Response{response}}
			env, _ := newEnv(t, primary, external, nil)
			log, err := progress.Open(t.TempDir(), "add-user-auth", progress.Options{})
			if err != nil {
				t.Fatalf("open progress log: %v", err)
			}
			env.Log = log

			External(context.Background(), env)

			body := readFile(t, log.Path())
			if n := strings.Count(body, "the handle outlives the request"); n != 1 {
				t.Errorf("the tool's report was recorded %d times, want exactly one copy:\n%s", n, body)
			}
		})
	}
}

// A tool that says why it is dying on stdout and exits silently on stderr must
// leave that reason in the log, not an exit status alone.
func TestFailureCauseReachesTheLogAndConsole(t *testing.T) {
	primary := mock("claude", "")
	env, _ := newEnv(t, primary, nil, nil)
	log, err := progress.Open(t.TempDir(), "add-user-auth", progress.Options{})
	if err != nil {
		t.Fatalf("open progress log: %v", err)
	}
	env.Log = log
	var console strings.Builder
	env.Out = &console
	primary.Handler = func(_ context.Context, _ executor.Request) (executor.Result, error) {
		return executor.Result{Output: "Reviewing.\nError: prompt is too long for the context window"},
			&executor.Error{Tool: "claude", ExitCode: 1, Output: "Reviewing.\nError: prompt is too long for the context window", Err: errors.New("exit status 1")}
	}

	res := Comprehensive(context.Background(), env)

	if res.Reason != ReasonFailure {
		t.Fatalf("reason = %q, want %q", res.Reason, ReasonFailure)
	}
	for _, want := range []string{"- **failed** claude: failure (exit 1) — comprehensive iteration 1", "  Error: prompt is too long for the context window"} {
		if got := readFile(t, log.Path()); !strings.Contains(got, want) {
			t.Errorf("log missing %q:\n%s", want, got)
		}
	}
	// Two call sites write the record — the retry loop for a superseded
	// attempt and the phase's result — and a failure nothing supersedes
	// belongs to exactly one of them.
	if got := readFile(t, log.Path()); strings.Count(got, "- **failed** ") != 1 {
		t.Errorf("failure recorded %d times, want once:\n%s", strings.Count(got, "- **failed** "), got)
	}
	for _, want := range []string{"comprehensive review failed: claude: failure (exit 1)", "  Error: prompt is too long for the context window"} {
		if !strings.Contains(console.String(), want) {
			t.Errorf("console missing %q:\n%s", want, console.String())
		}
	}
}

// The evaluator is shown the tool's findings under their recorded ids, and a
// disposition carrying one lands on that entry: one finding, one ledger row.
func TestEvaluatorDispositionLandsOnTheReportedEntry(t *testing.T) {
	external := mock("codex", "FINDING: minor | pkg/a.go:10 | external | - | the loop bound is off by one")
	primary := mock("claude", "")
	env, repo := newEnv(t, primary, external, func(c *config.Config) { c.ExternalMaxIterations = 1 })
	log, err := progress.Open(t.TempDir(), "add-user-auth", progress.Options{})
	if err != nil {
		t.Fatalf("open progress log: %v", err)
	}
	env.Log = log
	var shown string
	primary.Handler = func(_ context.Context, req executor.Request) (executor.Result, error) {
		shown = req.Prompt
		repo.commit("head-eval")
		return executor.Result{Output: "REJECTED[R1]: pkg/a.go:10 | external | off by one | the bound is inclusive by design"}, nil
	}

	External(context.Background(), env)

	if !strings.Contains(shown, "FINDING[R1]: minor | pkg/a.go:10 | external | - | the loop bound is off by one") {
		t.Errorf("evaluator was not shown the finding under its id:\n%s", shown)
	}
	got := readFile(t, log.Path())
	if strings.Contains(got, "`R2`") {
		t.Errorf("the disposition opened a second entry:\n%s", got)
	}
	if !strings.Contains(got, "- **R1** `pkg/a.go:10`") {
		t.Errorf("ledger does not hold the reported entry as the rejected one:\n%s", got)
	}
	// Invocation and outcome precede the finding they summarise.
	invoked, reported := strings.Index(got, "external tool `codex`: reported 1 finding(s)"), strings.Index(got, "- **reported** `R1`")
	if invoked < 0 || reported < 0 || invoked > reported {
		t.Errorf("the tool's outcome must precede its findings:\n%s", got)
	}
}

// Every round assigns its own ids and shows the evaluator those, not the ids
// of the round before. A loop that handed round 2 stale ids would file its
// dispositions against round 1's entries, which is the merge rrev must never
// make on its own.
func TestEachRoundShowsTheEvaluatorItsOwnIds(t *testing.T) {
	external := mock("codex",
		"FINDING: major | pkg/a.go:10 | external | - | the loop bound is off by one",
		"FINDING: major | pkg/b.go:20 | external | - | the retry count is unbounded")
	primary := mock("claude", "")
	env, repo := newEnv(t, primary, external, func(c *config.Config) { c.ExternalMaxIterations = 2 })
	log, err := progress.Open(t.TempDir(), "add-user-auth", progress.Options{})
	if err != nil {
		t.Fatalf("open progress log: %v", err)
	}
	env.Log = log
	var shown []string
	primary.Handler = func(_ context.Context, req executor.Request) (executor.Result, error) {
		shown = append(shown, req.Prompt)
		repo.commit("head-eval" + strings.Repeat("x", len(shown)))
		if len(shown) == 1 {
			return executor.Result{Output: "REJECTED[R1]: pkg/a.go:10 | external | off by one | the bound is inclusive by design"}, nil
		}
		return executor.Result{Output: "REJECTED[R2]: pkg/b.go:20 | external | unbounded retries | the caller's budget bounds it"}, nil
	}

	External(context.Background(), env)

	if len(shown) != 2 {
		t.Fatalf("evaluations = %d, want 2", len(shown))
	}
	if !strings.Contains(shown[1], "FINDING[R2]: major | pkg/b.go:20 | external | - | the retry count is unbounded") {
		t.Errorf("the second round's evaluator was not shown its own finding under R2:\n%s", shown[1])
	}
	if strings.Contains(shown[1], "FINDING[R1]") {
		t.Errorf("the second round's evaluator was shown the first round's id:\n%s", shown[1])
	}
	got := readFile(t, log.Path())
	// Two reports, two dispositions, two entries: neither disposition opened
	// one of its own.
	if strings.Contains(got, "`R3`") {
		t.Errorf("a disposition opened a third entry over two rounds:\n%s", got)
	}
	for _, want := range []string{"- **reported** `R1`", "- **reported** `R2`", "- **R1** `pkg/a.go:10`", "- **R2** `pkg/b.go:20`"} {
		if !strings.Contains(got, want) {
			t.Errorf("log missing %q:\n%s", want, got)
		}
	}
}

// An evaluator that drops the id degrades to exactly the old behaviour: its
// disposition is a new finding, and the reported entry stays as reported.
func TestEvaluatorOmittingTheIdRecordsANewFinding(t *testing.T) {
	external := mock("codex", "FINDING: minor | pkg/a.go:10 | external | - | the loop bound is off by one")
	primary := mock("claude", "REJECTED: pkg/a.go:10 | external | off by one | the bound is inclusive by design")
	env, repo := newEnv(t, primary, external, func(c *config.Config) { c.ExternalMaxIterations = 1 })
	log, err := progress.Open(t.TempDir(), "add-user-auth", progress.Options{})
	if err != nil {
		t.Fatalf("open progress log: %v", err)
	}
	env.Log = log
	primary.Handler = changingHandler(primary, repo)

	External(context.Background(), env)

	got := readFile(t, log.Path())
	if !strings.Contains(got, "- **rejected** `R2`") {
		t.Errorf("an undeclared disposition must be a new finding:\n%s", got)
	}
	if strings.Contains(got, "**R1** `pkg/a.go:10`") {
		t.Errorf("the reported entry must not enter the ledger on its own:\n%s", got)
	}
}

// The ids pair with the findings by position, which is the order record()
// handed them out in; a finding the log gave no id renders bare, so the
// evaluator declares nothing for it rather than something wrong.
func TestRenderExternalFindingsPairsIdsByPosition(t *testing.T) {
	findings := []Finding{
		{Severity: "minor", File: "a.go", Line: 1, Reviewer: "external", Summary: "one"},
		{Severity: "major", File: "b.go", Line: 2, Reviewer: "external", Summary: "two", ReRaises: "R9"},
		{Severity: "minor", File: "c.go", Line: 3, Reviewer: "external", Summary: "three"},
	}
	want := []string{
		"FINDING[R1]: minor | a.go:1 | external | - | one",
		"FINDING[R3]: major | b.go:2 | external | - | two",
		"FINDING: minor | c.go:3 | external | - | three",
	}
	got := renderExternalFindings([]string{"R1", "R3"}, findings)
	if got != strings.Join(want, "\n") {
		t.Errorf("rendered:\n%s\nwant:\n%s", got, strings.Join(want, "\n"))
	}
	if got := renderExternalFindings(nil, findings[:1]); got != "FINDING: minor | a.go:1 | external | - | one" {
		t.Errorf("with no ids the lines must render bare, got %q", got)
	}
	// A disabled log hands out an empty id per finding, not a short list.
	if got := renderExternalFindings([]string{""}, findings[:1]); got != "FINDING: minor | a.go:1 | external | - | one" {
		t.Errorf("an empty id must render the line bare, got %q", got)
	}
}

// A transient failure of the tool itself leaves no report to reuse: the retry
// invokes the tool again, and the report it then gives is the one the
// evaluator is shown, recorded once, after the failed attempt's cause.
func TestExternalToolRetriedAfterTransientFailure(t *testing.T) {
	external := mock("codex", "FINDING: major | pkg/a.go:10 | external | - | the loop bound is off by one")
	external.Responses = append([]executor.Response{
		{Err: &executor.LimitError{Tool: "codex", Reason: "overloaded_error", Retryable: true}},
	}, external.Responses...)
	primary := mock("claude", "")
	env, repo := newEnv(t, primary, external, func(c *config.Config) { c.ExternalMaxIterations = 1 })
	log, err := progress.Open(t.TempDir(), "add-user-auth", progress.Options{})
	if err != nil {
		t.Fatalf("open progress log: %v", err)
	}
	env.Log = log
	var shown string
	primary.Handler = func(_ context.Context, req executor.Request) (executor.Result, error) {
		shown = req.Prompt
		repo.commit("head-eval")
		return executor.Result{Output: "REJECTED[R1]: pkg/a.go:10 | external | off by one | the bound is inclusive by design"}, nil
	}

	External(context.Background(), env)

	if external.CallCount() != 2 {
		t.Errorf("external calls = %d, want 2: a tool that failed left nothing to reuse", external.CallCount())
	}
	if !strings.Contains(shown, "FINDING[R1]: major | pkg/a.go:10 | external | - | the loop bound is off by one") {
		t.Errorf("the evaluator was not shown the retried tool's finding under its id:\n%s", shown)
	}
	got := readFile(t, log.Path())
	if !strings.Contains(got, "- **failed** codex: transient failure — external iteration 1") {
		t.Errorf("the failed attempt's cause is not recorded:\n%s", got)
	}
	// The ledger below the iterations echoes the claim; the record itself
	// must appear once.
	iterations, _, _ := strings.Cut(got, "## Standing rejections")
	if n := strings.Count(iterations, "the loop bound is off by one"); n != 1 {
		t.Errorf("the tool's finding is recorded %d times, want once:\n%s", n, got)
	}
}

// The confirming half of the round trip: a FINDING line carrying the reported
// id lands on that entry, so the ledger holds one finding, now confirmed.
func TestEvaluatorConfirmationLandsOnTheReportedEntry(t *testing.T) {
	external := mock("codex", "FINDING: minor | pkg/a.go:10 | external | - | the loop bound is off by one")
	primary := mock("claude", "")
	env, repo := newEnv(t, primary, external, func(c *config.Config) { c.ExternalMaxIterations = 1 })
	log, err := progress.Open(t.TempDir(), "add-user-auth", progress.Options{})
	if err != nil {
		t.Fatalf("open progress log: %v", err)
	}
	env.Log = log
	primary.Handler = func(_ context.Context, _ executor.Request) (executor.Result, error) {
		repo.commit("head-eval")
		return executor.Result{Output: "FINDING[R1]: minor | pkg/a.go:10 | external | - | the loop bound is off by one"}, nil
	}

	res := External(context.Background(), env)

	if len(res.Findings) != 1 {
		t.Fatalf("findings = %+v, want the one the evaluator confirmed", res.Findings)
	}
	got := readFile(t, log.Path())
	if !strings.Contains(got, "- **confirmed** `R1`") {
		t.Errorf("the confirmation did not land on the reported entry:\n%s", got)
	}
	if strings.Contains(got, "`R2`") {
		t.Errorf("the confirmation opened a second entry:\n%s", got)
	}
}

// The tool may itself name a standing entry, or an id the log does not hold.
// Either way the evaluator must be shown the id the log resolved the finding
// to — the standing entry, or the new one opened for the unknown id — since a
// disposition against the tool's own token would land on nothing.
func TestEvaluatorIsShownTheResolvedIdNotTheToolsToken(t *testing.T) {
	external := mock("codex",
		"FINDING[R1]: major | pkg/a.go:7 | external | - | the token is echoed\n"+
			"FINDING[R99]: minor | pkg/b.go:2 | external | - | the cast is unchecked")
	primary := mock("claude", "")
	env, repo := newEnv(t, primary, external, func(c *config.Config) { c.ExternalMaxIterations = 1 })
	log, err := progress.Open(t.TempDir(), "add-user-auth", progress.Options{})
	if err != nil {
		t.Fatalf("open progress log: %v", err)
	}
	env.Log = log
	log.Rejected(progress.Finding{Reviewer: "quality", Severity: "major", File: "pkg/a.go", Line: 7, Summary: "the token is echoed"},
		"the value is not key material")
	var shown string
	primary.Handler = func(_ context.Context, req executor.Request) (executor.Result, error) {
		shown = req.Prompt
		repo.commit("head-eval")
		return executor.Result{Output: "REJECTED[R1]: pkg/a.go:7 | external | the token is echoed | the value is not key material\n" +
			"REJECTED[R2]: pkg/b.go:2 | external | the cast is unchecked | the type is fixed by the caller"}, nil
	}

	External(context.Background(), env)

	for _, want := range []string{
		"FINDING[R1]: major | pkg/a.go:7 | external | - | the token is echoed",
		"FINDING[R2]: minor | pkg/b.go:2 | external | - | the cast is unchecked",
	} {
		if !strings.Contains(shown, want) {
			t.Errorf("evaluator prompt missing %q:\n%s", want, shown)
		}
	}
	got := readFile(t, log.Path())
	if strings.Contains(got, "`R3`") {
		t.Errorf("a disposition opened a third entry:\n%s", got)
	}
}
