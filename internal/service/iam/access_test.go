package iam_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/noderings/terraform-provider-noderings/internal/acctest"
	"github.com/noderings/terraform-provider-noderings/internal/fakeapi"
)

func resourceID(s *terraform.State, addr string) string {
	return s.RootModule().Resources[addr].Primary.ID
}

func expectAction(addr string, a plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, a)}}
}

// --- noderings_role ---

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

func TestPermissionDataSources(t *testing.T) {
	env := acctest.New(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.Config(`
data "noderings_permission" "by_name" {
  name = "DnsZoneService::CreateDnsZone"
}
data "noderings_permission" "by_parts" {
  service = "dnszoneservice"
  method  = "creatednszone"
}
data "noderings_permissions" "dns" {
  services = ["DnsZoneService", "DnsRecordService"]
}
data "noderings_permissions" "all" {}
`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet("data.noderings_permission.by_name", "id"),
				resource.TestCheckResourceAttr("data.noderings_permission.by_name", "service", "DnsZoneService"),
				resource.TestCheckResourceAttr("data.noderings_permission.by_name", "method", "CreateDnsZone"),
				resource.TestCheckResourceAttr("data.noderings_permission.by_parts", "name", "dnszoneservice::creatednszone"),
				resource.TestCheckResourceAttrPair("data.noderings_permission.by_name", "id", "data.noderings_permission.by_parts", "id"),
				resource.TestCheckTypeSetElemAttrPair("data.noderings_permissions.dns", "ids.*", "data.noderings_permission.by_name", "id"),
				resource.TestCheckTypeSetElemNestedAttrs("data.noderings_permissions.dns", "permissions.*", map[string]string{"service": "dnsrecordservice", "method": "UpsertDnsRecord"}),
				resource.TestCheckTypeSetElemAttrPair("data.noderings_permissions.all", "ids.*", "data.noderings_permission.by_name", "id"),
			),
		}},
	})
}

func TestPermissionDataSourceErrors(t *testing.T) {
	env := acctest.New(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{Config: env.Config(`data "noderings_permission" "x" { name = "DnsZoneService::NoSuchMethod" }`), ExpectError: regexp.MustCompile(`Permission not found`)},
			{Config: env.Config(`data "noderings_permission" "x" { name = "CreateDnsZone" }`), ExpectError: regexp.MustCompile(`must look like Service::Method`)},
			{Config: env.Config(`data "noderings_permission" "x" { service = "DnsZoneService" }`), ExpectError: regexp.MustCompile(`(?i)must be configured`)},
			{Config: env.Config(`data "noderings_permissions" "x" { services = ["NoSuchService"] }`), ExpectError: regexp.MustCompile(`No permissions match NoSuchService`)},
		},
	})
}

func TestRoleDataSources(t *testing.T) {
	env := acctest.New(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.Config(`
data "noderings_role" "viewer" {
  name = "viewer"
}
data "noderings_roles" "all" {}
`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.noderings_role.viewer", "name", "Viewer"),
				resource.TestCheckResourceAttr("data.noderings_role.viewer", "builtin", "true"),
				resource.TestCheckResourceAttrSet("data.noderings_role.viewer", "id"),
				resource.TestCheckTypeSetElemNestedAttrs("data.noderings_roles.all", "roles.*", map[string]string{"name": "Viewer", "builtin": "true"}),
				resource.TestCheckTypeSetElemNestedAttrs("data.noderings_roles.all", "roles.*", map[string]string{"name": "Owner", "builtin": "true"}),
			),
		}},
	})
}

func TestRoleDataSourceNotFound(t *testing.T) {
	env := acctest.New(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{{
			Config:      env.Config(`data "noderings_role" "x" { name = "no-such-role-tf-acc" }`),
			ExpectError: regexp.MustCompile(`No role named "no-such-role-tf-acc"`),
		}},
	})
}

func TestUsersDataSource(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.Config(fmt.Sprintf(`
data "noderings_users" "all" {}
data "noderings_users" "some" {
  emails = [%q, %q]
}
`, "BOB@example.com", fakeapi.UserAliceEmail)),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.noderings_users.all", "users.#", "3"),
				resource.TestCheckResourceAttr("data.noderings_users.all", "ids_by_email."+fakeapi.UserCarolEmail, fakeapi.UserCarolID),
				resource.TestCheckResourceAttr("data.noderings_users.some", "ids.#", "2"),
				resource.TestCheckResourceAttr("data.noderings_users.some", "ids.0", fakeapi.UserBobID),
				resource.TestCheckResourceAttr("data.noderings_users.some", "ids.1", fakeapi.UserAliceID),
				resource.TestCheckResourceAttr("data.noderings_users.some", "users.0.status", "active"),
			),
		}},
	})
}

