# Import a virtual machine by its ID. The API does not return ssh_key_ids,
# password, instant_app_variables or coupon_code: keep them in the
# configuration. Filling them in after an import does not replace the VM.
terraform import noderings_virtual_machine.web 00000000-0000-0000-0000-000000000000
