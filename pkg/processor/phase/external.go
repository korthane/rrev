package phase

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/korthane/rrev/pkg/config"
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

	state := &externalState{}
	return e.drive(ctx, loopSpec{
		name:  NameExternal,
		limit: e.Config.ExternalMaxIterations,
		arm:   e.Break,
		run: func(ctx context.Context, n, limit int) (stepResult, error) {
			return e.externalRound(ctx, n, limit, state)
		},
	})
}

// externalState is what the loop carries between iterations and between
// attempts at one iteration.
type externalState struct {
	rounds []round
	// pending is the tool's report for the iteration in progress, kept so an
	// evaluation re-run after a transient failure answers the same report —
	// and the same recorded ids — rather than invoking the tool again and
	// recording its findings a second time.
	pending *toolReport
}

// toolReport is one external tool call's outcome, recorded in the log under
// the ids the evaluator will be shown.
type toolReport struct {
	n          int
	step       stepResult
	ids        []string
	failed     error
	unreadable bool
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
func (e *Env) externalRound(ctx context.Context, n, limit int, state *externalState) (stepResult, error) {
	vars := e.iterVars(n, limit)
	vars.PriorFindings = renderPriorFindings(state.rounds)

	tool := state.pending
	state.pending = nil
	if tool == nil || tool.n != n {
		tool = e.runExternalTool(ctx, vars)
	}
	report, err := tool.step, tool.failed
	if err != nil || report.Converged {
		// A round that ends here was never evaluated, so the tool's own claims
		// stay out of the phase's result. Its report was not written eagerly
		// either: it is handed back for whoever decides the attempt is final.
		return stepResult{Converged: report.Converged, output: report.output, writeReport: report.writeReport}, err
	}

	evalVars := vars
	evalVars.ExternalOutput = report.output
	evalVars.ExternalFindings = renderExternalFindings(tool.ids, report.Findings)
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
	state.rounds = recordRound(state.rounds, round{n: n, reported: report.Findings, confirmed: eval.Findings, rejections: eval.Rejections})
	if errors.Is(err, executor.ErrRetryable) {
		// The retry of this iteration answers the same report, under the
		// same ids, rather than invoking the tool again.
		state.pending = tool
	}

	// A round whose output could not be read as a review is not this phase
	// converging on silence, however little the evaluator then found in it:
	// ending here would file a broken tool as a clean pass, which is the
	// confusion the recorded outcome above exists to remove.
	if tool.unreadable {
		eval.Converged = false
	}

	// The external tool's own findings are the primary executor's input, not
	// the loop's result: only what the executor confirmed counts as a finding
	// of this phase.
	return eval, err
}

// externalOutcome describes what came back from the external tool. A tool that
// reports nothing and a tool that died look the same from the loop's side —
// both simply fail to produce findings — so the log has to tell them apart or a
// quiet failure reads as a clean pass. It returns whether the round was
// unreadable as well as saying so, since rewording the message must not change
// what the loop then does about it.
func externalOutcome(report stepResult, err error) (outcome, detail string, unreadable bool) {
	switch {
	case err != nil:
		// The summary alone: the failure record written when the attempt is
		// judged final carries the diagnostic tail, and repeating the raw
		// error here would put the command line and stderr on one line first.
		return "failed", executor.Describe(err).Summary(), false
	case len(report.Findings) > 0:
		return fmt.Sprintf("reported %d finding(s)", len(report.Findings)), "", false
	case !report.Converged:
		// Neither a finding nor the done signal: the tool ran, but nothing in
		// what it wrote could be read as a review. Recording that as "no
		// findings" would file it as the clean convergence it is not.
		return "output not understood", "no findings and no completion signal in the tool's output", true
	default:
		return "no findings reported", "", false
	}
}

// runExternalTool invokes the tool and records its report at once, rather than
// holding the report for the round's end: the evaluator has to be shown the ids
// the findings were recorded under, since that is what lets its disposition
// land on the reported entry instead of opening a second one.
func (e *Env) runExternalTool(ctx context.Context, vars config.Vars) *toolReport {
	// Recorded before the call, not only after: a run killed mid-review leaves
	// the same unexplained silence in the log that it leaves on the console.
	e.Log.ExternalTool(e.External.Name(), "invoked", "")
	step, err := e.review(ctx, reviewCall{
		phase:    NameExternal,
		prompt:   PromptExternal,
		exec:     e.External,
		model:    executor.PhaseExternal,
		done:     executor.SignalExternalDone,
		vars:     vars,
		renderAs: e.External.Name(),
	})
	outcome, detail, unreadable := externalOutcome(step, err)
	// The outcome precedes the findings it summarises, so a reader meets the
	// summary before its detail.
	e.Log.ExternalTool(e.External.Name(), outcome, detail)
	tool := &toolReport{n: vars.Iteration, step: step, failed: err, unreadable: unreadable}
	if err == nil && !step.Converged {
		tool.ids = step.recordReport()
		tool.step.writeReport = nil
	}
	return tool
}

// renderExternalFindings renders the tool's findings as the report lines the
// evaluator will answer, each opening with the id the log assigned it. When the
// log handed out no ids — logging disabled — the lines are rendered bare, and
// the evaluator's dispositions are recorded as new, as any undeclared report is.
func renderExternalFindings(ids []string, findings []Finding) string {
	lines := make([]string, 0, len(findings))
	for i, f := range findings {
		if i < len(ids) && ids[i] != "" {
			f.ReRaises = ids[i]
		}
		lines = append(lines, f.String())
	}
	return strings.Join(lines, "\n")
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
