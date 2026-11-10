terraform {
  required_providers {
    paperclip = { source = "guseynov/paperclip", version = "0.1.0" }
  }
}
variable "endpoint" { type = string }
provider "paperclip" {
  endpoint = var.endpoint
  token    = "board-token"
}
# A custom connection whose declared config names a gallery template; its
# gallery_key is not declared.
resource "paperclip_connection" "custom" {
  company_id        = "template-connection"
  app_id            = "github-app"
  name              = "GitHub App"
  transport         = "mcp_remote"
  auth_kind         = "api_key"
  credential_policy = "shared"
  status            = "active"
  enabled           = true
  config = jsonencode({
    url                  = "https://api.githubcopilot.com/mcp/"
    sourceTemplateKey    = "github"
    connectionMethodKey  = "app-installation"
    quarantineNewEntries = true
    methodConfig         = { version = "1", appId = "3635012", installationId = "131703038", repositories = "virtex-main/virtex", permissionProfile = "author" }
  })
}
# An included connector connected through its gallery definition.
resource "paperclip_connection" "gallery" {
  company_id  = "template-connection"
  gallery_key = "hugging-face"
  transport   = "mcp_remote"
  auth_kind   = "oauth"
  config      = jsonencode({ sourceTemplateKey = "hugging-face", connectionMethodKey = "mcp-oauth", url = "https://huggingface.co/mcp" })
}
