package gameserver_test

import (
	"os"
	"testing"
	"time"

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

const srv = "noderings_game_server.test"

func attr(s *terraform.State, name, key string) string {
	return s.RootModule().Resources[name].Primary.Attributes[key]
}
