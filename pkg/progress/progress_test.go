package progress_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/korthane/rrev/pkg/progress"
)

// entryStart matches the first line of an entry; continuation lines are
// indented, so a whole-entry check is a check on how lines start.
var entryStart = regexp.MustCompile(`^\[\d{4}-\d{2}-\d{2}T[0-9:+\-Z]+\] [a-z]+:`)

// wholeHeader is the exact header the concurrent writers emit, so a header
// missing its tail is a torn write rather than a formatting difference.
var wholeHeader = regexp.MustCompile(`^\[[^\]]+\] rejected: reviewer=reviewer severity=major location=pkg/a\.go(:\d+)?$`)

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
		`run: change=change goal="review the branch" base=main head=abc1234 mode=full`,
		`phase: name="comprehensive review"`,
		`iteration: phase="comprehensive review" n=1/10`,
		`finding: reviewer=conformance severity=major location=pkg/config/resolve.go:42 requirement="Layered resolution"`,
		`confirmed: reviewer=conformance severity=major location=pkg/config/resolve.go:42 requirement="Layered resolution" action=fixed`,
		`rejected: reviewer=testing severity=minor location=a.go`,
		"  covered by TestResolve",
		`commit: hash=deadbee subject="Fix the flag layer"`,
		`end: phase="comprehensive review" reason="review-done signal" iterations=3`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("log missing %q\n--- log ---\n%s", want, got)
		}
	}
}

func TestMultiLineDetailStaysInsideOneEntry(t *testing.T) {
	dir := t.TempDir()
	log := openLog(t, dir, "change", progress.Options{})
	log.Rejected(progress.Finding{Reviewer: "quality"}, "first line\n\nsecond line\n")

	lines := strings.Split(strings.TrimRight(readLog(t, log), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatalf("lines = %q, want a header plus two detail lines", lines)
	}
	if !entryStart.MatchString(lines[0]) {
		t.Errorf("header = %q", lines[0])
	}
	for _, line := range lines[1:] {
		if !strings.HasPrefix(line, "  ") {
			t.Errorf("detail line %q should be indented so it cannot be read as a new entry", line)
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

	headers, details := 0, 0
	for _, line := range lines {
		switch {
		case entryStart.MatchString(line):
			headers++
			if !wholeHeader.MatchString(line) {
				t.Fatalf("truncated or interleaved header: %q", line)
			}
		case strings.HasPrefix(line, "  "):
			details++
			if strings.TrimSpace(line) != strings.TrimSpace(strings.Repeat("reason ", 40)) {
				t.Fatalf("truncated or interleaved detail: %q", line)
			}
		default:
			t.Fatalf("line is neither a whole header nor a detail line: %q", line)
		}
	}
	if want := writers * entries; headers != want || details != want {
		t.Errorf("headers = %d, details = %d, want %d of each", headers, details, want)
	}
}
