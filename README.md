# rrev

`rrev` reviews the current branch against a named [OpenSpec](https://github.com/Fission-AI/OpenSpec)
change and autonomously fixes and commits what it finds.

Most AI code review is generic: "find bugs in this diff". rrev is spec-driven —
the change's proposal, design, delta specs, and tasks are the source of truth, so
the requirements and scenarios written before implementation become explicit
conformance criteria for the diff that followed.

A run alternates independent reviewer agents with a fixing executor across three
phases — comprehensive review, cross-model external review, and a final
regression pass — iterating until the reviewers go quiet.

> **Status: in development.** The CLI is a stub; see
> `docs/plans/20260827-rrev-spec-driven-review-pipeline.md` for the build order.

## Build

```sh
make build    # build ./rrev
make test     # run tests
make lint     # run golangci-lint
make coverage # test with coverage report
```

## Credits

The review pipeline mechanic — parallel reviewer agents, a cross-model external
review loop, a final regression pass, and sentinel signals parsed out of executor
output — originates in [ralphex](https://github.com/umputun/ralphex) by Umputun,
MIT licensed. rrev reimplements it in fresh Go code with no dependency on the
ralphex module; only the shipped prompt and agent text is adapted from ralphex
defaults.

The following default files are derived from ralphex and carry that attribution
in their own headers:

- Reviewer agents: `agents/quality.txt`, `agents/implementation.txt`,
  `agents/testing.txt`, `agents/simplification.txt`, `agents/documentation.txt`
- Phase prompts: `prompts/review_first.txt`, `prompts/review_external.txt`,
  `prompts/review_external_eval.txt`, `prompts/review_final.txt`,
  `prompts/finalize.txt`

rrev's own `agents/conformance.txt` and `agents/tasks.txt` are not derived from
ralphex — they exist because rrev has a spec to check against.

## License

MIT — see [LICENSE](LICENSE).
