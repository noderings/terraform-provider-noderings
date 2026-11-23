# Terraform Provider for NodeRings

The NodeRings provider lets Terraform and OpenTofu manage resources on [NodeRings](https://noderings.com).

- Registry: [registry.terraform.io/providers/noderings/noderings](https://registry.terraform.io/providers/noderings/noderings)
- Documentation: [`docs/`](docs/index.md)

## What it manages

| Area | Resources | Data sources |
|---|---|---|
| DNS | `noderings_dns_zone`, `noderings_dns_record`, `noderings_dns_geo_record`, `noderings_dns_zone_dnssec` | `noderings_dns_zone` |
| IAM | `noderings_ssh_key` | `noderings_ssh_key`, `noderings_organization` |
| Catalog | | `noderings_distros`, `noderings_instant_app`, `noderings_marketplace_regions` |

## Usage

```terraform
terraform {
  required_providers {
    noderings = {
      source = "noderings/noderings"
    }
  }
}

provider "noderings" {} # reads NODERINGS_TOKEN

resource "noderings_dns_zone" "example" {
  name = "example.com"
}

resource "noderings_dns_record" "www" {
  zone_id = noderings_dns_zone.example.id
  name    = "www"
  type    = "A"
  ttl     = 300
  records = ["192.0.2.10"]
}
```

Create a service-account token in the NodeRings console under **Organization > Access Control > Service accounts** and export it as `NODERINGS_TOKEN`. See the [provider documentation](docs/index.md) for all arguments.

## Requirements

- [Terraform](https://developer.hashicorp.com/terraform/downloads) 1.5 or later, or OpenTofu
- [Go](https://golang.org/doc/install) (see `go.mod`) to build the provider

## Development

```shell
make build      # compile
make test       # run all tests against an in-memory fake of the API
make lint       # golangci-lint
make generate   # format examples and regenerate docs/
```

`make test` runs real Terraform plans, applies, imports and destroys against `internal/fakeapi`, an in-memory stand-in for the NodeRings API. It needs no account and no network.

To run the same tests against the real API (this creates and deletes real resources):

```shell
export NODERINGS_TOKEN=...            # service-account token
export NODERINGS_ACC_DOMAIN=...       # a domain the account may create test zones under
make testacc
```

To try a local build, add a [development override](https://developer.hashicorp.com/terraform/cli/config/config-file#development-overrides-for-provider-developers) for `noderings/noderings` pointing at your `GOBIN`, then run `make install`.

### Layout

```text
internal/client       HTTP client: auth, retries, rate limiting, errors
internal/provider     provider schema and configuration
internal/service/*    one package per product (dns, iam, catalog)
internal/dnsutil      DNS name and record-value normalisation
internal/fakeapi      in-memory API used by the tests
internal/acctest      test harness (fake or live API)
examples/, templates/ inputs for the generated docs/
```

## Releasing

Releases are built by GoReleaser when a `v*` tag is pushed (`.github/workflows/release.yml`). The workflow signs the checksums with the GPG key stored in the `GPG_PRIVATE_KEY` and `PASSPHRASE` repository secrets; the matching public key must be added to the namespace in the Terraform Registry.

## License

[MPL-2.0](LICENSE)
