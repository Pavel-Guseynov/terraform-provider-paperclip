package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// Environment bindings are used transiently to preserve unmanaged keys. They
// are never copied into Terraform state or diagnostics.
type PaperclipEnvironment struct {
	ID        string                 `json:"id"`
	CompanyID string                 `json:"companyId,omitempty"`
	Driver    string                 `json:"driver"`
	EnvVars   map[string]interface{} `json:"envVars"`
	Status    string                 `json:"status"`
	Config    map[string]interface{} `json:"config"`
}

func (e *PaperclipEnvironment) InheritsServicePath() bool {
	_, present := e.EnvVars["PATH"]
	return !present
}

func (c *Client) FindLocalEnvironment(ctx context.Context, companyID string) (*PaperclipEnvironment, error) {
	var environments []PaperclipEnvironment
	endpoint := "/api/companies/" + url.PathEscape(companyID) + "/environments?driver=local"
	if err := c.paperclipRequest(ctx, http.MethodGet, endpoint, nil, &environments); err != nil {
		return nil, err
	}
	if len(environments) != 1 || environments[0].ID == "" || environments[0].Driver != "local" {
		return nil, fmt.Errorf("expected exactly one existing Local execution environment")
	}
	return &environments[0], nil
}

func (c *Client) GetLocalEnvironment(ctx context.Context, id string) (*PaperclipEnvironment, error) {
	env, err := c.GetEnvironment(ctx, id)
	if err != nil {
		return nil, err
	}
	if env.Driver != "local" {
		return nil, fmt.Errorf("environment identity does not identify the requested local execution environment")
	}
	return env, nil
}

func (c *Client) GetEnvironment(ctx context.Context, id string) (*PaperclipEnvironment, error) {
	var env PaperclipEnvironment
	if err := c.paperclipRequest(ctx, http.MethodGet, "/api/environments/"+url.PathEscape(id), nil, &env); err != nil {
		return nil, err
	}
	if env.ID != id {
		return nil, fmt.Errorf("environment identity does not identify the requested execution environment")
	}
	return &env, nil
}

func (c *Client) InheritLocalServicePath(ctx context.Context, companyID, id string) (*PaperclipEnvironment, error) {
	env, err := c.GetLocalEnvironment(ctx, id)
	if err != nil {
		return nil, err
	}
	if env.InheritsServicePath() {
		return env, nil
	}
	delete(env.EnvVars, "PATH")
	endpoint := "/api/environments/" + url.PathEscape(id) + "?companyId=" + url.QueryEscape(companyID)
	if err := c.paperclipRequest(ctx, http.MethodPatch, endpoint, map[string]interface{}{"envVars": env.EnvVars}, nil); err != nil {
		return nil, err
	}
	updated, err := c.GetLocalEnvironment(ctx, id)
	if err != nil {
		return nil, err
	}
	if !updated.InheritsServicePath() {
		return nil, fmt.Errorf("local execution environment still overrides the service PATH after update")
	}
	return updated, nil
}

// CheckEnvironmentCapability verifies that the target Paperclip installation mounts the
// environment API routes before any resource mutation is planned or executed.
func (c *Client) CheckEnvironmentCapability(ctx context.Context, companyID string) error {
	cid := companyID
	if cid == "" {
		cid = "__capability_probe__"
	}
	var envs []PaperclipEnvironment
	endpoint := "/api/companies/" + url.PathEscape(cid) + "/environments?driver=local"
	err := c.paperclipRequest(ctx, http.MethodGet, endpoint, nil, &envs)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) {
			if apiErr.IsUnknownRoute {
				return fmt.Errorf("target Paperclip installation lacks required capability %q (route not found)", "environments")
			}
			if apiErr.StatusCode == http.StatusNotFound {
				return nil
			}
		}
		return fmt.Errorf("failed to verify capability %q: %w", "environments", err)
	}
	return nil
}
