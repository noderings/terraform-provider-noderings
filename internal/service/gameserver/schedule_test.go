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

const sched = "noderings_game_server_schedule.test"
