package phase

import (
	"context"
	"fmt"
	"io"

	"github.com/korthane/rrev/pkg/config"
	"github.com/korthane/rrev/pkg/executor"
	"github.com/korthane/rrev/pkg/git"
	"github.com/korthane/rrev/pkg/progress"
)

// Phase names, used for terminal attribution, the executor request, and the
// progress log.
const (
	NameComprehensive = "comprehensive"
	NameExternal      = "external"
	NameFinal         = "final"
	NameFinalize      = "finalize"
)

// Prompt asset names each phase expands.
const (
	PromptComprehensive = "review_first"
	PromptExternal      = "external_review"
	PromptExternalEval  = "external_eval"
	PromptFinal         = "review_final"
	PromptFinalize      = "finalize"
)

// Reason is the condition that ended a loop. Every loop reports one.
type Reason string

// Terminating conditions.
const (
	ReasonConverged      Reason = "converged"
	ReasonIterationLimit Reason = "iteration limit reached"
	ReasonStalemate      Reason = "stalemate"
	ReasonFailure        Reason = "executor failure"
	ReasonUserBreak      Reason = "user break"
	ReasonAborted        Reason = "aborted"
	ReasonSinglePass     Reason = "single pass"
	ReasonSkipped        Reason = "skipped"
)

// Repo is the part of the repository a phase needs: enough to tell an iteration
// that changed something from one that spun in place, and to name the commits
// it produced.
type Repo interface {
	HeadHash(ctx context.Context) (string, error)
	WorkingTreeFingerprint(ctx context.Context) (string, error)
	Commits(ctx context.Context, baseRef string) ([]git.Commit, error)
}

// Result is what one phase did, and why it stopped.
type Result struct {
	Name       string
	Reason     Reason
	Iterations int
	// Changed reports whether any iteration produced a commit or a working-tree
	// change, which is what decides whether a later regression pass is needed.
	Changed bool
	// Skipped marks a phase that never ran; SkipReason says why, in the words
	// shown to the user.
	Skipped    bool
	SkipReason string

	Findings   []Finding
	Rejections []Rejection

	Err error
}

// OK reports whether the phase left nothing outstanding: it converged, was
// skipped, or ran the single pass report-only mode allows.
func (r Result) OK() bool {
	return r.Skipped || r.Reason == ReasonConverged || r.Reason == ReasonSinglePass
}

// Streamer routes an executor's activity to a destination that attributes it to
// the phase it came from.
type Streamer interface {
	Stream(phase string) io.Writer
}

// Env is everything the phases share for one run: the resolved configuration
// and review context, the executors, and where to report.
type Env struct {
	// Dir is the repository the executors run in.
	Dir  string
	Repo Repo
	Log  *progress.Log

	Config *config.Config
	Assets config.Assets
	// Vars are the run-wide template values; each phase overlays only the
	// per-iteration ones.
	Vars config.Vars

	// Primary runs the review phases and applies fixes; External is the
	// independent second opinion, nil when external review is disabled.
	Primary  executor.Executor
	External executor.Executor

	// Stream routes executor activity, attributed to the phase that produced
	// it; nil discards it.
	Stream Streamer
	// Out receives rrev's own phase-level narration; nil discards it.
	Out io.Writer

	// Break ends the external review loop at the next iteration boundary.
	Break <-chan struct{}
	// SinglePass caps every loop at one iteration, which is what report-only
	// mode needs: with no fixes applied there is nothing for a second pass to
	// verify.
	SinglePass bool
}

// stream is where one phase's executor activity goes; nil discards it.
func (e *Env) stream(phase string) io.Writer {
	if e.Stream == nil {
		return nil
	}
	return e.Stream.Stream(phase)
}

// say narrates a phase-level event to the user and returns the same text, so a
// caller can hand it to the progress log too.
func (e *Env) say(format string, args ...any) string {
	text := fmt.Sprintf(format, args...)
	if e.Out != nil {
		// Losing narration is not worth failing a review for.
		_, _ = fmt.Fprintln(e.Out, text)
	}
	return text
}

// note narrates and records the same text.
func (e *Env) note(format string, args ...any) {
	e.Log.Note(e.say(format, args...))
}

// skip builds the result for a phase that never ran, reporting why.
func (e *Env) skip(name, format string, args ...any) Result {
	reason := fmt.Sprintf(format, args...)
	e.note("%s skipped: %s", Label(name), reason)
	return Result{Name: name, Reason: ReasonSkipped, Skipped: true, SkipReason: reason}
}

// Label names a step the way it is described to the user: the review phases are
// loops, the finalize step is not. The run summary uses it too, so the closing
// report and the narration that preceded it cannot drift apart.
func Label(name string) string {
	if name == NameFinalize {
		return NameFinalize + " step"
	}
	return name + " review"
}
