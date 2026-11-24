---
page_title: "Choosing a marketplace offer"
subcategory: "Guides"
description: |-
  How the provider picks which hosting provider, plan and region a virtual machine or game server is ordered from, and how to control that choice.
---

# Choosing a marketplace offer

NodeRings is a marketplace: independent hosting providers sell compute, GPU, storage, network and other resources through it. Nothing in this Terraform provider names a hosting provider. Which one your server runs on is decided when Terraform reads the marketplace, from the filters you set.

## How the marketplace is organised

| Term | What it is | Example |
|---|---|---|
| **Plan code** | A NodeRings-wide size. Every hosting provider that sells this size lists it under the same code. | `SC-GC-1C-1G-20` is shared compute, 1 vCPU, 1 GB RAM, 20 GB disk |
| **Provider** | A hosting company selling on NodeRings. Each has an ID and may have a public storefront. | `Acme Hosting`, storefront `acme-hosting` |
| **Offer** | One provider's plan for a code, in one city, with its prices. | Acme Hosting's `SC-GC-1C-1G-20` in Frankfurt, billed hourly |
| **Plan ID / region ID** | The IDs of that offer's plan and city. These are what a VM or game server is ordered with. | `plan_id`, `region_id` |

The same plan code is usually sold by several providers, in several cities, at different prices. Plan and region IDs belong to one provider and can change, so look them up with a data source instead of hard-coding them.

## How an offer is chosen

[`noderings_marketplace_offer`](../data-sources/marketplace_offer.md) turns a plan code and a city into one offer:

1. It lists every offer for `plan_code` in `country` and `city`.
2. It keeps only offers that match the optional filters:
   - `billing_type`: `usage` (hourly, paid from prepaid credit), `subscription` (monthly, charged to your card) or `free`. Set it: without it each provider contributes one plan of its choice for the code.
   - `require_capabilities`: only providers whose platform supports all of these, for example `guest_firewall`, `on_demand_backup`, `snapshots` or `instant_apps`.
   - `require_ipv6`: only offers with IPv6 in the city.
   - `provider_id` or `storefront_slug`: only offers from that one provider.
3. It sorts what is left by `sort`: `price` (cheapest first, the default) or `rating` (best-rated provider first), and returns the first one.

If nothing matches, the data source fails and says which filter left no offers.

The result tells you who was chosen: `provider_id`, `provider_name`, `rating`, `platform_driver`, `capabilities` and `prices`.

```terraform
data "noderings_marketplace_offer" "web" {
  plan_code            = "SC-GC-1C-1G-20"
  country              = "United States"
  city                 = "Ashburn"
  billing_type         = "usage"
  require_capabilities = ["guest_firewall"]
}

output "chosen_provider" {
  value = "${data.noderings_marketplace_offer.web.provider_name} (${data.noderings_marketplace_offer.web.provider_id})"
}
```

## Keeping a server where it is

A data source is read again on every `plan`. If a cheaper or better-rated offer appears later, or the chosen one stops being sold, the lookup returns a different `plan_id` or `region_id`. Changing either on [`noderings_virtual_machine`](../resources/virtual_machine.md) or [`noderings_game_server`](../resources/game_server.md) **replaces the server**: Terraform plans to delete it and order a new one, and the disk is lost.

Always read the plan before applying. To stop this from happening, do one or more of the following.

**Pin the provider.** With `provider_id` (or `storefront_slug`) the lookup only considers that provider, so a cheaper competitor cannot move your server:

```terraform
data "noderings_marketplace_offer" "web" {
  plan_code    = "SC-GC-1C-1G-20"
  country      = "United States"
  city         = "Ashburn"
  billing_type = "usage"
  provider_id  = "00000000-0000-0000-0000-000000000000"
}
```

**Ignore later changes.** Use the lookup only to create the server, and keep it on its offer afterwards:

```terraform
resource "noderings_virtual_machine" "web" {
  hostname  = "web-1"
  plan_id   = data.noderings_marketplace_offer.web.plan_id
  region_id = data.noderings_marketplace_offer.web.region_id
  distro    = "ubuntu_24_04"
  ssh_key_ids = [noderings_ssh_key.me.id]

  lifecycle {
    ignore_changes = [plan_id, region_id]
  }
}
```

**Guard against deletion.** With `deletion_protection = true`, Terraform refuses to delete or replace the server until you set it back to `false`:

```terraform
resource "noderings_virtual_machine" "web" {
  # ...
  deletion_protection = true
}
```

## Finding plan codes, cities and providers

| To find | Use |
|---|---|
| Cities with offers | [`noderings_marketplace_regions`](../data-sources/marketplace_regions.md) |
| Plan codes, optionally per city or billing type | [`noderings_marketplace_plan_codes`](../data-sources/marketplace_plan_codes.md) |
| Every offer for a code in a city, best first | [`noderings_marketplace_offers`](../data-sources/marketplace_offers.md) |
| Providers, their IDs, ratings and capabilities | [`noderings_marketplace_providers`](../data-sources/marketplace_providers.md) |
| One provider's cities and plans | [`noderings_marketplace_provider`](../data-sources/marketplace_provider.md) |
| A provider's public storefront | [`noderings_storefront`](../data-sources/storefront.md) |
| A plan's size and prices | [`noderings_marketplace_plan`](../data-sources/marketplace_plan.md) |
| Add-ons (backups, additional IPs) and their prices | [`noderings_marketplace_addon`](../data-sources/marketplace_addon.md) |

For example, to compare every hourly offer for a code before choosing:

```terraform
data "noderings_marketplace_offers" "small" {
  plan_code    = "SC-GC-1C-1G-20"
  country      = "United States"
  city         = "Ashburn"
  billing_type = "usage"
}

output "offers" {
  value = [for o in data.noderings_marketplace_offers.small.offers : {
    provider = o.provider_name
    id       = o.provider_id
    prices   = [for p in o.prices : "${p.unit_amount} ${p.currency} ${p.unit_name}"]
  }]
}
```

And to list providers with their ratings:

```terraform
data "noderings_marketplace_providers" "vm" {
  asset_type = "virtual_machine"
}

output "providers" {
  value = [for p in data.noderings_marketplace_providers.vm.providers : "${p.name} (${p.id}), rating ${p.rating}"]
}
```

## Game servers

Game servers are chosen the same way, with a game-server plan code (`SC-GS-...`) and `asset_type = "game_server"`. The games a plan can run depend on its provider; list them with [`noderings_game_server_games`](../data-sources/game_server_games.md).

## Add-ons

Backups and additional IP addresses are sold by the provider the VM runs on. [`noderings_marketplace_addon`](../data-sources/marketplace_addon.md) with `virtual_machine_id` finds the add-ons that provider sells for that VM. When a provider sells several of one type (for example daily and weekly backups), choose one with `code`; the error message lists the codes.
