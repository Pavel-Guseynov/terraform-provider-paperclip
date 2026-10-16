# First adopt pending setup with a trigger resource depending on this producer.
# Bind the write-only consumer to that persistent trigger ID, then create
# paperclip_routine_webhook_delivery depending on the successful consumer.
# Generation changes are rejected because native rotation is irreversible.
ephemeral "paperclip_routine_webhook_secret" "initial" {
  routine_id = paperclip_routine.daily.id
  name = "daily-webhook"
  generation = 1
  applying = tofu.applying
  signing_mode = "hmac_sha256"
  replay_window_sec = 300
}
