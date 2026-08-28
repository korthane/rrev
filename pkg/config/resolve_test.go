package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeConfig creates dir and puts a configuration file with body in it.
func writeConfig(t *testing.T, dir, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestResolveNoFilesUsesEmbeddedDefaults(t *testing.T) {
	root := t.TempDir()
	res, err := Resolve(Options{RepoRoot: root, UserDir: filepath.Join(t.TempDir(), "absent")})
	if err != nil {
		t.Fatalf("resolve with no configuration files: %v", err)
	}

	cfg := res.Config
	if cfg.Executor != ExecutorClaude {
		t.Errorf("executor = %q, want %q", cfg.Executor, ExecutorClaude)
	}
	if cfg.ExternalReviewTool != ExternalToolCodex {
		t.Errorf("external_review_tool = %q, want %q", cfg.ExternalReviewTool, ExternalToolCodex)
	}
	if cfg.MaxIterations < 1 {
		t.Errorf("max_iterations = %d, want a positive default", cfg.MaxIterations)
	}
	if cfg.Finalize {
		t.Error("finalize defaults to enabled, want disabled")
	}
	if cfg.SessionTimeout != 0 || cfg.IdleTimeout != 0 {
		t.Errorf("timeouts default to %v/%v, want both disabled", cfg.SessionTimeout, cfg.IdleTimeout)
	}
	if cfg.ProgressDir == "" || cfg.ReportFile == "" {
		t.Errorf("progress_dir = %q, report_file = %q, want both set", cfg.ProgressDir, cfg.ReportFile)
	}

	for _, key := range Keys() {
		if src := cfg.Origin(key); src.Layer != LayerDefaults {
			t.Errorf("origin of %q = %v, want the embedded defaults", key, src.Layer)
		}
	}
}

func TestResolveProjectOverridesUser(t *testing.T) {
	root, userDir := t.TempDir(), t.TempDir()
	writeConfig(t, userDir, "executor = codex\nmodel = user-model\n")
	projectFile := writeConfig(t, filepath.Join(root, DirName), "model = project-model\n")

	res, err := Resolve(Options{RepoRoot: root, UserDir: userDir})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if got := res.Config.Model; got != "project-model" {
		t.Errorf("model = %q, want the project value", got)
	}
	if src := res.Config.Origin("model"); src.Layer != LayerProject || src.File != projectFile {
		t.Errorf("origin of model = %+v, want the project file", src)
	}
	// The user file still supplies what the project file omits.
	if got := res.Config.Executor; got != ExecutorCodex {
		t.Errorf("executor = %q, want the user value to survive", got)
	}
}

func TestResolvePartialFileDoesNotZeroLowerPrecedence(t *testing.T) {
	root, userDir := t.TempDir(), t.TempDir()
	writeConfig(t, userDir, "max_iterations = 3\nreview_model = sonnet\n")
	writeConfig(t, filepath.Join(root, DirName), "no_color = true\n")

	res, err := Resolve(Options{
		RepoRoot: root,
		UserDir:  userDir,
		Flags:    map[string]string{"base_ref": "develop"},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	cfg := res.Config

	if cfg.MaxIterations != 3 || cfg.ReviewModel != "sonnet" {
		t.Errorf("user values lost: max_iterations = %d, review_model = %q", cfg.MaxIterations, cfg.ReviewModel)
	}
	if !cfg.NoColor {
		t.Error("no_color = false, want the project value")
	}
	if cfg.BaseRef != "develop" {
		t.Errorf("base_ref = %q, want the flag value", cfg.BaseRef)
	}
	if cfg.Executor != ExecutorClaude {
		t.Errorf("executor = %q, want the embedded default", cfg.Executor)
	}
	if cfg.ExternalReviewTool != ExternalToolCodex {
		t.Errorf("external_review_tool = %q, want the embedded default", cfg.ExternalReviewTool)
	}
}

func TestResolveFlagBeatsConfig(t *testing.T) {
	root, userDir := t.TempDir(), t.TempDir()
	writeConfig(t, filepath.Join(root, DirName), "review_model = configured\n")

	res, err := Resolve(Options{
		RepoRoot: root,
		UserDir:  userDir,
		Flags:    map[string]string{"review_model": "from-flag"},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got := res.Config.ReviewModel; got != "from-flag" {
		t.Errorf("review_model = %q, want the flag value", got)
	}
	if !res.Config.SetByFlag("review_model") {
		t.Error("review_model is not attributed to the command line")
	}
	if res.Config.SetByFlag("executor") {
		t.Error("executor is attributed to the command line, but no flag set it")
	}
}

func TestResolveMalformedFileFailsWithFileAndLine(t *testing.T) {
	tests := []struct {
		name string
		body string
		line int
		want string
	}{
		{name: "no separator", body: "executor = codex\nthis is not a setting\n", line: 2, want: "expected `key = value`"},
		{name: "unknown setting", body: "\n# comment\nnot_a_setting = 1\n", line: 3, want: `unknown setting "not_a_setting"`},
		{name: "section header", body: "[review]\nexecutor = codex\n", line: 1, want: "section headers are not supported"},
		{name: "empty key", body: " = value\n", line: 1, want: "empty setting name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, userDir := t.TempDir(), t.TempDir()
			path := writeConfig(t, filepath.Join(root, DirName), tt.body)

			res, err := Resolve(Options{RepoRoot: root, UserDir: userDir})
			if err == nil {
				t.Fatalf("resolve succeeded with %v, want a parse error", res.Config)
			}
			var perr *ParseError
			if !errors.As(err, &perr) {
				t.Fatalf("error %v is not a *ParseError", err)
			}
			if perr.File != path {
				t.Errorf("error names %q, want %q", perr.File, path)
			}
			if perr.Line != tt.line {
				t.Errorf("error names line %d, want %d", perr.Line, tt.line)
			}
			if !strings.Contains(perr.Error(), tt.want) {
				t.Errorf("error %q does not mention %q", perr, tt.want)
			}
		})
	}
}

func TestResolveInvalidValueNamesSourceAndAcceptedValues(t *testing.T) {
	root, userDir := t.TempDir(), t.TempDir()
	path := writeConfig(t, filepath.Join(root, DirName), "executor = gemini\n")

	if _, err := Resolve(Options{RepoRoot: root, UserDir: userDir}); err == nil {
		t.Fatal("resolve accepted an unsupported executor")
	} else {
		var verr *ValueError
		if !errors.As(err, &verr) {
			t.Fatalf("error %v is not a *ValueError", err)
		}
		if verr.Key != "executor" || verr.Value != "gemini" {
			t.Errorf("error = %+v, want it to name executor and the rejected value", verr)
		}
		msg := verr.Error()
		for _, want := range []string{path + ":1", "claude", "codex"} {
			if !strings.Contains(msg, want) {
				t.Errorf("error %q does not mention %q", msg, want)
			}
		}
	}
}

func TestResolveInvalidFlagValueBlamesCommandLine(t *testing.T) {
	root, userDir := t.TempDir(), t.TempDir()
	_, err := Resolve(Options{
		RepoRoot: root,
		UserDir:  userDir,
		Flags:    map[string]string{"external_review_tool": "gpt"},
	})
	var verr *ValueError
	if !errors.As(err, &verr) {
		t.Fatalf("error %v is not a *ValueError", err)
	}
	if verr.Src.Layer != LayerFlags {
		t.Errorf("error blames %v, want the command line", verr.Src.Layer)
	}
	if got := Allowed("external_review_tool"); len(got) != 3 {
		t.Errorf("accepted values for external_review_tool = %v, want three", got)
	}
}

func TestResolveValueParsing(t *testing.T) {
	root, userDir := t.TempDir(), t.TempDir()
	writeConfig(t, filepath.Join(root, DirName), strings.Join([]string{
		"finalize = yes",
		"debug = 1",
		"no_color = off",
		"session_timeout = 45m",
		"idle_timeout = 90s",
		"stalemate_patience = 0",
		"validation_command = make test && echo '#done'",
	}, "\n")+"\n")

	res, err := Resolve(Options{RepoRoot: root, UserDir: userDir})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	cfg := res.Config
	if !cfg.Finalize || !cfg.Debug || cfg.NoColor {
		t.Errorf("booleans parsed as finalize=%v debug=%v no_color=%v", cfg.Finalize, cfg.Debug, cfg.NoColor)
	}
	if cfg.SessionTimeout != 45*time.Minute || cfg.IdleTimeout != 90*time.Second {
		t.Errorf("timeouts = %v/%v", cfg.SessionTimeout, cfg.IdleTimeout)
	}
	if cfg.StalematePatience != 0 {
		t.Errorf("stalemate_patience = %d, want 0 to disable it", cfg.StalematePatience)
	}
	// Inline comments are not a thing, so a `#` belongs to the value.
	if want := "make test && echo '#done'"; cfg.ValidationCommand != want {
		t.Errorf("validation_command = %q, want %q", cfg.ValidationCommand, want)
	}
}

func TestResolveRejectsOutOfRangeNumbers(t *testing.T) {
	tests := map[string]string{
		"max_iterations = 0":      "greater than zero",
		"max_iterations = many":   "whole number",
		"stalemate_patience = -1": "zero or a positive number",
		"session_timeout = soon":  "duration",
		"finalize = maybe":        "true or false",
	}
	for body, want := range tests {
		t.Run(body, func(t *testing.T) {
			root, userDir := t.TempDir(), t.TempDir()
			writeConfig(t, filepath.Join(root, DirName), body+"\n")
			_, err := Resolve(Options{RepoRoot: root, UserDir: userDir})
			if err == nil {
				t.Fatalf("resolve accepted %q", body)
			}
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not explain %q", err, want)
			}
		})
	}
}

func TestEveryKeyHasAnEmbeddedDefault(t *testing.T) {
	values, err := defaultValues()
	if err != nil {
		t.Fatalf("parse embedded defaults: %v", err)
	}
	for _, key := range Keys() {
		if _, ok := values[key]; !ok {
			t.Errorf("setting %q has no embedded default", key)
		}
	}
	if len(values) != len(Keys()) {
		t.Errorf("embedded defaults set %d values for %d settings", len(values), len(Keys()))
	}
}

func TestUserDirHonoursXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdg")
	if got, want := UserDir(), filepath.Join("/tmp/xdg", "rrev"); got != want {
		t.Errorf("UserDir() = %q, want %q", got, want)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if got, want := UserDir(), filepath.Join(home, ".config", "rrev"); got != want {
		t.Errorf("UserDir() = %q, want %q", got, want)
	}
}

// The progress directory gets a catch-all ignore rule of its own, so a value
// that resolves to the repository root would ignore the whole repository.
func TestResolveRejectsProgressDirAtTheRoot(t *testing.T) {
	for _, value := range []string{"", ".", "./", "/"} {
		t.Run("value "+value, func(t *testing.T) {
			root, userDir := t.TempDir(), t.TempDir()
			writeConfig(t, filepath.Join(root, DirName), "progress_dir = "+value+"\n")

			if _, err := Resolve(Options{RepoRoot: root, UserDir: userDir}); err == nil {
				t.Fatalf("progress_dir = %q was accepted, want it rejected", value)
			} else if !strings.Contains(err.Error(), "progress_dir") {
				t.Errorf("error %q does not name the setting", err)
			}
		})
	}
}
