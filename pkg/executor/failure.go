package executor

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Sentinels for calls that produced no reviewable result, so a phase can tell
// a throttled or flaky call apart from a review that found nothing.
var (
	ErrRateLimited = errors.New("provider usage limit reached")
	ErrRetryable   = errors.New("transient executor failure")
)

// LimitError reports a call the provider refused or could not complete. A phase
// must never record it as a converged iteration.
type LimitError struct {
	Tool string
	// Reason is the tool's own line that identified the failure.
	Reason    string
	Retryable bool
	// Output is what a call that exited zero said before refusing. Such a
	// call carries no *Error, so this is the only tail its record can have.
	Output string
	// Err is the failure the classification was made from, kept so the exit
	// status, stderr tail, or timeout underneath it stays reachable.
	Err error
}

func (e *LimitError) Unwrap() error { return e.Err }

func (e *LimitError) Error() string {
	kind := "usage limit reached"
	if e.Retryable {
		kind = "transient failure"
	}
	return fmt.Sprintf("%s: %s: %s", e.Tool, kind, e.Reason)
}

// Is matches whichever sentinel describes the failure.
func (e *LimitError) Is(target error) bool {
	if e.Retryable {
		return target == ErrRetryable
	}
	return target == ErrRateLimited
}

// rateLimitPatterns identify a provider refusing to serve the call at all.
var rateLimitPatterns = []string{
	"usage limit reached",
	"hit your usage limit",
	// Claude throttles with "session limit", not "usage limit": without these
	// its refusal is recorded as a plain crash.
	"session limit reached",
	"hit your session limit",
	"rate limit exceeded",
	"rate_limit_error",
	"429 too many requests",
	"quota exceeded",
}

// retryablePatterns identify a failure the tool itself suggests retrying.
var retryablePatterns = []string{
	"overloaded_error",
	"service unavailable",
	"temporarily unavailable",
	"connection reset by peer",
	"internal server error",
	"please try again",
}

// classify upgrades a finished call to a rate-limit or transient failure when
// the tool said so, and otherwise leaves the original error alone.
func classify(tool string, result Result, err error) error {
	// A bound rrev enforced itself, or a run the user aborted, is not the
	// provider refusing: what such a call captured is partial review prose.
	if errors.Is(err, ErrTimeout) || errors.Is(err, context.Canceled) {
		return err
	}
	if err == nil && !refusal(result) {
		return nil
	}
	lines := diagnostics(result, err)
	// Only for a call that exited zero: a failure carries its own tail on the
	// *Error beneath, and the two would render the same text twice.
	var said string
	if err == nil {
		said = result.Output
	}
	if reason, ok := match(lines, rateLimitPatterns); ok {
		return &LimitError{Tool: tool, Reason: reason, Output: said, Err: err}
	}
	if reason, ok := match(lines, retryablePatterns); ok {
		return &LimitError{Tool: tool, Reason: reason, Retryable: true, Output: said, Err: err}
	}
	return err
}

// refusalLines bounds how much a call that exited zero may have said and still
// be read as a refusal: a throttled tool prints its message and stops, while a
// review that ran says far more.
const refusalLines = 5

// refusal reports whether an exit-zero call looks like a provider refusal
// rather than a review: it carried no signal, reported nothing, and said too
// little to have reviewed anything. The bound counts the lines the tool wrote,
// not the frames it painted over one of them: a progress bar redrawing with a
// carriage return says one line's worth, and counting each frame would put a
// throttled call over the bound and leave its refusal unclassified.
func refusal(result Result) bool {
	return !reviewed(result) && len(speech(cleanLines(result.Output))) <= refusalLines
}

// reviewed reports whether the call produced a review rather than a refusal. A
// throttled call writes neither a signal nor a report line, which is what keeps
// an exit-zero refusal classifiable.
func reviewed(result Result) bool {
	if result.Signal != "" {
		return true
	}
	for line := range strings.SplitSeq(result.Output, "\n") {
		if isReportLine(line) {
			return true
		}
	}
	return false
}

// diagnostics collects the lines a tool spoke in its own voice: everything it
// wrote to stderr, plus the parts of its output that are not quoted material.
func diagnostics(result Result, err error) []string {
	lines := speech(flatLines(result.Output))
	if failure, ok := errors.AsType[*Error](err); ok && failure.Stderr != "" {
		lines = append(lines, flatLines(failure.Stderr)...)
	}
	return lines
}

