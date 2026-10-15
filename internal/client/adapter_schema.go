package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// AdapterConfigSchema is the native form contract, not a closed JSON Schema.
// An absent additionalProperties policy leaves unlisted keys unconstrained.
type AdapterConfigSchema struct {
	Fields []struct {
		Key      string `json:"key"`
		Type     string `json:"type"`
		Required bool   `json:"required"`
		Options  []struct {
			Value string `json:"value"`
		} `json:"options"`
	} `json:"fields"`
	AdditionalProperties *bool `json:"additionalProperties,omitempty"`
}

func (c *Client) GetAdapterConfigSchema(ctx context.Context, adapterType string) (*AdapterConfigSchema, error) {
	var result AdapterConfigSchema
	if err := c.paperclipRequest(ctx, http.MethodGet, fmt.Sprintf("/api/adapters/%s/config-schema", url.PathEscape(adapterType)), nil, &result); err != nil {
		return nil, err
	}
	if result.Fields == nil {
		return nil, fmt.Errorf("adapter config-schema response has no fields array; cannot validate the published contract")
	}
	return &result, nil
}
