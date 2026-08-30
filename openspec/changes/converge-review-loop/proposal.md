# Converge the review loop

## Why

Two dogfooding runs (`.rrev/progress/progress-record-failure-causes-and-external-ids.md`) each burned a full 5-hour claude session and died at comprehensive iteration 8 without converging. Majors dried up by iteration 4–5 in both runs; every later iteration re-ran the full seven-agent branch review in 30–48 minutes and mined bottomless minor wells (surviving test mutations, documentation phrasing). The review-done bar — zero findings across seven agents — is practically unreachable, so the loop always runs to its iteration limit or dies trying.

## What Changes

- The comprehensive phase converges on the first iteration that confirms nothing critical or major: the executor fixes the confirmed minors, commits, and emits the review-done signal. Today the signal is allowed only for a zero-finding, zero-change iteration.
- rrev enforces the same rule itself as a backstop: when an iteration's parsed report confirms no critical or major finding and validation did not fail, the phase ends as converged even if the executor never emitted the signal.
- Comprehensive iterations after the first use a new `review_repeat` prompt whose primary scope is the diff since the last reviewed commit — the fixes — with the full branch diff kept for context and regressions anywhere in it still in scope. When no commit landed since the last iteration, the repeat iteration falls back to the full branch scope.
- The external loop's termination is untouched: its convergence already keys on the external tool reporting nothing, and cross-model disagreement is the phase's point. The final phase already ignores minors.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `review-pipeline`: the "Comprehensive review phase" requirement's convergence contract changes from zero-findings to nothing-critical-or-major, with rrev-side enforcement; a new "Repeat iteration scope" requirement narrows what iterations after the first primarily review.

## Impact

- `pkg/processor/phase`: per-iteration prompt selection (first vs repeat), the severity gate on the parsed report, and tracking the last reviewed commit for the repeat scope.
- `pkg/config/defaults/prompts/review_first.txt`: the signal contract section changes; a new `review_repeat.txt` default prompt ships and becomes overridable like every other prompt.
- `pkg/config`: the iteration-aware `{{DIFF_INSTRUCTION}}` value; docs tests that pin the prompt inventory.
- `README.md`: the pipeline description of when comprehensive converges and what repeat iterations review.
