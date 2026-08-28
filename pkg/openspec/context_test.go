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
	checklist := strings.Join(openspec.ChecklistEntries(rc.Requirements), "")
	if !strings.Contains(checklist, "[REMOVED] auth:") {
		t.Errorf("checklist does not label the removed requirement:\n%s", checklist)
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
	if !slices.ContainsFunc(rc.UnparsedSpecs, func(p string) bool { return strings.HasSuffix(p, "specs/auth/spec.md") }) {
		t.Errorf("unparsed specs = %v, want the file the reviewers must read themselves", rc.UnparsedSpecs)
	}
}

// TestContextIsResolvedOnce pins the context's immutability: it is a value, so a
// phase cannot pick up an artifact edited on disk mid-run.
func TestContextIsResolvedOnce(t *testing.T) {
	rc := resolveFixture(t, stubCLI(t))
	goal, change := rc.Goal, rc.Change
	requirements := len(rc.Requirements)
	specs := len(rc.Artifacts.Specs)

	// A phase takes the context by value and reassigns fields on its own copy,
	// which is what keeps a later phase from seeing the earlier one's edits.
	// The artifacts a copy points at are read-only by contract, not by type.
	perPhase := func(c openspec.Context) {
		c.Goal = "mutated"
		c.Change.Name = "mutated"
		c.Artifacts.Proposal = nil
		c.Requirements = nil
		c.Artifacts.Specs = nil
	}
	perPhase(rc)

	if rc.Goal != goal || !reflect.DeepEqual(rc.Change, change) {
		t.Errorf("a phase mutated the shared review context: goal %q, change %+v", rc.Goal, rc.Change)
	}
	if rc.Artifacts.Proposal == nil {
		t.Error("a phase dropping its own proposal reference cleared the shared one")
	}
	if len(rc.Requirements) != requirements || len(rc.Artifacts.Specs) != specs {
		t.Errorf("requirements = %d and specs = %d, want %d and %d left alone",
			len(rc.Requirements), len(rc.Artifacts.Specs), requirements, specs)
	}
}

// TestArtifactEditedMidRunIsNotPickedUp covers the mid-run edit for real: the
// context resolves once, so rewriting an artifact on disk afterwards moves
// neither the checklist, nor the goal, nor the captured text a phase is handed.
func TestArtifactEditedMidRunIsNotPickedUp(t *testing.T) {
	root := openspec.Root{Dir: t.TempDir()}
	proposal := "## Why\n\nUsers need a second factor.\n"
	spec := "## ADDED Requirements\n\n### Requirement: TOTP enrollment\n" +
		"The system SHALL let a user enroll an authenticator.\n\n" +
		"#### Scenario: Enrollment succeeds\n- **WHEN** a valid code is submitted\n- **THEN** it is activated\n"
	change := writeChange(t, root, "add-thing", map[string]string{
		"proposal.md":        proposal,
		"tasks.md":           "- [ ] 1.1 Do it\n",
		"specs/auth/spec.md": spec,
	})

	rc, err := openspec.Resolve(openspec.CLI{Disabled: true}, root, change, openspec.Discovery{})
	if err != nil {
		t.Fatal(err)
	}
	checklist := strings.Join(openspec.ChecklistEntries(rc.Requirements), "")

	// The fix step of a review edits the change's own files, so this is the run
	// the scenario describes rather than a hypothetical one.
	writeChange(t, root, "add-thing", map[string]string{
		"proposal.md": "## Why\n\nSomething else entirely.\n",
		"specs/auth/spec.md": spec + "\n### Requirement: Recovery codes\n" +
			"The system SHALL issue recovery codes.\n\n" +
			"#### Scenario: Codes issued\n- **WHEN** enrollment completes\n- **THEN** codes are returned\n",
	})

	if got := strings.Join(openspec.ChecklistEntries(rc.Requirements), ""); got != checklist {
		t.Errorf("checklist changed after a mid-run edit:\n%s\nwant:\n%s", got, checklist)
	}
	if !strings.Contains(rc.Goal, "second factor") {
		t.Errorf("goal = %q, want the one derived at startup", rc.Goal)
	}
	if rc.Artifacts.Proposal.Content != proposal {
		t.Errorf("proposal content = %q, want the text captured at startup", rc.Artifacts.Proposal.Content)
	}
}
