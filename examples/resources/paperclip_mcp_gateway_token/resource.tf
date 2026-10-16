# Use the same attributes for the ephemeral producer and durable record.
# Deliver the ephemeral token to an external write-only consumer. Bind that
# consumer's rotation trigger to this generation, and add depends_on referencing
# that consumer to the durable record before applying.
resource "paperclip_mcp_gateway_token" "automation" {
  company_id = paperclip_company.acme.id
  gateway_id = paperclip_mcp_gateway.automation.id
  name = "automation-client"
  client_label = "Automation"
  generation = 1
  expires_at = "2030-01-01T00:00:00Z"
}
