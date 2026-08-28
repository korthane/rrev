package processor

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/korthane/rrev/pkg/processor/phase"
)

// ReportOnlyRules is the run-mode paragraph a read-only run expands into every
// phase prompt in place of the default one, which tells the executor to fix,
// validate, and commit.
const ReportOnlyRules = "Run mode: report-only. Do NOT edit, create, or delete any file, do NOT run fix or format commands, " +
	"and do NOT stage, commit, or push anything: the working tree and the commit history must be exactly as you found them " +
	"when you finish. Verify every reported issue against the real code as usual, report the confirmed ones with FINDING " +
	"lines and the ones you dismissed with REJECTED lines. This is a single pass: no later iteration will revisit what you " +
	"leave behind, so report every finding you confirmed."

// missing is what a report shows for a field a finding did not carry.
const missing = "-"

// Report is the findings report a read-only run writes: every verified finding
// with the location, severity, reviewer, and requirement it was reported under.
type Report struct {
	Change   string
	Goal     string
	BaseRef  string
	Mode     Mode
	Findings []phase.Finding
}

// Write renders the report and writes it to file, which is resolved against dir
// when it is relative. It returns the path written.
func (r Report) Write(dir, file string) (string, error) {
	if strings.TrimSpace(file) == "" {
		return "", errors.New("no report file is configured, so the findings report has nowhere to go")
	}
	path := file
	if !filepath.IsAbs(path) {
		path = filepath.Join(dir, path)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return "", fmt.Errorf("create report directory %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(r.Render()), 0o600); err != nil {
		return "", fmt.Errorf("write findings report %s: %w", path, err)
	}
	return path, nil
}

// Render produces the report body: a header naming what was reviewed, then one
// table row per finding.
func (r Report) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# rrev findings: %s\n\n", orMissing(r.Change))
	for _, line := range [][2]string{
		{"Goal", r.Goal},
		{"Base ref", r.BaseRef},
		{"Mode", r.Mode.String()},
	} {
		fmt.Fprintf(&b, "- %s: %s\n", line[0], orMissing(line[1]))
	}
	fmt.Fprintf(&b, "- Verified findings: %d\n\n", len(r.Findings))

	if len(r.Findings) == 0 {
		b.WriteString("No verified findings.\n")
		return b.String()
	}
	b.WriteString("| Severity | Location | Reviewer | Requirement | Finding |\n")
	b.WriteString("| --- | --- | --- | --- | --- |\n")
	for _, f := range r.Findings {
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s |\n",
			cell(f.Severity), cell(f.Location()), cell(f.Reviewer), cell(f.Requirement), cell(f.Summary))
	}
	return b.String()
}

// cell escapes the pipes a summary may contain, so one finding cannot break the
// table it is rendered into.
func cell(v string) string {
	return orMissing(strings.ReplaceAll(strings.TrimSpace(v), "|", `\|`))
}

func orMissing(v string) string {
	if strings.TrimSpace(v) == "" {
		return missing
	}
	return v
}
