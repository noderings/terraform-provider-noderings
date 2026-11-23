package marketplace_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/noderings/terraform-provider-noderings/internal/acctest"
	"github.com/noderings/terraform-provider-noderings/internal/fakeapi"
)

func offerConfig(extra string) string {
	return fmt.Sprintf(`
data "noderings_marketplace_offer" "test" {
  plan_code = %q
  country   = "germany"
  city      = "Frankfurt"
  %s
}
`, fakeapi.VMPlanCode, extra)
}

const offer = "data.noderings_marketplace_offer.test"

func TestMarketplaceOfferDataSource(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				// Cheapest first by default.
				Config: env.Config(offerConfig(`billing_type = "usage"`)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(offer, "plan_id", fakeapi.BudgetVMPlanUsageFRAID),
					resource.TestCheckResourceAttr(offer, "region_id", fakeapi.BudgetRegionFRAID),
					resource.TestCheckResourceAttr(offer, "provider_id", fakeapi.BudgetProviderID),
					resource.TestCheckResourceAttr(offer, "provider_name", fakeapi.BudgetProviderName),
					resource.TestCheckResourceAttr(offer, "platform_driver", "virtfusion"),
					resource.TestCheckResourceAttr(offer, "billing_type", "usage"),
					resource.TestCheckResourceAttr(offer, "ipv6_available", "false"),
				),
			},
			{
				Config: env.Config(offerConfig(`
  billing_type         = "usage"
  require_capabilities = ["guest_firewall", "on_demand_backup"]`)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(offer, "plan_id", fakeapi.VMPlanUsageFRAID),
					resource.TestCheckResourceAttr(offer, "region_id", fakeapi.RegionFrankfurtID),
					resource.TestCheckResourceAttr(offer, "provider_id", fakeapi.ProviderID),
					resource.TestCheckResourceAttr(offer, "platform_driver", "proxmox"),
					resource.TestCheckTypeSetElemAttr(offer, "capabilities.*", "snapshots"),
					resource.TestCheckResourceAttr(offer, "vcpus", "2"),
					resource.TestCheckResourceAttr(offer, "ram_gb", "4"),
					resource.TestCheckResourceAttr(offer, "disk_gb", "80"),
					resource.TestCheckResourceAttr(offer, "prices.0.billing_period", "hour"),
					resource.TestCheckResourceAttr(offer, "prices.0.pricing_model", "usage_based"),
					resource.TestCheckResourceAttr(offer, "prices.0.currency", "EUR"),
				),
			},
			{
				Config: env.Config(offerConfig(`
  billing_type = "usage"
  require_ipv6 = true`)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(offer, "plan_id", fakeapi.VMPlanUsageFRAID),
					resource.TestCheckResourceAttr(offer, "ipv6_available", "true"),
				),
			},
			{
				Config: env.Config(offerConfig(`
  billing_type = "usage"
  sort         = "rating"`)),
				Check: resource.TestCheckResourceAttr(offer, "provider_id", fakeapi.ProviderID),
			},
			{
				// The storefront slug resolves the provider; the catalog gives the monthly plan.
				Config: env.Config(offerConfig(fmt.Sprintf(`
  billing_type    = "subscription"
  storefront_slug = %q`, fakeapi.StorefrontSlug))),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(offer, "plan_id", fakeapi.VMPlanSubscriptionFRA),
					resource.TestCheckResourceAttr(offer, "billing_type", "subscription"),
					resource.TestCheckResourceAttr(offer, "prices.0.billing_period", "month"),
				),
			},
			{
				Config: env.Config(fmt.Sprintf(`
data "noderings_marketplace_offer" "test" {
  plan_code    = %q
  country      = "Netherlands"
  city         = "amsterdam"
  billing_type = "usage"
  provider_id  = %q
}
`, fakeapi.VMPlanCode, fakeapi.ProviderID)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(offer, "plan_id", fakeapi.VMPlanUsageAMSID),
					resource.TestCheckResourceAttr(offer, "region_id", fakeapi.RegionAmsterdamID),
				),
			},
		},
	})
}

func TestMarketplaceOfferNoMatch(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(fmt.Sprintf(`
data "noderings_marketplace_offer" "test" {
  plan_code    = %q
  country      = "France"
  city         = "Paris"
  billing_type = "usage"
}
`, fakeapi.VMPlanCode)),
				ExpectError: regexp.MustCompile(`(?s)No\s+offer\s+for\s+plan\s+code.*sold\s+in:\s+Amsterdam\s+\(Netherlands\),\s+Frankfurt\s+\(Germany\)`),
			},
			{
				Config: env.Config(offerConfig(fmt.Sprintf(`
  storefront_slug      = %q
  require_capabilities = ["snapshots"]`, fakeapi.BudgetStorefrontSlug))),
				ExpectError: regexp.MustCompile(`1\s+offer\(s\)\s+matched\s+the\s+location`),
			},
			{
				Config: env.Config(offerConfig(`billing_type = "usage"`)),
			},
		},
	})
}

