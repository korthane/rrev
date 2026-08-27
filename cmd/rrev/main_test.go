package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/korthane/rrev/pkg/status"
)

func TestRunVersion(t *testing.T) {
	var out strings.Builder
	if code := run(context.Background(), []string{"--version"}, &out, io.Discard); code != status.CodeOK {
		t.Fatalf("code = %d, want %d", code, status.CodeOK)
	}
	if got := out.String(); !strings.HasPrefix(got, "rrev ") {
		t.Errorf("got %q, want it to start with %q", got, "rrev ")
	}
}

func TestRunHelp(t *testing.T) {
	var out, errOut strings.Builder
	if code := run(context.Background(), []string{"-h"}, &out, &errOut); code != status.CodeOK {
		t.Fatalf("code = %d, want %d", code, status.CodeOK)
	}
	if !strings.Contains(errOut.String(), "usage: rrev") {
		t.Errorf("usage output = %q", errOut.String())
	}
}

// TestRunBanner covers what the banner has to state before the first phase: a
// wrong change, base ref, mode, executor, or model must be visible there.
func TestRunBanner(t *testing.T) {
	repo := newFixtureRepo(t, "add-user-auth")
	fakeBin(t, "claude", "codex")
	t.Chdir(repo)

	var out strings.Builder
	code := run(context.Background(), []string{"add-user-auth", "--report-only", "--review-model", "opus:high"}, &out, io.Discard)
	if code != status.CodeOK {
		t.Fatalf("code = %d, want %d; output:\n%s", code, status.CodeOK, out.String())
	}
	for _, want := range []string{
		"add-user-auth", "main", "git diff main...HEAD", "report-only",
		"claude (primary)", "codex (external review)", "review opus:high",
		"1 requirement", "1 scenario", ".rrev/progress/",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("banner is missing %q; output:\n%s", want, out.String())
		}
	}
}

// TestRunUnconvergedExitStatus covers the status that separates a review that
// ran out of iterations from one that found nothing left to fix.
func TestRunUnconvergedExitStatus(t *testing.T) {
	repo := newFixtureRepo(t, "add-user-auth")
	fakeBin(t, "claude", "codex")
	t.Chdir(repo)

	var out strings.Builder
	code := run(context.Background(), []string{"--phase1-only", "--max-iterations", "1"}, &out, io.Discard)
	if code != status.CodeUnconverged {
		t.Fatalf("code = %d, want %d; output:\n%s", code, status.CodeUnconverged, out.String())
	}
	for _, want := range []string{"comprehensive review did not converge", "iteration limit reached"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("summary is missing %q; output:\n%s", want, out.String())
		}
	}
}

// TestRunConvergedExitStatus covers the clean case: the executor emits the
// review-done signal and the run exits zero.
func TestRunConvergedExitStatus(t *testing.T) {
	repo := newFixtureRepo(t, "add-user-auth")
	signalBin(t, "claude", "<<<RREV:REVIEW_DONE>>>")
	t.Chdir(repo)

	var out strings.Builder
	code := run(context.Background(), []string{"--phase1-only"}, &out, io.Discard)
	if code != status.CodeOK {
		t.Fatalf("code = %d, want %d; output:\n%s", code, status.CodeOK, out.String())
	}
	if !strings.Contains(out.String(), "run converged") {
		t.Errorf("summary is missing the convergence line; output:\n%s", out.String())
	}
}

func TestRunPreflightFailureExitsFailed(t *testing.T) {
	repo := newFixtureRepo(t, "add-user-auth")
	marker := fakeBin(t, "claude", "codex")
	t.Chdir(repo)

	var errOut strings.Builder
	code := run(context.Background(), []string{"--base-ref", "nope"}, io.Discard, &errOut)
	if code != status.CodeFailed {
		t.Fatalf("code = %d, want %d", code, status.CodeFailed)
	}
	if !strings.Contains(errOut.String(), "rrev:") {
		t.Errorf("a failed preflight must say why; got %q", errOut.String())
	}
	assertNoExecutorRan(t, marker)
}

// TestLaunchAbortedExitsFailed covers the abort path: a cancelled run reports
// the interrupt, records it in the progress log, and exits non-zero.
func TestLaunchAbortedExitsFailed(t *testing.T) {
	repo := newFixtureRepo(t, "add-user-auth")
	fakeBin(t, "claude", "codex")
	t.Chdir(repo)

	opts, err := parseArgs([]string{"--phase1-only"}, io.Discard)
	if err != nil {
		t.Fatalf("parse args: %v", err)
	}
	start, err := prepare(context.Background(), opts, repo)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var out strings.Builder
	if code := launch(ctx, start, &out, io.Discard); code != status.CodeFailed {
		t.Fatalf("code = %d, want %d; output:\n%s", code, status.CodeFailed, out.String())
	}
	if !strings.Contains(out.String(), "aborted on interrupt") {
		t.Errorf("an aborted run must say so; output:\n%s", out.String())
	}
	assertLogged(t, repo, "run aborted on interrupt")
}

// TestRunNoColorLeavesOutputPlain guards the flag against a printer that colours
// output regardless: escape sequences in a redirected log are unreadable.
func TestRunNoColorLeavesOutputPlain(t *testing.T) {
	repo := newFixtureRepo(t, "add-user-auth")
	fakeBin(t, "claude", "codex")
	t.Chdir(repo)

	var out strings.Builder
	run(context.Background(), []string{"--phase1-only", "--max-iterations", "1", "--no-color"}, &out, io.Discard)
	if strings.Contains(out.String(), "\x1b[") {
		t.Errorf("output carries escape sequences: %q", out.String())
	}
}

// assertLogged fails unless the change's progress log recorded text.
func assertLogged(t *testing.T, repo, text string) {
	t.Helper()
	entries, err := filepath.Glob(filepath.Join(repo, ".rrev", "progress", "*.md"))
	if err != nil || len(entries) == 0 {
		t.Fatalf("no progress log was written: %v", err)
	}
	body, err := os.ReadFile(entries[0]) //nolint:gosec // the path is a test temp file
	if err != nil {
		t.Fatalf("read progress log: %v", err)
	}
	if !strings.Contains(string(body), text) {
		t.Errorf("progress log is missing %q:\n%s", text, body)
	}
}
