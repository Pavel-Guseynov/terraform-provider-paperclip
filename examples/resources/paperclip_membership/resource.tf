resource "paperclip_membership" "operator" {
  company_id = paperclip_company.acme.id
  user_id    = data.paperclip_user.operator.id
  role       = "operator"
  status     = "active"
}
