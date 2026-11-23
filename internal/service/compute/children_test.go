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

func attr(s *terraform.State, addr, name string) string {
	return s.RootModule().Resources[addr].Primary.Attributes[name]
}

// budgetVMConfig is a VM on the VirtFusion provider, which supports no
// firewall, backups or snapshots.
func budgetVMConfig(name string) string {
	return fmt.Sprintf(`
resource "noderings_virtual_machine" "budget" {
  hostname  = %q
  plan_id   = %q
  region_id = %q
  distro    = "debian_13"
  password  = "correct-horse-battery"
}
`, name, fakeapi.BudgetVMPlanUsageFRAID, fakeapi.BudgetRegionFRAID)
}

// --- noderings_virtual_machine_backup ---

const backupAddr = "noderings_virtual_machine_backup.test"

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

func ipConfig(name string, count int) string {
	return vmConfig(name, `distro = "ubuntu_24_04"`) + fmt.Sprintf(`
resource "noderings_virtual_machine_additional_ip" "test" {
  count              = %d
  virtual_machine_id = noderings_virtual_machine.test.id
}
`, count)
}

func TestVirtualMachineAdditionalIPResource(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	name := acctest.RandomName("tf-acc")
	const ip0 = "noderings_virtual_machine_additional_ip.test.0"
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(ipConfig(name, 2)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr(ip0, "ip_address", regexp.MustCompile(`^198\.51\.100\.\d+$`)),
					resource.TestCheckResourceAttr(ip0, "gateway", "198.51.100.1"),
					resource.TestCheckResourceAttr(ip0, "prefix_length", "24"),
					resource.TestCheckResourceAttr(ip0, "monthly_unit_price", "2.50"),
					resource.TestCheckResourceAttr(ip0, "prorated_charge_amount", "0.00"),
					resource.TestCheckResourceAttr(ip0, "currency", "EUR"),
					resource.TestMatchResourceAttr(ip0, "id", regexp.MustCompile(`^[0-9a-f-]{36}/198\.51\.100\.\d+$`)),
				),
			},
			{
				// The VM shows both addresses after a refresh.
				Config: env.Config(ipConfig(name, 2)),
				Check:  resource.TestCheckResourceAttr(vmAddr, "additional_ipv4.#", "2"),
			},
			{
				ResourceName: "noderings_virtual_machine_additional_ip.test[0]",
				ImportStateIdFunc: func(s *terraform.State) (string, error) {
					return attr(s, ip0, "id"), nil
				},
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"monthly_unit_price", "prorated_charge_amount", "currency"},
			},
			{
				Config: env.Config(ipConfig(name, 2)),
				Check: func(s *terraform.State) error {
					env.Fake.RemoveAdditionalIPOutOfBand(vmID(s), attr(s, ip0, "ip_address"))
					return nil
				},
				ExpectNonEmptyPlan: true,
			},
			{
				// Removing one address releases it.
				Config: env.Config(ipConfig(name, 1)),
				Check: func(s *terraform.State) error {
					if _, ok := s.RootModule().Resources["noderings_virtual_machine_additional_ip.test.1"]; ok {
						return fmt.Errorf("second address still in state")
					}
					return nil
				},
			},
		},
	})
}

// Every enable call adds and bills an address, so after a lost response the
// provider adopts the address that appeared instead of retrying.
func TestVirtualMachineAdditionalIPLostResponse(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	name := acctest.RandomName("tf-acc")
	const ip0 = "noderings_virtual_machine_additional_ip.test.0"
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{Config: env.Config(vmConfig(name, `distro = "ubuntu_24_04"`))},
			{
				PreConfig: func() { env.Fake.LoseNextResponse("EnableAdditionalIP") },
				Config:    env.Config(ipConfig(name, 1)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(ip0, "ip_address"),
					resource.TestCheckNoResourceAttr(ip0, "monthly_unit_price"),
				),
			},
			{
				Config: env.Config(ipConfig(name, 1)),
				Check:  resource.TestCheckResourceAttr(vmAddr, "additional_ipv4.#", "1"),
			},
		},
	})
}

