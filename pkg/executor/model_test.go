package executor_test

import (
	"strings"
	"testing"

	"github.com/korthane/rrev/pkg/executor"
)

func TestParseSpec(t *testing.T) {
	tests := []struct {
		spec   string
		model  string
		effort string
	}{
		{spec: "", model: "", effort: ""},
		{spec: "opus", model: "opus", effort: ""},
		{spec: "opus:high", model: "opus", effort: "high"},
		{spec: ":high", model: "", effort: "high"},
		{spec: " opus : high ", model: "opus", effort: "high"},
	}
	for _, tt := range tests {
		got := executor.ParseSpec(tt.spec)
		if got.Model != tt.model || got.Effort != tt.effort {
			t.Errorf("ParseSpec(%q) = %+v, want model %q effort %q", tt.spec, got, tt.model, tt.effort)
		}
	}
}

func TestSpecInheritsPerPart(t *testing.T) {
	base := executor.Spec{Model: "opus", Effort: "medium"}

	if got := executor.ParseSpec(":high").Inherit(base); got.Model != "opus" || got.Effort != "high" {
		t.Errorf("effort-only spec = %+v, want the base model with the overridden effort", got)
	}
	if got := executor.ParseSpec("sonnet").Inherit(base); got.Model != "sonnet" || got.Effort != "medium" {
		t.Errorf("model-only spec = %+v, want the overridden model with the base effort", got)
	}
	if got := executor.ParseSpec("").Inherit(base); got != base {
		t.Errorf("empty spec = %+v, want the base unchanged", got)
	}
}

func TestSpecForPhaseInheritance(t *testing.T) {
	cfg := defaultConfig(t)
	cfg.Model = "opus:medium"

	// No review model configured, so review phases run the primary model.
	review := executor.SpecFor(cfg, executor.PhaseReview)
	if review.Model != "opus" || review.Effort != "medium" {
		t.Errorf("review spec = %+v, want the run-wide model", review)
	}

	cfg.ReviewModel = "sonnet"
	cfg.ExternalModel = ":high"
	if got := executor.SpecFor(cfg, executor.PhaseExternal); got.Model != "sonnet" || got.Effort != "high" {
		t.Errorf("external spec = %+v, want the review model at the overridden effort", got)
	}
	if got := executor.SpecFor(cfg, executor.PhaseFinal); got.Model != "sonnet" || got.Effort != "medium" {
		t.Errorf("final spec = %+v, want the review model and the run-wide effort", got)
	}
	// finalize is not a review phase, so it inherits the run-wide model.
	if got := executor.SpecFor(cfg, executor.PhaseFinalize); got.Model != "opus" {
		t.Errorf("finalize spec = %+v, want the run-wide model", got)
	}
}

func TestSpecForToolDropsUnsupportedEffort(t *testing.T) {
	spec, warning := executor.Spec{Model: "opus", Effort: "ultra"}.For("claude")

	if spec.Effort != "" {
		t.Errorf("effort = %q, want it dropped", spec.Effort)
	}
	if spec.Model != "opus" {
		t.Errorf("model = %q, want it kept", spec.Model)
	}
	for _, want := range []string{"ultra", "claude", "high"} {
		if !strings.Contains(warning, want) {
			t.Errorf("warning %q does not mention %q", warning, want)
		}
	}
}

func TestSpecForToolKeepsSupportedEffort(t *testing.T) {
	spec, warning := executor.Spec{Model: "gpt-5", Effort: "minimal"}.For("codex")
	if warning != "" || spec.Effort != "minimal" {
		t.Errorf("spec = %+v, warning = %q, want the effort kept without a warning", spec, warning)
	}
	if got := executor.Efforts("custom"); got != nil {
		t.Errorf("Efforts(custom) = %v, want none", got)
	}
}

func TestSelectWarnsForPhase(t *testing.T) {
	cfg := defaultConfig(t)
	cfg.Model = "opus"
	cfg.ReviewModel = ":ultra"

	spec, warning := executor.Select(cfg, executor.PhaseReview, "claude")
	if spec.Model != "opus" || spec.Effort != "" {
		t.Errorf("spec = %+v, want the model kept and the effort dropped", spec)
	}
	if warning == "" {
		t.Error("an unsupported effort was dropped without a warning")
	}
}
