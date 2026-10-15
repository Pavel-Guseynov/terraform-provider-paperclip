resource "paperclip_app_permission" "gitea_perms" {
  company_id    = paperclip_company.acme.id
  connection_id = paperclip_connection.gitea_conn.id
  access_type   = "agents"
  agent_ids     = [paperclip_agent.engineer.id]
  tool_selection = jsonencode({
    approval_mode = "writes"
    enabled_tools = ["search_repos", "issue_read"]
  })
}
