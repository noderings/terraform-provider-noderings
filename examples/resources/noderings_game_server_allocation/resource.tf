resource "noderings_game_server_allocation" "query" {
  game_server_id = noderings_game_server.survival.id
  notes          = "query port"
}

output "query_port" {
  value = noderings_game_server_allocation.query.port
}
