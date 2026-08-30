## 1. Failure Cause in the Executor

- [x] 1.1 Capture a bounded tail of standard output on a failing call and carry it on `executor.Error` alongside the stderr tail, verified by a test asserting a tool that exits non-zero after writing only to stdout yields an error carrying those lines
- [x] 1.2 Add a renderer that reduces a failure to its classification, exit status, and diagnostic tail — standard error, or the stdout tail when standard error is empty — truncated and marked at the bound, verified by tests over both sources and an oversized tail
- [x] 1.3 Write tests for new functionality and run project tests - must pass before next task

## 2. Failure Record in the Log

- [x] 2.1 Add an `ExecutorFailure` record to the progress log carrying phase, iteration, tool, exit status, classification, and diagnostic tail, rendered as a summary line with indented detail, verified by a test over a rate-limited failure and a plain non-zero exit
- [x] 2.2 Record every phase loop's executor failure through that record instead of a free-text note, verified by a phase test asserting the log names the exit status and the stdout tail for a tool silent on stderr
- [x] 2.3 Render the same classification and tail on the console when a phase fails, verified by an output test
- [x] 2.4 Write tests for new functionality and run project tests - must pass before next task

## 3. External Finding Identity

- [x] 3.1 Return the assigned identifier from `Log.Finding`, verified by a test asserting the returned id matches the one written to the log
- [x] 3.2 Collect the identifiers of the external tool's reported findings in the external round and render them as `FINDING[<id>]:` lines through a new `{{EXTERNAL_FINDINGS}}` template variable, verified by a template test
- [x] 3.3 Instruct the evaluator in `external_eval.txt` to carry each shown identifier into its own report line, and verify the shipped prompt expands with the new variable under both executors
- [x] 3.4 Verify by a phase test that an evaluator rejecting `REJECTED[<id>]:` updates the reported entry so the ledger holds one entry for the finding, and that an evaluator omitting the id records a new finding with the reported entry left as reported
- [x] 3.5 Move the external tool's outcome record to precede the findings it reports, verified by a test asserting the invocation line's position in the log
- [x] 3.6 Write tests for new functionality and run project tests - must pass before next task

## 4. Integration and Documentation

- [x] 4.1 Add an end-to-end test asserting a final-phase executor that exits non-zero with its reason on stdout leaves that reason in the progress log and on the console
- [x] 4.2 Add an end-to-end test asserting an external finding rejected by the evaluator resolves to a single ledger entry across report and disposition
- [x] 4.3 Document the failure record, the diagnostic-tail rule, and `{{EXTERNAL_FINDINGS}}` in the README, and verify every documented variable exists
- [x] 4.4 Verify all requirements from the proposal are implemented, run the full project test suite with the race detector, and run the project linter with all issues fixed
