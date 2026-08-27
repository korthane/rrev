package status

import (
	"errors"
	"strings"
	"testing"

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
				t.Errorf("Outcome = %d (%s), want %d (%s)", got, got, tt.want, tt.want)
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
	failure := errors.New("claude reported <<<RREV:TASK_FAILED>>>")
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
