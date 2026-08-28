//go:build !windows

package main

import (
	"context"
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/korthane/rrev/pkg/status"
)

// waitFor bounds how long a test waits for a signal to be delivered.
const waitFor = 2 * time.Second

// TestListenBreakEndsOnlyTheLoop is guarded to the platforms that have a break
// signal at all; where there is none, listen reports no channel instead.
func TestListenBreakEndsOnlyTheLoop(t *testing.T) {
	ctx, arm, stop := listen(context.Background())
	defer stop()

	sig, hint, ok := status.BreakSignal()
	if !ok {
		t.Skip("this platform has no break signal")
	}
	if arm == nil {
		t.Fatalf("%s is documented as the break key but no loop watches for it", hint)
	}
	brk := arm()
	if err := syscall.Kill(os.Getpid(), sig.(syscall.Signal)); err != nil {
		t.Fatalf("send break signal: %v", err)
	}

	select {
	case <-brk:
	case <-time.After(waitFor):
		t.Fatal("the break signal never reached the review loop")
	}
	if ctx.Err() != nil {
		t.Error("a break must end the loop only, not abort the run")
	}
}

// Every review loop arms the break for itself, so a run whose earlier phase
// swallowed a press must still be breakable: the key cannot be one-shot.
func TestListenRearmsTheBreakForEachLoop(t *testing.T) {
	_, arm, stop := listen(context.Background())
	defer stop()

	sig, _, ok := status.BreakSignal()
	if !ok {
		t.Skip("this platform has no break signal")
	}
	for press := 1; press <= 2; press++ {
		brk := arm()
		if err := syscall.Kill(os.Getpid(), sig.(syscall.Signal)); err != nil {
			t.Fatalf("send break signal: %v", err)
		}
		select {
		case <-brk:
		case <-time.After(waitFor):
			t.Fatalf("break %d never reached the loop; the key dies after the first press", press)
		}
	}
}

func TestListenInterruptAbortsTheRun(t *testing.T) {
	ctx, _, stop := listen(context.Background())
	defer stop()

	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		t.Fatalf("send interrupt: %v", err)
	}
	select {
	case <-ctx.Done():
	case <-time.After(waitFor):
		t.Fatal("an interrupt must cancel the run context, which is what kills the executor")
	}
}
