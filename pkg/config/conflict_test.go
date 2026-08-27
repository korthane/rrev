package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resolveWith writes a project configuration file and resolves it together with
// the given flags.
func resolveWith(t *testing.T, projectINI string, flags map[string]string) *Config {
	t.Helper()
	projectDir := filepath.Join(t.TempDir(), DirName)
	if projectINI != "" {
		if err := os.MkdirAll(projectDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(projectDir, FileName), []byte(projectINI), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	resolved, err := Resolve(Options{ProjectDir: projectDir, UserDir: t.TempDir(), Flags: flags})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return resolved.Config
}

func TestReconcileConflictingFlagsIsAStartupError(t *testing.T) {
	cfg := resolveWith(t, "", map[string]string{"executor": "codex", "external_review_tool": "codex"})

	warnings, err := cfg.Reconcile()
	var conflictErr *ConflictError
	if !errors.As(err, &conflictErr) {
		t.Fatalf("error = %v, want a *ConflictError", err)
	}
	if warnings != nil {
		t.Errorf("warnings = %v, want none alongside the error", warnings)
	}
	for _, want := range []string{"executor", "external_review_tool", "command line", "self-review"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if cfg.ExternalReviewTool != ExternalToolCodex {
		t.Errorf("external review tool = %q, want it left alone when the conflict is an error", cfg.ExternalReviewTool)
	}
}

func TestReconcileConflictOnlyInConfigWarnsAndOverrides(t *testing.T) {
	cfg := resolveWith(t, "executor = codex\nexternal_review_tool = codex\n", nil)

	warnings, err := cfg.Reconcile()
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly one", warnings)
	}
	if cfg.ExternalReviewTool != ExternalToolNone {
		t.Errorf("external review tool = %q, want the external phase disabled", cfg.ExternalReviewTool)
	}
	for _, want := range []string{"external_review_tool", FileName, "disabling the external review phase"} {
		if !strings.Contains(warnings[0], want) {
			t.Errorf("warning %q does not mention %q", warnings[0], want)
		}
	}
}

func TestReconcileFlagOnOneSideOfAFileConflictStillErrors(t *testing.T) {
	cfg := resolveWith(t, "external_review_tool = codex\n", map[string]string{"executor": "codex"})

	if _, err := cfg.Reconcile(); err == nil {
		t.Fatal("a flag half of the contradiction was resolved silently, want a startup error")
	}
}

func TestReconcileCustomExternalToolWithoutCommand(t *testing.T) {
	cfg := resolveWith(t, "external_review_tool = custom\n", nil)

	warnings, err := cfg.Reconcile()
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "external_review_command") {
		t.Fatalf("warnings = %v, want one naming the missing command", warnings)
	}
	if cfg.ExternalReviewTool != ExternalToolNone {
		t.Errorf("external review tool = %q, want the external phase disabled", cfg.ExternalReviewTool)
	}

	flagged := resolveWith(t, "", map[string]string{"external_review_tool": "custom"})
	if _, err := flagged.Reconcile(); err == nil {
		t.Fatal("the same contradiction from a flag was resolved silently, want a startup error")
	}
}

func TestReconcileDefaultsAreConsistent(t *testing.T) {
	cfg := resolveWith(t, "", nil)

	warnings, err := cfg.Reconcile()
	if err != nil || len(warnings) != 0 {
		t.Fatalf("defaults reconcile to warnings %v and error %v, want neither", warnings, err)
	}
}
