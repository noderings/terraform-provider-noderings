package firewall_test

import (
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func id(s *terraform.State, addr string) string { return s.RootModule().Resources[addr].Primary.ID }
