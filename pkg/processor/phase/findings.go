package phase

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/korthane/rrev/pkg/progress"
)

// Report line kinds every phase prompt instructs the executor to emit. A kind
// may carry a bracketed ledger id — FINDING[R7]: — declaring that the line
// re-raises something the log already holds.
const (
	findingKind    = "FINDING"
	rejectedKind   = "REJECTED"
	validationKind = "VALIDATION"
)

// noRequirement is what a prompt tells the executor to write when a finding
// relates to no requirement in particular.
const noRequirement = "-"

// Finding is one issue an executor verified and reported, in the line form the
// prompts define.
type Finding struct {
	// ReRaises is the ledger id the executor declared this finding re-raises,
	// empty when it declared none.
	ReRaises    string
	Severity    string
	File        string
	Line        int
	Reviewer    string
	Requirement string
	Summary     string
}

// Rejection is a reported issue the executor dismissed. The reason is the
// load-bearing part: it is what a later round is shown instead of letting the
// same finding come back unchanged.
type Rejection struct {
	// ReRaises is the ledger id the executor declared this rejection re-raises.
	ReRaises string
	File     string
	Line     int
	Reviewer string
	// Claim is what the reviewer asserted, kept apart from Reason so a ledger
	// entry can say what was argued as well as why it was dismissed.
	Claim  string
	Reason string
}

// Validation is the outcome an executor reported for the validation command it
// ran before committing. rrev cannot observe the run itself, so the report line
// is the only record of whether the fixes were validated.
type Validation struct {
	// Outcome is the word the executor reported, normally pass or fail.
	Outcome string
	Command string
	// Detail is what failed, empty when the executor reported nothing.
	Detail string
}

func (v Validation) entry() progress.Validation {
	return progress.Validation{Outcome: v.Outcome, Command: v.Command, Detail: v.Detail}
}

// Location renders file and line the way a report and the progress log show it.
func (f Finding) Location() string { return location(f.File, f.Line) }

// Location renders file and line the way a report and the progress log show it.
func (r Rejection) Location() string { return location(r.File, r.Line) }

// String renders the finding back into the line form it was parsed from.
func (f Finding) String() string {
	return fmt.Sprintf("%s %s | %s | %s | %s | %s",
		kindPrefix(findingKind, f.ReRaises), orDash(f.Severity), orDash(f.Location()), orDash(f.Reviewer),
		orDash(f.Requirement), f.Summary)
}

// String renders the rejection back into the line form it was parsed from.
func (r Rejection) String() string {
	return fmt.Sprintf("%s %s | %s | %s | %s",
		kindPrefix(rejectedKind, r.ReRaises), orDash(r.Location()), orDash(r.Reviewer), orDash(r.Claim), r.Reason)
}

// kindPrefix renders a report line's opening token, carrying the ledger id when
// the line re-raises an existing entry.
func kindPrefix(kind, reRaises string) string {
	if reRaises == "" {
		return kind + ":"
	}
	return kind + "[" + reRaises + "]:"
}

func (f Finding) entry() progress.Finding {
	return progress.Finding{
		ReRaises:    f.ReRaises,
		Reviewer:    f.Reviewer,
		Severity:    f.Severity,
		File:        f.File,
		Line:        f.Line,
		Requirement: f.Requirement,
		Summary:     f.Summary,
	}
}

func (r Rejection) entry() progress.Finding {
	return progress.Finding{
		ReRaises: r.ReRaises,
		Reviewer: r.Reviewer,
		File:     r.File,
		Line:     r.Line,
		// The claim only, never the reason standing in for it: a rejection
		// reported without one must leave the ledger's claim empty so a later
		// raise that does carry a claim can still fill it.
		Summary: r.Claim,
	}
}

// ParseReport extracts the findings, rejections, and validation outcomes an
// executor reported. Lines inside a fenced block are ignored for the same reason
// signals are: a model quoting the report format must not be read as reporting.
func ParseReport(output string) ([]Finding, []Rejection, []Validation) {
	var (
		findings    []Finding
		rejections  []Rejection
		validations []Validation
		fenced      bool
	)
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		line = strings.TrimLeft(line, "-*• \t")
		switch kind, id, rest, ok := cutKind(line); {
		case !ok:
		case kind == findingKind:
			findings = append(findings, parseFinding(id, rest))
		case kind == rejectedKind:
			rejections = append(rejections, parseRejection(id, rest))
		case kind == validationKind:
			validations = append(validations, parseValidation(rest))
		}
	}
	return findings, rejections, validations
}

