resource "paperclip_local_environment" "host" {
  company_id           = paperclip_company.acme.id
  inherit_service_path = true
}
