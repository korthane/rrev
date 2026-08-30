# review-pipeline Delta

## MODIFIED Requirements

### Requirement: Comprehensive review phase
The first phase SHALL launch a set of independent reviewer agents concurrently against the branch diff, then have the executor deduplicate their findings, verify each against the actual code, fix the confirmed ones, run the project's validation commands, and commit. The agent set MUST include reviewers for spec conformance and for task completeness in addition to general code quality. The phase SHALL converge on the first iteration that confirms nothing critical or major, and rrev MUST enforce that rule from the iteration's parsed report even when the executor does not emit the convergence signal.

#### Scenario: Agents run concurrently
- **WHEN** the phase starts
- **THEN** all configured reviewer agents for that phase are launched in a single message and the phase waits for all of them before evaluating findings

#### Scenario: Conformance reviewed
- **WHEN** the conformance agent runs
- **THEN** it evaluates the diff against each requirement and scenario in the change's checklist and reports requirements that are unimplemented, partially implemented, or contradicted

#### Scenario: Task completeness reviewed
- **WHEN** the change's task list marks tasks complete
- **THEN** the task agent verifies each against the diff and reports any marked complete without corresponding implementation

#### Scenario: Findings verified before fixing
- **WHEN** agents report findings
- **THEN** the executor reads the code at each cited location, discards findings it cannot confirm, and fixes only the confirmed ones

#### Scenario: Fixes committed
- **WHEN** the executor fixes confirmed findings
- **THEN** it runs the configured validation commands and commits the fixes before the phase iterates

#### Scenario: Phase converges
- **WHEN** an iteration confirms no critical or major finding
- **THEN** the executor fixes any confirmed minor findings, validates, commits, emits the review-done signal, and the phase ends as converged

#### Scenario: Convergence enforced without the signal
- **WHEN** an iteration's parsed report confirms at least one finding, none of them critical or major, validation was not reported as failed, and the executor emitted no signal
- **THEN** rrev ends the phase as converged and reports that convergence came from the severity of the confirmed findings rather than the signal

#### Scenario: Empty report does not converge without the signal
- **WHEN** an iteration's parsed report contains no findings and the executor emitted no signal
- **THEN** the phase runs another iteration, since an empty report may be a reporting failure rather than a clean review

#### Scenario: Major finding keeps the loop alive
- **WHEN** an iteration confirms at least one critical or major finding
- **THEN** the executor fixes, validates, and commits without emitting the convergence signal, and the phase runs another iteration to check the fixes

#### Scenario: Failed validation blocks convergence
- **WHEN** an iteration confirms only minor findings but reports its validation as failed
- **THEN** the phase does not converge and runs another iteration

## ADDED Requirements

### Requirement: Repeat iteration scope
Comprehensive iterations after the first SHALL be driven by a distinct repeat prompt and SHALL direct reviewers primarily at the changes made since the last commit the phase has already reviewed, while keeping the full branch diff available and regressions anywhere in it in scope. When no commit has landed since the last reviewed point, the iteration MUST fall back to the full branch scope.

#### Scenario: Repeat iteration scoped to the fixes
- **WHEN** a comprehensive iteration after the first follows an iteration that committed fixes
- **THEN** its diff instruction names the diff from the last reviewed commit to HEAD as the primary review scope and the full branch diff as context

#### Scenario: Repeat prompt used
- **WHEN** a comprehensive iteration after the first runs
- **THEN** it is driven by the repeat prompt, which is overridable the same way as every other phase prompt

#### Scenario: No new commit since the last review
- **WHEN** a comprehensive iteration after the first follows an iteration that committed nothing
- **THEN** its diff instruction covers the full branch diff, the same scope as the first iteration

#### Scenario: Other phases unaffected
- **WHEN** the external loop or the final phase assembles a prompt
- **THEN** its diff instruction covers the full branch diff, unchanged by this requirement
