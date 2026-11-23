package gameserver_test

import (
	"fmt"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/noderings/terraform-provider-noderings/internal/acctest"
	"github.com/noderings/terraform-provider-noderings/internal/fakeapi"
	"github.com/noderings/terraform-provider-noderings/internal/service/gameserver"
)

func TestMain(m *testing.M) {
	if !acctest.Live() {
		gameserver.PollInterval = 5 * time.Millisecond
	}
	os.Exit(m.Run())
}

// game returns the plan, game and second game to test with. Live runs order
// real (billed) servers and need NODERINGS_ACC_GAME_PLAN_ID,
// NODERINGS_ACC_GAME_TEMPLATE_ID and NODERINGS_ACC_GAME_TEMPLATE2_ID for a
// Minecraft-like game with MAX_PLAYERS and SERVER_JARFILE variables.
func game(t *testing.T) (plan, template, template2 string) {
	t.Helper()
	if !acctest.Live() {
		return fakeapi.GamePlanID, fakeapi.GameTemplateID, fakeapi.GameTemplateVanillaID
	}
	plan, template, template2 = os.Getenv("NODERINGS_ACC_GAME_PLAN_ID"), os.Getenv("NODERINGS_ACC_GAME_TEMPLATE_ID"), os.Getenv("NODERINGS_ACC_GAME_TEMPLATE2_ID")
	if plan == "" || template == "" || template2 == "" {
		t.Skip("set NODERINGS_ACC_GAME_PLAN_ID, NODERINGS_ACC_GAME_TEMPLATE_ID and NODERINGS_ACC_GAME_TEMPLATE2_ID to run game server tests against the real API")
	}
	return plan, template, template2
}

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

const srv = "noderings_game_server.test"

func serverID(s *terraform.State) string { return s.RootModule().Resources[srv].Primary.ID }

func attr(s *terraform.State, name, key string) string {
	return s.RootModule().Resources[name].Primary.Attributes[key]
}

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

func TestGameServerDataSources(t *testing.T) {
	env := acctest.New(t)
	name := acctest.RandomName("tf-gs")
	cfg := serverConfig(t, name, "") + `
data "noderings_game_server" "by_id" {
  id = noderings_game_server.test.id
}
data "noderings_game_server" "by_name" {
  name = noderings_game_server.test.name
}
data "noderings_game_servers" "all" {
  name       = noderings_game_server.test.name
  depends_on = [noderings_game_server.test]
}
data "noderings_game_server_startup" "test" {
  game_server_id = noderings_game_server.test.id
}
data "noderings_game_server_resources" "test" {
  game_server_id = noderings_game_server.test.id
}
`
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{{
			Config: env.Config(cfg),
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttrPair("data.noderings_game_server.by_id", "primary_port", srv, "primary_port"),
				resource.TestCheckResourceAttrPair("data.noderings_game_server.by_name", "id", srv, "id"),
				resource.TestCheckResourceAttr("data.noderings_game_server.by_name", "phase", "ready"),
				resource.TestCheckResourceAttrPair("data.noderings_game_server.by_name", "region_city", srv, "region_city"),
				resource.TestCheckResourceAttr("data.noderings_game_servers.all", "ids.#", "1"),
				resource.TestCheckResourceAttrPair("data.noderings_game_servers.all", "game_servers.0.id", srv, "id"),
				resource.TestCheckResourceAttrPair("data.noderings_game_server_startup.test", "docker_image", srv, "docker_image"),
				resource.TestCheckResourceAttrSet("data.noderings_game_server_startup.test", "docker_images.0.image"),
				resource.TestCheckResourceAttrSet("data.noderings_game_server_startup.test", "variables.0.env_variable"),
				resource.TestCheckResourceAttrSet("data.noderings_game_server_resources.test", "state"),
				resource.TestCheckResourceAttrSet("data.noderings_game_server_resources.test", "memory_bytes"),
			),
		}},
	})
}

