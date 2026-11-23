# Import a secret by its ID. The API never returns the value, so the next
# apply writes the configured value.
terraform import noderings_secret.registry_password 00000000-0000-0000-0000-000000000000
