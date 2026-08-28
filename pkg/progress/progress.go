// Package progress appends structured run history to the per-change progress log.
package progress

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ignoreFile keeps the pipeline's own commits from picking up run artifacts.
const ignoreFile = ".gitignore"

const ignoreBody = "# rrev progress logs are run artifacts, not source.\n*\n"

// Entry kinds, one per thing a later reader must be able to reconstruct.
const (
	kindRun       = "run"
	kindPhase     = "phase"
	kindIteration = "iteration"
	kindFinding   = "finding"
	kindConfirmed = "confirmed"
	kindRejected  = "rejected"
	kindValidate  = "validation"
	kindCommit    = "commit"
	kindEnd       = "end"
	kindNote      = "note"
)

// indent prefixes the continuation lines of an entry, so every entry starts at
// column zero with its timestamp and a reader can split the log on that.
const indent = "  "

// RunInfo identifies what a run reviewed, written once at the top of a run.
type RunInfo struct {
	Change  string
	Goal    string
	BaseRef string
	Head    string
	Mode    string
}

// Finding is one reported issue, recorded with enough context to tell whether a
// later iteration is re-reporting it.
type Finding struct {
	Reviewer    string
	Severity    string
	File        string
	Line        int
	Requirement string
	Summary     string
}

// Validation is the outcome of the validation command an executor ran before
// committing, as the executor reported it.
type Validation struct {
	Outcome string
	Command string
	Detail  string
}

// Options tunes a log. The zero value is what a normal run uses.
type Options struct {
	// LockWait bounds how long an append waits for a competing run; zero uses
	// DefaultLockWait.
	LockWait time.Duration
	// Warn receives contention and degradation notices as they happen. A nil
	// Warn discards them.
	Warn func(string)
	// now overrides the clock in tests.
	now func() time.Time
}

// Log is the append-only progress log for one change. A disabled Log accepts
// every call and writes nothing, so a run whose progress directory is
// unwritable needs no special-casing downstream.
type Log struct {
	path     string
	lock     fileLock
	lockWait time.Duration
	warn     func(string)
	now      func() time.Time

	mu sync.Mutex
	// lockWarned keeps a lock rrev cannot take from repeating its warning on
	// every entry: a run writes tens of them, and the first one says it all.
	lockWarned bool
}

// Open opens the progress log for change under dir, creating the directory and
// its ignore rule when missing and appending to an existing log so prior runs
// are preserved. When the directory or file cannot be written it returns a
// disabled log together with the error: logging is not worth aborting a review
// for, so the caller reports the failure and keeps going.
func Open(dir, change string, opts Options) (*Log, error) {
	log := &Log{
		path:     filepath.Join(dir, fileName(change)),
		lockWait: opts.LockWait,
		warn:     opts.Warn,
		now:      opts.now,
	}
	if log.lockWait <= 0 {
		log.lockWait = DefaultLockWait
	}
	if log.now == nil {
		log.now = time.Now
	}
	if log.warn == nil {
		log.warn = func(string) {}
	}
	log.lock = fileLock{path: log.path + ".lock"}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return Disabled(), fmt.Errorf("create progress directory %s: %w", dir, err)
	}
	if err := ensureIgnoreRule(dir); err != nil {
		return Disabled(), err
	}
	f, err := os.OpenFile(log.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return Disabled(), fmt.Errorf("open progress log %s: %w", log.path, err)
	}
	if err := f.Close(); err != nil {
		return Disabled(), fmt.Errorf("open progress log %s: %w", log.path, err)
	}
	return log, nil
}

// Disabled returns a log that records nothing.
func Disabled() *Log { return &Log{} }

// Enabled reports whether entries are being recorded.
func (l *Log) Enabled() bool { return l != nil && l.path != "" }

// Path is the resolved log path prompts point reviewers at; it is empty when
// logging is disabled.
func (l *Log) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// RunStart records what this run reviews, so a log holding several runs stays
// readable.
func (l *Log) RunStart(info RunInfo) {
	l.append(kindRun, []kv{
		{"change", info.Change},
		{"goal", info.Goal},
		{"base", info.BaseRef},
		{"head", info.Head},
		{"mode", info.Mode},
	}, "")
}

// PhaseStart records a phase boundary.
func (l *Log) PhaseStart(phase string) {
	l.append(kindPhase, []kv{{"name", phase}}, "")
}

// IterationStart records an iteration boundary within a phase.
func (l *Log) IterationStart(phase string, n, limit int) {
	l.append(kindIteration, []kv{
		{"phase", phase},
		{"n", iterationCount(n, limit)},
	}, "")
}

// Finding records an issue a reviewer reported, before it has been judged.
func (l *Log) Finding(f Finding) {
	l.append(kindFinding, f.fields(), f.Summary)
}

