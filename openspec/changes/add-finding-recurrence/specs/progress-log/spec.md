## ADDED Requirements

### Requirement: Finding identity
Every finding recorded in the progress log SHALL carry an identifier that is stable across iterations, so a later iteration re-raising the same finding can be recognized as a recurrence rather than recorded as a new finding. When a reviewer reports a finding that re-raises one the log already holds, the executor SHALL name the existing entry it re-raises; rrev SHALL NOT infer the match itself.

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

### Requirement: Standing rejection ledger
The progress log SHALL maintain a ledger of rejected findings in which each distinct finding appears once, carrying its identifier, location, claim, the rationale for rejecting it, and every phase and iteration in which it was raised. The ledger SHALL span the whole run rather than resetting per phase, since a later phase's reviewers re-raise findings an earlier phase rejected. A recurrence MUST update the existing ledger entry rather than appending a restatement of its rationale.

#### Scenario: Entry created on first rejection
- **WHEN** a finding is rejected for the first time
- **THEN** the ledger gains an entry recording its identifier, location, claim, rationale, and the phase and iteration that raised it

#### Scenario: Recurrence updates the entry
- **WHEN** a rejected finding is raised again in a later iteration
- **THEN** the existing ledger entry gains that phase and iteration, and its rationale is not restated

#### Scenario: Recurrence across phases
- **WHEN** a finding rejected during one phase is raised again during a later phase
- **THEN** it updates the same ledger entry rather than creating a new one, and the entry records both phases

#### Scenario: Ledger is readable as a whole
- **WHEN** a reader opens the log
- **THEN** the ledger appears as one section listing every standing rejection, rather than requiring the reader to reconstruct it from chronological entries

#### Scenario: Confirmed finding leaves the ledger
- **WHEN** a finding previously rejected is later confirmed and fixed
- **THEN** its ledger entry records that it was subsequently confirmed, so a reader is not told a fixed issue is still standing

### Requirement: External tool activity recorded
The progress log SHALL record that an external review tool was invoked and what it returned, including when it returns no findings, so that a phase converging on silence is distinguishable from one whose tool failed or produced nothing usable.

#### Scenario: Invocation recorded
- **WHEN** a phase invokes an external review tool
- **THEN** the log records the invocation and which tool was used

#### Scenario: Tool reports no findings
- **WHEN** the external tool completes and reports nothing
- **THEN** the log records that it returned no findings, rather than leaving the iteration empty

#### Scenario: Tool fails
- **WHEN** the external tool errors, times out, or returns output the executor cannot interpret
- **THEN** the log records the failure and its cause, and the phase's termination reason distinguishes it from convergence

## MODIFIED Requirements

### Requirement: Progress log lifecycle
rrev SHALL maintain one progress log per run, stored under the project's `.rrev/progress/` directory and named so that concurrent runs against different changes do not collide. The directory MUST be created when missing, and its contents MUST be excluded from version control by default. A log already written in an earlier, unstructured format MUST be appended to as it stands; rrev MUST NOT rewrite or retire it.

#### Scenario: Log created
- **WHEN** a run starts and no progress directory exists
- **THEN** rrev creates it and opens a progress log identified by the change under review

#### Scenario: Existing log reused
- **WHEN** a run starts for a change that already has a progress log
- **THEN** rrev appends to it, preserving the prior run's history

#### Scenario: Pre-existing unstructured log
- **WHEN** a run opens a progress log written before this format existed
- **THEN** rrev appends to it without rewriting its earlier content, and does not attempt to populate a ledger from it

#### Scenario: Not version controlled
- **WHEN** the progress directory is created
- **THEN** it contains an ignore rule so progress logs are not committed by the pipeline's own commits

#### Scenario: Directory unwritable
- **WHEN** the progress directory cannot be created or written
- **THEN** rrev reports the failure and continues the review with logging disabled rather than aborting the run

### Requirement: Recorded content
The progress log SHALL record enough for a later reader to reconstruct the run: the change and goal under review, the base ref, each phase and iteration boundary, the findings reported, which were confirmed and fixed, which were rejected and why, validation and commit outcomes, and each loop's termination reason. It SHALL be organized so that a reader can skim it: each iteration MUST form a delimited, titled section carrying a summary of its own outcome, and a timestamp MUST be recorded at each iteration boundary rather than repeated on every entry within it.

#### Scenario: Iteration boundaries recorded
- **WHEN** a phase begins an iteration
- **THEN** the log opens a titled section for it recording the phase, the iteration number, and a timestamp

#### Scenario: Iteration summary recorded
- **WHEN** an iteration ends
- **THEN** its section records how many findings were confirmed and at what severities, how many were rejected split into newly raised and re-raised, the validation outcome, and the commit if one was made

#### Scenario: Finding recorded without a severity or location
- **WHEN** a finding is recorded carrying no severity, or no location
- **THEN** the iteration summary counts it under an explicit unclassified total rather than silently folding it into a severity bucket

#### Scenario: Timestamps not repeated per entry
- **WHEN** several findings are recorded within one iteration
- **THEN** they carry no individual timestamp, the iteration boundary's timestamp covering them

#### Scenario: Rejections recorded with reason
- **WHEN** the executor rejects a reported finding as a false positive
- **THEN** the log records the finding and the reason it was rejected

#### Scenario: Termination recorded
- **WHEN** a loop ends
- **THEN** the log records the terminating condition and the number of iterations run

### Requirement: Log as reviewer context
rrev SHALL make the progress log path available to every phase prompt and MUST instruct reviewers to consult it before reporting, so that findings already rejected with a stated reason are not re-reported unchanged. The standing rejection ledger MUST be expanded into the prompt directly rather than left for the reviewer to reconstruct, and reviewers MUST be instructed to name the ledger entry a finding re-raises.

#### Scenario: Path exposed to prompts
- **WHEN** a phase prompt is assembled
- **THEN** it contains the resolved progress log path and an instruction to read prior iterations before reporting

#### Scenario: Ledger expanded into the prompt
- **WHEN** a phase prompt is assembled and the ledger holds standing rejections
- **THEN** those entries appear in the prompt with their identifiers, locations, claims, and rationales

#### Scenario: Reviewer declares a recurrence
- **WHEN** a reviewer reports a finding that matches a standing ledger entry
- **THEN** it is instructed to name that entry's identifier alongside the finding

#### Scenario: Repeat findings suppressed
- **WHEN** an external review iteration would report a finding that the log records as rejected with a reason
- **THEN** the reviewer is instructed to either accept the recorded reason or state why it is wrong, rather than re-reporting it identically

#### Scenario: Ledger too large for the prompt
- **WHEN** the ledger exceeds the configured prompt budget
- **THEN** rrev includes the most frequently raised entries, states in the prompt that the ledger was truncated, and does not silently drop the remainder
