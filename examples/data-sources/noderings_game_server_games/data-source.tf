variable "plan_id" {
  type = string
}

# Every game on the plan.
data "noderings_game_server_games" "all" {
  plan_id = var.plan_id
}

# One game, with its startup variables.
data "noderings_game_server_games" "paper" {
  plan_id = var.plan_id
  name    = "Minecraft · Paper"
}

output "games" {
  value = data.noderings_game_server_games.all.games[*].name
}

output "editable_variables" {
  value = [for v in data.noderings_game_server_games.paper.variables : v.env_variable if v.user_editable]
}
