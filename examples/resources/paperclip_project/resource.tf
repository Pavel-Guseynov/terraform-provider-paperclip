resource "paperclip_project" "core" {
  company_id                 = paperclip_company.acme.id
  name                       = "core-platform"
  description                = "Core platform engineering project"
  execution_workspace_policy {
    enabled = true
    default_mode = "isolated_workspace"
    shared_workspace_concurrency = "serialize"
    workspace_strategy {
      type = "git_worktree"
      base_ref = "develop"
    }
    authorization_policy {
      assignment_policy {
        mode = "protected"
      }
    }
  }
}
