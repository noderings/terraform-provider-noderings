package gameserver_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/noderings/terraform-provider-noderings/internal/acctest"
)

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

const backup = "noderings_game_server_backup.test"
