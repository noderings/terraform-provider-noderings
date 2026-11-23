data "noderings_game_server_resources" "survival" {
  game_server_id = noderings_game_server.survival.id
}

output "memory_mib" {
  value = data.noderings_game_server_resources.survival.memory_bytes / 1048576
}
