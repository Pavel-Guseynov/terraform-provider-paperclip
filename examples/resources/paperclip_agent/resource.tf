resource "paperclip_agent" "engineer" {
  company_id             = paperclip_company.acme.id
  name                   = "Platform Engineer"
  role                   = "engineer"
  title                  = "Senior Platform Engineer"
  adapter_type           = "antigravity_local"
  default_environment_id = paperclip_local_environment.host.id
  can_assign_tasks       = true
  can_create_agents      = false
  can_create_skills      = true
  trust_preset           = "standard"
}
