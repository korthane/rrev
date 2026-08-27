package main

import (
	"strings"
	"testing"
)

func TestRunVersion(t *testing.T) {
	var out strings.Builder
	if err := run([]string{"--version"}, &out); err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := out.String(); !strings.HasPrefix(got, "rrev ") {
		t.Errorf("got %q, want it to start with %q", got, "rrev ")
	}
}