func TestGameServerGamesDataSource(t *testing.T) {
	env := acctest.New(t)
	plan, template, _ := game(t)
	cfg := func(name string) string {
		return env.Config(fmt.Sprintf(`
data "noderings_game_server_games" "test" {
  plan_id = %q
  name    = %q
}
`, plan, name))
	}
	steps := []resource.TestStep{{
		Config: env.Config(fmt.Sprintf(`
data "noderings_game_server_games" "all" {
  plan_id = %q
}
`, plan)),
		Check: resource.ComposeAggregateTestCheckFunc(
			resource.TestCheckResourceAttrSet("data.noderings_game_server_games.all", "games.0.id"),
			resource.TestCheckNoResourceAttr("data.noderings_game_server_games.all", "id"),
		),
	}}
	if !acctest.Live() {
		steps = append(steps,
			resource.TestStep{
				Config: cfg("minecraft · paper"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.noderings_game_server_games.test", "id", template),
					resource.TestCheckResourceAttr("data.noderings_game_server_games.test", "eula", "true"),
					resource.TestCheckResourceAttr("data.noderings_game_server_games.test", "games.#", "2"),
					resource.TestCheckResourceAttr("data.noderings_game_server_games.test", "variables.0.env_variable", "MINECRAFT_VERSION"),
					resource.TestCheckResourceAttr("data.noderings_game_server_games.test", "variables.1.user_editable", "false"),
					resource.TestCheckResourceAttr("data.noderings_game_server_games.test", "variables.3.required", "true"),
				),
			},
			resource.TestStep{Config: cfg("Tetris"), ExpectError: regexp.MustCompile(`has no game named "Tetris"`)},
			resource.TestStep{Config: cfg(fakeapi.GameTemplateName)},
		)
	}
	env.Run(t, resource.TestCase{Steps: steps})
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

const alloc = "noderings_game_server_allocation.test"

func allocationConfig(t *testing.T, name, body string) string {
	return serverConfig(t, name, "") + fmt.Sprintf(`
resource "noderings_game_server_allocation" "test" {
  game_server_id = noderings_game_server.test.id
  %s
}

data "noderings_game_server" "test" {
  id         = noderings_game_server.test.id
  depends_on = [noderings_game_server_allocation.test]
}
`, body)
}

func TestGameServerAllocation(t *testing.T) {
	env := acctest.New(t)
	name := acctest.RandomName("tf-gs")
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(allocationConfig(t, name, `notes = "query port"`)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(alloc, "notes", "query port"),
					resource.TestCheckResourceAttr(alloc, "primary", "false"),
					resource.TestCheckResourceAttrSet(alloc, "ip"),
					resource.TestCheckResourceAttrSet(alloc, "port"),
					resource.TestMatchResourceAttr(alloc, "id", regexp.MustCompile(`^[0-9a-f-]{36}/\d+$`)),
				),
			},
			{
				Config: env.Config(allocationConfig(t, name, `notes = "rcon"`)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(alloc, plancheck.ResourceActionUpdate)},
				},
				Check: resource.TestCheckResourceAttr(alloc, "notes", "rcon"),
			},
			{
				ResourceName:      alloc,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				// Making it primary moves the server's address.
				Config: env.Config(allocationConfig(t, name, `
  notes   = "rcon"
  primary = true`)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(alloc, "primary", "true"),
					resource.TestCheckResourceAttrPair("data.noderings_game_server.test", "primary_port", alloc, "port"),
				),
			},
			{
				Config: env.Config(allocationConfig(t, name, `
  notes   = "rcon"
  primary = false`)),
				ExpectError: regexp.MustCompile(`Cannot unset the primary allocation`),
			},
			{
				// Destroying the primary allocation moves the primary role back first.
				Config: env.Config(serverConfig(t, name, "") + `
data "noderings_game_server" "test" {
  id = noderings_game_server.test.id
}
`),
				Check: resource.TestCheckResourceAttrPair("data.noderings_game_server.test", "primary_port", srv, "primary_port"),
			},
		},
	})
}

