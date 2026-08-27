package config

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

const (
	varOpen  = "{{"
	varClose = "}}"
)

// Directives that expand to something other than a plain value. Both spellings
// take a comma-separated list, so a prompt naming one agent reads naturally.
const (
	directiveAgent  = "AGENT"
	directiveAgents = "AGENTS"
)

// Placeholders for values a change does not provide, so a prompt says so
// instead of showing an empty path the model might try to open.
const (
	missingPath    = "(not present)"
	emptyList      = "(none)"
	emptyValue     = "(not configured)"
	noRequirements = "(no requirements extracted)"

	defaultModeRules = "Run mode: normal. Edit files, run the validation command, and commit, as the steps below describe."
	noPriorFindings  = "(first round of this loop: nothing has been reported or dispositioned yet)"
	noExternalOutput = "(the external review tool produced no output)"
)

// TemplateError reports a prompt or agent file rrev refused to expand. The file
// is always named: a broken template is the author's bug, and passing it to the
// model unexpanded would hide it.
type TemplateError struct {
	File string
	Msg  string
	Err  error
}

func (e *TemplateError) Error() string { return e.File + ": " + e.Msg }

func (e *TemplateError) Unwrap() error { return e.Err }

// Vars are the values a prompt or agent template expands. Every field is
// resolved once per run except the per-iteration ones.
type Vars struct {
	Change   string
	Goal     string
	GoalLine string

	BaseRef string
	// DiffInstruction is the command a reviewer is told to run; the diff itself
	// is never expanded into a prompt.
	DiffInstruction string

	ProgressLog       string
	ReportFile        string
	ValidationCommand string

	// ModeRules is the run-mode paragraph every prompt expands near its top.
	// Report-only mode replaces the default with its no-mutation rules there,
	// rather than each phase rewriting the prompt body.
	ModeRules string

	// PriorFindings is the external loop's round-to-round memory: earlier
	// findings and how they were dispositioned, so the external tool does not
	// re-report what was rejected with a reason.
	PriorFindings string
	// ExternalOutput is the external tool's raw report, evaluated by the
	// primary executor.
	ExternalOutput string

	OpenSpecDir string
	ChangeDir   string
	Proposal    string
	Design      string
	Tasks       string
	Specs       []string

	// Requirements are the rendered checklist entries, one per requirement, so
	// ChecklistBudget drops whole requirements rather than cutting one in half.
	Requirements []string
	// ChecklistBudget caps the expanded checklist in characters; zero is
	// unlimited.
	ChecklistBudget int

	Iteration     int
	MaxIterations int
}

// Expander turns a prompt file into the text handed to one executor, resolving
// the agents it references through the same layered sources as the prompt.
type Expander struct {
	Assets   Assets
	Executor string
	Vars     Vars
}

// Prompt resolves a phase prompt by name and expands it.
func (e Expander) Prompt(name string) (string, error) {
	asset, err := e.Assets.Prompt(name)
	if err != nil {
		return "", err
	}
	return e.Expand(asset)
}

// Expand substitutes variables and agent references in an already-resolved
// prompt.
func (e Expander) Expand(asset Asset) (string, error) { return e.expand(asset, true) }

// expand walks the template once. Agent definitions are expanded with
// allowAgents false: an agent that could reference agents would recurse.
func (e Expander) expand(asset Asset, allowAgents bool) (string, error) {
	values := e.Vars.values()
	var b strings.Builder
	rest := asset.Content
	for {
		before, after, found := strings.Cut(rest, varOpen)
		b.WriteString(before)
		if !found {
			return b.String(), nil
		}
		body, tail, closed := strings.Cut(after, varClose)
		if !closed {
			return "", &TemplateError{File: asset.Path, Msg: "unterminated " + varOpen + " ... " + varClose + " template directive"}
		}
		text, err := e.substitute(asset, values, body, allowAgents)
		if err != nil {
			return "", err
		}
		b.WriteString(text)
		rest = tail
	}
}

func (e Expander) substitute(asset Asset, values map[string]string, body string, allowAgents bool) (string, error) {
	ref := varOpen + strings.TrimSpace(body) + varClose
	name, arg, isDirective := strings.Cut(body, ":")
	name = strings.ToUpper(strings.TrimSpace(name))

	if isDirective {
		if name != directiveAgent && name != directiveAgents {
			return "", &TemplateError{File: asset.Path, Msg: "unknown template directive " + ref}
		}
		if !allowAgents {
			return "", &TemplateError{File: asset.Path, Msg: "an agent definition may not reference other agents: " + ref}
		}
		return e.expandAgents(asset, arg)
	}

	value, ok := values[name]
	if !ok {
		return "", &TemplateError{File: asset.Path, Msg: fmt.Sprintf("unknown template variable %s; known variables are %s",
			ref, strings.Join(slices.Sorted(maps.Keys(values)), ", "))}
	}
	return value, nil
}

