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

type mintedKeyRecord struct {
	keyID   string
	token   string
	name    string
	isBoard bool
	agentID string
}

type Client struct {
	Endpoint      string
	Token         string
	HTTPClient    *http.Client
	managedOwners map[string]string

	credMu                 sync.RWMutex
	mintedTokens           map[string]string // keyID -> token
	deliveredTokens        map[string]bool   // keyID -> true (delivery confirmed)
	supersededKeys         map[string]supersededKeyRecord
	agentKeyAgents         map[string]string // keyID -> agentID
	mintedKeys             map[string]mintedKeyRecord
	mintedKeysByName       map[string]string // lookupKey -> keyID
	acknowledgedDeliveries map[string]bool   // keyID -> true
	deliveredGenerations   map[string]int64  // lookupKey -> generation
}

type HealthResponse struct {
	Status         string   `json:"status"`
	HiddenSettings []string `json:"hiddenSettings,omitempty"`
	Cloud          *struct {
		Managed   bool   `json:"managed"`
		ManagedBy string `json:"managedBy"`
	} `json:"cloud,omitempty"`
	DeploymentMode     string `json:"deploymentMode,omitempty"`
	DeploymentExposure string `json:"deploymentExposure,omitempty"`
}

func NewClient(endpoint, token string) (*Client, error) {
	if endpoint == "" {
		endpoint = "http://127.0.0.1:3100"
	}
	endpoint = strings.TrimRight(endpoint, "/")
	parsed, err := url.ParseRequestURI(endpoint)
	if err != nil || (!strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https")) || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(endpoint, "#") {
		return nil, errors.New("endpoint must be an absolute HTTP(S) URL with a host and without user information, a query, or a fragment")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	endpoint = parsed.String()
	c := &Client{
		Endpoint: endpoint,
		Token:    token,
		HTTPClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		mintedTokens:           make(map[string]string),
		deliveredTokens:        make(map[string]bool),
		supersededKeys:         make(map[string]supersededKeyRecord),
		agentKeyAgents:         make(map[string]string),
		mintedKeys:             make(map[string]mintedKeyRecord),
		mintedKeysByName:       make(map[string]string),
		acknowledgedDeliveries: make(map[string]bool),
		deliveredGenerations:   make(map[string]int64),
	}
	registerActiveClient(c)
	return c, nil
}

var (
	activeClientsMu sync.Mutex
	activeClients   []*Client
)

func registerActiveClient(c *Client) {
	activeClientsMu.Lock()
	defer activeClientsMu.Unlock()
	activeClients = append(activeClients, c)
}

func CleanupAllUnconfirmedKeys(ctx context.Context) error {
	activeClientsMu.Lock()
	clients := make([]*Client, len(activeClients))
	copy(clients, activeClients)
	activeClientsMu.Unlock()

	var errs []error
	for _, c := range clients {
		if err := c.CleanupUnconfirmedKeys(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (c *Client) Health(ctx context.Context) (*HealthResponse, error) {
	var resp HealthResponse
	if err := c.paperclipRequest(ctx, http.MethodGet, "/api/health", nil, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}
