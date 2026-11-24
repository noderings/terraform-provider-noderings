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

func rulesetConfig(name, extra string) string {
	return fmt.Sprintf(`
resource "noderings_firewall_ruleset" "test" {
  name = %q
  %s
}
`, name, extra)
}

func TestFirewallRuleSetResource(t *testing.T) {
	env := acctest.New(t)
	name := acctest.RandomName("tf-acc")
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(rulesetConfig(name, "")),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("noderings_firewall_ruleset.test", "name", name),
					resource.TestCheckResourceAttr("noderings_firewall_ruleset.test", "rule_count", "0"),
					resource.TestCheckResourceAttr("noderings_firewall_ruleset.test", "attached_vm_count", "0"),
					resource.TestCheckResourceAttr("noderings_firewall_ruleset.test", "attached_vm_hostnames.#", "0"),
					resource.TestCheckResourceAttr("noderings_firewall_ruleset.test", "force_destroy", "false"),
				),
			},
			{
				Config: env.Config(rulesetConfig(name+"-web", "")),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("noderings_firewall_ruleset.test", plancheck.ResourceActionUpdate)},
				},
				Check: resource.TestCheckResourceAttr("noderings_firewall_ruleset.test", "name", name+"-web"),
			},
			{
				ResourceName:      "noderings_firewall_ruleset.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config: env.Config(rulesetConfig(name+"-web", "") + `
data "noderings_firewall_ruleset" "by_name" {
  name = noderings_firewall_ruleset.test.name
}
data "noderings_firewall_ruleset" "by_id" {
  id = noderings_firewall_ruleset.test.id
}
`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair("data.noderings_firewall_ruleset.by_name", "id", "noderings_firewall_ruleset.test", "id"),
					resource.TestCheckResourceAttr("data.noderings_firewall_ruleset.by_id", "name", name+"-web"),
					resource.TestCheckResourceAttr("data.noderings_firewall_ruleset.by_id", "rule_count", "0"),
				),
			},
		},
	})
}

func TestFirewallRuleSetValidation(t *testing.T) {
	env := acctest.New(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{Config: env.Config(rulesetConfig("", "")), ExpectError: regexp.MustCompile(`between 1 and 63`)},
			{Config: env.Config(rulesetConfig("<web>", "")), ExpectError: regexp.MustCompile(`must not contain`)},
			{Config: env.Config(rulesetConfig(strings.Repeat("r", 64), "")), ExpectError: regexp.MustCompile(`between 1 and 63`)},
			{Config: env.Config("")},
		},
	})
}

func TestFirewallRuleSetDuplicateName(t *testing.T) {
	env := acctest.New(t)
	name := acctest.RandomName("tf-acc")
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.Config(rulesetConfig(name, "") + fmt.Sprintf(`
resource "noderings_firewall_ruleset" "dup" {
  name       = %q
  depends_on = [noderings_firewall_ruleset.test]
}
`, name)),
			ExpectError: regexp.MustCompile(`already has a firewall ruleset named`),
		}},
	})
}

func TestFirewallRuleSetDrift(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	name := acctest.RandomName("tf-acc")
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(rulesetConfig(name, "")),
				Check: func(s *terraform.State) error {
					env.Fake.RenameFirewallRuleSetOutOfBand(id(s, "noderings_firewall_ruleset.test"), "renamed")
					return nil
				},
				ExpectNonEmptyPlan: true,
			},
			{
				Config: env.Config(rulesetConfig(name, "")),
				Check: func(s *terraform.State) error {
					env.Fake.DeleteFirewallRuleSetOutOfBand(id(s, "noderings_firewall_ruleset.test"))
					return nil
				},
				ExpectNonEmptyPlan: true,
			},
			{
				Config: env.Config(rulesetConfig(name, "")),
				Check:  resource.TestCheckResourceAttr("noderings_firewall_ruleset.test", "name", name),
			},
		},
	})
}

// Deleting an attached ruleset needs force_destroy.
func TestFirewallRuleSetForceDestroy(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	name := acctest.RandomName("tf-acc")
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(rulesetConfig(name, "")),
				Check: func(s *terraform.State) error {
					env.Fake.SetFirewallRuleSetAttachedVMs(id(s, "noderings_firewall_ruleset.test"), "web-2", "web-1")
					return nil
				},
			},
			{
				Config: env.Config(rulesetConfig(name, "")),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("noderings_firewall_ruleset.test", "attached_vm_count", "2"),
					resource.TestCheckResourceAttr("noderings_firewall_ruleset.test", "attached_vm_hostnames.0", "web-1"),
				),
			},
			{
				Config: env.Config(`
data "noderings_firewall_rulesets" "attached" {
  attached = true
}
data "noderings_firewall_rulesets" "unattached" {
  attached = false
}
` + rulesetConfig(name, "") + `
resource "noderings_firewall_ruleset" "other" {
  name = "` + name + `-other"
}
`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.noderings_firewall_rulesets.attached", "rulesets.#", "1"),
					resource.TestCheckResourceAttr("data.noderings_firewall_rulesets.attached", "rulesets.0.name", name),
					resource.TestCheckResourceAttr("data.noderings_firewall_rulesets.attached", "rulesets.0.attached_vm_count", "2"),
				),
			},
			{Config: env.Config(""), ExpectError: regexp.MustCompile(`attached to 2 virtual machine\(s\): web-1, web-2[\s\S]*force_destroy`)},
			{
				Config: env.Config(rulesetConfig(name, "force_destroy = true")),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("noderings_firewall_ruleset.test", plancheck.ResourceActionUpdate)},
				},
			},
			{Config: env.Config("")},
		},
	})
}

func TestFirewallRuleSetsDataSource(t *testing.T) {
	env := acctest.New(t)
	name := acctest.RandomName("tf-acc")
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{Config: env.Config(rulesetConfig(name, ""))},
			{
				Config: env.Config(rulesetConfig(name, "") + `
data "noderings_firewall_rulesets" "all" {
  depends_on = [noderings_firewall_ruleset.test]
}
`),
				Check: func(s *terraform.State) error {
					ds := s.RootModule().Resources["data.noderings_firewall_rulesets.all"].Primary.Attributes
					n, _ := strconv.Atoi(ds["rulesets.#"])
					for i := 0; i < n; i++ {
						if ds[fmt.Sprintf("rulesets.%d.name", i)] == name {
							if ds[fmt.Sprintf("rulesets.%d.id", i)] != id(s, "noderings_firewall_ruleset.test") {
								return fmt.Errorf("wrong id for %s", name)
							}
							return nil
						}
					}
					return fmt.Errorf("ruleset %s not listed", name)
				},
			},
		},
	})
}

// --- Rules ---
