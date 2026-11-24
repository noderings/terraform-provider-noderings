package gameserver_test

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

func serverConfig(t *testing.T, name, extra string) string {
	plan, template, _ := game(t)
	return fmt.Sprintf(`
resource "noderings_game_server" "test" {
  name        = %q
  plan_id     = %q
  template_id = %q
  accept_eula = true
  %s
}
`, name, plan, template, extra)
}

func serverID(s *terraform.State) string { return s.RootModule().Resources[srv].Primary.ID }

func TestGameServerResource(t *testing.T) {
	env := acctest.New(t)
	name := acctest.RandomName("tf-gs")
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(serverConfig(t, name, `
  environment = { MAX_PLAYERS = "30" }
  power_state = "running"`)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(srv, "name", name),
					resource.TestCheckResourceAttr(srv, "phase", "ready"),
					resource.TestCheckResourceAttr(srv, "power_state", "running"),
					resource.TestCheckResourceAttr(srv, "environment.MAX_PLAYERS", "30"),
					resource.TestCheckResourceAttr(srv, "environment.%", "1"),
					resource.TestCheckResourceAttr(srv, "allow_destructive_game_change", "false"),
					resource.TestCheckResourceAttrSet(srv, "region_id"),
					resource.TestCheckResourceAttrSet(srv, "primary_ip"),
					resource.TestCheckResourceAttrSet(srv, "primary_port"),
					resource.TestCheckResourceAttrSet(srv, "docker_image"),
					resource.TestCheckResourceAttrSet(srv, "game_name"),
					resource.TestCheckResourceAttrSet(srv, "plan_billing_type"),
					resource.TestCheckResourceAttrSet(srv, "created_at"),
				),
			},
			{
				// Name, environment, image and power change in place.
				Config: env.Config(serverConfig(t, name+"-2", `
  environment  = { MAX_PLAYERS = "40" }
  docker_image = "ghcr.io/example/games:java_17"
  power_state  = "offline"`)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(srv, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(srv, "name", name+"-2"),
					resource.TestCheckResourceAttr(srv, "environment.MAX_PLAYERS", "40"),
					resource.TestCheckResourceAttr(srv, "docker_image", "ghcr.io/example/games:java_17"),
					resource.TestCheckResourceAttr(srv, "power_state", "offline"),
				),
			},
			{
				ResourceName:            srv,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"environment", "accept_eula", "version_value"},
			},
		},
	})
}

func TestGameServerValidation(t *testing.T) {
	env := acctest.New(t)
	plan, template, _ := game(t)
	noEULA := env.Config(fmt.Sprintf(`
resource "noderings_game_server" "test" {
  name        = "x"
  plan_id     = %q
  template_id = %q
}
`, plan, template))
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{Config: env.Config(serverConfig(t, "x", `environment = { NOPE = "1" }`)), ExpectError: regexp.MustCompile(`has no variable NOPE`)},
			{Config: env.Config(serverConfig(t, "x", `environment = { SERVER_JARFILE = "other.jar" }`)), ExpectError: regexp.MustCompile(`SERVER_JARFILE cannot be edited`)},
			{Config: noEULA, ExpectError: regexp.MustCompile(`Set accept_eula = true`)},
			{Config: env.Config(serverConfig(t, "x", `power_state = "paused"`)), ExpectError: regexp.MustCompile(`power_state`)},
			{Config: env.Config(serverConfig(t, "a <b>", "")), ExpectError: regexp.MustCompile(`must not contain`)},
			{Config: env.Config(serverConfig(t, "x", `timeouts { create = "soon" }`)), ExpectError: regexp.MustCompile(`not a positive duration`)},
			{Config: env.Config(`
resource "noderings_game_server" "test" {
  name        = "x"
  plan_id     = "not-a-uuid"
  template_id = "also-not"
}
`), ExpectError: regexp.MustCompile(`must be a UUID`)},
			{Config: env.Config("")},
		},
	})
}

// Changing the game without allow_destructive_game_change replaces the
// server; with it, the game changes in place.
func TestGameServerChangeGame(t *testing.T) {
	env := acctest.New(t)
	plan, template, template2 := game(t)
	name := acctest.RandomName("tf-gs")
	cfg := func(tmpl string, allow bool) string {
		return env.Config(fmt.Sprintf(`
resource "noderings_game_server" "test" {
  name                          = %q
  plan_id                       = %q
  template_id                   = %q
  accept_eula                   = true
  allow_destructive_game_change = %t
}
`, name, plan, tmpl, allow))
	}
	var first string
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: cfg(template, false),
				Check:  func(s *terraform.State) error { first = serverID(s); return nil },
			},
			{
				Config: cfg(template2, false),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(srv, plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: func(s *terraform.State) error {
					if serverID(s) == first {
						return fmt.Errorf("expected a new server")
					}
					first = serverID(s)
					return nil
				},
			},
			{Config: cfg(template2, true)},
			{
				Config: cfg(template, true),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(srv, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(srv, "template_id", template),
					resource.TestCheckResourceAttr(srv, "phase", "ready"),
					func(s *terraform.State) error {
						if serverID(s) != first {
							return fmt.Errorf("expected the same server after an in-place game change")
						}
						return nil
					},
				),
			},
		},
	})
}

