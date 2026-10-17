package client

// Planned template identities are local to one configured provider client.
// OpenTofu plans dependencies before their consumers, even when a required
// template_id is already known. These declarations permit plan-time deferral
// only; a connection write must still resolve the live approved template.
func (c *Client) RecordPlannedStdioTemplate(companyID, templateID string) {
	c.credMu.Lock()
	defer c.credMu.Unlock()
	if c.plannedStdioTemplates == nil {
		c.plannedStdioTemplates = map[[2]string]bool{}
	}
	c.plannedStdioTemplates[[2]string{companyID, templateID}] = true
}

func (c *Client) HasPlannedStdioTemplate(companyID, templateID string) bool {
	c.credMu.RLock()
	defer c.credMu.RUnlock()
	return c.plannedStdioTemplates[[2]string{companyID, templateID}]
}
