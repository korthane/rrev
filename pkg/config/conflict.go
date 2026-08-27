package config

import (
	"fmt"
	"slices"
	"strings"
)

// ConflictError reports settings that contradict each other, naming where each
// one was set. It is returned only when a flag is involved: a user who typed a
// contradiction gets an error, a stale config file gets an override.
type ConflictError struct {
	Keys []string
	Msg  string
}

func (e *ConflictError) Error() string { return e.Msg }

// conflict is one contradiction rrev knows how to spot: how to detect it, how
// to explain it, and what to do when only configuration files caused it.
type conflict struct {
	keys     []string
	detect   func(*Config) bool
	explain  string
	override func(*Config) string
}

var conflicts = []conflict{{
	keys:   []string{"executor", "external_review_tool"},
	detect: func(c *Config) bool { return c.Executor == ExecutorCodex && c.ExternalReviewTool == ExternalToolCodex },
	explain: "codex is both the primary executor and the external review tool, so the external phase would review its own work;" +
		" same-model self-review is not supported",
	override: func(c *Config) string {
		c.ExternalReviewTool = ExternalToolNone
		return "disabling the external review phase (external_review_tool = " + ExternalToolNone + ")"
	},
}, {
	keys: []string{"external_review_tool", "external_review_command"},
	detect: func(c *Config) bool {
		return c.ExternalReviewTool == ExternalToolCustom && strings.TrimSpace(c.ExternalReviewCommand) == ""
	},
	explain: "the external review tool is " + ExternalToolCustom + " but no external_review_command says what to run",
	override: func(c *Config) string {
		c.ExternalReviewTool = ExternalToolNone
		return "disabling the external review phase (external_review_tool = " + ExternalToolNone + ")"
	},
}}

// Reconcile detects settings that contradict each other. A contradiction any
// part of which came from the command line is a startup error; one that comes
// only from configuration files is resolved in place and described in the
// returned warnings, which the caller prints to stderr.
func (c *Config) Reconcile() ([]string, error) {
	var warnings []string
	for _, cf := range conflicts {
		if !cf.detect(c) {
			continue
		}
		if c.anySetByFlag(cf.keys) {
			return nil, &ConflictError{Keys: cf.keys, Msg: fmt.Sprintf("%s: %s", c.describe(cf.keys), cf.explain)}
		}
		warnings = append(warnings, fmt.Sprintf("%s: %s; %s", c.describe(cf.keys), cf.explain, cf.override(c)))
	}
	return warnings, nil
}

func (c *Config) anySetByFlag(keys []string) bool {
	return slices.ContainsFunc(keys, c.SetByFlag)
}

// describe names each setting with the source that gave it its value, so the
// user knows which file or flag to change.
func (c *Config) describe(keys []string) string {
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s (set in %s)", key, c.Origin(key)))
	}
	return strings.Join(parts, " and ")
}
