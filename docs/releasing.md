# Releasing rrev

A release is an annotated `vMAJOR.MINOR.PATCH` tag on `main`. Pushing the tag
runs the CI checks against it and, once they pass, publishes a GitHub Release —
whose source archive is what the Homebrew formula builds from.

**Tags are immutable.** The published formula pins the archive's checksum, and
GitHub's archives are byte-stable per tag: moving a tag breaks every install
that already resolved it. Fix a bad release with the next patch version.

## Cut a version

1. Start from a `main` whose CI is green, with nothing uncommitted.

2. Tag and push:

   ```sh
   git tag -a v0.1.0 -m "v0.1.0"
   git push origin v0.1.0
   ```

3. Watch the run: the `build` job runs the same build, tests, cross-build, and
   lint as any other commit, and `release` publishes the GitHub Release only
   after it passes.

   ```sh
   gh run watch
   gh release view v0.1.0
   ```

4. Confirm the released module reports its own version:

   ```sh
   GOBIN=$(mktemp -d) go install github.com/korthane/rrev/cmd/rrev@v0.1.0
   ```

   The installed binary must print `rrev v0.1.0` for `--version`. Nothing stamps
   a version into that build, so it is reading the tag out of its own build
   information — the check that this still works.

## Update the formula

The formula lives in [`korthane/homebrew-tap`](https://github.com/korthane/homebrew-tap)
at `Formula/rrev.rb`, and is bumped by hand in the same sitting as the tag.

1. Take the checksum of the archive the formula will point at:

   ```sh
   curl -sL https://github.com/korthane/rrev/archive/refs/tags/v0.1.0.tar.gz | shasum -a 256
   ```

2. In the tap, set the formula's `url` to that archive and `sha256` to that
   checksum. Nothing else in the formula names the version: the build reads it
   from the url.

3. Verify before pushing, then again after:

   ```sh
   brew audit --strict --new korthane/tap/rrev
   brew install --build-from-source korthane/tap/rrev
   brew test korthane/tap/rrev
   ```

   `brew test` fails when the installed binary does not report the formula's
   version, which is what catches a bumped url whose build forgot to stamp the
   version in.

## Raising the Go version

Homebrew's `go` formula is what builds rrev for a `brew install`, in a sandbox
that may deny the network. Raising the `go` directive in `go.mod` above the
version Homebrew currently ships would send the build looking for a toolchain to
download, and it would fail there rather than at a place that explains itself.
Check `brew info go` before bumping it.
