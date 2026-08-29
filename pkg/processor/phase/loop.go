package phase

import (
	"context"
	"errors"
	"fmt"

	"github.com/korthane/rrev/pkg/config"
	"github.com/korthane/rrev/pkg/executor"
)

// stepResult is what one iteration produced.
type stepResult struct {
	// Converged is true when the iteration ended on its phase's done signal.
	Converged  bool
	Findings   []Finding
	Rejections []Rejection
	// output is the executor's raw text, which the external loop hands to the
	// primary executor to evaluate.
	output string
}

// loopSpec describes one review loop: what to call it, how many times it may
// run, and what a single iteration does.
type loopSpec struct {
	name  string
	limit int
	// arm is called once when the loop starts and returns the channel a user
	// break closes; nil means this loop cannot be broken.
	arm func() <-chan struct{}
	run func(ctx context.Context, iteration, limit int) (stepResult, error)
}

// drive runs a review loop to one of its terminating conditions and reports
// which one ended it. An iteration that emits no done signal means "work was
// done, iterate again": convergence is only ever stated explicitly.
func (e *Env) drive(ctx context.Context, spec loopSpec) Result {
	limit := max(spec.limit, 1)
	if e.SinglePass {
		limit = 1
	}

	// Arming here is what makes a break belong to the loop that is running:
	// one sent while an earlier phase ran was meant for that phase, and the
	// key stays live for this loop instead of being spent on it.
	var brk <-chan struct{}
	if spec.arm != nil {
		brk = spec.arm()
	}

	// A break cancels the call in flight rather than waiting for it: a loop the
	// user ended should stop spending an executor on the iteration it is in.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	if brk != nil {
		go func() {
			select {
			case <-brk:
				cancel()
			case <-runCtx.Done():
			}
		}()
	}

	res := Result{Name: spec.name}
	e.Log.PhaseStart(spec.name)

	before := e.snapshot(ctx)
	stale := 0
	for n := 1; n <= limit; n++ {
		if interrupted(brk) {
			res.Reason = ReasonUserBreak
			break
		}
		e.Log.IterationStart(spec.name, n, limit)
		res.Iterations = n

		step, err := e.runStep(runCtx, spec, brk, n, limit)
		res.Findings = append(res.Findings, step.Findings...)
		res.Rejections = append(res.Rejections, step.Rejections...)
		if err != nil {
			res.Reason, res.Err = ReasonFailure, err
			switch {
			case interrupted(brk):
				// The user ending the loop is an outcome, not a failure: the
				// pipeline carries on with the phase after it.
				res.Reason, res.Err = ReasonUserBreak, nil
			case ctx.Err() != nil:
				res.Reason = ReasonAborted
			}
			break
		}

		after := e.snapshot(runCtx)
		e.recordCommits(runCtx, before, after)
		if after.same(before) {
			stale++
		} else {
			res.Changed, stale = true, 0
		}
		before = after

		switch {
		case step.Converged:
			res.Reason = ReasonConverged
		case e.SinglePass:
			res.Reason = ReasonSinglePass
		case e.Config.StalematePatience > 0 && stale >= e.Config.StalematePatience:
			res.Reason = ReasonStalemate
		case n == limit:
			res.Reason = ReasonIterationLimit
		default:
			continue
		}
		break
	}

	e.report(res)
	return res
}

// retryBudget bounds how many times one iteration may be re-run after the
// executor reported a failure it called transient: enough to ride out a blip,
// few enough that a provider that keeps failing still ends the run.
const retryBudget = 2

// runStep runs one iteration, re-running it when the executor reports a
// transient failure rather than ending the whole pipeline on a flaky call.
func (e *Env) runStep(ctx context.Context, spec loopSpec, brk <-chan struct{}, n, limit int) (stepResult, error) {
	for attempt := 0; ; attempt++ {
		step, err := spec.run(ctx, n, limit)
		if err == nil || attempt >= retryBudget || !errors.Is(err, executor.ErrRetryable) ||
			ctx.Err() != nil || interrupted(brk) {
			return step, err
		}
		e.note("%s iteration %d: %v; retrying", Label(spec.name), n, err)
	}
}

// report states the terminating condition and the iteration count, in the
// terminal and in the progress log.
func (e *Env) report(res Result) {
	e.Log.LoopEnd(res.Name, string(res.Reason), res.Iterations)
	e.say("%s ended after %s: %s", Label(res.Name), plural(res.Iterations, "iteration"), res.Reason)
	if res.Err != nil {
		e.note("%s error: %v", Label(res.Name), res.Err)
	}
}

