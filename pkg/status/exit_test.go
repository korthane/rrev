package status

import (
	"errors"
	"strings"
	"testing"

	"github.com/korthane/rrev/pkg/executor"
	"github.com/korthane/rrev/pkg/processor"
	"github.com/korthane/rrev/pkg/processor/phase"
)

func converged(name string) phase.Result {
	return phase.Result{Name: name, Reason: phase.ReasonConverged, Iterations: 2}
}

func TestOutcome(t *testing.T) {
	tests := []struct {
		name string
		res  processor.Result
		want Code
	}{
		{"every phase converged", processor.Result{
			Mode:      processor.ModeFull,
			Phases:    []phase.Result{converged(phase.NameComprehensive), converged(phase.NameFinal)},
			Converged: true,
		}, CodeOK},
		{"iteration limit reached", processor.Result{
			Mode: processor.ModeFull,
			Phases: []phase.Result{{
				Name: phase.NameComprehensive, Reason: phase.ReasonIterationLimit, Iterations: 10,
			}},
		}, CodeUnconverged},
		{"executor failure", processor.Result{
			Mode: processor.ModeFull,
			Phases: []phase.Result{{
				Name: phase.NameExternal, Reason: phase.ReasonFailure, Err: errors.New("codex: rate limited"),
			}},
			Err: errors.New("codex: rate limited"),
		}, CodeFailed},
		{"report-only with findings", processor.Result{
			Mode:      processor.ModeReportOnly,
			Phases:    []phase.Result{{Name: phase.NameComprehensive, Reason: phase.ReasonSinglePass}},
			Findings:  []phase.Finding{{Summary: "missing scenario"}},
			Converged: true,
		}, CodeUnconverged},
		{"report-only with nothing found", processor.Result{
			Mode:      processor.ModeReportOnly,
			Phases:    []phase.Result{{Name: phase.NameComprehensive, Reason: phase.ReasonSinglePass}},
			Converged: true,
		}, CodeOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Outcome(tt.res); got != tt.want {
				t.Errorf("Outcome = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSummaryNamesThePhaseThatDidNotConverge(t *testing.T) {
	res := processor.Result{
		Mode: processor.ModeFull,
		Phases: []phase.Result{
			converged(phase.NameComprehensive),
			{Name: phase.NameExternal, Reason: phase.ReasonStalemate, Iterations: 3},
		},
		Findings: []phase.Finding{{Summary: "unimplemented scenario"}},
	}

	got := Summary(res)
	for _, want := range []string{"external review did not converge: stalemate", "run did not converge", "1 finding outstanding"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "comprehensive") {
		t.Errorf("a converged phase does not belong in the summary:\n%s", got)
	}
}

func TestSummaryNamesTheFailingPhase(t *testing.T) {
	// A failure no tool owns — a prompt that would not expand — is the shape
	// that reaches the closing line as its own text.
	failure := errors.New("expand comprehensive prompt: unknown variable {{TYPO}}")
	res := processor.Result{
		Mode: processor.ModeFull,
		Phases: []phase.Result{
			{Name: phase.NameComprehensive, Reason: phase.ReasonFailure, Err: failure},
		},
		Err: failure,
	}

	got := Summary(res)
	for _, want := range []string{"comprehensive review did not converge", failure.Error(), "run failed during the comprehensive review"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary is missing %q:\n%s", want, got)
		}
	}
}

// A review the model gave up on returns a process failure carrying the marker
// as its wrapped error, not a bare error naming it. The closing line is the
// summary form of that shape; the marker itself is in the failure record's
// detail, which the console has already shown.
func TestSummaryNamesAFailureTheModelSignalled(t *testing.T) {
	failure := &executor.Error{Tool: "claude", ExitCode: -1,
		Output: "I cannot review this branch.", Err: errors.New("reported <<<RREV:TASK_FAILED>>>")}
	res := processor.Result{
		Mode:   processor.ModeFull,
		Phases: []phase.Result{{Name: phase.NameComprehensive, Reason: phase.ReasonFailure, Err: failure}},
		Err:    failure,
	}

	got := Summary(res)
	for _, want := range []string{
		"comprehensive review did not converge: executor failure: claude: failure",
		"run failed during the comprehensive review",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary is missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "I cannot review this branch.") {
		t.Errorf("the closing line must not repeat the tail its record carried:\n%s", got)
	}
}

// The closing line names the failure the way its record did. The error's own
// text carries the command line and the stderr tail, which the console has
// already shown as indented detail under that record.
func TestSummaryDoesNotRepeatTheCommandLineOrTheTail(t *testing.T) {
	failure := &executor.Error{Tool: "claude", Args: []string{"--print", "--verbose"}, ExitCode: 1,
		Stderr: "Error: prompt is too long\nfor the context window", Err: errors.New("exit status 1")}
	res := processor.Result{
		Mode:   processor.ModeFull,
		Phases: []phase.Result{{Name: phase.NameFinal, Reason: phase.ReasonFailure, Err: failure}},
		Err:    failure,
	}

	got := Summary(res)
	if !strings.Contains(got, "did not converge: executor failure: claude: failure (exit 1)") {
		t.Errorf("summary does not name the failure in its summary form:\n%s", got)
	}
	for _, leak := range []string{"--print", "prompt is too long"} {
		if strings.Contains(got, leak) {
			t.Errorf("summary repeats %q from the error's text:\n%s", leak, got)
		}
	}
}

func TestSummaryReportsAConvergedRun(t *testing.T) {
	res := processor.Result{
		Mode:      processor.ModeFull,
		Phases:    []phase.Result{converged(phase.NameComprehensive), converged(phase.NameFinal)},
		Converged: true,
	}
	if got := Summary(res); got != "run converged with nothing outstanding" {
		t.Errorf("summary = %q", got)
	}
}
