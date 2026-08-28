package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/korthane/rrev/pkg/config"
	"github.com/korthane/rrev/pkg/processor"
)

// prepareIn parses a command line and runs preflight against dir, which is how
// every startup path is reached in production.
func prepareIn(t *testing.T, dir string, args ...string) (*startup, error) {
	t.Helper()
	opts, err := parseArgs(args, io.Discard)
	if err != nil {
		return nil, err
	}
	return prepare(context.Background(), opts, dir)
}

func TestPrepareAutoDetectsSingleChange(t *testing.T) {
	repo := newFixtureRepo(t, "add-user-auth")
	fakeBin(t, "claude", "codex")

	start, err := prepareIn(t, repo)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if start.Review.Change.Name != "add-user-auth" {
		t.Errorf("change = %q", start.Review.Change.Name)
	}
	if start.BaseRef != "main" {
		t.Errorf("base ref = %q, want the detected default branch", start.BaseRef)
	}
	if start.Mode != processor.ModeFull {
		t.Errorf("mode = %q", start.Mode)
	}
	if len(start.Review.Requirements) != 1 || start.Review.ScenarioCount() != 1 {
		t.Errorf("extracted %d requirements and %d scenarios, want 1 and 1",
			len(start.Review.Requirements), start.Review.ScenarioCount())
	}
	if start.Primary.Name() != config.ExecutorClaude || start.External.Name() != config.ExternalToolCodex {
		t.Errorf("executors = %q and %q", start.Primary.Name(), start.External.Name())
	}
}

func TestPrepareNamedChange(t *testing.T) {
	repo := newFixtureRepo(t, "add-user-auth", "add-billing")
	fakeBin(t, "claude", "codex")

	start, err := prepareIn(t, repo, "add-billing")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if start.Review.Change.Name != "add-billing" {
		t.Errorf("change = %q", start.Review.Change.Name)
	}
}

func TestPrepareAmbiguousChange(t *testing.T) {
	repo := newFixtureRepo(t, "add-user-auth", "add-billing")
	marker := fakeBin(t, "claude", "codex")

	_, err := prepareIn(t, repo)
	if err == nil {
		t.Fatal("prepare: want an error when the change is ambiguous")
	}
	for _, want := range []string{"add-user-auth", "add-billing"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q, want it to list %q", err, want)
		}
	}
	assertNoExecutorRan(t, marker)
}

func TestPrepareUnknownChange(t *testing.T) {
	repo := newFixtureRepo(t, "add-user-auth")
	marker := fakeBin(t, "claude", "codex")

	_, err := prepareIn(t, repo, "add-billing")
	if err == nil {
		t.Fatal("prepare: want an error for an unknown change")
	}
	for _, want := range []string{"add-billing", "add-user-auth"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q, want it to name the change and list the available ones", err)
		}
	}
	assertNoExecutorRan(t, marker)
}

func TestPrepareBaseRefOverride(t *testing.T) {
	repo := newFixtureRepo(t, "add-user-auth")
	fakeBin(t, "claude", "codex")
	runGit(t, repo, "branch", "develop", "main")

	start, err := prepareIn(t, repo, "--base-ref", "develop")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if start.BaseRef != "develop" {
		t.Errorf("base ref = %q, want the override", start.BaseRef)
	}
}

func TestPrepareFlagBeatsConfig(t *testing.T) {
	repo := newFixtureRepo(t, "add-user-auth")
	fakeBin(t, "claude", "codex")
	writeFile(t, repo, ".rrev/config.ini", "review_model = sonnet\nfinal_model = haiku\n")

	start, err := prepareIn(t, repo, "--review-model", "opus:high")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if start.Config.ReviewModel != "opus:high" {
		t.Errorf("review model = %q, want the flag value", start.Config.ReviewModel)
	}
	if start.Config.FinalModel != "haiku" {
		t.Errorf("final model = %q, want the configured value left alone", start.Config.FinalModel)
	}
}

