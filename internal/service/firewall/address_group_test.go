package firewall_test

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/noderings/terraform-provider-noderings/internal/acctest"
)

func groupConfig(name string, cidrs ...string) string {
	q := make([]string, len(cidrs))
	for i, c := range cidrs {
		q[i] = strconv.Quote(c)
	}
	return fmt.Sprintf(`
resource "noderings_firewall_address_group" "test" {
  name  = %q
  cidrs = [%s]
}
`, name, strings.Join(q, ", "))
}

func TestFirewallAddressGroupResource(t *testing.T) {
	env := acctest.New(t)
	name := acctest.RandomName("tf-acc")
	renamed := name + "-2"
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(groupConfig(name, "203.0.113.0/24", "2001:db8:10::/48")),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("noderings_firewall_address_group.test", "name", name),
					resource.TestCheckResourceAttr("noderings_firewall_address_group.test", "cidrs.#", "2"),
					resource.TestCheckTypeSetElemAttr("noderings_firewall_address_group.test", "cidrs.*", "2001:db8:10::/48"),
					resource.TestCheckResourceAttrSet("noderings_firewall_address_group.test", "id"),
					resource.TestCheckResourceAttrSet("noderings_firewall_address_group.test", "created_at"),
				),
			},
			{
				ResourceName:      "noderings_firewall_address_group.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				// Name and CIDRs change in place. Non-canonical CIDRs are kept as written.
				Config: env.Config(groupConfig(renamed, "198.51.100.7/24", "2001:0db8:0020:0000::/64", "192.0.2.1/32")),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("noderings_firewall_address_group.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("noderings_firewall_address_group.test", "name", renamed),
					resource.TestCheckResourceAttr("noderings_firewall_address_group.test", "cidrs.#", "3"),
					resource.TestCheckTypeSetElemAttr("noderings_firewall_address_group.test", "cidrs.*", "198.51.100.7/24"),
				),
			},
			{
				Config:   env.Config(groupConfig(renamed, "198.51.100.7/24", "2001:0db8:0020:0000::/64", "192.0.2.1/32")),
				PlanOnly: true,
			},
			{
				// The data source shows the canonical form.
				Config: env.Config(groupConfig(renamed, "198.51.100.7/24", "2001:0db8:0020:0000::/64", "192.0.2.1/32") + `
data "noderings_firewall_address_group" "by_name" {
  name = noderings_firewall_address_group.test.name
}
data "noderings_firewall_address_group" "by_id" {
  id = noderings_firewall_address_group.test.id
}
`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair("data.noderings_firewall_address_group.by_name", "id", "noderings_firewall_address_group.test", "id"),
					resource.TestCheckResourceAttr("data.noderings_firewall_address_group.by_id", "name", renamed),
					resource.TestCheckTypeSetElemAttr("data.noderings_firewall_address_group.by_id", "cidrs.*", "198.51.100.0/24"),
					resource.TestCheckTypeSetElemAttr("data.noderings_firewall_address_group.by_id", "cidrs.*", "2001:db8:20::/64"),
					resource.TestCheckTypeSetElemAttr("data.noderings_firewall_address_group.by_id", "cidrs.*", "192.0.2.1/32"),
				),
			},
		},
	})
}

func TestFirewallAddressGroupDuplicateName(t *testing.T) {
	env := acctest.New(t)
	name := acctest.RandomName("tf-acc")
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.Config(groupConfig(name, "192.0.2.0/24") + fmt.Sprintf(`
resource "noderings_firewall_address_group" "dup" {
  name       = %q
  cidrs      = ["198.51.100.0/24"]
  depends_on = [noderings_firewall_address_group.test]
}
`, name)),
			ExpectError: regexp.MustCompile(`already has an address group named`),
		}},
	})
}

func TestFirewallAddressGroupValidation(t *testing.T) {
	env := acctest.New(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{Config: env.Config(groupConfig("bad", "192.0.2.300/24")), ExpectError: regexp.MustCompile(`not a valid IP address or CIDR`)},
			{Config: env.Config(groupConfig("bad", "192.0.2.1")), ExpectError: regexp.MustCompile(`is not a CIDR`)},
			{Config: env.Config(groupConfig("bad")), ExpectError: regexp.MustCompile(`at least 1`)},
			{Config: env.Config(groupConfig("a & b", "192.0.2.0/24")), ExpectError: regexp.MustCompile(`must not contain`)},
			{Config: env.Config(groupConfig(strings.Repeat("a", 64), "192.0.2.0/24")), ExpectError: regexp.MustCompile(`between 1 and 63`)},
			{Config: env.Config(groupConfig("dup", "192.0.2.0/24", "192.0.2.9/24")), ExpectError: regexp.MustCompile(`same network`)},
			// A valid last step, so the post-test destroy has a usable configuration.
			{Config: env.Config("")},
		},
	})
}

func TestFirewallAddressGroupDrift(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	name := acctest.RandomName("tf-acc")
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(groupConfig(name, "192.0.2.0/24")),
				Check: func(s *terraform.State) error {
					env.Fake.SetFirewallAddressGroupOutOfBand(id(s, "noderings_firewall_address_group.test"), "198.51.100.0/24")
					return nil
				},
				ExpectNonEmptyPlan: true,
			},
			{
				Config: env.Config(groupConfig(name, "192.0.2.0/24")),
				Check: func(s *terraform.State) error {
					env.Fake.DeleteFirewallAddressGroupOutOfBand(id(s, "noderings_firewall_address_group.test"))
					return nil
				},
				ExpectNonEmptyPlan: true,
			},
			{
				Config: env.Config(groupConfig(name, "192.0.2.0/24")),
				Check:  resource.TestCheckTypeSetElemAttr("noderings_firewall_address_group.test", "cidrs.*", "192.0.2.0/24"),
			},
		},
	})
}

func TestFirewallAddressGroupInUse(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	group := acctest.RandomName("tf-acc")
	rs := acctest.RandomName("tf-acc")
	var ruleID string
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(groupConfig(group, "192.0.2.0/24") + rulesetConfig(rs, "")),
				Check: func(s *terraform.State) error {
					ruleID = env.Fake.AddFirewallRuleOutOfBand(id(s, "noderings_firewall_ruleset.test"), "accept", id(s, "noderings_firewall_address_group.test"))
					return nil
				},
			},
			{
				Config:      env.Config(rulesetConfig(rs, "")),
				ExpectError: regexp.MustCompile(`still use the group: ` + rs),
			},
			{
				PreConfig: func() { env.Fake.DeleteFirewallRuleOutOfBand(ruleID) },
				Config:    env.Config(rulesetConfig(rs, "")),
			},
		},
	})
}

// --- Rulesets ---