// cutKind reads a report line's opening token, returning its kind, the ledger
// id it declared, and the rest of the line. A line whose token rrev does not
// know is not a report line. Spacing and a missing closing bracket are both
// tolerated: losing a whole finding over `FINDING [R3]:` or `FINDING[R3:`
// costs a reviewer's report, which is far worse than a mangled id.
func cutKind(line string) (kind, id, rest string, ok bool) {
	head, rest, ok := strings.Cut(line, ":")
	if !ok {
		return "", "", "", false
	}
	head = strings.TrimSpace(head)
	kind = head
	if before, after, found := strings.Cut(head, "["); found {
		kind, id = strings.TrimSpace(before), strings.Trim(after, "] \t")
		// An id never contains whitespace, so a bracket body that does is
		// prose mentioning a finding, not a declaration of one. Without this
		// the tolerance above swallows `FINDING[R7] restated: ...` and mints
		// a finding whose severity is the sentence.
		if strings.ContainsFunc(id, unicode.IsSpace) {
			return "", "", "", false
		}
	}
	switch kind {
	case findingKind, rejectedKind, validationKind:
		return kind, strings.TrimSpace(id), rest, true
	default:
		return "", "", "", false
	}
}

// parseFinding reads `severity | file:line | reviewer | requirement | summary`,
// tolerating a report that stops early: a finding missing its trailing fields is
// still worth recording, and a summary containing a pipe stays intact.
func parseFinding(reRaises, rest string) Finding {
	fields := splitFields(rest, 5)
	f := Finding{
		ReRaises:    reRaises,
		Severity:    strings.ToLower(field(fields, 0)),
		Reviewer:    field(fields, 2),
		Requirement: undash(field(fields, 3)),
		Summary:     field(fields, 4),
	}
	f.File, f.Line = parseLocation(field(fields, 1))
	return f
}

// parseRejection reads `file:line | reviewer | claim | reason`, tolerating the
// three-field form a prompt override may still emit by reading its last field
// as the reason and leaving the claim empty.
func parseRejection(reRaises, rest string) Rejection {
	fields := splitFields(rest, 4)
	r := Rejection{ReRaises: reRaises, Reviewer: field(fields, 1)}
	r.File, r.Line = parseLocation(field(fields, 0))
	if len(fields) < 4 {
		r.Reason = undash(field(fields, 2))
		return r
	}
	// The reason is undashed like every other field: `-` is the templates' own
	// stand-in for an absent one, and left literal it would settle the ledger
	// entry with a rationale no later rejection can replace.
	r.Claim, r.Reason = undash(field(fields, 2)), undash(field(fields, 3))
	return r
}

// parseValidation reads `outcome | command | detail`.
func parseValidation(rest string) Validation {
	fields := splitFields(rest, 3)
	return Validation{
		Outcome: strings.ToLower(field(fields, 0)),
		Command: undash(field(fields, 1)),
		Detail:  undash(field(fields, 2)),
	}
}

func splitFields(rest string, n int) []string {
	fields := strings.SplitN(rest, "|", n)
	for i := range fields {
		fields[i] = strings.TrimSpace(fields[i])
	}
	return fields
}

func field(fields []string, i int) string {
	if i >= len(fields) {
		return ""
	}
	return fields[i]
}

// parseLocation splits a `file:line` citation at its last colon, so a path that
// contains one keeps it, and leaves a location with no trailing number as a
// bare path.
func parseLocation(text string) (string, int) {
	text = undash(text)
	i := strings.LastIndex(text, ":")
	if i < 0 {
		return text, 0
	}
	line, err := strconv.Atoi(text[i+1:])
	if err != nil || line < 1 {
		return text, 0
	}
	return text[:i], line
}

func location(file string, line int) string {
	switch {
	case file == "":
		return ""
	case line > 0:
		return file + ":" + strconv.Itoa(line)
	default:
		return file
	}
}

func undash(v string) string {
	if v == noRequirement {
		return ""
	}
	return v
}

func orDash(v string) string {
	if v == "" {
		return noRequirement
	}
	return v
}
