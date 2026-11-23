data "noderings_game_server_startup" "survival" {
  game_server_id = noderings_game_server.survival.id
}

output "docker_images" {
  value = data.noderings_game_server_startup.survival.docker_images[*].image
}