func TestVirtualMachineAdditionalIPImportID(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	name := acctest.RandomName("tf-acc")
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{Config: env.Config(ipConfig(name, 1))},
			{
				ResourceName:  "noderings_virtual_machine_additional_ip.test[0]",
				ImportState:   true,
				ImportStateId: "not-an-id",
				ExpectError:   regexp.MustCompile(`<virtual_machine_id>/<ip_address>`),
			},
			{Config: env.Config(ipConfig(name, 1))},
		},
	})
}

// --- noderings_virtual_machine_snapshot ---

const snapAddr = "noderings_virtual_machine_snapshot.test"

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

const (
	attachAddr = "noderings_firewall_ruleset_attachment.test"
	rulesetA   = "11111111-2222-4333-8444-555555555555"
	rulesetB   = "66666666-7777-4888-9999-000000000000"
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

func TestVirtualMachineDataSources(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	name := acctest.RandomName("tf-acc")
	vm := vmConfig(name, "distro = \"ubuntu_24_04\"\n  tags = [\"web\", \"blue\"]")
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{Config: env.Config(vm)},
			{
				Config: env.Config(vm + fmt.Sprintf(`
data "noderings_virtual_machine" "by_id" {
  id = noderings_virtual_machine.test.id
}

data "noderings_virtual_machine" "by_name" {
  hostname = %q
}

data "noderings_virtual_machine" "by_tags" {
  tags = ["web", "blue"]
}

data "noderings_virtual_machines" "all" {}

data "noderings_virtual_machines" "filtered" {
  power_state    = "running"
  tags           = ["blue"]
  hostname_regex = "^tf-acc-"
}

data "noderings_virtual_machines" "none" {
  tags = ["green"]
}

data "noderings_virtual_machine_tags" "all" {
  depends_on = [noderings_virtual_machine.test]
}
`, name)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair("data.noderings_virtual_machine.by_id", "hostname", vmAddr, "hostname"),
					resource.TestCheckResourceAttrPair("data.noderings_virtual_machine.by_id", "plan_id", vmAddr, "plan_id"),
					resource.TestCheckResourceAttr("data.noderings_virtual_machine.by_id", "platform_driver", "proxmox"),
					resource.TestCheckResourceAttr("data.noderings_virtual_machine.by_id", "power_state", "running"),
					resource.TestCheckResourceAttrPair("data.noderings_virtual_machine.by_name", "id", vmAddr, "id"),
					resource.TestCheckResourceAttrPair("data.noderings_virtual_machine.by_tags", "id", vmAddr, "id"),
					resource.TestCheckResourceAttr("data.noderings_virtual_machines.all", "virtual_machines.#", "1"),
					resource.TestCheckResourceAttrPair("data.noderings_virtual_machines.all", "virtual_machines.0.id", vmAddr, "id"),
					resource.TestCheckResourceAttr("data.noderings_virtual_machines.all", "virtual_machines.0.region_city", "Frankfurt"),
					resource.TestCheckResourceAttr("data.noderings_virtual_machines.filtered", "virtual_machines.#", "1"),
					resource.TestCheckResourceAttr("data.noderings_virtual_machines.none", "virtual_machines.#", "0"),
					resource.TestCheckResourceAttr("data.noderings_virtual_machine_tags.all", "tags.#", "2"),
					resource.TestCheckResourceAttr("data.noderings_virtual_machine_tags.all", "tags.0", "blue"),
				),
			},
			{
				Config:      env.Config(vm + `data "noderings_virtual_machine" "x" { hostname = "no-such-vm" }`),
				ExpectError: regexp.MustCompile(`0\s+virtual\s+machines\s+match`),
			},
			{
				Config:      env.Config(vm + `data "noderings_virtual_machine" "x" {}`),
				ExpectError: regexp.MustCompile(`id`),
			},
			{Config: env.Config(vm)},
		},
	})
}
