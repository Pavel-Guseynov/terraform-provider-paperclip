terraform {
  required_providers {
    paperclip = { source = "guseynov/paperclip", version = "0.1.0" }
  }
}
variable "endpoint" { type = string }
variable "template_key" { default = "same-apply" }
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
  template_id = var.template_key
  name        = "Same apply"
  command     = "nix-mcp"
  tools       = file("producer-tools.json")
}
resource "paperclip_connection" "new" {
  company_id = "stdio-plan"
  app_id     = "stdio-app"
  name       = "Same apply"
  transport  = "local_stdio"
  auth_kind  = "none"
  status     = "active"
  enabled    = true
  config     = jsonencode({ templateId = var.connection_template == null ? paperclip_stdio_template.new.template_id : var.connection_template })
}
