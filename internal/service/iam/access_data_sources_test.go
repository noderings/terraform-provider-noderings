package iam_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/noderings/terraform-provider-noderings/internal/acctest"
	"github.com/noderings/terraform-provider-noderings/internal/fakeapi"
)

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
