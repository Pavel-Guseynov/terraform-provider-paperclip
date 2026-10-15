resource "paperclip_user_company_access" "operator_companies" {
  user_id     = data.paperclip_user.operator.id
  company_ids = [paperclip_company.acme.id]
}
