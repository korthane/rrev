package openspec_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/korthane/rrev/pkg/openspec"
)

// stubCLI writes an executable that replies to `list` and `show` with the JSON
// the real openspec CLI produced for testdata/repo, so the CLI code path is
// exercised on machines without openspec installed.
func stubCLI(t *testing.T) openspec.CLI {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("stub CLI relies on a POSIX shell")
	}
	fixtures, err := filepath.Abs("testdata")
	if err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\ncase \"$1\" in\n" +
		"list) cat " + filepath.Join(fixtures, "list.json") + " ;;\n" +
		"show) cat " + filepath.Join(fixtures, "show-add-thing.json") + " ;;\n" +
		"*) echo \"unexpected: $*\" >&2; exit 1 ;;\nesac\n"
	path := filepath.Join(t.TempDir(), "openspec")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil { //nolint:gosec // test helper
		t.Fatal(err)
	}
	return openspec.CLI{Bin: path}
}

func fixtureRoot(t *testing.T) openspec.Root {
	t.Helper()
	root, err := openspec.FindRoot("testdata/repo")
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func TestFindRoot(t *testing.T) {
	root := fixtureRoot(t)
	if filepath.Base(root.Dir) != "repo" {
		t.Fatalf("root = %q, want the fixture repo", root.Dir)
	}

	nested, err := openspec.FindRoot("testdata/repo/openspec/changes/add-thing")
	if err != nil {
		t.Fatal(err)
	}
	if nested.Dir != root.Dir {
		t.Errorf("nested root = %q, want %q", nested.Dir, root.Dir)
	}

	if _, err := openspec.FindRoot(t.TempDir()); !errors.Is(err, openspec.ErrNoRoot) {
		t.Errorf("err = %v, want ErrNoRoot", err)
	}
}

func TestDiscoverChangesViaCLI(t *testing.T) {
	disc := openspec.DiscoverChanges(stubCLI(t), fixtureRoot(t))
	if disc.Degraded {
		t.Errorf("degraded = true, want false: %s", disc.Note)
	}
	if got := disc.Names(); len(got) != 1 || got[0] != "add-thing" {
		t.Fatalf("names = %v, want [add-thing]", got)
	}
}

func TestDiscoverChangesFallsBackToFilesystem(t *testing.T) {
	disc := openspec.DiscoverChanges(openspec.CLI{Disabled: true}, fixtureRoot(t))
	if !disc.Degraded {
		t.Error("degraded = false, want true when the CLI is unavailable")
	}
	if !strings.Contains(disc.Note, "openspec CLI unavailable") {
		t.Errorf("note = %q, want it to explain the degraded mode", disc.Note)
	}
	if got := disc.Names(); len(got) != 1 || got[0] != "add-thing" {
		t.Fatalf("names = %v, want [add-thing]: archived changes must be excluded", got)
	}
}

func TestSelectChange(t *testing.T) {
	root := fixtureRoot(t)
	disc := openspec.DiscoverChanges(openspec.CLI{Disabled: true}, root)

	auto, err := openspec.SelectChange(root, "", disc)
	if err != nil {
		t.Fatal(err)
	}
	if auto.Name != "add-thing" {
		t.Errorf("auto-detected %q, want add-thing", auto.Name)
	}

	named, err := openspec.SelectChange(root, "add-thing", disc)
	if err != nil {
		t.Fatal(err)
	}
	if named.Dir != auto.Dir {
		t.Errorf("named dir = %q, want %q", named.Dir, auto.Dir)
	}

	_, err = openspec.SelectChange(root, "no-such-change", disc)
	if !errors.Is(err, openspec.ErrUnknownChange) {
		t.Fatalf("err = %v, want ErrUnknownChange", err)
	}
	if !strings.Contains(err.Error(), "no-such-change") || !strings.Contains(err.Error(), "add-thing") {
		t.Errorf("err = %v, want it to name the change and list the available ones", err)
	}
}

func TestSelectChangeAmbiguousAndEmpty(t *testing.T) {
	root := openspec.Root{Dir: t.TempDir()}
	if err := os.MkdirAll(root.ChangesDir(), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := openspec.SelectChange(root, "", openspec.DiscoverChanges(openspec.CLI{Disabled: true}, root)); !errors.Is(err, openspec.ErrNoActiveChanges) {
		t.Errorf("err = %v, want ErrNoActiveChanges", err)
	}

	for _, name := range []string{"add-a", "add-b"} {
		if err := os.MkdirAll(filepath.Join(root.ChangesDir(), name), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	_, err := openspec.SelectChange(root, "", openspec.DiscoverChanges(openspec.CLI{Disabled: true}, root))
	if !errors.Is(err, openspec.ErrAmbiguousChange) {
		t.Fatalf("err = %v, want ErrAmbiguousChange", err)
	}
	if !strings.Contains(err.Error(), "add-a") || !strings.Contains(err.Error(), "add-b") {
		t.Errorf("err = %v, want it to list every candidate", err)
	}
}

func TestResolveChangeFindsArchivedByName(t *testing.T) {
	root := fixtureRoot(t)
	disc := openspec.DiscoverChanges(openspec.CLI{Disabled: true}, root)
	archived, err := openspec.ResolveChange(root, "old-thing", disc)
	if err != nil {
		t.Fatal(err)
	}
	if !archived.Archived {
		t.Error("Archived = false, want true for a change under the archive directory")
	}
}
