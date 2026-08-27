package processor

import (
	"context"
	"fmt"
	"slices"

	"github.com/korthane/rrev/pkg/processor/phase"
)

// Mode selects where the pipeline starts and whether it may modify the
// repository. The modes are mutually exclusive; rejecting a combination of them
// is the command line's job, not this package's.
type Mode string

// Run modes, named after the flags that select them.
const (
	ModeFull         Mode = "full"
	ModeExternalOnly Mode = "external-only"
	ModePhase1Only   Mode = "phase1-only"
	ModeReportOnly   Mode = "report-only"
)

// Modes lists every run mode, in the order help output shows them.
func Modes() []Mode { return []Mode{ModeFull, ModeExternalOnly, ModePhase1Only, ModeReportOnly} }

func (m Mode) String() string { return string(m) }

// ReadOnly reports whether the mode forbids modifying the repository, which is
// also what caps every loop at a single pass: with no fixes applied, a second
// iteration would have nothing new to verify.
func (m Mode) ReadOnly() bool { return m == ModeReportOnly }

// step is one entry in a mode's phase sequence. Every step receives the results
// of the phases that already ran, which is how the final phase knows whether
// anything was changed that could have regressed.
type step func(ctx context.Context, e *phase.Env, prior ...phase.Result) phase.Result

// sequence maps a run mode to the phases it runs. Report-only runs the same
// phases as a full run; what differs is that they may not fix anything, and
// that finalize is never reached.
func sequence(m Mode) []step {
	comprehensive := func(ctx context.Context, e *phase.Env, _ ...phase.Result) phase.Result {
		return phase.Comprehensive(ctx, e)
	}
	external := func(ctx context.Context, e *phase.Env, _ ...phase.Result) phase.Result {
		return phase.External(ctx, e)
	}
	switch m {
	case ModePhase1Only:
		return []step{comprehensive}
	case ModeExternalOnly:
		return []step{external, phase.Final}
	default:
		return []step{comprehensive, external, phase.Final}
	}
}

// Runner executes one run: the phase sequence its mode selects, the optional
// finalize step, and the findings report a read-only run produces.
type Runner struct {
	Env  *phase.Env
	Mode Mode
}

// Result is the outcome of a whole run.
type Result struct {
	Mode Mode
	// Phases are the review phases the mode selected, in the order they ran,
	// including the ones that were skipped.
	Phases []phase.Result
	// Finalize is the finalize step's result, nil when the mode never reaches
	// it. Its failure never affects Converged.
	Finalize *phase.Result
	// Findings are the verified findings the phases reported, deduplicated.
	Findings []phase.Finding
	// ReportPath is where the findings report was written; empty when the mode
	// writes none.
	ReportPath string
	// Converged reports that every phase that ran left nothing outstanding.
	Converged bool
	// Err is the failure that ended the run: a phase that could not complete,
	// or a findings report that could not be written.
	Err error
}

// PhaseNames lists the phases the run stepped through, in order, whether they
// ran or were skipped.
func (r Result) PhaseNames() []string {
	names := make([]string, 0, len(r.Phases))
	for _, res := range r.Phases {
		names = append(names, res.Name)
	}
	return names
}

// Executed lists only the phases that actually invoked an executor.
func (r Result) Executed() []string {
	names := make([]string, 0, len(r.Phases))
	for _, res := range r.Phases {
		if !res.Skipped {
			names = append(names, res.Name)
		}
	}
	return names
}

// Run drives the pipeline to completion and reports what happened. A phase that
// fails outright ends the run; one that merely fails to converge does not, since
// a later phase can still be worth running against the branch as it stands.
func (r *Runner) Run(ctx context.Context) Result {
	mode := r.Mode
	if mode == "" {
		mode = ModeFull
	}
	if mode.ReadOnly() {
		// The no-mutation rule has to reach the executor through the prompts,
		// not just through rrev's own bookkeeping.
		r.Env.SinglePass = true
		r.Env.Vars.ModeRules = ReportOnlyRules
	}

	res := Result{Mode: mode, Converged: true}
	for _, run := range sequence(mode) {
		out := run(ctx, r.Env, res.Phases...)
		res.Phases = append(res.Phases, out)
		res.Findings = addFindings(res.Findings, out.Findings)
		if !out.OK() {
			res.Converged = false
		}
		if out.Err != nil {
			res.Err = out.Err
			break
		}
	}

	if len(res.Phases) > 0 && len(res.Executed()) == 0 {
		// A mode whose whole sequence was skipped reviewed nothing; reporting
		// convergence would be indistinguishable from a review that passed.
		res.Converged = false
	}
	if fin := r.runFinalize(ctx, res); fin != nil {
		res.Finalize = fin
		res.Findings = addFindings(res.Findings, fin.Findings)
	}
	if mode.ReadOnly() {
		r.writeReport(&res)
	}
	return res
}

// runFinalize runs the finalize step for the modes that carry a run through its
// last review phase. A run that ended on a failure never reaches it; the step
// itself decides whether the phases that ran justify rewriting the branch.
func (r *Runner) runFinalize(ctx context.Context, res Result) *phase.Result {
	if res.Mode == ModePhase1Only || res.Mode.ReadOnly() || res.Err != nil {
		return nil
	}
	out := phase.Finalize(ctx, r.Env, res.Phases...)
	return &out
}

// writeReport emits the findings report a read-only run exists to produce. A
// report that cannot be written is the run's failure: it is the only output the
// mode has.
func (r *Runner) writeReport(res *Result) {
	report := Report{
		Change:   r.Env.Vars.Change,
		Goal:     r.Env.Vars.Goal,
		BaseRef:  r.Env.Vars.BaseRef,
		Mode:     res.Mode,
		Findings: res.Findings,
	}
	path, err := report.Write(r.Env.Dir, r.Env.Config.ReportFile)
	if err != nil {
		if res.Err == nil {
			res.Err = err
		}
		r.note("%v", err)
		return
	}
	res.ReportPath = path
	r.note("wrote %s to %s", plural(len(res.Findings), "verified finding"), path)
}

// note narrates a run-level event and records it in the progress log.
func (r *Runner) note(format string, args ...any) {
	text := fmt.Sprintf(format, args...)
	if r.Env.Out != nil {
		// Losing narration is not worth failing a run for.
		_, _ = fmt.Fprintln(r.Env.Out, text)
	}
	r.Env.Log.Note(text)
}

// addFindings appends the findings a phase reported, dropping the ones an
// earlier phase already reported identically: the same issue surviving into the
// final pass is one finding, not two.
func addFindings(all, found []phase.Finding) []phase.Finding {
	for _, f := range found {
		if !slices.Contains(all, f) {
			all = append(all, f)
		}
	}
	return all
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
