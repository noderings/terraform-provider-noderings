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
