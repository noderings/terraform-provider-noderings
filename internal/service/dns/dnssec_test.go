package dns_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/noderings/terraform-provider-noderings/internal/acctest"
)

func TestDNSSECResource(t *testing.T) {
	env := acctest.New(t)
	domain := acctest.TestDomain(t)
	cfg := func(force bool) string {
		return zoneConfig(domain, "force_destroy = true") + fmt.Sprintf(`
resource "noderings_dns_zone_dnssec" "test" {
  zone_id       = noderings_dns_zone.test.id
  force_destroy = %t
}
`, force)
	}
	steps := []resource.TestStep{
		{
			Config: env.Config(cfg(false)),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("noderings_dns_zone_dnssec.test", "mode", "signed_ds_pending"),
				resource.TestMatchResourceAttr("noderings_dns_zone_dnssec.test", "ds", regexp.MustCompile(`^\d+ 13 2 [0-9A-Fa-f]+$`)),
			),
		},
		{
			ResourceName:      "noderings_dns_zone_dnssec.test",
			ImportState:       true,
			ImportStateVerify: true,
		},
	}
	if env.Fake != nil {
		// Once the DS is at the registrar, disabling needs force_destroy.
		steps = append(steps,
			resource.TestStep{
				Config: env.Config(cfg(false)),
				Check: func(s *terraform.State) error {
					env.Fake.SetDSAtParent(s.RootModule().Resources["noderings_dns_zone.test"].Primary.ID)
					return nil
				},
			},
			resource.TestStep{
				Config: env.Config(cfg(false)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("noderings_dns_zone_dnssec.test", "mode", "signed_active"),
					resource.TestCheckResourceAttr("noderings_dns_zone_dnssec.test", "ds_at_parent", "true"),
				),
			},
			resource.TestStep{Config: env.Config(zoneConfig(domain, "force_destroy = true")), ExpectError: regexp.MustCompile(`registrar first[\s\S]*force_destroy`)},
			resource.TestStep{Config: env.Config(cfg(true))},
		)
	}
	env.Run(t, resource.TestCase{Steps: steps})
}
