terraform {
  required_providers {
    paperclip    = { source = "guseynov/paperclip", version = "0.1.0" }
    testconsumer = { source = "example/testconsumer", version = "0.1.0" }
  }
}
variable "endpoint" { type = string }
variable "routine_id" { type = string }
variable "generation" { default = 1 }
variable "fail_delivery" { default = false }
provider "paperclip" {
  endpoint = var.endpoint
  token    = "board-token"
}
provider "testconsumer" {}
ephemeral "paperclip_routine_webhook_secret" "initial" {
  routine_id        = var.routine_id
  name              = "native-webhook"
  generation        = var.generation
  applying          = tofu.applying
  signing_mode      = "hmac_sha256"
  replay_window_sec = 300
}
resource "testconsumer_secret" "delivery" {
  name          = paperclip_routine_trigger.webhook.id
  value_wo      = ephemeral.paperclip_routine_webhook_secret.initial.secret
  endpoint      = "${var.endpoint}/api/test/routines/delivery"
  rotation      = tostring(var.generation)
  fail_on_write = var.fail_delivery
}
resource "paperclip_routine_trigger" "webhook" {
  routine_id            = var.routine_id
  kind                  = "webhook"
  webhook_delivery_name = "native-webhook"
  webhook_generation    = var.generation
  signing_mode          = "hmac_sha256"
  replay_window_sec     = 300
  depends_on            = [ephemeral.paperclip_routine_webhook_secret.initial]
}
resource "paperclip_routine_webhook_delivery" "confirmed" {
  routine_id = var.routine_id
  trigger_id = paperclip_routine_trigger.webhook.id
  generation = var.generation
  depends_on = [testconsumer_secret.delivery]
}
