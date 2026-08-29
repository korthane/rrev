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

// The budget is documented as a cap on what one prompt carries. A review prompt
// embeds seven reviewer agents that each expand the ledger too, so budgeting
// each copy separately would let the prompt run to eight times the setting.
func TestLedgerBudgetBoundsTheWholePromptNotEachCopy(t *testing.T) {
	entries := ledgerEntries(20)
	budget := len(entries[0]) * 8
	exp, projectDir := expanderFor(t, ExecutorClaude, Vars{Ledger: entries, LedgerBudget: budget})
	writeAsset(t, projectDir, KindAgent, "alpha", "{{LEDGER}}")
	writeAsset(t, projectDir, KindAgent, "beta", "{{LEDGER}}")
	writeAsset(t, projectDir, KindPrompt, "review_first", "{{LEDGER}}\n{{AGENTS: alpha, beta}}")

	got, err := exp.Prompt("review_first")
	if err != nil {
		t.Fatalf("expand prompt: %v", err)
	}

	// Every site still gets a ledger; the budget is what they share.
	if n := strings.Count(got, "- R1 "); n != 3 {
		t.Fatalf("the ledger expanded at %d sites, want the prompt and both agents", n)
	}
	if used := strings.Count(got, "- R") * len(entries[0]); used > budget {
		t.Errorf("ledger occupies %d characters across the prompt, over the %d budget", used, budget)
	}
	if n := strings.Count(got, "TRUNCATED"); n != 3 {
		t.Errorf("%d of 3 cut ledgers said they were cut", n)
	}
}

// A budget too small for even one entry must still hand over one. A section
// cut to nothing reads as a run with nothing standing, which is the opposite
// of what a cut means.
func TestBudgetSmallerThanOneEntryStillKeepsIt(t *testing.T) {
	entries := ledgerEntries(3)
	got := expandLedger(t, Vars{Ledger: entries, LedgerBudget: 1})

	if !strings.Contains(got, "- R1") {
		t.Errorf("an entry larger than the budget was dropped entirely\n--- got ---\n%s", got)
	}
	if !strings.Contains(got, "TRUNCATED") {
		t.Errorf("a cut ledger must say it was cut\n--- got ---\n%s", got)
	}
}

// Dividing the budget across sites can leave each one less than a single
// entry, and the floor is what keeps those copies from reading as a run with
// nothing standing. The singular {{AGENT:}} form must be counted too: a site
// the division misses expands at the full budget, which is the multiplication
// the division exists to stop.
func TestEachSiteKeepsAnEntryWhenTheDividedBudgetIsTiny(t *testing.T) {
	entries := ledgerEntries(3)
	exp, projectDir := expanderFor(t, ExecutorClaude, Vars{Ledger: entries, LedgerBudget: 2})
	writeAsset(t, projectDir, KindAgent, "alpha", "{{LEDGER}}")
	writeAsset(t, projectDir, KindPrompt, "review_first", "{{LEDGER}}\n{{AGENT: alpha}}")

	got, err := exp.Prompt("review_first")
	if err != nil {
		t.Fatalf("expand prompt: %v", err)
	}

	if n := strings.Count(got, "- R1 "); n != 2 {
		t.Errorf("the ledger expanded at %d sites, want the prompt and its agent\n--- got ---\n%s", n, got)
	}
	// One entry each and no more: a budget of 2 divided over two sites buys a
	// single entry per copy, and anything further means the floor overpaid.
	if strings.Contains(got, "- R2 ") {
		t.Errorf("a site carried more than the divided budget allows\n--- got ---\n%s", got)
	}
	if n := strings.Count(got, "TRUNCATED"); n != 2 {
		t.Errorf("%d of 2 cut ledgers said they were cut\n--- got ---\n%s", n, got)
	}
}

// A budget smaller than the number of sites divides to zero, and zero is how
// fitEntries spells "unlimited". Without the floor a small ledger_budget would
// expand the whole ledger at every site with no truncation notice - the exact
// multiplication the division exists to stop, inverted.
func TestBudgetSmallerThanTheSiteCountStillBoundsEachSite(t *testing.T) {
	entries := ledgerEntries(20)
	exp, projectDir := expanderFor(t, ExecutorClaude, Vars{Ledger: entries, LedgerBudget: 1})
	writeAsset(t, projectDir, KindAgent, "alpha", "{{LEDGER}}")
	writeAsset(t, projectDir, KindPrompt, "review_first", "{{LEDGER}}\n{{AGENT: alpha}}")

	got, err := exp.Prompt("review_first")
	if err != nil {
		t.Fatalf("expand prompt: %v", err)
	}

	if strings.Contains(got, "- R2 ") {
		t.Errorf("a site carried more than one entry on a sub-site budget\n--- got ---\n%s", got)
	}
	if n := strings.Count(got, "TRUNCATED"); n != 2 {
		t.Errorf("%d of 2 cut ledgers said they were cut\n--- got ---\n%s", n, got)
	}
}

// The reviewer agents carry the ledger but not {{PROGRESS_LOG}}, and the budget
// is divided across sites, so truncation bites hardest exactly where a bare
// "read the progress log" names nothing the reader can open.
func TestTruncatedLedgerNamesTheLogItWasCutFrom(t *testing.T) {
	entries := ledgerEntries(20)
	got := expandLedger(t, Vars{Ledger: entries, LedgerBudget: len(entries[0]) * 3, ProgressLog: ".rrev/progress/p.md"})

	if !strings.Contains(got, "Read .rrev/progress/p.md for the rest") {
		t.Errorf("the truncation notice does not name the log\n--- got ---\n%s", got)
	}
}
