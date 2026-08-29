package phase

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/korthane/rrev/pkg/config"
	"github.com/korthane/rrev/pkg/executor"
	"github.com/korthane/rrev/pkg/git"
	"github.com/korthane/rrev/pkg/progress"
)

// fakeRepo stands in for the repository, so a test can decide exactly which
// iterations changed something.
type fakeRepo struct {
	mu   sync.Mutex
	head string
	tree string
	err  error
}

func (r *fakeRepo) HeadHash(context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.head, r.err
}

func (r *fakeRepo) WorkingTreeFingerprint(context.Context) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.tree, r.err
}

// Commits reports one commit per hash the branch moved to, which is all the
// progress log needs to name what an iteration produced.
func (r *fakeRepo) Commits(_ context.Context, baseRef string) ([]git.Commit, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.head == baseRef {
		return nil, r.err
	}
	return []git.Commit{{Hash: r.head, Subject: "fix " + r.head}}, r.err
}

func (r *fakeRepo) commit(hash string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.head = hash
}

func newEnv(t *testing.T, primary, external executor.Executor, tune func(*config.Config)) (*Env, *fakeRepo) {
	t.Helper()
	resolved, err := config.Resolve(config.Options{UserDir: t.TempDir(), ProjectDir: t.TempDir()})
	if err != nil {
		t.Fatalf("resolve config: %v", err)
	}
	if tune != nil {
		tune(resolved.Config)
	}
	repo := &fakeRepo{head: "head0", tree: "tree0"}
	env := &Env{
		Dir:      t.TempDir(),
		Repo:     repo,
		Log:      progress.Disabled(),
		Config:   resolved.Config,
		Assets:   resolved.Assets,
		Vars:     config.Vars{Change: "add-user-auth", BaseRef: "main", DiffInstruction: "git diff main...HEAD"},
		Primary:  primary,
		External: external,
		Out:      &bytes.Buffer{},
	}
	return env, repo
}

func mock(tool string, outputs ...string) *executor.Mock {
	responses := make([]executor.Response, 0, len(outputs))
	for _, out := range outputs {
		responses = append(responses, executor.Response{Output: out})
	}
	return &executor.Mock{Tool: tool, Responses: responses}
}

const (
	reviewDone   = "<<<RREV:REVIEW_DONE>>>"
	externalDone = "<<<RREV:EXTERNAL_DONE>>>"
	taskFailed   = "<<<RREV:TASK_FAILED>>>"
)

// TestTransientFailureRetriesTheIteration covers the retryable classification:
// a blip the tool itself calls transient must not end a run that still has
// iterations left.
func TestTransientFailureRetriesTheIteration(t *testing.T) {
	primary := &executor.Mock{Tool: "claude", Responses: []executor.Response{
		{Err: &executor.LimitError{Tool: "claude", Reason: "service unavailable", Retryable: true}},
		{Output: reviewDone},
	}}
	env, _ := newEnv(t, primary, nil, nil)

	res := Comprehensive(context.Background(), env)

	if res.Reason != ReasonConverged || res.Err != nil {
		t.Fatalf("result = %+v, want a converged phase", res)
	}
	if res.Iterations != 1 {
		t.Errorf("iterations = %d, want the retry to reuse iteration 1", res.Iterations)
	}
	if primary.CallCount() != 2 {
		t.Errorf("executor calls = %d, want the failed call retried once", primary.CallCount())
	}
}

// TestPersistentTransientFailureEndsTheLoop pins the other side of the retry:
// a tool that keeps failing still ends the phase instead of spinning.
func TestPersistentTransientFailureEndsTheLoop(t *testing.T) {
	primary := &executor.Mock{Tool: "claude", Responses: []executor.Response{
		{Err: &executor.LimitError{Tool: "claude", Reason: "service unavailable", Retryable: true}},
	}}
	env, _ := newEnv(t, primary, nil, nil)

	res := Comprehensive(context.Background(), env)

	if res.Reason != ReasonFailure || res.Err == nil {
		t.Fatalf("result = %+v, want the phase to fail", res)
	}
	if primary.CallCount() != retryBudget+1 {
		t.Errorf("executor calls = %d, want %d", primary.CallCount(), retryBudget+1)
	}
}