func (e Expander) expandAgents(asset Asset, list string) (string, error) {
	var names []string
	for field := range strings.SplitSeq(list, ",") {
		if name := strings.TrimSpace(field); name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return "", &TemplateError{File: asset.Path, Msg: "agent directive names no agents"}
	}

	defs := make([]agentDef, 0, len(names))
	for _, name := range names {
		agent, err := e.Assets.Agent(name)
		if err != nil {
			return "", &TemplateError{File: asset.Path, Msg: err.Error(), Err: err}
		}
		body, err := e.expand(agent, false)
		if err != nil {
			return "", err
		}
		defs = append(defs, agentDef{name: name, body: body})
	}
	return renderAgents(e.Executor, defs), nil
}

type agentDef struct {
	name string
	body string
}

// renderAgents writes the agent definitions in the invocation form native to
// the executor, keeping the instruction to launch them in one message so they
// run concurrently.
func renderAgents(executor string, defs []agentDef) string {
	var b strings.Builder
	b.WriteString(agentPreamble(executor, len(defs)))
	for _, def := range defs {
		fmt.Fprintf(&b, "\n\n<<<AGENT %s\n%s\nAGENT>>>", def.name, strings.TrimRight(def.body, "\n"))
	}
	return b.String()
}

func agentPreamble(executor string, n int) string {
	subject := "the reviewer agent"
	if n > 1 {
		subject = fmt.Sprintf("each of the %d reviewer agents", n)
	}
	launch := "Launch"
	mechanism := "with claude's Task tool, using the agent definition as the subagent prompt"
	if executor == ExecutorCodex {
		launch, mechanism = "Spawn", "as a codex sub-agent, using the agent definition as its instructions"
	}

	preamble := fmt.Sprintf("%s %s below %s. A definition is the text between its <<<AGENT and AGENT>>>"+
		" markers, which you MUST pass through verbatim.", launch, subject, mechanism)
	if n > 1 {
		preamble += fmt.Sprintf(" Send all %d calls in a single message so the agents run concurrently.", n)
	}
	return preamble
}

func (v Vars) values() map[string]string {
	values := map[string]string{
		"CHANGE":             v.Change,
		"GOAL":               orElse(v.Goal, v.Change),
		"GOAL_LINE":          orElse(v.GoalLine, v.Change),
		"BASE_REF":           v.BaseRef,
		"DIFF_INSTRUCTION":   v.DiffInstruction,
		"PROGRESS_LOG":       orElse(v.ProgressLog, emptyValue),
		"REPORT_FILE":        orElse(v.ReportFile, emptyValue),
		"VALIDATION_COMMAND": orElse(v.ValidationCommand, emptyValue),
		"MODE_RULES":         orElse(v.ModeRules, defaultModeRules),
		"PRIOR_FINDINGS":     orElse(v.PriorFindings, noPriorFindings),
		"EXTERNAL_OUTPUT":    orElse(v.ExternalOutput, noExternalOutput),
		"OPENSPEC_DIR":       orElse(v.OpenSpecDir, missingPath),
		"CHANGE_DIR":         orElse(v.ChangeDir, missingPath),
		"PROPOSAL":           orElse(v.Proposal, missingPath),
		"DESIGN":             orElse(v.Design, missingPath),
		"TASKS":              orElse(v.Tasks, missingPath),
		"SPECS":              pathList(v.Specs),
		"ARTIFACTS":          pathList(artifactPaths(v)),
		"REQUIREMENTS":       renderChecklist(v.Requirements, v.ChecklistBudget),
		"REQUIREMENT_COUNT":  strconv.Itoa(len(v.Requirements)),
		"ITERATION":          strconv.Itoa(v.Iteration),
		"MAX_ITERATIONS":     strconv.Itoa(v.MaxIterations),
	}
	return values
}

func artifactPaths(v Vars) []string {
	var paths []string
	for _, path := range append([]string{v.Proposal, v.Design, v.Tasks}, v.Specs...) {
		if path != "" {
			paths = append(paths, path)
		}
	}
	return paths
}

// renderChecklist fits the checklist into budget characters, saying so when it
// does not fit: a reviewer that knows its checklist was cut can report that,
// while one silently handed a short list reports false conformance.
func renderChecklist(entries []string, budget int) string {
	if len(entries) == 0 {
		return noRequirements
	}
	kept, used := 0, 0
	for _, entry := range entries {
		if budget > 0 && kept > 0 && used+len(entry) > budget {
			break
		}
		used += len(entry)
		kept++
	}
	shown := strings.Join(entries[:kept], "\n")
	if kept == len(entries) {
		return shown
	}
	return shown + fmt.Sprintf("\n[TRUNCATED: this checklist was cut at %d characters. %d of %d requirements are shown;"+
		" %d are missing from this prompt. Read the delta spec files listed above for the rest, and say in your"+
		" report that your checklist was truncated.]\n", budget, kept, len(entries), len(entries)-kept)
}

func pathList(paths []string) string {
	if len(paths) == 0 {
		return emptyList
	}
	lines := make([]string, 0, len(paths))
	for _, path := range paths {
		lines = append(lines, "- "+path)
	}
	return strings.Join(lines, "\n")
}

func orElse(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
