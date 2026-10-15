data "paperclip_users" "directory" {
  company_id = data.paperclip_company.acme.id
  query      = "operator"
}
