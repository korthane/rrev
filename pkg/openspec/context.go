package openspec

import (
	"strings"
)

// Context is the review context resolved once per run and reused by every
// phase, so each reviewer judges the diff against identical criteria. Phases
// take it by value; the artifacts and requirements it points at are shared and
// must be treated as read-only.
type Context struct {
	Root         Root
	Change       Change
	Goal         string
	Artifacts    Artifacts
	Requirements []Requirement
	// Degraded is set when any part of the context was resolved without the
	// openspec CLI.
	Degraded bool
	// Notes records degraded modes, missing artifacts, and unparseable specs.
	Notes []string
}

// Resolve builds the review context for a change: its artifacts, its requirement
// checklist, and the goal every phase refers to.
func Resolve(cli CLI, root Root, change Change, disc Discovery) (Context, error) {
	arts, err := LoadArtifacts(root, change)
	if err != nil {
		return Context{}, err
	}

	rc := Context{Root: root, Change: change, Artifacts: arts, Degraded: disc.Degraded}
	if disc.Note != "" {
		rc.Notes = append(rc.Notes, disc.Note)
	}
	rc.Notes = append(rc.Notes, arts.Notes...)

	parsed, parseNotes := parseAllSpecs(arts.Specs)
	rc.Notes = append(rc.Notes, parseNotes...)
	if reqs, err := cli.ExtractRequirements(root.Dir, change.Name); err == nil && len(reqs) > 0 {
		rc.Requirements = nameFromParsed(reqs, parsed)
	} else {
		rc.Degraded = true
		if err != nil {
			rc.Notes = append(rc.Notes, "requirement extraction fell back to the markdown parser: "+err.Error())
		}
		rc.Requirements = parsed
	}

	rc.Goal = deriveGoal(change.Name, arts)
	return rc, nil
}

// ScenarioCount totals the scenarios across the checklist.
func (c Context) ScenarioCount() int {
	total := 0
	for _, req := range c.Requirements {
		total += len(req.Scenarios)
	}
	return total
}

// GoalLine names the change and its goal in one line, the form used in prompts,
// terminal output, and the progress log.
func (c Context) GoalLine() string {
	if c.Goal == "" || c.Goal == c.Change.Name {
		return c.Change.Name
	}
	return c.Change.Name + ": " + c.Goal
}

// parseAllSpecs parses every delta spec, keeping going past a spec it cannot
// parse and reporting that file instead.
func parseAllSpecs(specs []Artifact) ([]Requirement, []string) {
	var (
		reqs  []Requirement
		notes []string
	)
	for _, spec := range specs {
		parsed, err := ParseDeltaSpec(spec.Capability, spec.Content)
		if err != nil {
			notes = append(notes, "could not parse delta spec "+spec.Path+": "+err.Error()+
				"; its raw text is included in the review context")
			continue
		}
		reqs = append(reqs, parsed...)
	}
	return reqs, notes
}

// nameFromParsed copies requirement and scenario titles onto the CLI's output,
// which reports requirement bodies without their headings.
func nameFromParsed(cliReqs, parsed []Requirement) []Requirement {
	byText := make(map[string]Requirement, len(parsed))
	for _, req := range parsed {
		byText[collapseSpace(req.Text)] = req
	}
	named := make([]Requirement, 0, len(cliReqs))
	for _, req := range cliReqs {
		match, ok := byText[collapseSpace(req.Text)]
		if !ok {
			named = append(named, req)
			continue
		}
		req.Name = match.Name
		for i := range req.Scenarios {
			if i < len(match.Scenarios) {
				req.Scenarios[i].Name = match.Scenarios[i].Name
			}
		}
		named = append(named, req)
	}
	return named
}

// deriveGoal summarizes why the change is needed, preferring the proposal's
// "Why" section and falling back to the change name.
func deriveGoal(changeName string, arts Artifacts) string {
	if arts.Proposal == nil {
		return changeName
	}
	body := sectionBody(arts.Proposal.Content, "why")
	if body == "" {
		body = firstParagraph(arts.Proposal.Content)
	}
	if goal := summarize(body, 200); goal != "" {
		return goal
	}
	return changeName
}

// sectionBody returns the first paragraph under the markdown heading whose title
// matches name, case-insensitively.
func sectionBody(content, name string) string {
	var (
		body    strings.Builder
		inside  bool
		started bool
	)
	for line := range strings.SplitSeq(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			if inside {
				break
			}
			title := strings.ToLower(strings.TrimSpace(strings.TrimLeft(trimmed, "# ")))
			inside = title == name
			continue
		}
		if !inside {
			continue
		}
		if trimmed == "" {
			if started {
				break
			}
			continue
		}
		started = true
		body.WriteString(trimmed + " ")
	}
	return strings.TrimSpace(body.String())
}

// firstParagraph returns the first non-heading, non-empty block of text.
func firstParagraph(content string) string {
	var (
		body    strings.Builder
		started bool
	)
	for line := range strings.SplitSeq(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "<!--") {
			continue
		}
		if trimmed == "" {
			if started {
				break
			}
			continue
		}
		started = true
		body.WriteString(trimmed + " ")
	}
	return strings.TrimSpace(body.String())
}