func TestGameServerDeletionProtection(t *testing.T) {
	env := acctest.New(t)
	name := acctest.RandomName("tf-gs")
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{Config: env.Config(serverConfig(t, name, "deletion_protection = true"))},
			{Config: env.Config(""), ExpectError: regexp.MustCompile(`Deletion protection is on`)},
			{Config: env.Config(serverConfig(t, name, "deletion_protection = false"))},
		},
	})
}

func TestGameServerInstallFails(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	env.Fake.SetGameServerInstallOutcome(fakeapi.GamePhaseError, "install script exited with code 1", 1)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{{
			Config:      env.Config(serverConfig(t, acctest.RandomName("tf-gs"), "")),
			ExpectError: regexp.MustCompile(`failed to install: install\s+script exited with code 1`),
		}},
	})
}

func TestGameServerInstallTimeout(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	env.Fake.SetGameServerInstallOutcome("", "", 0)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{{
			Config:      env.Config(serverConfig(t, acctest.RandomName("tf-gs"), `timeouts { create = "50ms" }`)),
			ExpectError: regexp.MustCompile(`still installing after\s+50ms`),
		}},
	})
}

func TestGameServerRemovedOutOfBand(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.Config(serverConfig(t, acctest.RandomName("tf-gs"), "")),
			Check: func(s *terraform.State) error {
				env.Fake.DeleteGameServerOutOfBand(serverID(s))
				return nil
			},
			ExpectNonEmptyPlan: true,
		}},
	})
}

func TestGameServerDrift(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	name := acctest.RandomName("tf-gs")
	cfg := env.Config(serverConfig(t, name, `
  environment = { MAX_PLAYERS = "30" }
  power_state = "running"`))
	var gsID string
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: func(s *terraform.State) error {
					gsID = serverID(s)
					env.Fake.RenameGameServerOutOfBand(gsID, "renamed")
					env.Fake.SetGameServerVariableOutOfBand(gsID, "MAX_PLAYERS", "5")
					env.Fake.SetGameServerVariableOutOfBand(gsID, "MOTD", "unmanaged")
					env.Fake.SetGameServerPowerOutOfBand(gsID, "offline")
					return nil
				},
				ExpectNonEmptyPlan: true,
			},
			{
				Config: cfg,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(srv, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(srv, "name", name),
					resource.TestCheckResourceAttr(srv, "environment.MAX_PLAYERS", "30"),
					resource.TestCheckResourceAttr(srv, "power_state", "running"),
					func(*terraform.State) error {
						if v := env.Fake.GameServerVariable(gsID, "MOTD"); v != "unmanaged" {
							return fmt.Errorf("unmanaged variable changed to %q", v)
						}
						return nil
					},
				),
			},
		},
	})
}

// While the server is not ready, sub-resources cannot be read; their state is kept.
func TestGameServerNotReadyKeepsSubResources(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	cfg := env.Config(serverConfig(t, acctest.RandomName("tf-gs"), "") + `
resource "noderings_game_server_database" "test" {
  game_server_id = noderings_game_server.test.id
  name           = "stats"
}
`)
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: func(s *terraform.State) error {
					env.Fake.SetGameServerPhase(serverID(s), fakeapi.GamePhasePending, "")
					return nil
				},
			},
			{
				RefreshState: true,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(srv, "phase", "pending"),
					resource.TestCheckResourceAttrSet("noderings_game_server_database.test", "password"),
					func(s *terraform.State) error {
						env.Fake.SetGameServerPhase(serverID(s), fakeapi.GamePhaseReady, "")
						return nil
					},
				),
			},
			{Config: cfg, PlanOnly: true},
		},
	})
}

// Changing the game deletes every schedule and backup; Terraform plans to create them again.
func TestGameServerChangeGameRecreatesChildren(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	plan, template, template2 := game(t)
	cfg := func(tmpl string) string {
		return env.Config(fmt.Sprintf(`
resource "noderings_game_server" "test" {
  name                          = "change-game"
  plan_id                       = %q
  template_id                   = %q
  accept_eula                   = true
  allow_destructive_game_change = true
}

resource "noderings_game_server_backup" "test" {
  game_server_id = noderings_game_server.test.id
  name           = "kept"
}
`, plan, tmpl))
	}
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{Config: cfg(template)},
			{Config: cfg(template2), ExpectNonEmptyPlan: true},
			{
				Config: cfg(template2),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(backup, plancheck.ResourceActionCreate)},
				},
			},
		},
	})
}
