resource "noderings_game_server_database" "luckperms" {
  game_server_id = noderings_game_server.survival.id
  name           = "luckperms"

  # Change this value to set a new password, for example monthly.
  rotation_trigger = "2026-11"
}

output "luckperms_jdbc_url" {
  value     = noderings_game_server_database.luckperms.jdbc_url
  sensitive = true
}
