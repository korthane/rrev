// Package git inspects the repository under review: base ref, diff, and commits.
package git

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"
)

// ErrNotRepository is returned when the working directory is not inside a git work tree.
var ErrNotRepository = errors.New("not a git repository")

// ErrNoChanges reports that the branch is identical to the base ref, so there is
// nothing to review and no executor should be invoked.
var ErrNoChanges = errors.New("no changes relative to the base ref")

// ErrNoDefaultBranch is returned when no default branch can be detected for the repository.
var ErrNoDefaultBranch = errors.New("unable to detect the repository default branch")

// CommandError carries the failure of a single git invocation, including stderr
// so callers can surface git's own diagnostics.
type CommandError struct {
	Args   []string
	Stderr string
	Err    error
}

// Error renders the failed command, git's stderr, and the underlying exec error.
func (e *CommandError) Error() string {
	msg := "git " + strings.Join(e.Args, " ") + ": " + e.Err.Error()
	if e.Stderr != "" {
		msg += ": " + e.Stderr
	}
	return msg
}

// Unwrap exposes the underlying exec error for errors.Is and errors.As.
func (e *CommandError) Unwrap() error { return e.Err }

// Commit is one commit on the branch under review.
type Commit struct {
	Hash    string
	Author  string
	Date    time.Time
	Subject string
}

// Repo runs git commands against a single repository work tree.
type Repo struct {
	dir  string
	root string
}

// New opens the repository containing dir. It fails with ErrNotRepository when
// dir is outside a git work tree.
func New(ctx context.Context, dir string) (*Repo, error) {
	repo := &Repo{dir: dir}
	root, err := repo.git(ctx, "rev-parse", "--show-toplevel")
	if err != nil {
		if notRepository(err) {
			return nil, fmt.Errorf("%w: %s: %w", ErrNotRepository, dir, err)
		}
		return nil, err
	}
	// Every git command runs from the work-tree root, which is also where the
	// executors run: a diff issued from a subdirectory is restricted to it when
	// diff.relative is set, which would hide changes elsewhere on the branch.
	repo.root, repo.dir = root, root
	return repo, nil
}

// notRepository distinguishes git rejecting the directory from git failing to
// run at all. A missing git binary or a repository git refuses to open, such as
// one with dubious ownership, must not be reported as "not a git repository":
// that sends the user looking for a repository that is right there.
func notRepository(err error) bool {
	var cmdErr *CommandError
	if !errors.As(err, &cmdErr) {
		return false
	}
	var exitErr *exec.ExitError
	if !errors.As(cmdErr.Err, &exitErr) {
		return false
	}
	return strings.Contains(cmdErr.Stderr, "not a git repository")
}

// Root is the absolute path of the repository work tree.
func (r *Repo) Root() string { return r.root }

// DefaultBranch detects the ref a review diffs against when none is configured.
// It prefers each remote's recorded HEAD, so a repository whose default branch is
// named neither main nor master still resolves.
func (r *Repo) DefaultBranch(ctx context.Context) (string, error) {
	for _, remote := range r.remotes(ctx) {
		head, err := r.git(ctx, "symbolic-ref", "--quiet", "--short", "refs/remotes/"+remote+"/HEAD")
		if err != nil || head == "" {
			continue
		}
		if ref, ok := r.pickBase(ctx, strings.TrimPrefix(head, remote+"/"), head); ok {
			return ref, nil
		}
	}
	if configured, err := r.git(ctx, "config", "--get", "init.defaultBranch"); err == nil {
		if ref, ok := r.pickBase(ctx, configured, "origin/"+configured); ok {
			return ref, nil
		}
	}
	for _, name := range []string{"main", "master"} {
		if ref, ok := r.pickBase(ctx, name, "origin/"+name); ok {
			return ref, nil
		}
	}
	return "", ErrNoDefaultBranch
}

// pickBase chooses between a default branch and its remote-tracking counterpart,
// reporting false when neither resolves. The two can disagree - a local branch
// left behind by a fetch, or diverged from its remote - and the wrong choice
// puts commits the branch under review already contains outside the merge base,
// so the three-dot diff would present them as part of the review. Whichever ref
// forks from HEAD later is therefore the base, with the friendlier local name
// winning when they fork at the same commit.
func (r *Repo) pickBase(ctx context.Context, local, remote string) (string, bool) {
	switch {
	case !r.RefExists(ctx, local):
		return remote, r.RefExists(ctx, remote)
	case r.RefExists(ctx, remote) && r.forksLater(ctx, remote, local):
		return remote, true
	default:
		return local, true
	}
}

// forksLater reports whether ref shares more history with HEAD than other does,
// which is exactly when diffing against ref excludes commits that diffing
// against other would wrongly include.
func (r *Repo) forksLater(ctx context.Context, ref, other string) bool {
	base, ok := r.mergeBase(ctx, ref, "HEAD")
	against, okOther := r.mergeBase(ctx, other, "HEAD")
	if !ok || !okOther {
		// A ref with no common history cannot serve as a three-dot base at all,
		// so the one that still shares history with HEAD wins outright.
		return ok
	}
	if base == against {
		return false
	}
	return r.contains(ctx, base, against)
}

