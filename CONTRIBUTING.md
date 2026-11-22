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

### Repository settings (maintainers)

The workflows assume these settings, which live on GitHub and not in the repository:

- Pull requests are **squash-merged only**, with the pull request title as the commit message.
- `main` is protected: pull request required, and the `Go checks` and `Conventional Commit title` checks must pass
  (GitHub names a required check after its job, not its workflow).
- A tag ruleset on `v*` blocks updating and deleting tags, with an empty bypass list. A second, separate ruleset limits
  tag creation to maintainers and to the release workflow (GitHub Actions in its bypass list, otherwise the workflow
  cannot push its own tag). A release tag is never moved or re-created.
- **Immutable releases** are enabled, so a published release and its assets cannot be modified.
- The default `GITHUB_TOKEN` permission is read-only; each workflow asks for what it needs.

## License

By contributing you agree that your contribution is licensed under the [MIT License](LICENSE).
