## 1. Version reporting

- [x] 1.1 Extract the version string into a resolver that takes the stamped-in
      value and the binary's build information and returns what `--version`
      prints, so the fallback is testable without building a binary; verify
      `go build ./...` succeeds and `rrev --version` still prints `rrev dev`
      from a plain `go build`
- [x] 1.2 Implement the resolution order — stamped-in value wins, otherwise the
      main module's version, otherwise the `dev` placeholder — treating an
      empty version and `(devel)` as absent; verify with unit tests covering
      each of the three outcomes plus both absent forms
- [x] 1.3 Verify `make build` still stamps the version through the existing
      ldflag by building at a dirty checkout and checking `./rrev --version`
      reports the `git describe` value, not the module version

## 2. Release automation

- [x] 2.1 Add a `release` job to `.github/workflows/ci.yml` that runs only for
      `refs/tags/v*`, declares `needs: build`, grants `contents: write` at the
      job level, and creates the GitHub Release for the tag with `gh release
      create`; verify with `actionlint`
- [x] 2.2 Verify the job's condition by inspection against the workflow's
      existing `on: push` trigger: an untagged branch push must run `build`
      alone, and a failing `build` on a tagged commit must leave `release`
      skipped so no Release is published

## 3. Documentation

- [x] 3.1 Write `docs/releasing.md`: the tag-is-immutable rule, the steps to cut
      a version, the formula bump that follows it (url, sha256, version), and
      the note that raising `go.mod`'s Go directive above what Homebrew ships
      breaks the formula's sandboxed build; verify it names every step needed to
      go from a green `main` to an installable release without consulting this
      change
- [x] 3.2 Update the README's installation section with all three paths —
      `brew install korthane/tap/rrev`, `go install`, and the clone build — and
      scope the Go prerequisite to the paths that need it; verify `go test
      ./cmd/rrev/` still passes, since its documentation tests read the README

## 4. First release

- [ ] 4.1 Cut `v0.1.0`: annotated tag on a green default-branch commit, pushed;
      verify the `release` job succeeds and the GitHub Release exists with its
      source archive
- [ ] 4.2 Verify the released module reports its own version by running `go
      install github.com/korthane/rrev/cmd/rrev@v0.1.0` into a scratch `GOBIN`
      and checking `rrev --version` prints `rrev v0.1.0`

## 5. Homebrew tap

- [ ] 5.1 Create the public `korthane/homebrew-tap` repository with a README
      naming the install command; verify `brew tap korthane/tap` succeeds
- [ ] 5.2 Write `Formula/rrev.rb` — desc, homepage, the `v0.1.0` source archive
      url with its sha256, MIT license, `depends_on "go" => :build`, a build
      that stamps `main.version` with the formula's version, and a test
      asserting `rrev --version` reports it; verify `brew audit --strict --new
      korthane/tap/rrev` passes
- [ ] 5.3 Verify the formula end to end: `brew install korthane/tap/rrev`
      followed by `brew test korthane/tap/rrev`, then `rrev --version` printing
      `rrev v0.1.0` from the installed binary
- [ ] 5.4 Verify the test actually fails when the version is not stamped in, by
      dropping the ldflag locally and confirming `brew test` reports it; restore
      the formula afterwards

## 6. Close out

- [ ] 6.1 Remove the Homebrew entry from `TODO.md` now that it is delivered;
      verify the file is left with a valid list or removed entirely if empty
