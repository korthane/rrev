package phase

import (
	"context"
	"strings"
	"testing"

	"github.com/korthane/rrev/pkg/config"
	"github.com/korthane/rrev/pkg/executor"
)

// An iteration that confirmed only minor findings ends the phase. Hunting the
// last minor is what ran the dogfooding runs to their iteration limit, so the
// severity of what an iteration found is a terminating condition in its own
// right, whether or not the executor said so.
func TestMinorOnlyIterationConvergesWithoutTheSignal(t *testing.T) {
	primary := mock("claude",
		"FINDING: minor | a.go:3 | quality | - | the name reads oddly\n"+
			"VALIDATION: pass | make test | -")
	env, repo := newEnv(t, primary, nil, func(c *config.Config) { c.MaxIterations = 5 })
	primary.Handler = changingHandler(primary, repo)
	log := openLog(t, env)
	var console strings.Builder
	env.Out = &console

	res := Comprehensive(context.Background(), env)

	if res.Reason != ReasonMinorOnly {
		t.Fatalf("reason = %q, want %q", res.Reason, ReasonMinorOnly)
	}
	if res.Iterations != 1 || primary.CallCount() != 1 {
		t.Errorf("iterations = %d, calls = %d, want 1 and 1", res.Iterations, primary.CallCount())
	}
	if !res.OK() {
		t.Error("a phase that converged on severity left nothing outstanding and must report OK")
	}
	// Which of the two mechanisms ended the phase is what a reader of the log
	// needs: convergence rrev decided reads differently from one the executor
	// declared.
	if got := readFile(t, log.Path()); !strings.Contains(got, "**comprehensive ended:** converged: minor findings only after 1 iteration(s)") {
		t.Errorf("log does not name the terminating condition:\n%s", got)
	}
	if want := "comprehensive review ended after 1 iteration: converged: minor findings only"; !strings.Contains(console.String(), want) {
		t.Errorf("console missing %q:\n%s", want, console.String())
	}
}

// The gate is about severity, not about quantity: one major keeps the phase
// going however many minors came with it.
func TestMajorFindingKeepsTheLoopAlive(t *testing.T) {
	primary := mock("claude",
		"FINDING: minor | a.go:3 | quality | - | the name reads oddly\n"+
			"FINDING: major | a.go:9 | quality | - | the handle outlives the request",
		"FINDING: minor | a.go:4 | quality | - | the comment is stale")
	env, repo := newEnv(t, primary, nil, func(c *config.Config) { c.MaxIterations = 5 })
	primary.Handler = changingHandler(primary, repo)

	res := Comprehensive(context.Background(), env)

	if res.Reason != ReasonMinorOnly {
		t.Fatalf("reason = %q, want the second iteration to converge", res.Reason)
	}
	if res.Iterations != 2 {
		t.Errorf("iterations = %d, want the major to buy another iteration", res.Iterations)
	}
}

// Critical is the other severity that keeps the loop alive; nothing in the gate
// should treat it more leniently than major.
func TestCriticalFindingKeepsTheLoopAlive(t *testing.T) {
	primary := mock("claude",
		"FINDING: critical | a.go:9 | conformance | Review target | the base ref is never resolved",
		reviewDone)
	env, repo := newEnv(t, primary, nil, func(c *config.Config) { c.MaxIterations = 5 })
	primary.Handler = changingHandler(primary, repo)

	res := Comprehensive(context.Background(), env)

	if res.Iterations != 2 {
		t.Errorf("iterations = %d, want a critical finding to buy another iteration", res.Iterations)
	}
}

// Converging on fixes the executor itself reported as unvalidated would end the
// phase on a branch whose tests do not pass.
func TestFailedValidationBlocksConvergence(t *testing.T) {
	primary := mock("claude",
		"FINDING: minor | a.go:3 | quality | - | the name reads oddly\n"+
			"VALIDATION: fail | make test | TestSignIn failed",
		"FINDING: minor | a.go:4 | quality | - | the comment is stale\n"+
			"VALIDATION: pass | make test | -")
	env, repo := newEnv(t, primary, nil, func(c *config.Config) { c.MaxIterations = 5 })
	primary.Handler = changingHandler(primary, repo)

	res := Comprehensive(context.Background(), env)

	if res.Iterations != 2 {
		t.Errorf("iterations = %d, want the failed validation to block the first iteration converging", res.Iterations)
	}
	if res.Reason != ReasonMinorOnly {
		t.Errorf("reason = %q, want the validated second iteration to converge", res.Reason)
	}
}

