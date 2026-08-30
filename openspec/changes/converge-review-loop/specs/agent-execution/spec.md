# agent-execution Delta

## MODIFIED Requirements

### Requirement: Signal detection
rrev SHALL detect termination signals emitted by the executor in its output and use them to decide the phase outcome. The recognized signals MUST include one for a review iteration whose findings do not warrant another pass, one for an external review loop reaching agreement, and one for an unrecoverable failure. Output containing no signal MUST be treated as "work was done, iterate again" rather than as success, except where the phase supplies a rule that reads convergence off the iteration's own parsed report.

#### Scenario: Review-done signal
- **WHEN** the executor's output contains the review-done marker
- **THEN** the phase is treated as converged and does not iterate again

#### Scenario: No signal emitted
- **WHEN** the executor completes without emitting any recognized marker and the phase has no rule that reads convergence off the parsed report
- **THEN** the phase runs another iteration, up to its iteration limit

#### Scenario: No signal emitted but the phase rule converges
- **WHEN** the executor completes without emitting any recognized marker and the phase's own rule reads convergence off the iteration's parsed report
- **THEN** the phase ends as converged on that rule rather than iterating again

#### Scenario: Failure signal
- **WHEN** the executor emits the failure marker
- **THEN** rrev stops the pipeline and reports the failing phase with the executor's explanation

#### Scenario: Marker inside quoted text
- **WHEN** a marker appears only inside code the executor is quoting rather than as its own emitted line
- **THEN** rrev does not treat it as a signal
