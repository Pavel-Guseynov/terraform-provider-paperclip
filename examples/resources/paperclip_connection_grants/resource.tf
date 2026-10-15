resource "paperclip_connection_grants" "gitea_grants" {
  company_id    = paperclip_company.acme.id
  connection_id = paperclip_connection.gitea_conn.id
  credential_secret_refs = {
    "credentials.api_key" = {
      secret_id = paperclip_secret.api_token.id
      version   = "latest"
    }
  }
}
