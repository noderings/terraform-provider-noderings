# Security policy

## Reporting a vulnerability

Please do not open a public issue for security problems. Email **partners@noderings.com** with:

- a description of the issue and its impact
- steps to reproduce, or a proof of concept
- the provider version and Terraform or OpenTofu version (`terraform version`)

We acknowledge reports within three working days and keep you updated until the issue is fixed. Credit is given in the release notes unless you ask us not to.

## Supported versions

Security fixes are made for the latest minor release.

## Handling credentials

The provider reads the NodeRings service account token from the `token` argument or the `NODERINGS_TOKEN` environment variable. Prefer the environment variable, or a sensitive variable fed from your secret store, over a token written in configuration. Terraform state can hold sensitive values such as generated passwords, so store state in an encrypted backend with restricted access, and give the service account the narrowest NodeRings role that covers the resources you manage.
