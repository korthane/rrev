package config

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Layer names a configuration source, ordered by precedence.
type Layer string

// Configuration sources, highest precedence first.
const (
	LayerFlags    Layer = "flags"
	LayerProject  Layer = "project"
	LayerUser     Layer = "user"
	LayerDefaults Layer = "defaults"
)

// Source records where a resolved setting came from, so a later contradiction
// can be blamed on a flag or on a file with a line number.
type Source struct {
	Layer Layer
	File  string
	Line  int
}

// String describes the source the way it is shown to the user.
func (s Source) String() string {
	switch {
	case s.Layer == LayerFlags:
		return "command line"
	case s.File == "":
		return string(s.Layer)
	case s.Line > 0:
		return fmt.Sprintf("%s:%d", s.File, s.Line)
	default:
		return s.File
	}
}

// ValueError reports a setting whose value rrev cannot use, naming where the
// value was set.
type ValueError struct {
	Key   string
	Value string
	Src   Source
	Msg   string
}

func (e *ValueError) Error() string {
	return fmt.Sprintf("%s: invalid value %q for %s: %s", e.Src, e.Value, e.Key, e.Msg)
}

// Executor names a supported primary executor.
const (
	ExecutorClaude = "claude"
	ExecutorCodex  = "codex"
)

// External review tool selections.
const (
	ExternalToolCodex  = "codex"
	ExternalToolCustom = "custom"
	ExternalToolNone   = "none"
)

// Config is the fully resolved configuration for one run.
type Config struct {
	// Executor is the primary executor running review phases and fixes.
	Executor string
	// ClaudeCommand and CodexCommand are the binaries preflight looks for.
	ClaudeCommand string
	CodexCommand  string

	// ExternalReviewTool selects the independent second opinion; when it is
	// custom, ExternalReviewCommand is the script rrev runs.
	ExternalReviewTool    string
	ExternalReviewCommand string

	// BaseRef is empty when the default branch should be detected at startup.
	BaseRef string

	// Model specifications in the combined `model[:effort]` form. An empty
	// per-phase value inherits Model.
	Model         string
	ReviewModel   string
	ExternalModel string
	FinalModel    string
	FinalizeModel string

	MaxIterations         int
	ExternalMaxIterations int
	FinalMaxIterations    int
	// StalematePatience is the number of consecutive unchanged iterations a
	// loop tolerates; zero disables stalemate detection.
	StalematePatience int

	// SessionTimeout bounds a whole executor call, IdleTimeout only a silent
	// stretch of one. Both are disabled at zero.
	SessionTimeout   time.Duration
	IdleTimeout      time.Duration
	ProgressInterval time.Duration

	Finalize bool

	ProgressDir string
	ReportFile  string

	// ChecklistBudget caps how many characters of requirement checklist are
	// expanded into a prompt.
	ChecklistBudget int
	// ValidationCommand runs before a fix is committed.
	ValidationCommand string

	Debug   bool
	NoColor bool

	origins map[string]Source
}

// Origin reports where a setting's resolved value came from.
func (c *Config) Origin(key string) Source { return c.origins[key] }

// SetByFlag reports whether a setting was given on the command line, which is
// what makes a contradiction a startup error rather than something to warn
// about and override.
func (c *Config) SetByFlag(key string) bool { return c.origins[key].Layer == LayerFlags }

// field describes one setting: how to parse it and, for enums, what it accepts.
type field struct {
	key     string
	allowed []string
	set     func(*Config, string) error
}

