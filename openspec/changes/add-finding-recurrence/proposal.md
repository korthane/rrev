## Why

A completed run exposed a hole shared by rrev's log and its console: **a finding has no identity across iterations**, so neither a reader nor a reviewer can tell a new problem from the same one raised for the tenth time.

The evidence is `mnemocode`'s `add-age-key-format` review — 12 iterations across three phases, 68KB of log:

- 140 rejected findings against 64 confirmed. Two thirds of the log is the executor explaining why it is not acting.
- Self-reported recurrence ran at roughly half of every iteration from iteration 4 onward, peaking at 64%, in phrases the model typed by hand: *"rejected in ten prior iterations"*, *"rejected in iterations 1, 4, 5, 7-10"*, *"sixth re-litigation of the single-character echo"*. It is maintaining a recurrence index inside prose because rrev gives it nowhere else to put one.
- The re-litigation crossed phases. The final phase, with a different and narrower agent set, re-raised findings that the comprehensive phase had already rejected ten times.

Critically, that re-litigation was **not** a sign the loop had stalled. Majors were confirmed in 9 of 10 comprehensive iterations, including the last one before the iteration limit. The most re-litigious iteration — 64% re-raises — was also the one that found the most majors. Roughly half of each iteration's effort went to re-arguing settled questions while the other half kept finding real defects. The problem to solve is the waste, not the loop's length: reviewers need to be told what has already been settled, so their iterations are spent on new ground.

`Finding` already carries the fields to support this, and its own doc comment says it is "recorded with enough context to tell whether a later iteration is re-reporting it." Nothing ever compares two of them.

The same run made two other gaps concrete. The external phase ran for 55 seconds and wrote two lines — an iteration boundary and `end: reason=converged` — with no record that the external tool was invoked or what it returned, leaving a silent failure indistinguishable from a clean pass. And on the console, seven parallel reviewers all write through one `Stream("comprehensive")` writer, so during the longest phase of a run the phase label is a constant, while tool activity renders as a bare `· tool: Bash` with no command.

## What Changes

- **Findings gain identity.** Every reported finding gets a stable id. When a finding re-raises one the log already records, the executor names the entry it re-raises. Recurrence becomes data rather than prose.
- **A standing-rejection ledger, spanning the whole run.** A rejection with a stated reason is a durable decision, not an event. The log gains a ledger listing each rejected finding once — its location, claim, rationale, and every phase and iteration that raised it — instead of restating a 200-character rationale on each recurrence. Because re-litigation crosses phases, the ledger is per-run, not per-phase.
- **The ledger is what reviewers read first.** It is expanded into phase prompts directly, rather than the current instruction to infer the pattern from a chronological wall of entries. This is the change's primary lever: suppressing re-raises is what reclaims the wasted half of each iteration.
- **The log becomes structured and skimmable.** Iteration blocks with markdown headers and a scoreboard — confirmed by severity, rejected split into newly raised and re-raised, validation, commit. One timestamp per iteration boundary rather than one per line.
- **External tool activity is recorded.** The log records that an external review tool was invoked and what it returned, including when it returns nothing, so a phase that converges on silence is distinguishable from one that failed quietly.
- **Console output is attributed at the right granularity.** Executor activity names the reviewer agent that produced it, not just the phase, and a tool call shows its distinguishing argument — the command for a shell call, the path for a file read or write, the agent name for a sub-agent, the pattern for a search. Tool *outcome* is shown (exit status, and failure detail); tool output content is not, since a diff or a test run would flood the display.
- **No migration.** The new format applies to logs created after the upgrade. An existing flat-format log keeps being appended to as-is; rrev does not rewrite or retire it.

Non-goals: structured JSON findings for machine consumption, a findings report format change, changing what the reviewer agents look for, and any change to the phases themselves.

**Deliberately excluded: loop termination.** An earlier draft added repeat-rate stalemate detection — terminate when re-raises dominate an iteration. The completed run refutes it. A 50% threshold over two consecutive iterations fires at iteration 5, forfeiting the eight majors found in iterations 6 through 10. High re-litigation and high productivity coexisted throughout, so the repeat rate does not separate a stalled loop from a working one. Whether some other termination signal is warranted is worth revisiting once suppression has had a chance to reclaim that wasted effort — with data from a run that has the ledger.

## Capabilities

### New Capabilities
<!-- None. Both affected capabilities already exist. -->

### Modified Capabilities
- `progress-log`: gains finding identity and the run-wide standing-rejection ledger; recorded content becomes structured iteration blocks with a scoreboard and gains a record of external tool invocations; the reviewer is handed the ledger rather than told to infer from history; lifecycle gains the rule that a pre-existing flat log is appended to unchanged.
- `agent-execution`: live progress reporting attributes output to the reviewer agent within a phase and shows each tool call's distinguishing argument and outcome.

## Impact

- `pkg/progress` — finding identity, the ledger, and a renderer that emits iteration blocks instead of flat lines. The existing `Finding` struct already carries the fields; `render()` is what discards them.
- `pkg/status` — per-agent attribution and per-tool argument rendering. Requires the claude and codex stream parsers to surface the tool name, its arguments, and the owning sub-agent, which they currently drop.
- `pkg/processor/phase` — recording the external tool's invocation and result.
- Default prompts and reviewer agents — reviewers must be told to declare which ledger entry a finding re-raises, and the ledger must be expanded into the prompt.
- **No change to run behavior.** Every phase still terminates exactly as it does today. This change alters what rrev records and displays, and what reviewers are told, not when a loop stops.
- **Executor-dependent**: per-agent attribution and tool arguments are only as good as what each CLI's output format exposes. Claude's `stream-json` carries structured tool-use blocks; codex's format does not line up the same way, so the requirement is written against what each exposes rather than assuming parity.
