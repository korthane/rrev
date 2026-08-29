package phase

import (
	"reflect"
	"strings"
	"testing"

	"github.com/korthane/rrev/pkg/config"
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

	r := Rejection{File: "b.go", Line: 2, Reviewer: "external", Claim: "nil deref", Reason: "already handled by the caller"}
	if got, want := r.String(), "REJECTED: b.go:2 | external | nil deref | already handled by the caller"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	_, parsedRejections, _ := ParseReport(r.String())
	if len(parsedRejections) != 1 || !reflect.DeepEqual(parsedRejections[0], r) {
		t.Errorf("round trip = %+v, want %+v", parsedRejections, r)
	}
}

// A re-raise is declared in the line's own opening token, so a reviewer can
// name the ledger entry without disturbing the field layout.
func TestReportLinesCarryDeclaredReRaises(t *testing.T) {
	findings, rejections, _ := ParseReport(
		"FINDING[R3]: major | a.go:9 | quality | - | leaks a handle\n" +
			"REJECTED[R7]: b.go:2 | external | nil deref | already handled by the caller")

	if len(findings) != 1 || findings[0].ReRaises != "R3" {
		t.Errorf("findings = %+v, want one declaring R3", findings)
	}
	if len(rejections) != 1 || rejections[0].ReRaises != "R7" {
		t.Errorf("rejections = %+v, want one declaring R7", rejections)
	}
	if got := rejections[0].String(); got != "REJECTED[R7]: b.go:2 | external | nil deref | already handled by the caller" {
		t.Errorf("String() = %q, want the declaration preserved", got)
	}
}

