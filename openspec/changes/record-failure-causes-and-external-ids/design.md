## Context

See `proposal.md` for the two log excerpts that motivate this. Both gaps are in code that already has most of what it needs.

On failure, `executor.Error` carries the tool, args, exit code, and a stderr tail, and `classify` already recognises rate limits, transient failures, timeouts, and cancellation. What is missing is the captured stdout — held in the phase's `stepResult.output`, parsed for findings, then dropped — and any path from these fields into the log other than `err.Error()` pasted into a free-text note.

On identity, `Log.Finding` assigns an id in `track` and returns nothing. The external round records the tool's findings through the review call's `writeReport`, then hands the evaluator the raw `{{EXTERNAL_OUTPUT}}`. The evaluator has no id to declare, so it declares none, and `resolve` correctly opens a new entry for an undeclared finding.

## Goals / Non-Goals

**Goals:**
- A failed phase is diagnosable from the log alone.
- One finding, one id, across the external tool's report and the evaluator's disposition — without rrev matching anything.

**Non-Goals:**
- Retrying a failed final phase, or changing what any failure does to the run's outcome.
- Matching an evaluator's undeclared disposition to the tool's report by position or text. An evaluator that drops the id degrades to today's behaviour, by design.

## Decisions

### The failure record is a structured log entry, not a richer error string

Alternatives: enrich `Error.Error()` so the existing note carries more.

The note path renders `%v` of whatever error surfaced, which today is `claude: <args>: exit status 1` and tomorrow would be that plus a 40-line stdout tail on one line. A dedicated `Log.ExecutorFailure` record renders the fields the way the rest of the log does — one summary line, indented detail — and gives the console renderer the same structure to draw from. The error type still gains the stdout tail as a field, because the classification helpers already read `Error`, and the console failure message is built from the same fields.

### Stdout tail only when stderr is empty

A tool that writes to both usually puts the actual diagnostic on stderr and the review prose on stdout; rendering both doubles the entry for the common case. The tail is captured at the same bound as stderr (`stderrTailBytes`), rendered as its last twenty lines, and marked with an `[earlier lines omitted]` line when the line bound cut it. The classifier's own line — the matched refusal, or the bound a timeout expired — leads the detail unless the tail already holds it, since a refusal that exited zero has no other trace and a matched line may sit above the bound. The bound is a constant, not a setting: nothing in the spec asks for it to be tunable, and a setting nobody adjusts is a maintenance cost.

### Reported ids travel through the prompt, and the evaluator still declares

Alternatives: have rrev pair the evaluator's disposition to the tool's report by position or by file:line; pass ids through a side channel the evaluator does not see.

Positional or textual pairing is the inference the identity requirement forbids, and for the same reason it was forbidden there: the evaluator reorders, merges, and rewords. Instead, `Log.Finding` returns the id, the external round collects `(id, finding)` pairs, and a new `{{EXTERNAL_FINDINGS}}` variable renders each of the tool's findings as its own report line with the id in the opening token — `FINDING[R197]: minor | file:line | external | - | summary`. The evaluator is told to carry that token into its own line. The mechanism is identical to how a reviewer re-raises a ledger entry; the evaluator just sees the ids one round earlier than the ledger would show them.

`{{EXTERNAL_OUTPUT}}` stays: the tool's raw prose carries reasoning the parsed lines lose, and the evaluator is told to read both.

### The record-then-return order in `Log.Finding` is preserved

`Finding` records the entry and returns the id it assigned, so a call site cannot hold an id for something the log does not yet hold. That matters for the degraded no-lock path, where the record is appended without a ledger refresh: the id is still assigned in memory and still resolvable next round.

### The invocation line moves before the review call's report

`ExternalTool(…, "invoked")` is already written before the call; the outcome line is written after the call returns, but the call's own `writeReport` has already written the findings by then. The outcome line moves to precede `writeReport`, and the tool's `writeReport` now runs as soon as the tool returns rather than being held to the round's end, since the evaluator has to be shown the ids it hands back. That inverts the earlier hold-both-reports-together rule, so the round keeps the tool's recorded report across an evaluation re-run after a transient failure: the retry answers the same report and the same ids instead of invoking the tool again and recording its findings a second time. It reads correctly and it also means a run killed during evaluation leaves the outcome on disk.

## Risks / Trade-offs

- **Evaluator ignores the id.** → Degrades to exactly today's double entry; nothing is lost that is not already lost. The end-to-end test scripts both paths.
- **Stdout tail contains a signal marker or report line.** → It is rendered as indented detail under the failure record, which the report parser never reads, and inside the log it is inert; a later prompt expands the ledger, not the raw log.
- **Bounded tail cuts the useful line.** → The tail is the *last* lines, which is where a crashing tool puts its reason; a tool that reports its error early and then rambles is not a case seen in practice.
- **`Finding` gaining a return value touches every caller.** → One call site, in `pkg/processor/phase`; the compiler finds it.
