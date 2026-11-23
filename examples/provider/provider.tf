terraform {
  required_providers {
    noderings = {
      source = "noderings/noderings"
    }
  }
}

# The token is read from NODERINGS_TOKEN when not set here.
provider "noderings" {}
