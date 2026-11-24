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
)

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
