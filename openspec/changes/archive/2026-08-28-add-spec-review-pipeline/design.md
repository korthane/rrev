## Context

The repository is empty apart from OpenSpec scaffolding, so this change bootstraps the whole program. See `proposal.md` for motivation.

The mechanic being ported already exists and works: ralphex (`~/src/ralphex`, MIT, Umputun) runs a review pipeline of parallel reviewer agents → external cross-model review loop → final regression pass, terminating on sentinel signals parsed out of executor output. Its packages (`pkg/executor`, `pkg/processor/phase`, `pkg/config`, `pkg/progress`) are a working reference for the hard parts — process-group termination, stream parsing, stalemate detection, prompt layering.

Two constraints shape the design. First, rrev has no dependency on the ralphex module: its packages are internal-facing and unversioned as a public API. Second, the driving artifact is different in kind. A ralphex plan is a free-form checklist that the same tool wrote and then executes; an OpenSpec change is a structured set of requirements and scenarios written before implementation, by a different process (`/opsx:propose`, then `/opsx:apply`). rrev never implements — it only judges an existing diff against those requirements.

## Goals / Non-Goals

**Goals:**
- Requirements and scenarios are first-class review criteria, not background prose. A reviewer is asked "does this diff satisfy scenario X of requirement Y", not "does this code look good".
- The two executors are symmetric: any phase can run under claude or codex, from the same prompt files.
- Everything a user would want to tune — prompts, agents, models, iteration bounds — is data, not code.
- A run is reconstructable after the fact from its progress log.

**Non-Goals:**
- Reusing or importing ralphex code. Prompts and agent text derived from ralphex defaults carry attribution; Go code is written fresh.
- Supporting OpenSpec schemas beyond `spec-driven` in this change. The artifact loader is schema-aware enough not to hard-fail on others, but conformance extraction targets `spec-driven`.
- Structured machine-readable findings from reviewers. Findings stay prose in v1; see Open Questions.

## Decisions

### Reimplement rather than depend on ralphex

Alternatives: import `github.com/umputun/ralphex/pkg/...`; vendor a copy.

Importing couples rrev to packages that are shaped for ralphex's own orchestration — `phase.Config` carries plan-file fields, signals are hardcoded to the `RALPHEX:` prefix, and `Runner.Mode` includes task and plan-creation modes rrev has no use for. Adapting around that costs more than the ~2k lines being replaced, and every upstream release risks a break. Vendoring avoids the break but leaves dead code and an ambiguous provenance story.

Reimplementing keeps the abstractions rrev actually needs. The reference stays useful as a source of hard-won details (process-group kill on cancel, the claude `stream-json` event shapes, the codex rollout-file tail for progress when stdout goes quiet) that are cheaper to read than to rediscover.

### Package layout

```
cmd/rrev/          flag parsing, signal wiring, exit codes
pkg/openspec/      change discovery, artifact loading, requirement extraction → ReviewContext
pkg/config/        layered resolution, embedded defaults, prompt/agent overrides, templating
pkg/executor/      Executor interface, ClaudeExecutor, CodexExecutor, CustomExecutor
pkg/processor/     Runner (mode → phase sequence)
pkg/processor/phase/  ComprehensivePhase, ExternalPhase, FinalPhase, FinalizePhase, loop control
pkg/progress/      run journal, file locking
pkg/git/           base-ref detection, diff, HEAD hash, diff fingerprint
pkg/status/        signal constants, phase sections, terminal rendering
```

The seam between `pkg/openspec` and everything else is `ReviewContext` — resolved once at startup, immutable, passed by value into every phase. That is what makes "same criteria in every phase" a structural property rather than a discipline.

It carries artifact *paths*, not frozen copies of their text: reviewers cite `file:line` in the live tree, and the fix step edits the change's own task list, so a phase reading a snapshot would cite lines that no longer exist and re-report tasks the previous iteration already unchecked. What is pinned is rrev's own resolution — goal, paths, checklist — which is never recomputed mid-run.

### Requirement extraction: parse markdown, don't shell out per-requirement

Alternatives: call `openspec show <change> --json --deltas-only` for structured requirements; parse the delta spec markdown directly.

`openspec show --json` gives exactly the structure needed and is the obvious first choice — so rrev uses it when the CLI is present. But rrev must also work without the CLI installed (a reviewer running in CI on a checkout, for instance), and the delta format is simple and stable enough to parse directly: `### Requirement:` headers, `#### Scenario:` sub-headers, `## ADDED|MODIFIED|REMOVED|RENAMED Requirements` operation sections.

So: CLI when available, markdown parser as fallback, both producing the same `[]Requirement`. The parser is the one that needs real test coverage, since it is the path that can silently under-extract. A requirement count mismatch between the two paths, when both are available, is worth asserting in tests.

