package dns_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/noderings/terraform-provider-noderings/internal/acctest"
)

func zoneConfig(domain string, extra string) string {
	return fmt.Sprintf(`
resource "noderings_dns_zone" "test" {
  name = %q
  %s
}
`, domain, extra)
}

func TestDNSZoneResource(t *testing.T) {
	env := acctest.New(t)
	domain := acctest.TestDomain(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(zoneConfig(domain, "")),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("noderings_dns_zone.test", "name", domain),
					resource.TestCheckResourceAttr("noderings_dns_zone.test", "state", "pending_ns"),
					resource.TestCheckResourceAttr("noderings_dns_zone.test", "dnssec_mode", "unsigned"),
					resource.TestCheckResourceAttr("noderings_dns_zone.test", "deletion_protection", "false"),
					resource.TestCheckResourceAttrSet("noderings_dns_zone.test", "nameservers.0"),
				),
			},
			{
				ResourceName:            "noderings_dns_zone.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"delegation_ok", "state"},
			},
			{
				Config: env.Config(zoneConfig(domain, "") + `
data "noderings_dns_zone" "by_name" {
  name = noderings_dns_zone.test.name
}
`),
				Check: resource.TestCheckResourceAttrPair("data.noderings_dns_zone.by_name", "id", "noderings_dns_zone.test", "id"),
			},
		},
	})
}

func TestDNSZoneResourceRejectsUnnormalizedName(t *testing.T) {
	env := acctest.New(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{{
			Config:      env.Config(zoneConfig("Example.COM.", "")),
			ExpectError: regexp.MustCompile(`example\.com`),
		}},
	})
}

func TestDNSZoneDeletionProtection(t *testing.T) {
	env := acctest.New(t)
	domain := acctest.TestDomain(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{Config: env.Config(zoneConfig(domain, "deletion_protection = true"))},
			{Config: env.Config(""), ExpectError: regexp.MustCompile(`Deletion protection is on`)},
			{Config: env.Config(zoneConfig(domain, "deletion_protection = false"))},
		},
	})
}

func TestDNSZoneRemovedOutOfBand(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	domain := acctest.TestDomain(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.Config(zoneConfig(domain, "")),
			Check: func(s *terraform.State) error {
				env.Fake.DeleteZoneOutOfBand(s.RootModule().Resources["noderings_dns_zone.test"].Primary.ID)
				return nil
			},
			ExpectNonEmptyPlan: true,
		}},
	})
}
