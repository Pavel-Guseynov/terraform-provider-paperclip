resource "paperclip_connection" "gitea_conn" {
  company_id = paperclip_company.acme.id
  app_id     = paperclip_app.gitea.id
  name       = "gitea-default"
  transport  = "stdio"
  auth_kind  = "api_key"
  config     = jsonencode({ command = "gitea-mcp" })
}
