# distribution Specification

## Purpose
Defines how rrev is versioned, released, and installed: what constitutes a release, what an installed binary reports as its own version, and the contract the Homebrew formula must satisfy for a macOS user to install rrev without a Go toolchain of their own.

## Requirements

### Requirement: Versioned releases
A release SHALL be an annotated git tag of the form `vMAJOR.MINOR.PATCH` on this repository, pointing at a commit of the default branch. A tag MUST NOT be published from a commit that does not build, does not pass the test suite, or does not pass the linter, and the checks that establish this MUST be the same ones every other commit is held to rather than a separate, weaker set. Pushing a version tag SHALL produce a GitHub Release for it, so that the tag's source archive is a published artifact rather than an incidental URL.

#### Scenario: Tag passes the release checks
- **WHEN** a `vX.Y.Z` tag is pushed
- **THEN** the build, the tests, and the linter run against the tagged commit, and a GitHub Release for that tag is created only after all of them pass

#### Scenario: Tag fails the release checks
- **WHEN** a `vX.Y.Z` tag is pushed and any of those checks fails
- **THEN** no GitHub Release is created and the failure is reported against the tag

#### Scenario: Ordinary pushes are not releases
- **WHEN** a commit is pushed to a branch without a version tag
- **THEN** no release is created

### Requirement: Version reporting
An installed rrev SHALL report the version it was built from when invoked with `--version`. The version stamped in at build time MUST take precedence; when no version was stamped in, rrev MUST fall back to the module version recorded in the binary's own build information, so that a binary installed from a tagged module reports that tag. A build with neither MUST report a placeholder rather than a version no commit corresponds to.

#### Scenario: Version stamped in at build time
- **WHEN** rrev is built with the version stamped in as `v0.1.0` and the user runs `rrev --version`
- **THEN** it prints `rrev v0.1.0`

#### Scenario: Installed from a tagged module
- **WHEN** rrev is installed with `go install github.com/korthane/rrev/cmd/rrev@v0.1.0`, which stamps in no version, and the user runs `rrev --version`
- **THEN** it prints `rrev v0.1.0`, read from the binary's build information

#### Scenario: Built from a working tree
- **WHEN** rrev is built from an untagged checkout, where the toolchain records a version identifying the commit rather than a release
- **THEN** `rrev --version` reports that commit-identifying version, since it names the source the binary was built from

#### Scenario: No version anywhere
- **WHEN** neither a version was stamped in nor any usable version was recorded — the build information is absent, or the toolchain recorded only its own placeholder for an unnamed build
- **THEN** `rrev --version` prints a placeholder rather than claiming a release version

### Requirement: Homebrew installation
rrev SHALL be installable on macOS with `brew install korthane/tap/rrev`, from a formula published in the `korthane/homebrew-tap` repository. The formula MUST build rrev from the source archive of a released tag and MUST declare Go as a build-time-only dependency, so that installing rrev neither requires the user to have a Go toolchain nor leaves one behind as a runtime dependency. The formula MUST stamp the released version into the binary it installs, and MUST carry a test that fails when the installed binary does not report that version.

#### Scenario: Installing from the tap
- **WHEN** a macOS user runs `brew install korthane/tap/rrev`
- **THEN** Homebrew builds rrev from the released source archive and installs an `rrev` executable on the user's PATH

#### Scenario: Installed binary reports the formula's version
- **WHEN** the formula's test runs against the installed binary
- **THEN** `rrev --version` reports the version the formula declares, and the test fails if it reports anything else

#### Scenario: No Go toolchain required
- **WHEN** a user without Go installed runs `brew install korthane/tap/rrev`
- **THEN** Homebrew provides Go for the build only, and the installed rrev runs without it

#### Scenario: Source archive does not match the formula
- **WHEN** the archive the formula points at does not match the checksum the formula declares
- **THEN** the install fails rather than installing unverified source

### Requirement: Documented install paths
The README SHALL document each supported way to install rrev — Homebrew, `go install`, and building from a clone — and MUST state the prerequisites that differ between them, so that a reader can tell which paths need a Go toolchain of their own. Documentation naming a release version MUST name one that exists.

#### Scenario: Reader chooses an install path
- **WHEN** a reader opens the README's installation section
- **THEN** it lists the Homebrew command, the `go install` command, and the clone-and-build steps, and says which of them require Go

#### Scenario: Prerequisites stay accurate
- **WHEN** the prerequisites section states that Go is required to install rrev
- **THEN** it scopes that requirement to the install paths that actually need it