// speech drops fenced blocks, markdown list, quote and heading lines, and the
// pipe-delimited report lines the prompts mandate: a reviewer citing "rate limit
// exceeded" from the code under review must not be mistaken for the provider
// refusing the call. It takes lines already split, so the classifier and the
// refusal bound can disagree about what counts as a line without disagreeing
// about which lines are the tool's own voice.
func speech(lines []string) []string {
	var kept []string
	fenced := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if isFence(line) {
			fenced = !fenced
			continue
		}
		if line == "" || fenced || isQuotedLine(line) || isReportLine(line) {
			continue
		}
		kept = append(kept, line)
	}
	return kept
}

func isQuotedLine(line string) bool {
	return line != "" && strings.ContainsRune("-*>#|", rune(line[0]))
}

// isReportLine spots the pipe-delimited FINDING/REJECTED lines a review speaks
// in. A provider refusing a call never writes one.
func isReportLine(line string) bool { return strings.Contains(line, " | ") }

func match(lines, patterns []string) (string, bool) {
	for _, line := range lines {
		lower := strings.ToLower(line)
		if slices.ContainsFunc(patterns, func(p string) bool { return strings.Contains(lower, p) }) {
			return strings.TrimSpace(line), true
		}
	}
	return "", false
}

// Cause is what a failed call can tell a reader without being re-run: how the
// failure was classified, the exit status, and the diagnostic tail. It is the
// one rendering both the progress log and the console use, so the two agree.
type Cause struct {
	Tool string
	// Kind names the classification: usage limit, transient failure, timeout,
	// cancelled, or failure.
	Kind     string
	ExitCode int
	// Reason is the line that explains the end: the refusal the classifier
	// matched, the bound that expired, or the error a call with no exit
	// status wrapped. A refusal that exited zero has no other trace.
	Reason string
	// Tail is the diagnostic text: the tool's stderr, or the last lines of its
	// stdout when stderr is empty.
	Tail string
	// Truncated and ReasonCut say which text the line bound cut, so the marker
	// renders above the one that is missing lines rather than above both.
	Truncated bool
	ReasonCut bool
}

// causeTailLines bounds the rendered tail. A crashing tool puts its reason in
// its last few lines; anything above that is the review it was giving up on.
const causeTailLines = 20

// omittedMark stands in for the lines causeTailLines dropped.
const omittedMark = "[earlier lines omitted]"

// Describe reduces a failed call's error to its cause.
func Describe(err error) Cause {
	c := Cause{Kind: "failure", ExitCode: -1}
	switch {
	case errors.Is(err, context.Canceled):
		c.Kind = "cancelled"
	case errors.Is(err, ErrTimeout):
		c.Kind = "timeout"
	case errors.Is(err, ErrRateLimited):
		c.Kind = "usage limit"
	case errors.Is(err, ErrRetryable):
		c.Kind = "transient failure"
	}
	if limit, ok := errors.AsType[*LimitError](err); ok {
		c.Tool, c.Reason = limit.Tool, limit.Reason
		// A refusal that exited zero has no *Error beneath it to take a tail
		// from, and the matched line alone drops the rest of what it said —
		// the reset time a reader needs to know when to resume.
		c.Tail, c.Truncated = lastLines(bounded(limit.Output), causeTailLines)
	}
	if timeout, ok := errors.AsType[*TimeoutError](err); ok {
		c.Tool, c.Reason = timeout.Tool, timeout.Error()
	}
	failure, ok := errors.AsType[*Error](err)
	if !ok {
		if c.Reason == "" && err != nil {
			c.Reason = flat(err.Error())
		}
		return c
	}
	c.Tool, c.ExitCode = failure.Tool, failure.ExitCode
	source := failure.Stderr
	if strings.TrimSpace(source) == "" {
		source = failure.Output
	}
	c.Tail, c.Truncated = lastLines(bounded(source), causeTailLines)
	// A known exit status is already in the summary, and the wrapped error
	// then says nothing more. Without one — a start failure, a signal, output
	// rrev could not read — the wrapped error is what explains the end, and
	// the tail beneath it is the review the tool was giving up on.
	if failure.ExitCode < 0 && c.Reason == "" && failure.Err != nil && !errors.Is(failure.Err, context.Canceled) {
		// Bounded and trimmed exactly as the tail is: a wrapped error carrying
		// the whole of what the tool said would otherwise flood the record the
		// bound exists to keep small, and a reason normalised differently from
		// the tail it is compared against would not match the text it repeats.
		//
		// Against the tail's last line, not the whole tail: claude appends its
		// result message to the stderr it captured, so the tail ends with the
		// <msg> that "claude reported an error: <msg>" ends with, and matching
		// whole strings would render the message twice under one warning line.
		reason, cut := lastLines(bounded(failure.Err.Error()), causeTailLines)
		if last := lastLine(c.Tail); last == "" || !strings.HasSuffix(reason, last) {
			c.Reason, c.ReasonCut = reason, cut
		}
	}
	return c
}

