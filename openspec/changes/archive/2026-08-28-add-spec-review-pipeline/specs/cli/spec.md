## Purpose

Defines the `rrev` command surface: how a user selects the OpenSpec change to review, chooses a run mode, overrides defaults for a single run, and interprets what rrev reports back on exit.

## ADDED Requirements

### Requirement: Change selection
`rrev` SHALL accept an optional OpenSpec change name as its sole positional argument and review the current branch against that change. When the argument is omitted, rrev SHALL resolve the change automatically and MUST refuse to guess when the choice is ambiguous.

#### Scenario: Explicit change name
- **WHEN** the user runs `rrev add-user-auth` in a repository containing the change `add-user-auth`
- **THEN** rrev reviews the branch against that change's artifacts

#### Scenario: Single active change auto-detected
- **WHEN** the user runs `rrev` with no argument and exactly one active change exists
- **THEN** rrev selects that change and reports the selection before the first phase starts

#### Scenario: Ambiguous selection
- **WHEN** the user runs `rrev` with no argument and more than one active change exists
- **THEN** rrev exits with a usage error that lists the available change names and does not start any phase

#### Scenario: Unknown change name
- **WHEN** the user names a change that does not exist
- **THEN** rrev exits with an error naming the change and listing the available ones

### Requirement: Run modes
`rrev` SHALL support run modes that select where the pipeline starts and whether it may modify the repository. Modes MUST be mutually exclusive, and combining two of them MUST fail at startup rather than silently preferring one.

#### Scenario: Default full pipeline
- **WHEN** the user runs `rrev` with no mode flag
- **THEN** rrev runs the comprehensive review phase, the external review loop, the final review phase, and the finalize step if enabled

#### Scenario: External-only
- **WHEN** the user passes `--external-only`
- **THEN** rrev skips the comprehensive review phase and starts at the external review loop, followed by the final review phase

#### Scenario: First phase only
- **WHEN** the user passes `--phase1-only`
- **THEN** rrev runs only the comprehensive review phase and exits after it converges

#### Scenario: Report-only
- **WHEN** the user passes `--report-only`
- **THEN** rrev collects verified findings, writes them to the configured report destination, and exits without modifying tracked files or creating commits

#### Scenario: Conflicting modes rejected
- **WHEN** the user passes both `--external-only` and `--phase1-only`
- **THEN** rrev exits with an error naming both flags and runs no phase

### Requirement: Per-run overrides
`rrev` SHALL expose flags that override configuration for a single run, including the base ref for review diffs, the primary executor, per-phase model and effort, iteration limits, stalemate patience, the external review tool, and session and idle timeouts. A flag value MUST take precedence over any configuration file value.

#### Scenario: Base ref override
- **WHEN** the user passes `--base-ref develop`
- **THEN** every phase reviews `git diff develop...HEAD` instead of the diff against the auto-detected default branch

#### Scenario: Flag beats config
- **WHEN** the configuration file sets a review model and the user passes `--review-model` on the command line
- **THEN** the flag value is used and the configured value is ignored

#### Scenario: Invalid override value
- **WHEN** the user passes an unrecognized value to `--external-review-tool`
- **THEN** rrev exits with an error naming the flag and its accepted values, before starting any phase

### Requirement: Startup preflight
`rrev` SHALL validate its environment before starting the first phase and MUST fail fast with an actionable message rather than starting a phase that cannot succeed. Preflight MUST verify that the working directory is inside a git repository, that the base ref resolves, that the selected change exists and has readable artifacts, and that every executable it intends to invoke is present on `PATH`.

#### Scenario: Not a git repository
- **WHEN** rrev starts outside a git repository
- **THEN** it exits with an error stating that a git repository is required and runs no phase

#### Scenario: Missing executor binary
- **WHEN** the configured primary executor command is not found on `PATH`
- **THEN** rrev exits with an error naming the missing command and how it was configured

#### Scenario: Base ref unresolvable
- **WHEN** the resolved base ref does not name a reachable commit
- **THEN** rrev exits with an error naming the ref and suggesting `--base-ref`

#### Scenario: Preflight passes
- **WHEN** all preflight checks succeed
- **THEN** rrev prints a startup banner naming the change, base ref, mode, executors, and resolved models, then starts the first phase

### Requirement: Exit status
`rrev` SHALL communicate the run outcome through its exit status: zero when the pipeline converged, and a distinct non-zero status for a run that ended without converging versus a run that failed to start or aborted.

#### Scenario: Clean convergence
- **WHEN** every executed phase reports no remaining findings
- **THEN** rrev exits with status 0

#### Scenario: Unconverged run
- **WHEN** a phase exhausts its iteration limit or terminates on a stalemate while findings remain
- **THEN** rrev exits with a non-zero status and prints which phase did not converge and why

#### Scenario: Executor failure
- **WHEN** the executor reports that it found issues it cannot fix
- **THEN** rrev stops the pipeline and exits with a non-zero status, reporting the phase that failed

### Requirement: Interactive interruption
`rrev` SHALL respond to interruption while a phase is running. An abort request MUST stop the run and terminate the child process group; a break request MUST end only the current review loop and let the pipeline continue with the next phase.

#### Scenario: Abort
- **WHEN** the user sends an interrupt (Ctrl+C) during any phase
- **THEN** rrev cancels the running executor, terminates its process group, writes what it has to the progress log, and exits with a non-zero status

#### Scenario: Break the external loop
- **WHEN** the user sends a break signal (Ctrl+\) during the external review loop on a platform that supports it
- **THEN** rrev cancels the current executor call, ends the loop, and continues with the final review phase

#### Scenario: Break unsupported
- **WHEN** the platform provides no break signal
- **THEN** rrev omits the break hint from its output and the loop terminates only on its own conditions
