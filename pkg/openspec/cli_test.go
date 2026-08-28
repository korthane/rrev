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

func TestCLIErrorsCarryDiagnostics(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub CLI relies on a POSIX shell")
	}
	path := filepath.Join(t.TempDir(), "openspec")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 'boom: no root' >&2\nexit 1\n"), 0o755); err != nil { //nolint:gosec // test helper
		t.Fatal(err)
	}
	_, err := (openspec.CLI{Bin: path}).ListChanges(t.TempDir())
	if !errors.Is(err, openspec.ErrCLIUnavailable) {
		t.Fatalf("err = %v, want ErrCLIUnavailable", err)
	}
	if !strings.Contains(err.Error(), "boom: no root") {
		t.Errorf("err = %v, want the CLI's own diagnostic", err)
	}
}

func TestCLIRejectsNonJSON(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stub CLI relies on a POSIX shell")
	}
	path := filepath.Join(t.TempDir(), "openspec")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 'not json'\n"), 0o755); err != nil { //nolint:gosec // test helper
		t.Fatal(err)
	}
	if _, err := (openspec.CLI{Bin: path}).ExtractRequirements(t.TempDir(), "add-thing"); !errors.Is(err, openspec.ErrCLIUnavailable) {
		t.Errorf("err = %v, want ErrCLIUnavailable", err)
	}
}
