package git_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/korthane/rrev/pkg/git"
)

// isolateGit points git at throwaway config files so the developer's global
// settings (default branch name, identity) cannot change what the tests see.
func isolateGit(t *testing.T) {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(cfg, nil, 0o600); err != nil {
		t.Fatalf("write git config: %v", err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_CONFIG_SYSTEM", cfg)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimRight(string(out), "\n")
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func commit(t *testing.T, dir, subject string) {
	t.Helper()
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-m", subject)
}

// newRepo creates a repository with one commit on defaultBranch.
func newRepo(t *testing.T, defaultBranch string) string {
	t.Helper()
	isolateGit(t)
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", defaultBranch)
	runGit(t, dir, "config", "user.name", "rrev test")
	runGit(t, dir, "config", "user.email", "rrev-test@example.com")
	writeFile(t, dir, "base.txt", "base\n")
	commit(t, dir, "initial commit")
	return dir
}

// newBranchedRepo returns a repository on a feature branch that adds feature.txt,
// with an unrelated commit landed on the default branch after the branch point.
func newBranchedRepo(t *testing.T, defaultBranch string) string {
	t.Helper()
	dir := newRepo(t, defaultBranch)
	runGit(t, dir, "checkout", "-b", "feature")
	writeFile(t, dir, "feature.txt", "feature\n")
	commit(t, dir, "add feature")

	runGit(t, dir, "checkout", defaultBranch)
	writeFile(t, dir, "unrelated.txt", "unrelated\n")
	commit(t, dir, "unrelated work on the base branch")

	runGit(t, dir, "checkout", "feature")
	return dir
}

func open(t *testing.T, dir string) *git.Repo {
	t.Helper()
	repo, err := git.New(t.Context(), dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return repo
}

func TestNewRejectsNonRepository(t *testing.T) {
	isolateGit(t)
	_, err := git.New(t.Context(), t.TempDir())
	if !errors.Is(err, git.ErrNotRepository) {
		t.Fatalf("want ErrNotRepository, got %v", err)
	}
}

func TestNewResolvesRoot(t *testing.T) {
	dir := newRepo(t, "main")
	writeFile(t, dir, "nested/file.txt", "x\n")
	repo := open(t, filepath.Join(dir, "nested"))

	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	got, err := filepath.EvalSymlinks(repo.Root())
	if err != nil {
		t.Fatalf("eval symlinks: %v", err)
	}
	if got != want {
		t.Errorf("Root() = %q, want %q", got, want)
	}
}

func TestDefaultBranchConventionalNames(t *testing.T) {
	for _, branch := range []string{"main", "master"} {
		t.Run(branch, func(t *testing.T) {
			repo := open(t, newRepo(t, branch))
			got, err := repo.DefaultBranch(t.Context())
			if err != nil {
				t.Fatalf("DefaultBranch: %v", err)
			}
			if got != branch {
				t.Errorf("DefaultBranch() = %q, want %q", got, branch)
			}
		})
	}
}

func TestDefaultBranchFromRemoteHead(t *testing.T) {
	origin := newRepo(t, "trunk")
	clone := t.TempDir()
	runGit(t, clone, "clone", origin, ".")
	runGit(t, clone, "config", "user.name", "rrev test")
	runGit(t, clone, "config", "user.email", "rrev-test@example.com")
	runGit(t, clone, "checkout", "-b", "feature")

	repo := open(t, clone)
	got, err := repo.DefaultBranch(t.Context())
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	if got != "trunk" {
		t.Errorf("DefaultBranch() = %q, want %q", got, "trunk")
	}
}

func TestDefaultBranchFromInitConfig(t *testing.T) {
	dir := newRepo(t, "development")
	runGit(t, dir, "config", "init.defaultBranch", "development")
	runGit(t, dir, "checkout", "-b", "feature")

	got, err := open(t, dir).DefaultBranch(t.Context())
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	if got != "development" {
		t.Errorf("DefaultBranch() = %q, want %q", got, "development")
	}
}

func TestDefaultBranchUndetectable(t *testing.T) {
	dir := newRepo(t, "trunk")
	runGit(t, dir, "checkout", "-b", "feature")

	_, err := open(t, dir).DefaultBranch(t.Context())
	if !errors.Is(err, git.ErrNoDefaultBranch) {
		t.Fatalf("want ErrNoDefaultBranch, got %v", err)
	}
}

func TestDiffExcludesBaseBranchWork(t *testing.T) {
	repo := open(t, newBranchedRepo(t, "main"))

	diff, err := repo.Diff(t.Context(), "main")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(diff, "feature.txt") {
		t.Errorf("diff missing branch change:\n%s", diff)
	}
	if strings.Contains(diff, "unrelated.txt") {
		t.Errorf("three-dot diff leaked base branch work:\n%s", diff)
	}
}

func TestChangedFiles(t *testing.T) {
	repo := open(t, newBranchedRepo(t, "main"))

	files, err := repo.ChangedFiles(t.Context(), "main")
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}
	if len(files) != 1 || files[0] != "feature.txt" {
		t.Errorf("ChangedFiles() = %v, want [feature.txt]", files)
	}
}

func TestHasChangesOnChangedBranch(t *testing.T) {
	repo := open(t, newBranchedRepo(t, "main"))

	changed, err := repo.HasChanges(t.Context(), "main")
	if err != nil {
		t.Fatalf("HasChanges: %v", err)
	}
	if !changed {
		t.Error("HasChanges() = false, want true")
	}
	if err := repo.EnsureChanges(t.Context(), "main"); err != nil {
		t.Errorf("EnsureChanges: %v", err)
	}
}

func TestEmptyDiffReportsNothingToReview(t *testing.T) {
	dir := newRepo(t, "main")
	runGit(t, dir, "checkout", "-b", "feature")
	repo := open(t, dir)

	changed, err := repo.HasChanges(t.Context(), "main")
	if err != nil {
		t.Fatalf("HasChanges: %v", err)
	}
	if changed {
		t.Error("HasChanges() = true for a branch identical to its base")
	}

	err = repo.EnsureChanges(t.Context(), "main")
	if !errors.Is(err, git.ErrNoChanges) {
		t.Fatalf("want ErrNoChanges, got %v", err)
	}
	if !strings.Contains(err.Error(), "main") {
		t.Errorf("error should name the base ref, got %q", err)
	}
}

func TestCommitsOnBranch(t *testing.T) {
	dir := newBranchedRepo(t, "main")
	writeFile(t, dir, "feature.txt", "feature v2\n")
	commit(t, dir, "refine feature")
	repo := open(t, dir)

	commits, err := repo.Commits(t.Context(), "main")
	if err != nil {
		t.Fatalf("Commits: %v", err)
	}
	if len(commits) != 2 {
		t.Fatalf("Commits() returned %d commits, want 2", len(commits))
	}
	if commits[0].Subject != "add feature" || commits[1].Subject != "refine feature" {
		t.Errorf("commits not oldest first: %q, %q", commits[0].Subject, commits[1].Subject)
	}
	if commits[0].Author != "rrev test" {
		t.Errorf("Author = %q, want %q", commits[0].Author, "rrev test")
	}
	if commits[0].Date.IsZero() {
		t.Error("Date not parsed")
	}
	if len(commits[0].Hash) != 40 {
		t.Errorf("Hash = %q, want a full hash", commits[0].Hash)
	}
}

func TestCommitsEmptyForUnchangedBranch(t *testing.T) {
	dir := newRepo(t, "main")
	runGit(t, dir, "checkout", "-b", "feature")

	commits, err := open(t, dir).Commits(t.Context(), "main")
	if err != nil {
		t.Fatalf("Commits: %v", err)
	}
	if len(commits) != 0 {
		t.Errorf("Commits() = %v, want none", commits)
	}
}

func TestHeadHashMatchesRevParse(t *testing.T) {
	dir := newBranchedRepo(t, "main")

	got, err := open(t, dir).HeadHash(t.Context())
	if err != nil {
		t.Fatalf("HeadHash: %v", err)
	}
	if want := runGit(t, dir, "rev-parse", "HEAD"); got != want {
		t.Errorf("HeadHash() = %q, want %q", got, want)
	}
}

func TestRefExistsAndResolve(t *testing.T) {
	dir := newBranchedRepo(t, "main")
	repo := open(t, dir)

	if !repo.RefExists(t.Context(), "main") {
		t.Error("RefExists(main) = false")
	}
	if repo.RefExists(t.Context(), "no-such-ref") {
		t.Error("RefExists(no-such-ref) = true")
	}
	if repo.RefExists(t.Context(), "") {
		t.Error("RefExists(\"\") = true")
	}
	if _, err := repo.ResolveRef(t.Context(), "no-such-ref"); err == nil {
		t.Error("ResolveRef(no-such-ref) succeeded")
	}
	hash, err := repo.ResolveRef(t.Context(), "main")
	if err != nil {
		t.Fatalf("ResolveRef: %v", err)
	}
	if want := runGit(t, dir, "rev-parse", "main"); hash != want {
		t.Errorf("ResolveRef(main) = %q, want %q", hash, want)
	}
}

func TestWorkingTreeFingerprint(t *testing.T) {
	dir := newBranchedRepo(t, "main")
	repo := open(t, dir)

	clean, err := repo.WorkingTreeFingerprint(t.Context())
	if err != nil {
		t.Fatalf("WorkingTreeFingerprint: %v", err)
	}
	again, err := repo.WorkingTreeFingerprint(t.Context())
	if err != nil {
		t.Fatalf("WorkingTreeFingerprint: %v", err)
	}
	if clean != again {
		t.Error("fingerprint changed without any repository change")
	}

	writeFile(t, dir, "feature.txt", "edited\n")
	edited, err := repo.WorkingTreeFingerprint(t.Context())
	if err != nil {
		t.Fatalf("WorkingTreeFingerprint: %v", err)
	}
	if edited == clean {
		t.Error("fingerprint unchanged after editing a tracked file")
	}

	writeFile(t, dir, "scratch.txt", "new\n")
	withUntracked, err := repo.WorkingTreeFingerprint(t.Context())
	if err != nil {
		t.Fatalf("WorkingTreeFingerprint: %v", err)
	}
	if withUntracked == edited {
		t.Error("fingerprint unchanged after adding an untracked file")
	}
}

func TestCommandErrorIncludesStderr(t *testing.T) {
	repo := open(t, newRepo(t, "main"))

	_, err := repo.Diff(t.Context(), "no-such-ref")
	if err == nil {
		t.Fatal("Diff against a missing ref succeeded")
	}
	cmdErr, ok := errors.AsType[*git.CommandError](err)
	if !ok {
		t.Fatalf("want *git.CommandError, got %T: %v", err, err)
	}
	if cmdErr.Stderr == "" {
		t.Error("CommandError.Stderr is empty")
	}
	if !strings.Contains(err.Error(), "no-such-ref") {
		t.Errorf("error should name the ref, got %q", err)
	}
}

func TestContextCancellationStopsGit(t *testing.T) {
	repo := open(t, newRepo(t, "main"))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := repo.HeadHash(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

// TestChangedFilesFromSubdirectoryWithRelativeDiff pins the work tree the git
// commands run in. With diff.relative set, a diff issued from a subdirectory
// covers only that subdirectory, so a repository queried from anywhere but its
// root would report a branch with changes as having none.
func TestChangedFilesFromSubdirectoryWithRelativeDiff(t *testing.T) {
	dir := newBranchedRepo(t, "main")
	runGit(t, dir, "config", "diff.relative", "true")
	sub := filepath.Join(dir, "sub")
	writeFile(t, sub, "keep.txt", "keep\n")
	commit(t, dir, "add a subdirectory untouched by the branch")

	repo := open(t, sub)
	files, err := repo.ChangedFiles(t.Context(), "main")
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}
	if !slices.Contains(files, "feature.txt") {
		t.Errorf("ChangedFiles = %v, want the branch's file outside the subdirectory", files)
	}
	if err := repo.EnsureChanges(t.Context(), "main"); err != nil {
		t.Errorf("EnsureChanges: %v", err)
	}
}

// TestDefaultBranchPrefersRemoteWhenLocalIsBehind covers the everyday state of a
// fetched-but-not-pulled clone: diffing against the stale local branch would put
// the upstream commits the branch was rebased onto into the review.
func TestDefaultBranchPrefersRemoteWhenLocalIsBehind(t *testing.T) {
	origin := newRepo(t, "main")
	clone := t.TempDir()
	runGit(t, clone, "clone", origin, ".")
	runGit(t, clone, "config", "user.name", "rrev test")
	runGit(t, clone, "config", "user.email", "rrev-test@example.com")
	runGit(t, clone, "checkout", "-b", "feature")
	writeFile(t, clone, "feature.txt", "feature\n")
	commit(t, clone, "add feature")

	writeFile(t, origin, "upstream.txt", "upstream\n")
	commit(t, origin, "upstream work")
	runGit(t, clone, "fetch", "origin")
	runGit(t, clone, "rebase", "origin/main")

	repo := open(t, clone)
	got, err := repo.DefaultBranch(t.Context())
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	if got != "origin/main" {
		t.Fatalf("DefaultBranch() = %q, want %q", got, "origin/main")
	}
	files, err := repo.ChangedFiles(t.Context(), got)
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}
	if !slices.Equal(files, []string{"feature.txt"}) {
		t.Errorf("review diff leaked upstream work: %v", files)
	}
}

// TestDefaultBranchPrefersLocalWhenAhead guards the other direction: unpushed
// work on the default branch must not be reviewed as if it were the branch's.
func TestDefaultBranchPrefersLocalWhenAhead(t *testing.T) {
	origin := newRepo(t, "main")
	clone := t.TempDir()
	runGit(t, clone, "clone", origin, ".")
	runGit(t, clone, "config", "user.name", "rrev test")
	runGit(t, clone, "config", "user.email", "rrev-test@example.com")
	writeFile(t, clone, "local.txt", "local\n")
	commit(t, clone, "unpushed work on main")
	runGit(t, clone, "checkout", "-b", "feature")

	got, err := open(t, clone).DefaultBranch(t.Context())
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	if got != "main" {
		t.Errorf("DefaultBranch() = %q, want %q", got, "main")
	}
}

// TestDefaultBranchPrefersRemoteWhenDiverged covers a local default branch that
// carries unpushed work and is missing upstream work at the same time: the
// branch under review was cut from the remote, so only the remote-tracking ref
// gives a merge base that leaves upstream commits out of the diff.
func TestDefaultBranchPrefersRemoteWhenDiverged(t *testing.T) {
	origin := newRepo(t, "main")
	clone := t.TempDir()
	runGit(t, clone, "clone", origin, ".")
	runGit(t, clone, "config", "user.name", "rrev test")
	runGit(t, clone, "config", "user.email", "rrev-test@example.com")

	writeFile(t, origin, "upstream.txt", "upstream\n")
	commit(t, origin, "upstream work")
	runGit(t, clone, "fetch", "origin")

	writeFile(t, clone, "local.txt", "local\n")
	commit(t, clone, "unpushed work on main")

	runGit(t, clone, "checkout", "-b", "feature", "origin/main")
	writeFile(t, clone, "feature.txt", "feature\n")
	commit(t, clone, "add feature")

	repo := open(t, clone)
	got, err := repo.DefaultBranch(t.Context())
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	if got != "origin/main" {
		t.Fatalf("DefaultBranch() = %q, want %q", got, "origin/main")
	}
	files, err := repo.ChangedFiles(t.Context(), got)
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}
	if !slices.Equal(files, []string{"feature.txt"}) {
		t.Errorf("review diff leaked upstream work: %v", files)
	}
}

// TestDefaultBranchPrefersLocalWhenDivergedAndBranchedLocally is the mirror of
// TestDefaultBranchPrefersRemoteWhenDiverged: the same diverged default branch,
// but the branch under review was cut from the local one, so the remote-tracking
// ref would drag the unpushed default-branch commit into the review.
func TestDefaultBranchPrefersLocalWhenDivergedAndBranchedLocally(t *testing.T) {
	origin := newRepo(t, "main")
	clone := t.TempDir()
	runGit(t, clone, "clone", origin, ".")
	runGit(t, clone, "config", "user.name", "rrev test")
	runGit(t, clone, "config", "user.email", "rrev-test@example.com")

	writeFile(t, origin, "upstream.txt", "upstream\n")
	commit(t, origin, "upstream work")
	runGit(t, clone, "fetch", "origin")

	writeFile(t, clone, "local.txt", "local\n")
	commit(t, clone, "unpushed work on main")

	runGit(t, clone, "checkout", "-b", "feature")
	writeFile(t, clone, "feature.txt", "feature\n")
	commit(t, clone, "add feature")

	repo := open(t, clone)
	got, err := repo.DefaultBranch(t.Context())
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	if got != "main" {
		t.Fatalf("DefaultBranch() = %q, want %q", got, "main")
	}
	files, err := repo.ChangedFiles(t.Context(), got)
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}
	if !slices.Equal(files, []string{"feature.txt"}) {
		t.Errorf("review diff leaked default-branch work: %v", files)
	}
}

// TestDefaultBranchPrefersRemoteWhenLocalHasUnrelatedHistory covers an upstream
// history rewrite: the local default branch shares no commit with the branch
// under review, so only the remote-tracking ref yields a merge base at all.
func TestDefaultBranchPrefersRemoteWhenLocalHasUnrelatedHistory(t *testing.T) {
	origin := newRepo(t, "main")
	clone := t.TempDir()
	runGit(t, clone, "clone", origin, ".")
	runGit(t, clone, "config", "user.name", "rrev test")
	runGit(t, clone, "config", "user.email", "rrev-test@example.com")

	runGit(t, clone, "checkout", "-b", "feature", "origin/main")
	writeFile(t, clone, "feature.txt", "feature\n")
	commit(t, clone, "add feature")

	runGit(t, clone, "checkout", "--orphan", "rewritten")
	runGit(t, clone, "rm", "-rf", ".")
	writeFile(t, clone, "rewritten.txt", "rewritten\n")
	commit(t, clone, "rewritten history")
	runGit(t, clone, "branch", "-f", "main", "rewritten")
	runGit(t, clone, "checkout", "feature")

	repo := open(t, clone)
	got, err := repo.DefaultBranch(t.Context())
	if err != nil {
		t.Fatalf("DefaultBranch: %v", err)
	}
	if got != "origin/main" {
		t.Fatalf("DefaultBranch() = %q, want %q", got, "origin/main")
	}
	files, err := repo.ChangedFiles(t.Context(), got)
	if err != nil {
		t.Fatalf("ChangedFiles: %v", err)
	}
	if !slices.Equal(files, []string{"feature.txt"}) {
		t.Errorf("ChangedFiles = %v, want the branch's own file", files)
	}
}
