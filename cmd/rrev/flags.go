package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/korthane/rrev/pkg/config"
	"github.com/korthane/rrev/pkg/processor"
)

// modeFlag pairs a run-mode flag with the mode it selects.
type modeFlag struct {
	name  string
	mode  processor.Mode
	usage string
}

var modeFlags = []modeFlag{
	{"external-only", processor.ModeExternalOnly, "skip the comprehensive phase and start at the external review loop"},
	{"phase1-only", processor.ModePhase1Only, "run only the comprehensive review phase"},
	{"report-only", processor.ModeReportOnly, "collect findings into the report file without touching the repository"},
}

// boolKeys names the settings whose flags take no value. Every other setting is
// registered as a string flag and parsed by the config package.
var boolKeys = map[string]bool{"finalize": true, "debug": true, "no_color": true}

// flagUsage documents each configuration setting as a command line flag. Every
// setting is overridable for a single run, so this covers config.Keys exactly.
var flagUsage = map[string]string{
	"executor":                "primary executor running the review phases and the fixes",
	"claude_command":          "claude executable to invoke",
	"codex_command":           "codex executable to invoke",
	"external_review_tool":    "independent second opinion for the external review loop",
	"external_review_command": "script the custom external reviewer runs",
	"base_ref":                "ref the review diffs against, instead of the detected default branch",
	"model":                   "model[:effort] every phase inherits from",
	"review_model":            "model[:effort] for the comprehensive review phase",
	"external_model":          "model[:effort] for the external review loop",
	"final_model":             "model[:effort] for the final review phase",
	"finalize_model":          "model[:effort] for the finalize step",
	"max_iterations":          "iteration limit for the comprehensive review phase",
	"external_max_iterations": "iteration limit for the external review loop",
	"final_max_iterations":    "iteration limit for the final review phase",
	"stalemate_patience":      "consecutive unchanged iterations tolerated before a loop gives up; 0 disables",
	"session_timeout":         "bound on a whole executor call; 0 disables",
	"idle_timeout":            "bound on a silent stretch of an executor call; 0 disables",
	"progress_interval":       "how often to report that a silent executor is still working",
	"finalize":                "run the finalize step after the last review phase",
	"progress_dir":            "directory the per-change progress log is written to",
	"report_file":             "destination of the findings report",
	"checklist_budget":        "maximum characters of requirement checklist expanded into a prompt",
	"ledger_budget":           "maximum characters of standing-rejection ledger expanded into a prompt",
	"validation_command":      "command the executor runs before committing a fix",
	"debug":                   "record resolved command lines, full prompts, and the full arguments and output of reported tool calls",
	"no_color":                "disable coloured terminal output",
}

// flagName renders a configuration key as the flag that overrides it.
func flagName(key string) string { return strings.ReplaceAll(key, "_", "-") }

// options is one parsed command line: what to review, how, and the settings the
// run overrides.
type options struct {
	// Change is the positional change name; empty means auto-detect.
	Change string
	Mode   processor.Mode
	// Flags holds only the settings actually given, keyed as the config file
	// keys them, so a flag never zeroes a value it did not mention.
	Flags   map[string]string
	Version bool
}

// modeConflictError reports run-mode flags that were combined, naming each one:
// preferring one silently would run a pipeline the user did not ask for.
type modeConflictError struct {
	Flags []string
}

func (e *modeConflictError) Error() string {
	return fmt.Sprintf("%s: run modes are mutually exclusive, pass only one", joinAnd(e.Flags))
}

// parseArgs parses a command line into options, writing usage to out. It
// returns flag.ErrHelp when usage was requested.
func parseArgs(args []string, out io.Writer) (*options, error) {
	fs := flag.NewFlagSet("rrev", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.Usage = func() { writeUsage(out, fs) }

	registerFlags(fs)

	positional, err := parseInterspersed(fs, args)
	if err != nil {
		return nil, err
	}
	if len(positional) > 1 {
		return nil, fmt.Errorf("expected at most one change name, got %s", joinAnd(quoteAll(positional)))
	}

	opts := &options{Mode: processor.ModeFull, Flags: map[string]string{}, Version: isSet(fs, "version")}
	if len(positional) == 1 {
		opts.Change = positional[0]
	}

	var selected []string
	for _, m := range modeFlags {
		if isSet(fs, m.name) {
			selected = append(selected, "--"+m.name)
			opts.Mode = m.mode
		}
	}
	if len(selected) > 1 {
		return nil, &modeConflictError{Flags: selected}
	}

	byFlag := make(map[string]string, len(config.Keys()))
	for _, key := range config.Keys() {
		byFlag[flagName(key)] = key
	}
	fs.Visit(func(f *flag.Flag) {
		if key, ok := byFlag[f.Name]; ok {
			opts.Flags[key] = f.Value.String()
		}
	})
	return opts, nil
}

// registerFlags declares every flag rrev accepts: the version flag, one per run
// mode, and one per configuration setting, since every setting is overridable
// for a single run.
func registerFlags(fs *flag.FlagSet) {
	fs.Bool("version", false, "print the rrev version and exit")
	for _, m := range modeFlags {
		fs.Bool(m.name, false, m.usage)
	}
	for _, key := range config.Keys() {
		if boolKeys[key] {
			fs.Bool(flagName(key), false, flagUsage[key])
			continue
		}
		fs.String(flagName(key), "", flagUsage[key])
	}
}

// isSet reports whether a boolean flag was turned on.
func isSet(fs *flag.FlagSet, name string) bool { return fs.Lookup(name).Value.String() == "true" }

// parseInterspersed parses args, collecting positional arguments wherever they
// appear. Go's flag package stops at the first one, which would silently ignore
// every flag typed after the change name.
func parseInterspersed(fs *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fs.Arg(0))
		rest = slices.Clone(fs.Args()[1:])
	}
}

func writeUsage(out io.Writer, fs *flag.FlagSet) {
	// A terminal that cannot be written to is not worth reporting on.
	_, _ = fmt.Fprint(out, `usage: rrev [flags] [change]

Reviews the current branch against an OpenSpec change, alternating independent
reviewers with a fixing executor until the reviewers go quiet. With no change
name rrev reviews the single active change and refuses to guess between several.

flags:
`)
	fs.PrintDefaults()
}

// describeFlagError renames a rejected configuration value after the flag that
// set it, so the message points at what the user typed rather than at the
// setting the flag happens to override.
func describeFlagError(err error) error {
	var invalid *config.ValueError
	if !errors.As(err, &invalid) || invalid.Src.Layer != config.LayerFlags {
		return err
	}
	return fmt.Errorf("--%s: invalid value %q: %s", flagName(invalid.Key), invalid.Value, invalid.Msg)
}

func joinAnd(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	default:
		return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
	}
}

func quoteAll(items []string) []string {
	quoted := make([]string, 0, len(items))
	for _, item := range items {
		quoted = append(quoted, strconv.Quote(item))
	}
	return quoted
}
