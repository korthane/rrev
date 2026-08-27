package status

import (
	"fmt"
	"strings"

	"github.com/korthane/rrev/pkg/processor"
	"github.com/korthane/rrev/pkg/processor/phase"
)

// Code is the process exit status a run ends with. The two non-zero values are
// distinguishable on purpose: a caller scripting rrev needs to tell a review
// that ran and left findings from one that never got going or was cut short.
type Code int

// Exit statuses.
const (
	// CodeOK is a pipeline that converged, and the status of a run that only
	// printed usage or a version.
	CodeOK Code = 0
	// CodeFailed is a run that failed to start, aborted, or could not complete.
	CodeFailed Code = 1
	// CodeUnconverged is a run that completed with findings still outstanding.
	CodeUnconverged Code = 2
)

// Outcome classifies a finished run. A read-only run converges nothing, so it
// is judged on whether it found anything instead: a report with findings in it
// is what a caller gating on rrev wants to hear about.
func Outcome(res processor.Result) Code {
	switch {
	case res.Err != nil:
		return CodeFailed
	case res.Mode.ReadOnly():
		if len(res.Findings) > 0 {
			return CodeUnconverged
		}
		return CodeOK
	case !res.Converged:
		return CodeUnconverged
	default:
		return CodeOK
	}
}

// Summary renders the closing report: every phase that ended with something
// outstanding and why, then the one line stating the run's outcome.
func Summary(res processor.Result) string {
	var lines []string
	if len(res.Phases) > 0 && len(res.Executed()) == 0 {
		lines = append(lines, "no review phase ran: every phase this mode selects was skipped")
	}
	for _, p := range res.Phases {
		if p.Skipped || p.OK() {
			continue
		}
		line := fmt.Sprintf("%s did not converge: %s", phase.Label(p.Name), p.Reason)
		if p.Err != nil {
			line += ": " + p.Err.Error()
		}
		lines = append(lines, line)
	}

	switch Outcome(res) {
	case CodeFailed:
		lines = append(lines, "run failed"+failedIn(res))
	case CodeUnconverged:
		lines = append(lines, fmt.Sprintf("run did not converge; %s outstanding", plural(len(res.Findings), "finding")))
	default:
		lines = append(lines, "run converged with nothing outstanding")
	}
	return strings.Join(lines, "\n")
}

// failedIn names the phase a failed run died in, when a phase is what failed:
// the report itself can fail a read-only run instead.
func failedIn(res processor.Result) string {
	for _, p := range res.Phases {
		if p.Err != nil {
			return " during the " + phase.Label(p.Name)
		}
	}
	if res.Err != nil {
		return ": " + res.Err.Error()
	}
	return ""
}
