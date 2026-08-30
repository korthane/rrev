package phase

import (
	"context"
	"fmt"

	"github.com/korthane/rrev/pkg/config"
	"github.com/korthane/rrev/pkg/executor"
)

// Comprehensive runs the first phase: each iteration launches every configured
// reviewer agent concurrently, then has the primary executor deduplicate, verify
// against the real code, fix what it confirmed, validate, and commit. It repeats
// until an iteration confirms nothing critical or major, or another terminating
// condition ends it.
func Comprehensive(ctx context.Context, e *Env) Result {
	return e.drive(ctx, loopSpec{
		name:      NameComprehensive,
		limit:     e.Config.MaxIterations,
		converged: minorOnly,
		run: func(ctx context.Context, it iteration) (stepResult, error) {
			return e.review(ctx, reviewCall{
				phase:    NameComprehensive,
				prompt:   comprehensivePrompt(it),
				exec:     e.Primary,
				model:    executor.PhaseReview,
				done:     executor.SignalReviewDone,
				vars:     e.comprehensiveVars(it),
				renderAs: e.Config.Executor,
				verified: true,
			})
		},
	})
}

// minorOnly reports an iteration that found nothing worth another full pass:
// every finding it confirmed is below major, and the fixes validated. Hunting
// the last minor is what runs a review to its iteration limit — the well never
// empties, since each fix is itself reviewable — so the phase stops here and
// leaves the regression pass to look for what the fixes broke.
//
// An empty report does not qualify: with no findings at all it cannot be told
// apart from an executor that died before writing one, and a review that truly
// found nothing has the done signal to say so.
func minorOnly(step stepResult) bool {
	if len(step.Findings) == 0 {
		return false
	}
	for _, f := range step.Findings {
		if f.Severity == severityCritical || f.Severity == severityMajor {
			return false
		}
	}
	for _, v := range step.Validations {
		if v.Outcome == outcomeFail {
			return false
		}
	}
	return true
}

// Severities that keep the loop alive, and the validation outcome that does.
// Parsing lowercases both, so these are compared as written.
const (
	severityCritical = "critical"
	severityMajor    = "major"
	outcomeFail      = "fail"
)

// comprehensivePrompt picks the prompt for one iteration: the first sweeps the
// branch, later ones review what the sweep changed.
func comprehensivePrompt(it iteration) string {
	if it.n > 1 {
		return PromptComprehensiveRepeat
	}
	return PromptComprehensive
}

// comprehensiveVars scopes a repeat iteration at the fixes the previous one
// committed. The full branch diff stays in the instruction: the fixes are what
// this iteration is for, but a regression they caused can surface anywhere.
func (e *Env) comprehensiveVars(it iteration) config.Vars {
	vars := e.iterVars(it)
	if it.reviewedBase != "" {
		vars.DiffInstruction = fmt.Sprintf(
			"git diff %s..HEAD - the fixes made since the last reviewed commit; review these first\n"+
				"  %s - the full branch, for context and for regressions those fixes may have caused in it",
			it.reviewedBase, vars.DiffInstruction)
	}
	return vars
}
