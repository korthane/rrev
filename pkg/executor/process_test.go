package executor_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/korthane/rrev/pkg/executor"
)

// signalWriter closes seen on the tool's first output, so a test can act on
// the tool having started rather than on a sleep.
type signalWriter struct {
	once sync.Once
	seen chan struct{}
}

func (w *signalWriter) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.seen) })
	return len(p), nil
}

// newScript writes an executable shell script, for a test that needs a tool
// doing more than replaying a recorded stream.
func newScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tool")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o700); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}

func TestSessionTimeoutTerminatesAndKeepsOutput(t *testing.T) {
	tool := newScript(t, "echo partial finding\nsleep 30\n")
	// The bound has to clear process start plus the first write, which under a
	// loaded parallel run is far slower than the bound itself needs to be.
	custom := executor.Custom{Command: tool, Limits: executor.Limits{Session: 2 * time.Second}}

	start := time.Now()
	result, err := custom.Run(t.Context(), executor.Request{Prompt: "p"})

	if !errors.Is(err, executor.ErrSessionTimeout) {
		t.Fatalf("error = %v, want a session timeout", err)
	}
	if errors.Is(err, executor.ErrIdleTimeout) {
		t.Errorf("error = %v, want the session bound, not the idle one", err)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("call took %s, want it cut short", elapsed)
	}
	if !strings.Contains(result.Output, "partial finding") {
		t.Errorf("output captured before the timeout was lost: %q", result.Output)
	}
}

func TestIdleTimeoutResetsOnOutput(t *testing.T) {
	tool := newScript(t, "i=0\nwhile [ $i -lt 6 ]; do echo tick; sleep 0.1; i=$((i+1)); done\n")
	custom := executor.Custom{Command: tool, Limits: executor.Limits{Idle: 400 * time.Millisecond}}

	result, err := custom.Run(t.Context(), executor.Request{Prompt: "p"})
	if err != nil {
		t.Fatalf("a tool producing output steadily was cut short: %v", err)
	}
	if n := strings.Count(result.Output, "tick"); n != 6 {
		t.Errorf("collected %d ticks, want 6:\n%s", n, result.Output)
	}
}

func TestIdleTimeoutFires(t *testing.T) {
	tool := newScript(t, "echo first line\nsleep 30\n")
	custom := executor.Custom{Command: tool, Limits: executor.Limits{Idle: 2 * time.Second}}

	result, err := custom.Run(t.Context(), executor.Request{Prompt: "p"})

	if !errors.Is(err, executor.ErrIdleTimeout) {
		t.Fatalf("error = %v, want an idle timeout", err)
	}
	if !errors.Is(err, executor.ErrTimeout) {
		t.Errorf("error = %v, does not match the generic timeout sentinel", err)
	}
	if !strings.Contains(result.Output, "first line") {
		t.Errorf("output captured before going idle was lost: %q", result.Output)
	}
}

func TestTimeoutsDisabledByDefault(t *testing.T) {
	tool := newScript(t, "sleep 0.3\necho done\n")

	result, err := (executor.Custom{Command: tool}).Run(t.Context(), executor.Request{Prompt: "p"})
	if err != nil {
		t.Fatalf("run with no bounds configured: %v", err)
	}
	if !strings.Contains(result.Output, "done") {
		t.Errorf("output = %q, want the tool to have run to completion", result.Output)
	}
}

func TestProgressIndicationWhileSilent(t *testing.T) {
	tool := newScript(t, "echo starting\nsleep 0.5\necho done\n")
	var stream strings.Builder
	custom := executor.Custom{Command: tool, Limits: executor.Limits{Progress: 100 * time.Millisecond}}

	if _, err := custom.Run(t.Context(), executor.Request{Prompt: "p", Stream: &stream}); err != nil {
		t.Fatalf("run custom: %v", err)
	}

	if !strings.Contains(stream.String(), "still working") {
		t.Errorf("silent stretch produced no progress indication:\n%s", stream.String())
	}
}

func TestCancellationReturnsCapturedOutput(t *testing.T) {
	tool := newScript(t, "echo partial\nsleep 30\n")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// Cancel once the tool has actually spoken, so the assertion tests what
	// survives cancellation rather than how fast the machine forks a shell.
	spoke := &signalWriter{seen: make(chan struct{})}
	go func() {
		<-spoke.seen
		cancel()
	}()
	result, err := (executor.Custom{Command: tool}).Run(ctx, executor.Request{Prompt: "p", Stream: spoke})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want the cancellation", err)
	}
	if !strings.Contains(result.Output, "partial") {
		t.Errorf("output captured before cancellation was lost: %q", result.Output)
	}
}

