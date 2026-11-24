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

const alloc = "noderings_game_server_allocation.test"
