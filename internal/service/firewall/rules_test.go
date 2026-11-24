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
	"github.com/noderings/terraform-provider-noderings/internal/service/firewall"
)

func rulesConfig(name, blocks, extra string) string {
	return groupConfig(name, "203.0.113.0/24") + rulesetConfig(name, "") + fmt.Sprintf(`
resource "noderings_firewall_ruleset_rules" "test" {
  ruleset_id = noderings_firewall_ruleset.test.id
  %s
%s
}
`, extra, blocks)
}

// checkServerOrder checks that the fake's rule order matches the state.
func checkServerOrder(env *acctest.Env) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		if env.Fake == nil {
			return nil
		}
		ids := env.Fake.FirewallRuleIDs(ruleAttr(s, "ruleset_id"))
		n, _ := strconv.Atoi(ruleAttr(s, "rule.#"))
		if len(ids) != n {
			return fmt.Errorf("server has %d rules, state %d", len(ids), n)
		}
		for i, want := range ids {
			if got := ruleAttr(s, fmt.Sprintf("rule.%d.id", i)); got != want {
				return fmt.Errorf("rule %d: state %s, server %s", i, got, want)
			}
			if got := ruleAttr(s, fmt.Sprintf("rule.%d.position", i)); got != strconv.Itoa(i) {
				return fmt.Errorf("rule %d: position %s", i, got)
			}
		}
		return nil
	}
}

func TestFirewallRuleSetRulesResource(t *testing.T) {
	env := acctest.New(t)
	name := acctest.RandomName("tf-acc")
	ids := map[string]string{}
	save := func(s *terraform.State) error {
		for i := 0; i < 4; i++ {
			ids[strconv.Itoa(i)] = ruleAttr(s, fmt.Sprintf("rule.%d.id", i))
		}
		return nil
	}
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(rulesConfig(name, ruleBlocks, "")),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(rulesAddr, "rule.#", "4"),
					resource.TestCheckResourceAttrPair(rulesAddr, "id", "noderings_firewall_ruleset.test", "id"),
					resource.TestCheckResourceAttr(rulesAddr, "rule.0.destination_ports", "443"),
					resource.TestCheckResourceAttr(rulesAddr, "rule.0.position", "0"),
					resource.TestCheckResourceAttrPair(rulesAddr, "rule.1.source.address_group_ids.0", "noderings_firewall_address_group.test", "id"),
					resource.TestCheckResourceAttr(rulesAddr, "rule.2.action", "drop"),
					resource.TestCheckResourceAttr(rulesAddr, "rule.3.source.cidrs.0", "::/0"),
					resource.TestCheckResourceAttr(rulesAddr, "rule.3.position", "3"),
					resource.TestCheckNoResourceAttr(rulesAddr, "rule.3.destination_ports"),
					checkServerOrder(env),
					save,
				),
			},
			{
				Config:   env.Config(rulesConfig(name, ruleBlocks, "")),
				PlanOnly: true,
			},
			{
				Config: env.Config(rulesConfig(name, ruleBlocksChanged, "")),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(rulesAddr, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(rulesAddr, "rule.#", "4"),
					resource.TestCheckResourceAttr(rulesAddr, "rule.0.direction", "egress"),
					resource.TestCheckResourceAttr(rulesAddr, "rule.0.source_ports", "1024-65535"),
					resource.TestCheckResourceAttr(rulesAddr, "rule.0.destination.cidrs.#", "2"),
					resource.TestCheckResourceAttr(rulesAddr, "rule.1.destination_ports", "2222"),
					// Rules keep their IDs: unchanged drop rules were moved, and
					// changed rules were updated in place at the same index.
					func(s *terraform.State) error {
						for i, want := range map[int]string{0: ids["0"], 1: ids["1"], 2: ids["3"], 3: ids["2"]} {
							if got := ruleAttr(s, fmt.Sprintf("rule.%d.id", i)); got != want {
								return fmt.Errorf("rule %d: id %s, want %s", i, got, want)
							}
						}
						return nil
					},
					checkServerOrder(env),
				),
			},
			{
				Config:   env.Config(rulesConfig(name, ruleBlocksChanged, "")),
				PlanOnly: true,
			},
			{
				ResourceName:      rulesAddr,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: func(s *terraform.State) (string, error) { return id(s, "noderings_firewall_ruleset.test"), nil },
			},
			{
				// Reverse the whole list.
				Config: env.Config(rulesConfig(name, reverseBlocks(ruleBlocksChanged), "")),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(rulesAddr, "rule.0.source.cidrs.0", "0.0.0.0/0"),
					resource.TestCheckResourceAttr(rulesAddr, "rule.3.direction", "egress"),
					checkServerOrder(env),
				),
			},
			{
				// Insert new rules in the middle and at the end of the list.
				Config: env.Config(rulesConfig(name, growBlocks(reverseBlocks(ruleBlocksChanged)), "")),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(rulesAddr, "rule.#", "6"),
					resource.TestCheckResourceAttr(rulesAddr, "rule.0.source.cidrs.0", "0.0.0.0/0"),
					resource.TestCheckResourceAttr(rulesAddr, "rule.2.destination_ports", "8000-8100"),
					resource.TestCheckResourceAttr(rulesAddr, "rule.2.source.vm_tags.#", "0"),
					resource.TestCheckResourceAttr(rulesAddr, "rule.5.protocol", "ipv6_icmp"),
					resource.TestCheckResourceAttr(rulesAddr, "rule.4.direction", "egress"),
					checkServerOrder(env),
				),
			},
			{
				Config:   env.Config(rulesConfig(name, growBlocks(reverseBlocks(ruleBlocksChanged)), "")),
				PlanOnly: true,
			},
			{
				// An empty list removes every rule but keeps the ruleset.
				Config: env.Config(rulesConfig(name, "", "")),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(rulesAddr, "rule.#", "0"),
					checkServerOrder(env),
				),
			},
		},
	})
}