// The gate reads the severity the report wrote, and the parser hands over
// whatever word stood in that field. A vocabulary rrev does not know, and a
// line whose fields shifted so the location lands in the severity slot, are
// both reports it could not read — the same case as an empty one, and neither
// may end the phase.
func TestUnreadableSeverityDoesNotConverge(t *testing.T) {
	for name, report := range map[string]string{
		"drifted vocabulary": "FINDING: blocker | a.go:9 | quality | - | the token is logged in plaintext",
		"shifted fields":     "FINDING: a.go:9 | quality | - | the token is logged in plaintext",
		"absent severity":    "FINDING:  | a.go:9 | quality | - | the token is logged in plaintext",
	} {
		t.Run(name, func(t *testing.T) {
			primary := mock("claude", report, reviewDone)
			env, repo := newEnv(t, primary, nil, func(c *config.Config) { c.MaxIterations = 5 })
			primary.Handler = changingHandler(primary, repo)

			res := Comprehensive(context.Background(), env)

			// Without this the case could pass for the wrong reason: a line
			// that parsed into no finding at all is the empty-report path.
			if len(res.Findings) != 1 {
				t.Fatalf("findings = %d, want the line to have parsed into one", len(res.Findings))
			}
			if res.Iterations != 2 {
				t.Errorf("iterations = %d, want a severity rrev cannot read to buy another iteration", res.Iterations)
			}
		})
	}
}

// A model writes the outcome in whichever tense it reaches for, and the phase
// must not converge on fixes the executor itself said do not build.
func TestValidationReportedFailedInAnyTenseBlocksConvergence(t *testing.T) {
	for _, outcome := range []string{"fail", "failed", "FAILED", "failure"} {
		t.Run(outcome, func(t *testing.T) {
			primary := mock("claude",
				"FINDING: minor | a.go:3 | quality | - | the name reads oddly\n"+
					"VALIDATION: "+outcome+" | make test | TestSignIn failed",
				reviewDone)
			env, repo := newEnv(t, primary, nil, func(c *config.Config) { c.MaxIterations = 5 })
			primary.Handler = changingHandler(primary, repo)

			res := Comprehensive(context.Background(), env)

			if res.Iterations != 2 {
				t.Errorf("iterations = %d, want %q to block the first iteration converging", res.Iterations, outcome)
			}
		})
	}
}

// The done signal is the executor's claim that the phase is finished, and this
// branch invites it on any all-minor iteration — the same iteration that has
// just run the validation command over its own fixes. So the report saying
// those fixes do not validate has to override the marker, or the gate's
// fail-closed check is one an executor bypasses by being confident.
func TestFailedValidationBlocksConvergenceEvenWithTheSignal(t *testing.T) {
	primary := mock("claude",
		"FINDING: minor | a.go:3 | quality | - | the name reads oddly\n"+
			"VALIDATION: fail | make test | TestSignIn failed\n"+reviewDone,
		"FINDING: minor | a.go:4 | quality | - | the comment is stale\n"+
			"VALIDATION: pass | make test | -\n"+reviewDone)
	env, repo := newEnv(t, primary, nil, func(c *config.Config) { c.MaxIterations = 5 })
	primary.Handler = changingHandler(primary, repo)

	res := Comprehensive(context.Background(), env)

	if res.Iterations != 2 {
		t.Errorf("iterations = %d, want the failed validation to override the done signal", res.Iterations)
	}
	if res.Reason != ReasonConverged {
		t.Errorf("reason = %q, want the validated second iteration to converge on its signal", res.Reason)
	}
}

// The gate decides on the iteration's report, which is what the requirement
// says it reads. An executor that confirmed only minors and committed no fix
// for them still converges the phase: whether it did the work the prompt asked
// for is not something the report lets rrev check.
func TestMinorOnlyIterationConvergesWithoutACommit(t *testing.T) {
	primary := mock("claude",
		"FINDING: minor | a.go:3 | quality | - | the name reads oddly\n"+
			"VALIDATION: pass | make test | -")
	env, _ := newEnv(t, primary, nil, func(c *config.Config) { c.MaxIterations = 5 })

	res := Comprehensive(context.Background(), env)

	if res.Reason != ReasonMinorOnly {
		t.Errorf("reason = %q, want %q", res.Reason, ReasonMinorOnly)
	}
	if res.Changed {
		t.Error("no iteration committed anything, so the phase changed nothing")
	}
}

// A report with no findings at all cannot be told apart from an executor that
// died before writing one, so it is not convergence: a review that genuinely
// found nothing has the done signal to say so.
func TestEmptyReportDoesNotConverge(t *testing.T) {
	primary := mock("claude", "I read the diff and it looks fine.", reviewDone)
	env, repo := newEnv(t, primary, nil, func(c *config.Config) { c.MaxIterations = 5 })
	primary.Handler = changingHandler(primary, repo)

	res := Comprehensive(context.Background(), env)

	if res.Iterations != 2 {
		t.Errorf("iterations = %d, want an empty report to iterate rather than converge", res.Iterations)
	}
	if res.Reason != ReasonConverged {
		t.Errorf("reason = %q, want the signal to be what ended the phase", res.Reason)
	}
}

