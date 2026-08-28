package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"

	"github.com/korthane/rrev/pkg/config"
	"github.com/korthane/rrev/pkg/executor"
	"github.com/korthane/rrev/pkg/openspec"
	"github.com/korthane/rrev/pkg/processor"
	"github.com/korthane/rrev/pkg/processor/phase"
	"github.com/korthane/rrev/pkg/progress"
	"github.com/korthane/rrev/pkg/status"
)

// launch turns a completed preflight into a finished run: the progress log, the
// phase environment, the signal handlers, and the status the process exits with.
func launch(ctx context.Context, start *startup, out, errOut io.Writer) status.Code {
	printer := status.New(out, status.UseColor(out, start.Config.NoColor))
	defer printer.Flush()

	log := openLog(start, errOut)
	log.RunStart(progress.RunInfo{
		Change:  start.Review.Change.Name,
		Goal:    start.Review.Goal,
		BaseRef: start.BaseRef,
		Head:    headHash(ctx, start),
		Mode:    start.Mode.String(),
	})
	printer.Say("%s", banner(start, log))

	runCtx, brk, stop := listen(ctx)
	defer stop()

	env := &phase.Env{
		Dir:      start.Repo.Root(),
		Repo:     start.Repo,
		Log:      log,
		Config:   start.Config,
		Assets:   start.Assets,
		Vars:     runVars(start, log),
		Primary:  start.Primary,
		External: start.External,
		Stream:   printer,
		Out:      printer.Plain(),
		Break:    brk,
	}
	res := (&processor.Runner{Env: env, Mode: start.Mode}).Run(runCtx)

	if runCtx.Err() != nil {
		// The executors are already gone with their process groups; what is
		// left is to say so in both places a reader looks.
		log.Note("run aborted on interrupt")
		printer.Say("aborted on interrupt")
		return status.CodeFailed
	}
	printer.Say("%s", status.Summary(res))
	return status.Outcome(res)
}

// listen installs the run's signal handlers. An abort cancels the context,
// which is what terminates the executor's process group; a break only closes
// the channel the external review loop watches. The returned function arms the
// break for one loop, and is nil where the platform has no break signal.
func listen(ctx context.Context) (context.Context, func() <-chan struct{}, func()) {
	runCtx, stopAbort := signal.NotifyContext(ctx, os.Interrupt)

	sig, _, ok := status.BreakSignal()
	if !ok {
		return runCtx, nil, stopAbort
	}
	received := make(chan os.Signal, 1)
	signal.Notify(received, sig)
	brk, done := &breaker{ch: make(chan struct{})}, make(chan struct{})
	go func() {
		for {
			select {
			case <-received:
				brk.fire()
			case <-done:
				return
			}
		}
	}()
	return runCtx, brk.arm, func() {
		signal.Stop(received)
		close(done)
		stopAbort()
	}
}

// breaker delivers break signals to whichever review loop is running. It hands
// out a fresh channel per loop and replaces it on every signal, so a break sent
// outside a loop is discarded instead of latching the key dead for the rest of
// the run.
type breaker struct {
	mu sync.Mutex
	ch chan struct{}
}

// arm returns the channel the next break closes, dropping any break that
// already fired.
func (b *breaker) arm() <-chan struct{} {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ch
}

// fire ends the armed loop and re-arms the key for the next one.
func (b *breaker) fire() {
	b.mu.Lock()
	defer b.mu.Unlock()
	close(b.ch)
	b.ch = make(chan struct{})
}

// openLog opens the progress log, reporting a log that cannot be written rather
// than failing the run: losing the history is not worth losing the review.
func openLog(start *startup, errOut io.Writer) *progress.Log {
	warn := func(text string) {
		// A terminal that cannot be written to is not worth failing a run for.
		_, _ = fmt.Fprintln(errOut, "rrev:", text)
	}
	log, err := progress.Open(absDir(start.Repo.Root(), start.Config.ProgressDir),
		start.Review.Change.Name, progress.Options{Warn: warn})
	if err != nil {
		warn(err.Error() + "; continuing without a progress log")
	}
	return log
}

