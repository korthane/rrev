## 1. Project Bootstrap

- [ ] 1.1 Initialize the Go module as `github.com/korthane/rrev` with the current stable toolchain, create the `cmd/rrev/` and `pkg/` skeleton from design.md's layout, and verify `go build ./...` succeeds
- [ ] 1.2 Add `Makefile` targets for build, test, lint, and coverage, and verify `make build` and `make test` both succeed on the empty skeleton
- [ ] 1.3 Add `.golangci.yml` and verify `make lint` runs clean
- [ ] 1.4 Add a GitHub Actions workflow running build, test, and lint on push, and verify it with `actionlint`
- [ ] 1.5 Add `LICENSE`, `.gitignore`, and a `README.md` stub crediting ralphex (MIT, Umputun) as the origin of the pipeline mechanic, and verify the attribution note names the derived prompt and agent files

## 2. Git Integration

- [ ] 2.1 Implement `pkg/git` base-ref detection for the repository's default branch with unit tests covering `main`, `master`, and a repo whose default branch is neither
- [ ] 2.2 Implement diff retrieval, commit-log retrieval, HEAD hash, and a working-tree diff fingerprint, verifying each against a fixture repository created in the test
- [ ] 2.3 Implement empty-diff detection so a branch with no changes relative to the base ref is reported as nothing to review, verified by a test asserting no executor is invoked

## 3. OpenSpec Change Context

- [ ] 3.1 Implement change discovery via the `openspec` CLI, and verify against a fixture repository that active changes are listed and archived ones are excluded
- [ ] 3.2 Implement the filesystem fallback that enumerates `openspec/changes/` when the CLI is absent, and verify both paths return the same change set for the same fixture
- [ ] 3.3 Implement artifact loading for proposal, design, delta specs, and tasks, verifying that a missing design document degrades gracefully and an unreadable artifact fails with the file named
- [ ] 3.4 Implement the delta-spec markdown parser producing requirements with capability path, delta operation, name, and scenarios; verify with table tests covering added/modified/removed/renamed sections and a requirement with no scenarios
- [ ] 3.5 Implement requirement extraction via `openspec show --json` and add a test asserting the CLI and parser paths extract the same requirement and scenario counts for the same fixture
- [ ] 3.6 Implement goal derivation from the proposal with a fallback to the change name, verified by tests over both cases
- [ ] 3.7 Assemble the immutable `ReviewContext` returned once per run, and verify by test that it carries change name, goal, artifact paths, and the requirement checklist

## 4. Configuration

- [ ] 4.1 Implement layered config resolution (flags → `.rrev/` → `~/.config/rrev/` → embedded defaults) with tests proving a partially populated file does not zero out lower-precedence values
- [ ] 4.2 Implement the INI-style parser rejecting malformed input with file and line in the error, verified by a test asserting no silent fallback to defaults
- [ ] 4.3 Embed the default config, prompts, and agents into the binary and verify a run with no config files on disk resolves every setting
- [ ] 4.4 Implement per-file prompt and agent override lookup across the three sources, verified by a test that overrides one prompt and asserts the rest still resolve to embedded defaults
- [ ] 4.5 Implement template variable expansion for change name, artifact paths, goal, requirement checklist, progress log path, base ref, and diff instruction, with an error naming the file for an unknown variable
- [ ] 4.6 Implement requirement-checklist truncation at a configured budget that states in the prompt that truncation occurred, verified by a test over an oversized checklist
- [ ] 4.7 Implement executor-aware `{{agent:<name>}}` expansion producing claude subagent invocations and codex spawn calls from one prompt file, with an error for an unresolvable agent name
- [ ] 4.8 Implement incompatible-option detection: hard error for conflicting flags, warn-and-override for a config-only conflict, verified by tests over both

## 5. Executors

- [ ] 5.1 Define the `Executor` interface returning output, detected signal, and error, and add a mock implementation for phase tests
- [ ] 5.2 Implement signal detection for `REVIEW_DONE`, `EXTERNAL_DONE`, and `TASK_FAILED`, verifying that a marker on its own line is detected and one embedded in quoted text is not
- [ ] 5.3 Implement `ClaudeExecutor` invoking the claude CLI with streamed JSON output, rendering incremental text and tolerating unknown event types, verified against recorded fixture streams
- [ ] 5.4 Implement `CodexExecutor` invoking the codex CLI with config overrides, verified against recorded fixture output
- [ ] 5.5 Implement `CustomExecutor` running a user-supplied external review script and treating its stdout as findings, verified with a script fixture
- [ ] 5.6 Implement model and effort selection parsing `model[:effort]` with per-part inheritance, review falling back to task model, and a warning for an effort level the tool does not support
- [ ] 5.7 Implement context cancellation terminating the child process group, verified by a test asserting the process tree is gone after cancel
- [ ] 5.8 Implement session and idle timeouts, both disabled by default, verified by tests asserting the idle countdown resets on output and that a timeout yields a distinguishable error preserving captured output
- [ ] 5.9 Implement rate-limit and retryable-failure detection in executor output, verified by tests asserting neither is recorded as a converged iteration
- [ ] 5.10 Implement throttled progress indication during long silent sub-agent work, verified by a test over a stream with a gap longer than the interval

## 6. Progress Log

