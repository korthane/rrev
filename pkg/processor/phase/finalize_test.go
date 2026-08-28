package phase

import (
	"context"
	"strings"
	"testing"

	"github.com/korthane/rrev/pkg/config"
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