### Signals: rename the prefix, keep the protocol

Signals become `<<<RREV:REVIEW_DONE>>>`, `<<<RREV:EXTERNAL_DONE>>>`, `<<<RREV:TASK_FAILED>>>`. The protocol's important property is inherited unchanged: **absence of a signal means "iterate again"**, not success. An executor that fixed something and stopped will be re-reviewed, because its fixes may have broken something else. Only an explicit done-signal ends a loop.

The detector matches a marker on its own line, not anywhere in the output, so a model quoting the protocol while explaining itself does not terminate the loop.

### Two new reviewer agents

ralphex's five agents (quality, implementation, testing, simplification, documentation) are language-agnostic and carry over. rrev adds two that only make sense with a spec:

- `conformance` — walks the requirement checklist and, per scenario, classifies the diff as satisfying, partially satisfying, contradicting, or not addressing it. This is the agent that justifies rrev's existence.
- `tasks` — cross-checks `tasks.md` checkboxes against the diff, catching tasks marked done during `/opsx:apply` that were not actually implemented.

`implementation` overlaps `conformance` but is not redundant: it judges whether code achieves its stated goal, while `conformance` judges against enumerated scenarios. Both run in the comprehensive phase; only `quality`, `implementation`, and `conformance` run in the final pass.

### The requirement checklist is expanded into the prompt; the diff is not

Reviewers are told to run `git diff` themselves — embedding a large diff into several parallel agent prompts is slow and expensive, and ralphex's prompts warn about exactly this. The requirement checklist is the opposite case: it is small, bounded by the change's size, and re-deriving it per agent would mean each agent parsing spec files independently and possibly differently. So `{{REQUIREMENTS}}` expands inline, `{{DIFF_INSTRUCTION}}` expands to a command.

For an unusually large change the checklist could still be big. The template expander truncates at a configured budget and says so explicitly in the prompt rather than silently dropping requirements — a reviewer that knows its checklist was cut can say so; one that does not will report false conformance.

### Report-only runs single-pass phases

Iteration exists to verify fixes. With no fixes, a second iteration would re-run identical reviewers on an identical diff. So report-only short-circuits every loop to one pass and collects findings instead of applying them. This means the report is *unverified-by-iteration* — it reflects one round of reviewer output after the executor's own verification step, which is the honest thing to promise.

### Config format

INI-style `key = value`, matching ralphex, with prompts and agents as plain text files in sibling directories. Alternatives: YAML or TOML. The repo's own OpenSpec config is YAML, which argues for consistency — but the values here are flat scalars, and the format's one real requirement is tolerating unquoted `#` in hex color values, which INI-with-no-inline-comments handles and YAML does not without quoting. Flat and boring wins.

## Risks / Trade-offs

- **Reviewers hallucinate conformance.** An agent told "check requirement X" will tend to find a way to say yes. → The conformance agent must cite file and line for each satisfied scenario; the executor's verification step reads those citations and rejects the unsupported ones. Uncited "satisfied" verdicts are treated as "not addressed".
- **Cost.** A full run is 7 parallel agents, plus an external loop, plus a final pass — several times a single review's token spend. → Per-phase model selection lets the expensive reviewers run on a strong model and the rest cheaper; `--phase1-only` and `--external-only` exist for partial runs.
- **Autonomous commits on the user's branch.** rrev fixes and commits without asking. → Fixes are committed separately with an identifiable message so they can be dropped; `--report-only` is the escape hatch; rrev never pushes unless a finalize prompt is configured to.
- **Spec parser under-extraction.** A silently dropped requirement means unreviewed behavior. → Cross-check against the `openspec show --json` path in tests; report the extracted requirement count in the startup banner so a wrong number is visible immediately.
- **Signal protocol drift.** Models sometimes paraphrase rather than emit a marker verbatim. → Absence-means-iterate makes the failure mode "one wasted iteration", not "false success". The iteration limit bounds the cost.
- **Loop non-convergence burning tokens.** Two models can disagree indefinitely. → Stalemate patience terminates on N iterations with no commit and no working-tree change; the iteration limit is the hard backstop.
- **Coupling to two CLIs rrev does not own.** Flag surfaces and output formats of `claude` and `codex` change. → The executor interface confines the coupling to two files; preflight verifies presence, and stream parsing tolerates unknown event types rather than failing on them.

## Open Questions

- Should reviewer findings become structured (JSON) rather than prose? Structure would make the report-only output machine-consumable and dedup exact rather than heuristic, but it constrains what a reviewer can express and every model complies with it imperfectly. Deferred: it changes prompt content and report rendering only, not the specs, the phase structure, or the task breakdown.
- Should a converged run offer to run `openspec archive`? It is a natural next step but belongs to the OpenSpec workflow, not the reviewer, and the finalize prompt can already do it for anyone who wants it.
