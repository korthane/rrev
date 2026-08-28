package main

import (
	"flag"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
)

// flagRef matches a long flag as the README writes it.
var flagRef = regexp.MustCompile(`--[a-z][a-z0-9-]*`)

// TestREADMEDocumentsEveryFlag keeps the CLI and its documentation in step in
// both directions: a flag nobody can discover is as broken as a documented flag
// that does not exist. Fenced blocks are skipped, since a shell example may
// carry another tool's options.
func TestREADMEDocumentsEveryFlag(t *testing.T) {
	body, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatalf("read README: %v", err)
	}

	documented := map[string]bool{}
	fenced := false
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		for _, ref := range flagRef.FindAllString(line, -1) {
			documented[strings.TrimPrefix(ref, "--")] = true
		}
	}

	fs := flag.NewFlagSet("rrev", flag.ContinueOnError)
	registerFlags(fs)
	declared := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) { declared[f.Name] = true })

	for _, name := range sorted(declared) {
		if !documented[name] {
			t.Errorf("--%s is a CLI flag but the README does not document it", name)
		}
	}
	for _, name := range sorted(documented) {
		if !declared[name] {
			t.Errorf("the README documents --%s, which the CLI does not accept", name)
		}
	}
}

func sorted(set map[string]bool) []string {
	names := make([]string, 0, len(set))
	for name := range set {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}
