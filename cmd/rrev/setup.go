package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
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
	if err := checkProgressDir(repo.Root(), cfg); err != nil {
		return nil, err
	}

	baseRef, err := resolveBaseRef(ctx, repo, cfg)
	if err != nil {
		return nil, err
	}
	// The change is resolved before the diff is checked: on a branch with no
	// changes, an unknown change name must still be reported as such rather
	// than hidden behind "nothing to review".
	review, err := resolveReview(dir, opts.Change)
	if err != nil {
		return nil, err
	}
	warnings = append(warnings, review.Notes...)

	if err := repo.EnsureChanges(ctx, baseRef); err != nil {
		return nil, err
	}

	primary, err := executor.Primary(cfg)
	if err != nil {
		return nil, fmt.Errorf("primary executor: %w", err)
	}
	external, err := executor.External(cfg)
	if err != nil {
		return nil, fmt.Errorf("external review tool: %w", err)
	}
	if err := checkExecutables(repo.Root(), cfg, opts.Mode, primary, external); err != nil {
		return nil, err
	}
	if err := checkPrompts(cfg, resolved.Assets, opts.Mode, external); err != nil {
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
// PATH. An executor with no binary to check, such as a mock, is skipped, and so
// is the external tool in a mode whose phases never reach it.
func checkExecutables(root string, cfg *config.Config, mode processor.Mode, primary, external executor.Executor) error {
	execs := []executor.Executor{primary}
	if runsExternalPhase(mode) {
		execs = append(execs, external)
	}
	for _, e := range execs {
		if e == nil || e.Bin() == "" {
			continue
		}
		if err := lookExecutable(root, e.Bin()); err != nil {
			return fmt.Errorf("%s executable %q was not found on PATH (set in %s)",
				e.Name(), e.Bin(), cfg.Origin(commandKeys[e.Name()]))
		}
	}
	return nil
}

// lookExecutable resolves a binary the way the run will: exec.Cmd resolves a
// relative path with a separator in it against Cmd.Dir, which is the repository
// root rather than rrev's own working directory.
func lookExecutable(root, bin string) error {
	if !filepath.IsAbs(bin) && filepath.Base(bin) != bin {
		bin = filepath.Join(root, bin)
	}
	_, err := exec.LookPath(bin)
	return err
}

// checkProgressDir rejects a progress directory that resolves onto the
// repository root, onto the filesystem root, or outside the repository
// altogether. rrev writes a catch-all ignore rule into that directory: at the
// repository root it would ignore the whole repository and leave the pipeline's
// own commits staging nothing, and above it, whatever tracks the parent.
func checkProgressDir(root string, cfg *config.Config) error {
	dir := filepath.Clean(absDir(root, cfg.ProgressDir))
	if dir != filepath.Clean(root) && filepath.Dir(dir) != dir && !escapes(root, dir) {
		return nil
	}
	return fmt.Errorf("progress_dir %q (set in %s) resolves to %s, which must be a directory of its own "+
		"inside the repository: rrev writes a catch-all ignore rule there",
		cfg.ProgressDir, cfg.Origin("progress_dir"), dir)
}

// escapes reports whether dir lies outside root.
func escapes(root, dir string) bool {
	rel, err := filepath.Rel(root, dir)
	return err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// runsExternalPhase reports whether the mode's phase sequence invokes the
// external review tool, read off the same prompt list preflight validates.
func runsExternalPhase(mode processor.Mode) bool {
	return slices.ContainsFunc(processor.PromptUses(mode, false),
		func(u processor.PromptUse) bool { return u.External })
}

// checkPrompts expands every prompt the selected mode will use, with the same
// agent resolution and executor rendering the run will apply. A broken override
// - an unknown variable, an agent that resolves to no file - is the author's
// bug, and finding it here costs nothing next to finding it mid-phase.
func checkPrompts(cfg *config.Config, assets config.Assets, mode processor.Mode, external executor.Executor) error {
	for _, use := range processor.PromptUses(mode, cfg.Finalize) {
		renderAs := cfg.Executor
		if use.External {
			if external == nil {
				// The external phase is disabled, so its prompt never expands.
				continue
			}
			renderAs = external.Name()
		}
		if _, err := (config.Expander{Assets: assets, Executor: renderAs}).Prompt(use.Name); err != nil {
			return fmt.Errorf("prompt %q: %w", use.Name, err)
		}
	}
	return nil
}
