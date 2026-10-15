resource "paperclip_project_lead" "core_lead" {
  project_id = paperclip_project.core.id
  agent_id   = paperclip_agent.lead.id
}
