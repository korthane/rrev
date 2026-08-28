package executor

import (
	"slices"
	"strings"
)

// Signal is a termination marker an executor emits in its output. The
// load-bearing property is that no signal means "work was done, iterate again"
// rather than success.
type Signal string

// Recognized signals.
const (
	SignalNone         Signal = ""
	SignalReviewDone   Signal = "REVIEW_DONE"
	SignalExternalDone Signal = "EXTERNAL_DONE"
	SignalFailed       Signal = "TASK_FAILED"
)

const (
	markerPrefix = "<<<RREV:"
	markerSuffix = ">>>"
)

var signals = []Signal{SignalReviewDone, SignalExternalDone, SignalFailed}

// Signals lists every recognized signal, in no particular order of precedence.
func Signals() []Signal { return slices.Clone(signals) }

// Marker is the wire form a prompt tells the executor to emit.
func (s Signal) Marker() string {
	if s == SignalNone {
		return ""
	}
	return markerPrefix + string(s) + markerSuffix
}

// String renders the signal for messages and the progress log.
func (s Signal) String() string {
	if s == SignalNone {
		return "none"
	}
	return string(s)
}

// Detect finds the termination signal in executor output. A marker counts only
// when it is the entire content of its own line and outside a fenced code
// block, so a model quoting the protocol does not end a loop. A failure marker
// wins over any other; otherwise the last marker emitted decides.
func Detect(output string) Signal {
	found := SignalNone
	fenced := false
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSpace(line)
		if isFence(line) {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		for _, signal := range signals {
			if line != signal.Marker() {
				continue
			}
			if signal == SignalFailed {
				return SignalFailed
			}
			found = signal
		}
	}
	return found
}

func isFence(line string) bool {
	return strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~")
}
