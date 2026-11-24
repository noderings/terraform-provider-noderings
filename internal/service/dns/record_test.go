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
