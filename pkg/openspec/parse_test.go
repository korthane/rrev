package openspec_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/korthane/rrev/pkg/openspec"
)

func readFixtureSpec(t *testing.T, capability string) string {
	t.Helper()
	content, err := os.ReadFile("testdata/repo/openspec/changes/add-thing/specs/" + capability + "/spec.md")
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func TestParseDeltaSpec(t *testing.T) {
	reqs, err := openspec.ParseDeltaSpec("auth", readFixtureSpec(t, "auth"))
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 4 {
		t.Fatalf("parsed %d requirements, want 4", len(reqs))
	}

	want := []struct {
		name      string
		operation openspec.Operation
		scenarios int
	}{
		{"TOTP enrollment", openspec.OperationAdded, 2},
		{"Recovery codes", openspec.OperationAdded, 1},
		{"Sign-in", openspec.OperationModified, 1},
		{"SMS fallback", openspec.OperationRemoved, 0},
	}
	for i, w := range want {
		got := reqs[i]
		if got.Name != w.name || got.Operation != w.operation || len(got.Scenarios) != w.scenarios {
			t.Errorf("requirement %d = %q/%s/%d scenarios, want %q/%s/%d",
				i, got.Name, got.Operation, len(got.Scenarios), w.name, w.operation, w.scenarios)
		}
		if got.Capability != "auth" {
			t.Errorf("requirement %d capability = %q, want auth", i, got.Capability)
		}
	}

	first := reqs[0]
	if !strings.Contains(first.Text, "enroll a TOTP authenticator") {
		t.Errorf("requirement text = %q, want the requirement body", first.Text)
	}
	if first.Scenarios[0].Name != "Enrollment succeeds" {
		t.Errorf("scenario name = %q, want %q", first.Scenarios[0].Name, "Enrollment succeeds")
	}
	if !strings.Contains(first.Scenarios[0].Text, "**WHEN**") || !strings.Contains(first.Scenarios[0].Text, "**THEN**") {
		t.Errorf("scenario text = %q, want the WHEN/THEN lines", first.Scenarios[0].Text)
	}
	if strings.Contains(first.Text, "Purpose") {
		t.Errorf("requirement text = %q, want the preamble excluded", first.Text)
	}
}

func TestParseDeltaSpecNestedCapability(t *testing.T) {
	reqs, err := openspec.ParseDeltaSpec("billing/invoices", readFixtureSpec(t, "billing/invoices"))
	if err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 1 || reqs[0].Capability != "billing/invoices" {
		t.Fatalf("reqs = %+v, want one requirement carrying the nested capability path", reqs)
	}
}

func TestParseDeltaSpecUnparseable(t *testing.T) {
	_, err := openspec.ParseDeltaSpec("auth", "## Purpose\n\nProse with no requirement headers.\n")
	if !errors.Is(err, openspec.ErrNoRequirements) {
		t.Errorf("err = %v, want ErrNoRequirements", err)
	}
}

func TestParseOperationUnknownIsUnspecified(t *testing.T) {
	reqs, err := openspec.ParseDeltaSpec("auth", "### Requirement: Loose\nSHALL do a thing.\n")
	if err != nil {
		t.Fatal(err)
	}
	if reqs[0].Operation != openspec.OperationUnspecified {
		t.Errorf("operation = %s, want UNSPECIFIED when no delta section declares one", reqs[0].Operation)
	}
}

// The blank line after a delta section header used to be flushed onto the
// requirement that preceded it, erasing that requirement's body and its last
// scenario's steps. Every requirement before a section boundary is checked.
func TestParseDeltaSpecKeepsBodiesAcrossSectionBoundaries(t *testing.T) {
	reqs, err := openspec.ParseDeltaSpec("auth", readFixtureSpec(t, "auth"))
	if err != nil {
		t.Fatal(err)
	}
	for _, req := range reqs {
		if strings.TrimSpace(req.Text) == "" {
			t.Errorf("requirement %q has no text", req.Name)
		}
		for _, scenario := range req.Scenarios {
			if strings.TrimSpace(scenario.Text) == "" {
				t.Errorf("requirement %q scenario %q has no steps", req.Name, scenario.Name)
			}
		}
	}
	if !strings.Contains(reqs[1].Text, "single-use recovery codes") {
		t.Errorf("last ADDED requirement text = %q, want its body", reqs[1].Text)
	}
	if !strings.Contains(reqs[2].Scenarios[0].Text, "**THEN**") {
		t.Errorf("last MODIFIED scenario text = %q, want its WHEN/THEN lines", reqs[2].Scenarios[0].Text)
	}
}

func TestChecklistEntries(t *testing.T) {
	reqs, err := openspec.ParseDeltaSpec("auth", readFixtureSpec(t, "auth"))
	if err != nil {
		t.Fatal(err)
	}
	entries := openspec.ChecklistEntries(reqs)
	if len(entries) != len(reqs) {
		t.Fatalf("entries = %d, want one per requirement (%d)", len(entries), len(reqs))
	}
	checklist := strings.Join(entries, "")
	for _, want := range []string{
		"[ADDED] auth: TOTP enrollment",
		"[MODIFIED] auth: Sign-in",
		"[REMOVED] auth: SMS fallback",
		"Enrollment succeeds",
		"no scenarios declared",
	} {
		if !strings.Contains(checklist, want) {
			t.Errorf("checklist missing %q:\n%s", want, checklist)
		}
	}
	if got := openspec.ChecklistEntries(nil); len(got) != 0 {
		t.Errorf("empty checklist = %v, want no entries", got)
	}
}
