package dns_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/noderings/terraform-provider-noderings/internal/acctest"
)

func geoConfig(domain string, eu string) string {
	return zoneConfig(domain, "") + fmt.Sprintf(`
resource "noderings_dns_geo_record" "test" {
  zone_id    = noderings_dns_zone.test.id
  name       = "app"
  type       = "A"
  default    = "192.0.2.1"
  continents = { EU = %q }
  countries  = { JP = "192.0.2.3" }
}
`, eu)
}

func TestDNSGeoRecordResource(t *testing.T) {
	env := acctest.New(t)
	domain := acctest.TestDomain(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(geoConfig(domain, "192.0.2.2")),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("noderings_dns_geo_record.test", "ttl", "60"),
					resource.TestCheckResourceAttr("noderings_dns_geo_record.test", "continents.EU", "192.0.2.2"),
					resource.TestCheckResourceAttr("noderings_dns_geo_record.test", "countries.JP", "192.0.2.3"),
					resource.TestCheckNoResourceAttr("noderings_dns_geo_record.test", "regions"),
				),
			},
			{
				Config: env.Config(geoConfig(domain, "192.0.2.20")),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("noderings_dns_geo_record.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.TestCheckResourceAttr("noderings_dns_geo_record.test", "continents.EU", "192.0.2.20"),
			},
			{
				ResourceName:      "noderings_dns_geo_record.test",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: func(s *terraform.State) (string, error) {
					return s.RootModule().Resources["noderings_dns_geo_record.test"].Primary.ID, nil
				},
			},
		},
	})
}

func TestDNSGeoRecordValidation(t *testing.T) {
	env := acctest.New(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.Config(zoneConfig("geo.example.com", "") + `
resource "noderings_dns_geo_record" "test" {
  zone_id    = noderings_dns_zone.test.id
  name       = "app"
  type       = "MX"
  ttl        = 300
  default    = "x"
  continents = { Europe = "192.0.2.2" }
}
`),
			ExpectError: regexp.MustCompile(`(value must be one of|continent code|between 30 and 60)`),
		}},
	})
}
