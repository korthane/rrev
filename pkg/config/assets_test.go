package config

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// writeAsset puts an override file in one of the layered asset directories.
func writeAsset(t *testing.T, dir, kind, name, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, kind), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, kind, name+assetExt)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAssetsFallBackToEmbeddedDefaults(t *testing.T) {
	assets := Assets{ProjectDir: filepath.Join(t.TempDir(), DirName), UserDir: t.TempDir()}

	for _, name := range assets.PromptNames() {
		got, err := assets.Prompt(name)
		if err != nil {
			t.Fatalf("prompt %q: %v", name, err)
		}
		if got.Layer != LayerDefaults || got.Content == "" {
			t.Errorf("prompt %q resolved to %v with %d bytes, want non-empty embedded default",
				name, got.Layer, len(got.Content))
		}
	}
	if len(assets.PromptNames()) == 0 || len(assets.AgentNames()) == 0 {
		t.Fatal("no embedded prompts or agents are discoverable")
	}
}

func TestAssetsSinglePromptOverrideLeavesTheRestOnDefaults(t *testing.T) {
	projectDir := filepath.Join(t.TempDir(), DirName)
	assets := Assets{ProjectDir: projectDir, UserDir: t.TempDir()}
	path := writeAsset(t, projectDir, KindPrompt, "review_first", "project review prompt")

	got, err := assets.Prompt("review_first")
	if err != nil {
		t.Fatalf("prompt review_first: %v", err)
	}
	if got.Layer != LayerProject || got.Path != path || got.Content != "project review prompt" {
		t.Errorf("overridden prompt = %+v, want the project file", got)
	}

	for _, name := range assets.PromptNames() {
		if name == "review_first" {
			continue
		}
		other, err := assets.Prompt(name)
		if err != nil {
			t.Fatalf("prompt %q: %v", name, err)
		}
		if other.Layer != LayerDefaults {
			t.Errorf("prompt %q resolved to %v, want the embedded default", name, other.Layer)
		}
	}
}

func TestAssetsProjectOverridesUserOverridesDefaults(t *testing.T) {
	projectDir, userDir := filepath.Join(t.TempDir(), DirName), t.TempDir()
	assets := Assets{ProjectDir: projectDir, UserDir: userDir}

	writeAsset(t, userDir, KindAgent, "quality", "user quality agent")
	got, err := assets.Agent("quality")
	if err != nil {
		t.Fatalf("agent quality: %v", err)
	}
	if got.Layer != LayerUser || got.Content != "user quality agent" {
		t.Errorf("agent quality = %+v, want the user file", got)
	}

	writeAsset(t, projectDir, KindAgent, "quality", "project quality agent")
	got, err = assets.Agent("quality")
	if err != nil {
		t.Fatalf("agent quality: %v", err)
	}
	if got.Layer != LayerProject || got.Content != "project quality agent" {
		t.Errorf("agent quality = %+v, want the project file", got)
	}
}

func TestAssetsCustomAgentIsDiscoverable(t *testing.T) {
	projectDir := filepath.Join(t.TempDir(), DirName)
	assets := Assets{ProjectDir: projectDir, UserDir: t.TempDir()}
	writeAsset(t, projectDir, KindAgent, "perf", "look for hot loops")

	got, err := assets.Agent("perf")
	if err != nil {
		t.Fatalf("agent perf: %v", err)
	}
	if got.Content != "look for hot loops" {
		t.Errorf("agent perf content = %q", got.Content)
	}
	names := assets.AgentNames()
	if !slices.Contains(names, "perf") {
		t.Errorf("agent names %v do not include the project's own agent", names)
	}
	if !slices.Contains(names, "conformance") {
		t.Errorf("agent names %v dropped the embedded defaults", names)
	}
	if !slices.IsSorted(names) {
		t.Errorf("agent names %v are not sorted", names)
	}
}

func TestAssetsMissingAgentIsReportedByName(t *testing.T) {
	assets := Assets{ProjectDir: filepath.Join(t.TempDir(), DirName), UserDir: t.TempDir()}

	_, err := assets.Agent("nonexistent")
	if !errors.Is(err, ErrAssetNotFound) {
		t.Fatalf("error = %v, want ErrAssetNotFound", err)
	}
	if got := err.Error(); !strings.Contains(got, "nonexistent") {
		t.Errorf("error %q does not name the agent", got)
	}
}

func TestAssetsRejectPathEscapes(t *testing.T) {
	assets := Assets{ProjectDir: t.TempDir(), UserDir: t.TempDir()}
	for _, name := range []string{"", "..", "../config", "sub/agent"} {
		if _, err := assets.Agent(name); err == nil {
			t.Errorf("agent %q was accepted", name)
		}
	}
}

func TestResolveSharesAssetDirectories(t *testing.T) {
	root, userDir := t.TempDir(), t.TempDir()
	writeAsset(t, filepath.Join(root, DirName), KindPrompt, "finalize", "project finalize")

	res, err := Resolve(Options{RepoRoot: root, UserDir: userDir})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	got, err := res.Assets.Prompt("finalize")
	if err != nil {
		t.Fatalf("prompt finalize: %v", err)
	}
	if got.Layer != LayerProject {
		t.Errorf("prompt finalize resolved to %v, want the project directory Resolve derived", got.Layer)
	}
}
