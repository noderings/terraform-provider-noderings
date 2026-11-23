resource "noderings_dns_geo_record" "app" {
  zone_id = noderings_dns_zone.example.id
  name    = "app"
  type    = "A"
  ttl     = 60
  default = "192.0.2.10"

  continents = {
    EU = "198.51.100.10"
  }
  countries = {
    JP = "203.0.113.10"
  }
  regions = {
    "US-CA" = "192.0.2.20"
  }
}
