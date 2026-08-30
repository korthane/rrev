# agent-execution Specification

## Purpose
Defines how rrev drives an AI coding CLI as a subprocess — sending a prompt, surfacing progress while it works, recognizing the outcome it signals, and bounding how long it may run.

## Requirements

### Requirement: Executor contract
rrev SHALL treat every AI tool it drives through one contract: it accepts a prompt and a cancellable context, and returns the tool's full output, the termination signal found in that output, and an error when the tool could not complete. Every supported tool MUST be interchangeable behind this contract.

#### Scenario: Successful call
- **WHEN** an executor is given a prompt and the tool exits successfully
- **THEN** rrev receives the complete output text and any signal it contained

#### Scenario: Tool exits non-zero
- **WHEN** the tool exits with a non-zero status
- **THEN** rrev receives an error carrying the tool's diagnostic output, and the calling phase decides whether to retry or abort

#### Scenario: Context cancelled
- **WHEN** the context is cancelled while the tool is running
- **THEN** rrev terminates the tool's entire process group and returns promptly with whatever output was captured

### Requirement: Supported executors
rrev SHALL support claude and codex as primary executors, and SHALL support codex, a user-supplied script, or nothing at all as the external review tool. The primary executor MUST be selectable independently of the external review tool.

#### Scenario: Claude primary with codex external
- **WHEN** the default configuration is used
- **THEN** claude runs the review phases and fixes, and codex runs the independent external review

#### Scenario: Codex primary
- **WHEN** codex is selected as the primary executor
- **THEN** codex runs the review phases and fixes, and the external review phase is skipped because it would be same-model self-review

#### Scenario: Custom external tool
- **WHEN** a custom external review script is configured
- **THEN** rrev invokes that script with the review prompt and treats its standard output as the external tool's findings

#### Scenario: External review disabled
- **WHEN** the external review tool is set to none
- **THEN** the pipeline runs the comprehensive and final phases only, and reports that the external phase was skipped

### Requirement: Live progress reporting
While an executor runs, rrev SHALL stream its activity to the terminal so the user can see progress rather than an unexplained pause. Output MUST be attributed to the phase that produced it and, where the executor's output format identifies the sub-agent that produced a line, to that agent as well. A reported tool call MUST carry the argument that distinguishes it from other calls to the same tool, and its outcome, but MUST NOT carry the tool's output content. Long-running sub-agent activity MUST produce periodic indication that work is still in progress.

#### Scenario: Streaming output
- **WHEN** an executor emits incremental output
- **THEN** rrev renders it as it arrives, prefixed or colored to identify the current phase

#### Scenario: Sub-agent attributed
- **WHEN** a phase runs several reviewer agents concurrently and the executor's output format identifies which agent produced a line
- **THEN** rrev attributes that line to the agent as well as the phase, so concurrent reviewers are tellable apart

#### Scenario: Sub-agent not identifiable
- **WHEN** the executor's output format does not identify which agent produced a line
- **THEN** rrev attributes it to the phase alone rather than guessing, and the run continues

#### Scenario: Tool call carries its distinguishing argument
- **WHEN** rrev reports that the executor invoked a tool
- **THEN** it renders the argument that distinguishes the call — the command for a shell invocation, the path for a file read or write, the agent name for a sub-agent launch, the pattern for a search

#### Scenario: Tool argument bounded
- **WHEN** a tool's distinguishing argument spans multiple lines or exceeds the display width
- **THEN** rrev renders its first line truncated to a fixed width and marks it as truncated, so a multi-line command cannot break the display

#### Scenario: Tool outcome without output
- **WHEN** a reported tool call completes
- **THEN** rrev renders its outcome, and failure detail when it failed, but not the tool's output content

#### Scenario: Silent sub-agent work
- **WHEN** the tool spends a long stretch inside sub-agents without emitting reportable text
- **THEN** rrev emits a throttled progress indication at a bounded interval

#### Scenario: Debug output
- **WHEN** debug output is enabled
- **THEN** rrev additionally records the resolved command line, the full prompt sent to the tool, and the full arguments and output of reported tool calls

### Requirement: Signal detection
rrev SHALL detect termination signals emitted by the executor in its output and use them to decide the phase outcome. The recognized signals MUST include one for a review iteration that found nothing, one for an external review loop reaching agreement, and one for an unrecoverable failure. Output containing no signal MUST be treated as "work was done, iterate again" rather than as success.

#### Scenario: Review-done signal
- **WHEN** the executor's output contains the review-done marker
- **THEN** the phase is treated as converged and does not iterate again

#### Scenario: No signal emitted
- **WHEN** the executor completes without emitting any recognized marker
- **THEN** the phase runs another iteration, up to its iteration limit

#### Scenario: Failure signal
- **WHEN** the executor emits the failure marker
- **THEN** rrev stops the pipeline and reports the failing phase with the executor's explanation

#### Scenario: Marker inside quoted text
- **WHEN** a marker appears only inside code the executor is quoting rather than as its own emitted line
- **THEN** rrev does not treat it as a signal

### Requirement: Model and effort selection
rrev SHALL let the model and reasoning effort be chosen per phase, expressed as a combined specification where either part may be omitted and inherits the configured default. An effort level unsupported by the selected tool MUST be reported and ignored rather than passed through.

#### Scenario: Model and effort both set
- **WHEN** a phase specifies both a model and an effort level
- **THEN** the executor invokes the tool with both

#### Scenario: Effort only
- **WHEN** a phase specifies only an effort level
- **THEN** the model resolves from the configured default and only the effort is overridden

#### Scenario: Review model falls back
- **WHEN** no review model is configured
- **THEN** review phases use the primary executor's configured model

#### Scenario: Unsupported effort
- **WHEN** an effort level the selected tool does not accept is requested
- **THEN** rrev warns naming the level and the tool, and proceeds with the tool's default effort

### Requirement: Execution timeouts
rrev SHALL support two independent bounds on an executor call: a total session timeout and an idle timeout that expires only when no output has arrived for the configured duration. Both MUST default to disabled, and a timeout MUST terminate the tool's process group and surface a distinguishable error.

#### Scenario: Session timeout
- **WHEN** a session timeout is configured and an executor call exceeds it
- **THEN** rrev terminates the tool and reports that the session timed out

#### Scenario: Idle timeout resets
- **WHEN** an idle timeout is configured and the tool keeps producing output
- **THEN** the idle countdown restarts on each line and the call is not terminated

#### Scenario: Idle timeout fires
- **WHEN** the tool produces no output for longer than the idle timeout
- **THEN** rrev terminates the tool and reports that the session went idle, preserving the output captured so far

#### Scenario: Timeouts disabled
- **WHEN** neither timeout is configured
- **THEN** the executor call runs until the tool exits or the context is cancelled

### Requirement: Rate-limit and transient failure handling
rrev SHALL recognize provider rate-limit and transient-failure responses in executor output and MUST distinguish them from a substantive review result, so a throttled call is not mistaken for a clean review.

#### Scenario: Rate limit hit
- **WHEN** the tool reports that a usage limit was reached
- **THEN** rrev surfaces a rate-limit error naming the tool, and the phase does not record the call as a converged iteration

#### Scenario: Retryable failure
- **WHEN** the tool reports a transient failure that it suggests retrying
- **THEN** rrev reports it as retryable and the calling phase may re-run the iteration within its limit