- [ ] 6.1 Implement progress log creation under `.rrev/progress/` named per change, creating the directory and its ignore rule when missing, verified by a test over a fresh repository
- [ ] 6.2 Implement append-and-reuse for an existing log, verified by a test asserting prior history survives a second run
- [ ] 6.3 Implement structured entry writing for phase and iteration boundaries, findings, confirmations, rejections with reason, validation and commit outcomes, and termination reasons, verified by a test asserting each appears in the written log
- [ ] 6.4 Implement file locking so concurrent appends interleave whole entries, verified by a test running concurrent writers and asserting no partial entry
- [ ] 6.5 Implement graceful degradation when the progress directory is unwritable so the review continues with logging disabled, verified by a test using a read-only directory
- [ ] 6.6 Implement bounded lock-wait that reports contention and continues rather than blocking indefinitely, verified by a test holding the lock past the bound

## 7. Default Prompts and Agents

- [ ] 7.1 Write the `conformance` agent that classifies the diff against each requirement scenario as satisfying, partially satisfying, contradicting, or not addressing it, requiring a file:line citation for every satisfied verdict and treating an uncited one as not addressed
- [ ] 7.2 Write the `tasks` agent cross-checking `tasks.md` checkboxes against the diff
- [ ] 7.3 Adapt the quality, implementation, testing, simplification, and documentation agents from ralphex defaults, with attribution recorded per design.md
- [ ] 7.4 Write the comprehensive review prompt launching all phase-one agents in one message, then deduplicating, verifying against real code, fixing, validating, and committing, and verify the embedded prompt expands with no unknown variables
- [ ] 7.5 Write the external review prompt including the requirement checklist, the progress log instruction, and prior-round findings with dispositions, and verify it expands cleanly for both executors
- [ ] 7.6 Write the external findings evaluation prompt that verifies each reported finding before fixing and records rejections with reasons
- [ ] 7.7 Write the final review prompt restricted to critical and major issues with quality, implementation, and conformance agents only
- [ ] 7.8 Write the default finalize prompt and verify it is inert when finalize is disabled
- [ ] 7.9 Verify every embedded prompt states the signal contract that absence of a signal means iterate again, with a test asserting each prompt file contains it

## 8. Phase Orchestration

- [ ] 8.1 Implement the comprehensive review phase running its agents concurrently and iterating until the review-done signal, verified with mock executors over converging and non-converging outputs
- [ ] 8.2 Implement the external review loop alternating the external tool and the primary executor, carrying prior findings and dispositions forward, verified with mock executors across multiple iterations
- [ ] 8.3 Implement the skip of the external phase when the primary executor and external tool are the same model, verified by a test asserting the phase is reported as skipped
- [ ] 8.4 Implement loop termination on signal, iteration limit, stalemate, executor failure, and user break, verified by a test per condition asserting the reported reason
- [ ] 8.5 Implement stalemate detection over consecutive iterations with no new commit and no working-tree change, verified by tests with patience configured and disabled
- [ ] 8.6 Implement the final review phase including the skip when the external loop converged on its first pass with no fixes applied, verified by tests over both cases
- [ ] 8.7 Implement the optional finalize step running once, disabled by default, best-effort on failure, and skipped on non-convergence, verified by a test per case
- [ ] 8.8 Implement report-only mode short-circuiting every loop to a single pass, verified by a test asserting the working tree and commit history are unchanged
- [ ] 8.9 Implement the findings report emitting file, line, severity, reporting reviewer, and related requirement for each verified finding, verified against a fixture set of findings
- [ ] 8.10 Implement the `Runner` mapping each run mode to its phase sequence, verified by tests asserting the executed phases for full, external-only, phase1-only, and report-only

## 9. CLI

- [ ] 9.1 Implement flag parsing for the positional change name, run modes, and per-run overrides, verified by tests asserting a flag beats a configured value and an invalid value fails before any phase
- [ ] 9.2 Implement change selection including single-change auto-detection, an ambiguity error listing candidates, and an unknown-name error, verified by tests over all three
- [ ] 9.3 Implement mutual exclusion of run modes as a startup error naming both flags, verified by a test
- [ ] 9.4 Implement startup preflight for git repository, base ref resolution, change readability, and executable presence, verified by a test per failure mode asserting no phase runs
- [ ] 9.5 Implement the startup banner reporting change, base ref, mode, executors, resolved models, and the extracted requirement count, verified by an output test
- [ ] 9.6 Implement exit statuses distinguishing convergence, non-convergence, and startup or abort failure, verified by tests over each outcome
- [ ] 9.7 Implement interrupt handling that aborts the run, terminates the process group, flushes the progress log, and exits non-zero, verified by an integration test
- [ ] 9.8 Implement the break signal ending only the external loop on platforms that support it, and omitting the hint where unsupported, verified by a platform-guarded test
- [ ] 9.9 Implement phase-attributed colored terminal output honoring a no-color option, verified by an output test

## 10. Integration and Documentation

- [ ] 10.1 Add an end-to-end test running the full pipeline against a fixture OpenSpec repository with scripted mock executors, asserting the phase sequence, commits, and exit status
- [ ] 10.2 Add an end-to-end test for report-only mode asserting a report is produced and the repository is unmodified
- [ ] 10.3 Add an end-to-end test asserting a conformance gap in the fixture is reported against the specific requirement it violates
- [ ] 10.4 Run the full pipeline manually against a real OpenSpec change with real claude and codex executors and record the outcome in the README
- [ ] 10.5 Write the README covering installation, prerequisites, run modes, configuration, prompt and agent customization, and the signal contract, and verify every documented flag exists in the CLI
- [ ] 10.6 Verify `make lint`, `make test`, and the CI workflow all pass on the completed implementation
