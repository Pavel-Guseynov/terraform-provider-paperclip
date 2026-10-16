package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// Gateway is read through the existing company inventory, which omits archives.
type Gateway struct {
	ID                  string                 `json:"id"`
	CompanyID           string                 `json:"companyId"`
	Name                string                 `json:"name"`
	Slug                string                 `json:"displaySlug"`
	Description         *string                `json:"description"`
	Status              string                 `json:"status"`
	ProfileID           string                 `json:"profileId"`
	DefaultProfileMode  string                 `json:"defaultProfileMode"`
	ContextScopeType    string                 `json:"contextScopeType"`
	ContextScopeID      *string                `json:"contextScopeId"`
	AgentID             *string                `json:"agentId"`
	ProjectID           *string                `json:"projectId"`
	IssueID             *string                `json:"issueId"`
	ApprovalIssueID     *string                `json:"approvalIssueId"`
	AuthConfig          map[string]interface{} `json:"authConfig"`
	HeaderPolicy        map[string]interface{} `json:"headerPolicy"`
	MetadataPolicy      map[string]interface{} `json:"metadataPolicy"`
	OnDemandToolsConfig map[string]interface{} `json:"onDemandToolsConfig"`
	Metadata            map[string]interface{} `json:"metadata"`
	Tokens              []GatewayToken         `json:"tokens"`
}

type GatewayToken struct {
	ID             string   `json:"id"`
	CompanyID      string   `json:"companyId"`
	GatewayID      string   `json:"gatewayId"`
	Name           string   `json:"name"`
	ClientLabel    string   `json:"clientLabel"`
	AllowedActions []string `json:"allowedActions"`
	ExpiresAt      string   `json:"expiresAt"`
	RevokedAt      *string  `json:"revokedAt"`
	Token          string   `json:"token,omitempty"`
}

func (c *Client) GetGateway(ctx context.Context, companyID, id string) (*Gateway, error) {
	var response struct {
		Gateways []Gateway `json:"gateways"`
	}
	if err := c.paperclipRequest(ctx, http.MethodGet, "/api/companies/"+url.PathEscape(companyID)+"/tools/gateways", nil, &response); err != nil {
		return nil, err
	}
	if response.Gateways == nil {
		return nil, fmt.Errorf("native gateway inventory is missing its gateways array; refusing to infer absence")
	}
	seen := map[string]bool{}
	for _, g := range response.Gateways {
		if g.ID == "" || g.CompanyID != companyID || seen[g.ID] || g.Name == "" || g.Slug == "" || g.Status == "" || g.ProfileID == "" || g.DefaultProfileMode == "" || g.ContextScopeType == "" || g.Metadata == nil || g.Tokens == nil || g.AuthConfig == nil || g.HeaderPolicy == nil || g.MetadataPolicy == nil || g.OnDemandToolsConfig == nil {
			return nil, fmt.Errorf("native gateway inventory is incomplete or contradictory; refusing to infer absence")
		}
		seen[g.ID] = true
		tokenIDs := map[string]bool{}
		for _, token := range g.Tokens {
			if token.ID == "" || token.CompanyID != companyID || token.GatewayID != g.ID || tokenIDs[token.ID] {
				return nil, fmt.Errorf("native gateway token inventory is incomplete or contradictory; refusing to infer absence")
			}
			tokenIDs[token.ID] = true
		}
	}
	for _, g := range response.Gateways {
		if g.ID == id {
			return &g, nil
		}
	}
	return nil, ErrNotFound
}

func (c *Client) WriteGateway(ctx context.Context, companyID, id string, payload map[string]interface{}) (*Gateway, error) {
	method, route := http.MethodPost, "/api/companies/"+url.PathEscape(companyID)+"/tools/gateways"
	if id != "" {
		method, route = http.MethodPatch, "/api/tool-gateway/gateways/"+url.PathEscape(id)
		payload["companyId"] = companyID
	}
	var g Gateway
	if err := c.paperclipRequest(ctx, method, route, payload, &g); err != nil {
		return nil, err
	}
	if g.ID == "" || g.CompanyID != companyID || (id != "" && g.ID != id) {
		return nil, fmt.Errorf("native gateway write returned an incomplete or contradictory identity")
	}
	return &g, nil
}