// Report-only mode runs one pass and reports what it found; the gate must not
// relabel that as convergence it never tested for.
func TestSinglePassKeepsItsReasonOnAMinorOnlyReport(t *testing.T) {
	primary := mock("claude", "FINDING: minor | a.go:3 | quality | - | the name reads oddly")
	env, _ := newEnv(t, primary, nil, func(c *config.Config) { c.MaxIterations = 5 })
	env.SinglePass = true

	res := Comprehensive(context.Background(), env)

	if res.Reason != ReasonSinglePass {
		t.Errorf("reason = %q, want %q", res.Reason, ReasonSinglePass)
	}
}

// The done signal keeps working exactly as before: it converges the phase
// whatever the report's severities were.
func TestDoneSignalStillConvergesThePhase(t *testing.T) {
	primary := mock("claude", "FINDING: major | a.go:9 | quality | - | the handle outlives the request\n"+reviewDone)
	env, repo := newEnv(t, primary, nil, func(c *config.Config) { c.MaxIterations = 5 })
	primary.Handler = changingHandler(primary, repo)

	res := Comprehensive(context.Background(), env)

	if res.Reason != ReasonConverged {
		t.Errorf("reason = %q, want the signal path unchanged", res.Reason)
	}
	if res.Iterations != 1 {
		t.Errorf("iterations = %d, want 1", res.Iterations)
	}
}

// A repeat iteration reviews what the one before it committed. The base it is
// scoped to is the commit that iteration started from, not the run's base and
// not the previous iteration's own scope: iteration 3 looks at what iteration 2
// changed, which is why the loop has to carry it forward one step at a time.
func TestRepeatIterationIsScopedToThePreviousIterationsFixes(t *testing.T) {
	primary := mock("claude", "FINDING: major | a.go:1 | quality | - | first", "FINDING: major | a.go:2 | quality | - | second", reviewDone)
	env, repo := newEnv(t, primary, nil, func(c *config.Config) { c.MaxIterations = 3 })
	primary.Handler = changingHandler(primary, repo)

	Comprehensive(context.Background(), env)

	calls := primary.Calls()
	if len(calls) != 3 {
		t.Fatalf("executor calls = %d, want 3", len(calls))
	}
	// The branch starts at head0 and each iteration commits before returning, so
	// iteration 2 reviews from the head iteration 1 began at, and iteration 3
	// from the head iteration 2 began at.
	for i, want := range []string{"", "git diff head0..HEAD", "git diff headx..HEAD"} {
		if want == "" {
			continue
		}
		if !strings.Contains(calls[i].Prompt, want) {
			t.Errorf("iteration %d prompt does not scope to %q:\n%s", i+1, want, calls[i].Prompt)
		}
	}
	if strings.Contains(calls[0].Prompt, "git diff head0..HEAD") {
		t.Error("the first iteration has nothing reviewed yet, so it must review the whole branch")
	}
	// The one positive assertion on the scope marker. Every other test checks
	// only for its absence, so a reword would leave those guards passing over
	// an instruction that no longer says which diff is primary.
	if !strings.Contains(calls[1].Prompt, "the fixes made since the last reviewed commit") {
		t.Errorf("a scoped iteration must say the fixes are the primary scope:\n%s", calls[1].Prompt)
	}
	for i, call := range calls {
		// The full branch never leaves the instruction: a fix can regress code
		// the scoped diff does not show.
		if !strings.Contains(call.Prompt, "git diff main...HEAD") {
			t.Errorf("iteration %d lost the full branch diff:\n%s", i+1, call.Prompt)
		}
	}
}

// An iteration that committed nothing leaves the next one an empty scoped diff,
// which would be a review of nothing. It goes back to the full branch instead.
func TestRepeatIterationFallsBackToTheFullBranchWithoutACommit(t *testing.T) {
	primary := mock("claude",
		"FINDING: major | a.go:1 | quality | - | first",
		"FINDING: major | a.go:2 | quality | - | second",
		reviewDone)
	env, repo := newEnv(t, primary, nil, func(c *config.Config) { c.MaxIterations = 3 })
	// Only the first iteration commits, so iteration 3 follows one that did not.
	primary.Handler = func(_ context.Context, _ executor.Request) (executor.Result, error) {
		n := primary.CallCount()
		if n == 1 {
			repo.commit("head1")
		}
		return respond(primary, n)
	}

	Comprehensive(context.Background(), env)

	calls := primary.Calls()
	if len(calls) != 3 {
		t.Fatalf("executor calls = %d, want 3", len(calls))
	}
	if !strings.Contains(calls[1].Prompt, "git diff head0..HEAD") {
		t.Errorf("iteration 2 follows a commit and must be scoped to it:\n%s", calls[1].Prompt)
	}
	// The scope marker, not a specific base: a base carried forward from an
	// older iteration is the regression worth catching, and naming one head
	// would let it through.
	if strings.Contains(calls[2].Prompt, "the fixes made since the last reviewed commit") {
		t.Errorf("iteration 3 follows an iteration that committed nothing and must review the whole branch:\n%s", calls[2].Prompt)
	}
	if !strings.Contains(calls[2].Prompt, "git diff main...HEAD") {
		t.Errorf("iteration 3 lost the full branch diff:\n%s", calls[2].Prompt)
	}
}

