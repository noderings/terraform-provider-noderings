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
3. Describe the change in [CHANGELOG.md](CHANGELOG.md), under a `## Unreleased` heading at the top (add it if it is not there), grouped as FEATURES, ENHANCEMENTS or BUG FIXES like the earlier entries.
4. Run `make fmt lint test` and make sure they pass.
5. Use [Conventional Commits](https://www.conventionalcommits.org/) for commit messages, and
   sign off each commit (`git commit -s`) to certify the
   [Developer Certificate of Origin](https://developercertificate.org/).

This project follows a [code of conduct](CODE_OF_CONDUCT.md).
