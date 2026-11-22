# Contributing

Thanks for your interest in agent-stats. Issues and pull requests are welcome.

## Before you open a pull request

```sh
go vet ./...
go test -race ./...
go build .
```

CI runs the same three commands on every pull request, after building the web front: `go generate ./web` (Node 22.12 or
later; `npm ci` then `npm run build`, which type-checks the front), and it vets, tests and builds with `-tags web` as well.
If you change `web/`, run `cd web && npm run typecheck` and look at the page with `agent-stats serve`
(`go generate ./web && go build -tags web .`; or `npm run dev` in `web/`).

- **Write tests with the code.** A new function, parser or report column comes with a test. Prefer testing
  behaviour (what the report says) over implementation details.
- **Keep the fixtures neutral.** `testdata/` holds short hand-written logs. Never commit a real session log, a real
  path under a home directory, a user name, an e-mail address or a secret.
- **Do not edit `testdata/report.approved.json` by hand.** `TestReportApproved` writes
  `testdata/report.received.json` when the report changes. Read the diff, and copy it over the approved file only
  if the change is intended; explain it in the pull request.
- Follow the layout described in the README: domain code does no I/O, adapters live in `infra`.
- **Front dependencies.** `web/package.json` pins exact versions and `web/package-lock.json` is committed; install scripts are
  off (`web/.npmrc`). Adding an npm dependency is a decision to take in the pull request, not a side effect. The built
  `web/dist` is never committed.

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

## Public documentation site

The Next.js and Fumadocs site lives in `site/`. Write guides in `site/content/docs/` and list their slugs in `meta.json` to set the sidebar order. The dashboard bundled in the CLI is developed separately in `web/`.

```sh
npm --prefix site ci
npm --prefix site run dev
```

To verify the GitHub Pages artifact:

```sh
npm --prefix site run build
npm --prefix site test
```

The build outputs `site/out/`, including the static search index. The repository currently publishes the legacy site from `main:/docs`. When the new workflow reaches `main`, select **GitHub Actions** under **Settings → Pages**, keep the existing custom domain, and run **Documentation site** from `main`. GitHub Actions then publishes changes under `site/` pushed to `main`.

## Releases

Do not bump a version, create a tag or edit a changelog by hand. When commits that count (see the table above) reach
`main` and CI passes, the release workflow computes the next version with git-cliff, tags it, builds the binaries
with GoReleaser and publishes a GitHub release with generated notes.

If a release fails after its tag was pushed, re-run the failed `ci` run of that commit (Actions, "Re-run failed
jobs"). The tag step accepts a tag that already names the commit and refuses one that names another. Until it is published, the release job of later commits fails with an error naming the tag, so a newer version
never goes out past an unfinished one; re-run those afterwards. The front built for the run is kept as an artifact for
the same 30 days, so re-running only the failed job works for as long as GitHub allows it.

GitHub only allows a re-run for 30 days. Past that, or for a tag pushed by hand, run the `release` workflow **on the
tag**: Actions, `release`, "Run workflow", "Use workflow from": Tag `vX.Y.Z` (or `gh workflow run release.yml --ref
vX.Y.Z`). It rebuilds, attests and publishes that tag from its own commit, like a re-run but without the Go checks (a
commit on `main` passed them when it was merged). It refuses a branch, a tag that is not on `main`, and a version that
git-cliff would not have computed for that commit. Those checks come from the workflow file of the tag itself, so
consumers verify the attestation as the README says rather than trust them. The tag must carry the workflow file with
this entry, which is the case for every tag the pipeline creates. A tag is never moved or deleted.

### AI summary in the release notes

A release body starts with a `## Summary` written by the OpenAI API, followed by the git-cliff notes, which stay whole.
If there is no summary, the body is the git-cliff notes alone, as before.

How it works (`release.yml`, `tools/releasenotes`):

- The `plan` job writes the git-cliff notes. The `release-notes` job, which can only read, builds `tools/releasenotes`,
  asks the API for the summary and hands over that summary alone. Only that job receives the key, only in the
  environment of the one step that calls the API. The job that tags, signs and publishes never sees the key and
  compiles nothing of `tools/`: it checks the summary again in a shell step and publishes `## Summary`, the summary and
  the git-cliff notes, or the notes alone if the summary is missing or invalid.
- The jobs hand artifacts over by ID: each producing job reports the `artifact-id` of its upload and the consuming job
  downloads that ID, never a name that another job of the run could reuse. The publishing job stops if the front or
  the notes have no ID. It reads the size of the summary artifact from the API (`actions: read`) before downloading it
  and ignores one over 8 KiB.
- The API receives the subject lines of the notes (group titles and `- ` items, at most 6 000 bytes, angle brackets and
  control characters removed) and fixed instructions. No code, no file, no diff. One call to the Responses API
  (`store: false`, `max_output_tokens: 800`, 30 s timeout), plus one retry on a network error, a 429 or a 5xx. The step
  is stopped after 2 minutes and the job after 3.
- Commit subjects are untrusted: the repository is public. The reply is an allowlist: paragraphs and `- ` bullets of
  at most 1 500 characters, in printable ASCII plus curly quotes, dashes and an ellipsis, with `*` or `_` for emphasis.
  Anything else is rejected, however it is nested: no quote, heading, other list, HTML, entity, backtick, `#`, `@`, `&`,
  `[`, `]`, address (`//`, `www.`), indented line or control character. Otherwise the reply is dropped whole, not
  repaired. The publishing job repeats the same rules in shell.
- Nothing here can stop a release. With no key, an API error, a timeout or a rejected reply, the step logs a notice
  (`AI release summary skipped: <reason>`, a fixed text that never carries the API answer) and the release goes out with
  its notes alone. The job is also `continue-on-error`.

To turn it off: set the repository variable `AI_RELEASE_NOTES` to `off` (the `release-notes` job is then skipped and
the key is never passed), or delete the `OPENAI_API_KEY` repository secret (the job starts, builds nothing and hands
nothing over). To change the model, edit `OPENAI_MODEL` in the `release-notes` job: it is a
dated snapshot (`gpt-5.4-mini-2026-03-17`), so the text of a release does not change without a commit. A release sends
about 2 000 input tokens at most and gets at most 800 back, one call plus at most one retry; check the price on OpenAI's
pricing page.

The key's life cycle:

- **Scope.** Use a key of a dedicated OpenAI project that only this repository uses, limited to the Responses API and the
  model above, with a monthly spend limit. Never a personal or shared key.
- **Owner.** Nathan owns the key. He sets the secret (`gh secret set OPENAI_API_KEY`) and rotates it every 90 days, and
  whenever the key may have leaked or someone who had access leaves: create the new key, set the secret, then revoke the
  old key in the OpenAI dashboard. The first rotation is tracked by the Kaneo ticket AST-41, due 2027-02-20; each
  rotation opens the ticket for the next one. If Nathan is unavailable, any repository admin may rotate or revoke the
  key (revoking is always safe, see below).
- **Revocation is safe and visible.** Nothing depends on the key: once it is revoked, the next release logs
  `AI release summary skipped: API answered 401` and goes out with its notes alone (a unit test pins this). Revoking a
  key therefore never blocks a release, and is the answer to a leak.

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
  job of every later commit fails. The manual run only accepts the version the pipeline would have computed for that
  commit, but it can be started by anyone with write access: keep write access to maintainers.
- **Immutable releases** are enabled, so a published release and its assets cannot be modified.
- The default `GITHUB_TOKEN` permission is read-only; each workflow asks for what it needs.

## License

By contributing you agree that your contribution is licensed under the [MIT License](LICENSE).