func TestMarketplaceOfferValidation(t *testing.T) {
	env := acctest.New(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{Config: env.Config(offerConfig(`billing_type = "hourly"`)), ExpectError: regexp.MustCompile(`value\s+must\s+be\s+one\s+of`)},
			{Config: env.Config(offerConfig(`sort = "cheapest"`)), ExpectError: regexp.MustCompile(`value\s+must\s+be\s+one\s+of`)},
			{
				Config:      env.Config(offerConfig(`provider_id = "5a1e0000-0000-4000-8000-000000000001"` + "\n" + `storefront_slug = "x"`)),
				ExpectError: regexp.MustCompile(`cannot\s+be\s+configured\s+together`),
			},
			{Config: env.Config(offerConfig(`provider_id = "nope"`)), ExpectError: regexp.MustCompile(`must\s+be\s+a\s+UUID`)},
		},
	})
}

func TestMarketplaceOffersDataSource(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.Config(fmt.Sprintf(`
data "noderings_marketplace_offers" "test" {
  plan_code    = %q
  country      = "Germany"
  city         = "Frankfurt"
  billing_type = "usage"
}
`, fakeapi.VMPlanCode)),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.noderings_marketplace_offers.test", "offers.#", "2"),
				resource.TestCheckResourceAttr("data.noderings_marketplace_offers.test", "offers.0.provider_name", fakeapi.BudgetProviderName),
				resource.TestCheckResourceAttr("data.noderings_marketplace_offers.test", "offers.1.plan_id", fakeapi.VMPlanUsageFRAID),
				resource.TestCheckResourceAttr("data.noderings_marketplace_offers.test", "offers.1.region_id", fakeapi.RegionFrankfurtID),
				resource.TestCheckResourceAttr("data.noderings_marketplace_offers.test", "offers.1.total_reviews", "120"),
			),
		}},
	})
}

func TestMarketplaceProviderDataSources(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	const p = "data.noderings_marketplace_provider.by_slug"
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(fmt.Sprintf(`
data "noderings_marketplace_provider" "by_slug" {
  storefront_slug = %q
}

data "noderings_marketplace_provider" "by_id" {
  id = %q
}

data "noderings_marketplace_providers" "all" {}

data "noderings_marketplace_providers" "budget" {
  search = "budget"
}

data "noderings_storefront" "test" {
  slug = %q
}
`, fakeapi.StorefrontSlug, fakeapi.BudgetProviderID, fakeapi.StorefrontSlug)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(p, "id", fakeapi.ProviderID),
					resource.TestCheckResourceAttr(p, "name", fakeapi.ProviderName),
					resource.TestCheckResourceAttr(p, "has_ipv6", "true"),
					resource.TestCheckResourceAttr(p, "cities.#", "2"),
					resource.TestCheckResourceAttr(p, "cities.0", "Amsterdam"),
					resource.TestCheckResourceAttr(p, "plans.#", "5"),
					// Sorted by plan code, then billing type.
					resource.TestCheckResourceAttr(p, "plans.0.plan_code", fakeapi.VMFreePlanCode),
					resource.TestCheckResourceAttr(p, "plans.0.billing_type", "free"),
					resource.TestCheckResourceAttr(p, "plans.1.billing_type", "subscription"),
					resource.TestCheckResourceAttr(p, "plans.4.plan_id", fakeapi.GamePlanID),
					resource.TestCheckResourceAttr(p, "plans.4.asset_type", "gameserver"),
					resource.TestCheckResourceAttr(p, "plans.4.regions.0.region_id", fakeapi.RegionFrankfurtID),
					resource.TestCheckResourceAttr("data.noderings_marketplace_provider.by_id", "platform_driver", "virtfusion"),
					resource.TestCheckResourceAttr("data.noderings_marketplace_providers.all", "providers.#", "2"),
					resource.TestCheckResourceAttr("data.noderings_marketplace_providers.all", "providers.0.name", fakeapi.BudgetProviderName),
					resource.TestCheckResourceAttr("data.noderings_marketplace_providers.all", "providers.1.id", fakeapi.ProviderID),
					resource.TestCheckResourceAttr("data.noderings_marketplace_providers.budget", "providers.#", "1"),
					resource.TestCheckResourceAttr("data.noderings_storefront.test", "provider_id", fakeapi.ProviderID),
					resource.TestCheckResourceAttr("data.noderings_storefront.test", "organization_name", fakeapi.ProviderName),
					resource.TestCheckResourceAttr("data.noderings_storefront.test", "faqs.#", "1"),
					resource.TestCheckResourceAttr("data.noderings_storefront.test", "highlights.#", "3"),
				),
			},
			{
				Config:      env.Config(`data "noderings_storefront" "nope" { slug = "no-such-storefront" }`),
				ExpectError: regexp.MustCompile(`not\s+found`),
			},
			{
				Config:      env.Config(`data "noderings_marketplace_provider" "none" {}`),
				ExpectError: regexp.MustCompile(`storefront_slug`),
			},
			{Config: env.Config(`data "noderings_marketplace_providers" "all" {}`)},
		},
	})
}

