package phase

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/korthane/rrev/pkg/config"
	"github.com/korthane/rrev/pkg/progress"
)

func TestFinalizeSkippedWhenTheRunMayNotModifyTheRepository(t *testing.T) {
	primary := mock("claude", reviewDone)
	env, _ := newEnv(t, primary, nil, func(c *config.Config) { c.Finalize = true })
	env.SinglePass = true

	res := Finalize(context.Background(), env, Result{Name: NameComprehensive, Reason: ReasonSinglePass, Iterations: 1})

	if !res.Skipped {
		t.Fatalf("result = %+v, want a skipped step", res)
	}
	if !strings.Contains(res.SkipReason, "may not modify") {
		t.Errorf("skip reason = %q, want it to name the read-only run", res.SkipReason)
	}
	if primary.CallCount() != 0 {
		t.Errorf("executor calls = %d, want 0", primary.CallCount())
	}
}

func TestFinalizeReportsChangesItMade(t *testing.T) {
	primary := mock("claude", "rebased and squashed")
	env, repo := newEnv(t, primary, nil, func(c *config.Config) { c.Finalize = true })
	primary.Handler = changingHandler(primary, repo)

	res := Finalize(context.Background(), env, Result{Name: NameComprehensive, Reason: ReasonConverged, Iterations: 1, Changed: true})

	if res.Reason != ReasonConverged || res.Err != nil {
		t.Fatalf("result = %+v, want a completed step", res)
	}
	if !res.Changed {
		t.Error("a finalize step that rewrote the branch must report the change")
	}
	if !res.OK() {
		t.Error("a completed finalize step should report OK")
	}
}

// The finalize step runs once, but its findings still belong to a section a
// reader can skim, and a ledger entry raised here has to record where it was
// raised: an entry that says "raised not yet" names nothing.
func TestFinalizeRecordsItsFindingsInsideAnIterationSection(t *testing.T) {
	primary := mock("claude", "REJECTED: pkg/a.go:7 | quality | the token is echoed | the value is not key material")
	env, _ := newEnv(t, primary, nil, func(c *config.Config) { c.Finalize = true })
	log, err := progress.Open(t.TempDir(), "add-user-auth", progress.Options{})
	if err != nil {
		t.Fatalf("open progress log: %v", err)
	}
	env.Log = log

	Finalize(context.Background(), env, Result{Name: NameComprehensive, Reason: ReasonConverged, Iterations: 1})

	data, err := os.ReadFile(log.Path())
	if err != nil {
		t.Fatalf("read progress log: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "### finalize · iteration 1/1") {
		t.Errorf("finalize wrote no iteration section:\n%s", got)
	}
	if !strings.Contains(got, "raised finalize 1") {
		t.Errorf("a ledger entry raised in finalize records no phase or iteration:\n%s", got)
	}
}
