# rrev - Spec-Driven Review Pipeline

## Overview

Teams that plan with OpenSpec write their intent down as requirements and scenarios, then implement it - but nothing checks the resulting code against that intent. Reviews are either manual or generic ("find bugs"), so drift between an approved change and its implementation is only caught by whoever happens to read the diff carefully.

rrev is a Go CLI that closes that gap. It reviews the current branch against a named OpenSpec change and autonomously fixes and commits what it finds, running three review phases that alternate independent reviewers with a fixing executor until the reviewers go quiet. The review mechanic is ported from ralphex; the difference is the source of truth - instead of a free-form plan file, rrev is driven by a change's proposal, design, delta specs, and tasks, so requirements and scenarios become explicit conformance criteria rather than background prose.

The delivered program supports two interchangeable executors (claude and codex), layered configuration with overridable prompts and reviewer agents, a cross-iteration progress log, and four run modes including a report-only mode that never touches the working tree.

## Context

- Bootstraps an empty repository. Module `github.com/korthane/rrev`, with `cmd/rrev/` and packages `pkg/openspec`, `pkg/config`, `pkg/executor`, `pkg/processor`, `pkg/processor/phase`, `pkg/progress`, `pkg/git`, `pkg/status`.
- Runtime dependencies on external tools: `git`, the `claude` CLI, the `codex` CLI (unless the external phase is disabled), and the `openspec` CLI (used for change discovery and requirement extraction, with a filesystem and markdown-parser fallback so rrev still works without it).
- No dependency on the ralphex Go module. The pipeline mechanic is ralphex-inspired (MIT, Umputun) and reimplemented; prompt and agent text derived from ralphex defaults carries attribution.
- rrev writes to the repository it reviews: it creates commits on the current branch and appends to `.rrev/progress/`. It never pushes unless a finalize prompt is configured to.
- Out of scope: task execution (rrev never implements a change), plan creation, a web UI, worktree isolation, and archiving the change on success.
- Adopted from OpenSpec change `add-spec-review-pipeline`. The change's artifacts under `openspec/changes/add-spec-review-pipeline/` remain the source of truth - if this plan and a spec file disagree, the spec file wins.

## Development Approach

- Testing approach: regular
- Complete each task fully before moving to the next
- Update this plan when scope changes during implementation

## Testing Strategy

- Unit tests required for every code-changing Task
- Run project tests after each Task before proceeding

## Progress Tracking

- Mark completed items with `[x]` immediately when done
- Update plan if implementation deviates from original scope

## Technical Details

The requirements below are copied from the change's delta specs. Each capability names its spec file; read that file for the authoritative text if anything here looks stale.

### Capability: cli

Source: `openspec/changes/add-spec-review-pipeline/specs/cli/spec.md`

**Requirement: Change selection.** `rrev` SHALL accept an optional OpenSpec change name as its sole positional argument and review the current branch against that change. When the argument is omitted, rrev SHALL resolve the change automatically and MUST refuse to guess when the choice is ambiguous.

- Explicit change name: WHEN the user runs `rrev add-user-auth` in a repository containing that change, THEN rrev reviews the branch against that change's artifacts.
- Single active change auto-detected: WHEN the user runs `rrev` with no argument and exactly one active change exists, THEN rrev selects it and reports the selection before the first phase starts.
- Ambiguous selection: WHEN no argument is given and more than one active change exists, THEN rrev exits with a usage error listing the available change names and starts no phase.
- Unknown change name: WHEN the user names a change that does not exist, THEN rrev exits with an error naming the change and listing the available ones.

**Requirement: Run modes.** `rrev` SHALL support run modes that select where the pipeline starts and whether it may modify the repository. Modes MUST be mutually exclusive, and combining two of them MUST fail at startup rather than silently preferring one.

- Default full pipeline: WHEN no mode flag is passed, THEN rrev runs the comprehensive review phase, the external review loop, the final review phase, and the finalize step if enabled.
- External-only: WHEN `--external-only` is passed, THEN rrev skips the comprehensive review phase and starts at the external review loop, followed by the final review phase.
- First phase only: WHEN `--phase1-only` is passed, THEN rrev runs only the comprehensive review phase and exits after it converges.
- Report-only: WHEN `--report-only` is passed, THEN rrev collects verified findings, writes them to the configured report destination, and exits without modifying tracked files or creating commits.
- Conflicting modes rejected: WHEN both `--external-only` and `--phase1-only` are passed, THEN rrev exits with an error naming both flags and runs no phase.

**Requirement: Per-run overrides.** `rrev` SHALL expose flags that override configuration for a single run, including the base ref for review diffs, the primary executor, per-phase model and effort, iteration limits, stalemate patience, the external review tool, and session and idle timeouts. A flag value MUST take precedence over any configuration file value.

- Base ref override: WHEN `--base-ref develop` is passed, THEN every phase reviews `git diff develop...HEAD` instead of the diff against the auto-detected default branch.
- Flag beats config: WHEN the configuration file sets a review model and the user passes `--review-model`, THEN the flag value is used and the configured value is ignored.
- Invalid override value: WHEN an unrecognized value is passed to `--external-review-tool`, THEN rrev exits with an error naming the flag and its accepted values, before starting any phase.

