// Package openspec loads OpenSpec change artifacts and extracts the
// requirements and scenarios a review is conducted against.
package openspec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// DirName is the directory an OpenSpec-driven repository keeps its specs in.
const DirName = "openspec"

const (
	changesDirName = "changes"
	archiveDirName = "archive"
)

// ErrNoRoot reports that no OpenSpec root exists at or above the starting directory.
var ErrNoRoot = errors.New("an OpenSpec-driven repository is required: no openspec/ directory found")

// ErrUnknownChange reports a change name that resolves to no directory.
var ErrUnknownChange = errors.New("unknown change")

// ErrNoActiveChanges reports that auto-detection found nothing to review.
var ErrNoActiveChanges = errors.New("no active OpenSpec changes")

// ErrAmbiguousChange reports that auto-detection found more than one candidate.
var ErrAmbiguousChange = errors.New("more than one active OpenSpec change")

// Root is a directory containing an `openspec/` tree.
type Root struct {
	Dir string
}

// SpecDir is the `openspec/` directory itself.
func (r Root) SpecDir() string { return filepath.Join(r.Dir, DirName) }

// ChangesDir holds one directory per active change.
func (r Root) ChangesDir() string { return filepath.Join(r.SpecDir(), changesDirName) }

// ArchiveDir holds changes that have already been archived.
func (r Root) ArchiveDir() string { return filepath.Join(r.ChangesDir(), archiveDirName) }

// FindRoot walks up from start looking for an `openspec/` directory, mirroring
// how the openspec CLI resolves its nearest root.
func FindRoot(start string) (Root, error) {
	dir, err := filepath.Abs(start)
	if err != nil {
		return Root{}, fmt.Errorf("resolve %q: %w", start, err)
	}
	for {
		if info, err := os.Stat(filepath.Join(dir, DirName)); err == nil && info.IsDir() {
			return Root{Dir: dir}, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return Root{}, fmt.Errorf("%w (searched from %s)", ErrNoRoot, start)
		}
		dir = parent
	}
}

// Change is one OpenSpec change directory.
type Change struct {
	Name     string
	Dir      string
	Archived bool
}

// Discovery is the outcome of listing the changes available for review.
type Discovery struct {
	// Changes holds active changes only; archived ones are never auto-detected.
	Changes []Change
	// Degraded is set when the openspec CLI was unusable and rrev enumerated
	// the changes directory itself.
	Degraded bool
	// Note explains the degraded mode in terms suitable for terminal output.
	Note string
}

// Names lists the discovered change names in listing order.
func (d Discovery) Names() []string {
	names := make([]string, 0, len(d.Changes))
	for _, c := range d.Changes {
		names = append(names, c.Name)
	}
	return names
}

// DiscoverChanges lists the active changes under root. It prefers the openspec
// CLI's machine-readable listing and falls back to enumerating the changes
// directory so a review can run without that CLI installed.
func DiscoverChanges(cli CLI, root Root) Discovery {
	names, err := cli.ListChanges(root.Dir)
	if err != nil {
		disc := discoverFromDisk(root)
		disc.Degraded = true
		disc.Note = "openspec CLI unavailable (" + err.Error() + "); enumerating " +
			filepath.Join(DirName, changesDirName) + " directly"
		return disc
	}
	changes := make([]Change, 0, len(names))
	for _, name := range names {
		changes = append(changes, Change{Name: name, Dir: filepath.Join(root.ChangesDir(), name)})
	}
	return Discovery{Changes: changes}
}

// discoverFromDisk enumerates change directories, skipping the archive subtree.
func discoverFromDisk(root Root) Discovery {
	entries, err := os.ReadDir(root.ChangesDir())
	if err != nil {
		return Discovery{}
	}
	var changes []Change
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == archiveDirName || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		changes = append(changes, Change{Name: entry.Name(), Dir: filepath.Join(root.ChangesDir(), entry.Name())})
	}
	slices.SortFunc(changes, func(a, b Change) int { return strings.Compare(a.Name, b.Name) })
	return Discovery{Changes: changes}
}

// ResolveChange finds the named change. An archived change resolves only when
// named explicitly, which is why this does not go through Discovery.
func ResolveChange(root Root, name string, disc Discovery) (Change, error) {
	for _, dir := range []string{root.ChangesDir(), root.ArchiveDir()} {
		path := filepath.Join(dir, name)
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return Change{Name: name, Dir: path, Archived: dir == root.ArchiveDir()}, nil
		}
	}
	return Change{}, fmt.Errorf("%w %q; available: %s", ErrUnknownChange, name, availableList(disc))
}

// SelectChange resolves the change to review: the named one, or the sole active
// change when no name is given. It refuses to guess between several candidates.
func SelectChange(root Root, name string, disc Discovery) (Change, error) {
	if name != "" {
		return ResolveChange(root, name, disc)
	}
	switch len(disc.Changes) {
	case 0:
		return Change{}, fmt.Errorf("%w under %s", ErrNoActiveChanges, root.ChangesDir())
	case 1:
		return disc.Changes[0], nil
	default:
		return Change{}, fmt.Errorf("%w; name one of: %s", ErrAmbiguousChange, availableList(disc))
	}
}

func availableList(disc Discovery) string {
	if len(disc.Changes) == 0 {
		return "none"
	}
	return strings.Join(disc.Names(), ", ")
}
