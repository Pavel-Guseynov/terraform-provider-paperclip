resource "paperclip_budget_policy" "monthly_cap" {
  company_id        = paperclip_company.acme.id
  scope_type        = "company"
  scope_id          = paperclip_company.acme.id
  metric            = "billed_cents"
  window_kind       = "calendar_month_utc"
  amount            = 500000
  warn_percent      = 80
  hard_stop_enabled = true
  notify_enabled    = true
}
