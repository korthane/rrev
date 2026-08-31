package phase

import (
	"context"
	"fmt"
	"strings"

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
			step, err := e.review(ctx, reviewCall{
				phase:    NameComprehensive,
				prompt:   comprehensivePrompt(it),
				exec:     e.Primary,
				model:    executor.PhaseReview,
				done:     executor.SignalReviewDone,
				vars:     e.comprehensiveVars(it),
				renderAs: e.Config.Executor,
				verified: true,
			})
			// The prompt now asks for the done signal on any all-minor
			// iteration — the same iteration that just ran the validation
			// command over its own fixes — so the marker and a failed
			// validation arrive together. The report wins, or the gate's
			// fail-closed check is one an executor skips by being confident.
			if validationFailed(step) {
				step.Converged = false
			}
			return step, err
		},
	})
}

// minorOnly reports an iteration that found nothing worth another full pass:
// every finding it confirmed is a minor, and the fixes validated. Hunting
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
		// Only an explicit minor converges. The parser takes whatever word the
		// report put in the severity field, so a drifted vocabulary or a line
		// whose fields shifted arrives here as a severity rrev cannot read —
		// the same reporting failure an empty report is, and the loop is what
		// an unreadable report deserves.
		if f.Severity != severityMinor {
			return false
		}
	}
	return !validationFailed(step)
}

// validationFailed reports an iteration whose own report says its fixes do not
// validate. Prefix, because a model writes the outcome as fail, failed, or
// failure about as often as the word the template asks for. A line that is
// missing entirely is not a failure: requiring an explicit pass would block on
// the format drift this change exists to stop punishing.
func validationFailed(step stepResult) bool {
	for _, v := range step.Validations {
		if strings.HasPrefix(v.Outcome, outcomeFail) {
			return true
		}
	}
	return false
}

// The one severity that converges the phase, and the prefix of the validation
// outcomes that keep it going. Parsing lowercases both, so these are compared
// as written.
const (
	severityMinor = "minor"
	outcomeFail   = "fail"
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