func TestComprehensiveConverges(t *testing.T) {
	primary := mock("claude", "nothing to fix\n"+reviewDone)
	env, _ := newEnv(t, primary, nil, nil)

	res := Comprehensive(context.Background(), env)

	if res.Reason != ReasonConverged {
		t.Fatalf("reason = %q, want %q", res.Reason, ReasonConverged)
	}
	if res.Iterations != 1 {
		t.Errorf("iterations = %d, want 1", res.Iterations)
	}
	if !res.OK() {
		t.Error("converged phase should report OK")
	}
	if res.Changed {
		t.Error("no commit was made, so the phase must not report a change")
	}
	if primary.CallCount() != 1 {
		t.Errorf("executor calls = %d, want 1", primary.CallCount())
	}
}

func TestComprehensivePromptLaunchesAgentsConcurrently(t *testing.T) {
	primary := mock("claude", reviewDone)
	env, _ := newEnv(t, primary, nil, nil)

	Comprehensive(context.Background(), env)

	calls := primary.Calls()
	if len(calls) != 1 {
		t.Fatalf("executor calls = %d, want 1", len(calls))
	}
	prompt := calls[0].Prompt
	if !strings.Contains(prompt, "single message") {
		t.Error("prompt does not instruct the executor to launch the agents in one message")
	}
	for _, agent := range []string{"conformance", "tasks", "quality", "implementation", "testing", "simplification", "documentation"} {
		if !strings.Contains(prompt, "<<<AGENT "+agent) {
			t.Errorf("prompt is missing the %s agent definition", agent)
		}
	}
	if calls[0].Phase != NameComprehensive {
		t.Errorf("request phase = %q, want %q", calls[0].Phase, NameComprehensive)
	}
}

func TestComprehensiveIterationLimit(t *testing.T) {
	primary := mock("claude", "fixed something")
	env, repo := newEnv(t, primary, nil, func(c *config.Config) { c.MaxIterations = 3 })
	primary.Handler = changingHandler(primary, repo)

	res := Comprehensive(context.Background(), env)

	if res.Reason != ReasonIterationLimit {
		t.Fatalf("reason = %q, want %q", res.Reason, ReasonIterationLimit)
	}
	if res.Iterations != 3 {
		t.Errorf("iterations = %d, want 3", res.Iterations)
	}
	if res.OK() {
		t.Error("a loop that hit its iteration limit must not report OK")
	}
	if !res.Changed {
		t.Error("iterations that committed must be reported as changing the branch")
	}
}

func TestComprehensiveConvergesAfterFixes(t *testing.T) {
	primary := mock("claude", "FINDING: major | a.go:12 | quality | - | leaks a file handle", reviewDone)
	env, repo := newEnv(t, primary, nil, nil)
	primary.Handler = changingHandler(primary, repo)

	res := Comprehensive(context.Background(), env)

	if res.Reason != ReasonConverged {
		t.Fatalf("reason = %q, want %q", res.Reason, ReasonConverged)
	}
	if res.Iterations != 2 {
		t.Errorf("iterations = %d, want 2", res.Iterations)
	}
	if len(res.Findings) != 1 || res.Findings[0].File != "a.go" || res.Findings[0].Line != 12 {
		t.Errorf("findings = %+v, want one at a.go:12", res.Findings)
	}
}

func TestLoopEndsOnExecutorFailure(t *testing.T) {
	boom := errors.New("boom")
	primary := &executor.Mock{Tool: "claude", Responses: []executor.Response{{Err: boom}}}
	env, _ := newEnv(t, primary, nil, nil)

	res := Comprehensive(context.Background(), env)

	if res.Reason != ReasonFailure {
		t.Fatalf("reason = %q, want %q", res.Reason, ReasonFailure)
	}
	if !errors.Is(res.Err, boom) {
		t.Errorf("err = %v, want it to wrap %v", res.Err, boom)
	}
	if res.Iterations != 1 {
		t.Errorf("iterations = %d, want 1", res.Iterations)
	}
}