func TestUsersDataSourceUnknownEmail(t *testing.T) {
	env := acctest.New(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{{
			Config:      env.Config(`data "noderings_users" "x" { emails = ["nobody-tf-acc@example.com"] }`),
			ExpectError: regexp.MustCompile(`No user of the organization has the email address nobody-tf-acc@example.com`),
		}},
	})
}

// --- noderings_group ---

func groupConfig(name, description string) string {
	desc := ""
	if description != "" {
		desc = fmt.Sprintf("description = %q", description)
	}
	return fmt.Sprintf(`
resource "noderings_group" "test" {
  name = %q
  %s
}
`, name, desc)
}

func TestGroupResource(t *testing.T) {
	env := acctest.New(t)
	name := acctest.RandomName("tf-acc")
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(groupConfig(name, "Operations")),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("noderings_group.test", "name", name),
					resource.TestCheckResourceAttr("noderings_group.test", "description", "Operations"),
					resource.TestCheckResourceAttrSet("noderings_group.test", "id"),
				),
			},
			{ResourceName: "noderings_group.test", ImportState: true, ImportStateVerify: true},
			{
				Config:           env.Config(groupConfig(name+"-b", "Ops team")),
				ConfigPlanChecks: expectAction("noderings_group.test", plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("noderings_group.test", "name", name+"-b"),
					resource.TestCheckResourceAttr("noderings_group.test", "description", "Ops team"),
				),
			},
			{
				// Removing the description keeps it: the API cannot clear it.
				Config:           env.Config(groupConfig(name+"-b", "")),
				ConfigPlanChecks: resource.ConfigPlanChecks{PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()}},
				Check:            resource.TestCheckResourceAttr("noderings_group.test", "description", "Ops team"),
			},
		},
	})
}

func TestGroupResourceDrift(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	name := acctest.RandomName("tf-acc")
	cfg := env.Config(groupConfig(name, ""))
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: func(s *terraform.State) error {
					env.Fake.RenameGroupOutOfBand(resourceID(s, "noderings_group.test"), "renamed")
					return nil
				},
				ExpectNonEmptyPlan: true,
			},
			{Config: cfg, Check: resource.TestCheckResourceAttr("noderings_group.test", "name", name)},
			{
				Config: cfg,
				Check: func(s *terraform.State) error {
					env.Fake.DeleteGroupOutOfBand(resourceID(s, "noderings_group.test"))
					return nil
				},
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestGroupResourceRejectsBadInput(t *testing.T) {
	env := acctest.New(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{Config: env.Config(groupConfig("ab", "")), ExpectError: regexp.MustCompile(`length must be between 3 and 255`)},
			{Config: env.Config(groupConfig("ops", `"quoted"`)), ExpectError: regexp.MustCompile(`must not contain`)},
		},
	})
}

// --- noderings_group_membership ---

func membershipConfig(name string, users, sas []string) string {
	list := func(ids []string) string {
		out := "["
		for i, id := range ids {
			if i > 0 {
				out += ", "
			}
			out += fmt.Sprintf("%q", id)
		}
		return out + "]"
	}
	body := ""
	if users != nil {
		body += "user_ids = " + list(users) + "\n"
	}
	if sas != nil {
		body += "service_account_ids = " + list(sas) + "\n"
	}
	return groupConfig(name, "") + fmt.Sprintf(`
resource "noderings_group_membership" "test" {
  group_id = noderings_group.test.id
  %s
}
`, body)
}

