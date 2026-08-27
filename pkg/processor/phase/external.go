package phase

import (
	"context"
	"fmt"
	"strings"

	"github.com/korthane/rrev/pkg/executor"
)

// External runs the second phase: an independent tool reviews the branch, then
// the primary executor evaluates what it reported, fixes what it confirms, and
// records why it rejected the rest. The two alternate until the loop terminates.
func External(ctx context.Context, e *Env) Result {
	if e.External == nil {
		return e.skip(NameExternal, "external review is disabled")
	}
	if reason, same := sameModel(e); same {
		return e.skip(NameExternal, "%s", reason)
	}

	var rounds []round
	return e.drive(ctx, loopSpec{
		name:  NameExternal,
		limit: e.Config.ExternalMaxIterations,
		brk:   e.Break,
		run: func(ctx context.Context, n, limit int) (stepResult, error) {
			return e.externalRound(ctx, n, limit, &rounds)
		},
	})
}

// round is one alternation, kept so the next iteration can show the external
// tool what it reported and how each finding was dispositioned.
type round struct {
	n          int
	reported   []Finding
	confirmed  []Finding
	rejections []Rejection
}

// externalRound runs the external tool and then the primary executor's
// evaluation of what it reported. The tool reporting nothing converges the loop
// without spending an evaluation on it.
func (e *Env) externalRound(ctx context.Context, n, limit int, rounds *[]round) (stepResult, error) {
	vars := e.iterVars(n, limit)
	vars.PriorFindings = renderPriorFindings(*rounds)

	report, err := e.review(ctx, reviewCall{
		phase:    NameExternal,
		prompt:   promptExternal,
		exec:     e.External,
		model:    executor.PhaseExternal,
		done:     executor.SignalExternalDone,
		vars:     vars,
		renderAs: e.External.Name(),
	})
	if err != nil || report.Converged {
		// A round that ends here was never evaluated, so the tool's own claims
		// stay out of the phase's result; they are already in the progress log
		// as reported-only entries.
		return stepResult{Converged: report.Converged, output: report.output}, err
	}

	evalVars := vars
	evalVars.ExternalOutput = report.output
	eval, err := e.review(ctx, reviewCall{
		phase:    NameExternal,
		prompt:   promptExternalEval,
		exec:     e.Primary,
		model:    executor.PhaseExternal,
		done:     executor.SignalExternalDone,
		vars:     evalVars,
		renderAs: e.Config.Executor,
		verified: true,
	})
	*rounds = append(*rounds, round{n: n, reported: report.Findings, confirmed: eval.Findings, rejections: eval.Rejections})

	// The external tool's own findings are the primary executor's input, not
	// the loop's result: only what the executor confirmed counts as a finding
	// of this phase.
	return eval, err
}

// sameModel reports the self-review the external phase exists to avoid. The
// comparison is between the tool and model the primary executor reviewed with
// and the ones the external tool would review with: identical on both counts,
// the second opinion is the first model reading its own work.
func sameModel(e *Env) (string, bool) {
	primary, external := e.Primary.Name(), e.External.Name()
	if primary != external {
		return "", false
	}
	primarySpec, _ := executor.Select(e.Config, executor.PhaseReview, primary)
	externalSpec, _ := executor.Select(e.Config, executor.PhaseExternal, external)
	if primarySpec != externalSpec {
		return "", false
	}
	return fmt.Sprintf("the external review tool and the primary executor are both %s at %s, so it would review its own work",
		external, primarySpec), true
}

// renderPriorFindings is the loop's round-to-round memory: what the external
// tool reported, what the executor confirmed, and what it rejected with which
// reason, so a rejected finding comes back only with an argument against the
// recorded reason.
func renderPriorFindings(rounds []round) string {
	if len(rounds) == 0 {
		return ""
	}
	var b strings.Builder
	for _, r := range rounds {
		fmt.Fprintf(&b, "Round %d\n", r.n)
		writeLines(&b, "  reported: ", r.reported, func(f Finding) string { return f.String() })
		writeLines(&b, "  confirmed and fixed: ", r.confirmed, func(f Finding) string { return f.String() })
		writeLines(&b, "  rejected: ", r.rejections, func(r Rejection) string { return r.String() })
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func writeLines[T any](b *strings.Builder, label string, items []T, render func(T) string) {
	if len(items) == 0 {
		fmt.Fprintf(b, "%s(none)\n", label)
		return
	}
	for _, item := range items {
		fmt.Fprintf(b, "%s%s\n", label, render(item))
	}
}
