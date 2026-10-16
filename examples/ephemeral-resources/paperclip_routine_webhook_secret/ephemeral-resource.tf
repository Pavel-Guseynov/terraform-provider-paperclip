# Deliver secret to a write-only consumer bound to trigger_id. The managed
# webhook must depend on that consumer before acknowledging pending setup.
# Generation changes are rejected because native rotation is irreversible.
ephemeral "paperclip_routine_webhook_secret" "initial" {
  routine_id = paperclip_routine.daily.id
  name = "daily-webhook"
  generation = 1
  applying = tofu.applying
  signing_mode = "hmac_sha256"
  replay_window_sec = 300
}
