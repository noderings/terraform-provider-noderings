resource "noderings_game_server_backup" "before_upgrade" {
  game_server_id = noderings_game_server.survival.id
  name           = "before 1.21 upgrade"
  locked         = true
}
