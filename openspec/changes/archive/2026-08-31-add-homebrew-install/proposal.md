## Why

rrev has one install path — `go install github.com/korthane/rrev/cmd/rrev@latest` —
which requires a Go 1.27 toolchain and produces a binary that reports its version
as `dev`. macOS users expect `brew install`, and the project has no versioned
release for a formula to point at: the repository carries zero tags, so there is
no such thing as "rrev v0.1.0" today.

## What Changes

- Establish versioned releases: annotated `vX.Y.Z` tags on this repository, a
  release workflow that gates a tag on the same build/test/lint checks CI runs,
  and a documented procedure for cutting the next one.
- Cut `v0.1.0`, the first release, so an installable version exists.
- Publish a Homebrew formula in a new `korthane/homebrew-tap` repository. The
  formula builds from the tagged source tarball with a Go build dependency, so
  no release binaries or bottles need to be produced or hosted.
- Make an installed rrev report its real version on every supported install
  path: the formula injects it through the existing `main.version` ldflag, and
  `rrev --version` falls back to the module version recorded in the binary's
  build info so `go install ...@v0.1.0` no longer reports `dev`.
- Document `brew install korthane/tap/rrev` as the macOS install path in the
  README, alongside `go install` and the from-a-clone build.

## Capabilities

### New Capabilities
- `distribution`: how rrev is versioned, released, and installed — what a
  release is, what an installed binary reports as its version, and the contract
  the Homebrew formula must satisfy.

### Modified Capabilities
<!-- None. The review pipeline, CLI parsing, configuration, and progress log are
     untouched: this change adds no run-time behavior to a review. -->

## Impact

- `cmd/rrev/main.go`: the version string gains a build-info fallback.
- `.github/workflows/`: a release workflow triggered by version tags.
- `README.md`: install and prerequisites sections.
- `docs/`: a release procedure describing how to cut a version and update the
  formula.
- `TODO.md`: the Homebrew item is resolved by this change.
- A new repository, `korthane/homebrew-tap`, outside this codebase. It must be
  created before the formula can be published, and its formula must be updated
  by hand on each release — automating that bump is deliberately out of scope.
- No new runtime dependency: Homebrew and Go are build-time only, and nothing a
  review run does changes.