// review runs one prompt through an executor and reads the report and the
// signal back out of its output.
func (e *Env) review(ctx context.Context, call reviewCall) (stepResult, error) {
	prompt, err := config.Expander{Assets: e.Assets, Executor: call.renderAs, Vars: call.vars}.Prompt(call.prompt)
	if err != nil {
		return stepResult{}, err
	}

	spec, warning := executor.Select(e.Config, call.model, call.exec.Name())
	if warning != "" {
		e.note("%s: %s", Label(call.phase), warning)
	}

	out, runErr := call.exec.Run(ctx, executor.Request{
		Prompt: prompt,
		Dir:    e.Dir,
		Phase:  call.phase,
		Model:  spec.Model,
		Effort: spec.Effort,
		Stream: e.stream(call.phase),
	})

	// Record whatever the call produced before judging it: a cancelled or
	// throttled call may still have reported findings worth keeping.
	findings, rejections, validations := ParseReport(out.Output)
	e.record(call, findings, rejections, validations)
	step := stepResult{Findings: findings, Rejections: rejections, output: out.Output}

	switch {
	case runErr != nil:
		return step, fmt.Errorf("%s: %w", call.exec.Name(), runErr)
	case out.Signal == executor.SignalFailed:
		return step, fmt.Errorf("%s reported %s", call.exec.Name(), out.Signal.Marker())
	}
	step.Converged = out.Signal == call.done
	return step, nil
}

// reviewCall is one executor invocation within an iteration.
type reviewCall struct {
	phase  string
	prompt string
	exec   executor.Executor
	// model names the phase whose model specification this call resolves.
	model executor.Phase
	// done is the signal that means this call converged.
	done executor.Signal
	vars config.Vars
	// renderAs is the executor identity the prompt is rendered for, which
	// decides how agent references are spelled.
	renderAs string
	// verified marks a call whose report was checked against the real code.
	// What the independent review tool reports is not.
	verified bool
}

// record writes a call's report to the progress log. A finding the executor
// verified against real code before fixing is recorded as confirmed, while an
// unverified report from the independent tool is recorded as reported only. A
// rejection carries its reason, which is what a later round is shown instead of
// the finding itself, and a validation outcome is recorded as reported: rrev
// does not run the validation command itself.
func (e *Env) record(call reviewCall, findings []Finding, rejections []Rejection, validations []Validation) {
	// The fallback is written back into the slice, not into a copy: the same
	// findings become the run's result and the report's Reviewer column.
	for i := range findings {
		if findings[i].Reviewer == "" {
			findings[i].Reviewer = call.exec.Name()
		}
		if call.verified {
			e.Log.Confirmed(findings[i].entry(), e.confirmedAction())
			continue
		}
		e.Log.Finding(findings[i].entry())
	}
	for i := range rejections {
		if rejections[i].Reviewer == "" {
			rejections[i].Reviewer = call.exec.Name()
		}
		e.Log.Rejected(rejections[i].entry(), rejections[i].Reason)
	}
	for _, v := range validations {
		e.Log.Validation(v.entry())
	}
}

// confirmedAction names what became of a confirmed finding. Report-only mode
// forbids the executor from changing anything, so the log must not claim a fix
// the run was never allowed to make.
func (e *Env) confirmedAction() string {
	if e.SinglePass {
		return "reported; not fixed (report-only)"
	}
	return "fixed"
}

// iterVars overlays the per-iteration values on the run-wide ones.
func (e *Env) iterVars(n, limit int) config.Vars {
	vars := e.Vars
	vars.Iteration, vars.MaxIterations = n, limit
	// Read fresh each iteration: the ledger is what the previous iteration
	// learned, and expanding a stale copy would re-open questions it settled.
	vars.Ledger = e.Log.PromptEntries()
	return vars
}

// treeState is what stalemate detection compares: the branch tip plus the
// uncommitted state of the work tree.
type treeState struct {
	head  string
	tree  string
	known bool
}

// same reports two iterations as identical only when both states are known. An
// unknown state counts as movement, so a repository rrev cannot inspect never
// produces a false stalemate.
func (s treeState) same(o treeState) bool {
	return s.known && o.known && s.head == o.head && s.tree == o.tree
}

// recordCommits logs the commits an iteration produced. It is the only part of
// an iteration's outcome rrev can observe for itself; everything else the
// executor did it has to say in its own report.
func (e *Env) recordCommits(ctx context.Context, before, after treeState) {
	if e.Repo == nil || !before.known || !after.known || before.head == after.head {
		return
	}
	commits, err := e.Repo.Commits(ctx, before.head)
	if err != nil {
		return
	}
	for _, c := range commits {
		e.Log.Commit(c.Hash, c.Subject)
	}
}

func (e *Env) snapshot(ctx context.Context) treeState {
	if e.Repo == nil {
		return treeState{}
	}
	head, err := e.Repo.HeadHash(ctx)
	if err != nil {
		return treeState{}
	}
	tree, err := e.Repo.WorkingTreeFingerprint(ctx)
	if err != nil {
		return treeState{}
	}
	return treeState{head: head, tree: tree, known: true}
}

func interrupted(brk <-chan struct{}) bool {
	select {
	case <-brk:
		return true
	default:
		return false
	}
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
