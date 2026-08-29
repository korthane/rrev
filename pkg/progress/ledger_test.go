package progress_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/korthane/rrev/pkg/progress"
)

func countOf(s, sub string) int { return strings.Count(s, sub) }

// An identifier is what a reviewer quotes back to declare a re-raise, so it has
// to be visible next to the finding it names.
func TestFirstFindingIsAssignedAnIdentifier(t *testing.T) {
	log := openLog(t, t.TempDir(), "change", progress.Options{})

	log.IterationStart("comprehensive", 1, 10)
	log.Rejected(progress.Finding{Reviewer: "quality", File: "a.go", Line: 7}, "out of scope")

	if got := readLog(t, log); !strings.Contains(got, "- **rejected** `R1` `a.go:7`") {
		t.Errorf("finding is missing its identifier\n--- log ---\n%s", got)
	}
}

// The declared re-raise is the whole mechanism: rrev never works out for itself
// that two findings are the same one.
func TestDeclaredReRaiseUpdatesTheExistingEntry(t *testing.T) {
	log := openLog(t, t.TempDir(), "change", progress.Options{})

	log.IterationStart("comprehensive", 1, 10)
	log.Rejected(progress.Finding{Reviewer: "quality", File: "a.go", Line: 7}, "the echoed character is not key material")
	log.IterationStart("comprehensive", 2, 10)
	log.Rejected(progress.Finding{ReRaises: "R1", Reviewer: "implementation", File: "a.go", Line: 9}, "the echoed character is not key material")

	got := readLog(t, log)
	if strings.Contains(got, "`R2`") {
		t.Errorf("a declared re-raise must not open a second entry\n--- log ---\n%s", got)
	}
	if want := "raised comprehensive 1, 2"; !strings.Contains(got, want) {
		t.Errorf("ledger missing %q\n--- log ---\n%s", want, got)
	}
}

// A rationale restated on every recurrence is exactly the bloat the ledger
// exists to remove.
func TestRationaleIsStatedOnceAcrossRepeatedRaises(t *testing.T) {
	log := openLog(t, t.TempDir(), "change", progress.Options{})
	const reason = "reached only after the checksum verifies"

	log.IterationStart("comprehensive", 1, 10)
	log.Rejected(progress.Finding{Reviewer: "quality", File: "a.go", Line: 7}, reason)
	for _, n := range []int{2, 3} {
		log.IterationStart("comprehensive", n, 10)
		log.Rejected(progress.Finding{ReRaises: "R1", Reviewer: "quality", File: "a.go", Line: 7}, reason)
	}

	got := readLog(t, log)
	// Once per raise inside the iteration sections, plus once in the ledger.
	if n := countOf(got, reason); n != 4 {
		t.Errorf("reason appears %d times, want 3 records plus exactly 1 ledger statement\n--- log ---\n%s", n, got)
	}
	if n := countOf(got, "rejected: "+reason); n != 1 {
		t.Errorf("ledger states the rationale %d times, want exactly 1", n)
	}
}

// A wrongly declared re-raise merges two real findings. Recording every
// location is how an entry spanning unrelated files becomes visible.
func TestEntryRecordsEveryLocationItWasRaisedAt(t *testing.T) {
	log := openLog(t, t.TempDir(), "change", progress.Options{})

	log.IterationStart("comprehensive", 1, 10)
	log.Rejected(progress.Finding{Reviewer: "quality", File: "bech32.go", Line: 129}, "not key material")
	log.IterationStart("comprehensive", 2, 10)
	log.Rejected(progress.Finding{ReRaises: "R1", Reviewer: "quality", File: "bech32.go", Line: 163}, "not key material")

	if want := "`bech32.go:129, bech32.go:163`"; !strings.Contains(readLog(t, log), want) {
		t.Errorf("ledger missing %q\n--- log ---\n%s", want, readLog(t, log))
	}
}

// Re-litigation crosses phases: the final phase re-raises what comprehensive
// rejected, so a per-phase ledger would miss the recurrence entirely.
func TestLedgerSpansPhases(t *testing.T) {
	log := openLog(t, t.TempDir(), "change", progress.Options{})

	log.PhaseStart("comprehensive")
	log.IterationStart("comprehensive", 1, 10)
	log.Rejected(progress.Finding{Reviewer: "quality", File: "a.go", Line: 7}, "out of scope")
	log.PhaseStart("final")
	log.IterationStart("final", 1, 5)
	log.Rejected(progress.Finding{ReRaises: "R1", Reviewer: "quality", File: "a.go", Line: 7}, "out of scope")

	got := readLog(t, log)
	if strings.Contains(got, "`R2`") {
		t.Errorf("a cross-phase re-raise must reuse its entry\n--- log ---\n%s", got)
	}
	if want := "raised comprehensive 1; final 1"; !strings.Contains(got, want) {
		t.Errorf("ledger missing %q\n--- log ---\n%s", want, got)
	}
}

