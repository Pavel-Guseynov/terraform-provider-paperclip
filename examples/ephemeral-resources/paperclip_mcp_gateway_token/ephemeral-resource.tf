ephemeral "paperclip_mcp_gateway_token" "automation" {
  company_id = paperclip_company.acme.id
  gateway_id = paperclip_mcp_gateway.automation.id
  name = "automation-client"
  client_label = "Automation"
  generation = 1
  expires_at = "2030-01-01T00:00:00Z"
  allowed_actions = jsonencode(["tools/list", "tools/call"])
  applying = tofu.applying
}
# Pass .token only to an external write-only consumer. Couple its rotation
# trigger to generation and order the matching durable token record after it.
