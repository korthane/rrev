package phase

import (
	"context"

	"github.com/korthane/rrev/pkg/executor"
)

// Comprehensive runs the first phase: each iteration launches every configured
// reviewer agent concurrently, then has the primary executor deduplicate, verify
// against the real code, fix what it confirmed, validate, and commit. It repeats
// until the review-done signal or another terminating condition.
func Comprehensive(ctx context.Context, e *Env) Result {
	return e.drive(ctx, loopSpec{
		name:  NameComprehensive,
		limit: e.Config.MaxIterations,
		run: func(ctx context.Context, n, limit int) (stepResult, error) {
			return e.review(ctx, reviewCall{
				phase:    NameComprehensive,
				prompt:   PromptComprehensive,
				exec:     e.Primary,
				model:    executor.PhaseReview,
				done:     executor.SignalReviewDone,
				vars:     e.iterVars(n, limit),
				renderAs: e.Config.Executor,
				verified: true,
			})
		},
	})
}
