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
