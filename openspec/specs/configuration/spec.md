# configuration Specification

## Purpose
Defines how rrev resolves its settings, prompts, and reviewer agents from layered sources, so a project can tune the review pipeline without forking rrev or editing its source.

## Requirements

### Requirement: Layered configuration resolution
rrev SHALL resolve every setting from four sources with a fixed precedence: command-line flags, then project configuration under `.rrev/`, then user configuration under `~/.config/rrev/`, then embedded defaults. A source that omits a setting MUST NOT override a lower-precedence value with a zero value.

#### Scenario: Project overrides user
- **WHEN** the user configuration sets a value and the project configuration sets the same key to a different value
- **THEN** the project value is used

#### Scenario: Partial configuration file
- **WHEN** a configuration file sets only some keys
- **THEN** the unset keys resolve from the next source in precedence order, ending at the embedded defaults

#### Scenario: No configuration files
- **WHEN** neither a project nor a user configuration file exists
- **THEN** rrev runs entirely on embedded defaults without error

#### Scenario: Malformed configuration
- **WHEN** a configuration file cannot be parsed
- **THEN** rrev exits with an error naming the file and the offending line, and does not silently fall back to defaults

### Requirement: Prompt and agent overrides
rrev SHALL ship every phase prompt and every reviewer agent definition as embedded defaults, and SHALL let a project or user replace any one of them by placing a file with the same name in the corresponding `prompts/` or `agents/` directory. Overriding one file MUST NOT require copying the others.

#### Scenario: Single prompt overridden
- **WHEN** a project provides `.rrev/prompts/review_first.txt` and no other prompt files
- **THEN** rrev uses the project file for the comprehensive review phase and embedded defaults for every other prompt

#### Scenario: Custom agent added
- **WHEN** a project adds `.rrev/agents/perf.txt` and references it from a phase prompt
- **THEN** that agent is launched as part of that phase alongside the referenced default agents

#### Scenario: Agent removed from a phase
- **WHEN** a project's prompt file omits the reference to a default agent
- **THEN** that agent is not launched, even though its definition still exists

#### Scenario: Referenced agent missing
- **WHEN** a prompt references an agent name that resolves to no file in any source
- **THEN** rrev exits with an error naming the prompt file and the unresolved agent

### Requirement: Template expansion
rrev SHALL expand template variables in prompt files before sending them to an executor. Expansion MUST cover at least the selected change name, the change's artifact paths, the derived review goal, the requirement checklist, the progress log path, the base ref, and the diff instruction for the current iteration. An unrecognized variable MUST be reported rather than passed through to the model.

#### Scenario: Variables substituted
- **WHEN** a prompt contains the goal, progress log, and base ref variables
- **THEN** the executor receives the prompt with each replaced by its resolved value

#### Scenario: Unknown variable
- **WHEN** a prompt contains a variable name rrev does not define
- **THEN** rrev exits with an error naming the prompt file and the unknown variable

### Requirement: Executor-aware agent expansion
rrev SHALL expand each agent reference in a prompt into the invocation form native to the executor running that phase, so the same prompt file drives both supported executors.

#### Scenario: Claude executor
- **WHEN** a phase running under the claude executor expands an agent reference
- **THEN** the prompt instructs claude to launch that agent via its subagent tool, with the agent's definition as the agent prompt

#### Scenario: Codex executor
- **WHEN** the same phase runs under the codex executor
- **THEN** the prompt instructs codex to spawn that agent using its own agent mechanism, carrying the same agent definition

#### Scenario: Parallel launch preserved
- **WHEN** a prompt references several agents for one phase
- **THEN** the expansion instructs the executor to launch all of them in a single message so they run concurrently

### Requirement: Incompatible option detection
rrev SHALL reject configurations whose parts contradict each other. A contradiction introduced by command-line flags MUST be a startup error; a contradiction present only in configuration files MAY be resolved automatically, but rrev MUST warn on stderr describing what it overrode.

#### Scenario: Conflicting flags
- **WHEN** the user selects codex as the primary executor and also requests codex as the external review tool via flags
- **THEN** rrev exits with an error explaining that same-model self-review is not supported

#### Scenario: Conflict only in config
- **WHEN** the same contradiction comes entirely from configuration files
- **THEN** rrev disables the external review phase, prints a warning naming the overridden setting, and continues
