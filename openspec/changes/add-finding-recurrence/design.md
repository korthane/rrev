## Context

See `proposal.md` for motivation and the run that produced the evidence.

Three facts about the existing code shape this. `Finding` already carries reviewer, severity, file, line, requirement, and summary, and its doc comment states it is recorded "with enough context to tell whether a later iteration is re-reporting it" — the data model anticipated this and no consumer was written. `render()` flattens that struct to a `key=value` line and appends it, so the log is a write-only stream: nothing reads it back. And `Printer.Stream(name)` takes a phase name, so every concurrent reviewer in a phase shares one writer by construction.

The console and log problems have the same root — attribution at too coarse a grain — but they live behind different constraints. The log's grain is set by what rrev itself records. The console's grain is set by what two external CLIs choose to emit, which rrev does not control.

## Goals / Non-Goals

**Goals:**
- A finding is a thing with identity that persists across the run, and everything downstream — the ledger and the prompts built from it — reads from that one notion.
- The log is readable by a human skimming mid-run and by a model reading it as context, without those two needs pulling the format apart.
- Suppression is the lever: reviewers that can see what has been settled stop spending iterations re-arguing it.

**Non-Goals:**
- Parsing or migrating existing flat-format logs. New format, new logs.
- Making the log machine-readable as a data interchange format. It is a document that a model and a person both read; JSON would serve neither better.
- Parity between claude and codex in what the console can show. Whatever each format exposes is what gets shown.
- Changing when a loop terminates. See "Termination was considered and rejected" below.

## Decisions

### Identity is declared, not inferred

Alternatives: rrev computes a fingerprint over normalized file path plus summary text; rrev proposes a candidate and the executor confirms.

Computed matching fails on this data. Line numbers shift as fixes land — the same bech32 echo claim appears at `:129,151` in iteration 1 and `:136,163` in iteration 8. The claim text is reworded every round because a different reviewer agent raises it. A fingerprint tight enough to avoid false merges would miss most real recurrences, and a loose one would merge distinct findings in the same file, which is worse: a merged entry hides a real issue behind a standing rejection.

The executor already does this correctly in prose, 47 times in one run, including across rewordings and line shifts. It has the code, the ledger, and the judgment. Asking it to name an id is asking for something it is already producing.

The cost is an undeclared re-raise being recorded as new, which understates an entry's raise count and leaves that recurrence unsuppressed for a round. That is a cost the run already pays today on every recurrence, so the failure mode is "no worse than the status quo" rather than a new hazard.

### The ledger is a section of the log, not a second file

Alternatives: a sidecar ledger file; a human-facing log plus a separate reviewer-context file.

The two audiences want the ledger for the same reason. A person skimming wants to see "these twelve things keep coming back"; a reviewer needs to see them so it stops raising them. Splitting the artifact would mean writing the same content twice and letting the copies drift. One document with the ledger at a fixed position serves both, and it keeps the `progress-log` capability's existing promise that the log *is* the reviewer's context.

This does mean the log is no longer purely append-only — a recurrence updates an existing ledger entry in place. That interacts with the file lock: the ledger section is rewritten under the lock, rather than appended. The lock already exists and already bounds its wait; the change is that a writer now truncates to the offset where it last wrote the ledger before appending, so it never rewinds over a byte it did not write.

### The ledger is a rendered projection, not the source of truth

Ledger entries are derived from the recorded findings as they are made. The alternative — mutating a ledger section in the file as the authoritative store — makes the log its own database and every write a parse. Instead the iteration entries stay the record, the run holds the entries derived from them, and the ledger section is re-rendered on each write. Nothing reads the log back except a scan for the highest identifier it holds, so a hand-edited ledger section is overwritten rather than trusted; a hand-edited record is left alone.

The cost is rewriting the ledger section on every finding. Ledgers are small (twelve entries in the run that motivated this) and writes are already serialized by the lock, so this is cheap in practice, but it does mean the write path is O(ledger) rather than O(1).

### Termination was considered and rejected

An earlier draft of this change added repeat-rate stalemate detection: terminate a loop when re-raises of standing rejections dominate consecutive iterations, on the theory that a loop re-litigating settled questions has stopped making progress. The completed evidence run refutes the theory.

```
iteration    1   2   3   4   5   6   7   8   9  10
majors       2   2   0   1   4   2   2   1   2   1
repeat %     0  19  14  50  64  27  28  53  50  50
```

A 50% threshold sustained over two iterations fires at iteration 5 — the iteration that confirmed four majors, more than any other in the run — forfeiting the eight majors found in iterations 6 through 10. Majors were confirmed in 9 of 10 iterations, including the last before the limit. High re-litigation and high productivity were simultaneous throughout, not alternatives.

So the repeat rate does not separate a stalled loop from a working one, and no severity-based variant fares better on this data: no iteration combined a high repeat rate with an absence of majors. The loop was not running too long. It was spending half of each iteration on questions it had already answered.

That reframes the fix as suppression rather than termination. The ledger's job is to stop the re-raises from being made, which returns that half of each iteration to new ground. Whether any termination signal is then warranted is a question worth asking again — but with data from a run that has the ledger, since suppression changes the very distribution a brake would key on. Designing the brake now would tune it against a distribution this change is about to invalidate.

### Console attribution is best-effort by format

Claude's `stream-json` carries tool-use blocks with structured input and enough context to attribute a line to a sub-agent. Codex's format does not line up the same way. Rather than specify a normalized subset that throws away what claude offers, or a contract codex cannot meet, the requirement is conditional: attribute to the agent where the format identifies it, fall back to the phase where it does not. The failure mode is a less informative display, never a wrong attribution.

Tool arguments get a first-line-only, width-bounded rendering. Heredocs, multi-line commands, and long prompts are common in this codebase's own tasks; an unbounded argument would break the display the same way dumping output would.

## Risks / Trade-offs

- **The executor stops declaring ids.** The whole mechanism rests on prompt compliance. → Undeclared re-raises degrade to "recorded as new", so the log stays correct and only the recurrence counts understate. Nothing terminates on those counts, so a compliance regression costs accuracy in the ledger, never a wrongly killed run. Worth an assertion in the end-to-end test that a scripted re-raise is counted.
- **Wrong id declared, merging two distinct findings.** A real issue disappears into a standing rejection. → Worse than a miss, so the ledger entry records every raised location rather than only the first; a merged entry that spans unrelated locations is visible. Unknown ids are recorded as new and noted rather than silently dropped.
- **Ledger crowds out the prompt.** A long run's ledger competes with the requirement checklist for prompt budget. → Truncate by raise-count, keeping the most-repeated, and say so in the prompt, mirroring how the requirement checklist already handles its budget.
- **Read-modify-write under lock.** Re-rendering the ledger means readers and writers now contend on a file that was append-only. → The bounded lock wait already exists and already degrades to "report contention and continue"; this widens the window rather than introducing a new failure mode.
- **Format churn for anyone parsing the old log.** → Nothing in rrev parses it, and the change is additive for existing files, which keep their format.

## Open Questions

- Should the ledger also record confirmed-then-fixed findings that later regress? The evidence run has a case — the word-redaction added in iteration 1 was unpinned and re-found in iteration 2 — which is a different phenomenon from a standing rejection and might deserve its own treatment. Deferring: it does not change the identity mechanism or the ledger's shape, only what else might eventually be rendered from the same records.
- Does suppression actually reclaim the wasted effort, and does anything then warrant a brake? Answerable only by running with the ledger and comparing the recurrence distribution against this run's. Deferring is the point rather than a concession: any termination rule designed now would be tuned against a distribution this change is about to change.
