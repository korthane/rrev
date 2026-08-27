package openspec

import (
	"errors"
	"fmt"
	"strings"
)

// ErrNoRequirements reports a delta spec that yielded no requirements, which is
// how an unparseable spec file surfaces.
var ErrNoRequirements = errors.New("no requirements found")

const (
	operationHeaderPrefix = "## "
	operationHeaderSuffix = " Requirements"
	requirementPrefix     = "### Requirement:"
	scenarioPrefix        = "#### Scenario:"
)

// ParseDeltaSpec extracts requirements from one delta spec's markdown. capability
// is the spec's path under the change's specs/ tree and is copied onto every
// requirement. It returns ErrNoRequirements when the file declares none.
func ParseDeltaSpec(capability, content string) ([]Requirement, error) {
	var (
		reqs      []Requirement
		operation = OperationUnspecified
		text      strings.Builder
		scenario  strings.Builder
	)

	// Both flushes are guarded on real content, not on a non-empty builder: the
	// blank line after a section header lands in text, and flushing it at the
	// next header would erase the body already written for the requirement
	// before it.
	flushScenario := func() {
		steps := strings.TrimSpace(scenario.String())
		scenario.Reset()
		if steps == "" || len(reqs) == 0 {
			return
		}
		last := &reqs[len(reqs)-1]
		if len(last.Scenarios) > 0 {
			last.Scenarios[len(last.Scenarios)-1].Text = steps
		}
	}
	flushText := func() {
		body := strings.TrimSpace(text.String())
		text.Reset()
		if body == "" || len(reqs) == 0 {
			return
		}
		reqs[len(reqs)-1].Text = body
	}

	inScenario := false
	for line := range strings.SplitSeq(content, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case isOperationHeader(trimmed):
			flushScenario()
			flushText()
			inScenario = false
			operation = ParseOperation(strings.TrimSuffix(strings.TrimPrefix(trimmed, operationHeaderPrefix), operationHeaderSuffix))
		case strings.HasPrefix(trimmed, requirementPrefix):
			flushScenario()
			flushText()
			inScenario = false
			reqs = append(reqs, Requirement{
				Capability: capability,
				Operation:  operation,
				Name:       strings.TrimSpace(strings.TrimPrefix(trimmed, requirementPrefix)),
			})
		case strings.HasPrefix(trimmed, scenarioPrefix):
			flushScenario()
			flushText()
			inScenario = true
			if len(reqs) > 0 {
				last := &reqs[len(reqs)-1]
				last.Scenarios = append(last.Scenarios, Scenario{
					Name: strings.TrimSpace(strings.TrimPrefix(trimmed, scenarioPrefix)),
				})
			}
		case inScenario:
			scenario.WriteString(line + "\n")
		default:
			text.WriteString(line + "\n")
		}
	}
	flushScenario()
	flushText()

	if len(reqs) == 0 {
		return nil, fmt.Errorf("%w in capability %q", ErrNoRequirements, capability)
	}
	return reqs, nil
}

// isOperationHeader matches "## ADDED Requirements" and its siblings without
// matching an ordinary "## Purpose" heading.
func isOperationHeader(line string) bool {
	if !strings.HasPrefix(line, operationHeaderPrefix) || strings.HasPrefix(line, "###") {
		return false
	}
	return strings.HasSuffix(line, operationHeaderSuffix)
}
