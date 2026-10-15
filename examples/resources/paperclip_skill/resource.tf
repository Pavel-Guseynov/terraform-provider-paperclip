resource "paperclip_skill" "task_lifecycle" {
  company_id       = paperclip_company.acme.id
  name             = "paperclip-task-lifecycle"
  slug             = "paperclip-task-lifecycle"
  description      = "Deliver a Paperclip code task in its selected worktree"
  content          = "# Paperclip Task Lifecycle\n"
  version_snapshot = "v1.0.0"
}
