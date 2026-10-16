resource "paperclip_tool_profile" "qa" {
  company_id     = paperclip_company.acme.id
  profile_key    = "qa-read"
  name           = "QA scoped access"
  status         = "active"
  default_action = "deny"
  metadata       = jsonencode({ owner = "platform" })
  entries = jsonencode([
    { selectorType = "tool_name", toolName = "read_issue", effect = "include" }
  ])
}