func TestGroupMembershipResource(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	name := acctest.RandomName("tf-acc")
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(membershipConfig(name, []string{fakeapi.UserAliceID, fakeapi.UserBobID}, []string{fakeapi.ServiceAccountCIID})),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair("noderings_group_membership.test", "id", "noderings_group.test", "id"),
					resource.TestCheckResourceAttr("noderings_group_membership.test", "user_ids.#", "2"),
					resource.TestCheckTypeSetElemAttr("noderings_group_membership.test", "user_ids.*", fakeapi.UserBobID),
					resource.TestCheckResourceAttr("noderings_group_membership.test", "service_account_ids.#", "1"),
				),
			},
			{ResourceName: "noderings_group_membership.test", ImportState: true, ImportStateVerify: true},
			{
				Config:           env.Config(membershipConfig(name, []string{fakeapi.UserBobID}, []string{})),
				ConfigPlanChecks: expectAction("noderings_group_membership.test", plancheck.ResourceActionUpdate),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("noderings_group_membership.test", "user_ids.#", "1"),
					resource.TestCheckResourceAttr("noderings_group_membership.test", "service_account_ids.#", "0"),
				),
			},
			{
				// A member added outside Terraform shows up as drift and is removed again.
				Config: env.Config(membershipConfig(name, []string{fakeapi.UserBobID}, []string{})),
				Check: func(s *terraform.State) error {
					env.Fake.AddGroupMemberOutOfBand(resourceID(s, "noderings_group.test"), fakeapi.UserCarolID, false)
					env.Fake.AddGroupMemberOutOfBand(resourceID(s, "noderings_group.test"), fakeapi.ServiceAccountDeployID, true)
					return nil
				},
				ExpectNonEmptyPlan: true,
			},
			{
				Config: env.Config(membershipConfig(name, []string{fakeapi.UserBobID}, []string{})),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("noderings_group_membership.test", "user_ids.#", "1"),
					resource.TestCheckResourceAttr("noderings_group_membership.test", "service_account_ids.#", "0"),
				),
			},
			{
				// Only users are managed now; the service account added outside Terraform stays.
				Config: env.Config(membershipConfig(name, []string{fakeapi.UserBobID}, nil)),
				Check: func(s *terraform.State) error {
					env.Fake.AddGroupMemberOutOfBand(resourceID(s, "noderings_group.test"), fakeapi.ServiceAccountDeployID, true)
					return nil
				},
			},
		},
	})
}

func TestGroupMembershipFromEmails(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.Config(groupConfig(acctest.RandomName("tf-acc"), "") + fmt.Sprintf(`
data "noderings_users" "ops" {
  emails = [%q, %q]
}
resource "noderings_group_membership" "test" {
  group_id = noderings_group.test.id
  user_ids = data.noderings_users.ops.ids
}
`, fakeapi.UserAliceEmail, fakeapi.UserCarolEmail)),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckTypeSetElemAttr("noderings_group_membership.test", "user_ids.*", fakeapi.UserCarolID),
				resource.TestCheckNoResourceAttr("noderings_group_membership.test", "service_account_ids"),
			),
		}},
	})
}

func TestGroupMembershipRemovedWithGroup(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.Config(membershipConfig(acctest.RandomName("tf-acc"), []string{fakeapi.UserAliceID}, nil)),
			Check: func(s *terraform.State) error {
				env.Fake.DeleteGroupOutOfBand(resourceID(s, "noderings_group.test"))
				return nil
			},
			ExpectNonEmptyPlan: true,
		}},
	})
}

func TestGroupMembershipRejectsBadInput(t *testing.T) {
	env := acctest.New(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{Config: env.Config(membershipConfig("ops-tf-acc", nil, nil)), ExpectError: regexp.MustCompile(`(?i)at least one of these attributes`)},
			{Config: env.Config(membershipConfig("ops-tf-acc", []string{"alice@example.com"}, nil)), ExpectError: regexp.MustCompile(`must be a\s+UUID`)},
		},
	})
}

// --- noderings_role_assignment ---

func TestRoleAssignmentGroup(t *testing.T) {
	env := acctest.New(t)
	name := acctest.RandomName("tf-acc")
	cfg := func(role string) string {
		return groupConfig(name, "") + roleConfig(name, "", "get") + fmt.Sprintf(`
data "noderings_role" "viewer" {
  name = "Viewer"
}
resource "noderings_role_assignment" "test" {
  group_id = noderings_group.test.id
  role_id  = %s
}
`, role)
	}
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(cfg("noderings_role.test.id")),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair("noderings_role_assignment.test", "role_id", "noderings_role.test", "id"),
					resource.TestMatchResourceAttr("noderings_role_assignment.test", "id", regexp.MustCompile(`^group/`)),
				),
			},
			{ResourceName: "noderings_role_assignment.test", ImportState: true, ImportStateVerify: true},
			{
				Config:           env.Config(cfg("data.noderings_role.viewer.id")),
				ConfigPlanChecks: expectAction("noderings_role_assignment.test", plancheck.ResourceActionUpdate),
				Check:            resource.TestCheckResourceAttrPair("noderings_role_assignment.test", "role_id", "data.noderings_role.viewer", "id"),
			},
		},
	})
}

func assignmentConfig(attr, principal, role string) string {
	return fmt.Sprintf(`
resource "noderings_role_assignment" "test" {
  %s = %q
  role_id = %q
}
`, attr, principal, role)
}

