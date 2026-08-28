package phase

import (
	"reflect"
	"testing"
)

func TestParseReport(t *testing.T) {
	output := `Reviewed the branch.

FINDING: critical | pkg/cli/run.go:42 | conformance | Run modes | --external-only and --phase1-only are both accepted
- FINDING: Minor | pkg/a.go | quality | - | the helper name reads oddly
FINDING: major | pkg/b.go:7 | external | Exit status | status is 0 | even without convergence
REJECTED: pkg/c.go:19 | testing | the assertion is made in the table above
REJECTED: pkg/d.go | quality | minor

` + "```" + `
FINDING: critical | quoted.go:1 | quality | - | this is documentation, not a report
` + "```" + `
Done.`

	findings, rejections, _ := ParseReport(output)

	want := []Finding{
		{Severity: "critical", File: "pkg/cli/run.go", Line: 42, Reviewer: "conformance",
			Requirement: "Run modes", Summary: "--external-only and --phase1-only are both accepted"},
		{Severity: "minor", File: "pkg/a.go", Reviewer: "quality", Summary: "the helper name reads oddly"},
		{Severity: "major", File: "pkg/b.go", Line: 7, Reviewer: "external",
			Requirement: "Exit status", Summary: "status is 0 | even without convergence"},
	}
	if !reflect.DeepEqual(findings, want) {
		t.Errorf("findings =\n%+v\nwant\n%+v", findings, want)
	}

	wantRejections := []Rejection{
		{File: "pkg/c.go", Line: 19, Reviewer: "testing", Reason: "the assertion is made in the table above"},
		{File: "pkg/d.go", Reviewer: "quality", Reason: "minor"},
	}
	if !reflect.DeepEqual(rejections, wantRejections) {
		t.Errorf("rejections =\n%+v\nwant\n%+v", rejections, wantRejections)
	}
}

func TestParseReportToleratesShortLines(t *testing.T) {
	findings, rejections, _ := ParseReport("FINDING: major | pkg/a.go:3\nREJECTED: pkg/b.go:4")

	if len(findings) != 1 || findings[0].File != "pkg/a.go" || findings[0].Line != 3 || findings[0].Summary != "" {
		t.Errorf("findings = %+v, want the location kept and the rest empty", findings)
	}
	if len(rejections) != 1 || rejections[0].File != "pkg/b.go" || rejections[0].Line != 4 {
		t.Errorf("rejections = %+v, want the location kept", rejections)
	}
}

func TestParseReportIgnoresNonReportText(t *testing.T) {
	findings, rejections, _ := ParseReport("I found no FINDING: lines worth reporting\nNOTHING: here")
	if len(findings) != 0 || len(rejections) != 0 {
		t.Errorf("findings = %+v, rejections = %+v, want none", findings, rejections)
	}
}

func TestFindingRoundTrip(t *testing.T) {
	f := Finding{Severity: "major", File: "a.go", Line: 9, Reviewer: "quality", Summary: "leaks a handle"}
	if got, want := f.String(), "FINDING: major | a.go:9 | quality | - | leaks a handle"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	parsed, _, _ := ParseReport(f.String())
	if len(parsed) != 1 || !reflect.DeepEqual(parsed[0], f) {
		t.Errorf("round trip = %+v, want %+v", parsed, f)
	}

	r := Rejection{File: "b.go", Line: 2, Reviewer: "external", Reason: "already handled by the caller"}
	if got, want := r.String(), "REJECTED: b.go:2 | external | already handled by the caller"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestParseLocationKeepsPathsWithoutLineNumbers(t *testing.T) {
	tests := []struct {
		text string
		file string
		line int
	}{
		{"pkg/a.go:12", "pkg/a.go", 12},
		{"pkg/a.go", "pkg/a.go", 0},
		{"pkg/a.go:end", "pkg/a.go:end", 0},
		{"-", "", 0},
		{"C:/src/a.go:8", "C:/src/a.go", 8},
	}
	for _, tt := range tests {
		file, line := parseLocation(tt.text)
		if file != tt.file || line != tt.line {
			t.Errorf("parseLocation(%q) = %q, %d, want %q, %d", tt.text, file, line, tt.file, tt.line)
		}
	}
}

func TestParseReportReadsValidationOutcome(t *testing.T) {
	output := "VALIDATION: PASS | make test lint | -\nVALIDATION: fail | go test ./... | TestFoo failed"

	_, _, validations := ParseReport(output)

	want := []Validation{
		{Outcome: "pass", Command: "make test lint"},
		{Outcome: "fail", Command: "go test ./...", Detail: "TestFoo failed"},
	}
	if !reflect.DeepEqual(validations, want) {
		t.Errorf("validations =\n%+v\nwant\n%+v", validations, want)
	}
}
