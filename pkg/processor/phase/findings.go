package phase

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/korthane/rrev/pkg/progress"
)

// Report line prefixes every phase prompt instructs the executor to emit.
const (
	findingPrefix  = "FINDING:"
	rejectedPrefix = "REJECTED:"
)

// noRequirement is what a prompt tells the executor to write when a finding
// relates to no requirement in particular.
const noRequirement = "-"

// Finding is one issue an executor verified and reported, in the line form the
// prompts define.
type Finding struct {
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
	File     string
	Line     int
	Reviewer string
	Reason   string
}

// Location renders file and line the way a report and the progress log show it.
func (f Finding) Location() string { return location(f.File, f.Line) }

// Location renders file and line the way a report and the progress log show it.
func (r Rejection) Location() string { return location(r.File, r.Line) }

// String renders the finding back into the line form it was parsed from.
func (f Finding) String() string {
	return fmt.Sprintf("%s %s | %s | %s | %s | %s",
		findingPrefix, orDash(f.Severity), orDash(f.Location()), orDash(f.Reviewer), orDash(f.Requirement), f.Summary)
}

// String renders the rejection back into the line form it was parsed from.
func (r Rejection) String() string {
	return fmt.Sprintf("%s %s | %s | %s", rejectedPrefix, orDash(r.Location()), orDash(r.Reviewer), r.Reason)
}

func (f Finding) entry() progress.Finding {
	return progress.Finding{
		Reviewer:    f.Reviewer,
		Severity:    f.Severity,
		File:        f.File,
		Line:        f.Line,
		Requirement: f.Requirement,
		Summary:     f.Summary,
	}
}

func (r Rejection) entry() progress.Finding {
	return progress.Finding{Reviewer: r.Reviewer, File: r.File, Line: r.Line, Summary: r.Reason}
}

// ParseReport extracts the findings and rejections an executor reported. Lines
// inside a fenced block are ignored for the same reason signals are: a model
// quoting the report format must not be read as reporting.
func ParseReport(output string) ([]Finding, []Rejection) {
	var (
		findings   []Finding
		rejections []Rejection
		fenced     bool
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
		switch {
		case strings.HasPrefix(line, findingPrefix):
			findings = append(findings, parseFinding(strings.TrimPrefix(line, findingPrefix)))
		case strings.HasPrefix(line, rejectedPrefix):
			rejections = append(rejections, parseRejection(strings.TrimPrefix(line, rejectedPrefix)))
		}
	}
	return findings, rejections
}

// parseFinding reads `severity | file:line | reviewer | requirement | summary`,
// tolerating a report that stops early: a finding missing its trailing fields is
// still worth recording, and a summary containing a pipe stays intact.
func parseFinding(rest string) Finding {
	fields := splitFields(rest, 5)
	f := Finding{
		Severity:    strings.ToLower(field(fields, 0)),
		Reviewer:    field(fields, 2),
		Requirement: undash(field(fields, 3)),
		Summary:     field(fields, 4),
	}
	f.File, f.Line = parseLocation(field(fields, 1))
	return f
}

func parseRejection(rest string) Rejection {
	fields := splitFields(rest, 3)
	r := Rejection{Reviewer: field(fields, 1), Reason: field(fields, 2)}
	r.File, r.Line = parseLocation(field(fields, 0))
	return r
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