func TestGameServerAllocationOutOfBand(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	cfg := env.Config(allocationConfig(t, acctest.RandomName("tf-gs"), `notes = "voice"`))
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: func(s *terraform.State) error {
					env.Fake.SetAllocationNotesOutOfBand(serverID(s), attr(s, alloc, "allocation_id"), "changed")
					return nil
				},
				ExpectNonEmptyPlan: true,
			},
			{
				Config: cfg,
				Check: func(s *terraform.State) error {
					env.Fake.DeleteGameServerObjectOutOfBand(serverID(s), "allocation", attr(s, alloc, "allocation_id"))
					return nil
				},
				ExpectNonEmptyPlan: true,
			},
			{Config: cfg, Check: resource.TestCheckResourceAttr(alloc, "notes", "voice")},
			{
				ResourceName:  alloc,
				ImportState:   true,
				ImportStateId: "not-an-id",
				ExpectError:   regexp.MustCompile(`<game_server_id>/<allocation_id>`),
			},
			{Config: cfg},
		},
	})
}

const db = "noderings_game_server_database.test"

func databaseConfig(t *testing.T, server, name, extra string) string {
	return serverConfig(t, server, "") + fmt.Sprintf(`
resource "noderings_game_server_database" "test" {
  game_server_id = noderings_game_server.test.id
  name           = %q
  %s
}
`, name, extra)
}

func TestGameServerDatabase(t *testing.T) {
	env := acctest.New(t)
	server := acctest.RandomName("tf-gs")
	var password string
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(databaseConfig(t, server, "luckperms", "")),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(db, "remote", "%"),
					resource.TestMatchResourceAttr(db, "full_name", regexp.MustCompile(`_luckperms$`)),
					resource.TestCheckResourceAttrSet(db, "username"),
					resource.TestCheckResourceAttrSet(db, "host"),
					resource.TestCheckResourceAttrSet(db, "jdbc_url"),
					func(s *terraform.State) error {
						password = attr(s, db, "password")
						if password == "" {
							return fmt.Errorf("password not set after create")
						}
						return nil
					},
				),
			},
			{
				// A refresh must not wipe the password (lists do not return it).
				Config:   env.Config(databaseConfig(t, server, "luckperms", "")),
				PlanOnly: true,
			},
			{
				Config: env.Config(databaseConfig(t, server, "luckperms", `rotation_trigger = "2026-12"`)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(db, plancheck.ResourceActionUpdate)},
				},
				Check: func(s *terraform.State) error {
					if p := attr(s, db, "password"); p == "" || p == password {
						return fmt.Errorf("password not rotated")
					}
					return nil
				},
			},
			{
				ResourceName:            db,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"password", "jdbc_url", "rotation_trigger"},
			},
			{
				Config: env.Config(databaseConfig(t, server, "stats", `rotation_trigger = "2026-12"`)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(db, plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: resource.TestMatchResourceAttr(db, "full_name", regexp.MustCompile(`_stats$`)),
			},
		},
	})
}

// Reads of several sub-resources on one server share one list call per run.
func TestGameServerSubResourceListsAreShared(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	cfg := env.Config(serverConfig(t, acctest.RandomName("tf-gs"), "") + `
resource "noderings_game_server_database" "a" {
  game_server_id = noderings_game_server.test.id
  name           = "a"
}
resource "noderings_game_server_database" "b" {
  game_server_id = noderings_game_server.test.id
  name           = "b"
}
`)
	lists := func(server string) int {
		n := 0
		env.Fake.With(func() {
			for _, c := range env.Fake.Calls {
				if c == "GET /v1/game-servers/"+server+"/databases" {
					n++
				}
			}
		})
		return n
	}
	var server string
	var before int
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: func(s *terraform.State) error {
					server = serverID(s)
					before = lists(server)
					return nil
				},
			},
			{
				RefreshState: true,
				Check: func(*terraform.State) error {
					// One list per Terraform run (the post-apply plan and this
					// refresh), not one per database.
					if n := lists(server) - before; n != 2 {
						return fmt.Errorf("expected 2 database list calls, got %d", n)
					}
					return nil
				},
			},
		},
	})
}

