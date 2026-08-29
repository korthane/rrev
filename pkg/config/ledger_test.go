package config

import (
	"fmt"
	"strings"
	"testing"
)

func ledgerEntries(n int) []string {
	out := make([]string, 0, n)
	for i := range n {
		out = append(out, fmt.Sprintf("- R%d  a%d.go:1  (raised comprehensive 1)\n    rejected because: out of scope\n", i+1, i+1))
	}
	return out
}

// A reviewer handed the ledger stops reconstructing it from a wall of
// chronological entries, which is the whole point of keeping one.
func TestLedgerExpandsIntoAPrompt(t *testing.T) {
	got := expandLedger(t, Vars{Ledger: ledgerEntries(2)})

	for _, want := range []string{"- R1", "- R2", "rejected because: out of scope"} {
		if !strings.Contains(got, want) {
			t.Errorf("expansion missing %q\n--- got ---\n%s", want, got)
		}
	}
}

// An empty ledger must read as an explicit nothing, not a blank the model has
// to interpret.
func TestEmptyLedgerSaysSo(t *testing.T) {
	if got := expandLedger(t, Vars{}); !strings.Contains(got, "nothing has been rejected yet") {
		t.Errorf("expansion = %q, want an explicit empty statement", got)
	}
}

// A silently shortened ledger would have a reviewer re-raise what it was never
// shown, so a cut has to announce itself.
func TestOversizedLedgerIsTruncatedAndSaysSo(t *testing.T) {
	entries := ledgerEntries(20)
	got := expandLedger(t, Vars{Ledger: entries, LedgerBudget: len(entries[0]) * 3})

	if !strings.Contains(got, "TRUNCATED") {
		t.Errorf("a cut ledger must say it was cut\n--- got ---\n%s", got)
	}
	if !strings.Contains(got, "- R1") {
		t.Error("truncation must keep the entries it was given first")
	}
	if strings.Contains(got, "- R20") {
		t.Error("truncation kept an entry past the budget")
	}
}

// Entries arrive most-raised first, so a budget cut keeps whatever is being
// re-litigated hardest rather than whatever happened to be recorded first.
func TestTruncationKeepsTheEntriesItWasHandedFirst(t *testing.T) {
	entries := []string{"- R9  hot.go:1  (raised comprehensive 1, 2, 3)\n", "- R1  cold.go:1  (raised comprehensive 1)\n"}
	got := expandLedger(t, Vars{Ledger: entries, LedgerBudget: len(entries[0])})

	if !strings.Contains(got, "R9") || strings.Contains(got, "R1  cold.go") {
		t.Errorf("budget cut the wrong end of the ledger\n--- got ---\n%s", got)
	}
}

// expandLedger renders a prompt that is nothing but the ledger, so a test sees
// exactly what the variable produced.
func expandLedger(t *testing.T, vars Vars) string {
	t.Helper()
	exp, projectDir := expanderFor(t, ExecutorClaude, vars)
	writeAsset(t, projectDir, KindPrompt, "review_first", "{{LEDGER}}")
	got, err := exp.Prompt("review_first")
	if err != nil {
		t.Fatalf("expand ledger: %v", err)
	}
	return got
}
