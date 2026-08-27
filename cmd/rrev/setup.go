package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"

	"github.com/korthane/rrev/pkg/config"
	"github.com/korthane/rrev/pkg/executor"
	"github.com/korthane/rrev/pkg/git"
	"github.com/korthane/rrev/pkg/openspec"
	"github.com/korthane/rrev/pkg/processor"
)

// startup is what preflight resolved: the repository under review, the settings
// the run uses, the change it is judged against, and the executors it will
// invoke. Nothing here has run a phase yet.
type startup struct {
	Repo   *git.Repo
	Config *config.Config
	Assets config.Assets
	Review openspec.Context

	BaseRef string
	Mode    processor.Mode

	Primary  executor.Executor
	External executor.Executor

	// Warnings are the reconciled configuration conflicts and degraded modes
	// worth telling the user about before the first phase.
	Warnings []string
}

// prepare runs every startup check in the order a failure is most useful in:
// the repository first, since nothing else can be resolved without it, then the
// settings, the base ref, the change, and finally the executables. It never
// starts a phase.
func prepare(ctx context.Context, opts *options, dir string) (*startup, error) {
	repo, err := git.New(ctx, dir)
	if err != nil {
		if errors.Is(err, git.ErrNotRepository) {
			return nil, fmt.Errorf("a git repository is required: %s is not inside one", dir)
		}
		return nil, err
	}

	resolved, err := config.Resolve(config.Options{RepoRoot: repo.Root(), Flags: opts.Flags})
	if err != nil {
		return nil, describeFlagError(err)
	}
	cfg := resolved.Config

	warnings, err := cfg.Reconcile()
	if err != nil {
		return nil, err
	}

	baseRef, err := resolveBaseRef(ctx, repo, cfg)
	if err != nil {
		return nil, err
	}

	review, err := resolveReview(dir, opts.Change)
	if err != nil {
		return nil, err
	}
	warnings = append(warnings, review.Notes...)

	primary, err := executor.Primary(cfg)
	if err != nil {
		return nil, fmt.Errorf("primary executor: %w", err)
	}
	external, err := executor.External(cfg)
	if err != nil {
		return nil, fmt.Errorf("external review tool: %w", err)
	}
	if err := checkExecutables(cfg, primary, external); err != nil {
		return nil, err
	}

	return &startup{
		Repo: repo, Config: cfg, Assets: resolved.Assets, Review: review,
		BaseRef: baseRef, Mode: opts.Mode,
		Primary: primary, External: external, Warnings: warnings,
	}, nil
}

// resolveBaseRef settles what the review diffs against: the configured ref, or
// the repository's own default branch when none is configured.
func resolveBaseRef(ctx context.Context, repo *git.Repo, cfg *config.Config) (string, error) {
	ref := strings.TrimSpace(cfg.BaseRef)
	if ref == "" {
		detected, err := repo.DefaultBranch(ctx)
		if err != nil {
			return "", fmt.Errorf("%w; name the ref to review against with --base-ref", err)
		}
		return detected, nil
	}
	if !repo.RefExists(ctx, ref) {
		return "", fmt.Errorf("base ref %q (set in %s) does not name a reachable commit; "+
			"name a reachable one with --base-ref", ref, cfg.Origin("base_ref"))
	}
	return ref, nil
}

// resolveReview selects the change to review and loads its artifacts, which is
// also the check that the change is readable.
func resolveReview(dir, name string) (openspec.Context, error) {
	root, err := openspec.FindRoot(dir)
	if err != nil {
		return openspec.Context{}, err
	}
	cli := openspec.CLI{}
	disc := openspec.DiscoverChanges(cli, root)
	change, err := openspec.SelectChange(root, name, disc)
	if err != nil {
		return openspec.Context{}, err
	}
	review, err := openspec.Resolve(cli, root, change, disc)
	if err != nil {
		return openspec.Context{}, fmt.Errorf("change %q: %w", change.Name, err)
	}
	return review, nil
}

// commandKeys maps an executor to the setting that named its executable, so a
// missing binary can be reported against the place it was configured.
var commandKeys = map[string]string{
	config.ExecutorClaude:     "claude_command",
	config.ExecutorCodex:      "codex_command",
	config.ExternalToolCustom: "external_review_command",
}

// checkExecutables verifies every executable the run intends to invoke is on
// PATH. An executor with no binary to check, such as a mock, is skipped.
func checkExecutables(cfg *config.Config, execs ...executor.Executor) error {
	for _, e := range execs {
		if e == nil || e.Bin() == "" {
			continue
		}
		if _, err := exec.LookPath(e.Bin()); err != nil {
			return fmt.Errorf("%s executable %q was not found on PATH (set in %s)",
				e.Name(), e.Bin(), cfg.Origin(commandKeys[e.Name()]))
		}
	}
	return nil
}
