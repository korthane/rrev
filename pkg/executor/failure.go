package executor

import (
	"context"
	"errors"
	"fmt"
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
	if reason, ok := match(lines, rateLimitPatterns); ok {
		return &LimitError{Tool: tool, Reason: reason, Err: err}
	}
	if reason, ok := match(lines, retryablePatterns); ok {
		return &LimitError{Tool: tool, Reason: reason, Retryable: true, Err: err}
	}
	return err
}

// refusalLines bounds how much a call that exited zero may have said and still
// be read as a refusal: a throttled tool prints its message and stops, while a
// review that ran says far more.
const refusalLines = 5

// refusal reports whether an exit-zero call looks like a provider refusal
// rather than a review: it carried no signal, reported nothing, and said too
// little to have reviewed anything.
func refusal(result Result) bool {
	return !reviewed(result) && len(spoken(result.Output)) <= refusalLines
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
	lines := spoken(result.Output)
	if failure, ok := errors.AsType[*Error](err); ok && failure.Stderr != "" {
		lines = append(lines, strings.Split(failure.Stderr, "\n")...)
	}
	return lines
}

// spoken drops fenced blocks, markdown list, quote and heading lines, and the
// pipe-delimited report lines the prompts mandate: a reviewer citing "rate limit
// exceeded" from the code under review must not be mistaken for the provider
// refusing the call.
func spoken(output string) []string {
	var lines []string
	fenced := false
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSpace(line)
		if isFence(line) {
			fenced = !fenced
			continue
		}
		if line == "" || fenced || isQuotedLine(line) || isReportLine(line) {
			continue
		}
		lines = append(lines, line)
	}
	return lines
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