func TestMarketplacePlanDataSources(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	const codes = "data.noderings_marketplace_plan_codes.all"
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(fmt.Sprintf(`
data "noderings_marketplace_plan_codes" "all" {}

data "noderings_marketplace_plan_codes" "ams" {
  country = "Netherlands"
  city    = "Amsterdam"
}

data "noderings_marketplace_plan_codes" "games" {
  asset_type = "game_server"
}

data "noderings_marketplace_plan" "test" {
  plan_id = %q
}
`, fakeapi.VMPlanUsageFRAID)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(codes, "plan_codes.#", "4"),
					resource.TestCheckResourceAttr(codes, "plan_codes.0.plan_code", fakeapi.VMFreePlanCode),
					resource.TestCheckResourceAttr(codes, "plan_codes.1.plan_code", fakeapi.VMPlanCode),
					resource.TestCheckResourceAttr(codes, "plan_codes.1.billing_type", "subscription"),
					resource.TestCheckResourceAttr(codes, "plan_codes.2.billing_type", "usage"),
					resource.TestCheckResourceAttr(codes, "plan_codes.2.compute_type", "general_compute"),
					resource.TestCheckResourceAttr("data.noderings_marketplace_plan_codes.ams", "plan_codes.#", "1"),
					resource.TestCheckResourceAttr("data.noderings_marketplace_plan_codes.games", "plan_codes.0.plan_code", fakeapi.GamePlanCode),
					resource.TestCheckResourceAttr("data.noderings_marketplace_plan.test", "plan_code", fakeapi.VMPlanCode),
					resource.TestCheckResourceAttr("data.noderings_marketplace_plan.test", "region_id", fakeapi.RegionFrankfurtID),
					resource.TestCheckResourceAttr("data.noderings_marketplace_plan.test", "region_city", "Frankfurt"),
					resource.TestCheckResourceAttr("data.noderings_marketplace_plan.test", "provider_name", fakeapi.ProviderName),
					resource.TestCheckResourceAttr("data.noderings_marketplace_plan.test", "billing_type", "usage"),
					resource.TestCheckResourceAttr("data.noderings_marketplace_plan.test", "vcpus", "2"),
				),
			},
			{
				Config:      env.Config(`data "noderings_marketplace_plan_codes" "x" { city = "Amsterdam" }`),
				ExpectError: regexp.MustCompile(`country`),
			},
			{
				Config:      env.Config(`data "noderings_marketplace_plan" "gone" { plan_id = "7c3a0000-0000-4000-8000-000000000999" }`),
				ExpectError: regexp.MustCompile(`not\s+for\s+sale`),
			},
			{Config: env.Config(`data "noderings_marketplace_plan_codes" "all" {}`)},
		},
	})
}

func TestMarketplaceAddonDataSource(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	addon := func(name, body string) string {
		return fmt.Sprintf("data \"noderings_marketplace_addon\" %q {\n  provider_id = %q\n  %s\n}\n", name, fakeapi.ProviderID, body)
	}
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				// Two backup add-ons exist; only one is compatible with the plan code.
				Config: env.Config(addon("backup", fmt.Sprintf("type = \"backup\"\n  plan_code = %q", fakeapi.VMPlanCode)) +
					addon("ip", `type = "additional_ip"`)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.noderings_marketplace_addon.backup", "id", fakeapi.BackupAddonID),
					resource.TestCheckResourceAttr("data.noderings_marketplace_addon.backup", "code", "backup-daily"),
					resource.TestCheckResourceAttr("data.noderings_marketplace_addon.backup", "prices.0.unit_amount", "2.00"),
					resource.TestCheckResourceAttr("data.noderings_marketplace_addon.backup", "prices.0.kind", "subscription"),
					resource.TestCheckResourceAttr("data.noderings_marketplace_addon.ip", "id", fakeapi.AdditionalIPAddonID),
				),
			},
			{
				Config:      env.Config(addon("backup", `type = "backup"`)),
				ExpectError: regexp.MustCompile(`2\s+backup\s+add-ons\s+match\s+\(backup-daily,\s+backup-large\);\s+set\s+code`),
			},
			{
				Config: env.Config(addon("backup", "type = \"backup\"\n  code = \"backup-large\"")),
				Check:  resource.TestCheckResourceAttr("data.noderings_marketplace_addon.backup", "id", fakeapi.LargeBackupAddonID),
			},
			{
				Config:      env.Config(addon("backup", `type = "snapshot"`)),
				ExpectError: regexp.MustCompile(`value\s+must\s+be\s+one\s+of`),
			},
			{Config: env.Config(addon("ip", `type = "additional_ip"`))},
		},
	})
}
