resource "paperclip_secret" "api_token" {
  company_id    = paperclip_company.acme.id
  name          = "forge-token"
  key           = "forge_token"
  description   = "Forge API token"
  provider_type = "local_encrypted"
  value         = var.forge_token
  rotation      = "2026-10"
}
