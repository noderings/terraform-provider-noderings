package iam_test

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

func roleConfig(name, description string, methods ...string) string {
	desc := ""
	if description != "" {
		desc = fmt.Sprintf("description = %q", description)
	}
	ids := ""
	for _, m := range methods {
		ids += fmt.Sprintf("data.noderings_permission.%s.id, ", m)
	}
	return `
data "noderings_permission" "create" {
  name = "DnsZoneService::CreateDnsZone"
}
data "noderings_permission" "get" {
  service = "DnsZoneService"
  method  = "GetDnsZone"
}
data "noderings_permission" "list" {
  name = "DnsZoneService::ListDnsZones"
}
` + fmt.Sprintf(`
resource "noderings_role" "test" {
  name           = %q
  %s
  permission_ids = [%s]
}
`, name, desc, ids)
}

func TestRoleResource(t *testing.T) {
	env := acctest.New(t)
	name := acctest.RandomName("tf-acc")
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(roleConfig(name, "DNS readers", "create", "get")),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("noderings_role.test", "name", name),
					resource.TestCheckResourceAttr("noderings_role.test", "description", "DNS readers"),
					resource.TestCheckResourceAttr("noderings_role.test", "builtin", "false"),
					resource.TestCheckResourceAttr("noderings_role.test", "permission_ids.#", "2"),
					resource.TestCheckTypeSetElemAttrPair("noderings_role.test", "permission_ids.*", "data.noderings_permission.get", "id"),
				),
			},
			{ResourceName: "noderings_role.test", ImportState: true, ImportStateVerify: true},
			{
				// Import by name works too.
				ResourceName: "noderings_role.test", ImportState: true, ImportStateVerify: true, ImportStateId: name,
			},
			{
				ResourceName: "noderings_role.test", ImportState: true, ImportStateId: "Viewer",
				ExpectError: regexp.MustCompile(`built-in role`),
			},
			{
				// Rename, keep the description, and swap one permission: all in place.
				Config:           env.Config(roleConfig(name+"-b", "", "get", "list")),
				ConfigPlanChecks: expectAction("noderings_role.test", plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("noderings_role.test", "name", name+"-b"),
					resource.TestCheckResourceAttr("noderings_role.test", "description", "DNS readers"),
					resource.TestCheckResourceAttr("noderings_role.test", "permission_ids.#", "2"),
					resource.TestCheckTypeSetElemAttrPair("noderings_role.test", "permission_ids.*", "data.noderings_permission.list", "id"),
				),
			},
			{
				Config:           env.Config(roleConfig(name+"-b", "Changed", "get")),
				ConfigPlanChecks: expectAction("noderings_role.test", plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("noderings_role.test", "description", "Changed"),
					resource.TestCheckResourceAttr("noderings_role.test", "permission_ids.#", "1"),
				),
			},
		},
	})
}

func TestRoleResourceDrift(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	name := acctest.RandomName("tf-acc")
	cfg := env.Config(roleConfig(name, "", "get"))
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: func(s *terraform.State) error {
					env.Fake.AddRolePermissionOutOfBand(resourceID(s, "noderings_role.test"), env.Fake.PermissionID("SSHKeyService", "CreateSSHKey"))
					return nil
				},
				ExpectNonEmptyPlan: true,
			},
			{
				// Applying again removes the permission added outside Terraform.
				Config: cfg,
				Check:  resource.TestCheckResourceAttr("noderings_role.test", "permission_ids.#", "1"),
			},
			{
				Config: cfg,
				Check: func(s *terraform.State) error {
					env.Fake.DeleteRoleOutOfBand(resourceID(s, "noderings_role.test"))
					return nil
				},
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestRoleResourceDeleteWhileAssigned(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	var roleID string
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(roleConfig(acctest.RandomName("tf-acc"), "", "get")),
				Check: func(s *terraform.State) error {
					roleID = resourceID(s, "noderings_role.test")
					env.Fake.SetRoleOutOfBand("user", fakeapi.UserBobID, roleID)
					return nil
				},
			},
			{Config: env.Config(""), ExpectError: regexp.MustCompile(`(?s)still assigned to users.*terraform destroy again`)},
			{
				PreConfig: func() { env.Fake.SetRoleOutOfBand("user", fakeapi.UserBobID, "") },
				Config:    env.Config(""),
			},
		},
	})
}

func TestRoleResourceRejectsBadInput(t *testing.T) {
	env := acctest.New(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{Config: env.Config(roleConfig("viewer", "", "get")), ExpectError: regexp.MustCompile(`built-in role`)},
			{Config: env.Config(roleConfig("ab", "", "get")), ExpectError: regexp.MustCompile(`length must be between 3 and 255`)},
			{Config: env.Config(roleConfig("a<b>c", "", "get")), ExpectError: regexp.MustCompile(`must not contain`)},
			{Config: env.Config(`
resource "noderings_role" "test" {
  name           = "bad-permission"
  permission_ids = ["DnsZoneService::CreateDnsZone"]
}
`), ExpectError: regexp.MustCompile(`must be a\s+UUID`)},
		},
	})
}

func TestRoleResourceDuplicateName(t *testing.T) {
	env := acctest.New(t)
	name := acctest.RandomName("tf-acc")
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.Config(roleConfig(name, "", "get") + fmt.Sprintf(`
resource "noderings_role" "dup" {
  name           = %q
  permission_ids = []
  depends_on     = [noderings_role.test]
}
`, name)),
			ExpectError: regexp.MustCompile(`(?s)already exists.*terraform import`),
		}},
	})
}

// --- IAM data sources ---