func TestGameServerDatabaseOutOfBandAndValidation(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	server := acctest.RandomName("tf-gs")
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{Config: env.Config(databaseConfig(t, server, "bad name", "")), ExpectError: regexp.MustCompile(`must be 1 to 48 letters`)},
			{Config: env.Config(databaseConfig(t, server, "ok", `remote = "a b"`)), ExpectError: regexp.MustCompile(`MySQL host pattern`)},
			{
				Config: env.Config(databaseConfig(t, server, "lobby", "")),
				Check: func(s *terraform.State) error {
					env.Fake.DeleteGameServerObjectOutOfBand(serverID(s), "database", attr(s, db, "database_id"))
					return nil
				},
				ExpectNonEmptyPlan: true,
			},
			{Config: env.Config(databaseConfig(t, server, "lobby", "")), Check: resource.TestCheckResourceAttrSet(db, "password")},
		},
	})
}

const sched = "noderings_game_server_schedule.test"

func scheduleConfig(t *testing.T, server, body string) string {
	return serverConfig(t, server, "") + fmt.Sprintf(`
resource "noderings_game_server_schedule" "test" {
  game_server_id = noderings_game_server.test.id
  %s
}
`, body)
}

func TestGameServerSchedule(t *testing.T) {
	env := acctest.New(t)
	server := acctest.RandomName("tf-gs")
	var firstTask string
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(scheduleConfig(t, server, `
  name             = "nightly restart"
  cron             = { minute = "0", hour = "4" }
  only_when_online = true

  task {
    action  = "command"
    payload = "say Restarting in 5 minutes"
  }
  task {
    action      = "power"
    payload     = "restart"
    time_offset = 300
  }`)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(sched, "active", "true"),
					resource.TestCheckResourceAttr(sched, "cron.month", "*"),
					resource.TestCheckResourceAttr(sched, "cron.hour", "4"),
					resource.TestCheckResourceAttr(sched, "task.#", "2"),
					resource.TestCheckResourceAttr(sched, "task.0.sequence_id", "1"),
					resource.TestCheckResourceAttr(sched, "task.1.sequence_id", "2"),
					resource.TestCheckResourceAttr(sched, "task.1.time_offset", "300"),
					resource.TestCheckResourceAttrSet(sched, "task.1.id"),
					func(s *terraform.State) error { firstTask = attr(s, sched, "task.0.id"); return nil },
				),
			},
			{
				// Settings change in place; tasks are reconciled by position.
				Config: env.Config(scheduleConfig(t, server, `
  name   = "nightly backup"
  cron   = { minute = "30", hour = "3", day_of_week = "1-5" }
  active = false

  task {
    action  = "command"
    payload = "say Backing up"
  }
  task {
    action = "backup"
  }
  task {
    action              = "power"
    payload             = "restart"
    continue_on_failure = true
  }`)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(sched, plancheck.ResourceActionUpdate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(sched, "name", "nightly backup"),
					resource.TestCheckResourceAttr(sched, "active", "false"),
					resource.TestCheckResourceAttr(sched, "only_when_online", "false"),
					resource.TestCheckResourceAttr(sched, "cron.day_of_week", "1-5"),
					resource.TestCheckResourceAttr(sched, "task.#", "3"),
					resource.TestCheckResourceAttr(sched, "task.0.payload", "say Backing up"),
					resource.TestCheckResourceAttr(sched, "task.1.action", "backup"),
					resource.TestCheckResourceAttr(sched, "task.2.continue_on_failure", "true"),
					resource.TestCheckResourceAttr(sched, "task.2.sequence_id", "3"),
					func(s *terraform.State) error {
						if attr(s, sched, "task.0.id") != firstTask {
							return fmt.Errorf("task 1 should be updated in place")
						}
						return nil
					},
				),
			},
			{
				ResourceName:      sched,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config: env.Config(scheduleConfig(t, server, `
  name   = "nightly backup"
  cron   = { minute = "30", hour = "3", day_of_week = "1-5" }
  active = true

  task {
    action = "backup"
  }`)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(sched, "active", "true"),
					resource.TestCheckResourceAttr(sched, "task.#", "1"),
					resource.TestCheckResourceAttr(sched, "task.0.action", "backup"),
				),
			},
		},
	})
}

