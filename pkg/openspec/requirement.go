package openspec

import (
	"fmt"
	"strings"
)

// Operation is the delta operation a requirement is declared under. It tells a
// reviewer whether behavior is new, changed, withdrawn, or merely renamed.
type Operation string

// Delta operations recognized in a delta spec's section headers.
const (
	OperationAdded       Operation = "ADDED"
	OperationModified    Operation = "MODIFIED"
	OperationRemoved     Operation = "REMOVED"
	OperationRenamed     Operation = "RENAMED"
	OperationUnspecified Operation = "UNSPECIFIED"
)

// ParseOperation maps a section header word to an Operation, falling back to
// OperationUnspecified rather than inventing a delta meaning.
func ParseOperation(s string) Operation {
	switch op := Operation(strings.ToUpper(strings.TrimSpace(s))); op {
	case OperationAdded, OperationModified, OperationRemoved, OperationRenamed:
		return op
	default:
		return OperationUnspecified
	}
}

// Scenario is one verifiable case declared under a requirement.
type Scenario struct {
	Name string
	Text string
}

// Requirement is a single conformance criterion extracted from a delta spec.
type Requirement struct {
	// Capability is the spec's path under the change's specs/ tree, so a nested
	// capability keeps its full path (for example "billing/invoices").
	Capability string
	Operation  Operation
	Name       string
	Text       string
	Scenarios  []Scenario
}

// Title identifies the requirement in prompts and terminal output.
func (r Requirement) Title() string {
	name := r.Name
	if name == "" {
		name = summarize(r.Text, 80)
	}
	if name == "" {
		name = "(unnamed requirement)"
	}
	return name
}

// ChecklistEntries renders the checklist one requirement at a time, so a
// consumer working to a size budget can drop whole requirements instead of
// cutting one in half.
func ChecklistEntries(reqs []Requirement) []string {
	entries := make([]string, 0, len(reqs))
	for i, req := range reqs {
		var b strings.Builder
		fmt.Fprintf(&b, "%d. [%s] %s: %s\n", i+1, req.Operation, req.Capability, req.Title())
		if req.Text != "" {
			fmt.Fprintf(&b, "   %s\n", collapseSpace(req.Text))
		}
		if len(req.Scenarios) == 0 {
			b.WriteString("   (no scenarios declared - not verifiable as written)\n")
		}
		for _, scenario := range req.Scenarios {
			name := scenario.Name
			if name == "" {
				name = "Scenario"
			}
			fmt.Fprintf(&b, "   - %s: %s\n", name, collapseSpace(scenario.Text))
		}
		entries = append(entries, b.String())
	}
	return entries
}

// summarize reduces text to its first sentence, capped at maxLen runes.
func summarize(text string, maxLen int) string {
	text = collapseSpace(text)
	if text == "" {
		return ""
	}
	if end := sentenceEnd(text); end > 0 {
		text = text[:end]
	}
	runes := []rune(text)
	if len(runes) > maxLen {
		text = strings.TrimRight(string(runes[:maxLen]), " ") + "..."
	}
	return text
}

// sentenceEnd finds the first sentence terminator, ignoring the dot in an
// abbreviation-like "e.g." by requiring a following space or end of text.
func sentenceEnd(text string) int {
	for i, r := range text {
		if r != '.' && r != '!' && r != '?' {
			continue
		}
		if i+1 >= len(text) {
			return i + 1
		}
		if text[i+1] == ' ' && i > 1 && text[i-2] != '.' {
			return i + 1
		}
	}
	return -1
}

func collapseSpace(s string) string { return strings.Join(strings.Fields(s), " ") }
