package gameserver_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/noderings/terraform-provider-noderings/internal/acctest"
)

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

const db = "noderings_game_server_database.test"
