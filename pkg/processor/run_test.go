package processor

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/korthane/rrev/pkg/config"
	"github.com/korthane/rrev/pkg/executor"
	"github.com/korthane/rrev/pkg/git"
	"github.com/korthane/rrev/pkg/processor/phase"
	"github.com/korthane/rrev/pkg/progress"
)

const (
	reviewDone   = "<<<RREV:REVIEW_DONE>>>"
	externalDone = "<<<RREV:EXTERNAL_DONE>>>"
)

// fakeRepo lets a test decide which iterations changed the branch.
type fakeRepo struct {
	mu   sync.Mutex
	head string
	tree string
}

func (r *fakeRepo) HeadHash(context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.head, nil
}

func (r *fakeRepo) WorkingTreeFingerprint(context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.tree, nil
}

// Commits reports one commit per hash the branch moved to, which is all the
// progress log needs to name what an iteration produced.
func (r *fakeRepo) Commits(_ context.Context, baseRef string) ([]git.Commit, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.head == baseRef {
		return nil, nil
	}
	return []git.Commit{{Hash: r.head, Subject: "fix " + r.head}}, nil
}

func (r *fakeRepo) commit(hash string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.head = hash
}

func mock(tool string, outputs ...string) *executor.Mock {
	responses := make([]executor.Response, 0, len(outputs))
	for _, out := range outputs {
		responses = append(responses, executor.Response{Output: out})
	}
	return &executor.Mock{Tool: tool, Responses: responses}
}

func newEnv(t *testing.T, primary, external executor.Executor, tune func(*config.Config)) (*phase.Env, *fakeRepo) {
	t.Helper()
	resolved, err := config.Resolve(config.Options{UserDir: t.TempDir(), ProjectDir: t.TempDir()})
	if err != nil {
		t.Fatalf("resolve config: %v", err)
	}
	if tune != nil {
		tune(resolved.Config)
	}
	repo := &fakeRepo{head: "head0", tree: "tree0"}
	env := &phase.Env{
		Dir:      t.TempDir(),
		Repo:     repo,
		Log:      progress.Disabled(),
		Config:   resolved.Config,
		Assets:   resolved.Assets,
		Vars:     config.Vars{Change: "add-user-auth", Goal: "add authentication", BaseRef: "main", DiffInstruction: "git diff main...HEAD"},
		Primary:  primary,
		External: external,
		Out:      &bytes.Buffer{},
	}
	return env, repo
}

func TestRunnerPhaseSequencePerMode(t *testing.T) {
	tests := []struct {
		name     string
		mode     Mode
		phases   []string
		executed []string
	}{
		{"full", ModeFull, []string{phase.NameComprehensive, phase.NameExternal, phase.NameFinal}, []string{phase.NameComprehensive, phase.NameExternal}},
		{"default is full", "", []string{phase.NameComprehensive, phase.NameExternal, phase.NameFinal}, []string{phase.NameComprehensive, phase.NameExternal}},
		{"external only", ModeExternalOnly, []string{phase.NameExternal, phase.NameFinal}, []string{phase.NameExternal}},
		{"first phase only", ModePhase1Only, []string{phase.NameComprehensive}, []string{phase.NameComprehensive}},
		{"report only", ModeReportOnly, []string{phase.NameComprehensive, phase.NameExternal, phase.NameFinal}, []string{phase.NameComprehensive, phase.NameExternal}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			primary := mock("claude", reviewDone)
			external := mock("codex", externalDone)
			env, _ := newEnv(t, primary, external, nil)
			runner := &Runner{Env: env, Mode: tt.mode}

			res := runner.Run(context.Background())

			if got := res.PhaseNames(); !slices.Equal(got, tt.phases) {
				t.Errorf("phase sequence = %v, want %v", got, tt.phases)
			}
			if got := res.Executed(); !slices.Equal(got, tt.executed) {
				t.Errorf("executed phases = %v, want %v", got, tt.executed)
			}
			if !res.Converged {
				t.Errorf("run = %+v, want convergence", res)
			}
			if res.Err != nil {
				t.Errorf("err = %v, want none", res.Err)
			}
		})
	}
}

func TestRunnerFinalPhaseRunsAfterFixes(t *testing.T) {
	primary := mock("claude", "FINDING: major | a.go:3 | quality | - | leaks a handle", reviewDone)
	external := mock("codex", externalDone)
	env, repo := newEnv(t, primary, external, nil)
	primary.Handler = func(_ context.Context, req executor.Request) (executor.Result, error) {
		if req.Phase == phase.NameComprehensive && primary.CallCount() == 1 {
			repo.commit("head1")
			out := "FINDING: major | a.go:3 | quality | - | leaks a handle"
			return executor.Result{Output: out, Signal: executor.Detect(out)}, nil
		}
		return executor.Result{Output: reviewDone, Signal: executor.SignalReviewDone}, nil
	}

	res := (&Runner{Env: env, Mode: ModeFull}).Run(context.Background())

	if got := res.Executed(); !slices.Equal(got, []string{phase.NameComprehensive, phase.NameExternal, phase.NameFinal}) {
		t.Errorf("executed phases = %v, want the final regression pass to run", got)
	}
	if len(res.Findings) != 1 || res.Findings[0].File != "a.go" {
		t.Errorf("findings = %+v, want the one reported by the comprehensive phase", res.Findings)
	}
}

