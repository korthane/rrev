# rrev

`rrev` reviews the current branch against a named [OpenSpec](https://github.com/Fission-AI/OpenSpec)
change and autonomously fixes and commits what it finds.

Most AI code review is generic: "find bugs in this diff". rrev is spec-driven —
the change's proposal, design, delta specs, and tasks are the source of truth, so
the requirements and scenarios written before implementation become explicit
conformance criteria for the diff that followed.

A run alternates independent reviewer agents with a fixing executor across three
phases — comprehensive review, cross-model external review, and a final
regression pass — iterating until nothing serious is left.

## Installation

On macOS, with [Homebrew](https://brew.sh) — no Go toolchain of your own
needed, since the formula brings one for the build and leaves none behind:

```sh
brew install korthane/tap/rrev
```

Anywhere, with Go 1.27 or newer:

```sh
go install github.com/korthane/rrev/cmd/rrev@latest
```

Or from a clone, which also needs Go:

```sh
make build    # build ./rrev
make test     # run tests
make lint     # run golangci-lint
make coverage # test with coverage report
```

Whichever path you take, `rrev --version` reports the release it came from.
[docs/releasing.md](docs/releasing.md) covers how a release is cut.

## Prerequisites

- Go 1.27 or newer for `go install` or a build from a clone. Homebrew installs
  need it only during the build, and install it for you.
- `git`, and a working directory inside a git repository.
- An OpenSpec-driven repository: an `openspec/` directory with the change under
  `openspec/changes/<change>/`.
- The `claude` CLI — the default primary executor.
- The `codex` CLI — the default external review tool. Not needed when external
  review is set to `none` or to a custom script.
- The `openspec` CLI is optional. When it is present rrev discovers changes and
  extracts requirements through it; when it is not, rrev reads
  `openspec/changes/` and parses the delta specs itself, and says so in its
  output.

Startup preflight checks all of this before the first phase runs: a missing
binary, an unresolvable base ref, or an unreadable change fails immediately
rather than part-way through a review. A branch with no changes relative to the
base ref reports that there is nothing to review and exits zero without
invoking an executor.

## What a run is allowed to do

A review loop cannot answer approval prompts, so rrev invokes each tool with its
own approval and sandbox checks turned off:

```sh
claude --print --dangerously-skip-permissions ...
codex exec --dangerously-bypass-approvals-and-sandbox --skip-git-repo-check ...
```

The executor can therefore edit files, run commands, and commit in your
repository without asking. Run rrev only on repositories and branches you are
willing to have modified.

`--report-only` tells the executor to change nothing, but that instruction
travels in the prompt; it is not enforced by a sandbox.

## Usage

```sh
rrev add-user-auth   # review the branch against a named change
rrev                 # review against the single active change
```

With no positional argument rrev auto-detects the change. If more than one is
active it exits with a usage error listing them rather than guessing.

## Run modes

Modes are mutually exclusive; combining two is a startup error.

| Mode | Flag | What runs |
| --- | --- | --- |
| Full pipeline | *(default)* | comprehensive review, external review loop, final review, finalize when enabled |
| External only | `--external-only` | external review loop, final review, finalize when enabled |
| First phase only | `--phase1-only` | comprehensive review, then exit; finalize never runs |
| Report only | `--report-only` | comprehensive and external review as single read-only passes, writing a findings report; the final regression pass is skipped, since no fix was applied that could have regressed anything |

Report-only mode never edits a tracked file, stages, or commits: no tracked file
and no commit changes. It does write its own artifacts — the findings report at
`report_file` and the run's progress log under `progress_dir`.

A mode whose whole phase sequence is skipped — `--external-only` with external
review disabled, for instance — reviewed nothing, and says so rather than
reporting convergence.

### What a phase does

1. **Comprehensive review** — launches every reviewer agent concurrently against
   the branch diff, then deduplicates their findings, verifies each against the
   real code, fixes the confirmed ones, runs the validation command, and commits.
   It converges on the first iteration that confirms nothing critical or major:
   the minor findings are still fixed and committed, but hunting the last one is
   what runs a review to its iteration limit, since every fix is itself
   reviewable. The final regression pass looks at the branch again afterwards,
   whenever those fixes changed anything. The executor is asked to signal that
   itself, and rrev enforces the same rule from the iteration's report — an
   iteration that confirmed findings, all of them minor, and no validation
   reported as failed ends the phase as `converged: minor findings only`.
   Anything rrev cannot read that way keeps the loop going: a report with no
   findings at all cannot be told apart from an executor that died before
   writing one, and a severity outside `critical|major|minor` is a line it could
   not parse rather than a clean one, so a replacement prompt has to keep that
   vocabulary for the rule to hold. A review that truly found nothing has
   `<<<RREV:REVIEW_DONE>>>` to say so.

   Iterations after the first are driven by `review_repeat.txt` instead, and are
   pointed primarily at the fixes the previous iteration committed — `git diff
   <last reviewed commit>..HEAD` — with the full branch diff still in the
   instruction for regressions those fixes may have caused elsewhere. An
   iteration that follows one which committed nothing reviews the full branch,
   the same scope as the first.
2. **External review loop** — an independent tool reviews the same diff against
   the same requirement checklist; the primary executor then evaluates what it
   reported, fixes what it confirms, and records why it rejected the rest. Later
   rounds carry the earlier rounds' dispositions, so a rejected finding does not
   come back unchanged. Skipped when the primary executor and the external tool
   would be the same model reading its own work. A round converges only on what
   the external tool itself said: a tool that returns neither a finding nor
   `<<<RREV:EXTERNAL_DONE>>>` wrote nothing rrev can read as a review, and that
   round does not converge even if the evaluation that followed it signalled
   done. The loop then runs on to `external_max_iterations` and the run ends
   unconverged, rather than filing a broken tool as a clean pass.
3. **Final review** — a narrow regression pass restricted to critical and major
   issues, to catch what the earlier fixes broke. Skipped when nothing was
   changed that could have regressed.
4. **Finalize** — optional, disabled by default, driven by an overridable
   prompt, run once after every review phase converged. Skipped when the mode's
   whole phase sequence was skipped: it rewrites history, and a review that never
   ran is no basis for that. Its failure never changes the run's outcome.

Every phase reviews `git diff <base ref>...HEAD` together with the branch's
commit log; repeat comprehensive iterations add the narrower diff described
above and keep the full one. The diff is never expanded into a prompt: reviewers
are told the commands that produce it.

## Exit status

| Status | Meaning |
| --- | --- |
| 0 | the pipeline converged, or report-only found nothing |
| 1 | the run failed to start, aborted, or a phase could not complete |
| 2 | the run ended with findings outstanding — a loop hit its iteration limit, ended on a stalemate, or a report-only run reported findings |

A phase that ended `converged: minor findings only` converged: it counts towards
status 0 exactly as one that ended on the signal does.

## Interrupting a run

- **Ctrl+C** aborts: rrev cancels the running executor, terminates its process
  group, writes what it has to the progress log, and exits non-zero.
- **Ctrl+\\** ends only the external review loop and lets the pipeline continue
  with the final review phase. On platforms with no break signal the hint is
  omitted from the banner and the loop ends only on its own conditions.

## Configuration

Every setting resolves from four sources, highest precedence first:

1. command-line flags
2. project configuration — `.rrev/config.ini` in the repository
3. user configuration — `~/.config/rrev/config.ini`, or `$XDG_CONFIG_HOME/rrev`
4. embedded defaults

A source that omits a setting leaves the next source's value alone; it never
contributes a zero. With no configuration files at all rrev runs entirely on its
embedded defaults. A file that cannot be parsed is an error naming the file and
the offending line — rrev does not silently fall back to defaults.

The format is INI-style `key = value`. Inline comments are not supported, so a
`#` inside a value is part of the value. A line whose first non-space character
is `#` is a comment.

### Settings

Each setting has a flag named after it, with underscores written as hyphens.

| Setting | Flag | Default | Meaning |
| --- | --- | --- | --- |
| `executor` | `--executor` | `claude` | primary executor running the review phases and the fixes: `claude` or `codex` |
| `claude_command` | `--claude-command` | `claude` | claude executable to invoke |
| `codex_command` | `--codex-command` | `codex` | codex executable to invoke |
| `external_review_tool` | `--external-review-tool` | `codex` | independent second opinion: `codex`, `custom`, or `none` |
| `external_review_command` | `--external-review-command` | *(empty)* | script the custom external reviewer runs; its stdout is its findings |
| `base_ref` | `--base-ref` | *(detected)* | ref the review diffs against; empty detects the repository's default branch |
| `model` | `--model` | *(tool default)* | `model[:effort]` every phase inherits from |
| `review_model` | `--review-model` | inherits `model` | `model[:effort]` for the comprehensive review phase |
| `external_model` | `--external-model` | inherits `review_model`, then `model` | `model[:effort]` for the external review loop; an inherited model name is dropped when the external tool differs from the primary executor, so name that tool's model here |
| `final_model` | `--final-model` | inherits `review_model`, then `model` | `model[:effort]` for the final review phase |
| `finalize_model` | `--finalize-model` | inherits `model` | `model[:effort]` for the finalize step |
| `max_iterations` | `--max-iterations` | `10` | iteration limit for the comprehensive review phase |
| `external_max_iterations` | `--external-max-iterations` | `5` | iteration limit for the external review loop |
| `final_max_iterations` | `--final-max-iterations` | `5` | iteration limit for the final review phase |
| `stalemate_patience` | `--stalemate-patience` | `0` | consecutive unchanged iterations tolerated before a loop gives up; 0 disables |
| `session_timeout` | `--session-timeout` | `0` | bound on a whole executor call; 0 disables |
| `idle_timeout` | `--idle-timeout` | `0` | bound on a silent stretch of an executor call; 0 disables |
| `progress_interval` | `--progress-interval` | `30s` | how often to report that a silent executor is still working |
| `finalize` | `--finalize` | `false` | run the finalize step after the last review phase |
| `progress_dir` | `--progress-dir` | `.rrev/progress` | directory the per-change progress log is written to; rrev writes a catch-all ignore rule there, so it must be a directory of its own |
| `report_file` | `--report-file` | `.rrev/findings.md` | destination of the findings report |
| `checklist_budget` | `--checklist-budget` | `120000` | maximum characters of requirement checklist expanded into a prompt; 0 is unlimited |
| `ledger_budget` | `--ledger-budget` | `40000` | maximum characters of standing-rejection ledger expanded into a prompt; 0 is unlimited |
| `validation_command` | `--validation-command` | *(empty)* | command the executor runs before committing a fix |
| `debug` | `--debug` | `false` | record resolved command lines, full prompts, and the full arguments and output of reported tool calls |
| `no_color` | `--no-color` | `false` | disable coloured terminal output |

Two further flags select no setting: `--version` prints the version and exits,
and the mode flags above.

A model specification is `model[:effort]`. Either part may be omitted and
inherits the configured default; an effort level the selected tool does not
accept is reported and dropped rather than passed through.

rrev rejects configurations that contradict each other. Asking by flag for codex
as both the primary executor and the external review tool is a startup error;
the same contradiction reached any other way disables the external phase with a
warning on stderr. `--executor codex` alone is therefore not an error: the
default external tool is codex, so rrev skips the external phase rather than
having codex review its own work. The same applies to selecting `custom` as the
external review tool with no `external_review_command` to run.

`external_review_command` is split on whitespace and executed directly, not
through a shell: quotes and shell operators are not interpreted, and preflight
checks the first field is on `PATH`. Wrap anything needing a shell in a script
file. rrev writes the review prompt to the script's stdin and treats its stdout
as the findings — findings only, since a `REJECTED:` line from an unverified
second opinion is discarded rather than logged.

Colour is disabled by `no_color`, by a non-empty `NO_COLOR`, by `TERM=dumb`, and
whenever output is not a terminal.

### What a run prints

While an executor runs, rrev streams its activity rather than leaving an
unexplained pause. Every line is attributed to the phase that produced it, and a
line the executor's format attributes to a reviewer agent carries that agent's
name too, so seven concurrent reviewers are tellable apart; a line the format
does not attribute carries the phase alone rather than a guess.

A tool call renders the argument that distinguishes it — the command for a
shell invocation, the path for a read or write, the pattern for a search, the
agent for a sub-agent launch — followed by its outcome, and its failure detail
when it failed:

```
[comprehensive] · agent: conformance
[comprehensive] [conformance] Scenario 3 is not addressed.
[comprehensive] · [conformance] tool: Grep func Open → failed: no matches under pkg
[comprehensive] · tool: Bash go test ./... … → ok
```

A call is reported once its result arrives, so its line carries the outcome
rather than appearing twice. Sub-agent launches are the exception: a reviewer
runs for minutes, so the launch is announced as it happens and its outcome
follows on a second line. A run cut short still reports the calls that were in
flight, without an outcome.

The line prefix is the phase. `·` marks rrev's account of what the tool is
doing, as against the model's own words; `[agent]` names the reviewer when the
executor's format identified one. Reviewer agents are launched with their name
in the call's description, which is what rrev reads that attribution from. Only
a bare token no longer than 32 bytes is taken as a name: a description the
executor filled with a phrase instead, or an agent whose own name is longer than
that, contributes no attribution and its lines carry the phase alone.

The tool's own output never appears: a diff or a test run would flood the
display. An argument spanning several lines or longer than 100 bytes is cut to
its first line and marked `…`; that bound is fixed rather than read from the
terminal. Control characters are stripped, so neither a command nor a tool's
error text can repaint the display. `--debug` is the one place these caps
come off, recording each reported call's full arguments and output.

How much of this appears depends on the executor: claude's `stream-json` carries
per-agent attribution and structured tool arguments, while codex reports shell
commands and their exit status only. A long stretch inside sub-agents with
nothing to report still produces a heartbeat every `progress_interval`.

## Prompts and agents

Every phase prompt and every reviewer agent ships embedded in the binary. Any
one of them can be replaced by placing a file with the same name in
`.rrev/prompts/` or `.rrev/agents/` (project) or under the user configuration
directory. Overriding one file leaves every other one on its default.

An override is used exactly as written. One written before the standing-rejection
ledger existed carries no `{{LEDGER}}`, and a variable a file never mentions is
not an error — only an unrecognized one is — so that reviewer is shown nothing
that was settled and names no identifiers, and each of its re-raises opens a
fresh ledger entry. To bring an override up to date, copy the standing-rejections
block and the id instruction across from the shipped default. The same holds for
an `external_eval.txt` override that omits `{{EXTERNAL_FINDINGS}}`: the evaluator
is shown the tool's findings without their ids, so each confirmation or rejection
opens a second entry beside the reported one. Copy the parsed-findings block and
the carry-the-id paragraph from the shipped default.

The comprehensive phase runs two prompts, so a `review_first.txt` override
written before `review_repeat.txt` existed now covers the first iteration only
and every iteration after it runs the shipped repeat prompt. A project that
wants its own wording throughout the phase has to override both.

| Prompt | Phase |
| --- | --- |
| `review_first.txt` | comprehensive review, first iteration |
| `review_repeat.txt` | comprehensive review, every iteration after the first |
| `external_review.txt` | the external tool's review |
| `external_eval.txt` | the primary executor's evaluation of external findings |
| `review_final.txt` | final regression review |
| `finalize.txt` | finalize step |

Shipped agents: `conformance`, `tasks`, `quality`, `implementation`, `testing`,
`simplification`, `documentation`.

A prompt names the agents its phase launches:

```text
{{AGENTS: conformance, tasks, quality}}
```

The reference expands into the invocation form of whichever executor runs that
phase, and instructs it to launch all of them in one message so they run
concurrently. Adding `.rrev/agents/perf.txt` and naming it in a prompt launches
it alongside the defaults; dropping a name from the prompt stops that agent from
running, even though its definition still exists. A referenced agent that
resolves to no file is a startup error naming the prompt and the agent.

### Template variables

Prompts and agent definitions expand these. An unrecognized variable is an error
naming the file and the variable rather than text passed through to the model.

| Variable | Value |
| --- | --- |
| `{{CHANGE}}` | the selected change's name |
| `{{GOAL}}`, `{{GOAL_LINE}}` | the derived one-line review goal |
| `{{BASE_REF}}` | the resolved base ref |
| `{{DIFF_INSTRUCTION}}` | the command(s) producing the diff under review, expanded identically in every reviewer agent; a comprehensive iteration following one that committed gets two, per [What a phase does](#what-a-phase-does) |
| `{{PROGRESS_LOG}}` | path of this run's progress log |
| `{{REPORT_FILE}}` | path of the findings report |
| `{{VALIDATION_COMMAND}}` | the configured validation command |
| `{{MODE_RULES}}` | the run mode's rules paragraph |
| `{{REVIEWER_MODE_RULES}}` | the same paragraph for report-only reviewer prompts |
| `{{LEDGER}}` | the standing-rejection ledger, most-raised first |
| `{{PRIOR_FINDINGS}}` | earlier external rounds and their dispositions |
| `{{EXTERNAL_OUTPUT}}` | the external tool's raw report, for evaluation |
| `{{EXTERNAL_FINDINGS}}` | the tool's parsed findings, each opening with the id it was recorded under |
| `{{OPENSPEC_DIR}}`, `{{CHANGE_DIR}}` | the OpenSpec root and the change directory |
| `{{PROPOSAL}}`, `{{DESIGN}}`, `{{TASKS}}`, `{{SPECS}}`, `{{ARTIFACTS}}` | the change's artifact paths |
| `{{REQUIREMENTS}}`, `{{REQUIREMENT_COUNT}}` | the requirement checklist and its size |
| `{{ITERATION}}`, `{{MAX_ITERATIONS}}` | the current iteration and its limit |

The checklist is expanded inline and truncated at `checklist_budget`, saying so
explicitly rather than silently dropping requirements, and each expansion of it
gets that budget in full. The ledger announces a cut the same way and keeps the
most-raised entries, but `ledger_budget` is shared across every expansion in one
prompt rather than applied per copy (see [Standing rejections](#standing-rejections)).
The diff never is.

## Signal contract

rrev decides a phase's outcome from markers it reads back out of the executor's
output. A marker counts only when it is alone on its own line and outside a code
fence, so a model quoting the protocol does not end a loop.

| Marker | Meaning |
| --- | --- |
| `<<<RREV:REVIEW_DONE>>>` | this iteration confirmed nothing critical or major; the phase converged |
| `<<<RREV:EXTERNAL_DONE>>>` | the external review loop reached agreement (read from both calls: the evaluation's marker cannot end a round whose tool output carried neither a finding nor the marker) |
| `<<<RREV:TASK_FAILED>>>` | unrecoverable failure; the pipeline stops and reports the phase |

**Emitting no marker is not success.** rrev reads a missing marker as "work was
done, iterate again" and runs another iteration, up to the phase's limit. Silence
ends a loop as converged in exactly one place: the comprehensive phase's severity
gate, described under [What a phase does](#what-a-phase-does). Everywhere else —
the external loop, the final pass — silence iterates.

### Report lines

Signals decide whether a loop ends; these lines are what rrev reads findings out
of. They are recognised only at the start of a line outside a code fence.

```
FINDING:  <critical|major|minor> | <file>:<line> | <reviewer> | <requirement or -> | <summary>
REJECTED: <file>:<line> | <reviewer> | what was claimed | why it is not a real finding
VALIDATION: <pass|fail> | <the command that was run> | what failed, or -
```

A line naming an entry the log already holds carries that entry's id in its
opening token, `FINDING[R7]:` or `REJECTED[R7]:` — a standing rejection a
reviewer is re-raising, or a finding the log holds as reported, which is how the
external evaluator's disposition lands on the entry the tool's finding was
recorded under. This is the one thing rrev will not work out for itself: file,
line and wording all drift between
iterations while the finding stays the same, so a computed match would merge
distinct findings as readily as it caught real recurrences. An undeclared line
is recorded as a new finding, and an id the log does not hold is recorded as new
with a note. Neither costs the finding: a reviewer's undeclared re-raise costs
only the recurrence count, and an evaluator's undeclared disposition costs the
shared identity — a second entry opens beside the reported one, which stays as
reported.

A reviewer agent writes no report lines of its own: the phase's executor reads
its report and turns each finding into one. So the shipped agents are asked for
the id as its own `Re-raises: R7` field, which the executor carries into the
opening token; an id buried in an agent's prose does not survive that hop. A
replacement agent that drops the field costs recurrences, not findings.

For compatibility a three-field `REJECTED:` line still parses, reading its last
field as the reason and leaving the claim empty. A rejection whose reason is
missing is recorded with one stating that, rather than dropped from the ledger:
withheld from later reviewers, it is the finding most certain to come back.

Only a verified report may reject. The comprehensive, final, evaluation and
finalize calls check each claim against the real code before reporting, so their
`REJECTED:` lines are recorded and become ledger entries. The external review
tool is an unverified second opinion, so a `REJECTED:` line in its output is
discarded rather than logged — accepting one would silence every later
reviewer on a claim nobody checked. The shipped `external_review.txt` tells the
tool to report findings only, and a replacement prompt should say the same.

rrev never runs the validation command itself, so the `VALIDATION` line is the
only record of whether the fixes were validated.

A `-` stands in for a field the finding does not carry. Findings feed the
findings report and the progress log; a rejection with a stated reason becomes a
standing ledger entry that every later review phase and every reviewer agent is
shown, so a dismissed finding does not come back unchanged. A replacement
prompt or an `external_review_command` script that emits neither produces an
empty report, and the progress log records the round as `output not understood`
rather than as a converged one.

## Progress log

Each run appends to `.rrev/progress/progress-<change>.md`, creating the directory and an
ignore rule so logs are never picked up by the pipeline's own commits. A second
run against the same change appends to the same file, preserving history.

Each iteration is a titled section carrying its own timestamp — the entries
inside it carry none — and closes with a one-line summary: findings confirmed by
severity, rejections split into newly raised and re-raised, the validation
outcome, and the commit if one was made. A finding reported without a severity or
a location is counted as unclassified rather than folded into a bucket it does
not belong to.

The log records the change and goal, the base ref, every phase and iteration
boundary, the findings reported, which were confirmed and fixed, which were
rejected and why, the validation outcome each iteration reported, the commits it
produced, whether an external review tool ran and what it returned, and each
loop's termination reason. An external phase that converges in silence and one
whose tool died quietly are recorded differently, because they call for opposite
responses, and the tool's invocation and outcome — how many findings it
reported, that it reported none, or that it failed — are written before the
entries for those findings, so a reader meets the summary before its detail. A
progress directory that cannot be written degrades the run to logging disabled
rather than aborting it. Logging disabled takes the ledger with
it: with nothing recorded there are no standing rejections to expand, so every
prompt is told nothing has been rejected yet and no recurrence is counted. The
review still runs — it just re-argues what it dismissed, as it did before the
ledger existed. The external evaluator is likewise shown the tool's findings
without ids, since none were assigned, and its dispositions are recorded nowhere.

Every finding the log records carries an identifier — `R1`, `R2`, … — assigned
when it is first recorded and shown on its entry. They run in one sequence for
the whole run and continue past the highest id a log already holds, so a second
run never re-issues one. A finding only ever reported, or confirmed without
having been rejected first, has an identifier but no ledger row. Every rejection
gets one, including a rejection that arrived with no reason.

### Failures

When an executor call fails, the log records why, not only that it did: the
phase and iteration, the tool, its classification — usage limit, transient
failure, timeout, cancelled, or plain failure — and its exit status when the
tool exited on its own; a call cut short by a bound or a cancellation, one
that ended on `<<<RREV:TASK_FAILED>>>`, or a refusal the tool printed before
exiting zero, has none and the summary omits it. A failure no tool owns — a
prompt that would not expand — is summarised by its own first line instead,
with the rest of its message indented beneath as the detail. A
diagnostic tail follows. The tail is the tool's standard error, or the last
lines it wrote to standard output when standard error is empty, because a tool
that reports its own error on stdout and exits silently otherwise leaves an
exit status and nothing else. The tail keeps the final twenty non-blank lines
of the last 8 KiB the tool wrote, led by the line that explains the end — the
matched refusal, the bound a timeout expired, or, when there is no exit status
and the call was not cancelled, the error that stopped it. That leading line is
dropped when the tail already holds it, anywhere in the tail and not only at
its end, and the line bound marks its omission above whichever of the two it
cut. When that leading line is the
error a call with no exit status wrapped, it is held to the same bounds as the
tail beneath it. Lines are counted after terminal noise
is flattened: a carriage return a progress bar redraws with becomes a line
break, and escape sequences and other control characters are dropped, so the
log and the console show what the tool said rather than how it painted it. The
console prints the same summary and tail as the phase ends.

The run's closing report names every phase that ended with something
outstanding, and names a phase that failed in the same summary form its failure
record used — `final review did not converge: executor failure: claude: usage
limit (exit 1)`. The command line and the diagnostic tail stay in that record
rather than being repeated on the last line.

A call the executor classified as a transient failure is retried, up to twice
per iteration, and every attempt is recorded the same way, followed by a note
that the iteration is being retried — so a flaky provider is diagnosable
afterwards even when the retry succeeded.

### Standing rejections

A rejection with a stated reason is a durable decision, not an event, so the log
keeps a ledger of them at its end: one row per finding, carrying its id, every
location it was raised at, the claim, the reason it was dismissed, and every
phase and iteration that raised it. A recurrence updates that row instead of
restating its rationale, and the rationale it keeps is the one that first
settled the question rather than whatever the latest re-rejection restated. A
finding later confirmed and fixed keeps its row marked as resolved, so nobody is
told a fixed issue is still standing and nobody finds an entry they saw earlier
silently gone — until it is rejected again, which makes it standing once more.
Reviewers are shown the standing rows only.

The ledger is expanded into every review phase prompt and every reviewer agent,
which is what it is for. In the run that motivated it, roughly half of each late
iteration went to re-arguing a dozen questions the log had already answered —
one of them re-litigated in ten consecutive iterations — while the executor
tracked the recurrences by hand in prose. Reviewers are shown the settled
questions and told to name an entry's id rather than report it afresh. The
finalize step is not shown the ledger: it runs after review has converged and
reports no findings. `ledger_budget` caps what one prompt carries in all, shared
across the prompt's own expansion and the agents it embeds.

A finding the external tool reports keeps its id through evaluation: the
evaluator is shown each parsed finding as `FINDING[R197]:` and told to carry
that id into its own `FINDING[...]:` or `REJECTED[...]:` line, so its
disposition updates the reported entry rather than opening a second one. rrev
still infers nothing — an evaluator that drops the id files a new finding, as
any undeclared report does. An evaluation re-run after a transient failure
answers the same report and the same ids rather than invoking the tool again.
That first disposition counts in the iteration summary as a new rejection
rather than a repeat: the tool's report was a claim, not a judgement, so there
was nothing yet to recur. The rule is general — a rejection is re-raised only
when the entry it names was already judged, rejected with a reason or
confirmed — so any later phase naming a reported-only entry counts the same
way.

The ledger spans the whole run rather than resetting per phase, because
re-litigation crosses phases: a final-phase reviewer will re-raise what the
comprehensive phase rejected.

Each run writes its ledger at the end of its own records, so a log holding
several runs holds one section per run and only the last is live; a later run
continues the identifiers past the highest one the file already holds rather
than re-issuing `R1`. Concurrent runs serialize their appends, so entries
interleave whole; a writer that finds the file grown beneath it appends without
refreshing the ledger rather than rewinding over another run's records. A log
written before this format existed is appended to exactly as it stands — never
rewritten, and never parsed back into a ledger. No ledger is: a second run
against the same change starts with an empty one and suppresses only what it
rejects itself, though its reviewers are still pointed at the whole log.

## Findings report

Report-only mode writes `report_file` (`.rrev/findings.md` by default): one row
per verified finding with its file and line, severity, reporting reviewer, the
requirement it relates to, and a summary.

## Credits

The review pipeline mechanic — parallel reviewer agents, a cross-model external
review loop, a final regression pass, and sentinel signals parsed out of executor
output — originates in [ralphex](https://github.com/umputun/ralphex) by Umputun,
MIT licensed. rrev reimplements it in fresh Go code with no dependency on the
ralphex module; only the shipped prompt and agent text is adapted from ralphex
defaults.

The following default files are derived from ralphex and carry that attribution
in their own headers:

- Reviewer agents: `agents/quality.txt`, `agents/implementation.txt`,
  `agents/testing.txt`, `agents/simplification.txt`, `agents/documentation.txt`
- Phase prompts: `prompts/review_first.txt`, `prompts/review_repeat.txt`,
  `prompts/external_review.txt`, `prompts/external_eval.txt`,
  `prompts/review_final.txt`, `prompts/finalize.txt`

rrev's own `agents/conformance.txt` and `agents/tasks.txt` are not derived from
ralphex — they exist because rrev has a spec to check against.

## License

MIT — see [LICENSE](LICENSE).
