package executor

import (
	"fmt"
	"slices"
	"strings"

	"github.com/korthane/rrev/pkg/config"
)

// Phase names a pipeline phase, which is the granularity a model specification
// is chosen at.
type Phase string

// Phases whose model specification is configurable.
const (
	PhaseReview   Phase = "review"
	PhaseExternal Phase = "external"
	PhaseFinal    Phase = "final"
	PhaseFinalize Phase = "finalize"
)

// Spec is a model selection and the reasoning effort to run it at. Either part
// may be empty, which leaves the tool's own default in place.
type Spec struct {
	Model  string
	Effort string
}

// ParseSpec splits the combined `model[:effort]` configuration form.
func ParseSpec(spec string) Spec {
	model, effort, _ := strings.Cut(strings.TrimSpace(spec), ":")
	return Spec{Model: strings.TrimSpace(model), Effort: strings.TrimSpace(effort)}
}

// String renders the spec back into the form it is configured in.
func (s Spec) String() string {
	switch {
	case s.Model == "" && s.Effort == "":
		return "tool default"
	case s.Effort == "":
		return s.Model
	case s.Model == "":
		return ":" + s.Effort
	default:
		return s.Model + ":" + s.Effort
	}
}

// Inherit fills each empty part from base, so a phase naming only an effort
// keeps the run's model and a phase naming only a model keeps its effort.
func (s Spec) Inherit(base Spec) Spec {
	if s.Model == "" {
		s.Model = base.Model
	}
	if s.Effort == "" {
		s.Effort = base.Effort
	}
	return s
}

// efforts lists the reasoning effort levels each tool accepts. A tool absent
// from the map accepts none, so an effort configured for it is dropped rather
// than passed through to a flag it does not have.
var efforts = map[string][]string{
	"claude": {"low", "medium", "high", "xhigh", "max"},
	"codex":  {"minimal", "low", "medium", "high", "xhigh"},
}

// Efforts reports the reasoning effort levels a tool accepts.
func Efforts(tool string) []string { return slices.Clone(efforts[tool]) }

// For adapts the spec to the tool that will run it. An effort level the tool
// does not accept is dropped and described in the returned warning, so the call
// proceeds at the tool's own default instead of failing.
func (s Spec) For(tool string) (Spec, string) {
	if s.Effort == "" || slices.Contains(efforts[tool], s.Effort) {
		return s, ""
	}
	warning := fmt.Sprintf("%s does not accept effort %q", tool, s.Effort)
	if accepted := efforts[tool]; len(accepted) > 0 {
		warning += "; accepted levels are " + strings.Join(accepted, ", ")
	}
	warning += "; proceeding at its default effort"
	s.Effort = ""
	return s, warning
}

// SpecFor resolves a phase's model specification. Inheritance is per part: the
// external and final phases fall back to the review specification, which itself
// falls back to the run-wide model, so configuring one model covers every phase.
func SpecFor(cfg *config.Config, phase Phase) Spec {
	run := ParseSpec(cfg.Model)
	review := ParseSpec(cfg.ReviewModel).Inherit(run)
	switch phase {
	case PhaseReview:
		return review
	case PhaseExternal:
		return ParseSpec(cfg.ExternalModel).Inherit(review)
	case PhaseFinal:
		return ParseSpec(cfg.FinalModel).Inherit(review)
	case PhaseFinalize:
		return ParseSpec(cfg.FinalizeModel).Inherit(run)
	default:
		return run
	}
}

// Select resolves the model specification a phase runs with under a given tool,
// returning the warning to print when an effort level had to be dropped.
func Select(cfg *config.Config, phase Phase, tool string) (Spec, string) {
	return SpecFor(cfg, phase).For(tool)
}
