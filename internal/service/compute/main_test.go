package compute_test

import (
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/noderings/terraform-provider-noderings/internal/acctest"
	"github.com/noderings/terraform-provider-noderings/internal/fakeapi"
	"github.com/noderings/terraform-provider-noderings/internal/service/compute"
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

// Live runs order real, billed VMs, so they only run when an offer to use is
// given: NODERINGS_ACC_VM_PLAN_CODE, NODERINGS_ACC_VM_COUNTRY and
// NODERINGS_ACC_VM_CITY (a usage-billed plan with the guest_firewall,
// on_demand_backup and snapshots capabilities).
var liveOffer = struct{ code, country, city string }{fakeapi.VMPlanCode, "Germany", "Frankfurt"}