func (c *Client) CreateGatewayToken(ctx context.Context, companyID, gatewayID string, payload map[string]interface{}) (*GatewayToken, error) {
	payload["companyId"], payload["subjectType"] = companyID, "gateway_client"
	var token GatewayToken
	if err := c.paperclipRequest(ctx, http.MethodPost, "/api/tool-gateway/gateways/"+url.PathEscape(gatewayID)+"/tokens", payload, &token); err != nil {
		return nil, err
	}
	if token.ID == "" || token.CompanyID != companyID || token.GatewayID != gatewayID || token.Token == "" || token.ExpiresAt == "" {
		if token.ID != "" {
			if err := c.RevokeGatewayToken(context.WithoutCancel(ctx), companyID, token.ID); err != nil {
				return nil, fmt.Errorf("native token mint returned incomplete metadata and compensating revocation failed: %w", err)
			}
		}
		return nil, fmt.Errorf("native token mint returned incomplete metadata; inspect gateway token inventory before retrying")
	}
	return &token, nil
}

func (c *Client) RevokeGatewayToken(ctx context.Context, companyID, id string) error {
	return c.paperclipRequest(ctx, http.MethodPost, "/api/tool-gateway/gateway-tokens/"+url.PathEscape(id)+"/revoke", map[string]string{"companyId": companyID}, nil)
}

func (c *Client) AssertGatewayIssue(ctx context.Context, companyID, issueID, projectID string) error {
	var issue struct {
		ID        string  `json:"id"`
		CompanyID string  `json:"companyId"`
		ProjectID *string `json:"projectId"`
	}
	if err := c.paperclipRequest(ctx, http.MethodGet, "/api/issues/"+url.PathEscape(issueID), nil, &issue); err != nil {
		return err
	}
	if issue.ID != issueID || issue.CompanyID != companyID {
		return fmt.Errorf("gateway issue must belong to the company")
	}
	if projectID != "" && issue.ProjectID != nil && *issue.ProjectID != projectID {
		return fmt.Errorf("gateway issue must belong to the selected project; no mutation was sent")
	}
	return nil
}

func gatewayTokenLookup(companyID, gatewayID, name string) string {
	return "gateway:" + companyID + "/" + gatewayID + ":" + name
}

func (rec mintedKeyRecord) lookupKey() string {
	if rec.gatewayID != "" {
		return gatewayTokenLookup(rec.companyID, rec.gatewayID, rec.name)
	}
	return lookupKey(rec.name, rec.isBoard, rec.agentID)
}

func (c *Client) RegisterGatewayToken(companyID, gatewayID, name string, generation int64, id, token, configuration string) {
	c.credMu.Lock()
	defer c.credMu.Unlock()
	c.mintedKeys[id] = mintedKeyRecord{keyID: id, token: token, name: name, companyID: companyID, gatewayID: gatewayID, generation: generation, configuration: configuration}
	c.mintedKeysByName[gatewayTokenLookup(companyID, gatewayID, name)] = id
	c.mintedTokens[id] = token
	c.acknowledgedDeliveries[id] = false
}

func (c *Client) GatewayTokenConfiguration(id string) string {
	c.credMu.RLock()
	defer c.credMu.RUnlock()
	return c.mintedKeys[id].configuration
}

func (c *Client) GatewayTokenGeneration(companyID, gatewayID, name string, generation int64) (id, token string, delivered bool) {
	c.credMu.RLock()
	defer c.credMu.RUnlock()
	lk := gatewayTokenLookup(companyID, gatewayID, name)
	id = c.mintedKeysByName[lk]
	if c.mintedKeys[id].generation != generation {
		id = ""
	}
	token = c.mintedTokens[id]
	gen, ok := c.deliveredGenerations[lk]
	return id, token, ok && gen == generation
}

func (c *Client) RecordGatewayTokenDelivery(companyID, gatewayID, name string, generation int64, id string) {
	c.credMu.Lock()
	defer c.credMu.Unlock()
	c.deliveredGenerations[gatewayTokenLookup(companyID, gatewayID, name)] = generation
	c.acknowledgedDeliveries[id], c.deliveredTokens[id] = true, true
}
