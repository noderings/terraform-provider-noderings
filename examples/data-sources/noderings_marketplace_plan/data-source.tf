# Check that the plan of an existing VM is still for sale, and its price.
data "noderings_marketplace_plan" "current" {
  plan_id = noderings_virtual_machine.web.plan_id
}

output "prices" {
  value = data.noderings_marketplace_plan.current.prices
}
