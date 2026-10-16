resource "paperclip_instance_settings" "instance" {
  default_environment_id = var.default_environment_id
  general = jsonencode({
    censorUsernameInLogs = true
    keyboardShortcuts = true
    feedbackDataSharingPreference = "prompt"
    backupRetention = { dailyDays = 7, weeklyWeeks = 4, monthlyMonths = 1 }
    executionMode = "any"
  })
  experimental = jsonencode({ enableEnvironments = true })
}
