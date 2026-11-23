package catalog_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/noderings/terraform-provider-noderings/internal/acctest"
)

func TestInstantAppsDataSource(t *testing.T) {
	env := acctest.New(t)
	checks := []resource.TestCheckFunc{
		resource.TestCheckResourceAttrSet("data.noderings_instant_apps.all", "instant_apps.0.id"),
		resource.TestMatchResourceAttr("data.noderings_instant_apps.all", "instant_apps.0.distro", regexp.MustCompile(`^[a-z]+_[0-9_]+$`)),
	}
	if env.Fake != nil {
		checks = append(checks,
			resource.TestCheckResourceAttr("data.noderings_instant_apps.all", "instant_apps.#", "2"),
			resource.TestCheckResourceAttr("data.noderings_instant_apps.wp", "instant_apps.#", "1"),
			resource.TestCheckResourceAttr("data.noderings_instant_apps.wp", "instant_apps.0.slug", "wordpress"),
			resource.TestCheckResourceAttr("data.noderings_instant_apps.wp", "instant_apps.0.variables.#", "2"),
			resource.TestCheckResourceAttr("data.noderings_instant_apps.wp", "instant_apps.0.variables.0.name", "site_title"),
			resource.TestCheckResourceAttr("data.noderings_instant_apps.wp", "instant_apps.0.variables.0.required", "true"),
			resource.TestCheckNoResourceAttr("data.noderings_instant_apps.wp", "instant_apps.0.variables.0.default"),
			resource.TestCheckResourceAttr("data.noderings_instant_apps.wp", "instant_apps.0.variables.1.default", "admin@example.com"),
			resource.TestCheckResourceAttr("data.noderings_instant_apps.debian", "instant_apps.#", "1"),
			resource.TestCheckResourceAttr("data.noderings_instant_apps.debian", "instant_apps.0.slug", "docker"),
		)
	}
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config:      env.Config(`data "noderings_instant_apps" "bad" { distro = "Ubuntu 24.04" }`),
				ExpectError: regexp.MustCompile(`must be a distro`),
			},
			{
				Config: env.Config(`
data "noderings_instant_apps" "all" {}

data "noderings_instant_apps" "wp" {
  query      = "Word"
  categories = ["cms"]
}

data "noderings_instant_apps" "debian" {
  distro = "debian_13"
}
`),
				Check: resource.ComposeAggregateTestCheckFunc(checks...),
			},
		},
	})
}
