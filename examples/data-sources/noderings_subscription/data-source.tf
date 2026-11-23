data "noderings_subscription" "web" {
  virtual_machine_id = "00000000-0000-0000-0000-000000000000"
}

output "next_renewal" {
  value = data.noderings_subscription.web.current_period_end
}
