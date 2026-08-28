//go:build !windows

package executor_test

import (
	"context"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/korthane/rrev/pkg/executor"
)

// TestCancellationKillsProcessTree covers the reason the tool runs in its own
// session: a cancelled review must not leave sub-agents running.
func TestCancellationKillsProcessTree(t *testing.T) {
	pidFile := t.TempDir() + "/child.pid"
	tool := newScript(t, "sh -c 'while :; do sleep 1; done' &\necho $! > "+pidFile+"\necho started\nwhile :; do sleep 1; done\n")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		defer cancel()
		waitForFile(t, pidFile)
	}()

	result, err := (executor.Custom{Command: tool}).Run(ctx, executor.Request{Prompt: "p"})
	if !strings.Contains(result.Output, "started") {
		t.Fatalf("the tool never got going: %q, %v", result.Output, err)
	}

	child := readPID(t, pidFile)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if syscall.Kill(child, 0) == syscall.ESRCH {
			return
		}
		if time.Now().After(deadline) {
			_ = syscall.Kill(child, syscall.SIGKILL)
			t.Fatalf("grandchild %d survived the cancelled call", child)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if content, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(content)) != "" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func readPID(t *testing.T, path string) int {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read child pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(content)))
	if err != nil {
		t.Fatalf("parse child pid %q: %v", content, err)
	}
	return pid
}
