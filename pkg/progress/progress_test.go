package progress_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/korthane/rrev/pkg/progress"
)

// wholeRecord is the exact line the concurrent writers emit for a rejection, so
// a line that starts like one but does not match it is a torn write rather than
// a formatting difference.
var wholeRecord = regexp.MustCompile("^- \\*\\*rejected\\*\\* `R\\d+` major `pkg/a\\.go(:\\d+)?` — reviewer$")

// startsLikeRecord catches a rejection line whether or not it survived whole.
var startsLikeRecord = regexp.MustCompile(`^- \*\*rejected\*\* `)

func openLog(t *testing.T, dir, change string, opts progress.Options) *progress.Log {
	t.Helper()
	log, err := progress.Open(dir, change, opts)
	if err != nil {
		t.Fatalf("open progress log: %v", err)
	}
	return log
}

func readLog(t *testing.T, log *progress.Log) string {
	t.Helper()
	data, err := os.ReadFile(log.Path())
	if err != nil {
		t.Fatalf("read progress log: %v", err)
	}
	return string(data)
}

func TestOpenCreatesDirectoryIgnoreRuleAndLog(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".rrev", "progress")
	log := openLog(t, dir, "add-spec-review-pipeline", progress.Options{})

	if !log.Enabled() {
		t.Fatal("log should be enabled")
	}
	want := filepath.Join(dir, "progress-add-spec-review-pipeline.md")
	if log.Path() != want {
		t.Errorf("path = %q, want %q", log.Path(), want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Errorf("log file not created: %v", err)
	}

	ignore, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read ignore rule: %v", err)
	}
	if !strings.Contains(string(ignore), "*") {
		t.Errorf("ignore rule = %q, want an ignore-everything rule", ignore)
	}
}

func TestOpenKeepsExistingIgnoreRule(t *testing.T) {
	dir := t.TempDir()
	custom := "!keep-me\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(custom), 0o600); err != nil {
		t.Fatalf("seed ignore rule: %v", err)
	}
	openLog(t, dir, "change", progress.Options{})

	got, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read ignore rule: %v", err)
	}
	if string(got) != custom {
		t.Errorf("ignore rule = %q, want it left alone", got)
	}
}

func TestSecondRunAppendsToExistingLog(t *testing.T) {
	dir := t.TempDir()
	first := openLog(t, dir, "change", progress.Options{})
	first.Note("first run")

	second := openLog(t, dir, "change", progress.Options{})
	second.Note("second run")

	got := readLog(t, second)
	if !strings.Contains(got, "first run") || !strings.Contains(got, "second run") {
		t.Errorf("log = %q, want both runs preserved", got)
	}
	if strings.Index(got, "first run") > strings.Index(got, "second run") {
		t.Error("second run should be appended after the first")
	}
}

func TestSeparateChangesUseSeparateLogs(t *testing.T) {
	dir := t.TempDir()
	one := openLog(t, dir, "add-review", progress.Options{})
	two := openLog(t, dir, "add-export", progress.Options{})

	if one.Path() == two.Path() {
		t.Fatalf("both changes wrote to %s", one.Path())
	}
}

func TestChangeNameIsSanitizedIntoFileName(t *testing.T) {
	dir := t.TempDir()
	log := openLog(t, dir, "../weird name/v2", progress.Options{})

	name := filepath.Base(log.Path())
	if name != "progress-weird-name-v2.md" {
		t.Errorf("file name = %q, want the separators replaced", name)
	}
	if filepath.Dir(log.Path()) != dir {
		t.Errorf("log escaped the progress directory: %s", log.Path())
	}
}

