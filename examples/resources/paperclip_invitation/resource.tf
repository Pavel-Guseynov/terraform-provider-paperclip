resource "paperclip_invitation" "operator_invite" {
  company_id         = paperclip_company.acme.id
  allowed_join_types = "both"
  human_role         = "operator"
  agent_message      = "Join the Acme engineering fleet"
}
