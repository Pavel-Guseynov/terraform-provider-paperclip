terraform {
  required_providers {
    paperclip = { source = "guseynov/paperclip", version = "0.1.0" }
  }
}
variable "endpoint" { type = string }
variable "connection_template" {
  type    = string
  default = null
}
provider "paperclip" {
  endpoint = var.endpoint
  token    = "board-token"
}
resource "paperclip_stdio_template" "new" {
  company_id  = "stdio-plan"
  template_id = "same-apply"
  name        = "Same apply"
  command     = "nix-mcp"
}
resource "paperclip_connection" "new" {
  company_id = "stdio-plan"
  app_id     = "stdio-app"
  name       = "Same apply"
  transport  = "local_stdio"
  auth_kind  = "none"
  config     = jsonencode({ templateId = var.connection_template == null ? paperclip_stdio_template.new.template_id : var.connection_template })
}
