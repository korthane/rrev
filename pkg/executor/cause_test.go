package executor_test

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/korthane/rrev/pkg/executor"
)

// claude reports its own error on stdout and exits silently on stderr. Without
// the stdout tail such a failure is an exit status and nothing else.
func TestFailingToolSilentOnStderrKeepsItsStdoutTail(t *testing.T) {
	tool := newFakeTool(t, fakeToolOpts{stdout: "Reviewing.\nError: context window exceeded\n", exit: 1})

	_, err := (executor.Claude{Command: tool.path}).Run(t.Context(), executor.Request{Prompt: "review", Dir: t.TempDir()})
	failure, ok := errors.AsType[*executor.Error](err)
	if !ok {
		t.Fatalf("err = %v, want *executor.Error", err)
	}
	if failure.Stderr != "" {
		t.Errorf("stderr = %q, want empty", failure.Stderr)
	}
	if !strings.Contains(failure.Output, "context window exceeded") {
		t.Errorf("output tail = %q, want the tool's own error line", failure.Output)
	}
	if failure.ExitCode != 1 {
		t.Errorf("exit = %d, want 1", failure.ExitCode)
	}
}

func TestDescribePrefersStderrOverStdout(t *testing.T) {
	err := &executor.Error{Tool: "claude", ExitCode: 2, Stderr: "fatal: no api key", Output: "some review prose"}

	c := executor.Describe(err)
	if c.Summary() != "claude: failure (exit 2)" {
		t.Errorf("summary = %q", c.Summary())
	}
	if c.Detail() != "fatal: no api key" {
		t.Errorf("detail = %q, want the stderr tail alone", c.Detail())
	}
}

func TestDescribeFallsBackToStdoutWhenStderrIsEmpty(t *testing.T) {
	err := &executor.Error{Tool: "claude", ExitCode: 1, Output: "line one\n\nError: context window exceeded"}

	c := executor.Describe(err)
	if want := "line one\nError: context window exceeded"; c.Detail() != want {
		t.Errorf("detail = %q, want %q", c.Detail(), want)
	}
}

// Stderr holding only terminal noise is non-empty bytes that flatten to
// nothing, so choosing the source on the raw text would leave the record with
// the exit status alone — the state the stdout fallback exists to prevent.
func TestNoiseOnlyStderrStillFallsBackToStdout(t *testing.T) {
	for _, stderr := range []string{"\x1b[2K\r\x1b[2K\r", "\x1b[?25l\x1b[?25h", "\x00\x00"} {
		err := &executor.Error{Tool: "codex", ExitCode: 1, Stderr: stderr, Output: "Reviewing.\nError: context window exceeded"}
		want := "Reviewing.\nError: context window exceeded"
		if got := executor.Describe(err).Detail(); got != want {
			t.Errorf("detail with stderr %q = %q, want %q", stderr, got, want)
		}
	}
}

// The error shapes here are the ones the executors actually return: a timeout
// is a *TimeoutError wrapped by the process failure, a cancellation is the
// process failure wrapping context.Canceled, and a refusal that exited zero is
// a *LimitError with nothing underneath.
func TestDescribeNamesTheClassification(t *testing.T) {
	base := &executor.Error{Tool: "claude", ExitCode: 1, Stderr: "rate limit exceeded"}
	timeout := &executor.TimeoutError{Tool: "claude", Limit: time.Minute, Idle: true}
	for _, tc := range []struct {
		err  error
		want string
	}{
		{&executor.LimitError{Tool: "claude", Reason: "rate limit exceeded", Err: base}, "claude: usage limit (exit 1)"},
		{&executor.LimitError{Tool: "claude", Reason: "overloaded", Retryable: true, Err: base}, "claude: transient failure (exit 1)"},
		{&executor.LimitError{Tool: "claude", Reason: "hit your usage limit"}, "claude: usage limit"},
		{fmt.Errorf("claude: %w", &executor.Error{Tool: "claude", ExitCode: -1, Err: timeout}), "claude: timeout"},
		{timeout, "claude: timeout"},
		{fmt.Errorf("claude: %w", &executor.Error{Tool: "claude", ExitCode: -1, Err: context.Canceled}), "claude: cancelled"},
		{fmt.Errorf("claude: %w", context.Canceled), "cancelled"},
	} {
		if got := executor.Describe(tc.err).Summary(); got != tc.want {
			t.Errorf("Describe(%v).Summary() = %q, want %q", tc.err, got, tc.want)
		}
	}
}

