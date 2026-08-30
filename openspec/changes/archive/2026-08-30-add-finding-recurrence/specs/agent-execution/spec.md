## MODIFIED Requirements

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
