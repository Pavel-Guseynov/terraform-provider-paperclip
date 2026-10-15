resource "paperclip_app" "gitea" {
  company_id  = paperclip_company.acme.id
  name        = "gitea"
  type        = "mcp_stdio"
  description = "Gitea MCP Server"
}