// A reader must never be told a fixed issue is still standing.
func TestConfirmingAPreviouslyRejectedFindingRetiresItsEntry(t *testing.T) {
	log := openLog(t, t.TempDir(), "change", progress.Options{})

	log.IterationStart("comprehensive", 1, 10)
	log.Rejected(progress.Finding{Reviewer: "quality", File: "a.go", Line: 7}, "believed unreachable")
	if !strings.Contains(readLog(t, log), "## Standing rejections") {
		t.Fatal("the rejection should be standing before it is confirmed")
	}

	log.IterationStart("comprehensive", 2, 10)
	log.Confirmed(progress.Finding{ReRaises: "R1", Reviewer: "quality", Severity: "major", File: "a.go", Line: 7}, "fixed")

	got := readLog(t, log)
	if !strings.Contains(got, "since confirmed and fixed; no longer standing") {
		t.Errorf("the entry must record that it was subsequently confirmed\n--- log ---\n%s", got)
	}
	// The prompt ledger is the list of open questions, so a retired entry must
	// not be handed to the next reviewer as something still to argue with.
	if entries := log.PromptEntries(); len(entries) != 0 {
		t.Errorf("PromptEntries = %q, want a retired entry withheld from reviewers", entries)
	}
}

// A run appending to an existing log must not re-issue an identifier its
// records already use, or a reviewer reading R1 out of the log names a
// different finding than the one rrev holds.
func TestSecondRunDoesNotReuseAnEarlierRunsIdentifiers(t *testing.T) {
	dir := t.TempDir()
	first := openLog(t, dir, "change", progress.Options{})
	first.IterationStart("comprehensive", 1, 10)
	first.Rejected(progress.Finding{Reviewer: "quality", File: "a.go", Line: 7}, "out of scope")

	second := openLog(t, dir, "change", progress.Options{})
	second.IterationStart("comprehensive", 1, 10)
	second.Rejected(progress.Finding{Reviewer: "quality", File: "b.go", Line: 3}, "also out of scope")

	got := readLog(t, second)
	if countOf(got, "**rejected** `R1`") != 1 {
		t.Errorf("the second run re-issued R1\n--- log ---\n%s", got)
	}
	if !strings.Contains(got, "**rejected** `R2` `b.go:3`") {
		t.Errorf("the second run should continue past the ids already in the log\n--- log ---\n%s", got)
	}
}

// An undeclared finding is a new finding. Inferring a match would merge two
// real issues on a coincidence of file and wording.
func TestUndeclaredFindingIsRecordedAsNew(t *testing.T) {
	log := openLog(t, t.TempDir(), "change", progress.Options{})

	log.IterationStart("comprehensive", 1, 10)
	log.Rejected(progress.Finding{Reviewer: "quality", File: "a.go", Line: 7}, "same reason, same place")
	log.IterationStart("comprehensive", 2, 10)
	log.Rejected(progress.Finding{Reviewer: "quality", File: "a.go", Line: 7}, "same reason, same place")

	if got := readLog(t, log); !strings.Contains(got, "`R2`") {
		t.Errorf("an identical but undeclared finding must open its own entry\n--- log ---\n%s", got)
	}
}

// An identifier rrev does not hold is the executor's mistake, not grounds for
// losing the finding or failing the iteration.
func TestUnknownIdentifierIsRecordedAsNewAndNoted(t *testing.T) {
	log := openLog(t, t.TempDir(), "change", progress.Options{})

	log.IterationStart("comprehensive", 1, 10)
	log.Rejected(progress.Finding{ReRaises: "R99", Reviewer: "quality", File: "a.go", Line: 7}, "out of scope")

	got := readLog(t, log)
	if !strings.Contains(got, "- **rejected** `R1`") {
		t.Errorf("the finding must still be recorded\n--- log ---\n%s", got)
	}
	if want := "note: re-raise of unknown entry R99 recorded as new finding R1"; !strings.Contains(got, want) {
		t.Errorf("log missing %q\n--- log ---\n%s", want, got)
	}
}

// A log written before this format existed keeps its content untouched: the
// ledger is appended after it, never rewound into.
func TestPreExistingUnstructuredLogIsAppendedToUnchanged(t *testing.T) {
	dir := t.TempDir()
	const legacy = "[2026-08-28T06:11:29-04:00] rejected: reviewer=quality location=a.go:7\n  out of scope\n"
	path := filepath.Join(dir, "progress-change.md")
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatalf("seed legacy log: %v", err)
	}

	log := openLog(t, dir, "change", progress.Options{})
	log.IterationStart("comprehensive", 1, 10)
	log.Rejected(progress.Finding{Reviewer: "quality", File: "b.go", Line: 3}, "still out of scope")

	got := readLog(t, log)
	if !strings.HasPrefix(got, legacy) {
		t.Errorf("earlier content was rewritten\n--- log ---\n%s", got)
	}
	if strings.Contains(got, "R1` `a.go:7") {
		t.Error("the legacy entry must not be parsed back into the ledger")
	}
}