**Requirement: Startup preflight.** `rrev` SHALL validate its environment before starting the first phase and MUST fail fast with an actionable message rather than starting a phase that cannot succeed. Preflight MUST verify that the working directory is inside a git repository, that the base ref resolves, that the selected change exists and has readable artifacts, and that every executable it intends to invoke is present on `PATH`.

- Not a git repository: WHEN rrev starts outside a git repository, THEN it exits with an error stating that a git repository is required and runs no phase.
- Missing executor binary: WHEN the configured primary executor command is not found on `PATH`, THEN rrev exits with an error naming the missing command and how it was configured.
- Base ref unresolvable: WHEN the resolved base ref does not name a reachable commit, THEN rrev exits with an error naming the ref and suggesting `--base-ref`.
- Preflight passes: WHEN all checks succeed, THEN rrev prints a startup banner naming the change, base ref, mode, executors, and resolved models, then starts the first phase.

**Requirement: Exit status.** `rrev` SHALL communicate the run outcome through its exit status: zero when the pipeline converged, and a distinct non-zero status for a run that ended without converging versus a run that failed to start or aborted.

- Clean convergence: WHEN every executed phase reports no remaining findings, THEN rrev exits with status 0.
- Unconverged run: WHEN a phase exhausts its iteration limit or terminates on a stalemate while findings remain, THEN rrev exits non-zero and prints which phase did not converge and why.
- Executor failure: WHEN the executor reports issues it cannot fix, THEN rrev stops the pipeline and exits non-zero, reporting the failing phase.

**Requirement: Interactive interruption.** `rrev` SHALL respond to interruption while a phase is running. An abort request MUST stop the run and terminate the child process group; a break request MUST end only the current review loop and let the pipeline continue with the next phase.

- Abort: WHEN the user sends an interrupt (Ctrl+C) during any phase, THEN rrev cancels the running executor, terminates its process group, writes what it has to the progress log, and exits non-zero.
- Break the external loop: WHEN the user sends a break signal (Ctrl+\) during the external review loop on a platform that supports it, THEN rrev cancels the current executor call, ends the loop, and continues with the final review phase.
- Break unsupported: WHEN the platform provides no break signal, THEN rrev omits the break hint from its output and the loop terminates only on its own conditions.

### Capability: configuration

Source: `openspec/changes/add-spec-review-pipeline/specs/configuration/spec.md`

**Requirement: Layered configuration resolution.** rrev SHALL resolve every setting from four sources with a fixed precedence: command-line flags, then project configuration under `.rrev/`, then user configuration under `~/.config/rrev/`, then embedded defaults. A source that omits a setting MUST NOT override a lower-precedence value with a zero value.

- Project overrides user: WHEN both set the same key to different values, THEN the project value is used.
- Partial configuration file: WHEN a file sets only some keys, THEN unset keys resolve from the next source in precedence order, ending at embedded defaults.
- No configuration files: WHEN neither a project nor a user file exists, THEN rrev runs entirely on embedded defaults without error.
- Malformed configuration: WHEN a file cannot be parsed, THEN rrev exits with an error naming the file and the offending line, and does not silently fall back to defaults.

**Requirement: Prompt and agent overrides.** rrev SHALL ship every phase prompt and every reviewer agent definition as embedded defaults, and SHALL let a project or user replace any one of them by placing a file with the same name in the corresponding `prompts/` or `agents/` directory. Overriding one file MUST NOT require copying the others.

- Single prompt overridden: WHEN a project provides `.rrev/prompts/review_first.txt` and no other prompt files, THEN rrev uses the project file for the comprehensive review phase and embedded defaults for every other prompt.
- Custom agent added: WHEN a project adds `.rrev/agents/perf.txt` and references it from a phase prompt, THEN that agent is launched as part of that phase alongside the referenced default agents.
- Agent removed from a phase: WHEN a project's prompt omits the reference to a default agent, THEN that agent is not launched, even though its definition still exists.
- Referenced agent missing: WHEN a prompt references an agent name that resolves to no file in any source, THEN rrev exits with an error naming the prompt file and the unresolved agent.

**Requirement: Template expansion.** rrev SHALL expand template variables in prompt files before sending them to an executor. Expansion MUST cover at least the selected change name, the change's artifact paths, the derived review goal, the requirement checklist, the progress log path, the base ref, and the diff instruction for the current iteration. An unrecognized variable MUST be reported rather than passed through to the model.

- Variables substituted: WHEN a prompt contains the goal, progress log, and base ref variables, THEN the executor receives the prompt with each replaced by its resolved value.
- Unknown variable: WHEN a prompt contains a variable rrev does not define, THEN rrev exits with an error naming the prompt file and the unknown variable.

**Requirement: Executor-aware agent expansion.** rrev SHALL expand each agent reference in a prompt into the invocation form native to the executor running that phase, so the same prompt file drives both supported executors.

