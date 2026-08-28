package config

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

//go:embed defaults/config.ini defaults/prompts/*.txt defaults/agents/*.txt
var defaultsFS embed.FS

const (
	defaultsDir       = "defaults"
	defaultConfigPath = defaultsDir + "/" + FileName
)

// Asset kinds, which are also the directory names each kind lives in.
const (
	KindPrompt = "prompts"
	KindAgent  = "agents"
)

const assetExt = ".txt"

// ErrAssetNotFound reports a prompt or agent that resolves to no file in any
// source, so a prompt referencing it can be rejected by name.
var ErrAssetNotFound = errors.New("not found in the project, the user directory, or the embedded defaults")

// Asset is a prompt or agent definition resolved from the highest-precedence
// source that provides it.
type Asset struct {
	Kind    string
	Name    string
	Layer   Layer
	Path    string
	Content string
}

// Assets resolves prompts and agents per file across the project directory,
// the user directory, and the embedded defaults, so overriding one file leaves
// every other one on its default.
type Assets struct {
	ProjectDir string
	UserDir    string
}

// Prompt resolves a phase prompt by name, without its extension.
func (a Assets) Prompt(name string) (Asset, error) { return a.lookup(KindPrompt, name) }

// Agent resolves a reviewer agent definition by name, without its extension.
func (a Assets) Agent(name string) (Asset, error) { return a.lookup(KindAgent, name) }

// PromptNames lists every prompt available from any source, sorted.
func (a Assets) PromptNames() []string { return a.names(KindPrompt) }

// AgentNames lists every agent available from any source, sorted.
func (a Assets) AgentNames() []string { return a.names(KindAgent) }

func (a Assets) lookup(kind, name string) (Asset, error) {
	if name == "" || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return Asset{}, fmt.Errorf("invalid %s name %q", assetNoun(kind), name)
	}
	for _, dir := range a.dirs() {
		path := filepath.Join(dir.path, kind, name+assetExt)
		content, err := os.ReadFile(path) //nolint:gosec // the path is the user's own override directory
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return Asset{}, fmt.Errorf("read %s %q: %w", assetNoun(kind), name, err)
		}
		return Asset{Kind: kind, Name: name, Layer: dir.layer, Path: path, Content: string(content)}, nil
	}

	path := defaultsDir + "/" + kind + "/" + name + assetExt
	content, err := defaultsFS.ReadFile(path)
	if err != nil {
		return Asset{}, fmt.Errorf("%s %q: %w", assetNoun(kind), name, ErrAssetNotFound)
	}
	return Asset{Kind: kind, Name: name, Layer: LayerDefaults, Path: path, Content: string(content)}, nil
}

func (a Assets) names(kind string) []string {
	seen := map[string]bool{}
	for _, dir := range a.dirs() {
		addNames(seen, os.DirFS(filepath.Join(dir.path, kind)), ".")
	}
	addNames(seen, defaultsFS, defaultsDir+"/"+kind)
	return slices.Sorted(maps.Keys(seen))
}

type assetDir struct {
	layer Layer
	path  string
}

func (a Assets) dirs() []assetDir {
	var dirs []assetDir
	if a.ProjectDir != "" {
		dirs = append(dirs, assetDir{LayerProject, a.ProjectDir})
	}
	if a.UserDir != "" {
		dirs = append(dirs, assetDir{LayerUser, a.UserDir})
	}
	return dirs
}

func addNames(seen map[string]bool, fsys fs.FS, dir string) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), assetExt); ok && !e.IsDir() {
			seen[name] = true
		}
	}
}

func assetNoun(kind string) string {
	if kind == KindAgent {
		return "agent"
	}
	return "prompt"
}
