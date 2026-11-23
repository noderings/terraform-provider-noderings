# Terraform Provider for NodeRings

The NodeRings provider lets Terraform and OpenTofu manage resources on [NodeRings](https://noderings.com).

- Registry: [registry.terraform.io/providers/noderings/noderings](https://registry.terraform.io/providers/noderings/noderings)
- Documentation: [`docs/`](docs/index.md)

## What it manages

| Area | Resources | Data sources |
|---|---|---|
| Compute | `noderings_virtual_machine`, `noderings_virtual_machine_backup`, `noderings_virtual_machine_additional_ip`, `noderings_virtual_machine_snapshot` (experimental) | `noderings_virtual_machine`, `noderings_virtual_machines`, `noderings_virtual_machine_tags`, `noderings_virtual_machine_backups`, `noderings_virtual_machine_snapshots` |
| Marketplace | | `noderings_marketplace_offer`, `noderings_marketplace_offers`, `noderings_marketplace_provider`, `noderings_marketplace_providers`, `noderings_marketplace_plan`, `noderings_marketplace_plan_codes`, `noderings_marketplace_addon`, `noderings_storefront` |
| Firewall | `noderings_firewall_address_group`, `noderings_firewall_ruleset`, `noderings_firewall_ruleset_rules`, `noderings_firewall_ruleset_attachment` | `noderings_firewall_address_group`, `noderings_firewall_ruleset`, `noderings_firewall_rulesets` |
| DNS | `noderings_dns_zone`, `noderings_dns_record`, `noderings_dns_geo_record`, `noderings_dns_zone_dnssec` | `noderings_dns_zone` |
| Game Servers | `noderings_game_server`, `noderings_game_server_allocation`, `noderings_game_server_database`, `noderings_game_server_schedule`, `noderings_game_server_backup` | `noderings_game_server`, `noderings_game_servers`, `noderings_game_server_games`, `noderings_game_server_startup`, `noderings_game_server_resources` |
| IAM | `noderings_ssh_key`, `noderings_role`, `noderings_group`, `noderings_group_membership`, `noderings_role_assignment`, `noderings_secret`, `noderings_environment_variable` | `noderings_organization`, `noderings_ssh_key`, `noderings_permission`, `noderings_permissions`, `noderings_role`, `noderings_roles`, `noderings_users` |
| Billing | | `noderings_credit_balance`, `noderings_subscription`, `noderings_entitlements` |
| Catalog | | `noderings_distros`, `noderings_instant_app`, `noderings_instant_apps`, `noderings_marketplace_regions` |

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

data "noderings_marketplace_offer" "web" {
  plan_code    = "SC-GC-2C-4G-80"
  country      = "Germany"
  city         = "Frankfurt"
  billing_type = "usage"
}

resource "noderings_ssh_key" "me" {
  name       = "laptop"
  public_key = file("~/.ssh/id_ed25519.pub")
}

resource "noderings_virtual_machine" "web" {
  hostname    = "web-1"
  plan_id     = data.noderings_marketplace_offer.web.plan_id
  region_id   = data.noderings_marketplace_offer.web.region_id
  distro      = "ubuntu_24_04"
  ssh_key_ids = [noderings_ssh_key.me.id]
}

resource "noderings_dns_zone" "example" {
  name = "example.com"
}

resource "noderings_dns_record" "www" {
  zone_id = noderings_dns_zone.example.id
  name    = "www"
  type    = "A"
  ttl     = 300
  records = [noderings_virtual_machine.web.ipv4]
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

Tests that create billed resources only run when you choose what to buy:

| Variables | Enables |
|---|---|
| `NODERINGS_ACC_VM_PLAN_CODE`, `NODERINGS_ACC_VM_COUNTRY`, `NODERINGS_ACC_VM_CITY` | virtual machine tests (a usage-billed plan whose provider offers `guest_firewall`, `on_demand_backup` and `snapshots`) |
| `NODERINGS_ACC_GAME_PLAN_ID`, `NODERINGS_ACC_GAME_TEMPLATE_ID`, `NODERINGS_ACC_GAME_TEMPLATE2_ID` | game server tests |

To try a local build, add a [development override](https://developer.hashicorp.com/terraform/cli/config/config-file#development-overrides-for-provider-developers) for `noderings/noderings` pointing at your `GOBIN`, then run `make install`.

### Layout

```text
internal/client       HTTP client: auth, retries, rate limiting, errors
internal/provider     provider schema and configuration
internal/service/*    one package per product (compute, marketplace, firewall, dns,
                      gameserver, iam, billing, catalog)
internal/dnsutil      DNS name and record-value normalisation
internal/fakeapi      in-memory API used by the tests
internal/acctest      test harness (fake or live API)
examples/, templates/ inputs for the generated docs/
```

## Releasing

Releases are built by GoReleaser when a `v*` tag is pushed (`.github/workflows/release.yml`). The workflow signs the checksums with the GPG key stored in the `GPG_PRIVATE_KEY` and `PASSPHRASE` repository secrets; the matching public key must be added to the namespace in the Terraform Registry.

## License

[MPL-2.0](LICENSE)
