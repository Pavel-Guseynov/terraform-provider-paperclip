resource "paperclip_company" "acme" {
  name                                  = "Acme"
  description                           = "Autonomous engineering organization"
  status                                = "active"
  require_board_approval_for_new_agents = false
  feedback_data_sharing_enabled         = false
  archive_on_destroy                    = true
}
