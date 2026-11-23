resource "noderings_dns_record" "www" {
  zone_id = noderings_dns_zone.example.id
  name    = "www"
  type    = "A"
  ttl     = 300
  records = ["192.0.2.10", "192.0.2.11"]
}

resource "noderings_dns_record" "mx" {
  zone_id = noderings_dns_zone.example.id
  name    = "@"
  type    = "MX"
  ttl     = 3600
  records = ["10 mail.example.com.", "20 backup-mail.example.com."]
}

# TXT values are plain text: no surrounding quotes. Long values are split for you.
resource "noderings_dns_record" "spf" {
  zone_id = noderings_dns_zone.example.id
  name    = "@"
  type    = "TXT"
  ttl     = 3600
  records = ["v=spf1 mx -all"]
}