// growBlocks adds a rule after the second block and one at the end.
func growBlocks(blocks string) string {
	parts := strings.Split(blocks, "\n  rule {")
	middle := `
  rule {
    direction         = "ingress"
    action            = "accept"
    protocol          = "tcp"
    destination_ports = "8000-8100"
    source { cidrs = ["198.51.100.0/24", "2001:db8:1::/48"] }
  }`
	end := `
  rule {
    direction = "ingress"
    action    = "accept"
    protocol  = "ipv6_icmp"
    source { cidrs = ["::/0"] }
  }
`
	out := parts[0]
	for i := 1; i < len(parts); i++ {
		out += "\n  rule {" + parts[i]
		if i == 2 {
			out += middle
		}
	}
	return out + end
}

// reverseBlocks reverses the order of top-level rule blocks.
func reverseBlocks(blocks string) string {
	parts := strings.Split(blocks, "\n  rule {")
	var out []string
	for i := len(parts) - 1; i >= 1; i-- {
		out = append(out, "\n  rule {"+parts[i])
	}
	return strings.Join(out, "")
}

func TestFirewallRuleSetRulesDrift(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	name := acctest.RandomName("tf-acc")
	cfg := env.Config(rulesConfig(name, ruleBlocks, ""))
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				// A rule added in the console shows up as a change to remove.
				Config: cfg,
				Check: func(s *terraform.State) error {
					env.Fake.AddFirewallRuleOutOfBand(id(s, "noderings_firewall_ruleset.test"), "accept", "198.51.100.0/24")
					return nil
				},
				ExpectNonEmptyPlan: true,
			},
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(rulesAddr, "rule.#", "4"),
					resource.TestCheckResourceAttr(rulesAddr, "rule.0.destination_ports", "443"),
					checkServerOrder(env),
					func(s *terraform.State) error {
						env.Fake.DeleteFirewallRuleOutOfBand(ruleAttr(s, "rule.1.id"))
						return nil
					},
				),
				ExpectNonEmptyPlan: true,
			},
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(rulesAddr, "rule.#", "4"),
					resource.TestCheckResourceAttr(rulesAddr, "rule.1.destination_ports", "22"),
					checkServerOrder(env),
					func(s *terraform.State) error {
						env.Fake.DeleteFirewallRuleSetOutOfBand(id(s, "noderings_firewall_ruleset.test"))
						return nil
					},
				),
				ExpectNonEmptyPlan: true,
			},
			{
				Config: cfg,
				Check:  checkServerOrder(env),
			},
		},
	})
}

