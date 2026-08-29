package phase

import (
	"context"
	"fmt"
	"slices"
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
		arm:   e.Break,
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

	// Recorded before the call, not only after: a run killed mid-review leaves
	// the same unexplained silence in the log that it leaves on the console,
	// and the tool's own findings are written by the call itself.
	e.Log.ExternalTool(e.External.Name(), "invoked", "")
	report, err := e.review(ctx, reviewCall{
		phase:    NameExternal,
		prompt:   PromptExternal,
		exec:     e.External,
		model:    executor.PhaseExternal,
		done:     executor.SignalExternalDone,
		vars:     vars,
		renderAs: e.External.Name(),
	})
	outcome, detail := externalOutcome(report, err)
	e.Log.ExternalTool(e.External.Name(), outcome, detail)
	unreadable := outcome == outcomeUnreadable

	if err != nil || report.Converged {
		// A round that ends here was never evaluated, so the tool's own claims
		// stay out of the phase's result; they are already in the progress log
		// as reported-only entries.
		return stepResult{Converged: report.Converged, output: report.output, writeReport: report.writeReport}, err
	}

	evalVars := vars
	evalVars.ExternalOutput = report.output
	// The evaluation runs under the primary executor, so it resolves the review
	// phase's model: external_model names a model of the external tool, which
	// the primary executor would reject as unknown.
	eval, err := e.review(ctx, reviewCall{
		phase:    NameExternal,
		prompt:   PromptExternalEval,
		exec:     e.Primary,
		model:    executor.PhaseReview,
		done:     executor.SignalExternalDone,
		vars:     evalVars,
		renderAs: e.Config.Executor,
		verified: true,
	})
	*rounds = recordRound(*rounds, round{n: n, reported: report.Findings, confirmed: eval.Findings, rejections: eval.Rejections})

	// Both calls' reports are written together, so a round re-run after a
	// transient failure in the evaluation records neither the tool's findings
	// nor the evaluation the retry supersedes.
	eval.writeReport = writeReports(report.writeReport, eval.writeReport)

	// A round whose output could not be read as a review is not this phase
	// converging on silence, however little the evaluator then found in it:
	// ending here would file a broken tool as a clean pass, which is the
	// confusion the recorded outcome above exists to remove.
	if unreadable {
		eval.Converged = false
	}

	// The external tool's own findings are the primary executor's input, not
	// the loop's result: only what the executor confirmed counts as a finding
	// of this phase.
	return eval, err
}

// outcomeUnreadable is the outcome of a round the loop must not read as
// convergence: the tool ran, but nothing it wrote was a review.
const outcomeUnreadable = "output not understood"

// externalOutcome describes what came back from the external tool. A tool that
// reports nothing and a tool that died look the same from the loop's side —
// both simply fail to produce findings — so the log has to tell them apart or a
// quiet failure reads as a clean pass.
func externalOutcome(report stepResult, err error) (outcome, detail string) {
	switch {
	case err != nil:
		return "failed", err.Error()
	case len(report.Findings) > 0:
		return fmt.Sprintf("reported %d finding(s)", len(report.Findings)), ""
	case !report.Converged:
		// Neither a finding nor the done signal: the tool ran, but nothing in
		// what it wrote could be read as a review. Recording that as "no
		// findings" would file it as the clean convergence it is not.
		return outcomeUnreadable, "no findings and no completion signal in the tool's output"
	default:
		return "no findings reported", ""
	}
}

// recordRound keeps one round per iteration: an iteration re-run after a
// transient failure replaces the round its failed attempt left behind, so the
// memory shown to the next round never reports the same round twice.
func recordRound(rounds []round, r round) []round {
	if i := slices.IndexFunc(rounds, func(x round) bool { return x.n == r.n }); i >= 0 {
		rounds[i] = r
		return rounds
	}
	return append(rounds, r)
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