func TestGameServerScheduleOutOfBandAndValidation(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	server := acctest.RandomName("tf-gs")
	body := `
  name = "restart"
  cron = { minute = "0" }
  task {
    action  = "power"
    payload = "restart"
  }`
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{Config: env.Config(scheduleConfig(t, server, `
  name = "x"
  cron = {}
  task {
    action  = "power"
    payload = "reboot"
  }`)), ExpectError: regexp.MustCompile(`start, stop, restart or kill`)},
			{Config: env.Config(scheduleConfig(t, server, `
  name = "x"
  cron = {}
  task {
    action      = "backup"
    time_offset = 901
  }`)), ExpectError: regexp.MustCompile(`between 0 and 900`)},
			{Config: env.Config(scheduleConfig(t, server, `
  name = "x"
  cron = {}
  task {
    action = "command"
  }`)), ExpectError: regexp.MustCompile(`Missing command`)},
			{
				Config: env.Config(scheduleConfig(t, server, body)),
				Check: func(s *terraform.State) error {
					env.Fake.SetScheduleActiveOutOfBand(serverID(s), attr(s, sched, "schedule_id"), false)
					return nil
				},
				ExpectNonEmptyPlan: true,
			},
			{
				Config: env.Config(scheduleConfig(t, server, body)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(sched, "active", "true"),
					func(s *terraform.State) error {
						env.Fake.DeleteGameServerObjectOutOfBand(serverID(s), "schedule", attr(s, sched, "schedule_id"))
						return nil
					},
				),
				ExpectNonEmptyPlan: true,
			},
			{Config: env.Config(scheduleConfig(t, server, body))},
		},
	})
}

const backup = "noderings_game_server_backup.test"

func backupConfig(t *testing.T, server, name string, locked bool) string {
	return serverConfig(t, server, "") + fmt.Sprintf(`
resource "noderings_game_server_backup" "test" {
  game_server_id = noderings_game_server.test.id
  name           = %q
  locked         = %t
}
`, name, locked)
}

func TestGameServerBackup(t *testing.T) {
	env := acctest.New(t)
	server := acctest.RandomName("tf-gs")
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(backupConfig(t, server, "before-update", false)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(backup, "successful", "true"),
					resource.TestCheckResourceAttr(backup, "locked", "false"),
					resource.TestCheckResourceAttrSet(backup, "completed_at"),
					resource.TestCheckResourceAttrSet(backup, "bytes"),
				),
			},
			{
				Config: env.Config(backupConfig(t, server, "before-update", true)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(backup, plancheck.ResourceActionUpdate)},
				},
				Check: resource.TestCheckResourceAttr(backup, "locked", "true"),
			},
			{
				ResourceName:            backup,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"timeouts"},
			},
			{
				// A locked backup is unlocked before it is replaced.
				Config: env.Config(backupConfig(t, server, "second", true)),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(backup, plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: resource.TestCheckResourceAttr(backup, "name", "second"),
			},
		},
	})
}

func TestGameServerBackupOutOfBand(t *testing.T) {
	env := acctest.New(t)
	env.RequireFake(t)
	server := acctest.RandomName("tf-gs")
	var gsID, backupID string
	env.Run(t, resource.TestCase{
		Steps: []resource.TestStep{
			{
				Config: env.Config(backupConfig(t, server, "nightly", false)),
				Check: func(s *terraform.State) error {
					gsID, backupID = serverID(s), attr(s, backup, "backup_id")
					env.Fake.SetBackupLockedOutOfBand(gsID, backupID, true)
					return nil
				},
				ExpectNonEmptyPlan: true,
			},
			{
				// The lock is a toggle: Terraform reads it and only flips it when needed.
				Config: env.Config(backupConfig(t, server, "nightly", false)),
				Check: func(*terraform.State) error {
					if locked, ok := env.Fake.GameServerBackupLocked(gsID, backupID); !ok || locked {
						return fmt.Errorf("backup should exist and be unlocked (exists=%t locked=%t)", ok, locked)
					}
					env.Fake.DeleteGameServerObjectOutOfBand(gsID, "backup", backupID)
					return nil
				},
				ExpectNonEmptyPlan: true,
			},
			{Config: env.Config(backupConfig(t, server, "nightly", true))},
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
