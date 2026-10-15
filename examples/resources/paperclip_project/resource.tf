resource "paperclip_project" "core" {
  company_id                 = paperclip_company.acme.id
  name                       = "core-platform"
  description                = "Core platform engineering project"
  execution_workspace_policy = jsonencode({ mode = "git_worktree" })
}
