package config

import (
	"os"
	"regexp"
	"testing"
)

// variableRef matches a prompt variable as the README writes it.
var variableRef = regexp.MustCompile(`\{\{([A-Z_]+)\}\}`)

// TestREADMEDocumentsEveryVariable keeps the prompt variables and their
// documentation in step in both directions, as the CLI's flags are kept: a
// variable nobody can discover is as broken as a documented one the expander
// rejects as unrecognized.
func TestREADMEDocumentsEveryVariable(t *testing.T) {
	body, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	documented := map[string]bool{}
	for _, m := range variableRef.FindAllStringSubmatch(string(body), -1) {
		documented[m[1]] = true
	}

	declared := Vars{}.values()
	for name := range declared {
		if !documented[name] {
			t.Errorf("{{%s}} is a prompt variable but the README does not document it", name)
		}
	}
	for name := range documented {
		if _, ok := declared[name]; !ok {
			t.Errorf("the README documents {{%s}}, which no prompt can use", name)
		}
	}
}