func TestPrepareInvalidOverrideValue(t *testing.T) {
	repo := newFixtureRepo(t, "add-user-auth")
	marker := fakeBin(t, "claude", "codex")

	_, err := prepareIn(t, repo, "--external-review-tool", "gemini")
	if err == nil {
		t.Fatal("prepare: want an error for an unrecognized external review tool")
	}
	want := []string{"--external-review-tool", `"gemini"`}
	want = append(want, config.Allowed("external_review_tool")...)
	for _, part := range want {
		if !strings.Contains(err.Error(), part) {
			t.Errorf("error %q, want it to mention %q", err, part)
		}
	}
	assertNoExecutorRan(t, marker)
}

func TestPrepareConflictingFlags(t *testing.T) {
	repo := newFixtureRepo(t, "add-user-auth")
	marker := fakeBin(t, "claude", "codex")

	// codex reviewing its own work is the conflict the config layer rejects
	// outright when the user asked for both sides of it.
	_, err := prepareIn(t, repo, "--executor", "codex", "--external-review-tool", "codex")
	if err == nil {
		t.Fatal("prepare: want an error when codex is both executor and external reviewer")
	}
	assertNoExecutorRan(t, marker)
}

// Selecting codex as the primary executor must skip the external phase, not
// abort the run: the default external tool is codex, and rrev's own default is
// not a contradiction the user typed.
func TestPrepareCodexPrimarySkipsExternalReview(t *testing.T) {
	repo := newFixtureRepo(t, "add-user-auth")
	fakeBin(t, "claude", "codex")

	start, err := prepareIn(t, repo, "--executor", "codex")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if start.External != nil {
		t.Errorf("external reviewer = %q, want the external phase disabled", start.External.Name())
	}
	if !slices.ContainsFunc(start.Warnings, func(w string) bool { return strings.Contains(w, "external_review_tool") }) {
		t.Errorf("warnings = %v, want the overridden setting named", start.Warnings)
	}
}

func TestPrepareNotAGitRepository(t *testing.T) {
	marker := fakeBin(t, "claude", "codex")

	_, err := prepareIn(t, t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "git repository is required") {
		t.Fatalf("prepare: %v, want a git repository error", err)
	}
	assertNoExecutorRan(t, marker)
}

func TestPrepareUnresolvableBaseRef(t *testing.T) {
	repo := newFixtureRepo(t, "add-user-auth")
	marker := fakeBin(t, "claude", "codex")

	_, err := prepareIn(t, repo, "--base-ref", "nope")
	if err == nil {
		t.Fatal("prepare: want an error for an unresolvable base ref")
	}
	for _, want := range []string{`"nope"`, "command line", "--base-ref"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q, want it to mention %q", err, want)
		}
	}
	assertNoExecutorRan(t, marker)
}

func TestPrepareMissingExecutorBinary(t *testing.T) {
	repo := newFixtureRepo(t, "add-user-auth")
	marker := fakeBin(t, "codex")

	_, err := prepareIn(t, repo)
	if err == nil {
		t.Fatal("prepare: want an error when the primary executor is missing")
	}
	for _, want := range []string{"claude", "defaults"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q, want it to name the missing command and how it was configured", err)
		}
	}
	assertNoExecutorRan(t, marker)
}

func TestPrepareMissingExternalBinaryNamesItsSource(t *testing.T) {
	repo := newFixtureRepo(t, "add-user-auth")
	marker := fakeBin(t, "claude")
	writeFile(t, repo, ".rrev/config.ini", "codex_command = codex-cli\n")

	_, err := prepareIn(t, repo)
	if err == nil {
		t.Fatal("prepare: want an error when the external review tool is missing")
	}
	for _, want := range []string{"codex-cli", "config.ini"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q, want it to mention %q", err, want)
		}
	}
	assertNoExecutorRan(t, marker)
}

// --phase1-only never reaches the external phase, so demanding its binary would
// block a claude-only user from a mode that cannot invoke codex.
func TestPrepareSkipsTheExternalBinaryInPhase1Only(t *testing.T) {
	repo := newFixtureRepo(t, "add-user-auth")
	marker := fakeBin(t, "claude")

	start, err := prepareIn(t, repo, "--phase1-only")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if start.Mode != processor.ModePhase1Only {
		t.Errorf("mode = %q, want %q", start.Mode, processor.ModePhase1Only)
	}
	assertNoExecutorRan(t, marker)
}

func TestPrepareExternalReviewDisabled(t *testing.T) {
	repo := newFixtureRepo(t, "add-user-auth")
	fakeBin(t, "claude")

	start, err := prepareIn(t, repo, "--external-review-tool", "none")
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if start.External != nil {
		t.Errorf("external executor = %v, want none", start.External)
	}
}

