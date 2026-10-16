resource "paperclip_mcp_gateway" "automation" {
  company_id = paperclip_company.acme.id
  name = "Automation gateway"
  slug = "automation"
  profile_id = paperclip_tool_profile.automation.id
  default_profile_mode = "gateway_only"
  context_scope_type = "none"
  on_demand_tools_config = jsonencode({ enabled = true })
}
