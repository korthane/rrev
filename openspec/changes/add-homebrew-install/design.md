## Context

See proposal.md - Why. Three facts about the current repository shape the
approach:

- There are no tags. Every decision below has to work from a standing start:
  there is no release history to stay compatible with, and no user installed
  through a channel that must keep working.
- rrev has no external Go dependencies. `go.mod` requires only `go 1.27.0`, and
  there is no `go.sum`. A source build therefore needs no module downloads,
  which is what makes a build-from-source formula viable inside Homebrew's
  build sandbox.
- CI already runs build, test, cross-build, and lint on `on: push` with no
  branch filter, which includes tag pushes. The release path can gate on that
  existing job rather than restating its steps.

Homebrew currently ships `go 1.27.0`, which satisfies `go.mod`.

## Goals / Non-Goals

**Goals:**

- One command a macOS user can run: `brew install korthane/tap/rrev`.
- A release that is a single act — push a tag — with the checks and the
  published Release following from it automatically.
- A version string that is true on every install path, so a bug report that
  quotes `rrev --version` identifies an actual commit.

**Non-Goals:**

- Prebuilt binaries, bottles, checksummed release assets, or a cross-compile
  matrix. The formula compiles from source.
- Automatic formula bumps. The tap is updated by hand, following a written
  procedure; automating it is a later change if releases become frequent.
- Linux packaging, `brew` on Linux, or any non-Homebrew package manager.
- Submission to homebrew-core. Its notability bar is unmet, and its rules
  discourage tools whose function is to drive other CLIs.

## Decisions

### A tap of our own, not homebrew-core

`korthane/homebrew-tap` holds `Formula/rrev.rb`; users install through
`korthane/tap/rrev`. homebrew-core would give the shorter `brew install rrev`,
but it requires roughly 75 stars/forks/watchers and reviews out tools that
mainly orchestrate other CLIs — rrev drives `claude` and `codex`. A tap has no
gate, and moving to core later is a strictly additive change.

Alternative considered: a `Formula/` directory in this repository, tapped
directly. It avoids a second repo, but the install line becomes
`brew tap korthane/rrev https://github.com/korthane/rrev && brew install rrev`,
and formula edits then land in the same history as the code they install.

### Build from source, not from release binaries

The formula declares `depends_on "go" => :build` and compiles the tagged source
archive. With no third-party modules, the build is hermetic: nothing is fetched
beyond the archive Homebrew already downloaded and verified. A user pays roughly
half a minute of compile time and keeps no Go installation afterwards.

Alternative considered: GoReleaser publishing darwin/arm64 and darwin/amd64
binaries, with the formula pointing at those assets. It installs instantly and
would be the right answer for a package with a heavy dependency tree, but it
adds a release toolchain, a cross-compile matrix, per-architecture checksums,
and a second place for a release to go wrong — to save 30 seconds on an install
that happens once.

### Version resolution: stamped in, then build info, then a placeholder

`rrev --version` already prints `main.version`, which the Makefile stamps
through `-ldflags`. That stays authoritative, since it is what the formula sets.
When nothing was stamped in, rrev reads the main module's version from its own
build information and uses that. Go records a real version there for a binary
installed from a tagged module, so `go install ...@v0.1.0` reports `v0.1.0`
instead of today's `dev`.

Build info reports `(devel)`, or nothing at all, when the toolchain has no
version to record; both are treated as absent, leaving the `dev` placeholder.
Since Go 1.24 an untagged `go build` records a pseudo-version instead —
`v0.0.0-<timestamp>-<commit>`, suffixed `+dirty` for a modified tree — which is
kept as-is: it names the exact commit, which is what a version in a bug report
is for, and no release could be mistaken for it. This ordering means a
stamped-in version always wins, so a build that deliberately labels itself is
never second-guessed by the module metadata.

### The release job gates on the existing CI job

CI's `on: push` already fires for tag pushes, so a `release` job in the same
workflow with `needs: build` and `if: startsWith(github.ref, 'refs/tags/v')`
reuses the checks verbatim rather than copying them into a second workflow that
can drift. The job creates the GitHub Release with `gh release create`, needs
`contents: write` at the job level only — the workflow's top-level
`permissions: contents: read` stays as the default for every other job — and
uses the CLI already present on GitHub runners rather than a third-party action.

Alternative considered: a separate `release.yml` triggered on `push: tags`. It
reads more explicitly, at the cost of duplicating build, test, and lint, or of
converting CI into a reusable workflow to call — more machinery than one job
and one `if` condition.

### The formula's test asserts the version it declares

`brew test` runs `rrev --version` and requires the formula's own version in the
output. This is the one check that catches the failure mode specific to
build-from-source Go formulas: a formula that forgets the ldflag installs a
binary that works perfectly and lies about what it is. rrev's other behavior
needs a git repository, an OpenSpec change, and two external CLIs, so a deeper
smoke test is not available to a formula test.

## Risks / Trade-offs

- **The tap repository does not exist yet, and creating it is outside this
  codebase.** → The change is sequenced so nothing here depends on it until the
  final step; the release procedure documents the tap's layout so it can be
  recreated. Until the tap exists and holds the formula, the README must not
  claim the brew command works.

- **Homebrew's `go` could fall behind `go.mod`'s `go 1.27.0`.** → It is at
  1.27.0 today, so the risk is a future bump of rrev's minimum. Go would then
  try to download a newer toolchain mid-build, which the build sandbox may
  block. Raising the `go` directive is therefore a decision that has to consider
  what Homebrew ships, and the release procedure says so.

- **A hand-updated formula drifts from the released version.** → The formula
  test fails when the installed binary does not report the formula's version,
  which catches a bumped url with a stale version but not a forgotten bump. The
  release procedure keeps the formula update in the same checklist as the tag.

- **The source archive's checksum is assumed stable.** → GitHub commits to
  stable checksums for source archives of a given tag; a tag must therefore
  never be moved once pushed, since a re-pointed tag invalidates the published
  formula. The procedure treats tags as immutable and fixes mistakes with a new
  patch version.

- **`v0.1.0` is a public commitment to a version number.** → It is deliberately
  below 1.0: the CLI surface, the config format, and the progress log format are
  still moving, and 0.x signals that.

## Migration Plan

No migration: no released version exists to migrate from. `go install
...@latest` keeps working unchanged and simply starts reporting a real version
once a tag exists.

Rollback, should the formula prove broken: `brew untap korthane/tap` on the
user's side, and the README's `go install` path remains the documented
fallback. A broken release is corrected with a new patch tag, never by moving
an existing one.