// banner describes the run before its first phase.
func banner(start *startup, log *progress.Log) status.Banner {
	_, breakHint, _ := status.BreakSignal()
	b := status.Banner{
		Version:      version,
		Change:       start.Review.GoalLine(),
		BaseRef:      start.BaseRef,
		DiffCommand:  diffInstruction(start.BaseRef),
		Mode:         start.Mode.String(),
		Primary:      start.Primary.Name(),
		Models:       models(start),
		Requirements: len(start.Review.Requirements),
		Scenarios:    start.Review.ScenarioCount(),
		BreakHint:    breakHint,
	}
	if start.External != nil {
		b.External = start.External.Name()
	}
	if log.Enabled() {
		b.ProgressLog = repoPath(start.Repo.Root(), log.Path())
	}
	return b
}

// models resolves what each phase will actually run with, which is the only
// place the inheritance between the model settings becomes visible.
func models(start *startup) []status.Model {
	type phaseTool struct {
		phase executor.Phase
		tool  string
	}
	primary := start.Primary.Name()
	phases := []phaseTool{{executor.PhaseReview, primary}, {executor.PhaseFinal, primary}}
	if start.External != nil {
		phases = append(phases, phaseTool{executor.PhaseExternal, start.External.Name()})
	}
	if start.Config.Finalize {
		phases = append(phases, phaseTool{executor.PhaseFinalize, primary})
	}

	resolved := make([]status.Model, 0, len(phases))
	for _, p := range phases {
		spec, _ := executor.Select(start.Config, p.phase, p.tool)
		resolved = append(resolved, status.Model{Phase: string(p.phase), Spec: spec.String()})
	}
	return resolved
}

// runVars are the template values every phase shares. Paths are given relative
// to the repository root, since that is where the executor runs.
func runVars(start *startup, log *progress.Log) config.Vars {
	review, root := start.Review, start.Repo.Root()
	vars := config.Vars{
		Change:            review.Change.Name,
		Goal:              review.Goal,
		GoalLine:          review.GoalLine(),
		BaseRef:           start.BaseRef,
		DiffInstruction:   diffInstruction(start.BaseRef),
		ReportFile:        start.Config.ReportFile,
		ValidationCommand: start.Config.ValidationCommand,
		OpenSpecDir:       repoPath(root, review.Root.SpecDir()),
		ChangeDir:         repoPath(root, review.Change.Dir),
		Requirements:      openspec.ChecklistEntries(review.Requirements),
		ChecklistBudget:   start.Config.ChecklistBudget,
	}
	if log.Enabled() {
		vars.ProgressLog = repoPath(root, log.Path())
	}
	arts := review.Artifacts
	for _, art := range []struct {
		src *openspec.Artifact
		dst *string
	}{{arts.Proposal, &vars.Proposal}, {arts.Design, &vars.Design}, {arts.Tasks, &vars.Tasks}} {
		if art.src != nil {
			*art.dst = repoPath(root, filepath.Join(review.Root.Dir, art.src.Path))
		}
	}
	for _, spec := range arts.Specs {
		vars.Specs = append(vars.Specs, repoPath(root, filepath.Join(review.Root.Dir, spec.Path)))
	}
	for _, spec := range review.UnparsedSpecs {
		vars.UnparsedSpecs = append(vars.UnparsedSpecs, repoPath(root, filepath.Join(review.Root.Dir, spec)))
	}
	return vars
}

func diffInstruction(baseRef string) string { return "git diff " + baseRef + "...HEAD" }

// repoPath renders a path the way a reviewer would type it: relative to the
// repository root, or absolute when it lies outside.
func repoPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return path
	}
	return filepath.ToSlash(rel)
}

// absDir resolves a configured directory against the repository root, so a
// relative setting means the same thing wherever rrev was started from.
func absDir(root, dir string) string {
	if filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(root, dir)
}

func headHash(ctx context.Context, start *startup) string {
	head, err := start.Repo.HeadHash(ctx)
	if err != nil {
		return ""
	}
	return head
}
