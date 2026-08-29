// Package progress appends structured run history to the per-change progress log.
package progress

import (
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ignoreFile keeps the pipeline's own commits from picking up run artifacts.
const ignoreFile = ".gitignore"

const ignoreBody = "# rrev progress logs are run artifacts, not source.\n*\n"

// indent prefixes the continuation lines of an entry, so every entry starts at
// column zero with its timestamp and a reader can split the log on that.
const indent = "  "

// noReasonGiven stands in for a rejection the executor reported without one.
const noReasonGiven = "(no reason given)"

// RunInfo identifies what a run reviewed, written once at the top of a run.
type RunInfo struct {
	Change  string
	Goal    string
	BaseRef string
	Head    string
	Mode    string
}

// Finding is one reported issue. ReRaises carries the executor's own judgement
// that this is something already in the log; rrev never works that out itself,
// because file, line and wording all drift between iterations while the finding
// stays the same.
type Finding struct {
	// ReRaises names the ledger entry the executor declared this finding
	// re-raises. Empty means the executor declared nothing, which is recorded
	// as a new finding.
	ReRaises    string
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

	// ledger holds one entry per distinct finding for the whole run, spanning
	// phases: a later phase re-raises what an earlier one rejected.
	ledger map[string]*ledgerEntry
	order  []string
	seq    int

	// ledgerAt is where this run's ledger section starts in the file, or -1
	// when none has been written. Truncating to a remembered offset is what
	// keeps a re-render from ever touching content rrev did not write, which
	// includes a log left behind in the older unstructured format.
	ledgerAt int64
	// lastSize is the file size this log left behind. A file that has grown
	// since means another writer appended, and rewinding over its records to
	// refresh a ledger would destroy them, so the ledger is dropped instead.
	lastSize int64

	cur *iteration
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
		ledger:   map[string]*ledgerEntry{},
		ledgerAt: -1,
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
	size, statErr := f.Stat()
	if err := errors.Join(statErr, f.Close()); err != nil {
		return Disabled(), fmt.Errorf("open progress log %s: %w", log.path, err)
	}
	// Whatever is already here was written by an earlier run, possibly in the
	// older unstructured format. Starting the tail at its end is what makes the
	// first ledger append after it rather than rewind into it.
	log.lastSize = size.Size()
	log.seq = seedSeq(log.path)
	return log, nil
}

// Disabled returns a log that records nothing.
func Disabled() *Log { return &Log{ledger: map[string]*ledgerEntry{}, ledgerAt: -1} }

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
	if !l.Enabled() {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n# Run: %s\n\n", orUnknown(info.Change))
	if info.Goal != "" {
		fmt.Fprintf(&b, "%s\n\n", oneLine(info.Goal))
	}
	fmt.Fprintf(&b, "- base `%s` … head `%s`\n- mode %s\n- started %s\n",
		orUnknown(info.BaseRef), orUnknown(info.Head), orUnknown(info.Mode), l.stamp())
	l.emit(b.String())
}

// PhaseStart records a phase boundary, closing any iteration still open so its
// summary lands before the next phase's heading.
func (l *Log) PhaseStart(phase string) {
	if !l.Enabled() {
		return
	}
	l.closeIteration()
	l.emit(fmt.Sprintf("\n## Phase: %s\n", phase))
}

// IterationStart opens a titled section for an iteration. Its timestamp is the
// only one the iteration carries: the entries inside it are all covered by this
// boundary, and repeating a stamp that changes once per iteration on every one
// of twenty entries is noise a reader has to look past.
func (l *Log) IterationStart(phase string, n, limit int) {
	if !l.Enabled() {
		return
	}
	l.closeIteration()
	l.mu.Lock()
	l.cur = &iteration{phase: phase, n: n, confirmed: map[string]int{}}
	l.mu.Unlock()
	l.emit(fmt.Sprintf("\n### %s · iteration %s · %s\n", phase, iterationCount(n, limit), l.stamp()))
}

// Finding records an issue a reviewer reported, before it has been judged.
func (l *Log) Finding(f Finding) {
	if !l.Enabled() {
		return
	}
	e, note := l.track(f, reported, "")
	l.emit(l.bullet("reported", f, e) + "\n" + noteLine(note))
}

// Confirmed records a finding the executor verified against real code, with
// what it did about it. Confirming a finding that was previously rejected
// retires its ledger entry, so a reader is never told a fixed issue is still
// standing.
func (l *Log) Confirmed(f Finding, action string) {
	if !l.Enabled() {
		return
	}
	e, note := l.track(f, confirmed, "")
	suffix := ""
	if action != "" {
		suffix = " — " + action
	}
	l.emit(l.bullet("confirmed", f, e) + suffix + "\n" + noteLine(note))
}

// Rejected records a finding dismissed as a false positive. The reason is the
// load-bearing part: a later reviewer reads it and must either accept it or
// argue with it instead of re-reporting the finding unchanged.
func (l *Log) Rejected(f Finding, reason string) {
	if !l.Enabled() {
		return
	}
	// A rejection whose reason went missing still has to reach the ledger:
	// withheld from later reviewers, it is the one finding guaranteed to be
	// raised again unchanged.
	if strings.TrimSpace(reason) == "" {
		reason = noReasonGiven
	}
	e, note := l.track(f, rejected, reason)
	l.emit(l.bullet("rejected", f, e) + "\n" + indent + oneLine(reason) + "\n" + noteLine(note))
}

// ExternalTool records that an external review tool ran and what came back.
// A phase that converges in silence and one whose tool died quietly look
// identical in the log otherwise, and they call for opposite responses.
func (l *Log) ExternalTool(tool, outcome, detail string) {
	if !l.Enabled() {
		return
	}
	line := fmt.Sprintf("\n- external tool `%s`: %s\n", orUnknown(tool), orUnknown(outcome))
	if detail != "" {
		line += indent + oneLine(detail) + "\n"
	}
	l.emit(line)
}

// Validation records what the executor reported about the validation command it
// ran before committing, which is the only account of it a later reader gets.
func (l *Log) Validation(v Validation) {
	if !l.Enabled() {
		return
	}
	l.mu.Lock()
	if l.cur != nil {
		l.cur.validation = v.Outcome
	}
	l.mu.Unlock()
	line := fmt.Sprintf("\n- validation **%s** `%s`\n", orUnknown(v.Outcome), v.Command)
	if v.Detail != "" {
		line += indent + oneLine(v.Detail) + "\n"
	}
	l.emit(line)
}

// Commit records a commit the pipeline made.
func (l *Log) Commit(hash, subject string) {
	if !l.Enabled() {
		return
	}
	l.mu.Lock()
	if l.cur != nil {
		l.cur.commit = shortHash(hash)
	}
	l.mu.Unlock()
	l.emit(fmt.Sprintf("- commit `%s` %s\n", shortHash(hash), subject))
}

// LoopEnd records which condition ended a loop and how many iterations ran.
func (l *Log) LoopEnd(phase, reason string, iterations int) {
	if !l.Enabled() {
		return
	}
	l.closeIteration()
	l.emit(fmt.Sprintf("\n**%s ended:** %s after %d iteration(s)\n", phase, reason, iterations))
}

// Note records anything else worth reconstructing later, such as a skipped
// phase or a degraded context.
func (l *Log) Note(text string) {
	if !l.Enabled() {
		return
	}
	l.emit("\n- note: " + oneLine(text) + "\n")
}

// disposition is what the executor decided about a finding. It settles both how
// the ledger entry stands and which of the iteration's counters moves, so the
// two can never disagree about the same record.
type disposition int

const (
	reported disposition = iota
	confirmed
	rejected
)

// track settles the finding's ledger entry and folds what it reports into it,
// under one lock: resolving an entry and counting it in two acquisitions would
// let a concurrent recorder interleave between them.
func (l *Log) track(f Finding, d disposition, rationale string) (*ledgerEntry, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, note := l.resolve(f)
	e.addLocation(f.location())
	if e.Claim == "" {
		e.Claim = f.Summary
	}
	switch d {
	case confirmed:
		e.Confirmed = true
	case rejected:
		// The rationale that first settled the question is the one a reviewer
		// has to answer. A later re-rejection tends to restate it as "as
		// recorded above", which would hollow out every prompt built from it.
		// The placeholder is not such a rationale: it settles nothing, so a
		// real reason arriving later replaces it.
		if e.Rationale == "" || e.Rationale == noReasonGiven {
			e.Rationale = rationale
		}
		// Confirming is not final: a finding fixed in one iteration and
		// re-raised in the next is rejected again, and keeping it retired
		// would withhold that reason from every later reviewer.
		e.Confirmed = false
	case reported:
	}
	if l.cur == nil {
		return e, note
	}
	e.addRaise(raise{Phase: l.cur.phase, Iteration: l.cur.n})
	switch d {
	case confirmed:
		l.cur.countConfirmed(f)
	case rejected:
		// A note means the declared id did not resolve, so a new entry was
		// opened; counting that as a re-raise would contradict the entry
		// beside it and inflate the recurrence rate a reader judges by.
		l.cur.countRejected(f.ReRaises != "" && note == "")
	case reported:
	}
	return e, note
}

// bullet renders one finding as a list item. The identifier leads because it is
// what a reviewer has to quote back, and what a reader follows into the ledger.
func (l *Log) bullet(kind string, f Finding, e *ledgerEntry) string {
	var b strings.Builder
	fmt.Fprintf(&b, "- **%s** `%s`", kind, e.ID)
	if f.Severity != "" {
		fmt.Fprintf(&b, " %s", f.Severity)
	}
	if loc := f.location(); loc != "" {
		fmt.Fprintf(&b, " `%s`", loc)
	}
	if f.Requirement != "" {
		fmt.Fprintf(&b, " (%s)", f.Requirement)
	}
	if f.Reviewer != "" {
		fmt.Fprintf(&b, " — %s", f.Reviewer)
	}
	if kind != "rejected" && f.Summary != "" {
		fmt.Fprintf(&b, ": %s", oneLine(f.Summary))
	}
	return b.String()
}

func noteLine(note string) string {
	if note == "" {
		return ""
	}
	return indent + "note: " + note + "\n"
}

// iteration accumulates what an iteration did, so its section can close with a
// summary a reader can take in without counting entries by hand.
type iteration struct {
	phase         string
	n             int
	confirmed     map[string]int
	unclassified  int
	newRejects    int
	repeatRejects int
	validation    string
	commit        string
}

// countConfirmed buckets by severity, keeping a finding that arrived with no
// severity or no location out of the severity counts entirely: folding it into
// one would misreport it, and dropping it would hide it.
func (it *iteration) countConfirmed(f Finding) {
	if f.Severity == "" || f.location() == "" {
		it.unclassified++
		return
	}
	it.confirmed[f.Severity]++
}

func (it *iteration) countRejected(reRaise bool) {
	if reRaise {
		it.repeatRejects++
		return
	}
	it.newRejects++
}

func (it *iteration) total() int {
	n := it.unclassified
	for _, c := range it.confirmed {
		n += c
	}
	return n
}

// summary is the iteration's closing line.
func (it *iteration) summary() string {
	parts := []string{fmt.Sprintf("confirmed %d%s", it.total(), it.severityBreakdown())}
	parts = append(parts, fmt.Sprintf("rejected %d (%d new, %d repeat)",
		it.newRejects+it.repeatRejects, it.newRejects, it.repeatRejects))
	if it.validation != "" {
		parts = append(parts, "validation "+it.validation)
	}
	if it.commit != "" {
		parts = append(parts, "commit "+it.commit)
	}
	return "\n_" + strings.Join(parts, " · ") + "_\n"
}

func (it *iteration) severityBreakdown() string {
	if len(it.confirmed) == 0 && it.unclassified == 0 {
		return ""
	}
	keys := slices.Sorted(maps.Keys(it.confirmed))
	parts := make([]string, 0, len(keys)+1)
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%d %s", it.confirmed[k], k))
	}
	if it.unclassified > 0 {
		parts = append(parts, fmt.Sprintf("%d unclassified", it.unclassified))
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

// closeIteration writes the open iteration's summary, if one is open.
func (l *Log) closeIteration() {
	if !l.Enabled() {
		return
	}
	l.mu.Lock()
	it := l.cur
	l.cur = nil
	l.mu.Unlock()
	if it == nil {
		return
	}
	l.emit(it.summary())
}

func (l *Log) stamp() string {
	if l == nil || l.now == nil {
		return ""
	}
	return l.now().Format(time.RFC3339)
}

// emit writes one record and re-renders the ledger behind it, both under the
// cross-process lock. The ledger sits at the end of the file and is replaced by
// truncating to the offset where rrev last wrote it, so a re-render can never
// disturb a byte rrev did not write — including a log left behind in the older
// unstructured format.
//
// A record that cannot take the lock in time is still written, because an
// unserialized append risks interleaving whereas dropping it loses history the
// reader needs. That append skips the ledger and forgets the offset: rewinding
// a file another writer may have extended would destroy its records, and a
// stale ledger section is a far cheaper loss.
func (l *Log) emit(record string) {
	if !l.Enabled() {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	locked := false
	switch err := l.lock.acquire(l.lockWait); {
	case err == nil:
		locked = true
		defer l.lock.release()
	case errors.Is(err, errLockBusy):
		l.warnOnce(fmt.Sprintf("progress log %s is busy after %s; appending without the lock", l.path, l.lockWait))
	default:
		l.warnOnce(fmt.Sprintf("progress log lock %s: %v; appending without the lock", l.lock.path, err))
	}

	if !locked {
		l.ledgerAt, l.lastSize = -1, -1
		if err := appendFile(l.path, record); err != nil {
			l.warn(fmt.Sprintf("write progress log: %v", err))
		}
		return
	}
	if err := l.rewriteTail(record); err != nil {
		l.warn(fmt.Sprintf("write progress log: %v", err))
	}
}

// rewriteTail drops the previous ledger section, appends the record, and writes
// the current ledger after it.
func (l *Log) rewriteTail(record string) (err error) {
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, f.Close()) }()

	end, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return err
	}
	owned := end == l.lastSize
	if owned && l.ledgerAt >= 0 {
		if err := f.Truncate(l.ledgerAt); err != nil {
			return err
		}
		end = l.ledgerAt
	}
	if _, err := f.WriteAt([]byte(record), end); err != nil {
		return err
	}
	end += int64(len(record))

	ledger := l.renderLedger()
	if ledger == "" || !owned {
		l.ledgerAt, l.lastSize = -1, end
		return nil
	}
	l.ledgerAt = end
	if _, err := f.WriteAt([]byte("\n"+ledger), end); err != nil {
		return err
	}
	l.lastSize = end + int64(len("\n"+ledger))
	return nil
}

// warnOnce reports a lock the run could not take, once per log.
func (l *Log) warnOnce(text string) {
	if l.lockWarned {
		return
	}
	l.lockWarned = true
	l.warn(text)
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

func iterationCount(n, limit int) string {
	if limit > 0 {
		return fmt.Sprintf("%d/%d", n, limit)
	}
	return strconv.Itoa(n)
}

// orUnknown keeps a missing value from rendering as a gap a reader must guess at.
func orUnknown(v string) string {
	if v == "" {
		return "unknown"
	}
	return v
}

// shortHash renders a commit at the length a human reads it.
func shortHash(hash string) string {
	if len(hash) > 7 {
		return hash[:7]
	}
	return hash
}
