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

const offer = "data.noderings_marketplace_offer.test"
