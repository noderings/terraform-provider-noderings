package provider_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/noderings/terraform-provider-noderings/internal/acctest"
	"github.com/noderings/terraform-provider-noderings/internal/fakeapi"
)

const orgData = `data "noderings_organization" "this" {}`

func TestProviderMissingToken(t *testing.T) {
	fake := fakeapi.New()
	defer fake.Close()
	t.Setenv("NODERINGS_TOKEN", "")
	t.Setenv("NODERINGS_API_TOKEN", "")
	t.Setenv("NR_API_TOKEN", "")
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config:      fmt.Sprintf(`provider "noderings" { endpoint = %q }`, fake.URL) + "\n" + orgData,
			ExpectError: regexp.MustCompile(`Missing NodeRings token`),
		}},
	})
}

func TestProviderTokenFromEnvironment(t *testing.T) {
	fake := fakeapi.New()
	defer fake.Close()
	t.Setenv("NODERINGS_TOKEN", "")
	t.Setenv("NR_API_TOKEN", "from-env")
	t.Setenv("NODERINGS_API_URL", fake.URL)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: `provider "noderings" {}` + "\n" + orgData,
			Check:  resource.TestCheckResourceAttr("data.noderings_organization.this", "id", fakeapi.OrgID),
		}},
	})
}

func TestProviderRejectsUnknownOrganization(t *testing.T) {
	fake := fakeapi.New()
	defer fake.Close()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{{
			Config: fmt.Sprintf(`provider "noderings" {
  endpoint        = %q
  token           = "t"
  organization_id = "11111111-1111-4111-8111-111111111111"
}
`, fake.URL) + orgData,
			ExpectError: regexp.MustCompile(`(Organization not available|Invalid secondary organization)`),
		}},
	})
}

func TestProviderInvalidSettings(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      "provider \"noderings\" {\n  token        = \"t\"\n  http_timeout = \"soon\"\n}\n" + orgData,
				ExpectError: regexp.MustCompile(`Invalid http_timeout`),
			},
			{
				Config:      "provider \"noderings\" {\n  token    = \"t\"\n  endpoint = \"api.example.com\"\n}\n" + orgData,
				ExpectError: regexp.MustCompile(`invalid endpoint`),
			},
		},
	})
}

// The provider retries rate-limited requests.
func TestProviderRetriesRateLimit(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	env.Fake.FailNext("GET /v1/distros", 2)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.Config(`data "noderings_distros" "all" {}`),
			Check:  resource.TestCheckResourceAttrSet("data.noderings_distros.all", "distros.0.distro"),
		}},
	})
}
