resource "paperclip_stdio_template" "gitea_stdio" {
  company_id  = paperclip_company.acme.id
  template_id = "acme.mcp.gitea"
  name        = "gitea"
  description = "Managed Gitea MCP stdio launcher"
  command     = "gitea-mcp"
  args        = ["serve"]
  env_keys    = ["GITEA_TOKEN"]
}
