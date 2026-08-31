# review-pipeline Specification

## Purpose
Orchestrates the review of a branch against an OpenSpec change: three review phases that alternate independent reviewers with a fixing executor, the loop conditions that decide when to iterate and when to stop, and the optional step that runs once everything is clean.

## Requirements

### Requirement: Review target
Every phase SHALL review the changes on the current branch relative to the resolved base ref, comparing them against the selected change's requirement checklist. rrev MUST tell reviewers how to obtain the diff rather than embedding the diff in the prompt.

#### Scenario: Diff against base
- **WHEN** the pipeline starts on a branch ahead of the base ref
- **THEN** every phase reviews the three-dot diff between the base ref and HEAD, together with the branch's commit log

#### Scenario: Base ref auto-detected
- **WHEN** no base ref is configured or passed
- **THEN** rrev detects the repository's default branch and uses it

#### Scenario: Empty diff
- **WHEN** the branch has no changes relative to the base ref
- **THEN** rrev reports that there is nothing to review and exits successfully without invoking an executor

#### Scenario: Diff kept out of prompts
- **WHEN** a phase prompt is assembled
- **THEN** it contains the commands that produce the diff, not the diff text itself

### Requirement: Comprehensive review phase
The first phase SHALL launch a set of independent reviewer agents concurrently against the branch diff, then have the executor deduplicate their findings, verify each against the actual code, fix the confirmed ones, run the project's validation commands, and commit. The agent set MUST include reviewers for spec conformance and for task completeness in addition to general code quality. The phase SHALL converge on the first iteration whose confirmed findings are all minor, and rrev MUST enforce that rule from the iteration's parsed report even when the executor does not emit the convergence signal. A report rrev cannot read that way MUST NOT converge it.

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
- **WHEN** an iteration's parsed report confirms at least one finding, every one of them minor, validation was not reported as failed, and the executor emitted no signal
- **THEN** rrev ends the phase as converged and reports that convergence came from the severity of the confirmed findings rather than the signal

#### Scenario: Unreadable severity does not converge without the signal
- **WHEN** an iteration's parsed report carries a confirmed finding whose severity is not one rrev recognises, including a report line whose fields shifted or omitted it
- **THEN** the phase runs another iteration, since a severity it could not read is a reporting failure rather than a clean review

#### Scenario: Empty report does not converge without the signal
- **WHEN** an iteration's parsed report contains no findings and the executor emitted no signal
- **THEN** the phase runs another iteration, since an empty report may be a reporting failure rather than a clean review

#### Scenario: Major finding keeps the loop alive
- **WHEN** an iteration confirms at least one critical or major finding
- **THEN** the executor fixes, validates, and commits without emitting the convergence signal, and the phase runs another iteration to check the fixes

#### Scenario: Failed validation blocks convergence
- **WHEN** an iteration confirms only minor findings but reports its validation as failed, in whatever spelling of failure the executor used
- **THEN** the phase does not converge and runs another iteration

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

### Requirement: External review loop
The second phase SHALL alternate an independent external review tool with the primary executor: the external tool reviews the diff against the same spec context and reports findings, then the executor evaluates them, fixes what it confirms, and commits. The loop MUST repeat until it terminates on one of its defined conditions.

#### Scenario: Findings evaluated, not applied blindly
- **WHEN** the external tool reports findings
- **THEN** the executor verifies each against the code and rejects the ones it determines to be false positives, recording the rejection and its reason

#### Scenario: External tool reports nothing
- **WHEN** the external tool reports no issues
- **THEN** the loop terminates as converged

#### Scenario: Previous rounds carried forward
- **WHEN** the loop runs a second or later iteration
- **THEN** the external tool's prompt includes the prior rounds' findings and their dispositions, so it does not re-report what was already rejected with reason

#### Scenario: Skipped for same-model self-review
- **WHEN** the primary executor and the external review tool would be the same model
- **THEN** the phase is skipped and the pipeline reports why

### Requirement: Loop termination
Each review loop SHALL terminate on the first of: the convergence signal, a phase rule that reads convergence off the iteration's own parsed report, its iteration limit, a detected stalemate, an unrecoverable executor failure, or a user break. rrev MUST report which condition ended the loop.

#### Scenario: Iteration limit
- **WHEN** a loop reaches its configured maximum iterations without converging
- **THEN** it stops, reports that the limit was reached, and the run's exit status reflects non-convergence

#### Scenario: Stalemate detected
- **WHEN** stalemate patience is configured and the configured number of consecutive iterations produce no new commit and no working-tree change
- **THEN** the loop terminates early and reports the stalemate

#### Scenario: Patience disabled
- **WHEN** stalemate patience is not configured
- **THEN** unchanged iterations do not by themselves terminate the loop

#### Scenario: Termination reason reported
- **WHEN** any loop ends
- **THEN** rrev prints the terminating condition and the iteration count, and records both in the progress log

#### Scenario: The two convergence mechanisms are distinguishable
- **WHEN** a loop ends because a phase rule read convergence off the report rather than because the executor emitted the signal
- **THEN** the condition it reports names the rule, so a reader can tell the two apart

### Requirement: Final review phase
After the external loop, the pipeline SHALL run a narrower review restricted to critical and major issues, to catch regressions introduced by fixes applied during earlier phases. It MUST be skipped when the external loop found nothing on its first pass, since no fixes were applied that could have regressed anything.

#### Scenario: Regression pass runs
- **WHEN** earlier phases applied fixes
- **THEN** the final phase reviews the branch again and iterates until it reports no critical or major issues

#### Scenario: Minor issues ignored
- **WHEN** the final phase encounters a style or minor issue
- **THEN** it is not fixed and does not prevent the phase from converging

#### Scenario: Skipped when nothing changed
- **WHEN** the external loop converged on its first iteration with no fixes applied
- **THEN** the final phase is skipped and the pipeline reports it as unnecessary

### Requirement: Report-only runs
In report-only mode the pipeline SHALL run its reviewers and verification steps but MUST NOT modify tracked files or create commits. It MUST emit a findings report listing each verified finding with its location, severity, source reviewer, and the requirement it relates to when applicable.

#### Scenario: No repository mutation
- **WHEN** the pipeline runs in report-only mode
- **THEN** the working tree and commit history are unchanged when the run ends

#### Scenario: Report emitted
- **WHEN** verified findings exist at the end of a report-only run
- **THEN** rrev writes a report containing each finding's file and line, severity, reporting reviewer, and related requirement

#### Scenario: Loops do not iterate on fixes
- **WHEN** report-only mode is active
- **THEN** each review phase runs a single pass, since there are no fixes for a subsequent iteration to verify

### Requirement: Finalize step
rrev SHALL support an optional finalize step that runs once after all review phases converge. It MUST be disabled by default, MUST be driven by an overridable prompt, and its failure MUST NOT change the run's outcome.

#### Scenario: Disabled by default
- **WHEN** finalize is not enabled in configuration
- **THEN** the pipeline ends after the last review phase

#### Scenario: Enabled and successful
- **WHEN** finalize is enabled and all review phases converged
- **THEN** rrev runs the finalize prompt once through the primary executor

#### Scenario: Finalize fails
- **WHEN** the finalize step fails
- **THEN** rrev logs the failure and still reports the run as successful

#### Scenario: Not reached on non-convergence
- **WHEN** a review phase ended without converging
- **THEN** finalize does not run
