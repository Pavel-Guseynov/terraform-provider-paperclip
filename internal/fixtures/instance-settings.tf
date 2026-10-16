terraform {
  required_providers {
    paperclip = { source = "guseynov/paperclip", version = "0.1.0" }
  }
}
variable "endpoint" { type = string }
variable "token" { default = "settings-admin" }
variable "environment_id" { default = "11111111-1111-4111-8111-111111111111" }
variable "general" {
  default = { censorUsernameInLogs = true, keyboardShortcuts = true, feedbackDataSharingPreference = "not_allowed", backupRetention = { dailyDays = 14, weeklyWeeks = 2, monthlyMonths = 3 }, executionMode = "any" }
}
variable "experimental" { default = { enableEnvironments = true } }
provider "paperclip" {
  endpoint = var.endpoint
  token = var.token
}
resource "paperclip_instance_settings" "instance" {
  default_environment_id = var.environment_id
  general = jsonencode(var.general)
  experimental = jsonencode(var.experimental)
}
