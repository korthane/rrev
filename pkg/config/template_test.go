package config

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// expanderFor builds an expander over a temporary project directory, so tests
// can supply their own prompts and agents without touching the defaults.
func expanderFor(t *testing.T, executor string, vars Vars) (Expander, string) {
	t.Helper()
	projectDir := filepath.Join(t.TempDir(), DirName)
	return Expander{
		Assets:   Assets{ProjectDir: projectDir, UserDir: t.TempDir()},
		Executor: executor,
		Vars:     vars,
	}, projectDir
}

func TestExpandSubstitutesVariables(t *testing.T) {
	vars := Vars{
		Change:      "add-user-auth",
		Goal:        "let users sign in",
		BaseRef:     "origin/main",
		ProgressLog: ".rrev/progress/add-user-auth.md",
		Proposal:    "changes/add-user-auth/proposal.md",
		Specs:       []string{"changes/add-user-auth/specs/auth/spec.md"},
	}
	exp, projectDir := expanderFor(t, ExecutorClaude, vars)
	writeAsset(t, projectDir, KindPrompt, "review_first",
		"Review {{CHANGE}} ({{GOAL}}) against {{BASE_REF}}.\nLog: {{PROGRESS_LOG}}\nSpecs:\n{{SPECS}}\nDesign: {{DESIGN}}\n")

	got, err := exp.Prompt("review_first")
	if err != nil {
		t.Fatalf("expand review_first: %v", err)
	}
	want := "Review add-user-auth (let users sign in) against origin/main.\n" +
		"Log: .rrev/progress/add-user-auth.md\n" +
		"Specs:\n- changes/add-user-auth/specs/auth/spec.md\n" +
		"Design: " + missingPath + "\n"
	if got != want {
		t.Errorf("expanded prompt =\n%q\nwant\n%q", got, want)
	}
}

func TestExpandUnknownVariableNamesFileAndVariable(t *testing.T) {
	exp, projectDir := expanderFor(t, ExecutorClaude, Vars{Change: "c"})
	path := writeAsset(t, projectDir, KindPrompt, "review_first", "ok {{CHANGE}} bad {{NOPE}}\n")

	_, err := exp.Prompt("review_first")
	var tmplErr *TemplateError
	if !errors.As(err, &tmplErr) {
		t.Fatalf("error = %v, want a *TemplateError", err)
	}
	if tmplErr.File != path {
		t.Errorf("error file = %q, want %q", tmplErr.File, path)
	}
	if !strings.Contains(err.Error(), "{{NOPE}}") || !strings.Contains(err.Error(), path) {
		t.Errorf("error %q does not name both the file and the variable", err)
	}
}

func TestExpandRejectsUnterminatedDirective(t *testing.T) {
	exp, projectDir := expanderFor(t, ExecutorClaude, Vars{})
	writeAsset(t, projectDir, KindPrompt, "review_first", "start {{CHANGE\n")

	if _, err := exp.Prompt("review_first"); err == nil {
		t.Fatal("expanded an unterminated directive without an error")
	}
}

func TestChecklistTruncationStatesThatItTruncated(t *testing.T) {
	entries := []string{"1. first requirement\n", "2. second requirement\n", "3. third requirement\n"}

	full := renderChecklist(entries, 0, nil)
	if strings.Contains(full, "TRUNCATED") {
		t.Errorf("unbudgeted checklist was truncated: %q", full)
	}
	if fits := renderChecklist(entries, 1000, nil); fits != full {
		t.Errorf("checklist under budget = %q, want the full checklist", fits)
	}

	got := renderChecklist(entries, len(entries[0])+len(entries[1]), nil)
	if !strings.Contains(got, "TRUNCATED") || !strings.Contains(got, "2 of 3 requirements") {
		t.Errorf("truncated checklist does not say so: %q", got)
	}
	if !strings.Contains(got, "second requirement") || strings.Contains(got, "third requirement") {
		t.Errorf("truncation did not drop exactly the last requirement: %q", got)
	}
}

func TestChecklistTruncationKeepsWholeRequirements(t *testing.T) {
	entries := []string{"1. a requirement far longer than the budget\n", "2. another\n"}

	got := renderChecklist(entries, 5, nil)
	if !strings.Contains(got, "1. a requirement far longer than the budget") {
		t.Errorf("a requirement was cut in half or dropped entirely: %q", got)
	}
	if !strings.Contains(got, "1 of 2 requirements") {
		t.Errorf("truncation note = %q, want it to report 1 of 2 shown", got)
	}
}

func TestChecklistEmpty(t *testing.T) {
	if got := renderChecklist(nil, 100, nil); got != noRequirements {
		t.Errorf("empty checklist = %q, want %q", got, noRequirements)
	}
}