// The summary's new/repeat split is what a reader judges convergence by, so a
// declaration rrev could not resolve must not be counted as a recurrence: the
// entry beside it says the finding is new.
func TestUnresolvedDeclarationCountsAsNewlyRaised(t *testing.T) {
	log := openLog(t, t.TempDir(), "change", progress.Options{})

	log.IterationStart("comprehensive", 1, 10)
	log.Rejected(progress.Finding{ReRaises: "R99", Reviewer: "quality", File: "a.go", Line: 7}, "out of scope")
	log.LoopEnd("comprehensive", "converged", 1)

	if got := readLog(t, log); !strings.Contains(got, "rejected 1 (1 new, 0 repeat)") {
		t.Errorf("an unresolved declaration was counted as a repeat\n--- log ---\n%s", got)
	}
}

// Confirming is not the end of the argument: a finding fixed in one iteration
// and re-raised in the next is rejected again, and a ledger that kept it
// retired would withhold that reason from every later reviewer - which is the
// re-litigation loop the ledger exists to close.
func TestRejectingAPreviouslyConfirmedFindingMakesItStandAgain(t *testing.T) {
	log := openLog(t, t.TempDir(), "change", progress.Options{})

	log.IterationStart("comprehensive", 1, 10)
	log.Confirmed(progress.Finding{Reviewer: "quality", Severity: "major", File: "a.go", Line: 7}, "fixed")
	log.IterationStart("comprehensive", 2, 10)
	log.Rejected(progress.Finding{ReRaises: "R1", Reviewer: "testing", File: "a.go", Line: 7}, "already fixed in iteration 1")

	got := readLog(t, log)
	if strings.Contains(got, "no longer standing") {
		t.Errorf("a re-rejected entry must not stay retired\n--- log ---\n%s", got)
	}
	entries := log.PromptEntries()
	if len(entries) != 1 || !strings.Contains(entries[0], "already fixed in iteration 1") {
		t.Errorf("PromptEntries = %q, want the standing reason handed to the next reviewer", entries)
	}
}

// A rejection whose reason went missing is the one finding guaranteed to come
// back unchanged, so it still has to reach the ledger and say what it lacks.
func TestRejectionWithoutAReasonStillEntersTheLedger(t *testing.T) {
	log := openLog(t, t.TempDir(), "change", progress.Options{})

	log.IterationStart("comprehensive", 1, 10)
	log.Rejected(progress.Finding{Reviewer: "quality", File: "a.go", Line: 7}, "  ")

	got := readLog(t, log)
	if !strings.Contains(got, "rejected: (no reason given)") {
		t.Errorf("a reasonless rejection left the ledger\n--- log ---\n%s", got)
	}
	if strings.Contains(got, "\n  \n") {
		t.Errorf("a blank continuation line was written\n--- log ---\n%q", got)
	}
}

// The rationale a prompt carries is the one that settled the question. A later
// re-rejection tends to restate it as "as recorded above", and adopting that
// would hollow out the entry every reviewer reads.
func TestLedgerKeepsTheRationaleThatSettledTheQuestion(t *testing.T) {
	log := openLog(t, t.TempDir(), "change", progress.Options{})

	log.IterationStart("comprehensive", 1, 10)
	log.Rejected(progress.Finding{Reviewer: "quality", File: "a.go", Line: 7}, "the value is not key material")
	log.IterationStart("comprehensive", 2, 10)
	log.Rejected(progress.Finding{ReRaises: "R1", Reviewer: "quality", File: "a.go", Line: 7}, "as recorded above")

	entries := log.PromptEntries()
	if len(entries) != 1 || !strings.Contains(entries[0], "the value is not key material") {
		t.Errorf("PromptEntries = %q, want the first rationale kept", entries)
	}
	if strings.Contains(entries[0], "as recorded above") {
		t.Errorf("a restatement replaced the settling rationale: %q", entries[0])
	}
}

// Re-rendering the ledger on every write must not inflate what a reader and a
// truncated prompt both rank entries by.
func TestRepeatedRaisesWithinOneIterationCountOnce(t *testing.T) {
	log := openLog(t, t.TempDir(), "change", progress.Options{})

	log.IterationStart("comprehensive", 1, 10)
	log.Rejected(progress.Finding{Reviewer: "quality", File: "a.go", Line: 7}, "out of scope")
	log.Rejected(progress.Finding{ReRaises: "R1", Reviewer: "testing", File: "a.go", Line: 7}, "out of scope")

	if want, got := "raised comprehensive 1\n", readLog(t, log); !strings.Contains(got, want) {
		t.Errorf("one iteration counted twice\n--- log ---\n%s", got)
	}
}
