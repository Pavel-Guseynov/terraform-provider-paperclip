package client

import (
	"context"
	"fmt"
	"net/http"
)

type InstanceSettings struct {
	ID                   string                 `json:"id"`
	DefaultEnvironmentID *string                `json:"defaultEnvironmentId"`
	General              map[string]interface{} `json:"general"`
	Experimental         map[string]interface{} `json:"experimental"`
}

func (c *Client) GetInstanceSettings(ctx context.Context) (*InstanceSettings, error) {
	var value InstanceSettings
	if err := c.paperclipRequest(ctx, http.MethodGet, "/api/instance/settings", nil, &value); err != nil {
		return nil, err
	}
	if value.ID == "" || value.General == nil || value.Experimental == nil {
		return nil, fmt.Errorf("instance settings response is incomplete")
	}
	return &value, nil
}

// Each PATCH is native and independent; the API has no cross-section transaction.
func (c *Client) PatchInstanceSettings(ctx context.Context, section string, body map[string]interface{}) error {
	return c.paperclipRequest(ctx, http.MethodPatch, "/api/instance/settings"+section, body, nil)
}
