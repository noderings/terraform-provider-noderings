package compute_test

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/noderings/terraform-provider-noderings/internal/acctest"
	"github.com/noderings/terraform-provider-noderings/internal/fakeapi"
	"github.com/noderings/terraform-provider-noderings/internal/service/compute"
)

// Live runs order real, billed VMs, so they only run when an offer to use is
// given: NODERINGS_ACC_VM_PLAN_CODE, NODERINGS_ACC_VM_COUNTRY and
// NODERINGS_ACC_VM_CITY (a usage-billed plan with the guest_firewall,
// on_demand_backup and snapshots capabilities).
var liveOffer = struct{ code, country, city string }{fakeapi.VMPlanCode, "Germany", "Frankfurt"}

func TestMain(m *testing.M) {
	if !acctest.Live() {
		compute.PollInterval = 5 * time.Millisecond
		compute.MaxPollInterval = 20 * time.Millisecond
	} else {
		liveOffer.code = os.Getenv("NODERINGS_ACC_VM_PLAN_CODE")
		liveOffer.country = os.Getenv("NODERINGS_ACC_VM_COUNTRY")
		liveOffer.city = os.Getenv("NODERINGS_ACC_VM_CITY")
		if liveOffer.code == "" || liveOffer.country == "" || liveOffer.city == "" {
			fmt.Println("skipping compute tests: set NODERINGS_ACC_VM_PLAN_CODE, NODERINGS_ACC_VM_COUNTRY and NODERINGS_ACC_VM_CITY to run them against the live API")
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

const vmAddr = "noderings_virtual_machine.test"

// offerConfig resolves a usage-billed Proxmox offer in Frankfurt.
func offerConfig() string {
	return fmt.Sprintf(`
data "noderings_marketplace_offer" "test" {
  plan_code            = %q
  country              = %q
  city                 = %q
  billing_type         = "usage"
  require_capabilities = ["guest_firewall", "on_demand_backup", "snapshots"]
}
`, liveOffer.code, liveOffer.country, liveOffer.city)
}

// vmConfig is a VM on the offer above. body holds the remaining arguments.
func vmConfig(hostname, body string) string {
	return offerConfig() + fmt.Sprintf(`
resource "noderings_virtual_machine" "test" {
  hostname  = %q
  plan_id   = data.noderings_marketplace_offer.test.plan_id
  region_id = data.noderings_marketplace_offer.test.region_id
  password  = "correct-horse-battery"
  %s
}
`, hostname, body)
}

func vmID(s *terraform.State) string { return s.RootModule().Resources[vmAddr].Primary.ID }

func TestVirtualMachineResource(t *testing.T) {
	env := acctest.New(t)
	name := acctest.RandomName("tf-acc")
	var firstID string
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(vmConfig(name, `
  distro = "ubuntu_24_04"
  tags   = ["web", "prod"]`)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair(vmAddr, "plan_id", "data.noderings_marketplace_offer.test", "plan_id"),
					resource.TestCheckResourceAttrPair(vmAddr, "region_id", "data.noderings_marketplace_offer.test", "region_id"),
					resource.TestCheckResourceAttr(vmAddr, "provisioning_state", "applied"),
					resource.TestCheckResourceAttr(vmAddr, "power_state", "running"),
					resource.TestCheckResourceAttr(vmAddr, "distro", "ubuntu_24_04"),
					resource.TestCheckResourceAttr(vmAddr, "tags.#", "2"),
					resource.TestCheckTypeSetElemAttr(vmAddr, "tags.*", "prod"),
					resource.TestCheckResourceAttr(vmAddr, "plan_code", fakeapi.VMPlanCode),
					resource.TestCheckResourceAttr(vmAddr, "plan_billing_type", "usage"),
					resource.TestCheckResourceAttr(vmAddr, "platform_driver", "proxmox"),
					resource.TestCheckTypeSetElemAttr(vmAddr, "capabilities.*", "guest_firewall"),
					resource.TestCheckResourceAttr(vmAddr, "reinstall_on_change", "false"),
					resource.TestCheckResourceAttr(vmAddr, "deletion_protection", "false"),
					resource.TestCheckResourceAttrSet(vmAddr, "ipv4"),
					resource.TestCheckResourceAttrSet(vmAddr, "created_at"),
					resource.TestCheckResourceAttr(vmAddr, "has_active_backup", "false"),
					func(s *terraform.State) error { firstID = vmID(s); return nil },
				),
			},
			{
				// Tags change in place.
				Config: env.Config(vmConfig(name, `
  distro = "ubuntu_24_04"
  tags   = ["web", "staging"]`)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(vmAddr, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckTypeSetElemAttr(vmAddr, "tags.*", "staging"),
					resource.TestCheckResourceAttr(vmAddr, "tags.#", "2"),
				),
			},
			{
				// Power state changes in place.
				Config: env.Config(vmConfig(name, `
  distro      = "ubuntu_24_04"
  tags        = ["web", "staging"]
  power_state = "stopped"`)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(vmAddr, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(vmAddr, "power_state", "stopped"),
					func(s *terraform.State) error {
						if vmID(s) != firstID {
							return fmt.Errorf("VM was replaced")
						}
						return nil
					},
				),
			},
			{
				ResourceName:            vmAddr,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"password", "ssh_key_ids", "instant_app_variables", "coupon_code"},
			},
			{
				// The hostname can only change by replacing (or reinstalling) the VM.
				Config: env.Config(vmConfig(name+"-b", `
  distro      = "ubuntu_24_04"
  tags        = ["web", "staging"]
  power_state = "stopped"`)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(vmAddr, plancheck.ResourceActionReplace)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(vmAddr, "hostname", name+"-b"),
					resource.TestCheckResourceAttr(vmAddr, "power_state", "stopped"),
				),
			},
		},
	})
}

