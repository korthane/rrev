## Purpose

Turns a named OpenSpec change into the review context that every phase works from — the goal being reviewed, the requirement and scenario checklist the diff must satisfy, and the artifact paths reviewers are told to read.

## ADDED Requirements

### Requirement: Change discovery
rrev SHALL discover active OpenSpec changes for the resolved OpenSpec root. It MUST prefer the `openspec` CLI when it is available, and MUST fall back to reading `openspec/changes/` directly when it is not, so a review can run without that CLI installed.

#### Scenario: CLI available
- **WHEN** the `openspec` CLI is on `PATH`
- **THEN** rrev lists active changes through the CLI's machine-readable output

#### Scenario: CLI absent
- **WHEN** the `openspec` CLI is not installed
- **THEN** rrev enumerates the subdirectories of `openspec/changes/` and continues, noting the degraded mode in its output

#### Scenario: No OpenSpec root
- **WHEN** no `openspec/` directory is found at or above the working directory
- **THEN** rrev exits with an error stating that an OpenSpec-driven repository is required

#### Scenario: Archived change excluded
- **WHEN** a change exists only under the archive directory
- **THEN** it is not offered for auto-detection, though it MAY still be reviewed when named explicitly

### Requirement: Artifact loading
For the selected change, rrev SHALL load the proposal, the design document, every delta spec under the change's `specs/` tree, and the task list. A missing optional artifact MUST NOT abort the run; a missing proposal or an empty set of delta specs MUST be reported to the user and recorded in the context handed to reviewers.

#### Scenario: Full artifact set
- **WHEN** the change contains a proposal, a design document, delta specs, and tasks
- **THEN** all four are loaded and their paths appear in the review context

#### Scenario: Optional artifact absent
- **WHEN** the change has no design document
- **THEN** rrev proceeds, and the review context records that no design document exists rather than referencing a missing path

#### Scenario: Change opted out of specs
- **WHEN** the change declares that it skips specs
- **THEN** rrev proceeds with proposal and tasks as the conformance basis and states in the context that no delta specs are available

#### Scenario: Unreadable artifact
- **WHEN** an artifact file exists but cannot be read
- **THEN** rrev exits with an error naming the file and the underlying cause

### Requirement: Requirement extraction
rrev SHALL extract a checklist of requirements and their scenarios from the change's delta specs, preserving each requirement's capability path, delta operation, requirement name, and scenario text. The checklist MUST be usable as explicit conformance criteria in a prompt without the reviewer needing to reparse the spec files.

#### Scenario: Requirements and scenarios extracted
- **WHEN** a delta spec declares requirements with scenarios
- **THEN** each requirement appears in the checklist with its capability path and every one of its scenarios

#### Scenario: Delta operations distinguished
- **WHEN** a delta spec contains added, modified, and removed requirements
- **THEN** the checklist labels each requirement with its operation so a reviewer can tell new behavior from changed and withdrawn behavior

#### Scenario: Requirement without scenarios
- **WHEN** a requirement declares no scenario
- **THEN** it still appears in the checklist and is flagged as lacking verifiable scenarios

#### Scenario: Unparseable spec file
- **WHEN** a delta spec cannot be parsed into requirements
- **THEN** rrev reports the file, includes its raw text in the review context, and continues rather than failing the run

### Requirement: Review goal derivation
rrev SHALL derive a short human-readable goal for the run from the selected change and use it consistently in prompts, terminal output, and the progress log, so every reviewer and every log line refers to the same subject.

#### Scenario: Goal from proposal
- **WHEN** the change's proposal states why the change is needed
- **THEN** the derived goal summarizes it in a single line alongside the change name

#### Scenario: Goal fallback
- **WHEN** no usable summary can be derived from the artifacts
- **THEN** the goal is the change name itself, and the run proceeds

### Requirement: Context reuse across phases
The review context SHALL be resolved once per run and reused by every phase and every executor call, so that a reviewer, the external tool, and the fixing executor all evaluate the diff against identical criteria.

#### Scenario: Same context in every phase
- **WHEN** the pipeline runs the comprehensive phase, the external loop, and the final phase
- **THEN** each receives the same change name, goal, artifact paths, and requirement checklist

#### Scenario: Artifacts changed mid-run
- **WHEN** an artifact file is edited on disk while the pipeline is running
- **THEN** rrev does not re-resolve the context, and every later phase is still handed the change name, goal, artifact paths, and requirement checklist captured at startup
