package phase

import (
	"context"
	"strings"
	"testing"

	"github.com/korthane/rrev/pkg/config"
)

func TestFinalSkippedWhenNothingChanged(t *testing.T) {
	primary := mock("claude", reviewDone)
	env, _ := newEnv(t, primary, nil, nil)
	prior := []Result{
		{Name: NameComprehensive, Reason: ReasonConverged, Iterations: 1},
		{Name: NameExternal, Reason: ReasonConverged, Iterations: 1},
	}

	res := Final(context.Background(), env, prior...)

	if !res.Skipped {
		t.Fatalf("result = %+v, want a skipped phase", res)
	}
	if !strings.Contains(res.SkipReason, "regressed") {
		t.Errorf("skip reason = %q, want it to explain there is nothing to regress", res.SkipReason)
	}
	if primary.CallCount() != 0 {
		t.Errorf("executor calls = %d, want 0", primary.CallCount())
	}
}

// Converging on severity is the outcome this change makes common, and it
// reaches skipFinal through Result.OK() exactly as the signal does. Whether the
// regression pass runs turns on what the phase changed, not on which of the two
// mechanisms ended it.
func TestFinalKeysOffChangesNotOnWhichConvergenceEndedTheComprehensivePhase(t *testing.T) {
	for _, tt := range []struct {
		name      string
		changed   bool
		wantSkip  bool
		wantCalls int
	}{
		{"minor-only iteration committed fixes", true, false, 1},
		{"minor-only iteration committed nothing", false, true, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			primary := mock("claude", reviewDone)
			env, _ := newEnv(t, primary, nil, nil)
			prior := []Result{
				{Name: NameComprehensive, Reason: ReasonMinorOnly, Iterations: 1, Changed: tt.changed},
				{Name: NameExternal, Reason: ReasonConverged, Iterations: 1},
			}

			res := Final(context.Background(), env, prior...)

			if res.Skipped != tt.wantSkip {
				t.Errorf("skipped = %v, want %v; result = %+v", res.Skipped, tt.wantSkip, res)
			}
			if primary.CallCount() != tt.wantCalls {
				t.Errorf("executor calls = %d, want %d", primary.CallCount(), tt.wantCalls)
			}
		})
	}
}

func TestFinalRunsAfterFixes(t *testing.T) {
	primary := mock("claude", "no critical or major issues\n"+reviewDone)
	env, _ := newEnv(t, primary, nil, nil)
	prior := []Result{
		{Name: NameComprehensive, Reason: ReasonConverged, Iterations: 2, Changed: true},
		{Name: NameExternal, Reason: ReasonConverged, Iterations: 1},
	}

	res := Final(context.Background(), env, prior...)

	if res.Skipped {
		t.Fatalf("result = %+v, want the phase to run", res)
	}
	if res.Reason != ReasonConverged || res.Iterations != 1 {
		t.Errorf("result = %+v, want convergence after one iteration", res)
	}
	calls := primary.Calls()
	if len(calls) != 1 {
		t.Fatalf("executor calls = %d, want 1", len(calls))
	}
	if calls[0].Phase != NameFinal {
		t.Errorf("request phase = %q, want %q", calls[0].Phase, NameFinal)
	}
	prompt := calls[0].Prompt
	if !strings.Contains(prompt, "critical and major") {
		t.Error("the final prompt does not restrict the pass to critical and major issues")
	}
	for _, agent := range []string{"quality", "implementation", "conformance"} {
		if !strings.Contains(prompt, "<<<AGENT "+agent) {
			t.Errorf("the final prompt is missing the %s agent", agent)
		}
	}
	for _, agent := range []string{"testing", "simplification", "documentation", "tasks"} {
		if strings.Contains(prompt, "<<<AGENT "+agent) {
			t.Errorf("the final prompt must not launch the %s agent", agent)
		}
	}
}

func TestFinalRunsWhenAnEarlierPhaseDidNotConverge(t *testing.T) {
	primary := mock("claude", reviewDone)
	env, _ := newEnv(t, primary, nil, nil)
	prior := []Result{{Name: NameExternal, Reason: ReasonIterationLimit, Iterations: 5}}

	res := Final(context.Background(), env, prior...)

	if res.Skipped {
		t.Fatalf("result = %+v, want the phase to run", res)
	}
}

func TestFinalRunsWithNoPriorResults(t *testing.T) {
	primary := mock("claude", reviewDone)
	env, _ := newEnv(t, primary, nil, nil)

	if res := Final(context.Background(), env); res.Skipped {
		t.Fatalf("result = %+v, want the phase to run when nothing is known about earlier phases", res)
	}
}

func TestFinalUsesItsOwnIterationLimit(t *testing.T) {
	primary := mock("claude", "fixed a regression")
	env, repo := newEnv(t, primary, nil, func(c *config.Config) {
		c.MaxIterations = 10
		c.FinalMaxIterations = 2
	})
	primary.Handler = changingHandler(primary, repo)

	res := Final(context.Background(), env, Result{Reason: ReasonConverged, Changed: true})

	if res.Reason != ReasonIterationLimit || res.Iterations != 2 {
		t.Fatalf("result = %+v, want the final phase to stop at its own limit of 2", res)
	}
}