- Claude executor: WHEN a phase running under claude expands an agent reference, THEN the prompt instructs claude to launch that agent via its subagent tool, with the agent's definition as the agent prompt.
- Codex executor: WHEN the same phase runs under codex, THEN the prompt instructs codex to spawn that agent using its own agent mechanism, carrying the same agent definition.
- Parallel launch preserved: WHEN a prompt references several agents for one phase, THEN the expansion instructs the executor to launch all of them in a single message so they run concurrently.

**Requirement: Incompatible option detection.** rrev SHALL reject configurations whose parts contradict each other. A contradiction introduced by command-line flags MUST be a startup error; a contradiction present only in configuration files MAY be resolved automatically, but rrev MUST warn on stderr describing what it overrode.

- Conflicting flags: WHEN codex is selected as the primary executor and also requested as the external review tool via flags, THEN rrev exits with an error explaining that same-model self-review is not supported.
- Conflict only in config: WHEN the same contradiction comes entirely from configuration files, THEN rrev disables the external review phase, prints a warning naming the overridden setting, and continues.

### Capability: spec-context

Source: `openspec/changes/add-spec-review-pipeline/specs/spec-context/spec.md`

**Requirement: Change discovery.** rrev SHALL discover active OpenSpec changes for the resolved OpenSpec root. It MUST prefer the `openspec` CLI when available, and MUST fall back to reading `openspec/changes/` directly when it is not, so a review can run without that CLI installed.

- CLI available: WHEN the `openspec` CLI is on `PATH`, THEN rrev lists active changes through the CLI's machine-readable output.
- CLI absent: WHEN it is not installed, THEN rrev enumerates the subdirectories of `openspec/changes/` and continues, noting the degraded mode in its output.
- No OpenSpec root: WHEN no `openspec/` directory is found at or above the working directory, THEN rrev exits with an error stating that an OpenSpec-driven repository is required.
- Archived change excluded: WHEN a change exists only under the archive directory, THEN it is not offered for auto-detection, though it MAY still be reviewed when named explicitly.

**Requirement: Artifact loading.** For the selected change, rrev SHALL load the proposal, the design document, every delta spec under the change's `specs/` tree, and the task list. A missing optional artifact MUST NOT abort the run; a missing proposal or an empty set of delta specs MUST be reported to the user and recorded in the context handed to reviewers.

- Full artifact set: WHEN the change contains a proposal, design document, delta specs, and tasks, THEN all four are loaded and their paths appear in the review context.
- Optional artifact absent: WHEN the change has no design document, THEN rrev proceeds, and the review context records that none exists rather than referencing a missing path.
- Change opted out of specs: WHEN the change declares that it skips specs, THEN rrev proceeds with proposal and tasks as the conformance basis and states in the context that no delta specs are available.
- Unreadable artifact: WHEN an artifact exists but cannot be read, THEN rrev exits with an error naming the file and the underlying cause.

**Requirement: Requirement extraction.** rrev SHALL extract a checklist of requirements and their scenarios from the change's delta specs, preserving each requirement's capability path, delta operation, requirement name, and scenario text. The checklist MUST be usable as explicit conformance criteria in a prompt without the reviewer needing to reparse the spec files.

- Requirements and scenarios extracted: WHEN a delta spec declares requirements with scenarios, THEN each requirement appears in the checklist with its capability path and every one of its scenarios.
- Delta operations distinguished: WHEN a delta spec contains added, modified, and removed requirements, THEN the checklist labels each with its operation so a reviewer can tell new behavior from changed and withdrawn behavior.
- Requirement without scenarios: WHEN a requirement declares no scenario, THEN it still appears in the checklist and is flagged as lacking verifiable scenarios.
- Unparseable spec file: WHEN a delta spec cannot be parsed into requirements, THEN rrev reports the file, includes its raw text in the review context, and continues rather than failing the run.

**Requirement: Review goal derivation.** rrev SHALL derive a short human-readable goal for the run from the selected change and use it consistently in prompts, terminal output, and the progress log, so every reviewer and every log line refers to the same subject.

- Goal from proposal: WHEN the proposal states why the change is needed, THEN the derived goal summarizes it in a single line alongside the change name.
- Goal fallback: WHEN no usable summary can be derived, THEN the goal is the change name itself, and the run proceeds.

**Requirement: Context reuse across phases.** The review context SHALL be resolved once per run and reused by every phase and every executor call, so that a reviewer, the external tool, and the fixing executor all evaluate the diff against identical criteria.

- Same context in every phase: WHEN the pipeline runs the comprehensive phase, the external loop, and the final phase, THEN each receives the same change name, goal, artifact paths, and requirement checklist.
- Artifacts changed mid-run: WHEN an artifact file is edited on disk while the pipeline is running, THEN the run continues with the context captured at startup, and the change is not silently picked up mid-pipeline.

### Capability: agent-execution

Source: `openspec/changes/add-spec-review-pipeline/specs/agent-execution/spec.md`

**Requirement: Executor contract.** rrev SHALL treat every AI tool it drives through one contract: it accepts a prompt and a cancellable context, and returns the tool's full output, the termination signal found in that output, and an error when the tool could not complete. Every supported tool MUST be interchangeable behind this contract.

