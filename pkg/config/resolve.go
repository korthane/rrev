package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
)

// DirName is the per-project configuration directory.
const DirName = ".rrev"

// FileName is the configuration file looked for in each directory source.
const FileName = "config.ini"

// Options selects the sources a run resolves its configuration from.
type Options struct {
	// RepoRoot is the repository being reviewed; project configuration lives
	// in its .rrev directory.
	RepoRoot string
	// ProjectDir overrides the project configuration directory. Empty means
	// RepoRoot/.rrev.
	ProjectDir string
	// UserDir overrides the user configuration directory. Empty means
	// $XDG_CONFIG_HOME/rrev, falling back to ~/.config/rrev.
	UserDir string
	// Flags holds only the settings given on the command line, keyed the same
	// way as the configuration file.
	Flags map[string]string
}

// Resolved is a configuration together with the asset lookup that shares its
// sources, so prompts and agents resolve over the same three directories.
type Resolved struct {
	Config *Config
	Assets Assets
}

// Resolve merges flags, project configuration, user configuration, and the
// embedded defaults, in that order of precedence. A source that omits a
// setting leaves the next source's value alone; it never contributes a zero.
func Resolve(opts Options) (*Resolved, error) {
	projectDir := opts.ProjectDir
	if projectDir == "" && opts.RepoRoot != "" {
		projectDir = filepath.Join(opts.RepoRoot, DirName)
	}
	userDir := opts.UserDir
	if userDir == "" {
		userDir = UserDir()
	}

	merged, err := defaultValues()
	if err != nil {
		return nil, err
	}
	for _, l := range []struct {
		layer Layer
		dir   string
	}{{LayerUser, userDir}, {LayerProject, projectDir}} {
		if l.dir == "" {
			continue
		}
		values, err := loadFile(l.layer, filepath.Join(l.dir, FileName))
		if err != nil {
			return nil, err
		}
		maps.Copy(merged, values)
	}
	maps.Copy(merged, flagValues(opts.Flags))

	cfg, err := decode(merged)
	if err != nil {
		return nil, err
	}
	return &Resolved{
		Config: cfg,
		Assets: Assets{ProjectDir: projectDir, UserDir: userDir},
	}, nil
}

// UserDir is the user-level configuration directory, `~/.config/rrev` unless
// XDG_CONFIG_HOME says otherwise.
func UserDir() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "rrev")
}

// resolvedValue is a raw setting value tagged with where it came from.
type resolvedValue struct {
	raw string
	src Source
}

func defaultValues() (map[string]resolvedValue, error) {
	data, err := defaultsFS.ReadFile(defaultConfigPath)
	if err != nil {
		return nil, fmt.Errorf("read embedded defaults: %w", err)
	}
	entries, err := parseINI(defaultConfigPath, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	values := make(map[string]resolvedValue, len(entries))
	for key, e := range entries {
		values[key] = resolvedValue{raw: e.raw, src: Source{Layer: LayerDefaults, File: defaultConfigPath, Line: e.line}}
	}
	return values, nil
}

func loadFile(layer Layer, path string) (map[string]resolvedValue, error) {
	f, err := os.Open(path) //nolint:gosec // the path is the user's own configuration file
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read %s configuration: %w", layer, err)
	}
	defer func() { _ = f.Close() }()

	entries, err := parseINI(path, f)
	if err != nil {
		return nil, err
	}
	values := make(map[string]resolvedValue, len(entries))
	for key, e := range entries {
		values[key] = resolvedValue{raw: e.raw, src: Source{Layer: layer, File: path, Line: e.line}}
	}
	return values, nil
}

func flagValues(flags map[string]string) map[string]resolvedValue {
	values := make(map[string]resolvedValue, len(flags))
	for key, raw := range flags {
		values[key] = resolvedValue{raw: raw, src: Source{Layer: LayerFlags}}
	}
	return values
}

func decode(values map[string]resolvedValue) (*Config, error) {
	cfg := &Config{origins: make(map[string]Source, len(values))}
	for _, f := range fields {
		v, ok := values[f.key]
		if !ok {
			return nil, fmt.Errorf("setting %q has no value in any source, not even the embedded defaults", f.key)
		}
		if err := f.set(cfg, v.raw); err != nil {
			return nil, &ValueError{Key: f.key, Value: v.raw, Src: v.src, Msg: err.Error()}
		}
		cfg.origins[f.key] = v.src
	}
	for key := range values {
		if _, known := fieldByKey[key]; !known {
			return nil, fmt.Errorf("unknown setting %q", key)
		}
	}
	return cfg, nil
}
