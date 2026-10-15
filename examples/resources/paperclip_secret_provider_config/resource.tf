resource "paperclip_secret_provider_config" "vault" {
  company_id    = paperclip_company.acme.id
  provider_type = "local_encrypted"
  display_name  = "Local Encrypted Store"
  is_default    = true
}