var fields = []field{
	{key: "executor", allowed: []string{ExecutorClaude, ExecutorCodex},
		set: func(c *Config, v string) error { return setEnum(&c.Executor, v, ExecutorClaude, ExecutorCodex) }},
	{key: "claude_command", set: func(c *Config, v string) error { c.ClaudeCommand = v; return nil }},
	{key: "codex_command", set: func(c *Config, v string) error { c.CodexCommand = v; return nil }},
	{key: "external_review_tool", allowed: []string{ExternalToolCodex, ExternalToolCustom, ExternalToolNone},
		set: func(c *Config, v string) error {
			return setEnum(&c.ExternalReviewTool, v, ExternalToolCodex, ExternalToolCustom, ExternalToolNone)
		}},
	{key: "external_review_command", set: func(c *Config, v string) error { c.ExternalReviewCommand = v; return nil }},
	{key: "base_ref", set: func(c *Config, v string) error { c.BaseRef = v; return nil }},
	{key: "model", set: func(c *Config, v string) error { c.Model = v; return nil }},
	{key: "review_model", set: func(c *Config, v string) error { c.ReviewModel = v; return nil }},
	{key: "external_model", set: func(c *Config, v string) error { c.ExternalModel = v; return nil }},
	{key: "final_model", set: func(c *Config, v string) error { c.FinalModel = v; return nil }},
	{key: "finalize_model", set: func(c *Config, v string) error { c.FinalizeModel = v; return nil }},
	{key: "max_iterations", set: func(c *Config, v string) error { return setPositiveInt(&c.MaxIterations, v) }},
	{key: "external_max_iterations", set: func(c *Config, v string) error { return setPositiveInt(&c.ExternalMaxIterations, v) }},
	{key: "final_max_iterations", set: func(c *Config, v string) error { return setPositiveInt(&c.FinalMaxIterations, v) }},
	{key: "stalemate_patience", set: func(c *Config, v string) error { return setNonNegativeInt(&c.StalematePatience, v) }},
	{key: "session_timeout", set: func(c *Config, v string) error { return setDuration(&c.SessionTimeout, v) }},
	{key: "idle_timeout", set: func(c *Config, v string) error { return setDuration(&c.IdleTimeout, v) }},
	{key: "progress_interval", set: func(c *Config, v string) error { return setDuration(&c.ProgressInterval, v) }},
	{key: "finalize", set: func(c *Config, v string) error { return setBool(&c.Finalize, v) }},
	{key: "progress_dir", set: func(c *Config, v string) error { c.ProgressDir = v; return nil }},
	{key: "report_file", set: func(c *Config, v string) error { c.ReportFile = v; return nil }},
	{key: "checklist_budget", set: func(c *Config, v string) error { return setNonNegativeInt(&c.ChecklistBudget, v) }},
	{key: "validation_command", set: func(c *Config, v string) error { c.ValidationCommand = v; return nil }},
	{key: "debug", set: func(c *Config, v string) error { return setBool(&c.Debug, v) }},
	{key: "no_color", set: func(c *Config, v string) error { return setBool(&c.NoColor, v) }},
}

var fieldByKey = func() map[string]field {
	byKey := make(map[string]field, len(fields))
	for _, f := range fields {
		byKey[f.key] = f
	}
	return byKey
}()

// Keys lists every recognized setting, in declaration order.
func Keys() []string {
	keys := make([]string, 0, len(fields))
	for _, f := range fields {
		keys = append(keys, f.key)
	}
	return keys
}

// Allowed reports the accepted values for an enum setting, so a caller can name
// them when rejecting a flag value.
func Allowed(key string) []string { return slices.Clone(fieldByKey[key].allowed) }

func setEnum(dst *string, v string, allowed ...string) error {
	if !slices.Contains(allowed, v) {
		return fmt.Errorf("accepted values are %s", strings.Join(allowed, ", "))
	}
	*dst = v
	return nil
}

func setPositiveInt(dst *int, v string) error {
	n, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("expected a whole number")
	}
	if n < 1 {
		return fmt.Errorf("expected a number greater than zero")
	}
	*dst = n
	return nil
}

func setNonNegativeInt(dst *int, v string) error {
	n, err := strconv.Atoi(v)
	if err != nil {
		return fmt.Errorf("expected a whole number")
	}
	if n < 0 {
		return fmt.Errorf("expected zero or a positive number")
	}
	*dst = n
	return nil
}

func setDuration(dst *time.Duration, v string) error {
	d, err := time.ParseDuration(v)
	if err != nil {
		return fmt.Errorf("expected a duration such as 30s, 5m, or 0 to disable")
	}
	if d < 0 {
		return fmt.Errorf("expected a duration of zero or more")
	}
	*dst = d
	return nil
}

func setBool(dst *bool, v string) error {
	switch strings.ToLower(v) {
	case "true", "yes", "on", "1":
		*dst = true
	case "false", "no", "off", "0":
		*dst = false
	default:
		return fmt.Errorf("expected true or false")
	}
	return nil
}
