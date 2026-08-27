package openspec_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/korthane/rrev/pkg/openspec"
)

func TestLoadArtifactsFullSet(t *testing.T) {
	root := fixtureRoot(t)
	change, err := openspec.ResolveChange(root, "add-thing", openspec.Discovery{})
	if err != nil {
		t.Fatal(err)
	}
	arts, err := openspec.LoadArtifacts(root, change)
	if err != nil {
		t.Fatal(err)
	}
	for name, art := range map[string]*openspec.Artifact{
		"proposal": arts.Proposal, "design": arts.Design, "tasks": arts.Tasks,
	} {
		if art == nil {
			t.Fatalf("%s not loaded", name)
		}
		if art.Content == "" {
			t.Errorf("%s content is empty", name)
		}
	}
	if len(arts.Specs) != 2 {
		t.Fatalf("loaded %d delta specs, want 2", len(arts.Specs))
	}
	caps := []string{arts.Specs[0].Capability, arts.Specs[1].Capability}
	slices.Sort(caps)
	if want := []string{"auth", "billing/invoices"}; !slices.Equal(caps, want) {
		t.Errorf("capabilities = %v, want %v", caps, want)
	}
	want := "openspec/changes/add-thing/proposal.md"
	if arts.Proposal.Path != want {
		t.Errorf("proposal path = %q, want %q", arts.Proposal.Path, want)
	}
	if len(arts.Paths()) != 5 {
		t.Errorf("paths = %v, want all five artifacts", arts.Paths())
	}
	if len(arts.Notes) != 0 {
		t.Errorf("notes = %v, want none for a complete change", arts.Notes)
	}
}

func TestLoadArtifactsOptionalMissing(t *testing.T) {
	root := openspec.Root{Dir: t.TempDir()}
	change := writeChange(t, root, "add-min", map[string]string{
		"proposal.md": "## Why\n\nBecause.\n",
	})
	arts, err := openspec.LoadArtifacts(root, change)
	if err != nil {
		t.Fatal(err)
	}
	if arts.Design != nil {
		t.Error("Design is set, want nil rather than a path to a missing file")
	}
	if !slices.ContainsFunc(arts.Notes, func(n string) bool { return strings.Contains(n, "no design document") }) {
		t.Errorf("notes = %v, want one recording the absent design document", arts.Notes)
	}
	if !slices.ContainsFunc(arts.Notes, func(n string) bool { return strings.Contains(n, "no delta specs") }) {
		t.Errorf("notes = %v, want one recording the empty delta spec set", arts.Notes)
	}
	for _, path := range arts.Paths() {
		if strings.Contains(path, "design.md") {
			t.Errorf("paths = %v, want no reference to the missing design document", arts.Paths())
		}
	}
}

func TestLoadArtifactsSkipSpecs(t *testing.T) {
	root := openspec.Root{Dir: t.TempDir()}
	change := writeChange(t, root, "add-nospec", map[string]string{
		".openspec.yaml": "schema: spec-driven\nskip_specs: true\n",
		"proposal.md":    "## Why\n\nNo specs needed.\n",
		"tasks.md":       "- [ ] 1.1 Do it\n",
	})
	arts, err := openspec.LoadArtifacts(root, change)
	if err != nil {
		t.Fatal(err)
	}
	if !arts.SkipSpecs {
		t.Fatal("SkipSpecs = false, want true when the change declares it")
	}
	if !slices.ContainsFunc(arts.Notes, func(n string) bool { return strings.Contains(n, "skip_specs") }) {
		t.Errorf("notes = %v, want one stating no delta specs are available", arts.Notes)
	}
	if arts.Proposal == nil || arts.Tasks == nil {
		t.Error("proposal and tasks must still be the conformance basis")
	}
}

func TestLoadArtifactsUnreadable(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
	root := openspec.Root{Dir: t.TempDir()}
	change := writeChange(t, root, "add-broken", map[string]string{"proposal.md": "## Why\n\nBecause.\n"})
	unreadable := filepath.Join(change.Dir, "design.md")
	if err := os.WriteFile(unreadable, []byte("secret"), 0o200); err != nil {
		t.Fatal(err)
	}
	_, err := openspec.LoadArtifacts(root, change)
	if err == nil {
		t.Fatal("err = nil, want a failure naming the unreadable artifact")
	}
	if !strings.Contains(err.Error(), "design.md") || !strings.Contains(err.Error(), "permission") {
		t.Errorf("err = %v, want it to name the file and the underlying cause", err)
	}
}

// writeChange creates a change directory under root from a file map.
func writeChange(t *testing.T, root openspec.Root, name string, files map[string]string) openspec.Change {
	t.Helper()
	dir := filepath.Join(root.ChangesDir(), name)
	for rel, content := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	return openspec.Change{Name: name, Dir: dir}
}
