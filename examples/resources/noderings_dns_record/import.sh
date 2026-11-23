# Import records as <zone_id>/<name>/<type>. Use @ for the zone apex.
terraform import noderings_dns_record.www 00000000-0000-0000-0000-000000000000/www/A
terraform import noderings_dns_record.mx 00000000-0000-0000-0000-000000000000/@/MX
