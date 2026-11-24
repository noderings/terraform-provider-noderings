package gameserver_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/noderings/terraform-provider-noderings/internal/acctest"
	"github.com/noderings/terraform-provider-noderings/internal/fakeapi"
)

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
