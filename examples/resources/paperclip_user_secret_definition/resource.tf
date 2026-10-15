resource "paperclip_user_secret_definition" "personal_token" {
  company_id     = paperclip_company.acme.id
  key            = "personal_access_token"
  name           = "Personal Access Token"
  description    = "User-supplied personal access token"
  usage_guidance = "Provide a read/write token from your account settings."
}