// TestIdleTimeoutResetsOnStderr covers a tool that reports its progress on
// stderr: it is working, so the idle bound must not cut it short.
func TestIdleTimeoutResetsOnStderr(t *testing.T) {
	tool := newScript(t, "echo starting\ni=0\nwhile [ $i -lt 6 ]; do echo working >&2; sleep 0.1; i=$((i+1)); done\n")
	custom := executor.Custom{Command: tool, Limits: executor.Limits{Idle: 400 * time.Millisecond}}

	result, err := custom.Run(t.Context(), executor.Request{Prompt: "p"})
	if err != nil {
		t.Fatalf("a tool reporting progress on stderr was cut short: %v", err)
	}
	if !strings.Contains(result.Output, "starting") {
		t.Errorf("output = %q, want the tool to have run to completion", result.Output)
	}
}

// TestProgressIndicationWhileOnlyStderrSpeaks covers the same tool from the
// user's side: stderr never reaches the terminal, so the progress note is the
// only sign of life and must keep firing.
func TestProgressIndicationWhileOnlyStderrSpeaks(t *testing.T) {
	// stderr arrives far more often than the progress interval, so a note only
	// appears if stderr stopped resetting that interval.
	tool := newScript(t, "i=0\nwhile [ $i -lt 20 ]; do echo diagnostics >&2; sleep 0.05; i=$((i+1)); done\necho done\n")
	var stream strings.Builder
	custom := executor.Custom{Command: tool, Limits: executor.Limits{Progress: 250 * time.Millisecond}}

	if _, err := custom.Run(t.Context(), executor.Request{Prompt: "p", Stream: &stream}); err != nil {
		t.Fatalf("run custom: %v", err)
	}

	if strings.Contains(stream.String(), "diagnostics") {
		t.Errorf("stderr leaked into the attributed stream:\n%s", stream.String())
	}
	if !strings.Contains(stream.String(), "still working") {
		t.Errorf("a stderr-only stretch produced no progress indication:\n%s", stream.String())
	}
}

// TestProgressIndicationWhilePartialLine covers a tool writing a long line: the
// bytes prove it is alive, but nothing is rendered until the line ends, so the
// progress note is again the only sign of life.
func TestProgressIndicationWhilePartialLine(t *testing.T) {
	tool := newScript(t, "i=0\nwhile [ $i -lt 20 ]; do printf x; sleep 0.05; i=$((i+1)); done\necho\n")
	var stream strings.Builder
	custom := executor.Custom{Command: tool, Limits: executor.Limits{Progress: 250 * time.Millisecond}}

	if _, err := custom.Run(t.Context(), executor.Request{Prompt: "p", Stream: &stream}); err != nil {
		t.Fatalf("run custom: %v", err)
	}

	if !strings.Contains(stream.String(), "still working") {
		t.Errorf("an unfinished line produced no progress indication:\n%s", stream.String())
	}
}

// TestIdleTimeoutResetsOnPartialLine is the other side of the same tool: bytes
// arriving mean it is working, so the idle bound must not cut it short.
func TestIdleTimeoutResetsOnPartialLine(t *testing.T) {
	tool := newScript(t, "i=0\nwhile [ $i -lt 6 ]; do printf x; sleep 0.1; i=$((i+1)); done\necho done\n")
	custom := executor.Custom{Command: tool, Limits: executor.Limits{Idle: 400 * time.Millisecond}}

	result, err := custom.Run(t.Context(), executor.Request{Prompt: "p"})
	if err != nil {
		t.Fatalf("a tool writing one long line was cut short: %v", err)
	}
	if !strings.Contains(result.Output, "done") {
		t.Errorf("output = %q, want the tool to have run to completion", result.Output)
	}
}

// The bound that cut a call short must not also cut what the tool said before
// it: a timeout without the tool's last words is an idle stretch and nothing
// else to go on.
func TestTimeoutFailureKeepsTheToolsLastWords(t *testing.T) {
	tool := newScript(t, "echo running the suite\nsleep 30\n")
	custom := executor.Custom{Command: tool, Limits: executor.Limits{Session: 2 * time.Second}}

	_, err := custom.Run(t.Context(), executor.Request{Prompt: "p"})

	if !errors.Is(err, executor.ErrSessionTimeout) {
		t.Fatalf("error = %v, want a session timeout", err)
	}
	cause := executor.Describe(err)
	if !strings.HasSuffix(cause.Summary(), ": timeout") {
		t.Errorf("summary = %q, want the tool named with its classification", cause.Summary())
	}
	for _, want := range []string{"session timeout", "running the suite"} {
		if !strings.Contains(cause.Detail(), want) {
			t.Errorf("detail = %q, want it to hold %q", cause.Detail(), want)
		}
	}
}