func TestEntriesRecordEveryReconstructableEvent(t *testing.T) {
	dir := t.TempDir()
	log := openLog(t, dir, "change", progress.Options{})

	finding := progress.Finding{
		Reviewer:    "conformance",
		Severity:    "major",
		File:        "pkg/config/resolve.go",
		Line:        42,
		Requirement: "Layered resolution",
		Summary:     "flag layer is ignored",
	}
	log.RunStart(progress.RunInfo{
		Change:  "change",
		Goal:    "review the branch",
		BaseRef: "main",
		Head:    "abc1234",
		Mode:    "full",
	})
	log.PhaseStart("comprehensive review")
	log.IterationStart("comprehensive review", 1, 10)
	log.Finding(finding)
	log.Confirmed(finding, "fixed")
	log.Rejected(progress.Finding{Reviewer: "testing", Severity: "minor", File: "a.go"}, "covered by TestResolve")
	log.Commit("deadbee", "Fix the flag layer")
	log.LoopEnd("comprehensive review", "review-done signal", 3)

	got := readLog(t, log)
	for _, want := range []string{
		"# Run: change",
		"review the branch",
		"- base `main` … head `abc1234`",
		"- mode full",
		"## Phase: comprehensive review",
		"### comprehensive review · iteration 1/10 ·",
		"- **reported** `R1` major `pkg/config/resolve.go:42` (Layered resolution) — conformance",
		"- **confirmed** `R2` major `pkg/config/resolve.go:42` (Layered resolution) — conformance",
		"- **rejected** `R3` minor `a.go` — testing",
		"  covered by TestResolve",
		"- commit `deadbee` Fix the flag layer",
		"_confirmed 1 (1 major) · rejected 1 (1 new, 0 repeat) · commit deadbee_",
		"**comprehensive review ended:** review-done signal after 3 iteration(s)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("log missing %q\n--- log ---\n%s", want, got)
		}
	}
}