// bounded applies the capture bound to text that did not pass through the
// process reader: a failure the model signalled carries its whole output.
// Text the reader already bounded passes unchanged.
func bounded(text string) string {
	w := &tailWriter{limit: stderrTailBytes}
	_, _ = w.Write([]byte(text))
	return w.String()
}

// flat is text with the same terminal noise removed that the tail's lines
// have, so a reason is matched against the tail and rendered on equal terms.
func flat(text string) string {
	return strings.TrimSpace(strings.Join(flatLines(text), "\n"))
}

// lastLines keeps the final n non-blank lines of text.
func lastLines(text string, n int) (string, bool) {
	var lines []string
	for _, line := range flatLines(text) {
		if strings.TrimSpace(line) != "" {
			lines = append(lines, strings.TrimRight(line, " \t"))
		}
	}
	if len(lines) <= n {
		return strings.Join(lines, "\n"), false
	}
	return strings.Join(lines[len(lines)-n:], "\n"), true
}

// lastLine is the final line of text, empty only when text is: lastLines
// leaves no blank line for it to land on.
func lastLine(text string) string {
	return text[strings.LastIndexByte(text, '\n')+1:]
}

// flatLines splits text into lines with terminal noise flattened: a carriage
// return a progress bar redraws with becomes a line break, and escape
// sequences go. The classifier matches and the tails render on these same
// lines, so the log and the console show what the tool said rather than how
// it painted it, and the matched reason is found in the tail it came from.
func flatLines(text string) []string { return cleanLines(redraws.Replace(text)) }

// cleanLines splits on the line breaks the tool wrote, leaving a line a
// progress bar repainted as the one line it is. Escape sequences go, and so do
// the control characters left over — a bare carriage return among them, which
// is why normalising CRLF first would change nothing. Only the refusal bound
// reads lines this way; everything else goes through flatLines, which reads a
// repainted line as the separate lines it renders as.
func cleanLines(text string) []string {
	text = ansiSequence.ReplaceAllString(text, "")
	var lines []string
	for line := range strings.SplitSeq(text, "\n") {
		lines = append(lines, strings.Map(printable, line))
	}
	return lines
}

var (
	redraws = strings.NewReplacer("\r\n", "\n", "\r", "\n")
	// CSI sequences (colour, cursor) and OSC ones (window title, hyperlinks).
	ansiSequence = regexp.MustCompile(`\x1b(?:\[[0-9;?]*[ -/]*[@-~]|\][^\x07\x1b]*(?:\x07|\x1b\\))`)
)

// printable drops the control characters left once line breaks and escape
// sequences are handled; a tab is kept as the indentation it is.
func printable(r rune) rune {
	if r < 0x20 && r != '\t' || r == 0x7f {
		return -1
	}
	return r
}

// headline is what a failure no tool owns and nothing classified has to say
// for itself: the error's first line, where the summary would otherwise read
// as a bare "failure" under the log's "failed" marker.
func (c Cause) headline() string {
	if c.Tool != "" || c.Kind != "failure" {
		return ""
	}
	head, _, _ := strings.Cut(c.Reason, "\n")
	return head
}

// Summary is the one-line form: tool, classification, and exit status.
func (c Cause) Summary() string {
	if head := c.headline(); head != "" {
		return head
	}
	var b strings.Builder
	if c.Tool != "" {
		b.WriteString(c.Tool + ": ")
	}
	b.WriteString(c.Kind)
	if c.ExitCode >= 0 {
		fmt.Fprintf(&b, " (exit %d)", c.ExitCode)
	}
	return b.String()
}

// Detail is the diagnostic tail, marked when it was cut. The classifier's
// reason leads it unless the tail already holds that line: the matched line
// may sit above the bound, and then it is the one line worth keeping.
func (c Cause) Detail() string {
	var lines []string
	reason := c.Reason
	if c.headline() != "" {
		_, reason, _ = strings.Cut(c.Reason, "\n")
	}
	if reason != "" && !strings.Contains(c.Tail, reason) {
		if c.ReasonCut {
			lines = append(lines, omittedMark)
		}
		lines = append(lines, reason)
	}
	if c.Tail != "" {
		if c.Truncated {
			lines = append(lines, omittedMark)
		}
		lines = append(lines, c.Tail)
	}
	return strings.Join(lines, "\n")
}
