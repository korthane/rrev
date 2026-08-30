## Why

The first run of the new progress log against a real change exposed two gaps, both in `progress-log`, both visible in `.rrev/progress/progress-add-finding-recurrence.md`.

**The final phase failed and the log cannot say why.** After ten iterations of fixes, the regression pass — the one phase whose job is to catch what those fixes broke — ended with:

```
**final ended:** executor failure after 1 iteration(s)
- note: final review error: claude: claude --print --output-format stream-json ... : exit status 1
```

That is the command line and an exit code. The executor keeps a stderr tail for exactly this case, but claude wrote nothing to stderr; whatever it did say went to stdout, which rrev captured, parsed for findings, and then discarded. The exit code is on the error but not rendered, and the rate-limit and transient-failure classification the executor already performs never reaches the log. A rate limit, a context overflow after ten iterations, and a crash all look identical. The `progress-log` spec requires this distinction for an *external* tool ("Tool fails" records the failure and its cause) and is silent about the primary executor, which is the one that failed.

**The external phase mints two ids for one finding.** Codex reported a finding, recorded as `R197`; claude's evaluation rejected it, recorded as `R198`. Same finding, two entries, and the ledger permanently holds `R198` beside an orphan `R197`. A reviewer predicted this in iteration 1 (`R22`) and the executor dismissed it as self-correcting from the second round — but the loop converged on round two, which is the common case, so it never corrected. The root cause is mechanical: `Log.Finding` assigns an id and returns nothing, so the external round cannot tell the evaluator which id each reported finding received, and the evaluator is handed the tool's raw output with no id to declare.

## What Changes

- **Executor failures are recorded with their cause.** When any phase's executor call fails, the log records the phase and iteration, the tool, the exit status, the failure's classification (rate limit, transient, timeout, cancelled, or plain failure), and a bounded diagnostic tail — standard error, or the last lines the tool wrote to standard output when standard error is empty. The same detail reaches the console.
- **The external tool's findings keep their identity through evaluation.** Recording a reported finding returns its id, the evaluator is shown each of the tool's findings under the id it was assigned, and is instructed to carry that id into its own report line. A confirmed or rejected disposition then updates the reported entry rather than opening a second one. This stays inside the declared-not-inferred rule: the executor still writes the id; rrev only tells it which one.
- **The external tool's report is recorded as soon as the tool returns.** Its findings are held to the end of the iteration today, so the evaluator cannot be shown the ids they were recorded under, and an evaluation retried after a transient failure re-invokes the tool and records its findings a second time. Recording them at the call keeps the existing invocation-then-findings order and lets the retry answer the same report.

Non-goals: changing loop termination, retrying a failed final phase, or any change to how the comprehensive phase records findings.

## Capabilities

### New Capabilities
<!-- None. -->

### Modified Capabilities
- `progress-log`: gains a requirement that executor failures are recorded with their cause; *Finding identity* gains the rule that an evaluated external finding keeps its reported identifier; *External tool activity recorded* requires the invocation record to precede the findings it reports.
- `agent-execution`: *Executor contract*'s non-zero-exit scenario names what the diagnostic output consists of — standard error, or the standard-output tail when standard error is empty — since that is what the log will render.

## Impact

- `pkg/executor` — the failure error carries the captured output tail alongside stderr; a helper renders a failure's cause in one bounded form for both the log and the console.
- `pkg/progress` — `Finding` returns the assigned id; a new `ExecutorFailure` record.
- `pkg/status` — the closing report names a failed phase in the same summary form, instead of the error's full text.
- `pkg/processor/phase` — the loop records a failure through the log rather than as a free-text note; the external round threads reported ids into the evaluation prompt.
- Default `external_eval.txt` prompt and a new template variable for the id-annotated report.
- No behaviour change to when any loop stops. A run that failed before still fails; it now says why.
