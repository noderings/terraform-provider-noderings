# Contributing

Thanks for helping improve the NodeRings provider for Terraform and OpenTofu.

## Before you start

- Open an issue describing the bug or the change before a large pull request, so we can
  agree on the approach.
- Report security problems privately as described in [SECURITY.md](SECURITY.md), not in an
  issue.

## Development

You need Go (the version in `go.mod`) and [golangci-lint](https://golangci-lint.run/).

```shell
make build      # compile
make test       # every test, against an in-memory fake of the NodeRings API
make lint       # golangci-lint
make generate   # format examples and regenerate docs/ from the schema and templates/
```

`make test` needs no account or network. Use it for most changes.

To try the provider in a configuration, run `make install` and point Terraform at your local
build with a `dev_overrides` block in `~/.terraformrc`.

## Acceptance tests

`make testacc` runs the same tests against the real NodeRings API. It creates and deletes
real, sometimes billed, resources. Set:

```shell
export NODERINGS_TOKEN=...          # a service account token, ideally in a test organization
export NODERINGS_ACC_DOMAIN=...     # a domain the account may create test zones under
```

The README lists the extra variables for virtual machine and game server tests.

## Pull requests

1. Add or update tests for the change. New resources and data sources need tests against
   the fake API, and an acceptance test.
2. Update the docs: edit the schema descriptions, `templates/` or `examples/`, then run
   `make generate`. Do not edit `docs/` by hand.
3. Run `make fmt lint test` and make sure they pass.
4. Use [Conventional Commits](https://www.conventionalcommits.org/) for commit messages and
   the pull request title (pull requests are squash-merged, so the title becomes the
   commit), and sign off each commit (`git commit -s`) to certify the
   [Developer Certificate of Origin](https://developercertificate.org/). Do not edit
   [CHANGELOG.md](CHANGELOG.md) by hand; it is written from these messages at release time:

   | Prefix | Changelog section | Version bump (before 1.0) |
   | --- | --- | --- |
   | `feat:` | FEATURES | minor |
   | `fix:` | BUG FIXES | patch |
   | `perf:` | ENHANCEMENTS | patch |
   | `deps:` | DEPENDENCIES | patch |
   | `docs:` | DOCUMENTATION | patch |
   | `chore:`, `ci:`, `test:`, `refactor:`, `build:` | not listed | none |

   Add `!` after the type (`feat!:`) or a `BREAKING CHANGE:` footer for a breaking change.

## Releasing

Releases are automated; nobody pushes tags or builds by hand.

1. [release-please](https://github.com/googleapis/release-please) keeps one open
   pull request, `chore: release X.Y.Z`, that updates `CHANGELOG.md` and
   `.release-please-manifest.json` from the commits merged since the last release.
2. Merging it pushes the `vX.Y.Z` tag and a draft GitHub release with the changelog.
3. The tag starts `.github/workflows/release.yml`. GoReleaser builds the zips, writes
   `SHA256SUMS` and the registry manifest, signs `SHA256SUMS` with the provider GPG key,
   uploads everything to the draft, publishes it, and a build provenance attestation is
   added for every zip.
4. The Terraform Registry (through its GitHub webhook) and the OpenTofu Registry pick up
   the published release.

Repository settings this needs: secrets `GPG_PRIVATE_KEY` and `PASSPHRASE` (the signing
key), variable `RELEASE_BOT_APP_ID` and secret `RELEASE_BOT_PRIVATE_KEY` (the release bot
GitHub App, whose tags start workflows; tags pushed with the default `GITHUB_TOKEN` do
not). The public half of the signing key is printed by the **Signing key** workflow
(Actions → Signing key → Run workflow).

If a release run fails, fix the cause and re-run it from the Actions tab, or run the
**Release** workflow by hand with the tag selected.

This project follows a [code of conduct](CODE_OF_CONDUCT.md).
