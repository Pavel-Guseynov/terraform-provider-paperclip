terraform {
  required_providers {
    paperclip = { source = "guseynov/paperclip", version = "0.1.0" }
  }
}
variable "endpoint" { type = string }
variable "clear" { default = false }
variable "trigger_kind" { default = "api" }
variable "project_id" {
  default = null
  type    = string
}
provider "paperclip" {
  endpoint = var.endpoint
  token    = "board-token"
}
resource "paperclip_routine" "daily" {
  company_id           = "comp-1"
  title                = var.clear ? "Daily" : "Daily {{name}}"
  description          = var.clear ? null : "Report {{date}}"
  project_id           = var.project_id
  status               = "paused"
  priority             = var.clear ? "low" : null
  concurrency_policy   = var.clear ? "skip_if_active" : null
  catch_up_policy      = var.clear ? "enqueue_missed_with_cap" : null
  activity_gate_policy = var.clear ? "require_external_activity" : null
  activity_gate_scope  = "company"
  variables            = var.clear ? null : jsonencode([{ name = "name", label = null, type = "text", defaultValue = "World", required = true, options = [] }])
  env                  = var.clear ? null : { MODE = { type = "plain", value = "report" } }
}
resource "paperclip_routine_trigger" "daily" {
  routine_id      = paperclip_routine.daily.id
  kind            = var.trigger_kind
  label           = var.clear ? null : "Daily trigger"
  enabled         = !var.clear
  cron_expression = var.trigger_kind == "schedule" ? (var.clear ? "30 10 * * *" : "0 9 * * *") : null
  timezone        = var.trigger_kind == "schedule" ? (var.clear ? "Asia/Almaty" : "UTC") : null
}
output "routine_id" { value = paperclip_routine.daily.id }
output "trigger_id" { value = paperclip_routine_trigger.daily.id }
