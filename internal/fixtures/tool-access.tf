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
variable "entries" {
  type    = string
  default = "[{\"selectorType\":\"tool_name\",\"toolName\":\"read_issue\",\"effect\":\"include\"}]"
}
variable "priority" { default = 0 }
variable "enabled" { default = false }
variable "conditions" { default = "{\"risk\":{\"isWrite\":true}}" }
variable "profile_description" { default = "Scoped QA access" }
variable "force_destroy" { default = false }
resource "paperclip_project" "scope" {
  company_id = "comp-1"
  name       = "Scoped tool project"
}
resource "paperclip_agent" "qa" {
  company_id  = "comp-1"
  name        = "QA"
  role        = "engineer"
  adapter_type = "antigravity_local"
  can_assign_tasks = false
  can_create_agents = false
}
resource "paperclip_tool_profile" "scope" {
  company_id     = "comp-1"
  profile_key    = "qa-project-tools"
  name           = "QA project tools"
  description    = var.profile_description
  entries        = var.entries
  force_destroy  = var.force_destroy
}
resource "paperclip_tool_profile_binding" "scope" {
  for_each = { company = "comp-1", project = paperclip_project.scope.id, agent = paperclip_agent.qa.id }
  company_id  = "comp-1"
  profile_id  = paperclip_tool_profile.scope.id
  target_type = each.key
  target_id   = each.value
}
resource "paperclip_policy" "rate" {
  company_id  = "comp-1"
  name        = "QA project rate"
  policy_type = "rate_limit"
  priority    = var.priority
  enabled     = var.enabled
  description = "Scoped project and QA policy"
  selectors   = jsonencode({ projectId = paperclip_project.scope.id, agentIds = [paperclip_agent.qa.id] })
  conditions  = var.conditions
  config      = jsonencode({ limit = 10, windowSeconds = 60, keyBy = ["company", "agent", "tool"] })
}
resource "paperclip_policy" "effects" {
  for_each    = toset(["allow", "block", "require_approval"])
  company_id  = "comp-1"
  name        = "QA ${each.key}"
  policy_type = each.key
  selectors   = jsonencode({ agentId = paperclip_agent.qa.id })
}
output "profile_id" { value = paperclip_tool_profile.scope.id }
output "binding_ids" { value = { for key, binding in paperclip_tool_profile_binding.scope : key => binding.id } }
output "policy_id" { value = paperclip_policy.rate.id }