// Confirmed records a finding the executor verified against real code, with
// what it did about it.
func (l *Log) Confirmed(f Finding, action string) {
	l.append(kindConfirmed, append(f.fields(), kv{"action", action}), f.Summary)
}

// Rejected records a finding dismissed as a false positive. The reason is the
// load-bearing part: a later reviewer reads it and must either accept it or
// argue with it instead of re-reporting the finding unchanged.
func (l *Log) Rejected(f Finding, reason string) {
	l.append(kindRejected, f.fields(), reason)
}

// Validation records what the executor reported about the validation command it
// ran before committing, which is the only account of it a later reader gets.
func (l *Log) Validation(v Validation) {
	l.append(kindValidate, []kv{{"outcome", v.Outcome}, {"command", v.Command}}, v.Detail)
}

// Commit records a commit the pipeline made.
func (l *Log) Commit(hash, subject string) {
	l.append(kindCommit, []kv{{"hash", hash}, {"subject", subject}}, "")
}

// LoopEnd records which condition ended a loop and how many iterations ran.
func (l *Log) LoopEnd(phase, reason string, iterations int) {
	l.append(kindEnd, []kv{
		{"phase", phase},
		{"reason", reason},
		{"iterations", strconv.Itoa(iterations)},
	}, "")
}

// Note records anything else worth reconstructing later, such as a skipped
// phase or a degraded context.
func (l *Log) Note(text string) {
	l.append(kindNote, nil, text)
}

// append writes one whole entry under the cross-process lock. An entry that
// cannot take the lock in time is still written: an unserialized append risks
// interleaving, whereas dropping it loses history the reader needs.
func (l *Log) append(kind string, fields []kv, detail string) {
	if !l.Enabled() {
		return
	}
	entry := l.render(kind, fields, detail)

	l.mu.Lock()
	defer l.mu.Unlock()

	switch err := l.lock.acquire(l.lockWait); {
	case err == nil:
		defer l.lock.release()
	case errors.Is(err, errLockBusy):
		l.warnOnce(fmt.Sprintf("progress log %s is busy after %s; appending without the lock", l.path, l.lockWait))
	default:
		l.warnOnce(fmt.Sprintf("progress log lock %s: %v; appending without the lock", l.lock.path, err))
	}

	if err := appendFile(l.path, entry); err != nil {
		l.warn(fmt.Sprintf("write progress log: %v", err))
	}
}

// warnOnce reports a lock the run could not take, once per log.
func (l *Log) warnOnce(text string) {
	if l.lockWarned {
		return
	}
	l.lockWarned = true
	l.warn(text)
}

func (l *Log) render(kind string, fields []kv, detail string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] %s:", l.now().Format(time.RFC3339), kind)
	for _, f := range fields {
		if f.value == "" {
			continue
		}
		fmt.Fprintf(&b, " %s=%s", f.key, quoteIfNeeded(f.value))
	}
	b.WriteString("\n")
	for line := range strings.SplitSeq(strings.TrimRight(detail, "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		b.WriteString(indent)
		b.WriteString(strings.TrimRight(line, " \t"))
		b.WriteString("\n")
	}
	return b.String()
}

// appendFile writes the entry in a single call, so even an append that lost the
// lock lands whole rather than interleaved with another writer's bytes.
func appendFile(path, entry string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(entry); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

type kv struct {
	key   string
	value string
}

func (f Finding) fields() []kv {
	return []kv{
		{"reviewer", f.Reviewer},
		{"severity", f.Severity},
		{"location", f.location()},
		{"requirement", f.Requirement},
	}
}

func (f Finding) location() string {
	if f.File == "" {
		return ""
	}
	if f.Line > 0 {
		return f.File + ":" + strconv.Itoa(f.Line)
	}
	return f.File
}

func ensureIgnoreRule(dir string) error {
	path := filepath.Join(dir, ignoreFile)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if err := os.WriteFile(path, []byte(ignoreBody), 0o600); err != nil {
		return fmt.Errorf("write progress ignore rule %s: %w", path, err)
	}
	return nil
}

// FilePrefix starts the name of every file a log writes, so a caller can tell
// rrev's own run artifacts apart from a directory's other contents.
const FilePrefix = "progress-"

// fileName keeps one log per change, so concurrent runs against different
// changes never share a file.
func fileName(change string) string {
	return FilePrefix + slug(change) + ".md"
}

func slug(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	trimmed := strings.Trim(b.String(), "-.")
	if trimmed == "" {
		return "change"
	}
	return trimmed
}

func quoteIfNeeded(v string) string {
	if strings.ContainsAny(v, " \t\"\n") {
		return strconv.Quote(v)
	}
	return v
}

func iterationCount(n, limit int) string {
	if limit > 0 {
		return fmt.Sprintf("%d/%d", n, limit)
	}
	return strconv.Itoa(n)
}
