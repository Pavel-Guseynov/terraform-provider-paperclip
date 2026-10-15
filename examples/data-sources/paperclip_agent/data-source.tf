data "paperclip_agent" "engineer" {
  company_id = data.paperclip_company.acme.id
  name       = "Platform Engineer"
}