- Successful call: WHEN an executor is given a prompt and the tool exits successfully, THEN rrev receives the complete output text and any signal it contained.
- Tool exits non-zero: WHEN the tool exits with a non-zero status, THEN rrev receives an error carrying the tool's diagnostic output, and the calling phase decides whether to retry or abort.
- Context cancelled: WHEN the context is cancelled while the tool is running, THEN rrev terminates the tool's entire process group and returns promptly with whatever output was captured.

**Requirement: Supported executors.** rrev SHALL support claude and codex as primary executors, and SHALL support codex, a user-supplied script, or nothing at all as the external review tool. The primary executor MUST be selectable independently of the external review tool.

- Claude primary with codex external: WHEN the default configuration is used, THEN claude runs the review phases and fixes, and codex runs the independent external review.
- Codex primary: WHEN codex is the primary executor, THEN codex runs the review phases and fixes, and the external review phase is skipped because it would be same-model self-review.
- Custom external tool: WHEN a custom external review script is configured, THEN rrev invokes that script with the review prompt and treats its standard output as the external tool's findings.
- External review disabled: WHEN the external review tool is set to none, THEN the pipeline runs the comprehensive and final phases only, and reports that the external phase was skipped.

**Requirement: Live progress reporting.** While an executor runs, rrev SHALL stream its activity to the terminal so the user can see progress rather than an unexplained pause. Output MUST be attributed to the phase that produced it, and long-running sub-agent activity MUST produce periodic indication that work is still in progress.

- Streaming output: WHEN an executor emits incremental output, THEN rrev renders it as it arrives, prefixed or colored to identify the current phase.
- Silent sub-agent work: WHEN the tool spends a long stretch inside sub-agents without emitting reportable text, THEN rrev emits a throttled progress indication at a bounded interval.
- Debug output: WHEN debug output is enabled, THEN rrev additionally records the resolved command line and the full prompt sent to the tool.

**Requirement: Signal detection.** rrev SHALL detect termination signals emitted by the executor in its output and use them to decide the phase outcome. The recognized signals MUST include one for a review iteration that found nothing, one for an external review loop reaching agreement, and one for an unrecoverable failure. Output containing no signal MUST be treated as "work was done, iterate again" rather than as success.

- Review-done signal: WHEN the output contains the review-done marker, THEN the phase is treated as converged and does not iterate again.
- No signal emitted: WHEN the executor completes without emitting any recognized marker, THEN the phase runs another iteration, up to its iteration limit.
- Failure signal: WHEN the executor emits the failure marker, THEN rrev stops the pipeline and reports the failing phase with the executor's explanation.
- Marker inside quoted text: WHEN a marker appears only inside code the executor is quoting rather than as its own emitted line, THEN rrev does not treat it as a signal.

**Requirement: Model and effort selection.** rrev SHALL let the model and reasoning effort be chosen per phase, expressed as a combined specification where either part may be omitted and inherits the configured default. An effort level unsupported by the selected tool MUST be reported and ignored rather than passed through.

- Model and effort both set: WHEN a phase specifies both, THEN the executor invokes the tool with both.
- Effort only: WHEN a phase specifies only an effort level, THEN the model resolves from the configured default and only the effort is overridden.
- Review model falls back: WHEN no review model is configured, THEN review phases use the primary executor's configured model.
- Unsupported effort: WHEN an effort level the tool does not accept is requested, THEN rrev warns naming the level and the tool, and proceeds with the tool's default effort.

**Requirement: Execution timeouts.** rrev SHALL support two independent bounds on an executor call: a total session timeout and an idle timeout that expires only when no output has arrived for the configured duration. Both MUST default to disabled, and a timeout MUST terminate the tool's process group and surface a distinguishable error.

- Session timeout: WHEN a session timeout is configured and a call exceeds it, THEN rrev terminates the tool and reports that the session timed out.
- Idle timeout resets: WHEN an idle timeout is configured and the tool keeps producing output, THEN the idle countdown restarts on each line and the call is not terminated.
- Idle timeout fires: WHEN the tool produces no output for longer than the idle timeout, THEN rrev terminates the tool and reports that the session went idle, preserving the output captured so far.
- Timeouts disabled: WHEN neither is configured, THEN the call runs until the tool exits or the context is cancelled.

**Requirement: Rate-limit and transient failure handling.** rrev SHALL recognize provider rate-limit and transient-failure responses in executor output and MUST distinguish them from a substantive review result, so a throttled call is not mistaken for a clean review.

- Rate limit hit: WHEN the tool reports that a usage limit was reached, THEN rrev surfaces a rate-limit error naming the tool, and the phase does not record the call as a converged iteration.
- Retryable failure: WHEN the tool reports a transient failure it suggests retrying, THEN rrev reports it as retryable and the calling phase may re-run the iteration within its limit.

### Capability: review-pipeline

Source: `openspec/changes/add-spec-review-pipeline/specs/review-pipeline/spec.md`

