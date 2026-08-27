package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// isolateGit points git at throwaway config files so the developer's global
// settings cannot change what the tests see.
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

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
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

// changeArtifacts writes a minimal but complete OpenSpec change: a proposal
// with a Why section, a task list, and one delta spec with a scenario.
func changeArtifacts(t *testing.T, dir, name string) {
	t.Helper()
	base := filepath.Join("openspec", "changes", name)
	writeFile(t, dir, filepath.Join(base, "proposal.md"), "# "+name+"\n\n## Why\n\nUsers cannot sign in.\n")
	writeFile(t, dir, filepath.Join(base, "tasks.md"), "## 1. Auth\n\n- [x] 1.1 add the sign-in handler\n")
	writeFile(t, dir, filepath.Join(base, "specs", "auth", "spec.md"),
		"## ADDED Requirements\n\n### Requirement: Sign in\nThe system SHALL authenticate a user.\n\n"+
			"#### Scenario: Valid credentials\n- **WHEN** the password matches\n- **THEN** a session is created\n")
}

// newFixtureRepo builds a repository on a feature branch that adds a file, with
// one OpenSpec change per name to review it against.
func newFixtureRepo(t *testing.T, changes ...string) string {
	t.Helper()
	isolateGit(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	runGit(t, dir, "init", "-b", "main")
	runGit(t, dir, "config", "user.name", "rrev test")
	runGit(t, dir, "config", "user.email", "rrev-test@example.com")
	writeFile(t, dir, "base.txt", "base\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-m", "initial commit")

	runGit(t, dir, "checkout", "-b", "feature")
	for _, name := range changes {
		changeArtifacts(t, dir, name)
	}
	writeFile(t, dir, "auth.go", "package auth\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-m", "add the sign-in handler")
	return dir
}

// fakeBin replaces PATH with a directory holding only git and the named stand-in
// executables, so preflight sees exactly the tools a test set up and no real
// claude, codex, or openspec CLI can be reached. Each stand-in records that it
// ran in the returned marker file.
func fakeBin(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	marker := filepath.Join(dir, "invocations")

	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("git is required to run these tests: %v", err)
	}
	if err := os.Symlink(gitPath, filepath.Join(dir, "git")); err != nil {
		t.Fatalf("link git: %v", err)
	}
	for _, name := range names {
		script := fmt.Sprintf("#!/bin/sh\necho %q >> %q\n", name, marker)
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil { //nolint:gosec // test helper
			t.Fatalf("write %s stand-in: %v", name, err)
		}
	}
	t.Setenv("PATH", dir)
	return marker
}

func assertNoExecutorRan(t *testing.T, marker string) {
	t.Helper()
	if out, err := os.ReadFile(marker); err == nil { //nolint:gosec // the path is a test temp file
		t.Errorf("an executor ran during a failed preflight: %s", out)
	}
}
