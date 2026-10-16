resource "paperclip_routine_trigger" "daily" {
  routine_id      = paperclip_routine.daily.id
  kind            = "schedule"
  cron_expression = "0 9 * * *"
  timezone        = "UTC"
}