func TestRunnerStopsOnPhaseFailure(t *testing.T) {
	boom := errors.New("boom")
	primary := &executor.Mock{Tool: "claude", Responses: []executor.Response{{Err: boom}}}
	external := mock("codex", externalDone)
	env, _ := newEnv(t, primary, external, func(c *config.Config) { c.Finalize = true })

	res := (&Runner{Env: env, Mode: ModeFull}).Run(context.Background())

	if got := res.PhaseNames(); !slices.Equal(got, []string{phase.NameComprehensive}) {
		t.Errorf("phase sequence = %v, want the run to stop at the failing phase", got)
	}
	if !errors.Is(res.Err, boom) {
		t.Errorf("err = %v, want it to wrap %v", res.Err, boom)
	}
	if res.Converged {
		t.Error("a failed phase must not report convergence")
	}
	if res.Finalize != nil {
		t.Errorf("finalize = %+v, want it never reached", res.Finalize)
	}
}

func TestRunnerNonConvergenceIsReported(t *testing.T) {
	primary := mock("claude", "still fixing")
	external := mock("codex", externalDone)
	env, repo := newEnv(t, primary, external, func(c *config.Config) { c.MaxIterations = 2 })
	primary.Handler = func(_ context.Context, _ executor.Request) (executor.Result, error) {
		repo.commit("head" + strings.Repeat("x", primary.CallCount()))
		return executor.Result{Output: "still fixing"}, nil
	}

	res := (&Runner{Env: env, Mode: ModePhase1Only}).Run(context.Background())

	if res.Converged {
		t.Error("a loop that hit its iteration limit must not report convergence")
	}
	if res.Err != nil {
		t.Errorf("err = %v, want none: non-convergence is not a failure", res.Err)
	}
}

func TestReportOnlyRunsOnePassPerPhaseAndWritesReport(t *testing.T) {
	primary := mock("claude", "FINDING: critical | pkg/auth.go:42 | conformance | Change selection | the flag is never parsed")
	external := mock("codex", "FINDING: minor | pkg/auth.go:9 | codex | - | naming")
	env, _ := newEnv(t, primary, external, func(c *config.Config) {
		c.MaxIterations = 5
		c.Finalize = true
	})

	res := (&Runner{Env: env, Mode: ModeReportOnly}).Run(context.Background())

	if res.Err != nil {
		t.Fatalf("err = %v, want none", res.Err)
	}
	// One comprehensive pass plus one evaluation of the external tool's report.
	if primary.CallCount() != 2 {
		t.Errorf("primary calls = %d, want 2: one per phase that ran, with no second iteration", primary.CallCount())
	}
	if external.CallCount() != 1 {
		t.Errorf("external calls = %d, want 1", external.CallCount())
	}
	if res.Finalize != nil {
		t.Errorf("finalize = %+v, want it skipped: it would modify the repository", res.Finalize)
	}
	if !res.Converged {
		t.Error("a single-pass report run must not be reported as non-convergence")
	}

	for _, call := range primary.Calls() {
		if !strings.Contains(call.Prompt, "Run mode: report-only") {
			t.Errorf("the %s prompt does not carry the report-only rules", call.Phase)
		}
		if !strings.Contains(call.Prompt, "do NOT stage, commit, or push") {
			t.Errorf("the %s prompt does not forbid committing", call.Phase)
		}
	}

	want := filepath.Join(env.Dir, ".rrev", "findings.md")
	if res.ReportPath != want {
		t.Fatalf("report path = %q, want %q", res.ReportPath, want)
	}
	body := readFile(t, res.ReportPath)
	for _, field := range []string{"pkg/auth.go:42", "critical", "conformance", "Change selection", "the flag is never parsed"} {
		if !strings.Contains(body, field) {
			t.Errorf("report is missing %q\n%s", field, body)
		}
	}
}

func TestReportOnlyReportsWhenNothingFound(t *testing.T) {
	primary := mock("claude", reviewDone)
	external := mock("codex", externalDone)
	env, _ := newEnv(t, primary, external, nil)

	res := (&Runner{Env: env, Mode: ModeReportOnly}).Run(context.Background())

	if res.ReportPath == "" {
		t.Fatal("report-only must write a report even when nothing was found")
	}
	if body := readFile(t, res.ReportPath); !strings.Contains(body, "No verified findings.") {
		t.Errorf("report = %q, want it to say nothing was found", body)
	}
}

func TestReportOnlyFailsWhenReportCannotBeWritten(t *testing.T) {
	primary := mock("claude", reviewDone)
	env, _ := newEnv(t, primary, nil, func(c *config.Config) { c.ReportFile = "" })

	res := (&Runner{Env: env, Mode: ModeReportOnly}).Run(context.Background())

	if res.Err == nil {
		t.Fatal("a report-only run that produced no report must report a failure")
	}
	if res.ReportPath != "" {
		t.Errorf("report path = %q, want none", res.ReportPath)
	}
}

func TestFindingsDeduplicatedAcrossPhases(t *testing.T) {
	finding := "FINDING: major | a.go:3 | quality | - | leaks a handle"
	primary := mock("claude", finding)
	external := mock("codex", externalDone)
	env, _ := newEnv(t, primary, external, func(c *config.Config) { c.MaxIterations = 2 })

	res := (&Runner{Env: env, Mode: ModeFull}).Run(context.Background())

	if len(res.Findings) != 1 {
		t.Errorf("findings = %+v, want the repeated one recorded once", res.Findings)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(body)
}

// A mode whose whole sequence is skipped reviewed nothing. Reporting that as
// convergence would be indistinguishable from a review that passed, which is
// what anything gating on rrev's exit status reads it as.
func TestRunnerAllPhasesSkippedDoesNotConverge(t *testing.T) {
	primary := mock("claude", reviewDone)
	env, _ := newEnv(t, primary, nil, nil)
	runner := &Runner{Env: env, Mode: ModeExternalOnly}

	res := runner.Run(context.Background())

	if got := res.Executed(); len(got) != 0 {
		t.Fatalf("executed phases = %v, want none with external review disabled", got)
	}
	if res.Converged {
		t.Error("a run that never invoked an executor reported convergence")
	}
}
