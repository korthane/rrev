package config

import (
	"slices"
	"strings"
	"testing"
)

// shippedPrompts are the phase prompts rrev embeds, with the agents each one is
// expected to launch. A prompt with no agents is sent to a tool that has none.
var shippedPrompts = map[string][]string{
	"review_first":    {"conformance", "tasks", "quality", "implementation", "testing", "simplification", "documentation"},
	"external_review": nil,
	"external_eval":   nil,
	"review_final":    {"quality", "implementation", "conformance"},
	"finalize":        nil,
}

// signalContract is the sentence every prompt must carry: a missing marker is
// the load-bearing case, and a prompt that omits it turns silence into success.
const signalContract = "Emitting no marker is not success."

func embeddedPrompts(t *testing.T) Assets {
	t.Helper()
	return Assets{ProjectDir: t.TempDir(), UserDir: t.TempDir()}
}

func TestShippedPromptsAreDiscoverable(t *testing.T) {
	assets := embeddedPrompts(t)
	names := assets.PromptNames()

	for name := range shippedPrompts {
		if !slices.Contains(names, name) {
			t.Errorf("prompt %q is not discoverable; got %v", name, names)
			continue
		}
		prompt, err := assets.Prompt(name)
		if err != nil {
			t.Errorf("prompt %q: %v", name, err)
			continue
		}
		if prompt.Layer != LayerDefaults {
			t.Errorf("prompt %q resolved to %v, want the embedded default", name, prompt.Layer)
		}
		if len(strings.TrimSpace(prompt.Content)) < 500 {
			t.Errorf("prompt %q is empty or a stub: %q", name, prompt.Content)
		}
	}
	for _, name := range names {
		if _, ok := shippedPrompts[name]; !ok {
			t.Errorf("prompt %q ships without a test entry; add it to shippedPrompts", name)
		}
	}
}

func TestShippedPromptsExpandForBothExecutors(t *testing.T) {
	assets := embeddedPrompts(t)
	for _, executor := range []string{ExecutorClaude, ExecutorCodex} {
		exp := Expander{Assets: assets, Executor: executor, Vars: fullVars()}
		for _, name := range assets.PromptNames() {
			got, err := exp.Prompt(name)
			if err != nil {
				t.Errorf("%s prompt %q: %v", executor, name, err)
				continue
			}
			if strings.Contains(got, varOpen) {
				t.Errorf("%s prompt %q left an unexpanded directive:\n%s", executor, name, got)
			}
			// finalize reorganizes commits rather than reviewing code, so it is
			// the one prompt with no diff to hand out.
			if name != "finalize" && !strings.Contains(got, "git diff main...HEAD") {
				t.Errorf("%s prompt %q never tells the reviewer how to obtain the diff:\n%s", executor, name, got)
			}
		}
	}
}

func TestShippedPromptsStateTheSignalContract(t *testing.T) {
	assets := embeddedPrompts(t)
	exp := Expander{Assets: assets, Executor: ExecutorClaude, Vars: fullVars()}
	for _, name := range assets.PromptNames() {
		got, err := exp.Prompt(name)
		if err != nil {
			t.Fatalf("prompt %q: %v", name, err)
		}
		if !strings.Contains(got, signalContract) {
			t.Errorf("prompt %q never says a missing marker is not success:\n%s", name, got)
		}
		if !strings.Contains(got, "<<<RREV:TASK_FAILED>>>") {
			t.Errorf("prompt %q does not name the failure marker:\n%s", name, got)
		}
		if !strings.Contains(got, "own line") {
			t.Errorf("prompt %q does not say a marker must stand on its own line:\n%s", name, got)
		}
	}
}

func TestReviewPromptsLaunchTheirAgentsInOneMessage(t *testing.T) {
	assets := embeddedPrompts(t)
	exp := Expander{Assets: assets, Executor: ExecutorClaude, Vars: fullVars()}
	for name, agents := range shippedPrompts {
		got, err := exp.Prompt(name)
		if err != nil {
			t.Fatalf("prompt %q: %v", name, err)
		}
		for _, agent := range assets.AgentNames() {
			marker := "<<<AGENT " + agent + "\n"
			if want := slices.Contains(agents, agent); strings.Contains(got, marker) != want {
				t.Errorf("prompt %q expands agent %q = %v, want %v", name, agent, !want, want)
			}
		}
		if len(agents) > 1 && !strings.Contains(got, "single message") {
			t.Errorf("prompt %q expands %d agents without asking for one message:\n%s", name, len(agents), got)
		}
	}
}

func TestComprehensivePromptVerifiesFixesValidatesAndCommits(t *testing.T) {
	got := expandPrompt(t, "review_first")
	for _, want := range []string{
		"git log main..HEAD --oneline",
		"git diff main...HEAD",
		".rrev/progress/add-user-auth.txt",
		"make test",  // the validation command, expanded rather than described
		"git commit", // fixes are committed inside the phase
		"FINDING:",   // the report shape the run's findings report is built from
		"REJECTED:",  // and the rejections carried to the next iteration
		"<<<RREV:REVIEW_DONE>>>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("review_first prompt missing %q:\n%s", want, got)
		}
	}
}

