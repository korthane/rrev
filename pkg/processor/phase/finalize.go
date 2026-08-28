package phase

import (
	"context"

	"github.com/korthane/rrev/pkg/executor"
)

// Finalize runs the optional step that follows the review phases: one call to
// the primary executor with the finalize prompt. It is disabled by default,
// never runs when a review phase ended without converging or when the run may
// not modify the repository, and runs exactly once - there is no iteration to
// pick up what it leaves behind. Its failure is recorded and reported but does
// not change the run's outcome, which is why it returns a Result the caller is
// free to ignore.
func Finalize(ctx context.Context, e *Env, prior ...Result) Result {
	if !e.Config.Finalize {
		// Disabled is the default, so the pipeline simply ends after the last
		// review phase; narrating it on every run would be noise.
		return Result{Name: NameFinalize, Reason: ReasonSkipped, Skipped: true, SkipReason: "finalize is not enabled"}
	}
	if e.SinglePass {
		return e.skip(NameFinalize, "the run may not modify the repository")
	}
	if len(prior) > 0 && allSkipped(prior) {
		// Rewriting history on the strength of a review that never ran is the
		// one outcome this step must never produce.
		return e.skip(NameFinalize, "no review phase ran, so there is nothing to finalize")
	}
	if name, ok := unconverged(prior); ok {
		return e.skip(NameFinalize, "the %s ended without converging", Label(name))
	}

	e.Log.PhaseStart(NameFinalize)
	before := e.snapshot(ctx)
	step, err := e.review(ctx, reviewCall{
		phase:    NameFinalize,
		prompt:   PromptFinalize,
		exec:     e.Primary,
		model:    executor.PhaseFinalize,
		done:     executor.SignalNone,
		vars:     e.iterVars(1, 1),
		renderAs: e.Config.Executor,
		verified: true,
	})

	after := e.snapshot(ctx)
	res := Result{
		Name:       NameFinalize,
		Reason:     ReasonConverged,
		Iterations: 1,
		Changed:    before.known && after.known && !after.same(before),
		Findings:   step.Findings,
		Rejections: step.Rejections,
	}
	if err != nil {
		res.Reason, res.Err = ReasonFailure, err
		if ctx.Err() != nil {
			res.Reason = ReasonAborted
		}
	}

	e.Log.LoopEnd(NameFinalize, string(res.Reason), res.Iterations)
	if res.Err != nil {
		e.note("finalize step failed: %v; the run's outcome is unchanged", res.Err)
		return res
	}
	e.note("finalize step done")
	return res
}

// allSkipped reports a run whose whole phase sequence was skipped, which every
// phase reports as OK because none of them left anything outstanding.
func allSkipped(prior []Result) bool {
	for _, res := range prior {
		if !res.Skipped {
			return false
		}
	}
	return true
}

// unconverged names the first earlier phase that left something outstanding,
// which is what keeps finalize from running on a branch the reviewers never
// agreed on.
func unconverged(prior []Result) (string, bool) {
	for _, res := range prior {
		if !res.OK() {
			return res.Name, true
		}
	}
	return "", false
}