// A refusal that exited zero has no stderr and no exit status: the matched
// line is the whole diagnosis, and dropping it leaves "usage limit" alone.
func TestDescribeKeepsTheRefusalLineOfAnExitZeroRefusal(t *testing.T) {
	c := executor.Describe(&executor.LimitError{Tool: "claude", Reason: "You've hit your usage limit. Try again at 3pm."})

	if c.Detail() != "You've hit your usage limit. Try again at 3pm." {
		t.Errorf("detail = %q, want the refusal line", c.Detail())
	}
}

// The matched line may sit above the tail's bound; it must still be shown,
// and shown once when it is already in the tail.
func TestDescribeLeadsWithTheReasonUnlessTheTailHoldsIt(t *testing.T) {
	inTail := &executor.LimitError{Tool: "claude", Reason: "rate limit exceeded",
		Err: &executor.Error{Tool: "claude", ExitCode: 1, Stderr: "rate limit exceeded\nexiting"}}
	if got := executor.Describe(inTail).Detail(); got != "rate limit exceeded\nexiting" {
		t.Errorf("detail = %q, want the reason once", got)
	}

	var lines []string
	for i := range 40 {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	aboveBound := &executor.LimitError{Tool: "claude", Reason: "overloaded_error", Retryable: true,
		Err: &executor.Error{Tool: "claude", ExitCode: 1, Output: "overloaded_error\n" + strings.Join(lines, "\n")}}
	got := executor.Describe(aboveBound).Detail()
	if !strings.HasPrefix(got, "overloaded_error\n[earlier lines omitted]\n") || !strings.HasSuffix(got, "line 39") {
		t.Errorf("detail = %q, want the reason, the cut marker, then the tail", got)
	}
}

// A timeout wrapped by the process failure keeps what the tool said before the
// bound cut it, and which bound that was.
func TestDescribeTimeoutKeepsTheBoundAndTheTail(t *testing.T) {
	timeout := &executor.TimeoutError{Tool: "claude", Limit: time.Minute, Idle: true}
	err := &executor.Error{Tool: "claude", ExitCode: -1, Output: "Reviewing.\nRunning the suite", Err: timeout}

	c := executor.Describe(err)
	if want := "claude produced no output for 1m0s\nReviewing.\nRunning the suite"; c.Detail() != want {
		t.Errorf("detail = %q, want %q", c.Detail(), want)
	}
	if c.Summary() != "claude: timeout" {
		t.Errorf("summary = %q", c.Summary())
	}
}

func TestDescribeBoundsTheTailAndSaysSo(t *testing.T) {
	var lines []string
	for i := range 60 {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	err := &executor.Error{Tool: "codex", ExitCode: 1, Stderr: strings.Join(lines, "\n")}

	c := executor.Describe(err)
	if !c.Truncated || !strings.HasPrefix(c.Detail(), "[earlier lines omitted]") {
		t.Errorf("a cut tail must say it was cut: %q", c.Detail())
	}
	if !strings.HasSuffix(c.Detail(), "line 59") || strings.Contains(c.Detail(), "line 0\n") {
		t.Errorf("the tail must keep the last lines, not the first: %q", c.Detail())
	}
}

// The bound counts non-blank lines: exactly twenty of them fit unmarked, and
// the twenty-first pushes the first out and says so.
func TestDescribeTailBoundIsExactAndSkipsBlankLines(t *testing.T) {
	numbered := func(n int) string {
		var lines []string
		for i := 1; i <= n; i++ {
			lines = append(lines, fmt.Sprintf("line %d", i), "", "   ")
		}
		return strings.Join(lines, "\n")
	}
	for _, tc := range []struct {
		lines     int
		truncated bool
		first     string
	}{
		{20, false, "line 1"},
		{21, true, "[earlier lines omitted]\nline 2"},
	} {
		c := executor.Describe(&executor.Error{Tool: "codex", ExitCode: 1, Stderr: numbered(tc.lines)})
		if c.Truncated != tc.truncated || !strings.HasPrefix(c.Detail(), tc.first) {
			t.Errorf("%d lines: truncated = %v, detail = %q; want truncated %v opening with %q",
				tc.lines, c.Truncated, c.Detail(), tc.truncated, tc.first)
		}
		if strings.Contains(c.Detail(), "\n\n") {
			t.Errorf("%d lines: blank lines must not survive into the tail: %q", tc.lines, c.Detail())
		}
	}
}

// The reason and the tail are bounded separately, so the marker has to sit
// above whichever of the two lost lines. Marking a complete tail tells the
// reader something is missing from the two lines that are all there is.
func TestCutMarkerSitsAboveTheTextItWasCutFrom(t *testing.T) {
	var reason strings.Builder
	for i := 1; i <= 40; i++ {
		fmt.Fprintf(&reason, "reason line %d\n", i)
	}
	c := executor.Describe(&executor.Error{Tool: "codex", ExitCode: -1,
		Stderr: "the tool's last word", Err: errors.New(reason.String())})

	if !strings.HasPrefix(c.Detail(), "[earlier lines omitted]\nreason line 21") {
		t.Errorf("a cut reason must be marked above itself: %q", c.Detail())
	}
	if want := "reason line 40\nthe tool's last word"; !strings.HasSuffix(c.Detail(), want) {
		t.Errorf("an uncut tail must follow the reason unmarked: %q", c.Detail())
	}
	if n := strings.Count(c.Detail(), "[earlier lines omitted]"); n != 1 {
		t.Errorf("marker appears %d times, want 1: %q", n, c.Detail())
	}
}

// A byte bound lands mid-line; the fragment before the first newline is not a
// line the tool wrote, and rendering it would put half a word in the log.
func TestStdoutTailStartsOnALineBoundary(t *testing.T) {
	var b strings.Builder
	for i := range 2000 {
		fmt.Fprintf(&b, "line %04d — still reviewing\n", i)
	}
	tool := newFakeTool(t, fakeToolOpts{stdout: b.String(), exit: 1})

	_, err := (executor.Claude{Command: tool.path}).Run(t.Context(), executor.Request{Prompt: "review", Dir: t.TempDir()})
	failure, ok := errors.AsType[*executor.Error](err)
	if !ok {
		t.Fatalf("err = %v, want *executor.Error", err)
	}
	// Bounded from both sides: an upper bound alone passes just as well with
	// the bound lowered to nothing, and a tail is only diagnostic when it is
	// as much of the tool's last words as the bound allows.
	if len(failure.Output) > tailBytes || len(failure.Output) < tailBytes/2 {
		t.Errorf("tail holds %d bytes, want close to the bound of %d", len(failure.Output), tailBytes)
	}
	first, _, _ := strings.Cut(failure.Output, "\n")
	if !strings.HasPrefix(first, "line ") || !strings.HasSuffix(first, "still reviewing") {
		t.Errorf("tail opens with a fragment %q, want a whole line", first)
	}
	if !strings.HasSuffix(failure.Output, "line 1999 — still reviewing") {
		t.Errorf("tail lost the last line: %q", failure.Output[max(0, len(failure.Output)-80):])
	}
}

// tailBytes is the byte bound both diagnostic tails are captured at.
const tailBytes = 8 << 10

// A last line longer than the byte bound — one minified error blob — has no
// earlier line boundary to start from. Keeping its end beats keeping nothing,
// on whichever stream it arrived.
func TestTailKeepsAnOversizedLastLine(t *testing.T) {
	line := "Error: " + strings.Repeat("é", 12000) + " END"
	for name, opts := range map[string]fakeToolOpts{
		"stdout": {stdout: "Reviewing.\n" + line + "\n", exit: 1},
		"stderr": {stderr: "Reviewing.\n" + line + "\n", exit: 1},
	} {
		t.Run(name, func(t *testing.T) {
			tool := newFakeTool(t, opts)

			_, err := (executor.Claude{Command: tool.path}).Run(t.Context(), executor.Request{Prompt: "review", Dir: t.TempDir()})
			failure, ok := errors.AsType[*executor.Error](err)
			if !ok {
				t.Fatalf("err = %v, want *executor.Error", err)
			}
			tail := failure.Output
			if name == "stderr" {
				tail = failure.Stderr
			}
			if !strings.HasSuffix(tail, "é END") {
				t.Errorf("tail lost the oversized last line: %q", tail[max(0, len(tail)-40):])
			}
			if !utf8.ValidString(tail) {
				t.Error("tail opens mid-rune")
			}
			if c := executor.Describe(err); !strings.HasSuffix(c.Detail(), "é END") {
				t.Errorf("detail = %q, want the line's end", c.Detail()[max(0, len(c.Detail())-40):])
			}
		})
	}
}

// A line over the scanner's bound ends the call without an exit status. The
// record still names the tool, leads with why, and keeps what came before;
// and the tool, still writing into a pipe nobody reads, must not hang Wait.
func TestScannerOverflowIsRecordedWithItsCause(t *testing.T) {
	blob := strings.Repeat("x", 9<<20)
	tool := newFakeTool(t, fakeToolOpts{stdout: "Reviewing.\n" + blob + "\nafter\n"})
	// The bound turns a missing drain into a failed test rather than a hang.
	custom := executor.Custom{Command: tool.path, Limits: executor.Limits{Session: 30 * time.Second}}

	_, err := custom.Run(t.Context(), executor.Request{Prompt: "review", Dir: t.TempDir()})

	failure, ok := errors.AsType[*executor.Error](err)
	if !ok || failure.ExitCode != -1 {
		t.Fatalf("err = %v, want *executor.Error without an exit status", err)
	}
	c := executor.Describe(err)
	if c.Summary() != "custom: failure" {
		t.Errorf("summary = %q", c.Summary())
	}
	if want := "read output: bufio.Scanner: token too long\nReviewing."; c.Detail() != want {
		t.Errorf("detail = %q, want %q", c.Detail(), want)
	}
}

// A failure no tool owns — a prompt that would not expand, an error from
// before any call — has only its own text to be summarised by; "failure"
// alone under the log's "failed" marker says nothing. The reason is shown
// with the same terminal noise removed as the tail.
func TestDescribeNamesTheErrorWhenNoToolOwnsIt(t *testing.T) {
	c := executor.Describe(errors.New("template external_eval.txt: \x1b[31munknown variable\x1b[0m"))
	if c.Summary() != "template external_eval.txt: unknown variable" || c.Detail() != "" {
		t.Errorf("summary = %q, detail = %q; want the error as the summary, once", c.Summary(), c.Detail())
	}
	owned := &executor.Error{Tool: "claude", ExitCode: -1, Output: "Reviewing.", Err: errors.New("signal: \x1b[1mkilled\x1b[0m")}
	c = executor.Describe(owned)
	if c.Summary() != "claude: failure" || c.Detail() != "signal: killed\nReviewing." {
		t.Errorf("summary = %q, detail = %q; want the tool's summary and a flattened reason", c.Summary(), c.Detail())
	}
	multi := executor.Describe(errors.New("template external_eval.txt: unknown variable\nnear line 12"))
	if multi.Summary() != "template external_eval.txt: unknown variable" || multi.Detail() != "near line 12" {
		t.Errorf("summary = %q, detail = %q; want the first line as the summary and the rest as detail", multi.Summary(), multi.Detail())
	}
}

// A failure the model signalled carries its whole output rather than the tail
// the process reader keeps, so the byte bound is applied when the cause is
// described: one pasted blob must not put kilobytes on a single log line.
func TestDescribeBoundsATailTheReaderDidNotCut(t *testing.T) {
	signalled := &executor.Error{Tool: "claude", ExitCode: -1,
		Output: "Reviewing.\n" + strings.Repeat("x", 9000) + "\nI cannot continue.\n<<<RREV:TASK_FAILED>>>",
		Err:    errors.New("reported <<<RREV:TASK_FAILED>>>")}
	c := executor.Describe(signalled)
	if len(c.Detail()) > 8<<10 {
		t.Errorf("detail is %d bytes, want at most 8 KiB", len(c.Detail()))
	}
	// The wrapped "reported <marker>" is dropped, not repeated: the model's own
	// last line is the marker, so the reason says nothing the tail does not.
	if want := "I cannot continue.\n<<<RREV:TASK_FAILED>>>"; c.Detail() != want {
		t.Errorf("detail = %q, want %q", c.Detail(), want)
	}
}

// A tool that exits non-zero having said nothing has only its exit status to
// show, and the summary already carries it; a call that never got an exit
// status keeps the error that explains why.
func TestDescribeDoesNotRepeatAKnownExitStatusAsTheTail(t *testing.T) {
	silent := &executor.Error{Tool: "claude", ExitCode: 1, Err: errors.New("exit status 1")}
	if c := executor.Describe(silent); c.Summary() != "claude: failure (exit 1)" || c.Detail() != "" {
		t.Errorf("summary = %q, detail = %q; want the exit status once, in the summary", c.Summary(), c.Detail())
	}
	unstarted := &executor.Error{Tool: "claude", ExitCode: -1, Err: errors.New(`exec: "claude": executable file not found in $PATH`)}
	if c := executor.Describe(unstarted); !strings.Contains(c.Detail(), "executable file not found") {
		t.Errorf("detail = %q, want the start failure", c.Detail())
	}
}

// A call that ended without an exit status while the tool was still talking —
// a line rrev could not read, a signal — has the wrapped error as its only
// explanation, and it must lead the tail rather than hide behind it.
func TestDescribeLeadsWithTheErrorWhenNoExitStatusExplainsIt(t *testing.T) {
	overflow := &executor.Error{Tool: "claude", ExitCode: -1,
		Output: "Reviewing the branch.\nStill reviewing.",
		Err:    fmt.Errorf("read output: %w", bufio.ErrTooLong)}
	c := executor.Describe(overflow)
	if c.Summary() != "claude: failure" {
		t.Errorf("summary = %q", c.Summary())
	}
	if want := "read output: bufio.Scanner: token too long\nReviewing the branch.\nStill reviewing."; c.Detail() != want {
		t.Errorf("detail = %q, want %q", c.Detail(), want)
	}
	cancelled := &executor.Error{Tool: "claude", ExitCode: -1, Output: "Reviewing.", Err: context.Canceled}
	if got := executor.Describe(cancelled).Detail(); got != "Reviewing." {
		t.Errorf("detail = %q, want a cancellation to carry the tail alone", got)
	}
}

// The stderr tail is the preferred diagnostic source, so a cut inside it must
// be tidied the way the stdout tail's is: no leading fragment, no half rune.
func TestStderrTailStartsOnALineBoundary(t *testing.T) {
	tool := newScript(t, "i=0\nwhile [ $i -lt 2000 ]; do echo \"line $i — still reviewing\" >&2; i=$((i+1)); done\nexit 1\n")

	_, err := (executor.Custom{Command: tool}).Run(t.Context(), executor.Request{Prompt: "review", Dir: t.TempDir()})
	failure, ok := errors.AsType[*executor.Error](err)
	if !ok {
		t.Fatalf("err = %v, want *executor.Error", err)
	}
	if len(failure.Stderr) > tailBytes || len(failure.Stderr) < tailBytes/2 {
		t.Errorf("stderr tail holds %d bytes, want close to the bound of %d", len(failure.Stderr), tailBytes)
	}
	first, _, _ := strings.Cut(failure.Stderr, "\n")
	if !strings.HasPrefix(first, "line ") || !strings.HasSuffix(first, "still reviewing") {
		t.Errorf("stderr tail opens with a fragment %q, want a whole line", first)
	}
	if !strings.HasSuffix(failure.Stderr, "line 1999 — still reviewing") {
		t.Errorf("stderr tail lost the last line: %q", failure.Stderr[max(0, len(failure.Stderr)-80):])
	}
}

// A progress bar redrawn with carriage returns and coloured with escape
// sequences must not paint over the console or land raw in the log.
func TestDescribeFlattensTerminalNoiseInTheTail(t *testing.T) {
	for stderr, want := range map[string]string{
		"10%\r20%\r100%\r\n\x1b[31mError: boom\x1b[0m\x07\n": "10%\n20%\n100%\nError: boom",
		"Error: bo\x7fom\n": "Error: boom",
		"\x1b]0;codex\x07\x1b]8;;https://x.test\x1b\\Error: boom\x1b]8;;\x1b\\\n": "Error: boom",
		// A tab is indentation, not noise: dropping it flattens every stack
		// frame and diff hunk in the tail against the margin.
		"panic: boom\n\tmain.go:12 +0x1f\n": "panic: boom\n\tmain.go:12 +0x1f",
	} {
		err := &executor.Error{Tool: "codex", ExitCode: 1, Stderr: stderr}
		if got := executor.Describe(err).Detail(); got != want {
			t.Errorf("detail of %q = %q, want %q", stderr, got, want)
		}
	}
}

// The classifier's matched line leads the detail unless the tail holds it. A
// reason matched on a raw line the tool painted with a progress bar and colour
// must be the flattened line, or it is repeated raw above its cleaned twin.
func TestMatchedReasonIsFlattenedLikeTheTail(t *testing.T) {
	tool := newFakeTool(t, fakeToolOpts{stderr: "10%\r20%\r\x1b[31mrate limit exceeded\x1b[0m\n", exit: 1})

	_, err := (executor.Claude{Command: tool.path}).Run(t.Context(), executor.Request{Prompt: "review", Dir: t.TempDir()})

	if !errors.Is(err, executor.ErrRateLimited) {
		t.Fatalf("err = %v, want a usage limit", err)
	}
	if want := "10%\n20%\nrate limit exceeded"; executor.Describe(err).Detail() != want {
		t.Errorf("detail = %q, want %q", executor.Describe(err).Detail(), want)
	}
}

// A failure with no exit status has only its wrapped error to explain itself.
// Suppressing that error because the captured tail happens to occur inside it
// would leave the record saying nothing about what stopped the call.
func TestDescribeKeepsAReasonThatSaysMoreThanTheTail(t *testing.T) {
	err := &executor.Error{Tool: "claude", ExitCode: -1, Output: "Error",
		Err: errors.New("read output: Error parsing stream")}

	c := executor.Describe(err)

	if want := "read output: Error parsing stream"; c.Reason != want {
		t.Errorf("reason = %q, want %q", c.Reason, want)
	}
	if want := "read output: Error parsing stream\nError"; c.Detail() != want {
		t.Errorf("detail = %q, want %q", c.Detail(), want)
	}
}

// The other direction still holds: a wrapped error that only restates the tail
// it ends with must not render the same message twice.
func TestDescribeDropsAReasonTheTailAlreadyEnds(t *testing.T) {
	err := &executor.Error{Tool: "claude", ExitCode: -1, Stderr: "tool execution failed",
		Err: errors.New("claude reported an error: tool execution failed")}

	if c := executor.Describe(err); c.Detail() != "tool execution failed" {
		t.Errorf("detail = %q, want the message once", c.Detail())
	}
}

// The tail is trimmed of blank lines and trailing whitespace before it is
// rendered, so a reason compared against it raw never matches the text it
// repeats: claude's result message is the case, since it reaches the record
// both as the wrapped error and as the stderr the message was appended to.
func TestDescribeDropsAReasonTheTailEndsPastItsBlankLines(t *testing.T) {
	msg := "Error: the request was rejected   \n\nRetry with a smaller prompt."
	err := &executor.Error{Tool: "claude", ExitCode: -1, Stderr: msg,
		Err: fmt.Errorf("claude reported an error: %s", msg)}

	want := "Error: the request was rejected\nRetry with a smaller prompt."
	if c := executor.Describe(err); c.Detail() != want {
		t.Errorf("detail = %q, want the message once as %q", c.Detail(), want)
	}
}

// A wrapped error carrying everything the tool said is held to the same bound
// as the tail, so a failure with no exit status cannot flood the record the
// bound exists to keep small.
func TestDescribeBoundsTheReasonLikeTheTail(t *testing.T) {
	var b strings.Builder
	b.WriteString("claude reported an error:\n")
	for i := range 400 {
		fmt.Fprintf(&b, "stack frame %d\n", i)
	}
	err := &executor.Error{Tool: "claude", ExitCode: -1, Output: "Reviewing.", Err: errors.New(b.String())}

	c := executor.Describe(err)

	if lines := strings.Count(c.Reason, "\n") + 1; lines > 20 {
		t.Errorf("reason holds %d lines, want at most the tail bound of 20", lines)
	}
	if !strings.HasSuffix(c.Reason, "stack frame 399") {
		t.Errorf("reason lost the end of the error: %q", c.Reason)
	}
	if !strings.Contains(c.Detail(), "[earlier lines omitted]") {
		t.Errorf("detail does not mark the omission:\n%s", c.Detail())
	}
}

// The line bound alone does not hold a reason down: twenty lines of a wrapped
// error can still be tens of kilobytes, so the byte bound has to cut first.
func TestDescribeBoundsTheReasonByBytesTooNotOnlyByLines(t *testing.T) {
	var b strings.Builder
	for i := range 20 {
		fmt.Fprintf(&b, "frame %d %s\n", i, strings.Repeat("x", 2000))
	}
	err := &executor.Error{Tool: "claude", ExitCode: -1, Output: "Reviewing.", Err: errors.New(b.String())}

	c := executor.Describe(err)

	if len(c.Reason) > 8<<10 {
		t.Errorf("reason holds %d bytes, want at most the 8 KiB capture bound", len(c.Reason))
	}
	if !strings.HasSuffix(c.Reason, strings.Repeat("x", 2000)) {
		t.Error("reason lost the end of the error, which is the part that explains it")
	}
}

// A classification carries the line that matched it. That line is the
// diagnosis, so the process error a failure with no exit status wraps must not
// displace it.
func TestDescribeKeepsTheMatchedReasonOverTheWrappedError(t *testing.T) {
	err := &executor.LimitError{Tool: "claude", Reason: "rate limit exceeded", Err: &executor.Error{
		Tool: "claude", ExitCode: -1,
		Stderr: "rate limit exceeded\n(node:1) ExperimentalWarning: something",
		Err:    errors.New("read output: token too long"),
	}}

	c := executor.Describe(err)

	if c.Reason != "rate limit exceeded" {
		t.Errorf("reason = %q, want the matched refusal rather than the process error", c.Reason)
	}
}