// Arguments the API never returns are null after import. Writing them in
// configuration must update the state in place, not replace the VM.
func TestVirtualMachineImportDoesNotReplace(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	name := acctest.RandomName("tf-acc")
	id := env.Fake.CreateVMOutOfBand(name, fakeapi.VMPlanUsageFRAID, "DISTRO_DEBIAN_13")
	cfg := env.Config(vmConfig(name, `distro = "debian_13"`))
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				ResourceName:       vmAddr,
				ImportState:        true,
				ImportStateId:      id,
				ImportStatePersist: true,
				Config:             cfg,
			},
			{
				Config: cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(vmAddr, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(vmAddr, "id", id),
					resource.TestCheckResourceAttr(vmAddr, "password", "correct-horse-battery"),
				),
			},
			{Config: cfg, PlanOnly: true},
			{
				// A real change afterwards still replaces the VM.
				Config: env.Config(strings.Replace(vmConfig(name, `distro = "debian_13"`), "correct-horse-battery", "another-secret-value", 1)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(vmAddr, plancheck.ResourceActionReplace)},
				},
			},
		},
	})
}

func TestVirtualMachineReinstallOnChange(t *testing.T) {
	env := acctest.New(t)
	name := acctest.RandomName("tf-acc")
	var firstID string
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(vmConfig(name, `
  distro              = "ubuntu_24_04"
  reinstall_on_change = true`)),
				Check: func(s *terraform.State) error { firstID = vmID(s); return nil },
			},
			{
				Config: env.Config(vmConfig(name+"-new", `
  distro              = "debian_13"
  reinstall_on_change = true`)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(vmAddr, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(vmAddr, "hostname", name+"-new"),
					resource.TestCheckResourceAttr(vmAddr, "distro", "debian_13"),
					resource.TestCheckResourceAttr(vmAddr, "reinstall_status", "completed"),
					resource.TestCheckResourceAttr(vmAddr, "power_state", "running"),
					resource.TestCheckResourceAttrSet(vmAddr, "reinstalled_at"),
					func(s *terraform.State) error {
						if vmID(s) != firstID {
							return fmt.Errorf("VM was replaced instead of reinstalled")
						}
						return nil
					},
				),
			},
		},
	})
}

func TestVirtualMachineSSHKeyAndInstantApp(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	name := acctest.RandomName("tf-acc")
	cfg := env.Config(offerConfig() + fmt.Sprintf(`
resource "noderings_ssh_key" "test" {
  name       = %[1]q
  public_key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBanaaPWXU3rsndX+KbHbpnJKjJG3Wrkv5FmwNMDQX+q tf-acc"
}

data "noderings_instant_app" "wp" {
  slug = "wordpress"
}

resource "noderings_virtual_machine" "test" {
  hostname       = %[1]q
  plan_id        = data.noderings_marketplace_offer.test.plan_id
  region_id      = data.noderings_marketplace_offer.test.region_id
  instant_app_id = data.noderings_instant_app.wp.id
  ssh_key_ids    = [noderings_ssh_key.test.id]

  instant_app_variables = {
    site_title = "My blog"
  }

  timeouts {
    create = "10m"
  }
}
`, name))
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(vmAddr, "distro", "ubuntu_24_04"),
					resource.TestCheckResourceAttr(vmAddr, "instant_app_slug", "wordpress"),
					resource.TestCheckResourceAttr(vmAddr, "instant_app_id", fakeapi.WordPressAppID),
					resource.TestCheckResourceAttr(vmAddr, "ssh_key_ids.#", "1"),
					resource.TestCheckResourceAttr(vmAddr, "timeouts.create", "10m"),
				),
			},
			{Config: cfg, PlanOnly: true},
		},
	})
}

