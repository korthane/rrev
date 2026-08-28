## Why

Teams that plan with OpenSpec write their intent down as requirements and scenarios, then implement it — but nothing checks the resulting code against that intent. Reviews are either manual or generic ("find bugs"), so drift between an approved change and its implementation is only caught by whoever happens to read the diff carefully.

ralphex already proves the mechanic works: a loop that alternates independent AI reviewers with a fixing executor, iterating until the reviewers go quiet. But ralphex is driven by a free-form `docs/plans/*.md` plan file, which OpenSpec repositories do not have. rrev ports that mechanic to OpenSpec: same loop, but the source of truth is a named change's proposal, design, delta specs, and tasks.

## What Changes

- New Go CLI, `rrev`, that reviews the current branch against a named OpenSpec change and autonomously fixes and commits what it finds.
- **Change-driven review context**: rrev resolves an OpenSpec change (explicitly named, or auto-detected when exactly one is active), loads `proposal.md`, `design.md`, `specs/**/spec.md`, and `tasks.md`, and derives a review goal plus a requirement/scenario checklist from them. Delta specs become conformance criteria, not just background reading.
- **Three-phase review pipeline**, modeled on ralphex:
  - Phase 1 — comprehensive review. Parallel reviewer agents inspect `git diff <base>...HEAD`. Alongside ralphex's language-agnostic agents (quality, testing, simplification, documentation) rrev adds a `conformance` agent that checks the diff against the change's requirements and scenarios, and a `tasks` agent that verifies claimed-complete tasks are genuinely done.
  - Phase 2 — external review loop. An independent model (codex by default) reviews the diff with the spec context, the primary executor evaluates the findings, fixes what is real, and commits; the loop repeats until the external tool reports nothing, iterations run out, or a stalemate is detected.
  - Phase 3 — final pass. A narrower agent set re-reviews for critical/major regressions introduced by the fixes.
- **Optional finalize step**, disabled by default, for post-review automation (rebase, push, notify).
- **Two interchangeable executors**: claude and codex, either usable as the primary fixing executor. Prompts are shared; agent invocations expand to the executor's native mechanism (`Task` tool for claude, `spawn_agent` for codex). Selecting codex as the primary executor skips the external codex phase, since same-model self-review has weak signal.
- **Signal protocol** for phase termination — the executor emits sentinel markers (`<<<RREV:REVIEW_DONE>>>`, `<<<RREV:EXTERNAL_DONE>>>`, `<<<RREV:TASK_FAILED>>>`) that rrev parses out of the output stream to decide whether to iterate, stop, or fail.
- **Layered configuration**: CLI flags over `.rrev/` project config over `~/.config/rrev/` user config over embedded defaults. Prompts and reviewer agents ship embedded and can be overridden per project or per user.
- **Progress log** at `.rrev/progress/`, written across iterations so each fresh executor session and each external reviewer can read what prior rounds already found and rejected.
- **Run modes**: full pipeline (default), `--external-only` (skip phase 1), `--phase1-only`, and `--report-only` (collect verified findings without touching the working tree).

Non-goals for this change: task execution (rrev never implements a change — `openspec apply` does that), plan creation, the ralphex web UI, worktree isolation, and archiving the change on success.

## Capabilities

### New Capabilities
- `cli`: command surface, run modes, flags, preflight validation, exit codes, and terminal output.
- `configuration`: layered config resolution, embedded defaults, prompt and agent overrides, template variable expansion.
- `spec-context`: OpenSpec change resolution, artifact loading, requirement/scenario extraction, and the review context handed to every phase.
- `agent-execution`: claude and codex executor contracts — process invocation, output streaming, signal detection, model/effort selection, session and idle timeouts, cancellation.
- `review-pipeline`: three-phase orchestration, the external review loop, git state and stalemate detection, termination conditions, and the optional finalize step.
- `progress-log`: the cross-iteration run journal — location, format, concurrency, and how phases read and append to it.

### Modified Capabilities
<!-- None. This is the first change in the repository; there are no existing specs. -->

## Impact

- Bootstraps an empty repository: `go.mod` (module `github.com/korthane/rrev`), `cmd/rrev/`, `pkg/` (config, executor, openspec, processor, progress, git), embedded default prompts and agents, `Makefile`, `.golangci.yml`, and CI.
- **External tool dependencies at runtime**: `git`, the `claude` CLI, the `codex` CLI (unless the external phase is disabled), and the `openspec` CLI (used for change discovery and validation, with a filesystem fallback so rrev still works if it is absent).
- rrev writes to the repository it reviews: it creates commits on the current branch and appends to `.rrev/progress/`. It never pushes unless a finalize prompt is configured to.
- No dependency on the ralphex module. The design is ralphex-inspired (MIT, Umputun) and reimplemented; prompt and agent text derived from ralphex defaults carries attribution.