**Requirement: Review target.** Every phase SHALL review the changes on the current branch relative to the resolved base ref, comparing them against the selected change's requirement checklist. rrev MUST tell reviewers how to obtain the diff rather than embedding the diff in the prompt.

- Diff against base: WHEN the pipeline starts on a branch ahead of the base ref, THEN every phase reviews the three-dot diff between the base ref and HEAD, together with the branch's commit log.
- Base ref auto-detected: WHEN none is configured or passed, THEN rrev detects the repository's default branch and uses it.
- Empty diff: WHEN the branch has no changes relative to the base ref, THEN rrev reports that there is nothing to review and exits successfully without invoking an executor.
- Diff kept out of prompts: WHEN a phase prompt is assembled, THEN it contains the commands that produce the diff, not the diff text itself.

**Requirement: Comprehensive review phase.** The first phase SHALL launch a set of independent reviewer agents concurrently against the branch diff, then have the executor deduplicate their findings, verify each against the actual code, fix the confirmed ones, run the project's validation commands, and commit. The agent set MUST include reviewers for spec conformance and for task completeness in addition to general code quality.

- Agents run concurrently: WHEN the phase starts, THEN all configured reviewer agents for that phase are launched in a single message and the phase waits for all of them before evaluating findings.
- Conformance reviewed: WHEN the conformance agent runs, THEN it evaluates the diff against each requirement and scenario in the change's checklist and reports requirements that are unimplemented, partially implemented, or contradicted.
- Task completeness reviewed: WHEN the change's task list marks tasks complete, THEN the task agent verifies each against the diff and reports any marked complete without corresponding implementation.
- Findings verified before fixing: WHEN agents report findings, THEN the executor reads the code at each cited location, discards findings it cannot confirm, and fixes only the confirmed ones.
- Fixes committed: WHEN the executor fixes confirmed findings, THEN it runs the configured validation commands and commits the fixes before the phase iterates.
- Phase converges: WHEN an iteration completes with no confirmed findings, THEN the executor emits the review-done signal and the phase ends.

**Requirement: External review loop.** The second phase SHALL alternate an independent external review tool with the primary executor: the external tool reviews the diff against the same spec context and reports findings, then the executor evaluates them, fixes what it confirms, and commits. The loop MUST repeat until it terminates on one of its defined conditions.

- Findings evaluated, not applied blindly: WHEN the external tool reports findings, THEN the executor verifies each against the code and rejects the ones it determines to be false positives, recording the rejection and its reason.
- External tool reports nothing: WHEN the external tool reports no issues, THEN the loop terminates as converged.
- Previous rounds carried forward: WHEN the loop runs a second or later iteration, THEN the external tool's prompt includes the prior rounds' findings and their dispositions, so it does not re-report what was already rejected with reason.
- Skipped for same-model self-review: WHEN the primary executor and the external review tool would be the same model, THEN the phase is skipped and the pipeline reports why.

**Requirement: Loop termination.** Each review loop SHALL terminate on the first of: the convergence signal, its iteration limit, a detected stalemate, an unrecoverable executor failure, or a user break. rrev MUST report which condition ended the loop.

- Iteration limit: WHEN a loop reaches its configured maximum iterations without converging, THEN it stops, reports that the limit was reached, and the run's exit status reflects non-convergence.
- Stalemate detected: WHEN stalemate patience is configured and the configured number of consecutive iterations produce no new commit and no working-tree change, THEN the loop terminates early and reports the stalemate.
- Patience disabled: WHEN stalemate patience is not configured, THEN unchanged iterations do not by themselves terminate the loop.
- Termination reason reported: WHEN any loop ends, THEN rrev prints the terminating condition and the iteration count, and records both in the progress log.

**Requirement: Final review phase.** After the external loop, the pipeline SHALL run a narrower review restricted to critical and major issues, to catch regressions introduced by fixes applied during earlier phases. It MUST be skipped when the external loop found nothing on its first pass, since no fixes were applied that could have regressed anything.

- Regression pass runs: WHEN earlier phases applied fixes, THEN the final phase reviews the branch again and iterates until it reports no critical or major issues.
- Minor issues ignored: WHEN the final phase encounters a style or minor issue, THEN it is not fixed and does not prevent the phase from converging.
- Skipped when nothing changed: WHEN the external loop converged on its first iteration with no fixes applied, THEN the final phase is skipped and the pipeline reports it as unnecessary.

**Requirement: Report-only runs.** In report-only mode the pipeline SHALL run its reviewers and verification steps but MUST NOT modify tracked files or create commits. It MUST emit a findings report listing each verified finding with its location, severity, source reviewer, and the requirement it relates to when applicable.

- No repository mutation: WHEN the pipeline runs in report-only mode, THEN the working tree and commit history are unchanged when the run ends.
- Report emitted: WHEN verified findings exist at the end of a report-only run, THEN rrev writes a report containing each finding's file and line, severity, reporting reviewer, and related requirement.
- Loops do not iterate on fixes: WHEN report-only mode is active, THEN each review phase runs a single pass, since there are no fixes for a subsequent iteration to verify.

