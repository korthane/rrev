## ADDED Requirements

### Requirement: Executor failure recorded with its cause
When an executor call in any phase fails, the progress log SHALL record the failure with enough to diagnose it without re-running: the phase and iteration, the tool, its exit status, the failure's classification, and a bounded diagnostic tail. The tail MUST be the tool's standard error, or the last lines it wrote to standard output when standard error is empty, since a tool that reports its own error on standard output otherwise leaves no trace. The same detail MUST reach the console.

#### Scenario: Tool exits non-zero with diagnostics on standard error
- **WHEN** an executor call exits non-zero and wrote to standard error
- **THEN** the log records the phase, iteration, tool, exit status, and the standard-error tail

#### Scenario: Tool exits non-zero silently on standard error
- **WHEN** an executor call exits non-zero having written nothing to standard error
- **THEN** the log records the last lines the tool wrote to standard output as the diagnostic tail, rather than the exit status alone

#### Scenario: Failure classified
- **WHEN** the failure is recognised as a usage limit, a transient failure, a timeout, or a user cancellation
- **THEN** the log names that classification alongside the diagnostic tail

#### Scenario: Tail bounded
- **WHEN** the diagnostic output exceeds the configured bound
- **THEN** the log records its tail, truncated and marked as such, so a failure cannot flood the log

#### Scenario: Console matches the log
- **WHEN** a failure is recorded
- **THEN** the console shows the same classification and tail, so a user watching the run learns the cause without opening the log

## MODIFIED Requirements

### Requirement: Finding identity
Every finding recorded in the progress log SHALL carry an identifier that is stable across iterations, so a later iteration re-raising the same finding can be recognized as a recurrence rather than recorded as a new finding. When a reviewer reports a finding that re-raises one the log already holds, the executor SHALL name the existing entry it re-raises; rrev SHALL NOT infer the match itself. A finding reported by the external review tool and then evaluated by the primary executor SHALL keep the identifier it was assigned when reported: rrev SHALL show the evaluator that identifier, and the evaluator's disposition SHALL update the reported entry rather than open a second one.

#### Scenario: Identifier assigned
- **WHEN** a finding is recorded for the first time
- **THEN** the log assigns it an identifier and records that identifier alongside the finding

#### Scenario: Recurrence declared
- **WHEN** the executor reports a finding as re-raising an entry already in the log
- **THEN** the log records it against that existing entry's identifier rather than creating a new one

#### Scenario: Undeclared recurrence
- **WHEN** the executor reports a finding without naming an existing entry
- **THEN** it is recorded as a new finding, and rrev does not attempt to match it against prior entries

#### Scenario: Unknown identifier named
- **WHEN** the executor names an identifier that the log does not hold
- **THEN** rrev records the finding as new, notes the unresolved reference, and continues rather than failing the iteration

#### Scenario: Evaluated external finding keeps its identifier
- **WHEN** the external tool reports a finding and the primary executor then confirms or rejects it, naming the identifier it was shown
- **THEN** the log records the disposition against the reported entry, and the ledger holds one entry for the finding

#### Scenario: Evaluator omits the identifier
- **WHEN** the primary executor's disposition names no identifier
- **THEN** it is recorded as a new finding, exactly as any undeclared report is, and the reported entry stays as reported

### Requirement: External tool activity recorded
The progress log SHALL record that an external review tool was invoked and what it returned, including when it returns no findings, so that a phase converging on silence is distinguishable from one whose tool failed or produced nothing usable. The record of the invocation and its outcome MUST precede the findings it reports, so a reader meets the summary before its detail.

#### Scenario: Invocation recorded
- **WHEN** a phase invokes an external review tool
- **THEN** the log records the invocation and which tool was used

#### Scenario: Tool reports no findings
- **WHEN** the external tool completes and reports nothing
- **THEN** the log records that it returned no findings, rather than leaving the iteration empty

#### Scenario: Tool fails
- **WHEN** the external tool errors, times out, or returns output the executor cannot interpret
- **THEN** the log records the failure and its cause, and the phase's termination reason distinguishes it from convergence

#### Scenario: Invocation precedes its findings
- **WHEN** the external tool reports findings
- **THEN** the log records the invocation and its outcome before the entries for those findings
