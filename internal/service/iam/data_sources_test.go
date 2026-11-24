package iam_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/noderings/terraform-provider-noderings/internal/acctest"
)

func TestOrganizationDataSource(t *testing.T) {
	env := acctest.New(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.Config(`data "noderings_organization" "this" {}`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet("data.noderings_organization.this", "id"),
				resource.TestMatchResourceAttr("data.noderings_organization.this", "type", regexp.MustCompile(`^(client|provider)$`)),
			),
		}},
	})
}