func TestLoopEndsOnFailureSignal(t *testing.T) {
	primary := mock("claude", "cannot obtain the diff\n"+taskFailed)
	env, _ := newEnv(t, primary, nil, nil)

	res := Comprehensive(context.Background(), env)

	if res.Reason != ReasonFailure {
		t.Fatalf("reason = %q, want %q", res.Reason, ReasonFailure)
	}
	if res.Err == nil || !strings.Contains(res.Err.Error(), taskFailed) {
		t.Errorf("err = %v, want it to name the failure signal", res.Err)
	}
}

func TestLoopEndsOnAbort(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	primary := mock("claude", "fixed something")
	env, _ := newEnv(t, primary, nil, nil)

	res := Comprehensive(ctx, env)

	if res.Reason != ReasonAborted {
		t.Fatalf("reason = %q, want %q", res.Reason, ReasonAborted)
	}
}

func TestStalemateEndsLoop(t *testing.T) {
	primary := mock("claude", "still working on it")
	env, _ := newEnv(t, primary, nil, func(c *config.Config) {
		c.MaxIterations = 10
		c.StalematePatience = 2
	})

	res := Comprehensive(context.Background(), env)

	if res.Reason != ReasonStalemate {
		t.Fatalf("reason = %q, want %q", res.Reason, ReasonStalemate)
	}
	if res.Iterations != 2 {
		t.Errorf("iterations = %d, want 2", res.Iterations)
	}
}

func TestStalematePatienceDisabled(t *testing.T) {
	primary := mock("claude", "still working on it")
	env, _ := newEnv(t, primary, nil, func(c *config.Config) {
		c.MaxIterations = 3
		c.StalematePatience = 0
	})

	res := Comprehensive(context.Background(), env)

	if res.Reason != ReasonIterationLimit {
		t.Fatalf("reason = %q, want %q", res.Reason, ReasonIterationLimit)
	}
	if res.Iterations != 3 {
		t.Errorf("iterations = %d, want 3", res.Iterations)
	}
}

func TestStalemateCounterResetsOnProgress(t *testing.T) {
	primary := mock("claude", "still working on it")
	env, repo := newEnv(t, primary, nil, func(c *config.Config) {
		c.MaxIterations = 10
		c.StalematePatience = 2
	})
	primary.Handler = func(_ context.Context, _ executor.Request) (executor.Result, error) {
		if primary.CallCount() == 2 {
			repo.commit("head1")
		}
		return executor.Result{Output: "still working on it"}, nil
	}

	res := Comprehensive(context.Background(), env)

	if res.Reason != ReasonStalemate {
		t.Fatalf("reason = %q, want %q", res.Reason, ReasonStalemate)
	}
	if res.Iterations != 4 {
		t.Errorf("iterations = %d, want 4: the commit in iteration 2 resets the counter", res.Iterations)
	}
}

func TestUnknownRepositoryStateNeverStalemates(t *testing.T) {
	primary := mock("claude", "still working on it")
	env, repo := newEnv(t, primary, nil, func(c *config.Config) {
		c.MaxIterations = 3
		c.StalematePatience = 1
	})
	repo.err = errors.New("git unavailable")

	res := Comprehensive(context.Background(), env)

	if res.Reason != ReasonIterationLimit {
		t.Fatalf("reason = %q, want %q", res.Reason, ReasonIterationLimit)
	}
}

func TestSinglePassRunsOneIteration(t *testing.T) {
	primary := mock("claude", "FINDING: minor | a.go:1 | quality | - | naming")
	env, _ := newEnv(t, primary, nil, func(c *config.Config) { c.MaxIterations = 5 })
	env.SinglePass = true

	res := Comprehensive(context.Background(), env)

	if res.Reason != ReasonSinglePass {
		t.Fatalf("reason = %q, want %q", res.Reason, ReasonSinglePass)
	}
	if res.Iterations != 1 || primary.CallCount() != 1 {
		t.Errorf("iterations = %d, calls = %d, want 1 and 1", res.Iterations, primary.CallCount())
	}
	if !res.OK() {
		t.Error("a report-only pass must not be reported as non-convergence")
	}
}

