package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

var (
	ErrNotFound = errors.New("resource not found")
	ErrConflict = errors.New("resource conflict")
)

// APIError carries actionable error evidence including HTTP status, method,
// path, correlation identity, and sanitized response body.
type APIError struct {
	Method         string `json:"method"`
	Path           string `json:"path"`
	StatusCode     int    `json:"statusCode"`
	CorrelationID  string `json:"correlationId"`
	Body           string `json:"body"`
	IsUnknownRoute bool   `json:"isUnknownRoute"`
}

func (e *APIError) Error() string {
	return fmt.Sprintf("paperclip http %d: method=%s path=%s correlation=%s body=%s",
		e.StatusCode, e.Method, e.Path, e.CorrelationID, e.Body)
}

func (e *APIError) Is(target error) bool {
	if target == ErrNotFound {
		return e.StatusCode == http.StatusNotFound && !e.IsUnknownRoute
	}
	if target == ErrConflict {
		return e.StatusCode == http.StatusConflict
	}
	return false
}

type supersededKeyRecord struct {
	oldKeyID string
	isBoard  bool
	agentID  string
}

type Client struct {
	Endpoint   string
	Token      string
	HTTPClient *http.Client

	credMu          sync.RWMutex
	mintedTokens    map[string]string
	deliveredTokens map[string]bool
	supersededKeys  map[string]supersededKeyRecord
}

type HealthResponse struct {
	Status             string `json:"status"`
	DeploymentMode     string `json:"deploymentMode,omitempty"`
	DeploymentExposure string `json:"deploymentExposure,omitempty"`
}

func NewClient(endpoint, token string) (*Client, error) {
	if endpoint == "" {
		endpoint = "http://127.0.0.1:3100"
	}
	endpoint = strings.TrimRight(endpoint, "/")
	if _, err := url.ParseRequestURI(endpoint); err != nil {
		return nil, fmt.Errorf("invalid endpoint URL: %w", err)
	}
	return &Client{
		Endpoint: endpoint,
		Token:    token,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		mintedTokens:    make(map[string]string),
		deliveredTokens: make(map[string]bool),
		supersededKeys:  make(map[string]supersededKeyRecord),
	}, nil
}

func (c *Client) Health(ctx context.Context) (*HealthResponse, error) {
	var resp HealthResponse
	if err := c.paperclipRequest(ctx, http.MethodGet, "/api/health", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