// The iteration boundary carries the run's only timestamp for everything inside
// it; a stamp on each of twenty entries is noise a reader has to look past.
func TestIterationCarriesOneTimestampForAllItsEntries(t *testing.T) {
	log := openLog(t, t.TempDir(), "change", progress.Options{})

	log.IterationStart("comprehensive", 1, 10)
	for i := range 5 {
		log.Rejected(progress.Finding{Reviewer: "quality", File: "a.go", Line: i}, "out of scope")
	}

	stamps := regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}`).FindAllString(readLog(t, log), -1)
	if len(stamps) != 1 {
		t.Errorf("timestamps = %d, want exactly one at the iteration boundary", len(stamps))
	}
}

// A finding that arrives with no severity or no location must not be folded
// into a severity bucket it does not belong to, nor vanish from the totals.
func TestUnclassifiedFindingIsCountedSeparately(t *testing.T) {
	log := openLog(t, t.TempDir(), "change", progress.Options{})

	log.IterationStart("comprehensive", 1, 10)
	log.Confirmed(progress.Finding{Reviewer: "quality", Severity: "major", File: "a.go", Line: 3}, "fixed")
	log.Confirmed(progress.Finding{Reviewer: "claude"}, "fixed")
	log.LoopEnd("comprehensive", "converged", 1)

	if want := "_confirmed 2 (1 major, 1 unclassified)"; !strings.Contains(readLog(t, log), want) {
		t.Errorf("summary missing %q\n--- log ---\n%s", want, readLog(t, log))
	}
}

// A phase that converges in silence and one whose tool died quietly look
// identical otherwise, and they call for opposite responses.
func TestExternalToolActivityIsRecorded(t *testing.T) {
	for _, tc := range []struct{ name, outcome, detail, want string }{
		{"no findings", "no findings reported", "", "- external tool `codex`: no findings reported"},
		{"failure", "failed", "exit status 1", "  exit status 1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			log := openLog(t, t.TempDir(), "change", progress.Options{})
			log.ExternalTool("codex", tc.outcome, tc.detail)
			if got := readLog(t, log); !strings.Contains(got, tc.want) {
				t.Errorf("log missing %q\n--- log ---\n%s", tc.want, got)
			}
		})
	}
}

func TestMultiLineDetailStaysInsideOneEntry(t *testing.T) {
	log := openLog(t, t.TempDir(), "change", progress.Options{})
	log.Rejected(progress.Finding{Reviewer: "quality", File: "a.go"}, "first line\n\nsecond line\n")

	got := readLog(t, log)
	if !strings.Contains(got, "  first line second line\n") {
		t.Errorf("a reason spanning lines must flatten into one indented line\n--- log ---\n%s", got)
	}
	for line := range strings.SplitSeq(strings.TrimRight(got, "\n"), "\n") {
		if strings.HasPrefix(line, "second line") {
			t.Errorf("detail line %q escaped its entry and reads as a new one", line)
		}
	}
}

func TestUnwritableDirectoryDisablesLoggingWithoutAborting(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocked, []byte("file"), 0o600); err != nil {
		t.Fatalf("seed blocker: %v", err)
	}

	log, err := progress.Open(filepath.Join(blocked, "progress"), "change", progress.Options{})
	if err == nil {
		t.Fatal("expected an error naming the unwritable directory")
	}
	if log == nil {
		t.Fatal("a failed Open must still return a usable disabled log")
	}
	if log.Enabled() || log.Path() != "" {
		t.Errorf("log should be disabled, got enabled=%v path=%q", log.Enabled(), log.Path())
	}
	log.Note("must not panic")
	log.LoopEnd("phase", "reason", 1)
}

func TestLockContentionIsReportedAndTheEntryStillLands(t *testing.T) {
	dir := t.TempDir()
	var warnings []string
	log := openLog(t, dir, "change", progress.Options{
		LockWait: 20 * time.Millisecond,
		Warn:     func(msg string) { warnings = append(warnings, msg) },
	})

	held := log.Path() + ".lock"
	if err := os.WriteFile(held, nil, 0o600); err != nil {
		t.Fatalf("hold lock: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		log.Note("written under contention")
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("append blocked on a held lock instead of giving up")
	}

	if len(warnings) != 1 || !strings.Contains(warnings[0], "busy") {
		t.Errorf("warnings = %q, want one contention report", warnings)
	}
	if !strings.Contains(readLog(t, log), "written under contention") {
		t.Error("entry was dropped instead of appended after the bounded wait")
	}
	if _, err := os.Stat(held); err != nil {
		t.Errorf("a lock rrev never acquired must not be removed: %v", err)
	}
}

func TestConcurrentWritersProduceWholeEntries(t *testing.T) {
	dir := t.TempDir()
	const (
		writers = 4
		entries = 25
	)

	var wg sync.WaitGroup
	for range writers {
		log := openLog(t, dir, "change", progress.Options{})
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range entries {
				log.Rejected(progress.Finding{
					Reviewer: "reviewer",
					Severity: "major",
					File:     "pkg/a.go",
					Line:     i,
				}, strings.Repeat("reason ", 40))
			}
		}()
	}
	wg.Wait()

	shared := openLog(t, dir, "change", progress.Options{})
	lines := strings.Split(strings.TrimRight(readLog(t, shared), "\n"), "\n")

	records, details := 0, 0
	for _, line := range lines {
		switch {
		case startsLikeRecord.MatchString(line):
			records++
			if !wholeRecord.MatchString(line) {
				t.Fatalf("truncated or interleaved record: %q", line)
			}
		case strings.TrimSpace(line) == strings.TrimSpace(strings.Repeat("reason ", 40)):
			details++
		}
	}
	// Every record and every reason must survive whole. Ledger sections may
	// also be present: writers that lost the tail to a competitor append
	// without one, so how many appear is a timing detail, not a contract.
	if want := writers * entries; records != want || details < want {
		t.Errorf("records = %d, whole reasons = %d, want %d records and at least that many reasons", records, details, want)
	}
}

// rrev cannot watch the validation command run, so the executor's own account of
// it is the only record a later reader gets.
func TestValidationOutcomeRecorded(t *testing.T) {
	log := openLog(t, t.TempDir(), "add-spec-review-pipeline", progress.Options{})

	log.Validation(progress.Validation{Outcome: "fail", Command: "make test", Detail: "TestFoo failed"})

	body := readLog(t, log)
	for _, want := range []string{"- validation **fail**", "`make test`", "TestFoo failed"} {
		if !strings.Contains(body, want) {
			t.Errorf("log %q does not contain %q", body, want)
		}
	}
}

// A run killed outright leaves its lock behind. Without reclaim every append of
// every later run would pay the full wait, forever.
func TestStaleLockIsReclaimed(t *testing.T) {
	dir := t.TempDir()
	var warnings []string
	log := openLog(t, dir, "change", progress.Options{
		LockWait: 20 * time.Millisecond,
		Warn:     func(msg string) { warnings = append(warnings, msg) },
	})

	abandoned := log.Path() + ".lock"
	if err := os.WriteFile(abandoned, nil, 0o600); err != nil {
		t.Fatalf("hold lock: %v", err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(abandoned, old, old); err != nil {
		t.Fatalf("age lock: %v", err)
	}

	log.Note("written after reclaiming the lock")

	if len(warnings) != 0 {
		t.Errorf("warnings = %q, want none: a stale lock is taken over, not reported", warnings)
	}
	if !strings.Contains(readLog(t, log), "written after reclaiming the lock") {
		t.Error("entry was not appended")
	}
	if _, err := os.Stat(abandoned); !os.IsNotExist(err) {
		t.Errorf("stale lock still present after release: %v", err)
	}
}

// A lock rrev cannot take reports itself once, not once per entry: a run writes
// tens of entries and the first report says all there is to say.
func TestHeldLockIsReportedOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	var warnings []string
	log := openLog(t, dir, "change", progress.Options{
		LockWait: 10 * time.Millisecond,
		Warn:     func(msg string) { warnings = append(warnings, msg) },
	})
	if err := os.WriteFile(log.Path()+".lock", nil, 0o600); err != nil {
		t.Fatalf("hold lock: %v", err)
	}

	for i := range 3 {
		log.Note("entry " + strconv.Itoa(i))
	}

	if len(warnings) != 1 {
		t.Errorf("warnings = %q, want exactly one", warnings)
	}
	for i := range 3 {
		if !strings.Contains(readLog(t, log), "entry "+strconv.Itoa(i)) {
			t.Errorf("entry %d was dropped", i)
		}
	}
}

// A finding that names a severity but no location is as unclassifiable as one
// naming neither: the summary must not file it under that severity.
func TestFindingWithoutALocationIsUnclassified(t *testing.T) {
	log := openLog(t, t.TempDir(), "change", progress.Options{})

	log.IterationStart("comprehensive", 1, 10)
	log.Confirmed(progress.Finding{Reviewer: "quality", Severity: "major"}, "fixed")
	log.LoopEnd("comprehensive", "converged", 1)

	if want := "_confirmed 1 (1 unclassified)"; !strings.Contains(readLog(t, log), want) {
		t.Errorf("summary missing %q\n--- log ---\n%s", want, readLog(t, log))
	}
}

// The iteration summary is what a reader skims; a validation outcome recorded
// inside the iteration has to reach it, not only the entry it was written as.
func TestIterationSummaryCarriesTheValidationOutcome(t *testing.T) {
	log := openLog(t, t.TempDir(), "change", progress.Options{})

	log.IterationStart("comprehensive", 1, 10)
	log.Confirmed(progress.Finding{Reviewer: "quality", Severity: "major", File: "a.go", Line: 3}, "fixed")
	log.Validation(progress.Validation{Outcome: "fail", Command: "go test ./...", Detail: "one test failed"})
	log.LoopEnd("comprehensive", "converged", 1)

	if want := "· validation fail_"; !strings.Contains(readLog(t, log), want) {
		t.Errorf("summary missing %q\n--- log ---\n%s", want, readLog(t, log))
	}
}
