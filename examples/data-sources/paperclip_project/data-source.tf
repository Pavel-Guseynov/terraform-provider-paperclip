data "paperclip_project" "core" {
  company_id = data.paperclip_company.acme.id
  name       = "core-platform"
}
