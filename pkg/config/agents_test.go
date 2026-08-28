package config

import (
	"slices"
	"strings"
	"testing"
)

// ralphexAgents are the agent definitions adapted from ralphex; each must keep
// its attribution header. The other two are rrev's own.
var ralphexAgents = []string{"documentation", "implementation", "quality", "simplification", "testing"}

func embeddedAgents(t *testing.T) Assets {
	t.Helper()
	return Assets{ProjectDir: t.TempDir(), UserDir: t.TempDir()}
}

// fullVars exercises every variable an agent definition may reference.
func fullVars() Vars {
	return Vars{
		Change:            "add-user-auth",
		Goal:              "let users sign in",
		GoalLine:          "add-user-auth: let users sign in",
		BaseRef:           "main",
		DiffInstruction:   "git diff main...HEAD",
		ProgressLog:       ".rrev/progress/add-user-auth.txt",
		ReportFile:        ".rrev/findings.md",
		ValidationCommand: "make test",
		OpenSpecDir:       "openspec",
		ChangeDir:         "openspec/changes/add-user-auth",
		Proposal:          "openspec/changes/add-user-auth/proposal.md",
		Design:            "openspec/changes/add-user-auth/design.md",
		Tasks:             "openspec/changes/add-user-auth/tasks.md",
		Specs:             []string{"openspec/changes/add-user-auth/specs/auth/spec.md"},
		Requirements:      []string{"1. [ADDED] auth: Sign in\n   - Valid password: WHEN ... THEN ...\n"},
		Iteration:         1,
		MaxIterations:     10,
	}
}

func TestShippedAgentsAreDiscoverableAndNonEmpty(t *testing.T) {
	assets := embeddedAgents(t)
	names := assets.AgentNames()

	want := append([]string{"conformance", "tasks"}, ralphexAgents...)
	for _, name := range want {
		if !slices.Contains(names, name) {
			t.Errorf("agent %q is not discoverable; got %v", name, names)
			continue
		}
		agent, err := assets.Agent(name)
		if err != nil {
			t.Errorf("agent %q: %v", name, err)
			continue
		}
		if agent.Layer != LayerDefaults {
			t.Errorf("agent %q resolved to %v, want the embedded default", name, agent.Layer)
		}
		if len(strings.TrimSpace(agent.Content)) < 200 {
			t.Errorf("agent %q is empty or a stub: %q", name, agent.Content)
		}
	}
}

func TestShippedAgentsExpandForBothExecutors(t *testing.T) {
	assets := embeddedAgents(t)
	for _, executor := range []string{ExecutorClaude, ExecutorCodex} {
		exp := Expander{Assets: assets, Executor: executor, Vars: fullVars()}
		for _, name := range assets.AgentNames() {
			agent, err := assets.Agent(name)
			if err != nil {
				t.Fatalf("agent %q: %v", name, err)
			}
			got, err := exp.Expand(agent)
			if err != nil {
				t.Errorf("%s agent %q: %v", executor, name, err)
				continue
			}
			if strings.Contains(got, varOpen) {
				t.Errorf("%s agent %q left an unexpanded directive:\n%s", executor, name, got)
			}
			if !strings.Contains(got, "git diff main...HEAD") {
				t.Errorf("%s agent %q never tells the reviewer how to obtain the diff:\n%s", executor, name, got)
			}
		}
	}
}

func TestRalphexDerivedAgentsCarryAttribution(t *testing.T) {
	assets := embeddedAgents(t)
	for _, name := range assets.AgentNames() {
		agent, err := assets.Agent(name)
		if err != nil {
			t.Fatalf("agent %q: %v", name, err)
		}
		derived := slices.Contains(ralphexAgents, name)
		if got := strings.Contains(agent.Content, "ralphex"); got != derived {
			t.Errorf("agent %q attribution = %v, want %v", name, got, derived)
		}
	}
}

func TestConformanceAgentDemandsCitedVerdicts(t *testing.T) {
	assets := embeddedAgents(t)
	exp := Expander{Assets: assets, Executor: ExecutorClaude, Vars: fullVars()}
	agent, err := assets.Agent("conformance")
	if err != nil {
		t.Fatalf("agent conformance: %v", err)
	}
	got, err := exp.Expand(agent)
	if err != nil {
		t.Fatalf("expand conformance: %v", err)
	}

	for _, want := range []string{
		"SATISFIED", "PARTIAL", "CONTRADICTED", "NOT ADDRESSED",
		"file:line",
		"1. [ADDED] auth: Sign in", // the checklist is expanded into the definition
		"openspec/changes/add-user-auth/specs/auth/spec.md", // and the specs it came from are named
	} {
		if !strings.Contains(got, want) {
			t.Errorf("conformance agent missing %q:\n%s", want, got)
		}
	}
}

func TestTasksAgentCrossChecksTheTaskList(t *testing.T) {
	assets := embeddedAgents(t)
	exp := Expander{Assets: assets, Executor: ExecutorClaude, Vars: fullVars()}
	agent, err := assets.Agent("tasks")
	if err != nil {
		t.Fatalf("agent tasks: %v", err)
	}
	got, err := exp.Expand(agent)
	if err != nil {
		t.Fatalf("expand tasks: %v", err)
	}
	for _, want := range []string{"openspec/changes/add-user-auth/tasks.md", "[x]", "(not present)"} {
		if !strings.Contains(got, want) {
			t.Errorf("tasks agent missing %q:\n%s", want, got)
		}
	}
}
