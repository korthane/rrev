package config

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// variableRef matches a prompt variable as the README writes it.
var variableRef = regexp.MustCompile(`\{\{([A-Z_]+)\}\}`)

// TestREADMEDocumentsEveryVariable keeps the prompt variables and their
// documentation in step in both directions, as the CLI's flags are kept: a
// variable nobody can discover is as broken as a documented one the expander
// rejects as unrecognized. Discovery means the reference table, not a mention
// anywhere: a variable named only in passing prose is one a reader looking it
// up will not find.
func TestREADMEDocumentsEveryVariable(t *testing.T) {
	body, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	listed, mentioned := map[string]bool{}, map[string]bool{}
	for line := range strings.SplitSeq(string(body), "\n") {
		for _, m := range variableRef.FindAllStringSubmatch(line, -1) {
			mentioned[m[1]] = true
			if strings.HasPrefix(line, "|") {
				listed[m[1]] = true
			}
		}
	}

	declared := Vars{}.values()
	for name := range declared {
		if !listed[name] {
			t.Errorf("{{%s}} is a prompt variable but the README's variable table does not list it", name)
		}
	}
	for name := range mentioned {
		if _, ok := declared[name]; !ok {
			t.Errorf("the README documents {{%s}}, which no prompt can use", name)
		}
	}
}
