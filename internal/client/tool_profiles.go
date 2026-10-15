package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ToolProfile owns the complete entries array, never the producer's catalog.
type ToolProfile struct {
	ID            string                   `json:"id,omitempty"`
	CompanyID     string                   `json:"companyId,omitempty"`
	ProfileKey    string                   `json:"profileKey"`
	Name          string                   `json:"name"`
	Description   *string                  `json:"description"`
	Status        string                   `json:"status"`
	DefaultAction string                   `json:"defaultAction"`
	Metadata      map[string]interface{}   `json:"metadata"`
	Entries       []map[string]interface{} `json:"entries"`
	Bindings      []ToolProfileBinding     `json:"bindings,omitempty"`
	Summary       struct {
		IsCompanyDefault bool `json:"isCompanyDefault"`
	} `json:"summary,omitempty"`
}

type ToolProfileBinding struct {
	ID         string                 `json:"id,omitempty"`
	CompanyID  string                 `json:"companyId,omitempty"`
	ProfileID  string                 `json:"profileId,omitempty"`
	TargetType string                 `json:"targetType"`
	TargetID   string                 `json:"targetId"`
	Priority   int64                  `json:"priority"`
	Metadata   map[string]interface{} `json:"metadata"`
}

func (p ToolProfile) AssertDeclarativeOwnership() error {
	if strings.HasPrefix(p.ProfileKey, "app:") || p.Metadata["source"] == "app_gallery_finish" {
		return fmt.Errorf("profile %s belongs to app_permission/finish; retain that owner and do not import, bind, reassign or mutate it", p.ID)
	}
	return nil
}

func (c *Client) ListToolProfiles(ctx context.Context, companyID string) ([]ToolProfile, error) {
	var response struct {
		Profiles []ToolProfile `json:"profiles"`
	}
	if err := c.paperclipRequest(ctx, http.MethodGet, "/api/companies/"+url.PathEscape(companyID)+"/tools/profiles", nil, &response); err != nil {
		return nil, fmt.Errorf("native profile inventory could not be read: %v", err)
	}
	if response.Profiles == nil {
		return nil, fmt.Errorf("native profile inventory did not return a profiles array; refresh/import is unsupported on this server")
	}
	seen := map[string]bool{}
	for _, p := range response.Profiles {
		if p.ID == "" || p.CompanyID != companyID || p.Entries == nil || p.Bindings == nil || seen[p.ID] {
			return nil, fmt.Errorf("native profile inventory is incomplete or contradictory; refusing to infer absence")
		}
		seen[p.ID] = true
	}
	return response.Profiles, nil
}

func (c *Client) GetToolProfile(ctx context.Context, companyID, profileID string) (*ToolProfile, error) {
	profiles, err := c.ListToolProfiles(ctx, companyID)
	if err != nil {
		return nil, err
	}
	for _, p := range profiles {
		if p.ID == profileID {
			return &p, nil
		}
	}
	return nil, ErrNotFound
}

func (c *Client) WriteToolProfile(ctx context.Context, companyID, profileID string, payload map[string]interface{}) (*ToolProfile, error) {
	method, route := http.MethodPost, "/api/companies/"+url.PathEscape(companyID)+"/tools/profiles"
	if profileID != "" {
		method, route = http.MethodPatch, "/api/tool-profiles/"+url.PathEscape(profileID)
	}
	var response ToolProfile
	if err := c.paperclipRequest(ctx, method, route, payload, &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) DeleteToolProfile(ctx context.Context, profileID string, force bool, reassign string) error {
	payload := map[string]interface{}{"force": force}
	if reassign != "" {
		payload["reassignToProfileId"] = reassign
	}
	return c.paperclipRequest(ctx, http.MethodDelete, "/api/tool-profiles/"+url.PathEscape(profileID), payload, nil)
}

func (c *Client) BindToolProfile(ctx context.Context, companyID, profileID string, payload ToolProfileBinding) (*ToolProfileBinding, error) {
	var response ToolProfileBinding
	err := c.paperclipRequest(ctx, http.MethodPost, "/api/companies/"+url.PathEscape(companyID)+"/tools/profiles/"+url.PathEscape(profileID)+"/bind", payload, &response)
	return &response, err
}

func (c *Client) UnbindToolProfile(ctx context.Context, companyID, profileID, targetType, targetID string) error {
	return c.paperclipRequest(ctx, http.MethodPost, "/api/companies/"+url.PathEscape(companyID)+"/tools/profiles/"+url.PathEscape(profileID)+"/unbind", map[string]string{"targetType": targetType, "targetId": targetID}, nil)
}

// AssertToolTarget uses existing native read authorities, before any mutation.
func (c *Client) AssertToolTarget(ctx context.Context, companyID, kind, id string) error {
	if kind == "application" {
		_, err := c.GetToolApp(ctx, companyID, id)
		return err
	}
	if kind == "catalog_entry" {
		connections, err := c.ListToolConnections(ctx, companyID)
		if err != nil {
			return err
		}
		for _, connection := range connections {
			entries, err := c.GetConnectionCatalog(ctx, connection.ID)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				if entry["id"] == id && entry["companyId"] == companyID {
					return nil
				}
			}
		}
		return fmt.Errorf("catalog entry %s is not visible in company %s", id, companyID)
	}
	if kind == "company" {
		if companyID != id {
			return fmt.Errorf("company binding must target the same company")
		}
		_, err := c.GetCompany(ctx, id)
		return err
	}
	if kind == "gateway" {
		var response interface{}
		if err := c.paperclipRequest(ctx, http.MethodGet, "/api/companies/"+url.PathEscape(companyID)+"/tools/gateways", nil, &response); err != nil {
			return err
		}
		for _, item := range extractItems(response, "gateways") {
			if item["id"] == id && item["companyId"] == companyID {
				return nil
			}
		}
		return fmt.Errorf("%s %s is not visible in company %s", kind, id, companyID)
	}
	routes := map[string]string{"agent": "agents", "project": "projects", "routine": "routines", "issue": "issues", "connection": "tool-connections"}
	route, ok := routes[kind]
	if !ok {
		return fmt.Errorf("unsupported tool reference kind %s", kind)
	}
	var response struct {
		ID        string `json:"id"`
		CompanyID string `json:"companyId"`
	}
	if err := c.paperclipRequest(ctx, http.MethodGet, "/api/"+route+"/"+url.PathEscape(id), nil, &response); err != nil {
		return err
	}
	if response.ID != id || response.CompanyID != companyID {
		return fmt.Errorf("%s %s must belong to company %s", kind, id, companyID)
	}
	return nil
}