func TestExternalReviewPromptCarriesChecklistLogAndPriorRounds(t *testing.T) {
	assets := embeddedPrompts(t)
	vars := fullVars()
	vars.PriorFindings = "round 1: REJECTED internal/auth.go:12 - the nil check is in the caller"
	exp := Expander{Assets: assets, Executor: ExecutorCodex, Vars: vars}
	got, err := exp.Prompt("external_review")
	if err != nil {
		t.Fatalf("prompt external_review: %v", err)
	}

	for _, want := range []string{
		"1. [ADDED] auth: Sign in", // the requirement checklist, expanded inline
		".rrev/progress/add-user-auth.txt",
		vars.PriorFindings,
		"git diff main...HEAD",
		"<<<RREV:EXTERNAL_DONE>>>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("external_review prompt missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "Do not edit files") {
		t.Errorf("external_review prompt lets the external tool write to the repository:\n%s", got)
	}
	// A normal run must not hand the independent reviewer the fixing
	// executor's rules: it would contradict the report-only instruction below
	// it and commit work the primary executor never verified.
	if strings.Contains(got, defaultModeRules) {
		t.Errorf("external_review prompt tells the external tool to fix and commit:\n%s", got)
	}
}

func TestExternalReviewPromptSaysWhenNoRoundsPrecedeIt(t *testing.T) {
	got := expandPrompt(t, "external_review")
	if !strings.Contains(got, noPriorFindings) {
		t.Errorf("external_review prompt does not mark the first round:\n%s", got)
	}
}

func TestExternalEvalPromptVerifiesBeforeFixingAndRecordsRejections(t *testing.T) {
	assets := embeddedPrompts(t)
	vars := fullVars()
	vars.ExternalOutput = "FINDING: major | internal/auth.go:12 | external | - | token is never checked"
	exp := Expander{Assets: assets, Executor: ExecutorClaude, Vars: vars}
	got, err := exp.Prompt("external_eval")
	if err != nil {
		t.Fatalf("prompt external_eval: %v", err)
	}

	for _, want := range []string{
		vars.ExternalOutput, // the tool's report is handed over verbatim
		"CONFIRMED", "REJECTED",
		"A rejection with no reason is not a rejection.",
		".rrev/progress/add-user-auth.txt",
		"<<<RREV:EXTERNAL_DONE>>>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("external_eval prompt missing %q:\n%s", want, got)
		}
	}
	if !strings.Contains(got, "Never run the external review tool yourself") {
		t.Errorf("external_eval prompt does not stop the executor re-running the tool:\n%s", got)
	}
}

func TestFinalPromptIsRestrictedToCriticalAndMajor(t *testing.T) {
	got := expandPrompt(t, "review_final")
	for _, want := range []string{
		"critical and major",
		"Discard every minor finding",
		"<<<RREV:REVIEW_DONE>>>",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("review_final prompt missing %q:\n%s", want, got)
		}
	}
}

func TestFinalizePromptIsBestEffortAndRunsOnce(t *testing.T) {
	got := expandPrompt(t, "finalize")
	for _, want := range []string{
		"runs once",
		"best-effort",
		"finalize is enabled in\nconfiguration", // it is inert unless the user asked for it
		"Do not push",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("finalize prompt missing %q:\n%s", want, got)
		}
	}
}

func TestPromptsExpandTheRunModeRules(t *testing.T) {
	assets := embeddedPrompts(t)
	vars := fullVars()
	vars.ModeRules = "Run mode: report only. Do not edit any file and do not commit."
	vars.ReviewerModeRules = vars.ModeRules
	exp := Expander{Assets: assets, Executor: ExecutorClaude, Vars: vars}

	for name := range shippedPrompts {
		got, err := exp.Prompt(name)
		if err != nil {
			t.Fatalf("prompt %q: %v", name, err)
		}
		if !strings.Contains(got, vars.ModeRules) {
			t.Errorf("prompt %q does not expand the run-mode rules, so report-only cannot constrain it:\n%s", name, got)
		}
		for _, fallback := range []string{defaultModeRules, defaultReviewerModeRules} {
			if strings.Contains(got, fallback) {
				t.Errorf("prompt %q kept the default run-mode rules alongside the override:\n%s", name, got)
			}
		}
	}
}

func TestPromptsCarryRalphexAttribution(t *testing.T) {
	assets := embeddedPrompts(t)
	for name := range shippedPrompts {
		prompt, err := assets.Prompt(name)
		if err != nil {
			t.Fatalf("prompt %q: %v", name, err)
		}
		if !strings.Contains(prompt.Content, "ralphex") {
			t.Errorf("prompt %q is adapted from ralphex but carries no attribution", name)
		}
	}
}

func expandPrompt(t *testing.T, name string) string {
	t.Helper()
	exp := Expander{Assets: embeddedPrompts(t), Executor: ExecutorClaude, Vars: fullVars()}
	got, err := exp.Prompt(name)
	if err != nil {
		t.Fatalf("prompt %q: %v", name, err)
	}
	return got
}

