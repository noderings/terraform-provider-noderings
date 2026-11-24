package compute_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/noderings/terraform-provider-noderings/internal/acctest"
	"github.com/noderings/terraform-provider-noderings/internal/fakeapi"
)

func backupConfig(name, addon string) string {
	return vmConfig(name, `distro = "ubuntu_24_04"`) + fmt.Sprintf(`
data "noderings_marketplace_addon" "backup" {
  type               = "backup"
  virtual_machine_id = noderings_virtual_machine.test.id
}

resource "noderings_virtual_machine_backup" "test" {
  virtual_machine_id = noderings_virtual_machine.test.id
  addon_id           = %s
}

data "noderings_virtual_machine_backups" "test" {
  virtual_machine_id = noderings_virtual_machine_backup.test.virtual_machine_id
}
`, addon)
}

func TestVirtualMachineBackupResource(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	name := acctest.RandomName("tf-acc")
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(backupConfig(name, "data.noderings_marketplace_addon.backup.id")),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.noderings_marketplace_addon.backup", "id", fakeapi.BackupAddonID),
					resource.TestCheckResourceAttr("data.noderings_marketplace_addon.backup", "plan_code", fakeapi.VMPlanCode),
					resource.TestCheckResourceAttr(backupAddr, "addon_id", fakeapi.BackupAddonID),
					resource.TestCheckResourceAttr(backupAddr, "addon_status", "active"),
					resource.TestCheckResourceAttrSet(backupAddr, "addon_subscription_id"),
					resource.TestCheckResourceAttrPair(backupAddr, "id", vmAddr, "id"),
					resource.TestCheckResourceAttr("data.noderings_virtual_machine_backups.test", "backups.#", "1"),
					resource.TestCheckResourceAttr("data.noderings_virtual_machine_backups.test", "backups.0.operation_status", "completed"),
					resource.TestCheckResourceAttr("data.noderings_virtual_machine_backups.test", "backups.0.size_bytes", "1073741824"),
				),
			},
			{
				ResourceName:      backupAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				// Switching add-on is an in-place change.
				Config: env.Config(backupConfig(name, fmt.Sprintf("%q", fakeapi.LargeBackupAddonID))),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(backupAddr, plancheck.ResourceActionUpdate)},
				},
				Check: resource.TestCheckResourceAttr(backupAddr, "addon_id", fakeapi.LargeBackupAddonID),
			},
			{
				// Cancelled in the console: the next plan enables it again.
				Config: env.Config(backupConfig(name, fmt.Sprintf("%q", fakeapi.LargeBackupAddonID))),
				Check: func(s *terraform.State) error {
					env.Fake.DisableBackupOutOfBand(vmID(s))
					return nil
				},
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// An add-on that is already active is adopted without calling EnableBackup,
// which would start an extra backup run.
func TestVirtualMachineBackupAdoptsActiveAddon(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	name := acctest.RandomName("tf-acc")
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(vmConfig(name, `distro = "ubuntu_24_04"`)),
				Check: func(s *terraform.State) error {
					env.Fake.EnableBackupOutOfBand(vmID(s), fakeapi.BackupAddonID)
					return nil
				},
			},
			{
				Config: env.Config(backupConfig(name, fmt.Sprintf("%q", fakeapi.BackupAddonID))),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(backupAddr, "addon_status", "active"),
					func(*terraform.State) error {
						if n := env.Fake.EnableBackupCalls(); n != 0 {
							return fmt.Errorf("EnableBackup was called %d times", n)
						}
						return nil
					},
				),
			},
		},
	})
}

func TestVirtualMachineBackupUnsupported(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	name := acctest.RandomName("tf-acc")
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(budgetVMConfig(name) + fmt.Sprintf(`
resource "noderings_virtual_machine_backup" "test" {
  virtual_machine_id = noderings_virtual_machine.budget.id
  addon_id           = %q
}
`, fakeapi.BackupAddonID)),
				ExpectError: regexp.MustCompile(`does\s+not\s+support\s+backups`),
			},
			{
				Config: env.Config(budgetVMConfig(name) + `
data "noderings_marketplace_addon" "b" {
  type               = "backup"
  virtual_machine_id = noderings_virtual_machine.budget.id
}
`),
				ExpectError: regexp.MustCompile(`No\s+backup\s+add-on\s+is\s+available`),
			},
			{
				Config: env.Config(`
resource "noderings_virtual_machine_backup" "x" {
  virtual_machine_id = "nope"
  addon_id           = "nope"
}
`),
				ExpectError: regexp.MustCompile(`must\s+be\s+a\s+UUID`),
			},
			{Config: env.Config(budgetVMConfig(name))},
		},
	})
}

// --- noderings_virtual_machine_additional_ip ---

const backupAddr = "noderings_virtual_machine_backup.test"
