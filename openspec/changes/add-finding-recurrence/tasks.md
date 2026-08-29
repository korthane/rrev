## 1. Finding Identity

- [x] 1.1 Add a stable identifier to `Finding` and assign one when a finding is first recorded, verified by a test asserting the identifier appears alongside the finding in the written log
- [x] 1.2 Accept a declared re-raise that names an existing identifier and record it against that entry rather than creating a new one, verified by a test over a two-iteration sequence
- [x] 1.3 Record an undeclared finding as new without attempting to match it against prior entries, verified by a test asserting no inference occurs
- [x] 1.4 Record a finding naming an unknown identifier as new, noting the unresolved reference and continuing, verified by a test asserting the iteration is not failed
- [x] 1.5 Write tests for new functionality and run project tests - must pass before next task

## 2. Standing Rejection Ledger

- [x] 2.1 Derive ledger entries from recorded findings, each carrying identifier, location, claim, rejection rationale, and the iterations that raised it
- [x] 2.2 Update an existing entry on recurrence by adding the iteration number without restating the rationale, verified by a test asserting the rationale appears exactly once across three raises
- [x] 2.3 Record every raised location on an entry, not only the first, so an entry spanning unrelated locations is visible
- [x] 2.4 Span the ledger across the whole run so a finding rejected in one phase and re-raised in a later phase updates the same entry, recording both phases, verified by a test over a comprehensive-then-final sequence
- [x] 2.5 Mark an entry as subsequently confirmed when a previously rejected finding is later confirmed and fixed, verified by a test asserting a fixed issue is not presented as still standing
- [x] 2.6 Render the ledger as one section of the log, re-projected from the recorded findings on each write rather than mutated as the source of truth
- [x] 2.7 Perform the ledger re-render as a read-modify-write inside the existing file lock, verified by a concurrent-writer test asserting no entry is lost or duplicated
- [x] 2.8 Write tests for new functionality and run project tests - must pass before next task

## 3. Log Structure

- [x] 3.1 Open a titled, delimited section per iteration recording phase, iteration number, and one timestamp, replacing the per-entry timestamp
- [x] 3.2 Record findings within an iteration section without individual timestamps, verified by a test asserting a multi-finding iteration carries exactly one timestamp
- [x] 3.3 Write an iteration summary on close: confirmed counts by severity, rejected split into newly raised and re-raised, validation outcome, and the commit if one was made
- [x] 3.4 Count a finding carrying no severity or no location under an explicit unclassified total in the iteration summary rather than folding it into a severity bucket, verified by a test over a degenerate entry
- [x] 3.5 Record an external tool's invocation and what it returned, including a no-findings return and a failure with its cause, verified by tests asserting a silent pass and a quiet failure are distinguishable in the log
- [x] 3.6 Append to a pre-existing unstructured log without rewriting its earlier content and without populating a ledger from it, verified by a test over a fixture flat-format log
- [x] 3.7 Write tests for new functionality and run project tests - must pass before next task

## 4. Reviewer Context

- [x] 4.1 Expand the standing ledger into phase prompts with identifiers, locations, claims, and rationales
- [x] 4.2 Truncate the ledger at the configured prompt budget by keeping the most frequently raised entries and stating in the prompt that truncation occurred, verified by a test over an oversized ledger
- [x] 4.3 Instruct reviewers in the default prompts to name the ledger entry a finding re-raises, and verify every shipped prompt carrying the ledger also carries that instruction
- [x] 4.4 Update the default reviewer agents so a re-raise is reported with its identifier rather than as prose, and verify each shipped agent definition still expands cleanly for both executors
- [x] 4.5 Write tests for new functionality and run project tests - must pass before next task

## 5. Console Attribution

- [ ] 5.1 Surface the tool name, its arguments, and the owning sub-agent from the claude stream parser, which currently drops them
- [ ] 5.2 Surface whatever equivalent the codex output format exposes, without assuming parity with claude
- [ ] 5.3 Attribute a streamed line to its reviewer agent as well as its phase where the format identifies the agent, falling back to the phase alone where it does not, verified by tests over both cases
- [ ] 5.4 Render a tool call's distinguishing argument - command for a shell call, path for a file read or write, agent name for a sub-agent launch, pattern for a search
- [ ] 5.5 Bound a tool argument to its first line truncated to the configured width, marked as truncated, verified by a test over a multi-line heredoc command
- [ ] 5.6 Render a tool call's outcome and its failure detail without rendering the tool's output content, verified by a test asserting a large output is not echoed
- [ ] 5.7 Extend debug output to include full tool arguments and output, verified by a test asserting they appear only under debug
- [ ] 5.8 Write tests for new functionality and run project tests - must pass before next task

## 6. Integration and Documentation

- [ ] 6.1 Add an end-to-end test over a scripted multi-iteration run asserting a declared re-raise increments its ledger entry rather than creating a new one
- [ ] 6.2 Add an end-to-end test asserting a finding rejected in the comprehensive phase and re-raised in the final phase resolves to one ledger entry recording both phases
- [ ] 6.3 Add an end-to-end test asserting the console attributes concurrent reviewers separately and renders tool arguments bounded
- [ ] 6.4 Document the ledger, the identifier contract reviewers must honour, the external-activity records, and the fact that pre-existing logs keep their format, and verify every documented setting exists
- [ ] 6.5 Verify all requirements from the proposal are implemented, run the full project test suite, and run the project linter with all issues fixed