// A ruleset that already has rules is not taken over by accident.
func TestFirewallRuleSetRulesAdoptExisting(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	name := acctest.RandomName("tf-acc")
	var kept string
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(groupConfig(name, "203.0.113.0/24") + rulesetConfig(name, "")),
				Check: func(s *terraform.State) error {
					rs := id(s, "noderings_firewall_ruleset.test")
					env.Fake.AddFirewallRuleOutOfBand(rs, "accept", "192.0.2.0/24")
					// Matches the third rule block (drop any from 0.0.0.0/0), so it is kept.
					kept = env.Fake.AddFirewallRuleOutOfBand(rs, "drop", "0.0.0.0/0")
					return nil
				},
			},
			{
				Config:      env.Config(rulesConfig(name, ruleBlocks, "")),
				ExpectError: regexp.MustCompile(`already has 2 rule\(s\)[\s\S]*adopt_existing`),
			},
			{
				Config: env.Config(rulesConfig(name, ruleBlocks, "adopt_existing = true")),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(rulesAddr, "rule.#", "4"),
					func(s *terraform.State) error {
						if got := ruleAttr(s, "rule.2.id"); got != kept {
							return fmt.Errorf("rule 2 is %s, want the adopted rule %s", got, kept)
						}
						return nil
					},
					checkServerOrder(env),
				),
			},
		},
	})
}

func TestFirewallRuleSetRulesValidation(t *testing.T) {
	env := acctest.New(t)
	rule := func(body string) string {
		return `
resource "noderings_firewall_ruleset_rules" "test" {
  ruleset_id = "00000000-0000-4000-8000-000000000001"
  rule {
    direction = "ingress"
    action    = "accept"
` + body + `
  }
}
`
	}
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config:      env.Config(rule(`protocol = "tcp"` + "\n" + `source { cidrs = ["0.0.0.0/0", "192.0.2.0/24"] }`)),
				ExpectError: regexp.MustCompile(`must be the only entry`),
			},
			{
				Config:      env.Config(rule(`protocol = "tcp"` + "\n" + `source { cidrs = ["0.0.0.0/0", "::/0"] }`)),
				ExpectError: regexp.MustCompile(`write two rules`),
			},
			{
				Config:      env.Config(rule(`protocol = "any"` + "\n" + `source { cidrs = ["::/0"] }` + "\n" + `destination { cidrs = ["192.0.2.0/24"] }` + "\n" + `destination_ports = "80"`)),
				ExpectError: regexp.MustCompile(`Ports are only supported with tcp`),
			},
			{
				Config:      env.Config(rule(`protocol = "icmp"` + "\n" + `source { cidrs = ["192.0.2.0/24"] }` + "\n" + `source_ports = "80"`)),
				ExpectError: regexp.MustCompile(`has no ports`),
			},
			{
				Config:      env.Config(rule(`protocol = "tcp"` + "\n" + `source { cidrs = ["192.0.2.0/24"] }` + "\n" + `destination_ports = "80,443"`)),
				ExpectError: regexp.MustCompile(`"80,443" is not a port`),
			},
			{
				Config:      env.Config(rule(`protocol = "tcp"` + "\n" + `source { cidrs = ["192.0.2.0/24"] }` + "\n" + `destination_ports = "9000-8000"`)),
				ExpectError: regexp.MustCompile(`is not a port`),
			},
			{
				Config:      env.Config(rule(`protocol = "udp"` + "\n" + `source { cidrs = ["192.0.2.0/24"] }` + "\n" + `destination_ports = "0"`)),
				ExpectError: regexp.MustCompile(`is not a port`),
			},
			{
				Config:      env.Config(rule(`protocol = "tcp"`)),
				ExpectError: regexp.MustCompile(`Add a source or destination block`),
			},
			{
				Config:      env.Config(rule(`protocol = "tcp"` + "\n" + `source {}`)),
				ExpectError: regexp.MustCompile(`Set cidrs, address_group_ids or vm_tags`),
			},
			{
				Config:      env.Config(rule(`protocol = "TCP"` + "\n" + `source { cidrs = ["192.0.2.0/24"] }`)),
				ExpectError: regexp.MustCompile(`value must be one of`),
			},
			{
				Config:      env.Config(rule(`protocol = "tcp"` + "\n" + `source { cidrs = ["192.0.2.0/33"] }`)),
				ExpectError: regexp.MustCompile(`not a valid IP address or CIDR`),
			},
			{
				Config:      env.Config(rule(`protocol = "tcp"` + "\n" + `source { vm_tags = ["a&b"] }`)),
				ExpectError: regexp.MustCompile(`must not contain`),
			},
			{
				Config:      env.Config(rule(`protocol = "tcp"` + "\n" + `source { address_group_ids = ["office"] }`)),
				ExpectError: regexp.MustCompile(`must be a UUID`),
			},
			{Config: env.Config("")},
		},
	})
}

