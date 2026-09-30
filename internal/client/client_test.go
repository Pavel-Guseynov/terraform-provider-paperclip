package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewClient(t *testing.T) {
	c, err := NewClient("", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Endpoint != "http://127.0.0.1:3100" {
		t.Fatalf("expected default endpoint, got: %s", c.Endpoint)
	}
	if c.Token != "" {
		t.Fatalf("expected empty token, got: %s", c.Token)
	}

	c2, err := NewClient("https://paperclip.example.com/", "token-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c2.Endpoint != "https://paperclip.example.com" {
		t.Fatalf("expected trailing slash trimmed, got: %s", c2.Endpoint)
	}
	if c2.Token != "token-123" {
		t.Fatalf("expected token-123, got: %s", c2.Token)
	}
}

func TestClientHealth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/health" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":             "ok",
			"deploymentMode":     "authenticated",
			"deploymentExposure": "private",
		})
	}))
	defer server.Close()

	c, err := NewClient(server.URL, "test-token")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	health, err := c.Health(context.Background())
	if err != nil {
		t.Fatalf("Health failed: %v", err)
	}
	if health.Status != "ok" {
		t.Fatalf("expected status ok, got %q", health.Status)
	}
	if health.DeploymentMode != "authenticated" {
		t.Fatalf("expected deploymentMode authenticated, got %q", health.DeploymentMode)
	}
}

func TestClientErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/404":
			http.Error(w, "not found", http.StatusNotFound)
		case "/api/409":
			http.Error(w, "conflict", http.StatusConflict)
		case "/api/500":
			http.Error(w, "internal server error detail with SECRET_KEY_123", http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	c, _ := NewClient(server.URL, "token")

	// 404 maps to ErrNotFound
	err := c.paperclipRequest(context.Background(), http.MethodGet, "/api/404", nil, nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}

	// 409 maps to ErrConflict
	err = c.paperclipRequest(context.Background(), http.MethodGet, "/api/409", nil, nil)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict, got: %v", err)
	}

	// Normal request includes error body
	err = c.paperclipRequest(context.Background(), http.MethodGet, "/api/500", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "internal server error detail") {
		t.Fatalf("expected detailed error, got: %v", err)
	}

	// Secret request redacts error body to prevent leaking sensitive credentials
	err = c.paperclipSecretRequest(context.Background(), http.MethodGet, "/api/500", nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "SECRET_KEY_123") {
		t.Fatalf("secret leaked in secret request error: %v", err)
	}
	if err.Error() != "paperclip http 500" {
		t.Fatalf("expected redacted error 'paperclip http 500', got: %v", err)
	}
}
