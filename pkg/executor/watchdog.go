package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// Limits bound one executor call. A zero duration disables that bound, which is
// the default: a thorough review legitimately takes a long time.
type Limits struct {
	// Session bounds the whole call; Idle only a stretch without output.
	Session time.Duration
	Idle    time.Duration
	// Progress is how often a silent call reports that it is still working.
	Progress time.Duration
}

// Timeout sentinels, so a phase can match either bound or one specifically.
var (
	ErrTimeout        = errors.New("executor timed out")
	ErrSessionTimeout = errors.New("executor session timed out")
	ErrIdleTimeout    = errors.New("executor went idle")
)

// TimeoutError reports a call rrev cut short because it outlived one of its
// bounds. It is distinguishable from a tool failure, and the output captured
// before the bound expired is returned alongside it.
type TimeoutError struct {
	Tool  string
	Limit time.Duration
	Idle  bool
}

func (e *TimeoutError) Error() string {
	if e.Idle {
		return fmt.Sprintf("%s produced no output for %s", e.Tool, e.Limit)
	}
	return fmt.Sprintf("%s exceeded its %s session timeout", e.Tool, e.Limit)
}

// Is matches the generic timeout sentinel and the specific bound that expired.
func (e *TimeoutError) Is(target error) bool {
	switch target {
	case ErrTimeout:
		return true
	case ErrIdleTimeout:
		return e.Idle
	case ErrSessionTimeout:
		return !e.Idle
	default:
		return false
	}
}

// expiry names the bound that cut a call short.
type expiry int

const (
	expiryNone expiry = iota
	expirySession
	expiryIdle
)

// watchdog enforces a call's bounds while it runs: it cancels the call when the
// session or idle bound expires, and reports that a quiet call is still alive
// so a long stretch inside sub-agents does not look like a hang.
type watchdog struct {
	limits Limits
	stream io.Writer
	touch  chan struct{}
	done   chan struct{}

	mu      sync.Mutex
	expired expiry
}

func newWatchdog(limits Limits, stream io.Writer) *watchdog {
	return &watchdog{
		limits: limits, stream: stream,
		touch: make(chan struct{}, 1), done: make(chan struct{}), expired: expiryNone,
	}
}

// touched records output arriving, which restarts the idle countdown. A
// dropped signal is harmless: the timer was already about to be reset.
func (w *watchdog) touched() {
	select {
	case w.touch <- struct{}{}:
	default:
	}
}

// watch runs until stop is called, cancelling the call on an expired bound.
func (w *watchdog) watch(cancel context.CancelFunc) {
	start := time.Now()
	session, idle, progress := newPulse(w.limits.Session), newPulse(w.limits.Idle), newPulse(w.limits.Progress)
	defer func() {
		session.stop()
		idle.stop()
		progress.stop()
	}()

	for {
		select {
		case <-w.done:
			return
		case <-w.touch:
			idle.reset()
			progress.reset()
		case <-session.C():
			w.expire(expirySession, cancel)
			return
		case <-idle.C():
			w.expire(expiryIdle, cancel)
			return
		case <-progress.C():
			w.report(time.Since(start))
			progress.reset()
		}
	}
}

// stop ends the watch once the call has finished.
func (w *watchdog) stop() { close(w.done) }

// timeout reports the bound that expired, if any.
func (w *watchdog) timeout(tool string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch w.expired {
	case expirySession:
		return &TimeoutError{Tool: tool, Limit: w.limits.Session}
	case expiryIdle:
		return &TimeoutError{Tool: tool, Limit: w.limits.Idle, Idle: true}
	default:
		return nil
	}
}

func (w *watchdog) expire(kind expiry, cancel context.CancelFunc) {
	w.mu.Lock()
	w.expired = kind
	w.mu.Unlock()
	cancel()
}

func (w *watchdog) report(elapsed time.Duration) {
	if w.stream == nil {
		return
	}
	// A terminal that cannot be written to is not worth failing the review for.
	_, _ = fmt.Fprintf(w.stream, "· still working (%s)\n", elapsed.Round(time.Second))
}

// pulse is a timer that a zero duration disables, so a disabled bound is a
// channel that never fires rather than a branch at every use.
type pulse struct {
	d time.Duration
	t *time.Timer
}

func newPulse(d time.Duration) *pulse {
	p := &pulse{d: d}
	if d > 0 {
		p.t = time.NewTimer(d)
	}
	return p
}

func (p *pulse) C() <-chan time.Time {
	if p.t == nil {
		return nil
	}
	return p.t.C
}

func (p *pulse) reset() {
	if p.t == nil {
		return
	}
	p.t.Reset(p.d)
}

func (p *pulse) stop() {
	if p.t != nil {
		p.t.Stop()
	}
}

// syncWriter serializes writes to the terminal so a watchdog note cannot
// interleave with the streamed output of the call it is watching. A nil target
// discards.
type syncWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func newSyncWriter(w io.Writer) *syncWriter { return &syncWriter{w: w} }

func (s *syncWriter) Write(p []byte) (int, error) {
	if s.w == nil {
		return len(p), nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}