// The protocol list covers every value of the API enum except UNSPECIFIED.
func TestFirewallProtocols(t *testing.T) {
	if len(firewall.Protocols) != 56 {
		t.Fatalf("got %d protocols, want 56", len(firewall.Protocols))
	}
	seen := map[string]bool{}
	for _, p := range firewall.Protocols {
		if seen[p] || p != strings.ToLower(p) {
			t.Fatalf("bad protocol %q", p)
		}
		seen[p] = true
	}
	for _, p := range []string{"tcp", "udp", "any", "icmp", "ipv6_icmp", "udplite", "ax_25", "mobility_header"} {
		if !seen[p] {
			t.Errorf("missing %s", p)
		}
	}
}

func ruleAttr(s *terraform.State, key string) string {
	return s.RootModule().Resources[rulesAddr].Primary.Attributes[key]
}

// --- Address groups ---

const ruleBlocks = `
  rule {
    direction         = "ingress"
    action            = "accept"
    protocol          = "tcp"
    destination_ports = "443"
    source { cidrs = ["0.0.0.0/0"] }
  }
  rule {
    direction         = "ingress"
    action            = "accept"
    protocol          = "tcp"
    destination_ports = "22"
    source { address_group_ids = [noderings_firewall_address_group.test.id] }
  }
  rule {
    direction = "ingress"
    action    = "drop"
    protocol  = "any"
    source { cidrs = ["0.0.0.0/0"] }
  }
  rule {
    direction = "ingress"
    action    = "drop"
    protocol  = "any"
    source { cidrs = ["::/0"] }
  }
`

// Reordered: the 0.0.0.0/0 drop moves to the end, the SSH rule changes port
// in place, the 443 rule is removed and a new egress rule is added on top.
const ruleBlocksChanged = `
  rule {
    direction        = "egress"
    action           = "accept"
    protocol         = "udp"
    source_ports     = "1024-65535"
    destination_ports = "53"
    destination { cidrs = ["192.0.2.53/32", "2001:db8::53"] }
  }
  rule {
    direction         = "ingress"
    action            = "accept"
    protocol          = "tcp"
    destination_ports = "2222"
    source { address_group_ids = [noderings_firewall_address_group.test.id] }
  }
  rule {
    direction = "ingress"
    action    = "drop"
    protocol  = "any"
    source { cidrs = ["::/0"] }
  }
  rule {
    direction = "ingress"
    action    = "drop"
    protocol  = "any"
    source { cidrs = ["0.0.0.0/0"] }
  }
`

const rulesAddr = "noderings_firewall_ruleset_rules.test"