func TestRoleAssignmentUser(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(assignmentConfig("user_id", fakeapi.UserBobID, fakeapi.RoleViewerID)),
				Check:  resource.TestCheckResourceAttr("noderings_role_assignment.test", "id", "user/"+fakeapi.UserBobID),
			},
			{ResourceName: "noderings_role_assignment.test", ImportState: true, ImportStateVerify: true},
			{
				// The role changed outside Terraform is detected and set back.
				Config: env.Config(assignmentConfig("user_id", fakeapi.UserBobID, fakeapi.RoleViewerID)),
				Check: func(*terraform.State) error {
					env.Fake.SetRoleOutOfBand("user", fakeapi.UserBobID, fakeapi.RoleOperatorID)
					return nil
				},
				ExpectNonEmptyPlan: true,
			},
			{
				Config: env.Config(assignmentConfig("user_id", fakeapi.UserBobID, fakeapi.RoleViewerID)),
				Check:  resource.TestCheckResourceAttr("noderings_role_assignment.test", "role_id", fakeapi.RoleViewerID),
			},
			{
				// A removed role makes Terraform recreate the assignment.
				Config: env.Config(assignmentConfig("user_id", fakeapi.UserBobID, fakeapi.RoleViewerID)),
				Check: func(*terraform.State) error {
					env.Fake.SetRoleOutOfBand("user", fakeapi.UserBobID, "")
					return nil
				},
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestRoleAssignmentServiceAccount(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(assignmentConfig("service_account_id", fakeapi.ServiceAccountCIID, fakeapi.RoleOperatorID)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("noderings_role_assignment.test", "id", "service_account/"+fakeapi.ServiceAccountCIID),
					resource.TestCheckResourceAttr("noderings_role_assignment.test", "role_id", fakeapi.RoleOperatorID),
				),
			},
			{ResourceName: "noderings_role_assignment.test", ImportState: true, ImportStateVerify: true},
			{
				ResourceName: "noderings_role_assignment.test", ImportState: true, ImportStateId: "robot/" + fakeapi.ServiceAccountCIID,
				ExpectError: regexp.MustCompile(`Expected user/<id>, group/<id> or service_account/<id>`),
			},
			{
				// Switching to a user replaces the assignment.
				Config:           env.Config(assignmentConfig("user_id", fakeapi.UserCarolID, fakeapi.RoleOperatorID)),
				ConfigPlanChecks: expectAction("noderings_role_assignment.test", plancheck.ResourceActionReplace),
				Check:            resource.TestCheckResourceAttr("noderings_role_assignment.test", "id", "user/"+fakeapi.UserCarolID),
			},
		},
	})
}

func TestRoleAssignmentRejectsBadInput(t *testing.T) {
	env := acctest.New(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{Config: env.Config(`
resource "noderings_role_assignment" "test" {
  user_id  = "1a2b0000-0000-4000-8000-000000000001"
  group_id = "1a2b0000-0000-4000-8000-000000000002"
  role_id  = "3c4d0000-0000-4000-8000-000000000005"
}
`), ExpectError: regexp.MustCompile(`(?i)exactly one of these attributes`)},
			{Config: env.Config(`
resource "noderings_role_assignment" "test" {
  role_id = "3c4d0000-0000-4000-8000-000000000005"
}
`), ExpectError: regexp.MustCompile(`(?i)exactly one of`)},
		},
	})
}

// --- noderings_secret ---

func secretConfig(key, value string) string {
	return fmt.Sprintf(`
resource "noderings_secret" "test" {
  key   = %q
  value = %q
}
`, key, value)
}

func TestSecretResource(t *testing.T) {
	env := acctest.New(t)
	key := "tf_acc-" + acctest.RandomName("s")
	fakeValue := func(want string) resource.TestCheckFunc {
		return func(s *terraform.State) error {
			if env.Fake == nil {
				return nil
			}
			if got := env.Fake.SecretValue(resourceID(s, "noderings_secret.test")); got != want {
				return fmt.Errorf("stored value %q, want %q", got, want)
			}
			return nil
		}
	}
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(secretConfig(key, "first-value")),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("noderings_secret.test", "key", key),
					resource.TestCheckResourceAttr("noderings_secret.test", "value", "first-value"),
					resource.TestCheckResourceAttrSet("noderings_secret.test", "id"),
					fakeValue("first-value"),
				),
			},
			{
				ResourceName: "noderings_secret.test", ImportState: true, ImportStateVerify: true,
				// The API never returns the value.
				ImportStateVerifyIgnore: []string{"value"},
			},
			{
				Config:           env.Config(secretConfig(key, "second-value")),
				ConfigPlanChecks: expectAction("noderings_secret.test", plancheck.ResourceActionUpdate),
				Check:            resource.ComposeAggregateTestCheckFunc(resource.TestCheckResourceAttr("noderings_secret.test", "value", "second-value"), fakeValue("second-value")),
			},
			{
				Config:           env.Config(secretConfig(key+"-b", "second-value")),
				ConfigPlanChecks: expectAction("noderings_secret.test", plancheck.ResourceActionDestroyBeforeCreate),
				Check:            resource.TestCheckResourceAttr("noderings_secret.test", "key", key+"-b"),
			},
		},
	})
}

