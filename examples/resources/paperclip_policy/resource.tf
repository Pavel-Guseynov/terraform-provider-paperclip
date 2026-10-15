resource "paperclip_policy" "require_review" {
  company_id  = paperclip_company.acme.id
  name        = "require-write-approval"
  policy_type = "require_approval"
  rules       = jsonencode({ enabled = true })
}
