resource "paperclip_workspace" "main" {
  project_id  = paperclip_project.core.id
  name        = "primary"
  repo_url    = "https://git.example.com/acme/core-platform.git"
  branch      = "develop"
  is_primary  = true
  source_type = "git_repo"
}