// mergeBase returns the best common ancestor of two refs, reporting false when
// they share no history or either ref does not resolve.
func (r *Repo) mergeBase(ctx context.Context, ref, other string) (string, bool) {
	hash, err := r.git(ctx, "merge-base", ref, other)
	return hash, err == nil && hash != ""
}

// contains reports whether ref already holds every commit of other.
func (r *Repo) contains(ctx context.Context, ref, other string) bool {
	_, err := r.git(ctx, "merge-base", "--is-ancestor", other, ref)
	return err == nil
}

// RefExists reports whether ref resolves to a commit in this repository.
func (r *Repo) RefExists(ctx context.Context, ref string) bool {
	if ref == "" {
		return false
	}
	_, err := r.git(ctx, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	return err == nil
}

// ResolveRef returns the commit hash ref points at.
func (r *Repo) ResolveRef(ctx context.Context, ref string) (string, error) {
	hash, err := r.git(ctx, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", fmt.Errorf("resolve ref %q: %w", ref, err)
	}
	return hash, nil
}

// HeadHash is the commit hash of HEAD.
func (r *Repo) HeadHash(ctx context.Context) (string, error) {
	return r.ResolveRef(ctx, "HEAD")
}

// Diff returns the three-dot diff between baseRef and HEAD: the branch's own
// changes, excluding whatever landed on the base ref since the branch started.
func (r *Repo) Diff(ctx context.Context, baseRef string) (string, error) {
	out, err := r.git(ctx, "diff", baseRef+"...HEAD")
	if err != nil {
		return "", fmt.Errorf("diff against %q: %w", baseRef, err)
	}
	return out, nil
}

// ChangedFiles lists the paths the branch changes relative to baseRef.
func (r *Repo) ChangedFiles(ctx context.Context, baseRef string) ([]string, error) {
	out, err := r.git(ctx, "diff", "--name-only", baseRef+"...HEAD")
	if err != nil {
		return nil, fmt.Errorf("diff against %q: %w", baseRef, err)
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// HasChanges reports whether the branch differs from baseRef at all. It is the
// cheap precondition for a review: an unchanged branch needs no executor.
func (r *Repo) HasChanges(ctx context.Context, baseRef string) (bool, error) {
	files, err := r.ChangedFiles(ctx, baseRef)
	if err != nil {
		return false, err
	}
	return len(files) > 0, nil
}

// EnsureChanges returns ErrNoChanges when the branch is identical to baseRef.
func (r *Repo) EnsureChanges(ctx context.Context, baseRef string) error {
	changed, err := r.HasChanges(ctx, baseRef)
	if err != nil {
		return err
	}
	if !changed {
		return fmt.Errorf("%w %q", ErrNoChanges, baseRef)
	}
	return nil
}

const (
	fieldSep  = "\x1f"
	recordSep = "\x1e"
)

// Commits lists the commits on the branch that are not reachable from baseRef,
// oldest first.
func (r *Repo) Commits(ctx context.Context, baseRef string) ([]Commit, error) {
	format := strings.Join([]string{"%H", "%an", "%aI", "%s"}, fieldSep) + recordSep
	out, err := r.git(ctx, "log", "--reverse", "--format="+format, baseRef+"..HEAD")
	if err != nil {
		return nil, fmt.Errorf("commit log against %q: %w", baseRef, err)
	}
	var commits []Commit
	for record := range strings.SplitSeq(out, recordSep) {
		record = strings.Trim(record, "\n")
		if record == "" {
			continue
		}
		fields := strings.Split(record, fieldSep)
		if len(fields) != 4 {
			return nil, fmt.Errorf("malformed commit record %q", record)
		}
		date, err := time.Parse(time.RFC3339, fields[2])
		if err != nil {
			return nil, fmt.Errorf("parse commit date %q: %w", fields[2], err)
		}
		commits = append(commits, Commit{Hash: fields[0], Author: fields[1], Date: date, Subject: fields[3]})
	}
	return commits, nil
}

// WorkingTreeFingerprint hashes the uncommitted state of the work tree. Two
// equal fingerprints across iterations mean an iteration changed nothing, which
// is how stalemate detection tells progress from spinning.
//
// Untracked files contribute their paths but not their contents, so editing an
// untracked file is invisible here.
func (r *Repo) WorkingTreeFingerprint(ctx context.Context) (string, error) {
	status, err := r.git(ctx, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return "", fmt.Errorf("working tree status: %w", err)
	}
	var diff string
	if r.RefExists(ctx, "HEAD") {
		if diff, err = r.git(ctx, "diff", "HEAD"); err != nil {
			return "", fmt.Errorf("working tree diff: %w", err)
		}
	}
	sum := sha256.Sum256([]byte(status + "\x00" + diff))
	return hex.EncodeToString(sum[:]), nil
}

// remotes lists the repository's remotes with origin first, since origin's
// recorded HEAD is the most reliable statement of the default branch.
func (r *Repo) remotes(ctx context.Context) []string {
	out, err := r.git(ctx, "remote")
	if err != nil || out == "" {
		return nil
	}
	remotes := strings.Split(out, "\n")
	slices.SortStableFunc(remotes, func(a, b string) int {
		return boolToInt(b == "origin") - boolToInt(a == "origin")
	})
	return remotes
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (r *Repo) git(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", &CommandError{Args: args, Stderr: strings.TrimSpace(stderr.String()), Err: err}
	}
	return strings.TrimRight(string(out), "\n"), nil
}
