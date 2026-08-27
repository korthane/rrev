package openspec_test

import (
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/korthane/rrev/pkg/openspec"
)

func resolveFixture(t *testing.T, cli openspec.CLI) openspec.Context {
	t.Helper()
	root := fixtureRoot(t)
	disc := openspec.DiscoverChanges(cli, root)
	change, err := openspec.SelectChange(root, "add-thing", disc)
	if err != nil {
		t.Fatal(err)
	}
	rc, err := openspec.Resolve(cli, root, change, disc)
	if err != nil {
		t.Fatal(err)
	}
	return rc
}

func TestResolveViaCLI(t *testing.T) {
	rc := resolveFixture(t, stubCLI(t))
	if rc.Degraded {
		t.Errorf("degraded = true, want false: %v", rc.Notes)
	}
	if len(rc.Requirements) != 5 {
		t.Fatalf("extracted %d requirements, want 5", len(rc.Requirements))
	}
	if rc.ScenarioCount() != 5 {
		t.Errorf("extracted %d scenarios, want 5", rc.ScenarioCount())
	}
	// The CLI reports requirement bodies without their headings; rrev recovers
	// the titles from the parsed markdown.
	if rc.Requirements[0].Name != "TOTP enrollment" {
		t.Errorf("name = %q, want the heading recovered from the delta spec", rc.Requirements[0].Name)
	}
	if rc.Requirements[0].Scenarios[0].Name != "Enrollment succeeds" {
		t.Errorf("scenario name = %q, want the heading recovered from the delta spec",
			rc.Requirements[0].Scenarios[0].Name)
	}
	if rc.Requirements[2].Operation != openspec.OperationModified {
		t.Errorf("operation = %s, want MODIFIED", rc.Requirements[2].Operation)
	}
	if !strings.Contains(rc.Checklist(), "[REMOVED] auth:") {
		t.Errorf("checklist does not label the removed requirement:\n%s", rc.Checklist())
	}
}

func TestResolveFallsBackToParser(t *testing.T) {
	rc := resolveFixture(t, openspec.CLI{Disabled: true})
	if !rc.Degraded {
		t.Error("degraded = false, want true when the CLI is unavailable")
	}
	if len(rc.Requirements) != 5 || rc.ScenarioCount() != 5 {
		t.Fatalf("parser extracted %d requirements / %d scenarios, want 5 / 5",
			len(rc.Requirements), rc.ScenarioCount())
	}
}

// TestExtractionPathsAgree guards the parser against silently under-extracting:
// it is the path that runs when the CLI is absent, so a drift in requirement or
// scenario counts between the two paths is a bug in the parser.
func TestExtractionPathsAgree(t *testing.T) {
	viaCLI := resolveFixture(t, stubCLI(t))
	viaParser := resolveFixture(t, openspec.CLI{Disabled: true})

	if len(viaCLI.Requirements) != len(viaParser.Requirements) {
		t.Fatalf("requirement counts differ: CLI %d, parser %d",
			len(viaCLI.Requirements), len(viaParser.Requirements))
	}
	if viaCLI.ScenarioCount() != viaParser.ScenarioCount() {
		t.Fatalf("scenario counts differ: CLI %d, parser %d",
			viaCLI.ScenarioCount(), viaParser.ScenarioCount())
	}
	for i := range viaCLI.Requirements {
		cliReq, parsedReq := viaCLI.Requirements[i], viaParser.Requirements[i]
		if cliReq.Capability != parsedReq.Capability || cliReq.Operation != parsedReq.Operation {
			t.Errorf("requirement %d: CLI %s/%s, parser %s/%s", i,
				cliReq.Capability, cliReq.Operation, parsedReq.Capability, parsedReq.Operation)
		}
		if len(cliReq.Scenarios) != len(parsedReq.Scenarios) {
			t.Errorf("requirement %q: CLI %d scenarios, parser %d",
				cliReq.Title(), len(cliReq.Scenarios), len(parsedReq.Scenarios))
		}
	}
}

