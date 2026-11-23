resource "noderings_game_server_schedule" "nightly" {
  game_server_id   = noderings_game_server.survival.id
  name             = "nightly restart"
  cron             = { minute = "0", hour = "4" }
  only_when_online = true

  task {
    action  = "command"
    payload = "say Restarting in 5 minutes"
  }

  task {
    action      = "backup"
    time_offset = 240
  }

  task {
    action      = "power"
    payload     = "restart"
    time_offset = 60
  }
}
