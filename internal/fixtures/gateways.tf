terraform {
  required_providers {
    paperclip = { source = "guseynov/paperclip", version = "0.1.0" }
    testconsumer = { source = "example/testconsumer", version = "0.1.0" }
  }
}
variable "endpoint" { type = string }
variable "generation" { default = 1 }
variable "fail_delivery" { default = false }
variable "gateway_status" { default = "active" }
variable "profile_id" { default = "42400000-0000-4000-8000-000000000001" }
provider "paperclip" {
  endpoint = var.endpoint
  token = "board-token"
}
resource "paperclip_mcp_gateway" "client" {
  company_id = "comp-1"
  name = "Client gateway"
  slug = "client-gateway"
  profile_id = var.profile_id
  status = var.gateway_status
  auth_config = jsonencode({bearer = {requireFiniteExpiry = true}})
  header_policy = jsonencode({callerPassthrough = {enabled = true, allowedHeaders = ["X-Request-Id"]}})
  metadata_policy = jsonencode({forwardGatewayId = true})
  on_demand_tools_config = jsonencode({enabled = true})
}
ephemeral "paperclip_mcp_gateway_token" "client" {
  company_id = "comp-1"
  gateway_id = paperclip_mcp_gateway.client.id
  name = "automation"
  client_label = "Automation"
  generation = var.generation
  expires_at = "2100-01-01T00:00:00Z"
  allowed_actions = jsonencode(["tools/list","tools/call"])
  applying = tofu.applying
}
resource "testconsumer_secret" "client" {
  name = "gateway-credential"
  value_wo = ephemeral.paperclip_mcp_gateway_token.client.token
  rotation = jsonencode({ company_id = "comp-1", gateway_id = paperclip_mcp_gateway.client.id, name = "automation", generation = var.generation })
  endpoint = "${var.endpoint}/api/test/gateway-delivery"
  fail_on_write = var.fail_delivery
}
resource "paperclip_mcp_gateway_token" "client" {
  company_id = "comp-1"
  gateway_id = paperclip_mcp_gateway.client.id
  name = "automation"
  client_label = "Automation"
  generation = var.generation
  expires_at = "2100-01-01T00:00:00Z"
  allowed_actions = jsonencode(["tools/list","tools/call"])
  depends_on = [testconsumer_secret.client]
}
output "gateway_id" { value = paperclip_mcp_gateway.client.id }
