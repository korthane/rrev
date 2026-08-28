package processor

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/korthane/rrev/pkg/processor/phase"
)

func TestReportRendersEveryReportedField(t *testing.T) {
	report := Report{
		Change:  "add-user-auth",
		Goal:    "add authentication",
		BaseRef: "main",
		Mode:    ModeReportOnly,
		Findings: []phase.Finding{
			{Severity: "critical", File: "pkg/auth.go", Line: 42, Reviewer: "conformance",
				Requirement: "Change selection", Summary: "the flag is never parsed"},
			{Severity: "minor", File: "pkg/auth.go", Reviewer: "quality", Summary: "confusing name"},
		},
	}

	body := report.Render()

	for _, want := range []string{
		"# rrev findings: add-user-auth",
		"- Goal: add authentication",
		"- Base ref: main",
		"- Mode: report-only",
		"- Verified findings: 2",
		"| critical | pkg/auth.go:42 | conformance | Change selection | the flag is never parsed |",
		"| minor | pkg/auth.go | quality | - | confusing name |",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("report is missing %q\n%s", want, body)
		}
	}
}

func TestReportEscapesPipesInSummaries(t *testing.T) {
	report := Report{Findings: []phase.Finding{{Severity: "major", File: "a.go", Line: 1, Summary: "a | b"}}}

	line := lastLine(t, report.Render())

	if strings.Count(line, "|")-strings.Count(line, `\|`) != 6 {
		t.Errorf("row = %q, want the summary's pipe escaped so the table stays intact", line)
	}
	if !strings.Contains(line, `a \| b`) {
		t.Errorf("row = %q, want the escaped summary", line)
	}
}

func TestReportWriteCreatesItsDirectory(t *testing.T) {
	dir := t.TempDir()
	report := Report{Change: "add-user-auth", Mode: ModeReportOnly}

	path, err := report.Write(dir, filepath.Join(".rrev", "findings.md"))
	if err != nil {
		t.Fatalf("write report: %v", err)
	}

	if want := filepath.Join(dir, ".rrev", "findings.md"); path != want {
		t.Errorf("path = %q, want %q", path, want)
	}
	if body := readFile(t, path); !strings.Contains(body, "No verified findings.") {
		t.Errorf("report = %q, want it to report an empty result", body)
	}
}

func TestModesAreDistinct(t *testing.T) {
	seen := map[Mode]bool{}
	for _, m := range Modes() {
		if seen[m] {
			t.Errorf("mode %q is listed twice", m)
		}
		seen[m] = true
	}
	if len(seen) != 4 {
		t.Errorf("modes = %v, want the four documented run modes", Modes())
	}
	if !ModeReportOnly.ReadOnly() {
		t.Error("report-only must be read-only")
	}
	for _, m := range []Mode{ModeFull, ModeExternalOnly, ModePhase1Only} {
		if m.ReadOnly() {
			t.Errorf("mode %q must be allowed to modify the repository", m)
		}
	}
}

func lastLine(t *testing.T, body string) string {
	t.Helper()
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	return lines[len(lines)-1]
}
