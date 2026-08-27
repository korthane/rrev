package status

import (
	"fmt"
	"strings"
)

// Model is one phase's resolved model specification, as the banner reports it.
type Model struct {
	Phase string
	Spec  string
}

// Banner is what a run states about itself before its first phase: everything
// that decides what the run will do, so a wrong change, base ref, or model is
// visible before an executor is spent on it.
type Banner struct {
	Version string
	// Change is the change name and its goal, in one line.
	Change  string
	BaseRef string
	// DiffCommand is the diff every reviewer is told to read.
	DiffCommand string
	Mode        string

	Primary string
	// External names the independent review tool, empty when external review
	// is disabled.
	External string

	Models []Model

	Requirements int
	Scenarios    int

	// ProgressLog is where the run's history is appended, empty when the log
	// could not be opened.
	ProgressLog string

	// BreakHint describes the break key, empty on a platform without one.
	BreakHint string
}

// String renders the banner as the aligned block printed before the first phase.
func (b Banner) String() string {
	rows := [][2]string{
		{"change", b.Change},
		{"base ref", b.baseRef()},
		{"mode", b.Mode},
		{"executors", b.executors()},
	}
	if models := b.models(); models != "" {
		rows = append(rows, [2]string{"models", models})
	}
	if b.ProgressLog != "" {
		rows = append(rows, [2]string{"progress", b.ProgressLog})
	}
	rows = append(rows,
		[2]string{"criteria", fmt.Sprintf("%s, %s", plural(b.Requirements, "requirement"), plural(b.Scenarios, "scenario"))},
		[2]string{"keys", b.keys()},
	)

	width := 0
	for _, row := range rows {
		width = max(width, len(row[0]))
	}
	var out strings.Builder
	fmt.Fprintf(&out, "rrev %s\n", b.Version)
	for _, row := range rows {
		fmt.Fprintf(&out, "  %-*s  %s\n", width, row[0], row[1])
	}
	return strings.TrimRight(out.String(), "\n")
}

func (b Banner) baseRef() string {
	if b.DiffCommand == "" {
		return b.BaseRef
	}
	return fmt.Sprintf("%s (%s)", b.BaseRef, b.DiffCommand)
}

func (b Banner) executors() string {
	primary := b.Primary + " (primary)"
	if b.External == "" {
		return primary + ", external review disabled"
	}
	return primary + ", " + b.External + " (external review)"
}

func (b Banner) models() string {
	parts := make([]string, 0, len(b.Models))
	for _, m := range b.Models {
		parts = append(parts, m.Phase+" "+m.Spec)
	}
	return strings.Join(parts, ", ")
}

// keys documents the interruption contract, omitting the break key where the
// platform has none rather than advertising a key that does nothing.
func (b Banner) keys() string {
	hint := "Ctrl+C aborts the run"
	if b.BreakHint != "" {
		hint += ", " + b.BreakHint + " ends the external review loop"
	}
	return hint
}

func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