func TestSinglePassLogsFindingsAsNotFixed(t *testing.T) {
	dir := t.TempDir()
	log, err := progress.Open(dir, "add-user-auth", progress.Options{})
	if err != nil {
		t.Fatalf("open progress log: %v", err)
	}
	primary := mock("claude", "FINDING: critical | pkg/a.go:42 | conformance | Change selection | the flag is never parsed")
	env, _ := newEnv(t, primary, nil, nil)
	env.Log = log
	env.SinglePass = true

	Comprehensive(context.Background(), env)

	body := readFile(t, log.Path())
	if !strings.Contains(body, "— reported; not fixed (report-only)") {
		t.Errorf("progress log claims a fix a report-only run cannot make\n%s", body)
	}
}

// changingHandler makes every iteration look like it committed something, so a
// loop that would otherwise be a stalemate runs to its own terminating
// condition.
func changingHandler(m *executor.Mock, repo *fakeRepo) func(context.Context, executor.Request) (executor.Result, error) {
	return func(_ context.Context, _ executor.Request) (executor.Result, error) {
		n := m.CallCount()
		repo.commit("head" + strings.Repeat("x", n))
		outputs := m.Responses
		out := outputs[min(n, len(outputs))-1]
		return executor.Result{Output: out.Output, Signal: executor.Detect(out.Output)}, out.Err
	}
}

