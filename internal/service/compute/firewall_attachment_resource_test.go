package compute_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/noderings/terraform-provider-noderings/internal/acctest"
)

func attachmentConfig(name, ruleset string) string {
	return vmConfig(name, "distro = \"ubuntu_24_04\"\n  power_state = \"stopped\"") + fmt.Sprintf(`
resource "noderings_firewall_ruleset_attachment" "test" {
  virtual_machine_id = noderings_virtual_machine.test.id
  ruleset_id         = %q
}
`, ruleset)
}

func TestFirewallRulesetAttachmentResource(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	name := acctest.RandomName("tf-acc")
	stillStopped := func(s *terraform.State) error {
		if op := env.Fake.VMOperationStatus(vmID(s)); op != "OPERATION_STATUS_STOPPED" {
			return fmt.Errorf("the VM should stay stopped, operation status is %s", op)
		}
		return nil
	}
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(attachmentConfig(name, rulesetA)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(attachAddr, "ruleset_id", rulesetA),
					resource.TestCheckResourceAttrPair(attachAddr, "id", vmAddr, "id"),
					stillStopped,
				),
			},
			{
				// The VM's power_state does not drift because of the attachment.
				Config:   env.Config(attachmentConfig(name, rulesetA)),
				PlanOnly: true,
			},
			{
				ResourceName:      attachAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config: env.Config(attachmentConfig(name, rulesetB)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(attachAddr, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(attachAddr, "ruleset_id", rulesetB),
					stillStopped,
				),
			},
			{
				Config: env.Config(attachmentConfig(name, rulesetB)),
				Check: func(s *terraform.State) error {
					env.Fake.SetFirewallOutOfBand(vmID(s), "")
					return nil
				},
				ExpectNonEmptyPlan: true,
			},
			{
				// Detached on destroy of the attachment only.
				Config: env.Config(vmConfig(name, "distro = \"ubuntu_24_04\"\n  power_state = \"stopped\"")),
				Check:  resource.TestCheckResourceAttr(vmAddr, "firewall_ruleset_id", ""),
			},
		},
	})
}

func TestFirewallRulesetAttachmentUnsupported(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	name := acctest.RandomName("tf-acc")
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(budgetVMConfig(name) + fmt.Sprintf(`
resource "noderings_firewall_ruleset_attachment" "test" {
  virtual_machine_id = noderings_virtual_machine.budget.id
  ruleset_id         = %q
}
`, rulesetA)),
				ExpectError: regexp.MustCompile(`does\s+not\s+support\s+the\s+firewall`),
			},
			{Config: env.Config(budgetVMConfig(name))},
		},
	})
}

// --- data sources ---

const (
	attachAddr = "noderings_firewall_ruleset_attachment.test"
	rulesetA   = "11111111-2222-4333-8444-555555555555"
	rulesetB   = "66666666-7777-4888-9999-000000000000"
)
