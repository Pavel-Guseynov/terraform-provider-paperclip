resource "paperclip_workspace" "main" {
  project_id  = paperclip_project.core.id
  name        = "primary"
  repo_url    = "https://git.guseynov.org/guseynov/paperclip.git"
  branch      = "develop"
  is_primary  = true
  source_type = "git_repo"
}
