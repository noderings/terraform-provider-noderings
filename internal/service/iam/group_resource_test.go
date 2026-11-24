package iam_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/noderings/terraform-provider-noderings/internal/acctest"
)

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
