resource "paperclip_policy" "require_review" {
  company_id  = paperclip_company.acme.id
  name        = "require-write-approval"
  policy_type = "require_approval"
  priority    = 50
  enabled     = true
  selectors   = jsonencode({ projectId = paperclip_project.core.id })
  conditions  = jsonencode({ risk = { isWrite = true } })
}
