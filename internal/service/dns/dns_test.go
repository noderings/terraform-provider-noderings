package dns_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
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

func recordConfig(domain, name, typ string, ttl int, values []string) string {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = fmt.Sprintf("%q", v)
	}
	return zoneConfig(domain, "") + fmt.Sprintf(`
resource "noderings_dns_record" "test" {
  zone_id = noderings_dns_zone.test.id
  name    = %q
  type    = %q
  ttl     = %d
  records = [%s]
}
`, name, typ, ttl, strings.Join(quoted, ", "))
}

func TestDNSRecordResource(t *testing.T) {
	env := acctest.New(t)
	domain := acctest.TestDomain(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(recordConfig(domain, "www", "A", 300, []string{"192.0.2.10"})),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("noderings_dns_record.test", "records.#", "1"),
					resource.TestCheckTypeSetElemAttr("noderings_dns_record.test", "records.*", "192.0.2.10"),
					resource.TestMatchResourceAttr("noderings_dns_record.test", "id", regexp.MustCompile(`/www/A$`)),
				),
			},
			{
				// TTL and values change in place.
				Config: env.Config(recordConfig(domain, "www", "A", 600, []string{"192.0.2.10", "192.0.2.11"})),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("noderings_dns_record.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("noderings_dns_record.test", "ttl", "600"),
					resource.TestCheckResourceAttr("noderings_dns_record.test", "records.#", "2"),
				),
			},
			{
				ResourceName:      "noderings_dns_record.test",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: func(s *terraform.State) (string, error) {
					return s.RootModule().Resources["noderings_dns_record.test"].Primary.ID, nil
				},
				ImportStateVerifyIgnore: []string{"allow_overwrite"},
			},
		},
	})
}

// The API stores CNAME and MX targets with a trailing dot. Configuration
// written without it must not produce a diff.
func TestDNSRecordHostnamesDoNotDrift(t *testing.T) {
	env := acctest.New(t)
	domain := acctest.TestDomain(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(recordConfig(domain, "@", "MX", 3600, []string{"10 mail.example.net", "20 backup.example.net."})),
				Check:  resource.TestCheckTypeSetElemAttr("noderings_dns_record.test", "records.*", "10 mail.example.net"),
			},
			{
				Config:             env.Config(recordConfig(domain, "@", "MX", 3600, []string{"10 mail.example.net", "20 backup.example.net."})),
				PlanOnly:           true,
				ExpectNonEmptyPlan: false,
			},
		},
	})
}

// TXT values are written as plain text. Long values are split into 255-byte
// strings by the provider and joined again when read.
func TestDNSRecordTXT(t *testing.T) {
	env := acctest.New(t)
	domain := acctest.TestDomain(t)
	dkim := "v=DKIM1; k=rsa; p=" + strings.Repeat("MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8A", 12)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(recordConfig(domain, "sel._domainkey", "TXT", 300, []string{dkim, `say "hi"`})),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckTypeSetElemAttr("noderings_dns_record.test", "records.*", dkim),
					resource.TestCheckTypeSetElemAttr("noderings_dns_record.test", "records.*", `say "hi"`),
				),
			},
			{
				Config:   env.Config(recordConfig(domain, "sel._domainkey", "TXT", 300, []string{dkim, `say "hi"`})),
				PlanOnly: true,
			},
		},
	})
}

func TestDNSRecordValidation(t *testing.T) {
	env := acctest.New(t)
	domain := "validation.example.com"
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{Config: env.Config(recordConfig(domain, "www", "A", 300, []string{"2001:db8::1"})), ExpectError: regexp.MustCompile(`not an IPv4 address`)},
			{Config: env.Config(recordConfig(domain, "@", "CNAME", 300, []string{"x.example.net."})), ExpectError: regexp.MustCompile(`CNAME not allowed at the apex`)},
			{Config: env.Config(recordConfig(domain, "www", "A", 10, []string{"192.0.2.1"})), ExpectError: regexp.MustCompile(`between 30 and 86400`)},
			{Config: env.Config(recordConfig(domain, "WWW.example.com.", "A", 300, []string{"192.0.2.1"})), ExpectError: regexp.MustCompile(`lower-case name relative`)},
			{Config: env.Config(recordConfig(domain, "www", "SPF", 300, []string{"x"})), ExpectError: regexp.MustCompile(`value must be one of`)},
		},
	})
}

func TestDNSRecordRefusesToOverwrite(t *testing.T) {
	env := acctest.New(t)
	domain := acctest.TestDomain(t)
	second := `
resource "noderings_dns_record" "dup" {
  zone_id    = noderings_dns_zone.test.id
  name       = "www"
  type       = "A"
  ttl        = 300
  records    = ["192.0.2.99"]
  depends_on = [noderings_dns_record.test]
}
`
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config:      env.Config(recordConfig(domain, "www", "A", 300, []string{"192.0.2.10"}) + second),
				ExpectError: regexp.MustCompile(`DNS records already exist[\s\S]*allow_overwrite`),
			},
		},
	})
}

func TestDNSRecordDetectsDrift(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	domain := acctest.TestDomain(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(recordConfig(domain, "www", "A", 300, []string{"192.0.2.10"})),
				Check: func(s *terraform.State) error {
					zoneID := s.RootModule().Resources["noderings_dns_zone.test"].Primary.ID
					env.Fake.SetRecordOutOfBand(zoneID, "www", "A", 300, []string{"198.51.100.1"})
					return nil
				},
				ExpectNonEmptyPlan: true,
			},
			{
				// Applying again restores the configured value.
				Config: env.Config(recordConfig(domain, "www", "A", 300, []string{"192.0.2.10"})),
				Check:  resource.TestCheckTypeSetElemAttr("noderings_dns_record.test", "records.*", "192.0.2.10"),
			},
		},
	})
}

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
				resource.TestMatchResourceAttr("noderings_dns_zone_dnssec.test", "ds", regexp.MustCompile(`^\d+ 13 2 [0-9A-F]+$`)),
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
