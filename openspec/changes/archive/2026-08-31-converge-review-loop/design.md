# Design

## Context

See proposal.md — Why. The relevant machinery today:

- `pkg/processor/phase/loop.go` `drive` owns the iteration loop. Convergence is only ever `step.Converged`, which is the executor emitting the phase's done signal. `drive` already snapshots the repository head before and after every iteration (`treeState`) for stalemate detection and commit recording — the heads the repeat scope needs are already observed, just not kept.
- `Comprehensive` (`comprehensive.go`) passes `PromptComprehensive` (`review_first`) for every iteration. Findings from this phase are all verified/confirmed (`verified: true`); severity is already parsed off every `FINDING:` line into `Finding.Severity`, and `VALIDATION:` lines into `Validation.Outcome`.
- `{{DIFF_INSTRUCTION}}` is a single run-wide string set once in `cmd/rrev/pipeline.go` from the base ref, and it is the only way both phase prompts and all seven agent definitions learn what diff to review. Changing its per-iteration value re-scopes every agent without touching agent files.

## Goals / Non-Goals

**Goals**
- Comprehensive converges on the first iteration with nothing critical or major, decided twice: by the executor (prompt contract) and by rrev (severity gate on the parsed report).
- Iterations after the first review the fixes, not the whole branch, at both the prompt level and the agent level.

**Non-Goals**
- No change to the external loop's or final phase's termination or scope.
- No change to signal parsing, the ledger, or finding identity.
- No new configuration knobs: the convergence rule and the repeat scope are not tunable. A project that wants different behavior overrides the prompts, which it already can.

## Decisions

### 1. The severity gate lives in `drive`, fed by the phase

`loopSpec` gains an optional `converged func(stepResult) bool` that only `Comprehensive` sets. After the existing `step.Converged` and `SinglePass` cases, `drive` consults it and ends the loop with a distinct reason (e.g. `ReasonMinorOnly`, rendered as `converged: minor findings only`) that the exit-status mapping treats exactly like `ReasonConverged`.

- Why a distinct reason: the log and console must say the phase converged on severity rather than the signal (spec scenario), and a reader debugging a run needs to know which mechanism fired.
- Why after `SinglePass`: report-only runs keep their existing reason; the gate must not relabel them.
- Alternative rejected: putting the gate inside `review`/`Comprehensive` and faking `step.Converged`. That erases the distinction the spec requires and hides the decision from `drive`'s termination logic.

### 2. The gate requires at least one parsed finding

The gate fires only when the report parsed at least one confirmed finding, every one of them an explicit `minor`, and no `VALIDATION:` line reported a failure (matched on the `fail` prefix, so `failed` and `failure` count). It fails closed on anything else: a severity outside the template's vocabulary, or one a shifted report line left holding a file path, is a line rrev could not read, which is the same case as the empty report and not a cleaner one. A report with zero findings and no signal keeps today's behavior (iterate): an empty report is indistinguishable from a reporting failure — an executor that crashed mid-output or never printed its report section must not read as a clean review. The zero-finding case already has a correct path: the prompt tells the executor to emit the done signal.

`stepResult` grows a `Validations` field so the gate can see outcomes; today they only pass through the `writeReport` closure.

### 3. Prompt contract: fix minors, commit, emit the signal

`review_first.txt`'s signal contract changes from "emit only when this iteration confirmed zero findings and you changed nothing" to: emit the done signal when nothing you confirmed is critical or major — after fixing, validating, and committing the minors. The final phase's fresh full review remains the safety net for a stray late major (this is the accepted trade; both logged runs support it: no major ever followed two all-minor iterations, and the one major after a single all-minor iteration would have been caught by the final phase's critical/major pass).

The rrev-side gate (Decisions 1–2) is the backstop for an executor that fixed only minors but withheld the signal out of the old "iterate again to check my fixes" instinct.

### 4. Repeat scope: track the last reviewed head in `drive`, pass it to `run`

The state iteration N has to re-check is what changed since the state iteration N−1 *started* from — iteration N−1 reviewed everything up to its starting head and then produced fixes on top. `drive` already holds that head (`before` at call time); it now remembers it across the call and hands it to the next iteration by widening `loopSpec.run`'s signature with the reviewed base (empty on iteration 1 and for phases that ignore it).

`Comprehensive`'s closure uses it: for n > 1 with a non-empty reviewed base *and* a head that has moved since, it overrides `vars.DiffInstruction` with a two-part instruction — primary scope `git diff <reviewedBase>..HEAD`, full branch `git diff <base>...HEAD` for context — and selects the `review_repeat` prompt. If nothing was committed since the reviewed base, it falls back to the full-branch instruction and still uses the repeat prompt (the prompt reads correctly either way; the scope is whatever `{{DIFF_INSTRUCTION}}` says).

- Why plumb through `run`'s signature instead of `Env` state: `Env` is shared across phases and a mutable "last reviewed head" field would leak comprehensive's bookkeeping into the external and final phases; the loop that observes the heads is the right owner.
- Why the var and not new agent files: `{{DIFF_INSTRUCTION}}` is already the one lever every agent definition pulls; a second variable would require editing all seven agents and every project override of them.

### 5. `review_repeat.txt` is a sibling of `review_first.txt`, not a diff of it

The new prompt repeats the structure (ledger, agents, verify, fix, report, signal contract) but frames the task as "review the fixes since the last reviewed commit; the full branch stays in scope for regressions those fixes could have caused" and drops the first-iteration framing ("read the progress log before anything else" stays — the ledger discipline matters more on repeats, not less). It ships embedded and is overridable like every other prompt; docs tests that pin the prompt inventory and README variable tables extend to it.

## Risks / Trade-offs

- [A late major slips through because the loop stopped at the first all-minor iteration] → The final phase runs a full fresh review restricted to critical/major and iterates until clean; both dogfooding logs show it would have caught the one such major (run 1, iteration 4).
- [Narrowed repeat scope hides a branch-wide issue the first sweep missed] → The full branch diff stays named in the repeat instruction as context and regressions anywhere remain in scope; the external loop and final phase still review the full branch with different eyes.
- [The severity gate converges on a report whose severities the executor mislabeled (majors filed as minor)] → The same executor controls the done signal today, so this adds no new trust; the final phase re-reviews with severity as its explicit brief.
- [An executor emits findings but no VALIDATION line after fixing, and the gate converges without proof of validation] → The prompts already require the validation line; the final phase re-runs validation-sensitive review. Tightening the gate to require an explicit pass would block convergence on prompt-format drift, which is the failure mode this change exists to remove. A line that is present but reports a failure does block, whichever tense it is written in.

## Migration Plan

No stored state or format changes. Old progress logs replay fine; the new reason string only appears in new runs. A project override of `review_first.txt` keeps working — it simply becomes the iteration-1 prompt, and the embedded `review_repeat` covers later iterations until the project overrides that too (README notes this).
