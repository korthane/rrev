# Tasks

## 1. Severity gate

- [x] 1.1 Add `Validations` to `stepResult` in `pkg/processor/phase/loop.go` and populate it in `review`, so the gate can read validation outcomes without waiting for `writeReport`; verify existing phase tests still pass (`go test ./pkg/processor/phase/`).
- [x] 1.2 Add an optional `converged func(stepResult) bool` to `loopSpec`, consulted by `drive` after the `step.Converged` and `SinglePass` cases, ending the loop with a new reason constant rendered as `converged: minor findings only`; map that reason to the same exit status as `ReasonConverged` via `phase.Result.OK()`, which `pkg/status` already keys off. Verify with a unit test that the reason reaches `LoopEnd` and the console line.
- [x] 1.3 Implement the gate in `Comprehensive`: fires only when the parsed report has at least one confirmed finding, every one of them an explicit `minor`, and no validation reported a failure. Unit-test all five spec scenarios: minor-only converges, at-least-one-major iterates, failed validation iterates in any spelling, a severity rrev cannot read iterates, empty report iterates.
- [x] 1.4 Verify the gate does not relabel report-only runs: a single-pass run with minor-only findings still ends with the single-pass reason (unit test).

## 2. Prompt contract

- [x] 2.1 Rewrite the signal contract in `pkg/config/defaults/prompts/review_first.txt`: emit the review-done signal when nothing confirmed is critical or major, after fixing, validating, and committing any confirmed minors; keep the failed-signal and missing-marker semantics. Verify `pkg/config` prompt tests pin the new wording.
- [x] 2.2 Update `README.md`'s pipeline section: when comprehensive converges, the severity backstop, and the new reason string. Verify the docs tests that cross-check README content pass.

## 3. Repeat iteration scope

- [x] 3.1 Widen `loopSpec.run` with the reviewed base head, tracked by `drive` from its existing snapshots (empty on iteration 1); external and final phases ignore it. Verify with a unit test that iteration N receives the head that iteration N−1 started from.
- [x] 3.2 Write `pkg/config/defaults/prompts/review_repeat.txt` per design decision 5, add a `PromptComprehensiveRepeat` constant, and select it in `Comprehensive` for iterations after the first. Verify prompt inventory/docs tests cover the new file and README documents it as overridable.
- [x] 3.3 In `Comprehensive`'s closure, override `vars.DiffInstruction` for repeat iterations: primary scope `git diff <reviewedBase>..HEAD` plus full-branch context when the head moved; full-branch instruction when it did not. Unit-test both branches and that iteration 1 keeps the run-wide instruction.
- [x] 3.4 Confirm agent definitions need no changes: grep that `{{DIFF_INSTRUCTION}}` is the only diff lever in `pkg/config/defaults/agents/`, and add a config test asserting the repeat prompt references the same agents as `review_first` unless the design says otherwise.

## 4. End-to-end verification

- [x] 4.1 e2e test (`cmd/rrev/e2e_test.go`): a scripted executor whose iteration confirms one minor finding and emits no signal — the run converges after that iteration with the minor-only reason in the progress log and exit status success.
- [x] 4.2 e2e test: iteration 2 of comprehensive receives the `review_repeat` prompt with a scoped diff instruction naming the commit iteration 1 started from; iteration 1 received the full-branch instruction.
- [x] 4.3 e2e test: an iteration confirming a major finding does not converge, and a later minor-only iteration with the done signal ends the phase as converged (signal path unchanged).
- [x] 4.4 Run `make test` and `make lint` clean; `openspec validate converge-review-loop --strict` passes.
