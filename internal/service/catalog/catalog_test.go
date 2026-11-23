package catalog_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/noderings/terraform-provider-noderings/internal/acctest"
)

func TestDistrosDataSource(t *testing.T) {
	env := acctest.New(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.Config(`data "noderings_distros" "all" {}`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrSet("data.noderings_distros.all", "distros.0.distro"),
				resource.TestMatchResourceAttr("data.noderings_distros.all", "distros.0.distro", regexp.MustCompile(`^[a-z]+_[0-9_]+$`)),
			),
		}},
	})
}

func TestInstantAppDataSource(t *testing.T) {
	env := acctest.New(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(`data "noderings_instant_app" "wp" { slug = "wordpress" }`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.noderings_instant_app.wp", "id"),
					resource.TestCheckResourceAttr("data.noderings_instant_app.wp", "slug", "wordpress"),
					resource.TestMatchResourceAttr("data.noderings_instant_app.wp", "distro", regexp.MustCompile(`^[a-z]+_[0-9_]+$`)),
				),
			},
			{
				Config:      env.Config(`data "noderings_instant_app" "nope" { slug = "does-not-exist-tf-acc" }`),
				ExpectError: regexp.MustCompile(`not found`),
			},
		},
	})
}

func TestMarketplaceRegionsDataSource(t *testing.T) {
	env := acctest.New(t)
	checks := []resource.TestCheckFunc{resource.TestCheckResourceAttrSet("data.noderings_marketplace_regions.vm", "regions.0.city")}
	if env.Fake != nil {
		// The API returns regions unsorted; the data source sorts them.
		checks = append(checks,
			resource.TestCheckResourceAttr("data.noderings_marketplace_regions.vm", "regions.0.country", "Germany"),
			resource.TestCheckResourceAttr("data.noderings_marketplace_regions.vm", "regions.1.country", "Netherlands"),
		)
	}
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config:      env.Config(`data "noderings_marketplace_regions" "bad" { asset_type = "vm" }`),
				ExpectError: regexp.MustCompile(`value must be one of`),
			},
			{
				Config: env.Config(`data "noderings_marketplace_regions" "vm" { asset_type = "virtual_machine" }`),
				Check:  resource.ComposeAggregateTestCheckFunc(checks...),
			},
		},
	})
}
