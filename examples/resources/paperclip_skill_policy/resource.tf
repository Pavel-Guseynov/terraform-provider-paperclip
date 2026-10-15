resource "paperclip_skill_policy" "company_policy" {
  company_id     = paperclip_company.acme.id
  default_effect = "allow"
  rules = jsonencode([
    {
      id       = "allow-skill-updates"
      priority = 100
      effect   = "allow"
      subject  = { type = "all_agents" }
      actions  = ["skills.edit", "skills.update"]
    }
  ])
}
