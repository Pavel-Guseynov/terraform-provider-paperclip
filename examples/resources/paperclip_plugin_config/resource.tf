resource "paperclip_plugin_config" "linear" {
  company_id = paperclip_company.acme.id
  plugin_id  = paperclip_plugin.linear.id
  config = jsonencode({
    apiKey = {
      type     = "secret_ref"
      secretId = paperclip_secret.api_token.id
    }
  })
}
