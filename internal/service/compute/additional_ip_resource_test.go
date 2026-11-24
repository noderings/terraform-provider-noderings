package compute_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/noderings/terraform-provider-noderings/internal/acctest"
)

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
