package status

import (
	"strings"
	"testing"
)

func fullBanner() Banner {
	return Banner{
		Version:     "1.2.3",
		Change:      "add-user-auth: let users sign in",
		BaseRef:     "main",
		DiffCommand: "git diff main...HEAD",
		Mode:        "full",
		Primary:     "claude",
		External:    "codex",
		Models: []Model{
			{Phase: "review", Spec: "opus:high"},
			{Phase: "external", Spec: "tool default"},
		},
		Requirements: 4,
		Scenarios:    1,
		ProgressLog:  ".rrev/progress/progress-add-user-auth.md",
		BreakHint:    `Ctrl+\`,
	}
}

func TestBannerReportsTheRun(t *testing.T) {
	got := fullBanner().String()
	for _, want := range []string{
		"rrev 1.2.3",
		"add-user-auth: let users sign in",
		"main (git diff main...HEAD)",
		"full",
		"claude (primary), codex (external review)",
		"review opus:high, external tool default",
		"4 requirements, 1 scenario",
		".rrev/progress/progress-add-user-auth.md",
		`Ctrl+C aborts the run, Ctrl+\ ends the external review loop`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("banner is missing %q:\n%s", want, got)
		}
	}
}

// TestBannerOmitsUnsupportedBreak covers the platform without a break signal:
// advertising a key that does nothing is worse than saying nothing.
func TestBannerOmitsUnsupportedBreak(t *testing.T) {
	b := fullBanner()
	b.BreakHint = ""

	got := b.String()
	if strings.Contains(got, "external review loop") {
		t.Errorf("banner offers a break the platform does not have:\n%s", got)
	}
	if !strings.Contains(got, "Ctrl+C aborts the run") {
		t.Errorf("banner must still document the abort:\n%s", got)
	}
}

func TestBannerReportsDisabledExternalReview(t *testing.T) {
	b := fullBanner()
	b.External = ""

	if got := b.String(); !strings.Contains(got, "claude (primary), external review disabled") {
		t.Errorf("banner does not report that external review is off:\n%s", got)
	}
}