func TestProgressLogRecordsFindingsAndTermination(t *testing.T) {
	dir := t.TempDir()
	log, err := progress.Open(dir, "add-user-auth", progress.Options{})
	if err != nil {
		t.Fatalf("open progress log: %v", err)
	}
	primary := mock("claude",
		"FINDING: critical | pkg/a.go:42 | conformance | Change selection | the flag is never parsed\n"+
			"REJECTED: pkg/b.go:7 | quality | the nil check is done by the caller",
		reviewDone)
	env, repo := newEnv(t, primary, nil, nil)
	env.Log = log
	primary.Handler = changingHandler(primary, repo)

	res := Comprehensive(context.Background(), env)

	body := readFile(t, log.Path())
	for _, want := range []string{
		"## Phase: comprehensive",
		"### comprehensive · iteration 1/10 ·",
		"- **confirmed** `R1` critical `pkg/a.go:42` (Change selection) — conformance",
		"the flag is never parsed",
		"- **rejected** `R2` `pkg/b.go:7` — quality",
		"the nil check is done by the caller",
		"**comprehensive ended:** converged after 2 iteration(s)",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("progress log is missing %q\n%s", want, body)
		}
	}
	if len(res.Rejections) != 1 || res.Rejections[0].Reason == "" {
		t.Errorf("rejections = %+v, want one carrying a reason", res.Rejections)
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

// fakeStreamer records which phase each stream was opened for.
type fakeStreamer struct {
	mu      sync.Mutex
	byPhase map[string]*bytes.Buffer
}

func (s *fakeStreamer) Stream(phase string) io.Writer {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byPhase == nil {
		s.byPhase = map[string]*bytes.Buffer{}
	}
	buf, ok := s.byPhase[phase]
	if !ok {
		buf = &bytes.Buffer{}
		s.byPhase[phase] = buf
	}
	return buf
}

func (s *fakeStreamer) text(phase string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if buf, ok := s.byPhase[phase]; ok {
		return buf.String()
	}
	return ""
}

// TestStreamIsAttributedToItsPhase covers the contract the terminal output
// depends on: activity reaches the stream opened for the phase that produced it.
func TestStreamIsAttributedToItsPhase(t *testing.T) {
	env, _ := newEnv(t, mock("claude", "checking the diff\n"+reviewDone), nil, nil)
	streams := &fakeStreamer{}
	env.Stream = streams

	Comprehensive(context.Background(), env)

	if got := streams.text(NameComprehensive); !strings.Contains(got, "checking the diff") {
		t.Errorf("the comprehensive stream got %q", got)
	}
	if got := streams.text(NameExternal); got != "" {
		t.Errorf("a phase that never ran got output: %q", got)
	}
}

// The commits an iteration produced are the one part of its outcome rrev can
// observe for itself, so the progress log must name them.
func TestIterationCommitsRecorded(t *testing.T) {
	dir := t.TempDir()
	log, err := progress.Open(dir, "add-user-auth", progress.Options{})
	if err != nil {
		t.Fatalf("open progress log: %v", err)
	}

	primary := mock("claude", "FINDING: major | a.go:3 | quality | - | leaks a handle", reviewDone)
	env, repo := newEnv(t, primary, nil, func(c *config.Config) { c.MaxIterations = 2 })
	env.Log = log
	primary.Handler = func(_ context.Context, _ executor.Request) (executor.Result, error) {
		if primary.CallCount() == 1 {
			repo.commit("head1")
			out := "FINDING: major | a.go:3 | quality | - | leaks a handle"
			return executor.Result{Output: out, Signal: executor.Detect(out)}, nil
		}
		return executor.Result{Output: reviewDone, Signal: executor.SignalReviewDone}, nil
	}

	Comprehensive(context.Background(), env)

	logged, err := os.ReadFile(filepath.Join(dir, "progress-add-user-auth.md"))
	if err != nil {
		t.Fatalf("read progress log: %v", err)
	}
	if !strings.Contains(string(logged), "- commit `head1`") {
		t.Errorf("progress log does not name the commit the iteration produced:\n%s", logged)
	}
}

// A finding reported without a reviewer is attributed to the executor that
// reported it, and that attribution has to reach the run's result: it is the
// source column of the findings report.
func TestFindingWithoutReviewerIsAttributedInTheResult(t *testing.T) {
	primary := mock("claude", "FINDING: major | a.go:1 |  | - | missing reviewer", reviewDone)
	env, _ := newEnv(t, primary, nil, nil)

	res := Comprehensive(context.Background(), env)

	if len(res.Findings) != 1 {
		t.Fatalf("findings = %+v, want exactly one", res.Findings)
	}
	if res.Findings[0].Reviewer != "claude" {
		t.Errorf("reviewer = %q, want the reporting executor", res.Findings[0].Reviewer)
	}
}

// The ledger only earns its keep if it reaches the next iteration's prompt.
// Nothing else in the suite covers the wire from the log to the reviewer, and a
// broken one fails silently: reviewers just keep re-arguing settled questions.
func TestStandingRejectionsReachTheNextIterationsPrompt(t *testing.T) {
	primary := mock("claude",
		"REJECTED: pkg/a.go:7 | quality | the token is echoed | the value is not key material",
		reviewDone)
	env, _ := newEnv(t, primary, nil, nil)
	log, err := progress.Open(t.TempDir(), "add-user-auth", progress.Options{})
	if err != nil {
		t.Fatalf("open progress log: %v", err)
	}
	env.Log = log

	Comprehensive(context.Background(), env)

	calls := primary.Calls()
	if len(calls) != 2 {
		t.Fatalf("executor calls = %d, want 2", len(calls))
	}
	if strings.Contains(calls[0].Prompt, "R1") {
		t.Error("the first iteration has nothing settled yet, so its ledger must be empty")
	}
	for _, want := range []string{"R1", "pkg/a.go:7", "the value is not key material"} {
		if !strings.Contains(calls[1].Prompt, want) {
			t.Errorf("the second iteration's prompt is missing %q from the ledger", want)
		}
	}
}

// A dead or rate-limited external tool that logged as a clean pass is the exact
// confusion the recorded-activity requirement exists to remove.
func TestExternalToolFailureIsRecordedWithItsCause(t *testing.T) {
	external := &executor.Mock{Tool: "codex", Responses: []executor.Response{
		{Err: errors.New("codex exited with status 1")},
	}}
	env, _ := newEnv(t, mock("claude", reviewDone), external, nil)
	dir := t.TempDir()
	log, err := progress.Open(dir, "add-user-auth", progress.Options{})
	if err != nil {
		t.Fatalf("open progress log: %v", err)
	}
	env.Log = log

	res := External(context.Background(), env)

	if res.Reason != ReasonFailure {
		t.Errorf("reason = %q, want the phase to fail rather than read as converged", res.Reason)
	}
	data, err := os.ReadFile(log.Path())
	if err != nil {
		t.Fatalf("read progress log: %v", err)
	}
	for _, want := range []string{"external tool `codex`: failed", "codex exited with status 1"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("progress log missing %q:\n%s", want, data)
		}
	}
}
