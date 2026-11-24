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
