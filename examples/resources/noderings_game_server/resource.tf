# A game-server plan ID, for example from the NodeRings marketplace.
variable "plan_id" {
  type = string
}

data "noderings_game_server_games" "paper" {
  plan_id = var.plan_id
  name    = "Minecraft · Paper"
}

resource "noderings_game_server" "survival" {
  name        = "survival"
  plan_id     = var.plan_id
  template_id = data.noderings_game_server_games.paper.id
  accept_eula = true

  # Only these variables are managed; the others keep their values.
  environment = {
    MINECRAFT_VERSION = "1.21.4"
    MAX_PLAYERS       = "30"
  }

  power_state         = "running"
  deletion_protection = true

  timeouts {
    create = "45m"
  }
}

output "address" {
  value = "${noderings_game_server.survival.primary_ip}:${noderings_game_server.survival.primary_port}"
}
