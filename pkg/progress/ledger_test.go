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
	log.LoopEnd("comprehensive", "converged", 2)

	got := readLog(t, log)
	if strings.Contains(got, "`R2`") {
		t.Errorf("a declared re-raise must not open a second entry\n--- log ---\n%s", got)
	}
	if want := "raised comprehensive 1, 2"; !strings.Contains(got, want) {
		t.Errorf("ledger missing %q\n--- log ---\n%s", want, got)
	}
	// The split is the convergence signal a reader skims: a run re-litigating
	// settled questions must not read as one turning up new ones.
	if want := "rejected 1 (1 new, 0 repeat)"; !strings.Contains(got, want) {
		t.Errorf("iteration 1's summary missing %q\n--- log ---\n%s", want, got)
	}
	if want := "rejected 1 (0 new, 1 repeat)"; !strings.Contains(got, want) {
		t.Errorf("iteration 2's summary missing %q\n--- log ---\n%s", want, got)
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

// A second run starts with an empty ledger while its reviewers are still
// pointed at the whole log, so a declaration naming an id only the earlier run
// issued is routine input rather than a malformed one.
func TestReRaiseNamingAnEarlierRunsIdentifierOpensANewEntry(t *testing.T) {
	dir := t.TempDir()
	first := openLog(t, dir, "change", progress.Options{})
	first.IterationStart("comprehensive", 1, 10)
	first.Rejected(progress.Finding{Reviewer: "quality", File: "a.go", Line: 7}, "out of scope")

	second := openLog(t, dir, "change", progress.Options{})
	second.IterationStart("comprehensive", 1, 10)
	second.Rejected(progress.Finding{ReRaises: "R1", Reviewer: "quality", File: "b.go", Line: 3}, "still out of scope")

	got := readLog(t, second)
	if !strings.Contains(got, "**rejected** `R2` `b.go:3`") {
		t.Errorf("a prior run's id must not be adopted; the finding is new\n--- log ---\n%s", got)
	}
	if !strings.Contains(got, "R1") || !strings.Contains(got, "note:") {
		t.Errorf("the unresolved reference must be noted rather than dropped\n--- log ---\n%s", got)
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

// A truncated prompt keeps a prefix of the ledger, so the order that prefix is
// taken from is what decides which rejections a reviewer is shown at all.
func TestLedgerRanksTheMostRaisedEntriesFirst(t *testing.T) {
	log := openLog(t, t.TempDir(), "change", progress.Options{})

	log.IterationStart("comprehensive", 1, 10)
	log.Rejected(progress.Finding{Reviewer: "quality", File: "once.go", Line: 1}, "raised a single time")
	log.Rejected(progress.Finding{Reviewer: "quality", File: "thrice.go", Line: 2}, "raised over and over")
	for _, n := range []int{2, 3} {
		log.IterationStart("comprehensive", n, 10)
		log.Rejected(progress.Finding{ReRaises: "R2", Reviewer: "quality", File: "thrice.go", Line: 2}, "raised over and over")
	}

	entries := log.PromptEntries()
	if len(entries) != 2 {
		t.Fatalf("PromptEntries = %d entries, want 2: %q", len(entries), entries)
	}
	if !strings.Contains(entries[0], "R2") {
		t.Errorf("the thrice-raised entry must lead, got %q first", entries[0])
	}
	got := readLog(t, log)
	if strings.Index(got, "**R2**") > strings.Index(got, "**R1**") {
		t.Errorf("the ledger section is not ranked most-raised first\n--- log ---\n%s", got)
	}
}

// The claim is what tells a reviewer whether the entry is the finding it is
// about to report. Since a rejection's summary is not written beside it in the
// iteration section, the ledger row is the only place the claim ever appears.
func TestLedgerRowCarriesTheClaimItRejected(t *testing.T) {
	log := openLog(t, t.TempDir(), "change", progress.Options{})

	log.IterationStart("comprehensive", 1, 10)
	log.Rejected(progress.Finding{
		Reviewer: "quality", File: "a.go", Line: 7,
		Summary: "the echoed byte leaks the key",
	}, "the byte is a length prefix, not key material")

	got := readLog(t, log)
	if !strings.Contains(got, "claim: the echoed byte leaks the key") {
		t.Errorf("the ledger row dropped the claim\n--- log ---\n%s", got)
	}
	if entries := log.PromptEntries(); len(entries) != 1 || !strings.Contains(entries[0], "claim: the echoed byte leaks the key") {
		t.Errorf("PromptEntries = %q, want the claim carried into the prompt", entries)
	}
}

// A rejection reported without a claim must not pin the entry to a blank one:
// a later reviewer that does say what it claimed is the entry's only chance to
// describe itself.
func TestLaterRaiseSuppliesAClaimTheFirstOneLacked(t *testing.T) {
	log := openLog(t, t.TempDir(), "change", progress.Options{})

	log.IterationStart("comprehensive", 1, 10)
	log.Rejected(progress.Finding{Reviewer: "quality", File: "a.go", Line: 7}, "reached only after the checksum verifies")
	log.IterationStart("comprehensive", 2, 10)
	log.Rejected(progress.Finding{
		ReRaises: "R1", Reviewer: "testing", File: "a.go", Line: 7,
		Summary: "the nil branch is unreachable",
	}, "reached only after the checksum verifies")

	entries := log.PromptEntries()
	if len(entries) != 1 || !strings.Contains(entries[0], "claim: the nil branch is unreachable") {
		t.Errorf("PromptEntries = %q, want the later claim to fill the empty slot", entries)
	}
}

// The placeholder settles nothing, so it is not the rationale a later reviewer
// has to answer. A real reason arriving afterwards has to displace it or the
// entry publishes "(no reason given)" for the rest of the run.
func TestRealRationaleReplacesTheMissingReasonPlaceholder(t *testing.T) {
	log := openLog(t, t.TempDir(), "change", progress.Options{})

	log.IterationStart("comprehensive", 1, 10)
	log.Rejected(progress.Finding{Reviewer: "quality", File: "a.go", Line: 7}, "")
	log.IterationStart("comprehensive", 2, 10)
	log.Rejected(progress.Finding{ReRaises: "R1", Reviewer: "quality", File: "a.go", Line: 7}, "the buffer is reused, not aliased")

	entries := log.PromptEntries()
	if len(entries) != 1 || !strings.Contains(entries[0], "the buffer is reused, not aliased") {
		t.Errorf("PromptEntries = %q, want the real rationale to replace the placeholder", entries)
	}
	if strings.Contains(entries[0], "no reason given") {
		t.Errorf("the placeholder outlived a stated reason: %q", entries[0])
	}
}

// The claim is what a reviewer matches its own finding against. The symmetric
// rationale invariant is pinned above; without this one a re-raise reported in
// the three-field form could blank the claim of the entry it names.
func TestLedgerKeepsTheClaimTheFirstRaiseSupplied(t *testing.T) {
	log := openLog(t, t.TempDir(), "change", progress.Options{})

	log.IterationStart("comprehensive", 1, 10)
	log.Rejected(progress.Finding{Reviewer: "quality", File: "a.go", Line: 7,
		Summary: "the echoed character is key material"}, "it is a prompt, not a secret")
	log.IterationStart("comprehensive", 2, 10)
	log.Rejected(progress.Finding{ReRaises: "R1", Reviewer: "testing", File: "a.go", Line: 7}, "still not a secret")

	entries := log.PromptEntries()
	if len(entries) != 1 || !strings.Contains(entries[0], "claim: the echoed character is key material") {
		t.Errorf("PromptEntries = %q, want the first claim kept", entries)
	}
}

// A finding re-raised at one line is the common case, so an entry that lists
// that line once per raise would bury the cross-location signal a merged entry
// is meant to show, in every copy of every prompt.
func TestOneLocationRaisedRepeatedlyIsListedOnce(t *testing.T) {
	log := openLog(t, t.TempDir(), "change", progress.Options{})

	log.IterationStart("comprehensive", 1, 10)
	log.Rejected(progress.Finding{Reviewer: "quality", File: "a.go", Line: 7}, "out of scope")
	log.IterationStart("comprehensive", 2, 10)
	log.Rejected(progress.Finding{ReRaises: "R1", Reviewer: "testing", File: "a.go", Line: 7}, "out of scope")
	log.IterationStart("comprehensive", 3, 10)
	log.Rejected(progress.Finding{ReRaises: "R1", Reviewer: "quality", File: "a.go", Line: 7}, "out of scope")

	if want, got := "- **R1** `a.go:7` — raised comprehensive 1, 2, 3\n", readLog(t, log); !strings.Contains(got, want) {
		t.Errorf("ledger row missing %q\n--- log ---\n%s", want, got)
	}
	if got := log.PromptEntries(); len(got) != 1 || countOf(got[0], "a.go:7") != 1 {
		t.Errorf("PromptEntries = %q, want one entry naming the location once", got)
	}
}

// A rejection reported with no location still has to be actionable: an entry
// rendered with an empty location leaves a reviewer nothing to anchor it to.
func TestEntryWithoutALocationSaysSo(t *testing.T) {
	log := openLog(t, t.TempDir(), "change", progress.Options{})

	log.IterationStart("comprehensive", 1, 10)
	log.Rejected(progress.Finding{Reviewer: "quality", Summary: "the design is wrong"}, "out of scope")

	if want, got := "- **R1** `(no location given)` —", readLog(t, log); !strings.Contains(got, want) {
		t.Errorf("ledger row missing %q\n--- log ---\n%s", want, got)
	}
	if got := log.PromptEntries(); len(got) != 1 || !strings.Contains(got[0], "(no location given)") {
		t.Errorf("PromptEntries = %q, want the missing location named", got)
	}
}

// showClaim guards both renderers. The log's arm is pinned elsewhere; without
// this one the prompt could drift into printing a bare "claim:" line on every
// claimless entry, which is the divergence showClaim exists to prevent.
func TestPromptEntryOmitsTheClaimLineWhenThereIsNoClaim(t *testing.T) {
	log := openLog(t, t.TempDir(), "change", progress.Options{})

	log.IterationStart("comprehensive", 1, 10)
	log.Rejected(progress.Finding{Reviewer: "quality", File: "a.go", Line: 7}, "out of scope")

	got := log.PromptEntries()
	if len(got) != 1 || strings.Contains(got[0], "claim:") {
		t.Errorf("PromptEntries = %q, want no claim line for a claimless entry", got)
	}
}

// A rejection's summary is the ledger entry's claim, so repeating it on the
// record line would print the same sentence twice in every iteration section.
func TestRejectionRecordLineLeavesTheClaimToTheLedger(t *testing.T) {
	log := openLog(t, t.TempDir(), "change", progress.Options{})

	log.IterationStart("comprehensive", 1, 10)
	log.Rejected(progress.Finding{Reviewer: "quality", File: "a.go", Line: 7,
		Summary: "the buffer is unbounded"}, "bounded by the caller")

	if got := readLog(t, log); countOf(got, "the buffer is unbounded") != 1 {
		t.Errorf("the claim is written %d times, want once (the ledger row)\n--- log ---\n%s",
			countOf(got, "the buffer is unbounded"), got)
	}
}
