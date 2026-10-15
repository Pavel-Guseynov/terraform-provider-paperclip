resource "paperclip_user_instance_access" "operator_access" {
  user_id           = data.paperclip_user.operator.id
  is_instance_admin = true
  company_ids       = [paperclip_company.acme.id]
}
