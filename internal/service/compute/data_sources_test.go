package compute_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/noderings/terraform-provider-noderings/internal/acctest"
)

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
