resource "paperclip_goal" "q4_delivery" {
  company_id     = paperclip_company.acme.id
  title          = "Deliver OpenTofu Provider Export"
  description    = "Publish standard OpenTofu Registry provider repository"
  level          = "company"
  status         = "active"
  owner_agent_id = paperclip_agent.engineer.id
  retirement     = "fail"
}
