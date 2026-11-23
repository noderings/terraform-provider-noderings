data "noderings_game_server" "survival" {
  name = "survival"
}

output "survival_address" {
  value = "${data.noderings_game_server.survival.primary_ip}:${data.noderings_game_server.survival.primary_port}"
}
