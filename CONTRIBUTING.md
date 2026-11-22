# Contributing

Thanks for your interest in agent-stats. Issues and pull requests are welcome.

## Before you open a pull request

```sh
go vet ./...
go test -race ./...
go build .
```

CI runs the same three commands on every pull request.

- **Write tests with the code.** A new function, parser or report column comes with a test. Prefer testing
  behaviour (what the report says) over implementation details.
- **Keep the fixtures neutral.** `testdata/` holds short hand-written logs. Never commit a real session log, a real
  path under a home directory, a user name, an e-mail address or a secret.
- **Do not edit `testdata/report.approved.json` by hand.** `TestReportApproved` writes
  `testdata/report.received.json` when the report changes. Read the diff, and copy it over the approved file only
  if the change is intended; explain it in the pull request.
- Follow the layout described in the README: domain code does no I/O, adapters live in `infra`.

## Commits and pull request titles

This project uses [Conventional Commits](https://www.conventionalcommits.org/). The release version and the release
notes are computed from them, so the format matters:

```text
type(scope): short description in the imperative, no final period
```

| Type | Effect on the next release |
|------|----------------------------|
| `feat` | minor bump |
| `fix`, `perf` | patch bump |
| `type!:` or a `BREAKING CHANGE:` footer | breaking change (while the version is `0.x`, a minor bump; major from `1.0.0`) |
| `docs`, `refactor`, `test`, `build`, `ci`, `chore`, `style` | no release |

Examples: `feat(reporting): add a per-day breakdown`, `fix(indexing): skip truncated codex rollouts`.

**The pull request title must follow the same format**: pull requests are squash-merged and the title becomes the
commit on `main`. A check fails the pull request if the title is not conventional. Keep one pull request to one
change; a large pull request is easier to review as several small ones.

## Releases

Do not bump a version, create a tag or edit a changelog by hand. When commits that count (see the table above) reach
`main` and CI passes, the release workflow computes the next version with git-cliff, tags it, builds the binaries
with GoReleaser and publishes a GitHub release with generated notes.

If a release fails after its tag was pushed, re-run the failed `ci` run of that commit (Actions, "Re-run failed
jobs"). Until it is published, the release job of later commits fails with an error naming the tag, so a newer version
never goes out past an unfinished one; re-run those afterwards.

GitHub only allows a re-run for 30 days. Past that, or for a tag pushed by hand, run the `release` workflow **on the
tag**: Actions, `release`, "Run workflow", "Use workflow from": Tag `vX.Y.Z` (or `gh workflow run release.yml --ref
vX.Y.Z`). It rebuilds, attests and publishes that tag from its own commit, and refuses a branch or a tag that is not on
`main`. The tag must carry the workflow file with this entry, which is the case for every tag the pipeline creates.
A tag is never moved or deleted.

**1.0.0** is a deliberate decision, and goes through the same pipeline: do not create the tag or the release by hand,
a hand-made release has no binaries and no attestation. In the pull request that declares the CLI and `--json`
contracts stable, set `breaking_always_bump_major = true` in `cliff.toml` and title the pull request `feat!: ...`
(or give it a `BREAKING CHANGE:` footer). The next release is then `v1.0.0`.

### Repository settings (maintainers)

The workflows assume these settings, which live on GitHub and not in the repository:

- Pull requests are **squash-merged only**, with the pull request title as the commit message.
- `main` is protected: pull request required, and the `Go checks` and `Conventional Commit title` checks must pass
  (GitHub names a required check after its job, not its workflow).
- A tag ruleset on `v*` blocks updating and deleting tags, with an empty bypass list: a release tag is never moved or
  deleted. **Do not restrict tag creation.** The release workflow pushes its own tag with the built-in `GITHUB_TOKEN`,
  which cannot be added to a bypass list: a creation rule makes that push fail with `GH013`, even with GitHub Actions
  in the bypass list (see [oras-go#1361](https://github.com/oras-project/oras-go/issues/1361)). Anyone with write access
  can therefore push a `v*` tag. It publishes nothing by itself, but until it is published (see "Releases") the release
  job of every later commit fails, and the manual run publishes any strict `vX.Y.Z` tag on `main` without checking its
  version number: keep write access to maintainers.
- **Immutable releases** are enabled, so a published release and its assets cannot be modified.
- The default `GITHUB_TOKEN` permission is read-only; each workflow asks for what it needs.

## License

By contributing you agree that your contribution is licensed under the [MIT License](LICENSE).
