data "paperclip_skill" "task_lifecycle" {
  company_id = data.paperclip_company.acme.id
  name       = "paperclip-task-lifecycle"
}
