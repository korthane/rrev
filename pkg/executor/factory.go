package executor

import (
	"fmt"

	"github.com/korthane/rrev/pkg/config"
)

// New builds the executor for a tool name using the resolved configuration, so
// a phase turns a configured string into something runnable.
func New(tool string, cfg *config.Config) (Executor, error) {
	switch tool {
	case config.ExecutorClaude:
		return Claude{Command: cfg.ClaudeCommand, Debug: cfg.Debug}, nil
	case config.ExecutorCodex:
		return Codex{Command: cfg.CodexCommand, Debug: cfg.Debug}, nil
	case config.ExternalToolCustom:
		if cfg.ExternalReviewCommand == "" {
			return nil, ErrNoCommand
		}
		return Custom{Command: cfg.ExternalReviewCommand, Debug: cfg.Debug}, nil
	default:
		return nil, fmt.Errorf("unknown executor %q", tool)
	}
}

// Primary builds the executor that runs the review phases and applies fixes.
func Primary(cfg *config.Config) (Executor, error) { return New(cfg.Executor, cfg) }

// External builds the independent second opinion for the external review loop.
// It returns a nil executor when external review is disabled, which is what
// tells the pipeline to report the phase as skipped.
func External(cfg *config.Config) (Executor, error) {
	if cfg.ExternalReviewTool == config.ExternalToolNone {
		return nil, nil
	}
	return New(cfg.ExternalReviewTool, cfg)
}
