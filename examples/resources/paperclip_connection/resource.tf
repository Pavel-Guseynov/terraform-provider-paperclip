resource "paperclip_connection" "gitea_conn" {
  company_id = paperclip_company.acme.id
  app_id     = paperclip_app.gitea.id
  name       = "gitea-default"
  transport  = "local_stdio"
  auth_kind  = "api_key"
  config     = jsonencode({ templateId = paperclip_stdio_template.gitea_stdio.template_id })
}
