package main

import (
	"context"
	"io"
	"strings"
	"testing"
)

func TestRunVersion(t *testing.T) {
	var out strings.Builder
	if err := run(context.Background(), []string{"--version"}, &out, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := out.String(); !strings.HasPrefix(got, "rrev ") {
		t.Errorf("got %q, want it to start with %q", got, "rrev ")
	}
}

func TestRunHelp(t *testing.T) {
	var out, errOut strings.Builder
	if err := run(context.Background(), []string{"-h"}, &out, &errOut); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(errOut.String(), "usage: rrev") {
		t.Errorf("usage output = %q", errOut.String())
	}
}

// TestRunReportsSelection covers the end of a successful preflight: the change,
// base ref, and mode are reported before any phase would start.
func TestRunReportsSelection(t *testing.T) {
	repo := newFixtureRepo(t, "add-user-auth")
	fakeBin(t, "claude", "codex")
	t.Chdir(repo)

	var out strings.Builder
	if err := run(context.Background(), []string{"add-user-auth", "--report-only"}, &out, io.Discard); err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, want := range []string{"add-user-auth", "main", "report-only"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output %q, want it to mention %q", out.String(), want)
		}
	}
}

func TestRunPreflightFailureRunsNoPhase(t *testing.T) {
	repo := newFixtureRepo(t, "add-user-auth")
	marker := fakeBin(t, "claude", "codex")
	t.Chdir(repo)

	err := run(context.Background(), []string{"--base-ref", "nope"}, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("run: want an error for an unresolvable base ref")
	}
	assertNoExecutorRan(t, marker)
}