// A prompt override still emitting the older three-field rejection must keep
// working: its last field is the reason, and the claim is simply absent.
func TestThreeFieldRejectionStillParses(t *testing.T) {
	_, rejections, _ := ParseReport("REJECTED: b.go:2 | external | already handled by the caller")
	if len(rejections) != 1 {
		t.Fatalf("rejections = %+v, want one", rejections)
	}
	if rejections[0].Reason != "already handled by the caller" || rejections[0].Claim != "" {
		t.Errorf("rejection = %+v, want the text read as the reason", rejections[0])
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

// A stray space around the declaration is a slip a model makes routinely.
// Dropping the whole line over it loses a reviewer's finding, which is far
// worse than losing the recurrence count.
func TestReportTokenToleratesSpacingAroundTheDeclaration(t *testing.T) {
	for _, line := range []string{
		"FINDING [R3]: major | a.go:1 | quality | - | the handler leaks",
		"FINDING[R3] : major | a.go:1 | quality | - | the handler leaks",
		"FINDING[ R3 ]: major | a.go:1 | quality | - | the handler leaks",
	} {
		findings, _, _ := ParseReport(line)
		if len(findings) != 1 {
			t.Errorf("%q parsed %d findings, want the finding kept", line, len(findings))
			continue
		}
		if findings[0].ReRaises != "R3" {
			t.Errorf("%q declared re-raise = %q, want R3", line, findings[0].ReRaises)
		}
	}
}

// slotOf names the field a report template's placeholder describes, using the
// prompt's own words. It is what lets the test tell a reordered template from a
// renamed one.
func slotOf(field string) string {
	switch text := strings.ToLower(field); {
	case strings.Contains(text, "<file>"):
		return "location"
	case strings.Contains(text, "critical") || strings.Contains(text, "major"):
		return "severity"
	case strings.Contains(text, "pass|fail"):
		return "outcome"
	case strings.Contains(text, "agent") || text == "external":
		return "reviewer"
	case strings.Contains(text, "requirement"):
		return "requirement"
	case strings.Contains(text, "summary"):
		return "summary"
	case strings.Contains(text, "claimed"):
		return "claim"
	case strings.Contains(text, "real finding"):
		return "reason"
	case strings.Contains(text, "command"):
		return "command"
	case strings.Contains(text, "what failed"):
		return "detail"
	default:
		return ""
	}
}

// The prompts document a field order and the parser splits on it, with nothing
// binding the two together. A prompt edit that reordered the last two fields
// would file the claim as the rationale - the text the ledger then publishes to
// every later reviewer as the reason a question was settled.
func TestShippedReportTemplatesMatchTheParser(t *testing.T) {
	assets := config.Assets{ProjectDir: t.TempDir(), UserDir: t.TempDir()}
	checked := 0
	for _, name := range assets.PromptNames() {
		asset, err := assets.Prompt(name)
		if err != nil {
			t.Fatalf("load prompt %s: %v", name, err)
		}
		for line := range strings.SplitSeq(asset.Content, "\n") {
			kind, rest, ok := strings.Cut(line, ": ")
			if !ok || !strings.Contains(line, "<") {
				continue
			}
			if kind != "FINDING" && kind != "REJECTED" && kind != "VALIDATION" {
				continue
			}
			checked++
			checkReportTemplate(t, name, kind, rest)
		}
	}
	if checked < 3 {
		t.Fatalf("only %d report templates were checked; the prompts should document all three kinds", checked)
	}
}

// splitTemplateFields splits a template on its field separator, leaving the
// pipes inside a placeholder alone: `<critical|major|minor>` is one field.
func splitTemplateFields(template string) []string {
	var fields []string
	depth, start := 0, 0
	for i, r := range template {
		switch r {
		case '<':
			depth++
		case '>':
			depth--
		case '|':
			if depth == 0 {
				fields = append(fields, template[start:i])
				start = i + 1
			}
		}
	}
	return append(fields, template[start:])
}

// checkReportTemplate fills a template's fields with markers and reads the
// result back through the parser the executor's real report goes through.
func checkReportTemplate(t *testing.T, prompt, kind, template string) {
	t.Helper()
	markers := map[string]string{
		"location": "pkg/example.go:42", "severity": "major", "outcome": "fail",
		"reviewer": "quality", "requirement": "3 Recorded content", "summary": "the summary text",
		"claim": "the claim text", "reason": "the reason text",
		"command": "go test ./...", "detail": "the detail text",
	}
	var built []string
	var slots []string
	for _, field := range splitTemplateFields(template) {
		slot := slotOf(strings.TrimSpace(field))
		if slot == "" {
			t.Fatalf("%s: %s template field %q says nothing about what belongs there", prompt, kind, field)
		}
		slots = append(slots, slot)
		built = append(built, markers[slot])
	}

	findings, rejections, validations := ParseReport(kind + ": " + strings.Join(built, " | "))
	var got map[string]string
	switch {
	case len(findings) == 1:
		f := findings[0]
		got = map[string]string{"severity": f.Severity, "location": location(f.File, f.Line),
			"reviewer": f.Reviewer, "requirement": f.Requirement, "summary": f.Summary}
	case len(rejections) == 1:
		r := rejections[0]
		got = map[string]string{"location": location(r.File, r.Line), "reviewer": r.Reviewer,
			"claim": r.Claim, "reason": r.Reason}
	case len(validations) == 1:
		v := validations[0]
		got = map[string]string{"outcome": v.Outcome, "command": v.Command, "detail": v.Detail}
	default:
		t.Fatalf("%s: the %s template does not parse as one report line: %q", prompt, kind, template)
	}
	if len(got) != len(slots) {
		t.Errorf("%s: the %s template documents %d fields, the parser reads %d", prompt, kind, len(slots), len(got))
	}
	for _, slot := range slots {
		if got[slot] != markers[slot] {
			t.Errorf("%s: the %s template's %s field parsed as %q, want %q", prompt, kind, slot, got[slot], markers[slot])
		}
	}
}

// A rejection reported without a claim must reach the ledger with an empty one.
// Standing the reason in for it printed the same sentence twice on the row and,
// worse, pinned the entry's claim so a later reviewer that did say what it
// claimed could never fill it in.
func TestRejectionWithoutAClaimCarriesNoClaim(t *testing.T) {
	_, rejections, _ := ParseReport("REJECTED: pkg/a.go:7 | quality | the buffer is aliased across goroutines")
	if len(rejections) != 1 {
		t.Fatalf("parsed %d rejections, want 1", len(rejections))
	}
	if got := rejections[0].entry().Summary; got != "" {
		t.Errorf("entry claim = %q, want empty rather than the reason standing in", got)
	}
}

func TestRejectionWithAClaimCarriesIt(t *testing.T) {
	_, rejections, _ := ParseReport("REJECTED: pkg/a.go:7 | quality | the buffer is aliased | it is copied before the goroutine starts")
	if len(rejections) != 1 {
		t.Fatalf("parsed %d rejections, want 1", len(rejections))
	}
	if want, got := "the buffer is aliased", rejections[0].entry().Summary; got != want {
		t.Errorf("entry claim = %q, want %q", got, want)
	}
}
