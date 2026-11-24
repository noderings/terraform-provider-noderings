package iam_test

import (
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// testPublicKey is a throwaway key generated for these tests only.
const testPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBanaaPWXU3rsndX+KbHbpnJKjJG3Wrkv5FmwNMDQX+q tf-acc"

func resourceID(s *terraform.State, addr string) string {
	return s.RootModule().Resources[addr].Primary.ID
}

func expectAction(addr string, a plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, a)}}
}

// --- noderings_role ---
