package phase

import (
	"context"

	"github.com/korthane/rrev/pkg/executor"
)

// Final runs the narrow regression pass that follows the external loop. It
// exists to catch what the earlier phases' own fixes broke, so it is skipped
// when those phases converged without changing anything: there is no fix that
// could have regressed.
func Final(ctx context.Context, e *Env, prior ...Result) Result {
	if reason, skip := skipFinal(prior); skip {
		return e.skip(NameFinal, "%s", reason)
	}
	return e.drive(ctx, loopSpec{
		name:  NameFinal,
		limit: e.Config.FinalMaxIterations,
		run: func(ctx context.Context, n, limit int) (stepResult, error) {
			return e.review(ctx, reviewCall{
				phase:    NameFinal,
				prompt:   promptFinal,
				exec:     e.Primary,
				model:    executor.PhaseFinal,
				done:     executor.SignalReviewDone,
				vars:     e.iterVars(n, limit),
				renderAs: e.Config.Executor,
				verified: true,
			})
		},
	})
}

// skipFinal reports whether the regression pass has nothing to look for: every
// earlier phase converged and none of them changed the branch, which is the case
// the external loop converging on its first pass produces.
func skipFinal(prior []Result) (string, bool) {
	if len(prior) == 0 {
		return "", false
	}
	for _, res := range prior {
		if res.Changed || !res.OK() {
			return "", false
		}
	}
	return "no earlier phase applied a fix that could have regressed anything", true
}
