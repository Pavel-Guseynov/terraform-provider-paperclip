resource "paperclip_permission_grants" "operator_grants" {
  company_id = paperclip_company.acme.id
  member_id  = paperclip_membership.operator.id
  grants = [
    {
      permission_key = "agents:create"
    }
  ]
}
