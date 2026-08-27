package openspec

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// DefaultCLIBin is the openspec executable rrev looks for on PATH.
const DefaultCLIBin = "openspec"

// ErrCLIUnavailable reports that the openspec CLI could not be used, which puts
// discovery and extraction on their filesystem fallbacks.
var ErrCLIUnavailable = errors.New("openspec CLI unavailable")

// CLI invokes the openspec command-line tool. The zero value uses the
// DefaultCLIBin looked up on PATH.
type CLI struct {
	// Bin overrides the executable name or path, mainly for tests.
	Bin string
	// Disabled forces the filesystem and markdown fallbacks.
	Disabled bool
}

func (c CLI) bin() string {
	if c.Bin != "" {
		return c.Bin
	}
	return DefaultCLIBin
}

// Available reports whether the openspec CLI can be invoked.
func (c CLI) Available() bool {
	if c.Disabled {
		return false
	}
	_, err := exec.LookPath(c.bin())
	return err == nil
}

func (c CLI) run(dir string, args ...string) ([]byte, error) {
	if c.Disabled {
		return nil, fmt.Errorf("%w: disabled", ErrCLIUnavailable)
	}
	cmd := exec.Command(c.bin(), args...)
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("%w: %s %s: %s", ErrCLIUnavailable, c.bin(), strings.Join(args, " "), msg)
	}
	return out, nil
}

type listPayload struct {
	Changes []struct {
		Name string `json:"name"`
	} `json:"changes"`
}

// ListChanges returns the active change names the CLI reports for dir. Archived
// changes are not part of that listing.
func (c CLI) ListChanges(dir string) ([]string, error) {
	out, err := c.run(dir, "list", "--changes", "--json", "--sort", "name")
	if err != nil {
		return nil, err
	}
	var payload listPayload
	if err := json.Unmarshal(out, &payload); err != nil {
		return nil, fmt.Errorf("%w: parse list output: %w", ErrCLIUnavailable, err)
	}
	names := make([]string, 0, len(payload.Changes))
	for _, change := range payload.Changes {
		if change.Name != "" {
			names = append(names, change.Name)
		}
	}
	return names, nil
}

type showPayload struct {
	Deltas []struct {
		Spec         string           `json:"spec"`
		Operation    string           `json:"operation"`
		Requirement  *cliRequirement  `json:"requirement"`
		Requirements []cliRequirement `json:"requirements"`
	} `json:"deltas"`
}

type cliRequirement struct {
	Text      string `json:"text"`
	Scenarios []struct {
		RawText string `json:"rawText"`
	} `json:"scenarios"`
}

// ExtractRequirements reads the change's delta requirements from the CLI's JSON
// output. The CLI reports no requirement or scenario titles, so callers pair the
// result with the markdown parser to recover them.
func (c CLI) ExtractRequirements(dir, change string) ([]Requirement, error) {
	out, err := c.run(dir, "show", change, "--json", "--deltas-only")
	if err != nil {
		return nil, err
	}
	return decodeShowPayload(out)
}

func decodeShowPayload(out []byte) ([]Requirement, error) {
	var payload showPayload
	if err := json.Unmarshal(out, &payload); err != nil {
		return nil, fmt.Errorf("%w: parse show output: %w", ErrCLIUnavailable, err)
	}
	var reqs []Requirement
	for _, delta := range payload.Deltas {
		// `requirements` is the current field; `requirement` is its singular
		// predecessor and carries the same first entry when both are present.
		entries := delta.Requirements
		if len(entries) == 0 && delta.Requirement != nil {
			entries = []cliRequirement{*delta.Requirement}
		}
		for _, entry := range entries {
			req := Requirement{
				Capability: delta.Spec,
				Operation:  ParseOperation(delta.Operation),
				Text:       strings.TrimSpace(entry.Text),
			}
			for _, scenario := range entry.Scenarios {
				req.Scenarios = append(req.Scenarios, Scenario{Text: strings.TrimSpace(scenario.RawText)})
			}
			reqs = append(reqs, req)
		}
	}
	return reqs, nil
}