func TestChecklistNamesUnparsedSpecs(t *testing.T) {
	entries := []string{"1. first requirement\n"}
	unparsed := []string{"openspec/changes/c/specs/auth/spec.md"}

	got := renderChecklist(entries, 0, unparsed)
	if !strings.Contains(got, "INCOMPLETE") || !strings.Contains(got, unparsed[0]) {
		t.Errorf("checklist does not name the spec it could not parse: %q", got)
	}

	empty := renderChecklist(nil, 0, unparsed)
	if !strings.Contains(empty, unparsed[0]) {
		t.Errorf("empty checklist does not name the spec it could not parse: %q", empty)
	}
}

func TestExpandAgentsPerExecutor(t *testing.T) {
	for _, tc := range []struct {
		executor string
		wants    []string
		// notWants are the instructions this executor must not carry. The
		// naming clause is claude-only: codex's format offers nothing to read
		// a name back out of, so asking for one there is an instruction the
		// tool cannot act on.
		notWants []string
	}{
		// The naming instruction is load-bearing, not decoration: rrev reads
		// the agent's name back out of the call's description, and a launch
		// that omits it renders every concurrent reviewer alike.
		{ExecutorClaude, []string{"Task tool", "subagent prompt", "as the call's `description`"}, nil},
		{ExecutorCodex, []string{"Spawn", "codex sub-agent"}, []string{"as the call's `description`"}},
	} {
		t.Run(tc.executor, func(t *testing.T) {
			exp, projectDir := expanderFor(t, tc.executor, Vars{Change: "add-user-auth"})
			writeAsset(t, projectDir, KindAgent, "conformance", "Check {{CHANGE}} against every scenario.")
			writeAsset(t, projectDir, KindAgent, "quality", "Judge the quality of the diff.")
			writeAsset(t, projectDir, KindPrompt, "review_first", "Phase 1.\n{{AGENTS: conformance, quality}}\n")

			got, err := exp.Prompt("review_first")
			if err != nil {
				t.Fatalf("expand review_first: %v", err)
			}
			for _, want := range append(tc.wants,
				"single message", "concurrently",
				"<<<AGENT conformance", "<<<AGENT quality",
				"Check add-user-auth against every scenario.", "Judge the quality of the diff.") {
				if !strings.Contains(got, want) {
					t.Errorf("expansion missing %q:\n%s", want, got)
				}
			}
			for _, unwanted := range tc.notWants {
				if strings.Contains(got, unwanted) {
					t.Errorf("expansion carries %q, which %s cannot act on:\n%s", unwanted, tc.executor, got)
				}
			}
		})
	}
}

func TestExpandSingleAgentOmitsParallelInstruction(t *testing.T) {
	exp, projectDir := expanderFor(t, ExecutorClaude, Vars{})
	writeAsset(t, projectDir, KindAgent, "quality", "Judge quality.")
	writeAsset(t, projectDir, KindPrompt, "review_final", "{{AGENT:quality}}")

	got, err := exp.Prompt("review_final")
	if err != nil {
		t.Fatalf("expand review_final: %v", err)
	}
	if strings.Contains(got, "concurrently") {
		t.Errorf("single-agent expansion talks about concurrency:\n%s", got)
	}
	if !strings.Contains(got, "<<<AGENT quality\nJudge quality.\nAGENT>>>") {
		t.Errorf("single-agent expansion missing the definition:\n%s", got)
	}
}

func TestExpandUnresolvableAgentNamesPromptAndAgent(t *testing.T) {
	exp, projectDir := expanderFor(t, ExecutorClaude, Vars{})
	path := writeAsset(t, projectDir, KindPrompt, "review_first", "{{AGENTS:quality,perf}}")

	_, err := exp.Prompt("review_first")
	if !errors.Is(err, ErrAssetNotFound) {
		t.Fatalf("error = %v, want it to wrap ErrAssetNotFound", err)
	}
	if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), `"perf"`) {
		t.Errorf("error %q does not name both the prompt file and the unresolved agent", err)
	}
}

func TestExpandRejectsAgentReferenceInsideAgent(t *testing.T) {
	exp, projectDir := expanderFor(t, ExecutorClaude, Vars{})
	writeAsset(t, projectDir, KindAgent, "quality", "{{AGENTS:testing}}")
	writeAsset(t, projectDir, KindAgent, "testing", "Judge tests.")
	writeAsset(t, projectDir, KindPrompt, "review_first", "{{AGENTS:quality}}")

	_, err := exp.Prompt("review_first")
	if err == nil || !strings.Contains(err.Error(), "may not reference other agents") {
		t.Fatalf("error = %v, want a rejection of the nested agent reference", err)
	}
}

func TestExpandRejectsEmptyAndUnknownDirectives(t *testing.T) {
	for name, prompt := range map[string]string{
		"empty agent list":  "{{AGENTS: , }}",
		"unknown directive": "{{INCLUDE:other.txt}}",
	} {
		t.Run(name, func(t *testing.T) {
			exp, projectDir := expanderFor(t, ExecutorClaude, Vars{})
			writeAsset(t, projectDir, KindPrompt, "review_first", prompt)
			if _, err := exp.Prompt("review_first"); err == nil {
				t.Fatalf("expanded %q without an error", prompt)
			}
		})
	}
}
