package iam_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	"github.com/noderings/terraform-provider-noderings/internal/acctest"
)

// testPublicKey is a throwaway key generated for these tests only.
const testPublicKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBanaaPWXU3rsndX+KbHbpnJKjJG3Wrkv5FmwNMDQX+q tf-acc"

func sshKeyConfig(name, key string) string {
	return fmt.Sprintf(`
resource "noderings_ssh_key" "test" {
  name       = %q
  public_key = %q
}
`, name, key)
}

func TestSSHKeyResource(t *testing.T) {
	env := acctest.New(t)
	name := acctest.RandomName("tf-acc")
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(sshKeyConfig(name, testPublicKey)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("noderings_ssh_key.test", "name", name),
					resource.TestCheckResourceAttr("noderings_ssh_key.test", "public_key", testPublicKey),
					resource.TestCheckResourceAttr("noderings_ssh_key.test", "key_type", "ed25519"),
					resource.TestMatchResourceAttr("noderings_ssh_key.test", "fingerprint", regexp.MustCompile(`^SHA256:`)),
					resource.TestCheckResourceAttrSet("noderings_ssh_key.test", "id"),
				),
			},
			{
				ResourceName:      "noderings_ssh_key.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				// Renaming replaces the key: the API has no update.
				Config: env.Config(sshKeyConfig(name+"-b", testPublicKey)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction("noderings_ssh_key.test", plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: resource.TestCheckResourceAttr("noderings_ssh_key.test", "name", name+"-b"),
			},
		},
	})
}

// file() keeps the key's trailing newline, which the API drops. After an
// import the state holds the API's spelling; that must not replace the key.
func TestSSHKeyImportWithTrailingNewline(t *testing.T) {
	env := acctest.New(t)
	name := acctest.RandomName("tf-acc")
	config := env.Config(sshKeyConfig(name, testPublicKey+"\n"))
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{Config: config},
			{
				// Plans an import block in a fresh state; fails unless the
				// plan only imports.
				Config:          config,
				ResourceName:    "noderings_ssh_key.test",
				ImportState:     true,
				ImportStateKind: resource.ImportBlockWithID,
			},
		},
	})
}

func TestSSHKeyResourceRejectsBadInput(t *testing.T) {
	env := acctest.New(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{Config: env.Config(sshKeyConfig("bad", "not an ssh key")), ExpectError: regexp.MustCompile(`OpenSSH public key`)},
			{Config: env.Config(sshKeyConfig(`a<b`, testPublicKey)), ExpectError: regexp.MustCompile(`must not contain`)},
		},
	})
}

func TestSSHKeyResourceDuplicate(t *testing.T) {
	env := acctest.New(t)
	name := acctest.RandomName("tf-acc")
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.Config(sshKeyConfig(name, testPublicKey) + fmt.Sprintf(`
resource "noderings_ssh_key" "dup" {
  name       = %q
  public_key = %q
  depends_on = [noderings_ssh_key.test]
}
`, name+"-dup", testPublicKey)),
			ExpectError: regexp.MustCompile(`already exists[\s\S]*terraform import`),
		}},
	})
}

func TestSSHKeyDataSource(t *testing.T) {
	env := acctest.New(t)
	name := acctest.RandomName("tf-acc")
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.Config(sshKeyConfig(name, testPublicKey) + `
data "noderings_ssh_key" "by_name" {
  name = upper(noderings_ssh_key.test.name)
}
data "noderings_ssh_key" "by_fingerprint" {
  fingerprint = noderings_ssh_key.test.fingerprint
}
data "noderings_ssh_key" "by_id" {
  id = noderings_ssh_key.test.id
}
`),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrPair("data.noderings_ssh_key.by_name", "id", "noderings_ssh_key.test", "id"),
				resource.TestCheckResourceAttrPair("data.noderings_ssh_key.by_fingerprint", "id", "noderings_ssh_key.test", "id"),
				resource.TestCheckResourceAttrPair("data.noderings_ssh_key.by_id", "public_key", "noderings_ssh_key.test", "public_key"),
			),
		}},
	})
}

func TestSSHKeyDataSourceRequiresOneFilter(t *testing.T) {
	env := acctest.New(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{{
			Config:      env.Config(`data "noderings_ssh_key" "x" {}`),
			ExpectError: regexp.MustCompile(`(?i)exactly one of these attributes must be configured`),
		}},
	})
}

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