// An executor that edited files but never committed them is the sharpest form
// of "committed nothing": the loop counts the iteration as having changed the
// branch, so only the head-versus-tree distinction keeps the next iteration off
// an empty `git diff HEAD..HEAD` presented as the fixes to review.
func TestUncommittedWorkDoesNotScopeTheNextIteration(t *testing.T) {
	primary := mock("claude",
		"FINDING: major | a.go:1 | quality | - | first",
		"FINDING: major | a.go:2 | quality | - | second")
	env, repo := newEnv(t, primary, nil, func(c *config.Config) { c.MaxIterations = 2 })
	primary.Handler = func(_ context.Context, _ executor.Request) (executor.Result, error) {
		n := primary.CallCount()
		repo.edit("tree" + strings.Repeat("x", n))
		return respond(primary, n)
	}

	res := Comprehensive(context.Background(), env)

	if !res.Changed {
		t.Fatal("the working tree moved, so the loop must have seen the iteration change something")
	}
	calls := primary.Calls()
	if len(calls) != 2 {
		t.Fatalf("executor calls = %d, want 2", len(calls))
	}
	if strings.Contains(calls[1].Prompt, "the fixes made since the last reviewed commit") {
		t.Errorf("nothing was committed, so iteration 2 must review the whole branch:\n%s", calls[1].Prompt)
	}
	if !strings.Contains(calls[1].Prompt, "git diff main...HEAD") {
		t.Errorf("iteration 2 lost the full branch diff:\n%s", calls[1].Prompt)
	}
}

// The first iteration sweeps the branch and later ones review the fixes, so
// they are driven by different prompts. Both are overridable; which one an
// iteration gets is what this pins.
func TestRepeatIterationsUseTheRepeatPrompt(t *testing.T) {
	primary := mock("claude", "FINDING: major | a.go:1 | quality | - | first", reviewDone)
	env, repo := newEnv(t, primary, nil, func(c *config.Config) { c.MaxIterations = 3 })
	primary.Handler = changingHandler(primary, repo)

	Comprehensive(context.Background(), env)

	calls := primary.Calls()
	if len(calls) != 2 {
		t.Fatalf("executor calls = %d, want 2", len(calls))
	}
	if !strings.Contains(calls[0].Prompt, "Comprehensive review of:") ||
		strings.Contains(calls[0].Prompt, "Repeat comprehensive review of:") {
		t.Errorf("the first iteration must run the first-sweep prompt:\n%s", calls[0].Prompt)
	}
	if !strings.Contains(calls[1].Prompt, "Repeat comprehensive review of:") {
		t.Errorf("the second iteration must run the repeat prompt:\n%s", calls[1].Prompt)
	}
}

// The external loop's whole value is a second opinion on the branch. Scoping it
// to what the primary executor just changed would make it review the first
// model's own fixes instead, and the final pass is a regression sweep of the
// same branch. Neither may pick up the comprehensive phase's scoping.
func TestOtherPhasesKeepTheFullBranchDiff(t *testing.T) {
	external := mock("codex", "FINDING: major | a.go:1 | external | - | first", externalDone)
	primary := mock("claude", "FINDING: major | a.go:1 | quality | - | first", externalDone, reviewDone, reviewDone)
	env, repo := newEnv(t, primary, external, func(c *config.Config) {
		c.ExternalMaxIterations = 2
		c.FinalMaxIterations = 2
	})
	primary.Handler = changingHandler(primary, repo)

	External(context.Background(), env)
	Final(context.Background(), env)

	// A phase that never ran would satisfy every assertion below vacuously.
	if len(primary.Calls()) == 0 || len(external.Calls()) == 0 {
		t.Fatalf("primary calls = %d, external calls = %d, want both phases to have run",
			len(primary.Calls()), len(external.Calls()))
	}
	for _, call := range append(primary.Calls(), external.Calls()...) {
		if strings.Contains(call.Prompt, "the fixes made since the last reviewed commit") {
			t.Errorf("a phase outside the comprehensive loop was scoped to the fixes:\n%s", call.Prompt)
		}
		if !strings.Contains(call.Prompt, "git diff main...HEAD") {
			t.Errorf("a phase outside the comprehensive loop lost the full branch diff:\n%s", call.Prompt)
		}
	}
}
