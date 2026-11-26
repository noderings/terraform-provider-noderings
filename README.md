# Terraform Provider for NodeRings

[![Tests](https://github.com/noderings/terraform-provider-noderings/actions/workflows/test.yml/badge.svg)](https://github.com/noderings/terraform-provider-noderings/actions/workflows/test.yml)
[![Release](https://img.shields.io/github/v/release/noderings/terraform-provider-noderings?sort=semver)](https://github.com/noderings/terraform-provider-noderings/releases)
[![Terraform Registry](https://img.shields.io/badge/terraform-noderings%2Fnoderings-7B42BC.svg?logo=terraform)](https://registry.terraform.io/providers/noderings/noderings)
[![OpenTofu Registry](https://img.shields.io/badge/opentofu-noderings%2Fnoderings-FFDA18.svg?logo=opentofu)](https://search.opentofu.org/provider/noderings/noderings)
[![License](https://img.shields.io/badge/license-MPL--2.0-blue.svg)](LICENSE)

The NodeRings provider lets Terraform and OpenTofu manage resources on [NodeRings](https://noderings.com).

New to it? The step-by-step guide [Order a VPS with Terraform and pay by the hour](https://noderings.com/blog/terraform-vps) walks through a first run.

- Registry: [registry.terraform.io/providers/noderings/noderings](https://registry.terraform.io/providers/noderings/noderings)
- Documentation: [`docs/`](docs/index.md)

## What it manages

| Area | Resources | Data sources |
|---|---|---|
| Compute | `noderings_virtual_machine`, `noderings_virtual_machine_backup`, `noderings_virtual_machine_additional_ip`, `noderings_virtual_machine_snapshot` (experimental) | `noderings_virtual_machine`, `noderings_virtual_machines`, `noderings_virtual_machine_tags`, `noderings_virtual_machine_backups`, `noderings_virtual_machine_snapshots` |
| Marketplace | | `noderings_marketplace_offer`, `noderings_marketplace_offers`, `noderings_marketplace_provider`, `noderings_marketplace_providers`, `noderings_marketplace_plan`, `noderings_marketplace_plan_codes`, `noderings_marketplace_addon`, `noderings_storefront` |
| Firewall | `noderings_firewall_address_group`, `noderings_firewall_ruleset`, `noderings_firewall_ruleset_rules`, `noderings_firewall_ruleset_attachment` | `noderings_firewall_address_group`, `noderings_firewall_ruleset`, `noderings_firewall_rulesets` |
| DNS | `noderings_dns_zone`, `noderings_dns_record`, `noderings_dns_geo_record`, `noderings_dns_zone_dnssec` | `noderings_dns_zone` |
| `noderings_game_server` | `noderings_game_server`, `noderings_game_server_allocation`, `noderings_game_server_database`, `noderings_game_server_schedule`, `noderings_game_server_backup` | `noderings_game_server`, `noderings_game_servers`, `noderings_game_server_games`, `noderings_game_server_startup`, `noderings_game_server_resources` |
| IAM | `noderings_ssh_key`, `noderings_role`, `noderings_group`, `noderings_group_membership`, `noderings_role_assignment`, `noderings_secret`, `noderings_environment_variable` | `noderings_organization`, `noderings_ssh_key`, `noderings_permission`, `noderings_permissions`, `noderings_role`, `noderings_roles`, `noderings_users` |
| Billing | | `noderings_credit_balance`, `noderings_subscription`, `noderings_entitlements` |
| Catalog | | `noderings_distros`, `noderings_instant_app`, `noderings_instant_apps`, `noderings_marketplace_regions` |

## Getting started

### 1. Create a token

The provider signs in with a **service-account token**:

1. In the NodeRings console, open **Organization > Access Control > Service accounts**.
2. Create a service account and give it a role that covers what you manage (the built-in **Administrator** role covers everything).
3. Generate a token. It is shown only once, so store it in your secret manager.

```sh
export NODERINGS_TOKEN="<your token>"
```

A token belongs to one organization. To manage several, use one provider block per organization with an `alias` (see the [provider documentation](docs/index.md#authentication)).

### 2. Configure the provider

```terraform
terraform {
  required_providers {
    noderings = {
      source  = "noderings/noderings"
      version = "~> 0.1"
    }
  }
}

provider "noderings" {} # reads NODERINGS_TOKEN
```

### 3. Plan and apply

```sh
terraform init
terraform plan
terraform apply
```

Terraform shows every change before it is made. Resources that cost money (virtual machines and add-ons) are only ordered on `apply`.

## Choosing where your server runs

NodeRings is a marketplace: independent hosting providers sell compute, GPU, storage, network and other resources on it. **Nothing in this Terraform provider names a hosting provider.** Which one a server is ordered from is decided each time Terraform reads the marketplace, from the filters you write.

### Plan codes, providers and offers

| Term | Meaning | Example |
|---|---|---|
| Plan code | A NodeRings-wide size. Every provider selling that size lists it under the same code. | `SC-GC-1C-1G-20`: shared compute, 1 vCPU, 1 GB RAM, 20 GB disk |
| Provider | A hosting company selling on NodeRings, with an ID and optionally a public storefront. | storefront `acme-hosting` |
| Offer | One provider's plan for a code in one city, with its prices. | Acme Hosting's `SC-GC-1C-1G-20` in Frankfurt, hourly |
| `plan_id`, `region_id` | The IDs of the chosen offer's plan and city. A VM is ordered with these. | |

### Who decides

The choice is split between the NodeRings API and this provider:

1. **The NodeRings API decides which offers exist.** When `noderings_marketplace_offer` is read, the provider asks the API for providers selling `plan_code` in `country`/`city`, narrowed by `billing_type` and `require_ipv6`. The API only returns providers that are currently selling that plan there.
2. **This provider filters and ranks them** (in [`internal/service/marketplace/offer.go`](internal/service/marketplace/offer.go), function `findOffers`):
   - drops offers whose platform lacks any of `require_capabilities` (for example `guest_firewall`, `on_demand_backup`, `snapshots`);
   - with `provider_id` or `storefront_slug`, only looks at that one provider's plans;
   - sorts by `sort`: `price` (cheapest first, the default) or `rating` (best-rated provider first); ties go to the better rating, then to the provider name;
   - returns the first offer.
3. **You decide** through those arguments. If nothing matches, the lookup fails and says which filter removed the offers and in which cities the plan code is sold.

The chosen offer is in the data source's attributes: `provider_id`, `provider_name`, `rating`, `platform_driver`, `capabilities` and `prices`.

```terraform
data "noderings_marketplace_offer" "web" {
  plan_code            = "SC-GC-1C-1G-20"
  country              = "United States"
  city                 = "Ashburn"
  billing_type         = "usage"            # hourly, paid from prepaid credit
  require_capabilities = ["guest_firewall"] # only platforms with the firewall
  # sort               = "rating"           # best-rated instead of cheapest
}

output "chosen_provider" {
  value = "${data.noderings_marketplace_offer.web.provider_name} (${data.noderings_marketplace_offer.web.provider_id})"
}
```

### Picking a specific provider

List the providers and compare offers first:

```terraform
data "noderings_marketplace_providers" "vm" {
  asset_type = "virtual_machine"
}

data "noderings_marketplace_offers" "small" {
  plan_code    = "SC-GC-1C-1G-20"
  country      = "United States"
  city         = "Ashburn"
  billing_type = "usage"
}

output "providers" {
  value = [for p in data.noderings_marketplace_providers.vm.providers : "${p.name} (${p.id}), rating ${p.rating}"]
}

output "offers" {
  value = [for o in data.noderings_marketplace_offers.small.offers : "${o.provider_name}: ${join(", ", [for p in o.prices : "${p.unit_amount} ${p.currency} ${p.unit_name}"])}"]
}
```

Then pin one with `provider_id` (or its `storefront_slug`):

```terraform
data "noderings_marketplace_offer" "web" {
  plan_code    = "SC-GC-1C-1G-20"
  country      = "United States"
  city         = "Ashburn"
  billing_type = "usage"
  provider_id  = "00000000-0000-0000-0000-000000000000"
}
```

Other lookups: `noderings_marketplace_regions` (cities with offers), `noderings_marketplace_plan_codes` (codes, per city or billing type), `noderings_marketplace_provider` (one provider's cities and plans), `noderings_storefront` and `noderings_marketplace_plan` (a plan's size and prices).

### Keeping a server on its offer

The lookup runs again on every `terraform plan`. If a cheaper or better-rated offer appears, or the chosen one is no longer sold, it returns a different `plan_id` or `region_id`, and **changing either replaces the VM**: Terraform plans to delete it and order a new one, and its disk is lost. Read the plan before applying, and use one or more of:

```terraform
resource "noderings_virtual_machine" "web" {
  hostname  = "web-1"
  plan_id   = data.noderings_marketplace_offer.web.plan_id
  region_id = data.noderings_marketplace_offer.web.region_id
  distro    = "ubuntu_24_04"
  ssh_key_ids = [noderings_ssh_key.me.id]

  # Refuse to delete or replace the VM until this is set back to false.
  deletion_protection = true

  # Use the lookup only to create the VM; keep it on that offer afterwards.
  lifecycle {
    ignore_changes = [plan_id, region_id]
  }
}
```

Pinning `provider_id`, as above, also stops a different provider from being picked.

Plans for `noderings_game_server` are chosen the same way, with a plan code (`SC-GS-...`) and `asset_type = "game_server"`. Add-ons (backups, additional IPs) are sold by the provider the VM runs on; `noderings_marketplace_addon` with `virtual_machine_id` finds them.

More detail: [Choosing a marketplace offer](docs/guides/marketplace-offers.md).

## Examples

Each example is a complete configuration: copy it into a `main.tf` next to the provider block above.

### A web server with SSH access, a firewall and DNS

Finds the cheapest hourly-billed offer for a plan, creates a VM with your SSH key, allows only HTTPS from anywhere and SSH from your office, and points `www` at it.

```terraform
data "noderings_marketplace_offer" "web" {
  plan_code            = "SC-GC-2C-4G-80"
  country              = "Germany"
  city                 = "Frankfurt"
  billing_type         = "usage"
  require_capabilities = ["guest_firewall"]
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
  tags        = ["web"]

  deletion_protection = true
}

resource "noderings_firewall_address_group" "office" {
  name  = "office"
  cidrs = ["203.0.113.0/24"]
}

resource "noderings_firewall_ruleset" "web" {
  name = "web"
}

# Rules are evaluated top to bottom. Terraform owns the whole list and its order.
resource "noderings_firewall_ruleset_rules" "web" {
  ruleset_id = noderings_firewall_ruleset.web.id

  rule {
    direction         = "ingress"
    action            = "accept"
    protocol          = "tcp"
    destination_ports = "443"
    source { cidrs = ["0.0.0.0/0"] }
  }

  rule {
    direction         = "ingress"
    action            = "accept"
    protocol          = "tcp"
    destination_ports = "22"
    source { address_group_ids = [noderings_firewall_address_group.office.id] }
  }

  # Traffic that matches no rule is accepted, so end with a deny.
  rule {
    direction = "ingress"
    action    = "drop"
    protocol  = "any"
    source { cidrs = ["0.0.0.0/0"] }
  }
}

resource "noderings_firewall_ruleset_attachment" "web" {
  virtual_machine_id = noderings_virtual_machine.web.id
  ruleset_id         = noderings_firewall_ruleset.web.id
}

resource "noderings_dns_zone" "site" {
  name = "example.com"
}

resource "noderings_dns_record" "www" {
  zone_id = noderings_dns_zone.site.id
  name    = "www"
  type    = "A"
  ttl     = 300
  records = [noderings_virtual_machine.web.ipv4]
}

output "ssh" {
  value = "ssh root@${noderings_virtual_machine.web.ipv4}"
}

# Point your registrar at these name servers.
output "nameservers" {
  value = noderings_dns_zone.site.nameservers
}
```

### Several identical servers

`count` (or `for_each`) works like with any provider. Each VM is a separate order.

```terraform
resource "noderings_virtual_machine" "worker" {
  count = 3

  hostname    = "worker-${count.index + 1}"
  plan_id     = data.noderings_marketplace_offer.web.plan_id
  region_id   = data.noderings_marketplace_offer.web.region_id
  distro      = "debian_13"
  ssh_key_ids = [noderings_ssh_key.me.id]
  tags        = ["worker"]
}

output "worker_ips" {
  value = noderings_virtual_machine.worker[*].ipv4
}
```

### Backups for a VM

```terraform
# If the provider sells several backup add-ons (daily, weekly, ...), pick one
# with code. The error message lists the codes when it is missing.
data "noderings_marketplace_addon" "backup" {
  type               = "backup"
  code               = "DAILY-BACKUP"
  virtual_machine_id = noderings_virtual_machine.web.id
}

resource "noderings_virtual_machine_backup" "web" {
  virtual_machine_id = noderings_virtual_machine.web.id
  addon_id           = data.noderings_marketplace_addon.backup.id
}
```

### DNS zone with DNSSEC and mail records

```terraform
resource "noderings_dns_zone" "mail" {
  name = "example.org"
}

resource "noderings_dns_record" "mx" {
  zone_id = noderings_dns_zone.mail.id
  name    = "@"
  type    = "MX"
  ttl     = 3600
  records = ["10 mail.example.org."]
}

resource "noderings_dns_record" "spf" {
  zone_id = noderings_dns_zone.mail.id
  name    = "@"
  type    = "TXT"
  ttl     = 3600
  records = ["v=spf1 mx -all"]
}

resource "noderings_dns_zone_dnssec" "mail" {
  zone_id = noderings_dns_zone.mail.id
}

# Add this DS record at your registrar to finish enabling DNSSEC.
output "ds_record" {
  value = noderings_dns_zone_dnssec.mail.ds
}
```

### A Minecraft server

`noderings_game_server` plans come from the NodeRings marketplace. `noderings_game_server_games` lists the games a plan can run.

```terraform
variable "game_plan_id" {
  type = string
}

# Game names depend on the plan's provider. List them with
# data.noderings_game_server_games.<name>.games[*].name.
data "noderings_game_server_games" "paper" {
  plan_id = var.game_plan_id
  name    = "Minecraft · Paper"
}

resource "noderings_game_server" "survival" {
  name        = "survival"
  plan_id     = var.game_plan_id
  template_id = data.noderings_game_server_games.paper.id
  accept_eula = true

  environment = {
    MINECRAFT_VERSION = "1.21.4"
    MAX_PLAYERS       = "30"
  }

  deletion_protection = true
}

output "address" {
  value = "${noderings_game_server.survival.primary_ip}:${noderings_game_server.survival.primary_port}"
}
```

### Team access

Create a role from API permissions, a group, and give the group the role.

```terraform
data "noderings_permissions" "dns" {
  services = ["DnsZoneService", "DnsRecordService", "DnssecService"]
}

resource "noderings_role" "dns_admin" {
  name           = "dns-admin"
  description    = "Manages DNS zones and records"
  permission_ids = data.noderings_permissions.dns.ids
}

resource "noderings_group" "dns_team" {
  name = "dns-team"
}

data "noderings_users" "team" {
  emails = ["alice@example.com", "bob@example.com"]
}

resource "noderings_group_membership" "dns_team" {
  group_id = noderings_group.dns_team.id
  user_ids = data.noderings_users.team.ids
}

resource "noderings_role_assignment" "dns_team" {
  group_id = noderings_group.dns_team.id
  role_id  = noderings_role.dns_admin.id
}
```

### Importing what you already have

Bring resources created in the console under Terraform with an `import` block (Terraform 1.5+), then run `terraform plan`:

```terraform
import {
  to = noderings_virtual_machine.web
  id = "<virtual machine id>"
}

import {
  to = noderings_dns_record.www
  id = "<zone id>/www/A"
}
```

Every resource page in the [documentation](docs/) shows its import ID format.

### Good to know

- **Billing.** Creating a VM or add-on places an order. Hourly (`usage`) plans are paid from prepaid credits; fixed-price plans are charged to your card. Destroying a resource cancels it right away, without a refund for fixed-price plans.
- **`deletion_protection`.** Set it on anything you don't want a `terraform destroy` to remove. Terraform refuses to delete it until you set it back to `false`.
- **Rate limits.** The API allows about 2 requests per second per operation. The provider paces itself and retries rate-limited calls, so large plans are slower but don't fail.
- **Debugging.** `TF_LOG=DEBUG terraform apply` logs every API call; `TF_LOG=TRACE` adds request and response bodies, with passwords, tokens and secret values masked.

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
| `NODERINGS_ACC_GAME_PLAN_ID`, `NODERINGS_ACC_GAME_TEMPLATE_ID`, `NODERINGS_ACC_GAME_TEMPLATE2_ID` | `noderings_game_server` acceptance tests |
| `NODERINGS_ACC_OS_MATRIX=1` (plus the VM variables) | `TestVirtualMachineGuestOSMatrix`: reinstalls one VM through every distro and checks over SSH that the guest runs it and that cloud-init set the hostname, root SSH key, IPv4 and DNS. Limit it with `NODERINGS_ACC_OS_MATRIX_DISTROS=ubuntu_24_04,rocky_9`; add `NODERINGS_ACC_REQUIRE_IPV6=1` to require IPv6. About an hour of one VM. |

```shell
make testacc-os   # only the guest OS matrix
```

To try a local build, add a [development override](https://developer.hashicorp.com/terraform/cli/config/config-file#development-overrides-for-provider-developers) for `noderings/noderings` pointing at your `GOBIN`, then run `make install`.

### Layout

```text
internal/client       HTTP client: auth, retries, rate limiting, errors
internal/provider     provider schema and configuration
internal/service/*    one package per product (compute, marketplace, firewall, dns,
                      gameserver, iam, billing, catalog); each resource's tests sit
                      next to it (zone.go, zone_test.go)
internal/dnsutil      DNS name and record-value normalisation
internal/fakeapi      in-memory API used by the tests
internal/acctest      test harness (fake or live API)
examples/, templates/ inputs for the generated docs/
```

## Releasing

Releases are automated with [release-please](https://github.com/googleapis/release-please) and GoReleaser: merging the release pull request tags `vX.Y.Z`, and `.github/workflows/release.yml` builds, signs and publishes the release that the Terraform Registry and the OpenTofu Registry read. Every release zip has a GitHub build provenance attestation (`gh attestation verify <zip> -R noderings/terraform-provider-noderings`). See [CONTRIBUTING.md](CONTRIBUTING.md#releasing) for the details.

## License

[MPL-2.0](LICENSE)