// TestPrepareReportsDegradedMode covers the openspec CLI being absent: the run
// still resolves, and says how it did.
func TestPrepareReportsDegradedMode(t *testing.T) {
	repo := newFixtureRepo(t, "add-user-auth")
	fakeBin(t, "claude", "codex")

	start, err := prepareIn(t, repo)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if !start.Review.Degraded || len(start.Warnings) == 0 {
		t.Errorf("degraded = %v, warnings = %v, want the missing openspec CLI reported",
			start.Review.Degraded, start.Warnings)
	}
}

func TestPreparePromptReferencingMissingAgent(t *testing.T) {
	repo := newFixtureRepo(t, "add-user-auth")
	marker := fakeBin(t, "claude", "codex")
	writeFile(t, repo, ".rrev/prompts/review_first.txt", "Review {{CHANGE}}.\n{{AGENTS: conformance, nosuchagent}}\n")

	_, err := prepareIn(t, repo)
	if err == nil {
		t.Fatal("prepare: want a startup error for a prompt naming an agent that resolves to no file")
	}
	for _, want := range []string{"review_first", "nosuchagent"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q, want it to name %q", err, want)
		}
	}
	assertNoExecutorRan(t, marker)
}

func TestPreparePromptWithUnknownVariable(t *testing.T) {
	repo := newFixtureRepo(t, "add-user-auth")
	marker := fakeBin(t, "claude", "codex")
	writeFile(t, repo, ".rrev/prompts/review_final.txt", "Regression pass for {{NOT_A_VARIABLE}}.\n")

	_, err := prepareIn(t, repo)
	if err == nil {
		t.Fatal("prepare: want a startup error for a prompt using an unknown variable")
	}
	if !strings.Contains(err.Error(), "NOT_A_VARIABLE") {
		t.Errorf("error %q, want it to name the variable", err)
	}
	assertNoExecutorRan(t, marker)
}

// TestPreparePromptCheckSkipsPhasesTheModeNeverRuns keeps preflight from
// failing a run over a prompt its mode never expands.
func TestPreparePromptCheckSkipsPhasesTheModeNeverRuns(t *testing.T) {
	repo := newFixtureRepo(t, "add-user-auth")
	fakeBin(t, "claude", "codex")
	writeFile(t, repo, ".rrev/prompts/review_final.txt", "Regression pass for {{NOT_A_VARIABLE}}.\n")

	if _, err := prepareIn(t, repo, "--phase1-only"); err != nil {
		t.Fatalf("prepare: %v", err)
	}
}

// rrev writes a catch-all ignore rule into its progress directory, so a value
// that resolves onto the repository root would ignore the whole repository.
func TestPrepareRejectsProgressDirAtTheRepositoryRoot(t *testing.T) {
	repo := newFixtureRepo(t, "add-user-auth")
	marker := fakeBin(t, "claude", "codex")
	writeFile(t, repo, ".rrev/config.ini", "progress_dir = .rrev/..\n")

	_, err := prepareIn(t, repo)
	if err == nil {
		t.Fatal("prepare: want an error when the progress directory is the repository root")
	}
	for _, want := range []string{"progress_dir", "config.ini"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q, want it to mention %q", err, want)
		}
	}
	assertNoExecutorRan(t, marker)
}

// The executor runs with the repository root as its working directory, so a
// relative command must be looked up there rather than in rrev's own.
func TestPrepareResolvesARelativeCommandAgainstTheRepositoryRoot(t *testing.T) {
	repo := newFixtureRepo(t, "add-user-auth")
	fakeBin(t, "claude")
	writeFile(t, repo, "tools/review.sh", "#!/bin/sh\nexit 0\n")
	if err := os.Chmod(filepath.Join(repo, "tools", "review.sh"), 0o700); err != nil { //nolint:gosec // test fixture
		t.Fatalf("chmod stand-in: %v", err)
	}
	writeFile(t, repo, ".rrev/config.ini",
		"external_review_tool = custom\nexternal_review_command = ./tools/review.sh\n")

	if _, err := prepareIn(t, repo); err != nil {
		t.Fatalf("prepare: %v", err)
	}
}
