# This receipt must depend on the successful write-only Lock consumer.
# Bind that consumer to paperclip_routine_trigger.webhook.id, and deliver
# ephemeral.paperclip_routine_webhook_secret.initial.secret to its write-only input.
resource "paperclip_routine_webhook_delivery" "confirmed" {
  routine_id = paperclip_routine.daily.id
  trigger_id = paperclip_routine_trigger.webhook.id
  generation = 1
  # depends_on = [your_write_only_consumer.resource_name]
}
