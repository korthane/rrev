package executor_test

import (
	"errors"
	"testing"

	"github.com/korthane/rrev/pkg/config"
	"github.com/korthane/rrev/pkg/executor"
)

func defaultConfig(t *testing.T) *config.Config {
	t.Helper()
	resolved, err := config.Resolve(config.Options{ProjectDir: t.TempDir(), UserDir: t.TempDir()})
	if err != nil {
		t.Fatalf("resolve configuration: %v", err)
	}
	return resolved.Config
}

func TestPrimaryAndExternalFromDefaults(t *testing.T) {
	cfg := defaultConfig(t)

	primary, err := executor.Primary(cfg)
	if err != nil {
		t.Fatalf("primary executor: %v", err)
	}
	if primary.Name() != "claude" {
		t.Errorf("primary = %q, want claude", primary.Name())
	}

	external, err := executor.External(cfg)
	if err != nil {
		t.Fatalf("external executor: %v", err)
	}
	if external == nil || external.Name() != "codex" {
		t.Fatalf("external = %v, want codex", external)
	}
}

func TestExternalDisabled(t *testing.T) {
	cfg := defaultConfig(t)
	cfg.ExternalReviewTool = config.ExternalToolNone

	external, err := executor.External(cfg)
	if err != nil {
		t.Fatalf("external executor: %v", err)
	}
	if external != nil {
		t.Errorf("external = %v, want nil so the phase reports itself skipped", external)
	}
}

func TestNewCustomUsesConfiguredCommand(t *testing.T) {
	cfg := defaultConfig(t)
	cfg.ExternalReviewTool = config.ExternalToolCustom
	cfg.ExternalReviewCommand = "./scripts/review.sh --json"

	external, err := executor.External(cfg)
	if err != nil {
		t.Fatalf("external executor: %v", err)
	}
	if external.Bin() != "./scripts/review.sh" {
		t.Errorf("Bin() = %q", external.Bin())
	}
}

func TestNewCustomWithoutCommand(t *testing.T) {
	cfg := defaultConfig(t)
	cfg.ExternalReviewTool = config.ExternalToolCustom

	if _, err := executor.External(cfg); !errors.Is(err, executor.ErrNoCommand) {
		t.Errorf("error = %v, want ErrNoCommand", err)
	}
}

func TestNewUsesConfiguredCommands(t *testing.T) {
	cfg := defaultConfig(t)
	cfg.ClaudeCommand = "/opt/bin/claude"
	cfg.CodexCommand = "/opt/bin/codex"

	claude, err := executor.New(config.ExecutorClaude, cfg)
	if err != nil {
		t.Fatalf("new claude: %v", err)
	}
	codex, err := executor.New(config.ExecutorCodex, cfg)
	if err != nil {
		t.Fatalf("new codex: %v", err)
	}
	if claude.Bin() != "/opt/bin/claude" || codex.Bin() != "/opt/bin/codex" {
		t.Errorf("bins = %q, %q", claude.Bin(), codex.Bin())
	}
}

func TestNewUnknownExecutor(t *testing.T) {
	if _, err := executor.New("gemini", defaultConfig(t)); err == nil {
		t.Error("unknown executor accepted")
	}
}