// ledgerPrompts are the prompts whose reviewers report findings and so must be
// shown what is already settled. finalize is deliberately absent: it runs after
// review has converged and reports nothing.
var ledgerPrompts = map[string]bool{
	"review_first":    true,
	"external_review": true,
	"external_eval":   true,
	"review_final":    true,
	"finalize":        false,
}

// A prompt that shows the ledger but never says how to name an entry leaves the
// reviewer with the same prose-only escape hatch the ledger exists to replace.
// Which prompts carry it is asserted in both directions: a prompt that silently
// loses {{LEDGER}} goes on re-arguing settled questions with nothing to fail.
func TestPromptsShowingTheLedgerAlsoSayHowToNameAnEntry(t *testing.T) {
	assets := embeddedPrompts(t)
	for _, name := range assets.PromptNames() {
		prompt, err := assets.Prompt(name)
		if err != nil {
			t.Fatalf("prompt %q: %v", name, err)
		}
		want, known := ledgerPrompts[name]
		if !known {
			t.Errorf("prompt %q is not listed in ledgerPrompts, so nothing decides whether it carries the ledger", name)
			continue
		}
		if got := strings.Contains(prompt.Content, "{{LEDGER}}"); got != want {
			t.Errorf("prompt %q expands the ledger = %v, want %v", name, got, want)
		}
		if !want {
			continue
		}
		// The literal token, not the prose around it: a reflow that keeps the
		// paragraph but loses the form the parser reads reverts the feature
		// silently, which is the drift this assertion exists to catch.
		if !strings.Contains(prompt.Content, "`FINDING[R7]:`") {
			t.Errorf("prompt %q expands the ledger but never shows the FINDING[R7]: form that declares a re-raise", name)
		}
	}
}

// The agents declare a re-raise in a `Re-raises:` field, but an agent writes no
// report line: the prompt that embeds it does. A prompt that never mentions the
// field leaves that declaration to be dropped in the merge and the match
// re-derived from prose, which is the inference the declaration replaces.
func TestPromptsEmbeddingAgentsCarryTheReRaisesField(t *testing.T) {
	assets := embeddedPrompts(t)
	var embedding int
	for _, name := range assets.PromptNames() {
		prompt, err := assets.Prompt(name)
		if err != nil {
			t.Fatalf("prompt %q: %v", name, err)
		}
		if !strings.Contains(prompt.Content, "{{AGENTS:") {
			continue
		}
		embedding++
		if !strings.Contains(prompt.Content, "`Re-raises: R7`") {
			t.Errorf("prompt %q embeds agents but never says what to do with the Re-raises: field they emit", name)
		}
	}
	if embedding == 0 {
		t.Fatal("no shipped prompt embeds agents, so nothing consumes a Re-raises: declaration")
	}
}

// The agents are where re-raises originate, so each has to be shown what is
// already settled and told to name it rather than report it afresh.
func TestShippedAgentsAreShownTheStandingRejections(t *testing.T) {
	assets := embeddedPrompts(t)
	names := assets.AgentNames()
	if len(names) == 0 {
		t.Fatal("no shipped agents found")
	}
	for _, name := range names {
		agent, err := assets.Agent(name)
		if err != nil {
			t.Fatalf("agent %q: %v", name, err)
		}
		if !strings.Contains(agent.Content, "{{LEDGER}}") {
			t.Errorf("agent %q is never shown the standing rejections, so it keeps rediscovering them", name)
		}
		// The field, not the prose: an agent writes no report lines itself, so
		// `Re-raises:` is the only form in which its declaration survives the
		// hop to the executor that does.
		if !strings.Contains(agent.Content, "`Re-raises: R7`") {
			t.Errorf("agent %q is not told to name the entry it re-raises as a Re-raises: field", name)
		}
	}
}

// The evaluator must be shown the tool's findings under their ids and told to
// carry them; without both, every disposition opens a second entry.
func TestExternalEvalPromptShowsReportedIdsAndSaysToCarryThem(t *testing.T) {
	prompt, err := embeddedPrompts(t).Prompt("external_eval")
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	for _, want := range []string{"{{EXTERNAL_FINDINGS}}", "Carry that id into your own opening token"} {
		if !strings.Contains(prompt.Content, want) {
			t.Errorf("external_eval is missing %q", want)
		}
	}
	vars := fullVars()
	vars.ExternalFindings = "FINDING[R7]: minor | a.go:1 | external | - | off by one"
	for _, exec := range []string{ExecutorClaude, ExecutorCodex} {
		got, err := (Expander{Assets: embeddedPrompts(t), Executor: exec, Vars: vars}).Prompt("external_eval")
		if err != nil {
			t.Fatalf("expand under %s: %v", exec, err)
		}
		if !strings.Contains(got, "FINDING[R7]:") {
			t.Errorf("under %s the reported id did not reach the prompt", exec)
		}
	}
}
