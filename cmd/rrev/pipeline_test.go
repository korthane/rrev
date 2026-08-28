package main

import "testing"

// A break sent while no review loop is running belongs to nothing: dropping it
// is what keeps the key alive for the loop that starts next.
func TestBreakerDropsABreakSentOutsideALoop(t *testing.T) {
	brk := &breaker{ch: make(chan struct{})}
	brk.fire()

	select {
	case <-brk.arm():
		t.Fatal("a break sent before the loop started ended it without an iteration")
	default:
	}

	armed := brk.arm()
	brk.fire()
	select {
	case <-armed:
	default:
		t.Error("the break did not reach the loop that armed it")
	}
}
