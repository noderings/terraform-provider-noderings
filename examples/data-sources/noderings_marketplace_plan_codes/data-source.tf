data "noderings_marketplace_plan_codes" "frankfurt" {
  country      = "Germany"
  city         = "Frankfurt"
  billing_type = "usage"
}

output "plan_codes" {
  value = data.noderings_marketplace_plan_codes.frankfurt.plan_codes[*].plan_code
}
