package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// Claims are scoped to a configured provider process. The API has no persistent
// declarative-owner or conditional-write field; independent states must not
// manage the same connection's aggregate.
func (c *Client) ClaimConnectionAggregate(kind, connectionID, ownerID string) error {
	c.credMu.Lock()
	defer c.credMu.Unlock()
	key := kind + ":" + connectionID
	if c.managedOwners == nil {
		c.managedOwners = map[string]string{}
	}
	if prior := c.managedOwners[key]; prior != "" && prior != ownerID {
		return fmt.Errorf("conflicting owners for connection %s %s; declare exactly one aggregate resource per connection in this provider configuration", connectionID, kind)
	}
	c.managedOwners[key] = ownerID
	return nil
}

func (c *Client) RevokeConnectionCredentialGrants(ctx context.Context, connectionID string) error {
	grant, err := c.GetDefaultConnectionGrant(ctx, connectionID)
	if err != nil {
		return err
	}
	if grant == nil {
		return nil
	}
	ids := grant.SupersededIDs
	if grant.Status == "active" {
		ids = append(ids, grant.ID)
	}
	for _, id := range ids {
		if err := c.RevokeConnectionGrant(ctx, connectionID, id); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) RevokeConnectionGrant(ctx context.Context, connectionID, grantID string) error {
	return c.paperclipRequest(ctx, http.MethodDelete, "/api/tool-connections/"+url.PathEscape(connectionID)+"/grants/"+url.PathEscape(grantID), nil, nil)
}

type ConnectionGrantAudience struct {
	PaperclipConnectionGrant
	SubjectUserID string `json:"subjectUserId"`
	CallerUserID  string `json:"-"`
	Members       []struct {
		SubjectID string `json:"subjectId"`
	} `json:"members"`
	Delegations []ConnectionGrantDelegation `json:"delegations"`
}

type ConnectionGrantDelegation struct {
	ID      string `json:"id"`
	AgentID string `json:"agentId"`
}

func (c *Client) ListConnectionGrantAudiences(ctx context.Context, connectionID string) ([]ConnectionGrantAudience, error) {
	var response struct {
		Grants        []ConnectionGrantAudience `json:"grants"`
		CurrentUserID string                    `json:"currentUserId"`
	}
	if err := c.paperclipRequest(ctx, http.MethodGet, "/api/tool-connections/"+url.PathEscape(connectionID)+"/grants", nil, &response); err != nil {
		return nil, err
	}
	if response.Grants == nil {
		return nil, fmt.Errorf("native grant inventory did not return a grants array")
	}
	for i := range response.Grants {
		response.Grants[i].CallerUserID = response.CurrentUserID
	}
	return response.Grants, nil
}

func (c *Client) ReplaceConnectionGrantMembers(ctx context.Context, connectionID, grantID string, members []string) error {
	return c.paperclipRequest(ctx, http.MethodPut, "/api/tool-connections/"+url.PathEscape(connectionID)+"/grants/"+url.PathEscape(grantID)+"/members", map[string]interface{}{"memberUserIds": members}, nil)
}

func (c *Client) CreateConnectionGrantDelegation(ctx context.Context, connectionID, grantID, agentID string) error {
	return c.paperclipRequest(ctx, http.MethodPost, "/api/tool-connections/"+url.PathEscape(connectionID)+"/grants/"+url.PathEscape(grantID)+"/delegations", map[string]string{"agentId": agentID}, nil)
}

func (c *Client) RevokeConnectionGrantDelegation(ctx context.Context, connectionID, grantID, delegationID string) error {
	return c.paperclipRequest(ctx, http.MethodDelete, "/api/tool-connections/"+url.PathEscape(connectionID)+"/grants/"+url.PathEscape(grantID)+"/delegations/"+url.PathEscape(delegationID), nil, nil)
}
