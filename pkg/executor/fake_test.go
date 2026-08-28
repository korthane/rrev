package executor_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeTool is an executable stand-in for a CLI: it replays a recorded output
// stream and records how it was invoked, so the executors can be exercised
// without the real tool installed.
type fakeTool struct {
	path      string
	argsFile  string
	stdinFile string
}

type fakeToolOpts struct {
	// fixture is a testdata file whose contents the tool writes to stdout.
	fixture string
	// stdout is written verbatim, for a tool with no recorded stream.
	stdout string
	stderr string
	exit   int
}

func newFakeTool(t *testing.T, opts fakeToolOpts) fakeTool {
	t.Helper()
	dir := t.TempDir()
	tool := fakeTool{
		path:      filepath.Join(dir, "faketool"),
		argsFile:  filepath.Join(dir, "args"),
		stdinFile: filepath.Join(dir, "stdin"),
	}

	var body strings.Builder
	fmt.Fprintf(&body, "#!/bin/sh\nprintf '%%s\\n' \"$@\" > %q\ncat > %q\n", tool.argsFile, tool.stdinFile)
	if opts.fixture != "" {
		abs, err := filepath.Abs(filepath.Join("testdata", opts.fixture))
		if err != nil {
			t.Fatalf("resolve fixture: %v", err)
		}
		fmt.Fprintf(&body, "cat %q\n", abs)
	}
	if opts.stdout != "" {
		// The output goes through a file rather than an inlined printf, so an
		// escape sequence in it reaches the executor unmangled by the shell.
		out := filepath.Join(dir, "stdout")
		if err := os.WriteFile(out, []byte(opts.stdout), 0o600); err != nil {
			t.Fatalf("write fake tool output: %v", err)
		}
		fmt.Fprintf(&body, "cat %q\n", out)
	}
	if opts.stderr != "" {
		fmt.Fprintf(&body, "printf '%%s' %q >&2\n", opts.stderr)
	}
	fmt.Fprintf(&body, "exit %d\n", opts.exit)

	if err := os.WriteFile(tool.path, []byte(body.String()), 0o700); err != nil {
		t.Fatalf("write fake tool: %v", err)
	}
	return tool
}

func (f fakeTool) args(t *testing.T) []string {
	t.Helper()
	content := strings.TrimRight(f.read(t, f.argsFile), "\n")
	if content == "" {
		return nil
	}
	return strings.Split(content, "\n")
}

func (f fakeTool) stdin(t *testing.T) string { return f.read(t, f.stdinFile) }

func (f fakeTool) read(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path) //nolint:gosec // the path is created by this test
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Base(path), err)
	}
	return string(content)
}

func hasArg(args []string, name, value string) bool {
	for i, arg := range args {
		if arg == name && i+1 < len(args) && args[i+1] == value {
			return true
		}
	}
	return false
}
