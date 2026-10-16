resource "paperclip_tool_profile_binding" "qa" {
  company_id  = paperclip_company.acme.id
  profile_id  = paperclip_tool_profile.qa.id
  target_type = "agent"
  target_id   = paperclip_agent.engineer.id
  priority    = 100
}
