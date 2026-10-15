resource "paperclip_workspace" "main" {
  project_id  = paperclip_project.core.id
  name        = "primary"
  repo_url    = "https://git.example.com/acme/core-platform.git"
  repo_ref    = "develop"
  default_ref = "main"
  is_primary  = true
  source_type = "git_repo"
}