**Requirement: Finalize step.** rrev SHALL support an optional finalize step that runs once after all review phases converge. It MUST be disabled by default, MUST be driven by an overridable prompt, and its failure MUST NOT change the run's outcome.

- Disabled by default: WHEN finalize is not enabled in configuration, THEN the pipeline ends after the last review phase.
- Enabled and successful: WHEN finalize is enabled and all review phases converged, THEN rrev runs the finalize prompt once through the primary executor.
- Finalize fails: WHEN the finalize step fails, THEN rrev logs the failure and still reports the run as successful.
- Not reached on non-convergence: WHEN a review phase ended without converging, THEN finalize does not run.

### Capability: progress-log

Source: `openspec/changes/add-spec-review-pipeline/specs/progress-log/spec.md`

**Requirement: Progress log lifecycle.** rrev SHALL maintain one progress log per run, stored under the project's `.rrev/progress/` directory and named so that concurrent runs against different changes do not collide. The directory MUST be created when missing, and its contents MUST be excluded from version control by default.

- Log created: WHEN a run starts and no progress directory exists, THEN rrev creates it and opens a progress log identified by the change under review.
- Existing log reused: WHEN a run starts for a change that already has a progress log, THEN rrev appends to it, preserving the prior run's history.
- Not version controlled: WHEN the progress directory is created, THEN it contains an ignore rule so progress logs are not committed by the pipeline's own commits.
- Directory unwritable: WHEN the progress directory cannot be created or written, THEN rrev reports the failure and continues the review with logging disabled rather than aborting the run.

**Requirement: Recorded content.** The progress log SHALL record enough for a later reader to reconstruct the run: the change and goal under review, the base ref, each phase and iteration boundary, the findings reported, which were confirmed and fixed, which were rejected and why, validation and commit outcomes, and each loop's termination reason.

- Iteration boundaries recorded: WHEN a phase begins an iteration, THEN the log records the phase, the iteration number, and a timestamp.
- Rejections recorded with reason: WHEN the executor rejects a reported finding as a false positive, THEN the log records the finding and the reason it was rejected.
- Termination recorded: WHEN a loop ends, THEN the log records the terminating condition and the number of iterations run.

**Requirement: Log as reviewer context.** rrev SHALL make the progress log path available to every phase prompt and MUST instruct reviewers to consult it before reporting, so that findings already rejected with a stated reason are not re-reported unchanged.

- Path exposed to prompts: WHEN a phase prompt is assembled, THEN it contains the resolved progress log path and an instruction to read prior iterations before reporting.
- Repeat findings suppressed: WHEN an external review iteration would report a finding the log records as rejected with a reason, THEN the reviewer is instructed to either accept the recorded reason or state why it is wrong, rather than re-reporting it identically.

**Requirement: Concurrent write safety.** rrev SHALL serialize writes to a progress log so that concurrent rrev processes appending to the same file produce interleaved-but-intact entries rather than corrupted ones.

- Two runs append: WHEN two rrev processes append to the same progress log, THEN each entry is written whole, with no entry partially overwriting another.
- Lock unavailable: WHEN a write lock cannot be acquired within a bounded wait, THEN rrev reports the contention and continues the review rather than blocking the pipeline indefinitely.

### Design decisions carried over

- Signals use the `RREV:` prefix (`<<<RREV:REVIEW_DONE>>>`, `<<<RREV:EXTERNAL_DONE>>>`, `<<<RREV:TASK_FAILED>>>`). The load-bearing property is that **absence of a signal means iterate again**, not success. The detector matches a marker on its own line so a model quoting the protocol does not terminate a loop.
- Two reviewer agents exist that only make sense with a spec: `conformance` (classifies the diff against each scenario, requiring a file:line citation for every "satisfied" verdict - uncited verdicts are treated as "not addressed") and `tasks` (cross-checks task checkboxes against the diff). The five language-agnostic agents adapted from ralphex are quality, implementation, testing, simplification, documentation.
- The requirement checklist is expanded inline into prompts; the diff is not - reviewers are told to run `git diff` themselves. The checklist truncates at a configured budget and says so explicitly rather than silently dropping requirements.
- Requirement extraction prefers `openspec show --json` and falls back to a markdown parser, both producing the same structure. The parser is the path that can silently under-extract, so the two are cross-checked in tests.
- Config format is INI-style `key = value` with no inline comments, so unquoted `#` in color values parses correctly.

## Implementation Steps

### Task 1: Project bootstrap

- [x] initialize the Go module as `github.com/korthane/rrev` with the current stable toolchain and create the `cmd/rrev/` and `pkg/` skeleton (openspec, config, executor, processor, processor/phase, progress, git, status)
- [x] add Makefile targets for build, test, lint, and coverage
- [x] add the linter configuration and confirm it runs clean on the skeleton
- [x] add a CI workflow running build, test, and lint on push, and validate the workflow file with a workflow linter
- [x] add LICENSE, .gitignore, and a README stub crediting ralphex (MIT, Umputun) as the origin of the pipeline mechanic, naming the derived prompt and agent files
- [x] write a placeholder test proving the test target executes
- [x] run project tests - must pass before next task

