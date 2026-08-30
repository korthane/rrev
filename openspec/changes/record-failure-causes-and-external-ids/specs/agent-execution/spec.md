## MODIFIED Requirements

### Requirement: Executor contract
rrev SHALL treat every AI tool it drives through one contract: it accepts a prompt and a cancellable context, and returns the tool's full output, the termination signal found in that output, and an error when the tool could not complete. Every supported tool MUST be interchangeable behind this contract.

#### Scenario: Successful call
- **WHEN** an executor is given a prompt and the tool exits successfully
- **THEN** rrev receives the complete output text and any signal it contained

#### Scenario: Tool exits non-zero
- **WHEN** the tool exits with a non-zero status
- **THEN** rrev receives an error carrying the exit status and the tool's diagnostic output — its standard error, or the last lines it wrote to standard output when standard error is empty — and the calling phase decides whether to retry or abort

#### Scenario: Context cancelled
- **WHEN** the context is cancelled while the tool is running
- **THEN** rrev terminates the tool's entire process group and returns promptly with whatever output was captured