// Free plans ignore the idempotency key. After a lost create response the
// provider finds the VM by its nonce tag instead of ordering a second one,
// and removes the tag afterwards.
func TestVirtualMachineFreePlanLostResponse(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	name := acctest.RandomName("tf-acc")
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{{
			PreConfig: func() { env.Fake.LoseNextResponse("CreateVirtualMachine") },
			Config: env.Config(fmt.Sprintf(`
resource "noderings_virtual_machine" "test" {
  hostname  = %q
  plan_id   = %q
  region_id = %q
  distro    = "ubuntu_24_04"
  password  = "correct-horse-battery"
  tags      = ["free"]
}
`, name, fakeapi.VMFreePlanFRAID, fakeapi.RegionFrankfurtID)),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(vmAddr, "plan_billing_type", "free"),
				resource.TestCheckResourceAttr(vmAddr, "tags.#", "1"),
				func(s *terraform.State) error {
					if n := env.Fake.VMCount(); n != 1 {
						return fmt.Errorf("want 1 VM, the fake has %d", n)
					}
					if tags := env.Fake.VMTags(vmID(s)); len(tags) != 1 || tags[0] != "free" {
						return fmt.Errorf("nonce tag not removed: %v", tags)
					}
					return nil
				},
			),
		}},
	})
}

// Paid plans honour the idempotency key, so a retry returns the same VM.
func TestVirtualMachinePaidPlanLostResponse(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	name := acctest.RandomName("tf-acc")
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{{
			PreConfig: func() {
				env.Fake.LoseNextResponse("CreateVirtualMachine")
				env.Fake.FailNext("POST /v1/virtual-machines", 1)
			},
			Config: env.Config(vmConfig(name, `distro = "ubuntu_24_04"`)),
			Check: func(s *terraform.State) error {
				if n := env.Fake.VMCount(); n != 1 {
					return fmt.Errorf("want 1 VM, the fake has %d", n)
				}
				if tags := env.Fake.VMTags(vmID(s)); len(tags) != 0 {
					return fmt.Errorf("paid plans need no nonce tag, got %v", tags)
				}
				return nil
			},
		}},
	})
}

func TestVirtualMachineProvisioningFailure(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	name := acctest.RandomName("tf-acc")
	cfg := env.Config(vmConfig(name, `distro = "ubuntu_24_04"`))
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				PreConfig:   env.Fake.FailNextVMProvisioning,
				Config:      cfg,
				ExpectError: regexp.MustCompile(`(?s)provisioning\s+of\s+virtual\s+machine\s+.*\s+failed`),
			},
			{
				// The failed VM is tainted, so the next apply replaces it.
				Config: cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(vmAddr, plancheck.ResourceActionReplace)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(vmAddr, "provisioning_state", "applied"),
					func(*terraform.State) error {
						if n := env.Fake.VMCount(); n != 1 {
							return fmt.Errorf("want 1 VM, the fake has %d", n)
						}
						return nil
					},
				),
			},
		},
	})
}

// The API refuses changes while an operation runs; the provider retries.
func TestVirtualMachineRetriesWhileBusy(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	name := acctest.RandomName("tf-acc")
	env.Run(t, resource.TestCase{
		CheckDestroy: func(*terraform.State) error {
			if n := env.Fake.VMCount(); n != 0 {
				return fmt.Errorf("%d VMs left", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: env.Config(vmConfig(name, `distro = "ubuntu_24_04"`)),
				Check:  func(s *terraform.State) error { env.Fake.SetVMBusy(vmID(s), 3); return nil },
			},
			{
				Config: env.Config(vmConfig(name, `
  distro      = "ubuntu_24_04"
  power_state = "stopped"`)),
				Check: func(s *terraform.State) error {
					if op := env.Fake.VMOperationStatus(vmID(s)); op != "OPERATION_STATUS_STOPPED" {
						return fmt.Errorf("operation status %s", op)
					}
					env.Fake.SetVMBusy(vmID(s), 3)
					return nil
				},
			},
		},
	})
}

func TestVirtualMachineDisappears(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	name := acctest.RandomName("tf-acc")
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.Config(vmConfig(name, `distro = "ubuntu_24_04"`)),
			Check: func(s *terraform.State) error {
				env.Fake.DeleteVMOutOfBand(vmID(s))
				return nil
			},
			ExpectNonEmptyPlan: true,
		}},
	})
}

