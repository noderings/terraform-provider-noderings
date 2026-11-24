package marketplace_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/noderings/terraform-provider-noderings/internal/acctest"
	"github.com/noderings/terraform-provider-noderings/internal/fakeapi"
)

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
