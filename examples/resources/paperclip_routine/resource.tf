resource "paperclip_routine" "daily" {
  company_id        = paperclip_company.acme.id
  title             = "Daily status {{date}}"
  description       = "Prepare the daily report"
  assignee_agent_id = paperclip_agent.engineer.id
  status            = "active"
  env = { REPORT_FORMAT = { type = "plain", value = "markdown" } }
}