### Task 2: Git integration

- [x] implement default-branch detection for the review base ref, handling repositories whose default branch is neither `main` nor `master`
- [x] implement diff retrieval, commit-log retrieval, HEAD hash, and a working-tree diff fingerprint
- [x] implement empty-diff detection so a branch with no changes relative to the base ref is reported as nothing to review without invoking an executor
- [x] write tests for new functionality, using a fixture repository created inside the test
- [x] run project tests - must pass before next task

### Task 3: OpenSpec change context

- [x] implement change discovery through the openspec CLI, excluding archived changes from auto-detection
- [x] implement the filesystem fallback enumerating the changes directory when the CLI is absent, noting the degraded mode in output
- [x] implement artifact loading for proposal, design, delta specs, and tasks, degrading gracefully on a missing optional artifact and failing with the filename on an unreadable one
- [x] implement the delta-spec markdown parser producing requirements with capability path, delta operation, name, and scenarios
- [x] implement requirement extraction through the openspec CLI JSON output as the preferred path
- [x] implement goal derivation from the proposal with a fallback to the change name
- [x] assemble the immutable review context resolved once per run, carrying change name, goal, artifact paths, and the requirement checklist
- [x] write tests for new functionality, including a cross-check asserting the CLI and parser paths extract identical requirement and scenario counts for the same fixture
- [x] run project tests - must pass before next task

### Task 4: Configuration resolution and defaults

- [x] implement layered config resolution across flags, project directory, user directory, and embedded defaults, ensuring a partially populated source never zeroes a lower-precedence value
- [x] implement the INI-style parser that rejects malformed input with file and line in the error and never silently falls back to defaults
- [x] embed the default config, prompts, and agents into the binary so a run with no config files on disk resolves every setting
- [x] implement per-file prompt and agent override lookup across the three sources, so overriding one file leaves the rest on embedded defaults
- [x] write tests for new functionality
- [x] run project tests - must pass before next task

### Task 5: Configuration templating and conflict detection

- [x] implement template variable expansion for change name, artifact paths, goal, requirement checklist, progress log path, base ref, and diff instruction, erroring with the filename on an unknown variable
- [x] implement requirement-checklist truncation at a configured budget that states in the prompt that truncation occurred
- [x] implement executor-aware agent reference expansion producing claude subagent invocations and codex spawn calls from one prompt file, erroring on an unresolvable agent name
- [x] implement incompatible-option detection: a hard startup error for conflicting flags, and a warn-and-override path for a conflict present only in config files
- [x] write tests for new functionality
- [x] run project tests - must pass before next task

### Task 6: Executor contract and implementations

- [x] define the executor interface returning output, detected signal, and error, with a mock implementation available to phase tests
- [x] implement signal detection for the review-done, external-done, and failure markers, matching a marker on its own line and ignoring one embedded in quoted text
- [x] implement the claude executor invoking the claude CLI with streamed JSON output, rendering incremental text and tolerating unknown event types
- [x] implement the codex executor invoking the codex CLI with config overrides
- [x] implement the custom executor running a user-supplied external review script and treating its stdout as findings
- [x] write tests for new functionality, using recorded fixture streams for both CLI executors
- [x] run project tests - must pass before next task

### Task 7: Executor models, lifecycle, and resilience

- [x] implement model and effort selection parsing the combined `model[:effort]` form with per-part inheritance, review falling back to the task model, and a warning for an unsupported effort level
- [x] implement context cancellation terminating the child process group so no orphaned process survives
- [x] implement session and idle timeouts, both disabled by default, with the idle countdown resetting on output and a distinguishable error preserving captured output
- [x] implement rate-limit and retryable-failure detection so neither is recorded as a converged iteration
- [x] implement throttled progress indication during long silent sub-agent work
- [x] write tests for new functionality, including a test asserting the process tree is gone after cancellation
- [x] run project tests - must pass before next task

### Task 8: Progress log

- [x] implement progress log creation under the project progress directory, named per change, creating the directory and its ignore rule when missing
- [x] implement append-and-reuse so a second run against the same change preserves prior history
- [x] implement structured entry writing for phase and iteration boundaries, findings, confirmations, rejections with reason, validation and commit outcomes, and termination reasons
- [x] implement file locking so concurrent appends interleave whole entries
- [x] implement graceful degradation when the progress directory is unwritable, continuing the review with logging disabled
- [x] implement a bounded lock wait that reports contention and continues rather than blocking indefinitely
- [x] write tests for new functionality, including a concurrent-writer test asserting no partial entry
- [x] run project tests - must pass before next task

### Task 9: Reviewer agents

- [x] write the conformance agent that classifies the diff against each requirement scenario as satisfying, partially satisfying, contradicting, or not addressing it, requiring a file and line citation for every satisfied verdict and treating an uncited one as not addressed
- [x] write the tasks agent that cross-checks task-list checkboxes against the diff and reports any marked complete without corresponding implementation
- [x] adapt the quality, implementation, testing, simplification, and documentation agents from ralphex defaults, recording attribution
- [x] write tests asserting every shipped agent definition is discoverable and non-empty
- [x] run project tests - must pass before next task