func TestSecretResourceRemovedOutOfBand(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.Config(secretConfig("tf_acc_gone", "v")),
			Check: func(s *terraform.State) error {
				env.Fake.DeleteSecretOutOfBand(resourceID(s, "noderings_secret.test"))
				return nil
			},
			ExpectNonEmptyPlan: true,
		}},
	})
}

func TestSecretResourceDuplicateKey(t *testing.T) {
	env := acctest.New(t)
	key := "tf_acc_" + acctest.RandomName("dup")
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.Config(secretConfig(key, "v") + fmt.Sprintf(`
resource "noderings_secret" "dup" {
  key        = %q
  value      = "v"
  depends_on = [noderings_secret.test]
}
`, key)),
			ExpectError: regexp.MustCompile(`(?s)already exists.*terraform import`),
		}},
	})
}

func TestSecretResourceRejectsBadInput(t *testing.T) {
	env := acctest.New(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{Config: env.Config(secretConfig("Bad Key", "v")), ExpectError: regexp.MustCompile(`lower-case letters, digits, _ and -`)},
			{Config: env.Config(secretConfig("ok", "")), ExpectError: regexp.MustCompile(`length must be between 1 and 255`)},
		},
	})
}

// --- noderings_environment_variable ---

func envVarConfig(key, value string) string {
	return fmt.Sprintf(`
resource "noderings_environment_variable" "test" {
  key   = %q
  value = %q
}
`, key, value)
}

func TestEnvironmentVariableResource(t *testing.T) {
	env := acctest.New(t)
	key := strings.ReplaceAll(acctest.RandomName("tf_acc"), "-", "_")
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(envVarConfig(key, "https://example.com/a?b=1&c=<2>")),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("noderings_environment_variable.test", "key", key),
					resource.TestCheckResourceAttr("noderings_environment_variable.test", "value", "https://example.com/a?b=1&c=<2>"),
				),
			},
			{ResourceName: "noderings_environment_variable.test", ImportState: true, ImportStateVerify: true},
			{
				Config:           env.Config(envVarConfig(key, "two")),
				ConfigPlanChecks: expectAction("noderings_environment_variable.test", plancheck.ResourceActionUpdate),
				Check:            resource.TestCheckResourceAttr("noderings_environment_variable.test", "value", "two"),
			},
			{
				Config:           env.Config(envVarConfig(key+"_b", "two")),
				ConfigPlanChecks: expectAction("noderings_environment_variable.test", plancheck.ResourceActionDestroyBeforeCreate),
				Check:            resource.TestCheckResourceAttr("noderings_environment_variable.test", "key", key+"_b"),
			},
		},
	})
}

func TestEnvironmentVariableResourceDrift(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	cfg := env.Config(envVarConfig("tf_acc_drift", "one"))
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: func(s *terraform.State) error {
					env.Fake.SetEnvironmentVariableOutOfBand(resourceID(s, "noderings_environment_variable.test"), "changed")
					return nil
				},
				ExpectNonEmptyPlan: true,
			},
			{Config: cfg, Check: resource.TestCheckResourceAttr("noderings_environment_variable.test", "value", "one")},
			{
				Config: cfg,
				Check: func(s *terraform.State) error {
					env.Fake.DeleteEnvironmentVariableOutOfBand(resourceID(s, "noderings_environment_variable.test"))
					return nil
				},
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

func TestEnvironmentVariableResourceRejectsBadInput(t *testing.T) {
	env := acctest.New(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{Config: env.Config(envVarConfig("has-dash", "v")), ExpectError: regexp.MustCompile(`lower-case letters, digits and _`)},
			{Config: env.Config(envVarConfig("UPPER", "v")), ExpectError: regexp.MustCompile(`lower-case letters, digits and _`)},
			{Config: env.Config(envVarConfig("ok", "")), ExpectError: regexp.MustCompile(`length must be between 1 and 32767`)},
		},
	})
}
