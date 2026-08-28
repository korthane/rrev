package main

import (
	"errors"
	"flag"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/korthane/rrev/pkg/config"
	"github.com/korthane/rrev/pkg/processor"
)

func TestParseArgsChangeAndOverrides(t *testing.T) {
	opts, err := parseArgs([]string{"add-user-auth", "--review-model", "opus:high", "--finalize", "--max-iterations", "3"}, io.Discard)
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if opts.Change != "add-user-auth" {
		t.Errorf("Change = %q", opts.Change)
	}
	if opts.Mode != processor.ModeFull {
		t.Errorf("Mode = %q, want the default full pipeline", opts.Mode)
	}
	want := map[string]string{"review_model": "opus:high", "finalize": "true", "max_iterations": "3"}
	if !maps.Equal(opts.Flags, want) {
		t.Errorf("Flags = %v, want %v", opts.Flags, want)
	}
}

// TestParseArgsFlagsAfterChangeName guards the interspersed-argument handling:
// Go's flag package stops at the first positional, which would drop every flag
// typed after the change name.
func TestParseArgsFlagsAfterChangeName(t *testing.T) {
	opts, err := parseArgs([]string{"--debug", "add-user-auth", "--base-ref", "develop"}, io.Discard)
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if opts.Change != "add-user-auth" {
		t.Errorf("Change = %q", opts.Change)
	}
	if opts.Flags["base_ref"] != "develop" || opts.Flags["debug"] != "true" {
		t.Errorf("Flags = %v", opts.Flags)
	}
}

// TestParseArgsUnsetFlagsAbsent is what keeps a flag from zeroing a configured
// value: only what the user typed is handed to the config layer.
func TestParseArgsUnsetFlagsAbsent(t *testing.T) {
	opts, err := parseArgs([]string{"--executor", "codex"}, io.Discard)
	if err != nil {
		t.Fatalf("parseArgs: %v", err)
	}
	if len(opts.Flags) != 1 || opts.Flags["executor"] != "codex" {
		t.Errorf("Flags = %v, want only the executor override", opts.Flags)
	}
}

func TestParseArgsModes(t *testing.T) {
	for _, m := range modeFlags {
		opts, err := parseArgs([]string{"--" + m.name}, io.Discard)
		if err != nil {
			t.Fatalf("parseArgs --%s: %v", m.name, err)
		}
		if opts.Mode != m.mode {
			t.Errorf("--%s selected mode %q, want %q", m.name, opts.Mode, m.mode)
		}
	}
}

func TestParseArgsConflictingModes(t *testing.T) {
	_, err := parseArgs([]string{"--external-only", "--phase1-only"}, io.Discard)
	var conflict *modeConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("parseArgs: %v, want a mode conflict", err)
	}
	for _, want := range []string{"--external-only", "--phase1-only"} {
		if !strings.Contains(conflict.Error(), want) {
			t.Errorf("error %q, want it to name %q", conflict, want)
		}
	}
}

func TestParseArgsRejectsSecondChangeName(t *testing.T) {
	_, err := parseArgs([]string{"one", "two"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "one") || !strings.Contains(err.Error(), "two") {
		t.Fatalf("parseArgs: %v, want an error naming both arguments", err)
	}
}

func TestParseArgsUnknownFlag(t *testing.T) {
	if _, err := parseArgs([]string{"--nope"}, io.Discard); err == nil {
		t.Fatal("parseArgs: want an error for an unknown flag")
	}
}

func TestParseArgsHelp(t *testing.T) {
	var out strings.Builder
	_, err := parseArgs([]string{"-h"}, &out)
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("parseArgs: %v, want flag.ErrHelp", err)
	}
	for _, want := range []string{"usage: rrev", "-report-only", "-review-model"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("usage %q, want it to mention %q", out.String(), want)
		}
	}
}

// TestEverySettingIsOverridable is the per-run override requirement stated as a
// test: a setting rrev reads from configuration must have a documented flag.
func TestEverySettingIsOverridable(t *testing.T) {
	keys := config.Keys()
	for _, key := range keys {
		if flagUsage[key] == "" {
			t.Errorf("setting %q has no flag documentation", key)
		}
	}
	for key := range flagUsage {
		if !slices.Contains(keys, key) {
			t.Errorf("flag %q documents no configuration setting", flagName(key))
		}
	}
}

// TestBoolKeysMatchConfig catches a setting that changes shape: a boolean flag
// registered as a string would need `--finalize=true`, and a string registered
// as a boolean would swallow its value.
func TestBoolKeysMatchConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, key := range config.Keys() {
		// Only a boolean setting accepts "true" and rejects "maybe"; a string
		// setting accepts both and every other kind rejects both.
		isBool := accepts(t, key, "true") && !accepts(t, key, "maybe")
		if isBool != boolKeys[key] {
			t.Errorf("setting %q: config treats it as bool=%v, boolKeys says %v", key, isBool, boolKeys[key])
		}
	}
}

func accepts(t *testing.T, key, value string) bool {
	t.Helper()
	_, err := config.Resolve(config.Options{UserDir: t.TempDir(), Flags: map[string]string{key: value}})
	return err == nil
}