// TestExtractionPathsAgreeWithRealCLI repeats the cross-check against the
// installed openspec CLI, catching drift in its JSON output that the recorded
// fixture cannot.
func TestExtractionPathsAgreeWithRealCLI(t *testing.T) {
	if _, err := exec.LookPath(openspec.DefaultCLIBin); err != nil {
		t.Skipf("openspec CLI not installed: %v", err)
	}
	viaCLI := resolveFixture(t, openspec.CLI{})
	viaParser := resolveFixture(t, openspec.CLI{Disabled: true})
	if viaCLI.Degraded {
		t.Fatalf("degraded = true with the CLI installed: %v", viaCLI.Notes)
	}
	if len(viaCLI.Requirements) != len(viaParser.Requirements) ||
		viaCLI.ScenarioCount() != viaParser.ScenarioCount() {
		t.Errorf("CLI %d requirements / %d scenarios, parser %d / %d",
			len(viaCLI.Requirements), viaCLI.ScenarioCount(),
			len(viaParser.Requirements), viaParser.ScenarioCount())
	}
}

func TestResolveGoalFromProposal(t *testing.T) {
	rc := resolveFixture(t, stubCLI(t))
	if !strings.Contains(rc.Goal, "second factor") {
		t.Errorf("goal = %q, want a summary of the proposal's Why section", rc.Goal)
	}
	if strings.Contains(rc.Goal, "\n") {
		t.Errorf("goal = %q, want a single line", rc.Goal)
	}
	if !strings.HasPrefix(rc.GoalLine(), "add-thing: ") {
		t.Errorf("goal line = %q, want the change name alongside the goal", rc.GoalLine())
	}
}

func TestResolveGoalFallsBackToChangeName(t *testing.T) {
	root := openspec.Root{Dir: t.TempDir()}
	change := writeChange(t, root, "add-bare", map[string]string{"tasks.md": "- [ ] 1.1 Do it\n"})
	rc, err := openspec.Resolve(openspec.CLI{Disabled: true}, root, change, openspec.Discovery{})
	if err != nil {
		t.Fatal(err)
	}
	if rc.Goal != "add-bare" {
		t.Errorf("goal = %q, want the change name", rc.Goal)
	}
	if rc.GoalLine() != "add-bare" {
		t.Errorf("goal line = %q, want just the change name", rc.GoalLine())
	}
	if !slices.ContainsFunc(rc.Notes, func(n string) bool { return strings.Contains(n, "no proposal") }) {
		t.Errorf("notes = %v, want the missing proposal reported", rc.Notes)
	}
}

func TestResolveKeepsUnparseableSpecRawText(t *testing.T) {
	root := openspec.Root{Dir: t.TempDir()}
	raw := "## Purpose\n\nProse that declares no requirements.\n"
	change := writeChange(t, root, "add-loose", map[string]string{
		"proposal.md":         "## Why\n\nBecause.\n",
		"specs/auth/spec.md":  raw,
		"specs/other/spec.md": "## ADDED Requirements\n\n### Requirement: Real\nSHALL work.\n",
	})
	rc, err := openspec.Resolve(openspec.CLI{Disabled: true}, root, change, openspec.Discovery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rc.Requirements) != 1 {
		t.Fatalf("extracted %d requirements, want the parseable one to survive", len(rc.Requirements))
	}
	if !slices.ContainsFunc(rc.Notes, func(n string) bool { return strings.Contains(n, "specs/auth/spec.md") }) {
		t.Errorf("notes = %v, want the unparseable file reported", rc.Notes)
	}
	if !slices.ContainsFunc(rc.Artifacts.Specs, func(a openspec.Artifact) bool { return a.Content == raw }) {
		t.Error("review context lost the unparseable spec's raw text")
	}
}

// TestContextIsResolvedOnce pins the context's immutability: it is a value, so a
// phase cannot pick up an artifact edited on disk mid-run.
func TestContextIsResolvedOnce(t *testing.T) {
	rc := resolveFixture(t, stubCLI(t))
	snapshot := rc

	perPhase := func(c openspec.Context) openspec.Context {
		c.Goal = "mutated"
		c.Change.Name = "mutated"
		return c
	}
	perPhase(rc)

	if !reflect.DeepEqual(rc.Change, snapshot.Change) || rc.Goal != snapshot.Goal {
		t.Error("a phase mutated the shared review context")
	}
	if rc.Artifacts.Proposal.Content != snapshot.Artifacts.Proposal.Content {
		t.Error("artifact content changed after resolution")
	}
}