### Task 10: Phase prompts

- [x] write the comprehensive review prompt launching all phase-one agents in one message, then deduplicating, verifying against real code, fixing, validating, and committing
- [x] write the external review prompt carrying the requirement checklist, the progress log instruction, and prior-round findings with their dispositions
- [x] write the external findings evaluation prompt that verifies each reported finding before fixing and records rejections with reasons
- [x] write the final review prompt restricted to critical and major issues, using the quality, implementation, and conformance agents only
- [x] write the default finalize prompt, inert when finalize is disabled
- [x] state the signal contract in every prompt, spelling out that absence of a signal means iterate again
- [x] write tests asserting every embedded prompt expands with no unknown variables for both executors and that each contains the signal contract
- [x] run project tests - must pass before next task

### Task 11: Review phases

- [x] implement the comprehensive review phase running its agents concurrently and iterating until the review-done signal
- [x] implement the external review loop alternating the external tool and the primary executor, carrying prior findings and dispositions forward
- [x] implement the skip of the external phase when the primary executor and external tool would be the same model, reporting it as skipped
- [x] implement loop termination on signal, iteration limit, stalemate, executor failure, and user break, reporting which condition ended the loop
- [x] implement stalemate detection over consecutive iterations with no new commit and no working-tree change, honouring a disabled patience setting
- [x] implement the final review phase including its skip when the external loop converged on the first pass with no fixes applied
- [x] write tests for new functionality using mock executors across converging and non-converging outputs, with a case per termination condition
- [x] run project tests - must pass before next task

### Task 12: Pipeline modes, reporting, and runner

- [x] implement the optional finalize step running once, disabled by default, best-effort on failure, and skipped on non-convergence
- [x] implement report-only mode short-circuiting every loop to a single pass and leaving the working tree and commit history unchanged
- [x] implement the findings report emitting file, line, severity, reporting reviewer, and related requirement for each verified finding
- [x] implement the runner mapping each run mode to its phase sequence for full, external-only, first-phase-only, and report-only
- [x] write tests for new functionality, asserting the executed phase sequence per mode
- [x] run project tests - must pass before next task

### Task 13: CLI flags, change selection, and preflight

- [x] implement flag parsing for the positional change name, run modes, and per-run overrides, with an invalid value failing before any phase
- [x] implement change selection with single-change auto-detection, an ambiguity error listing candidates, and an unknown-name error
- [x] implement mutual exclusion of run modes as a startup error naming both conflicting flags
- [x] implement startup preflight for git repository, base ref resolution, change readability, and presence of every executable to be invoked
- [x] write tests for new functionality, including a case per preflight failure asserting no phase runs
- [x] run project tests - must pass before next task

### Task 14: CLI output, exit status, and signal handling

- [x] implement the startup banner reporting change, base ref, mode, executors, resolved models, and the extracted requirement count
- [x] implement exit statuses distinguishing convergence, non-convergence, and startup or abort failure
- [x] implement interrupt handling that aborts the run, terminates the process group, flushes the progress log, and exits non-zero
- [x] implement the break signal ending only the external loop on platforms that support it, omitting the hint where unsupported
- [x] implement phase-attributed coloured terminal output honouring a no-colour option
- [x] write tests for new functionality, including output assertions and a platform-guarded case for the break signal
- [x] run project tests - must pass before next task

### Task 15: Integration and documentation

- [x] add an end-to-end test running the full pipeline against a fixture OpenSpec repository with scripted mock executors, asserting the phase sequence, commits, and exit status
- [x] add an end-to-end test for report-only mode asserting a report is produced and the repository is unmodified
- [x] add an end-to-end test asserting a conformance gap in the fixture is reported against the specific requirement it violates
- [x] write the README covering installation, prerequisites, run modes, configuration, prompt and agent customization, and the signal contract
- [x] verify every flag documented in the README exists in the CLI and every CLI flag is documented
- [x] run project tests - must pass before next task

### Task 16: Verify acceptance criteria

- [ ] verify all requirements from Overview are implemented
- [ ] verify each capability in Technical Details has corresponding implementation and test coverage
- [ ] run full project test suite
- [ ] run project linter - all issues must be fixed

## Post-Completion

*Items requiring manual intervention - no checkboxes, informational only*

- Run the full pipeline manually against a real OpenSpec change with the real claude and codex executors, and record the outcome in the README. This needs live credentials and human judgement about review quality, so it is deliberately not an autonomous task.
- Confirm the conformance agent's citation discipline holds against a real change: spot-check a handful of "satisfied" verdicts and confirm the cited file and line actually support them. This is the behaviour most likely to degrade silently.
- Decide whether reviewer findings should become structured JSON rather than prose (deferred open question from the change's design document).
- Decide whether a converged run should offer to run `openspec archive` (deferred open question from the change's design document).
- The requirement text in Technical Details is copied from the change's delta specs. If either side is edited later, re-sync from `openspec/changes/add-spec-review-pipeline/specs/`.
