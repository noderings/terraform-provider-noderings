data "noderings_game_servers" "ready" {
  phase = "ready"
}

output "ready_servers" {
  value = data.noderings_game_servers.ready.game_servers[*].name
}
