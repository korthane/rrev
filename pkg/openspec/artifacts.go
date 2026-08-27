package openspec

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Artifact kinds a change can contribute to the review context.
const (
	KindProposal = "proposal"
	KindDesign   = "design"
	KindTasks    = "tasks"
	KindSpec     = "spec"
)

// Artifact is one loaded file from a change directory.
type Artifact struct {
	Kind string
	// Path is relative to the OpenSpec root, which is what reviewers are told
	// to open.
	Path string
	// Capability is the spec's path under specs/, empty for other kinds.
	Capability string
	Content    string
}

// Artifacts is everything loaded for one change. Optional artifacts are nil when
// absent; that absence is recorded in Notes rather than as a missing path.
type Artifacts struct {
	Proposal *Artifact
	Design   *Artifact
	Tasks    *Artifact
	Specs    []Artifact
	// SkipSpecs is set when the change declares it produces no delta specs.
	SkipSpecs bool
	// Notes describe missing or degraded artifacts in terms fit for output.
	Notes []string
}

// Paths lists the relative paths of every loaded artifact, proposal first.
func (a Artifacts) Paths() []string {
	var paths []string
	for _, art := range []*Artifact{a.Proposal, a.Design, a.Tasks} {
		if art != nil {
			paths = append(paths, art.Path)
		}
	}
	for _, spec := range a.Specs {
		paths = append(paths, spec.Path)
	}
	return paths
}

// LoadArtifacts reads the change's proposal, design, tasks, and every delta spec.
// A missing optional artifact is noted and skipped; an artifact that exists but
// cannot be read aborts with the filename and the underlying cause.
func LoadArtifacts(root Root, change Change) (Artifacts, error) {
	arts := Artifacts{SkipSpecs: declaresSkipSpecs(change.Dir)}

	for _, spec := range []struct{ kind, file string }{
		{KindProposal, "proposal.md"},
		{KindDesign, "design.md"},
		{KindTasks, "tasks.md"},
	} {
		art, err := readArtifact(root, filepath.Join(change.Dir, spec.file), spec.kind)
		if err != nil {
			return Artifacts{}, err
		}
		if art == nil {
			arts.Notes = append(arts.Notes, "no "+spec.kind+" document in change "+change.Name)
			continue
		}
		switch spec.kind {
		case KindProposal:
			arts.Proposal = art
		case KindDesign:
			arts.Design = art
		case KindTasks:
			arts.Tasks = art
		}
	}

	specs, err := loadDeltaSpecs(root, change)
	if err != nil {
		return Artifacts{}, err
	}
	arts.Specs = specs
	if len(specs) == 0 {
		note := "no delta specs in change " + change.Name
		if arts.SkipSpecs {
			note += " (the change declares skip_specs; proposal and tasks are the conformance basis)"
		}
		arts.Notes = append(arts.Notes, note)
	}
	return arts, nil
}

// loadDeltaSpecs walks the change's specs/ tree; every spec.md under it belongs
// to the capability named by its directory path.
func loadDeltaSpecs(root Root, change Change) ([]Artifact, error) {
	specsDir := filepath.Join(change.Dir, "specs")
	var specs []Artifact
	err := filepath.WalkDir(specsDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return fs.SkipAll
			}
			return fmt.Errorf("read delta specs under %s: %w", specsDir, err)
		}
		if entry.IsDir() || entry.Name() != "spec.md" {
			return nil
		}
		art, readErr := readArtifact(root, path, KindSpec)
		if readErr != nil {
			return readErr
		}
		if art == nil {
			return nil
		}
		capability, relErr := filepath.Rel(specsDir, filepath.Dir(path))
		if relErr != nil {
			return fmt.Errorf("capability path for %s: %w", path, relErr)
		}
		art.Capability = filepath.ToSlash(capability)
		specs = append(specs, *art)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return specs, nil
}

// readArtifact returns nil when the file does not exist, and an error naming the
// file for any other read failure.
func readArtifact(root Root, path, kind string) (*Artifact, error) {
	content, err := os.ReadFile(path) //nolint:gosec // paths derive from the resolved OpenSpec root
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s artifact %s: %w", kind, relToRoot(root, path), err)
	}
	return &Artifact{Kind: kind, Path: relToRoot(root, path), Content: string(content)}, nil
}

func relToRoot(root Root, path string) string {
	rel, err := filepath.Rel(root.Dir, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
}

// declaresSkipSpecs reads the change's `.openspec.yaml` for a truthy
// `skip_specs`, the flag OpenSpec uses to mark a change that produces no specs.
func declaresSkipSpecs(changeDir string) bool {
	content, err := os.ReadFile(filepath.Join(changeDir, ".openspec.yaml")) //nolint:gosec // path derives from the resolved change
	if err != nil {
		return false
	}
	for line := range strings.SplitSeq(string(content), "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found || strings.TrimSpace(key) != "skip_specs" {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "true", "yes", "on":
			return true
		}
	}
	return false
}
