package executor_test

import (
	"strings"
	"testing"

	"github.com/korthane/rrev/pkg/executor"
)

func TestDetect(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   executor.Signal
	}{
		{name: "no output", output: "", want: executor.SignalNone},
		{name: "no marker", output: "Fixed two findings and committed.", want: executor.SignalNone},
		{name: "marker on its own line", output: "All clear.\n<<<RREV:REVIEW_DONE>>>\n", want: executor.SignalReviewDone},
		{name: "marker indented", output: "  <<<RREV:EXTERNAL_DONE>>>  ", want: executor.SignalExternalDone},
		{name: "failure marker", output: "Cannot continue.\n<<<RREV:TASK_FAILED>>>", want: executor.SignalFailed},
		{
			name:   "marker inline in prose",
			output: "Emit `<<<RREV:REVIEW_DONE>>>` when you are done.",
			want:   executor.SignalNone,
		},
		{
			name:   "marker with trailing text",
			output: "<<<RREV:REVIEW_DONE>>> is the marker to emit",
			want:   executor.SignalNone,
		},
		{
			name:   "marker inside a fenced block",
			output: "The protocol is:\n```\n<<<RREV:REVIEW_DONE>>>\n```\nStill working.",
			want:   executor.SignalNone,
		},
		{
			name:   "marker after a fenced block closes",
			output: "```\n<<<RREV:TASK_FAILED>>>\n```\n<<<RREV:REVIEW_DONE>>>",
			want:   executor.SignalReviewDone,
		},
		{
			name:   "marker inside a tilde fence",
			output: "~~~text\n<<<RREV:EXTERNAL_DONE>>>\n~~~",
			want:   executor.SignalNone,
		},
		{
			name:   "last marker wins",
			output: "<<<RREV:REVIEW_DONE>>>\nmore work happened\n<<<RREV:EXTERNAL_DONE>>>",
			want:   executor.SignalExternalDone,
		},
		{
			name:   "failure wins over a later convergence marker",
			output: "<<<RREV:TASK_FAILED>>>\n<<<RREV:REVIEW_DONE>>>",
			want:   executor.SignalFailed,
		},
		{name: "carriage returns tolerated", output: "done\r\n<<<RREV:REVIEW_DONE>>>\r\n", want: executor.SignalReviewDone},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := executor.Detect(tt.output); got != tt.want {
				t.Errorf("Detect() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSignalMarker(t *testing.T) {
	if got := executor.SignalReviewDone.Marker(); got != "<<<RREV:REVIEW_DONE>>>" {
		t.Errorf("marker = %q", got)
	}
	if got := executor.SignalNone.Marker(); got != "" {
		t.Errorf("marker of no signal = %q, want empty", got)
	}
	if got := executor.SignalNone.String(); got != "none" {
		t.Errorf("String() of no signal = %q", got)
	}
}

// Every recognized signal must round-trip through its own marker; a signal that
// cannot be detected from the text prompts tell the model to emit is useless.
func TestEverySignalRoundTrips(t *testing.T) {
	signals := executor.Signals()
	if len(signals) != 3 {
		t.Fatalf("Signals() = %v, want the three documented signals", signals)
	}
	for _, signal := range signals {
		output := "work happened\n" + signal.Marker() + "\n"
		if got := executor.Detect(output); got != signal {
			t.Errorf("Detect(%q) = %q, want %q", strings.TrimSpace(output), got, signal)
		}
	}
}
