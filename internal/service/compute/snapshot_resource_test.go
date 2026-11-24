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

func snapshotConfig(name, description string) string {
	return vmConfig(name, `distro = "ubuntu_24_04"`) + fmt.Sprintf(`
resource "noderings_virtual_machine_snapshot" "test" {
  virtual_machine_id = noderings_virtual_machine.test.id
  description        = %q
}

data "noderings_virtual_machine_snapshots" "test" {
  virtual_machine_id = noderings_virtual_machine_snapshot.test.virtual_machine_id
}
`, description)
}

func TestVirtualMachineSnapshotResource(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	name := acctest.RandomName("tf-acc")
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(snapshotConfig(name, "before upgrade")),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(snapAddr, "operation_status", "completed"),
					resource.TestCheckResourceAttr(snapAddr, "status", "active"),
					resource.TestCheckResourceAttr(snapAddr, "description", "before upgrade"),
					resource.TestCheckResourceAttrSet(snapAddr, "name"),
					resource.TestCheckResourceAttrSet(snapAddr, "created_at"),
					resource.TestCheckResourceAttr("data.noderings_virtual_machine_snapshots.test", "snapshots.#", "1"),
					resource.TestCheckResourceAttrPair("data.noderings_virtual_machine_snapshots.test", "snapshots.0.id", snapAddr, "snapshot_id"),
				),
			},
			{
				ResourceName:      snapAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config: env.Config(snapshotConfig(name, "after upgrade")),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(snapAddr, plancheck.ResourceActionReplace)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(snapAddr, "description", "after upgrade"),
					resource.TestCheckResourceAttr("data.noderings_virtual_machine_snapshots.test", "snapshots.#", "1"),
				),
			},
			{
				Config: env.Config(snapshotConfig(name, "after upgrade")),
				Check: func(s *terraform.State) error {
					env.Fake.DeleteSnapshotOutOfBand(vmID(s), attr(s, snapAddr, "snapshot_id"))
					return nil
				},
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestVirtualMachineSnapshotFailures(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	name := acctest.RandomName("tf-acc")
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				PreConfig:   env.Fake.FailNextSnapshot,
				Config:      env.Config(snapshotConfig(name, "doomed")),
				ExpectError: regexp.MustCompile(`(?s)snapshot\s+.*\s+failed`),
			},
			{
				Config: env.Config(budgetVMConfig(name) + `
resource "noderings_virtual_machine_snapshot" "b" {
  virtual_machine_id = noderings_virtual_machine.budget.id
}
`),
				ExpectError: regexp.MustCompile(`does\s+not\s+support\s+snapshots`),
			},
			{
				Config:      env.Config(snapshotConfig(name, `it's`)),
				ExpectError: regexp.MustCompile(`must\s+not\s+contain`),
			},
			{Config: env.Config(budgetVMConfig(name))},
		},
	})
}

// --- noderings_firewall_ruleset_attachment ---

const snapAddr = "noderings_virtual_machine_snapshot.test"