func TestVirtualMachineDrift(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	name := acctest.RandomName("tf-acc")
	cfg := env.Config(vmConfig(name, `
  distro      = "ubuntu_24_04"
  tags        = ["web"]
  power_state = "running"`))
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: func(s *terraform.State) error {
					env.Fake.SetVMPowerOutOfBand(vmID(s), "OPERATION_STATUS_STOPPED")
					env.Fake.SetVMTagsOutOfBand(vmID(s), "web", "manual")
					return nil
				},
				ExpectNonEmptyPlan: true,
			},
			{
				Config: cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(vmAddr, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(vmAddr, "power_state", "running"),
					resource.TestCheckResourceAttr(vmAddr, "tags.#", "1"),
					func(s *terraform.State) error {
						if op := env.Fake.VMOperationStatus(vmID(s)); op != "OPERATION_STATUS_STARTED" {
							return fmt.Errorf("operation status %s", op)
						}
						return nil
					},
				),
			},
		},
	})
}

func TestVirtualMachineDeletionProtection(t *testing.T) {
	env := acctest.New(t)
	name := acctest.RandomName("tf-acc")
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{Config: env.Config(vmConfig(name, "distro = \"ubuntu_24_04\"\n  deletion_protection = true"))},
			{Config: env.Config(offerConfig()), ExpectError: regexp.MustCompile(`Deletion\s+protection\s+is\s+on`)},
			{Config: env.Config(vmConfig(name, "distro = \"ubuntu_24_04\"\n  deletion_protection = false"))},
		},
	})
}

func TestVirtualMachineCreateErrors(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	name := acctest.RandomName("tf-acc")
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config:      env.Config(vmConfig(name, `distro = "centos_10"`)),
				ExpectError: regexp.MustCompile(`(?s)No\s+VM\s+template\s+matches.*Pick\s+another\s+distro`),
			},
			{
				Config: env.Config(fmt.Sprintf(`
resource "noderings_virtual_machine" "test" {
  hostname  = %q
  plan_id   = "7c3a0000-0000-4000-8000-000000000999"
  region_id = %q
  distro    = "ubuntu_24_04"
  password  = "correct-horse-battery"
}
`, name, fakeapi.RegionFrankfurtID)),
				ExpectError: regexp.MustCompile(`no\s+longer\s+on\s+the\s+marketplace`),
			},
			{Config: env.Config(offerConfig())},
		},
	})
}

func TestVirtualMachineValidation(t *testing.T) {
	env := acctest.New(t)
	name := acctest.RandomName("tf-acc")
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config:      env.Config(vmConfig(name, "distro = \"ubuntu_24_04\"\n  ssh_key_ids = [\"5a1e0000-0000-4000-8000-000000000001\"]")),
				ExpectError: regexp.MustCompile(`ssh_key_ids`),
			},
			{Config: env.Config(vmConfig(name, `distro = "windows_11"`)), ExpectError: regexp.MustCompile(`value\s+must\s+be\s+one\s+of`)},
			{Config: env.Config(vmConfig(name, "")), ExpectError: regexp.MustCompile(`instant_app_id`)},
			{Config: env.Config(vmConfig("web & db", `distro = "ubuntu_24_04"`)), ExpectError: regexp.MustCompile(`must\s+not\s+contain`)},
			{Config: env.Config(vmConfig(name, "distro = \"ubuntu_24_04\"\n  tags = [\"tf-create-123\"]")), ExpectError: regexp.MustCompile(`Reserved\s+tag`)},
			{Config: env.Config(vmConfig(name, "distro = \"ubuntu_24_04\"\n  power_state = \"paused\"")), ExpectError: regexp.MustCompile(`value\s+must\s+be\s+one\s+of`)},
			{
				Config:      env.Config(vmConfig(name, "distro = \"ubuntu_24_04\"\n  instant_app_variables = { a = \"b\" }")),
				ExpectError: regexp.MustCompile(`instant_app_id`),
			},
			{
				Config:      env.Config(vmConfig(name, "distro = \"ubuntu_24_04\"\n  timeouts {\n    create = \"soon\"\n  }")),
				ExpectError: regexp.MustCompile(`Invalid\s+duration`),
			},
			{Config: env.Config(offerConfig())},
		},
	})
}
