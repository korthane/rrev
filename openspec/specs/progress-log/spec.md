# progress-log Specification

## Purpose
Keeps a per-run journal of what each phase and iteration found, fixed, and rejected, so that a fresh executor session or an independent review tool can pick up the history instead of rediscovering and re-arguing the same findings.

## Requirements

### Requirement: Progress log lifecycle
rrev SHALL maintain one progress log per run, stored under the project's `.rrev/progress/` directory and named so that concurrent runs against different changes do not collide. The directory MUST be created when missing, and its contents MUST be excluded from version control by default.

#### Scenario: Log created
- **WHEN** a run starts and no progress directory exists
- **THEN** rrev creates it and opens a progress log identified by the change under review

#### Scenario: Existing log reused
- **WHEN** a run starts for a change that already has a progress log
- **THEN** rrev appends to it, preserving the prior run's history

#### Scenario: Not version controlled
- **WHEN** the progress directory is created
- **THEN** it contains an ignore rule so progress logs are not committed by the pipeline's own commits

#### Scenario: Directory unwritable
- **WHEN** the progress directory cannot be created or written
- **THEN** rrev reports the failure and continues the review with logging disabled rather than aborting the run

### Requirement: Recorded content
The progress log SHALL record enough for a later reader to reconstruct the run: the change and goal under review, the base ref, each phase and iteration boundary, the findings reported, which were confirmed and fixed, which were rejected and why, validation and commit outcomes, and each loop's termination reason.

#### Scenario: Iteration boundaries recorded
- **WHEN** a phase begins an iteration
- **THEN** the log records the phase, the iteration number, and a timestamp

#### Scenario: Rejections recorded with reason
- **WHEN** the executor rejects a reported finding as a false positive
- **THEN** the log records the finding and the reason it was rejected

#### Scenario: Termination recorded
- **WHEN** a loop ends
- **THEN** the log records the terminating condition and the number of iterations run

### Requirement: Log as reviewer context
rrev SHALL make the progress log path available to every phase prompt and MUST instruct reviewers to consult it before reporting, so that findings already rejected with a stated reason are not re-reported unchanged.

#### Scenario: Path exposed to prompts
- **WHEN** a phase prompt is assembled
- **THEN** it contains the resolved progress log path and an instruction to read prior iterations before reporting

#### Scenario: Repeat findings suppressed
- **WHEN** an external review iteration would report a finding that the log records as rejected with a reason
- **THEN** the reviewer is instructed to either accept the recorded reason or state why it is wrong, rather than re-reporting it identically

### Requirement: Concurrent write safety
rrev SHALL serialize writes to a progress log so that concurrent rrev processes appending to the same file produce interleaved-but-intact entries rather than corrupted ones.

#### Scenario: Two runs append
- **WHEN** two rrev processes append to the same progress log
- **THEN** each entry is written whole, with no entry partially overwriting another

#### Scenario: Lock unavailable
- **WHEN** a write lock cannot be acquired within a bounded wait
- **THEN** rrev reports the contention and continues the review rather than blocking the pipeline indefinitely
