package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestClientBoardKeyMethods(t *testing.T) {
	ctx := context.Background()

	t.Run("CreateBoardKey authenticated with options", func(t *testing.T) {
		exp := time.Now().Add(24 * time.Hour).Truncate(time.Second)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/api/board-api-keys" {
				http.NotFound(w, r)
				return
			}
			if r.Header.Get("Authorization") != "Bearer test-token" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if body["name"] == "minimal" {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(PaperclipBoardKey{
					ID:    "bk-min",
					Name:  "minimal",
					Token: "secret-min",
				})
				return
			}
			if body["name"] != "test-board-key" || body["requestedCompanyId"] != "comp-1" {
				http.Error(w, "invalid payload", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(PaperclipBoardKey{
				ID:        "bk-123",
				Name:      "test-board-key",
				Token:     "secret-token-123",
				ExpiresAt: &exp,
			})
		}))
		defer server.Close()

		cli, _ := NewClient(server.URL, "test-token")
		bk, err := cli.CreateBoardKey(ctx, "test-board-key", &exp, "comp-1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if bk.ID != "bk-123" || bk.Token != "secret-token-123" {
			t.Fatalf("unexpected board key: %+v", bk)
		}

		// Minimal authenticated call (without expiresAt and requestedCompanyId)
		bkMin, err := cli.CreateBoardKey(ctx, "minimal", nil, "")
		if err != nil || bkMin == nil {
			t.Fatalf("unexpected error on minimal create: %v", err)
		}

		// Error from API
		cliBad, _ := NewClient(server.URL, "wrong-token")
		if _, err := cliBad.CreateBoardKey(ctx, "test-board-key", nil, ""); err == nil {
			t.Fatal("expected error with wrong token")
		}
	})

	t.Run("GetBoardKey scenarios", func(t *testing.T) {
		past := time.Now().Add(-1 * time.Hour)
		future := time.Now().Add(1 * time.Hour)
		revoked := "2026-01-01T00:00:00Z"

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.URL.Path != "/api/board-api-keys" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]PaperclipBoardKey{
				{ID: "active", Name: "Active Key", ExpiresAt: &future},
				{ID: "expired", Name: "Expired Key", ExpiresAt: &past},
				{ID: "revoked", Name: "Revoked Key", RevokedAt: &revoked},
			})
		}))
		defer server.Close()

		cli, _ := NewClient(server.URL, "test-token")

		// Empty keyID validation
		if _, err := cli.GetBoardKey(ctx, "  "); err == nil {
			t.Fatal("expected error for empty keyID")
		}

		// Active key
		k, err := cli.GetBoardKey(ctx, "active")
		if err != nil || k.ID != "active" {
			t.Fatalf("expected active key, got %v, err: %v", k, err)
		}

		// Expired key returns ErrNotFound
		if _, err := cli.GetBoardKey(ctx, "expired"); err != ErrNotFound {
			t.Fatalf("expected ErrNotFound for expired key, got %v", err)
		}

		// Revoked key returns ErrNotFound
		if _, err := cli.GetBoardKey(ctx, "revoked"); err != ErrNotFound {
			t.Fatalf("expected ErrNotFound for revoked key, got %v", err)
		}

		// Non-existent key returns ErrNotFound
		if _, err := cli.GetBoardKey(ctx, "does-not-exist"); err != ErrNotFound {
			t.Fatalf("expected ErrNotFound for absent key, got %v", err)
		}

		// API error
		badCli, _ := NewClient("http://127.0.0.1:0", "test-token")
		if _, err := badCli.GetBoardKey(ctx, "active"); err == nil {
			t.Fatal("expected network error")
		}
	})

	t.Run("RevokeBoardKey scenarios", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodDelete {
				http.NotFound(w, r)
				return
			}
			switch r.URL.Path {
			case "/api/board-api-keys/active":
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{}`))
			case "/api/board-api-keys/absent":
				http.NotFound(w, r)
			default:
				http.Error(w, "server failure", http.StatusInternalServerError)
			}
		}))
		defer server.Close()

		cli, _ := NewClient(server.URL, "test-token")

		// Empty keyID validation
		if err := cli.RevokeBoardKey(ctx, "  "); err == nil {
			t.Fatal("expected error for empty keyID")
		}

		// Success
		if err := cli.RevokeBoardKey(ctx, "active"); err != nil {
			t.Fatalf("unexpected error revoking active key: %v", err)
		}

		// 404 is treated as already revoked (nil error)
		if err := cli.RevokeBoardKey(ctx, "absent"); err != nil {
			t.Fatalf("expected nil error for absent key, got %v", err)
		}

		// 500 error
		if err := cli.RevokeBoardKey(ctx, "error"); err == nil {
			t.Fatal("expected error on 500")
		}
	})
}

func TestClientAgentKeyMethods(t *testing.T) {
	ctx := context.Background()

	t.Run("CreateAgentKey scenarios", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != "/api/agents/ag-1/keys" {
				http.NotFound(w, r)
				return
			}
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(PaperclipAgentKey{
				ID:    "ak-1",
				Name:  body["name"].(string),
				Token: "agent-secret-tok",
			})
		}))
		defer server.Close()

		cli, _ := NewClient(server.URL, "test-token")

		// Empty agentID validation
		if _, err := cli.CreateAgentKey(ctx, "  ", "name", nil); err == nil {
			t.Fatal("expected error for empty agentID")
		}

		// With scope
		scope := &AgentKeyScope{
			Kind: "standard",
		}
		k, err := cli.CreateAgentKey(ctx, "ag-1", "my-key", scope)
		if err != nil || k.ID != "ak-1" || k.AgentID != "ag-1" {
			t.Fatalf("unexpected result: %+v, err: %v", k, err)
		}

		// Without scope
		k2, err := cli.CreateAgentKey(ctx, "ag-1", "my-key-2", nil)
		if err != nil || k2.ID != "ak-1" {
			t.Fatalf("unexpected result: %+v, err: %v", k2, err)
		}

		// Network error
		badCli, _ := NewClient("http://127.0.0.1:0", "test-token")
		if _, err := badCli.CreateAgentKey(ctx, "ag-1", "name", nil); err == nil {
			t.Fatal("expected network error")
		}
	})

	t.Run("GetAgentKey scenarios", func(t *testing.T) {
		revoked := "2026-01-01T00:00:00Z"
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet || r.URL.Path != "/api/agents/ag-1/keys" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]PaperclipAgentKey{
				{ID: "ak-active", Name: "Active"},
				{ID: "ak-revoked", Name: "Revoked", RevokedAt: &revoked},
			})
		}))
		defer server.Close()

		cli, _ := NewClient(server.URL, "test-token")

		// Validation errors
		if _, err := cli.GetAgentKey(ctx, "", "ak-1"); err == nil {
			t.Fatal("expected error for empty agentID")
		}
		if _, err := cli.GetAgentKey(ctx, "ag-1", ""); err == nil {
			t.Fatal("expected error for empty keyID")
		}

		// Found active key
		k, err := cli.GetAgentKey(ctx, "ag-1", "ak-active")
		if err != nil || k.ID != "ak-active" || k.AgentID != "ag-1" {
			t.Fatalf("unexpected key: %+v, err: %v", k, err)
		}

		// Revoked key returns ErrNotFound
		if _, err := cli.GetAgentKey(ctx, "ag-1", "ak-revoked"); err != ErrNotFound {
			t.Fatalf("expected ErrNotFound for revoked key, got %v", err)
		}

		// Absent key returns ErrNotFound
		if _, err := cli.GetAgentKey(ctx, "ag-1", "ak-missing"); err != ErrNotFound {
			t.Fatalf("expected ErrNotFound for absent key, got %v", err)
		}

		// Network error
		badCli, _ := NewClient("http://127.0.0.1:0", "test-token")
		if _, err := badCli.GetAgentKey(ctx, "ag-1", "ak-active"); err == nil {
			t.Fatal("expected network error")
		}
	})

	t.Run("RevokeAgentKey scenarios", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodDelete {
				http.NotFound(w, r)
				return
			}
			switch r.URL.Path {
			case "/api/agents/ag-1/keys/ak-active":
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{}`))
			case "/api/agents/ag-1/keys/ak-absent":
				http.NotFound(w, r)
			default:
				http.Error(w, "server failure", http.StatusInternalServerError)
			}
		}))
		defer server.Close()

		cli, _ := NewClient(server.URL, "test-token")

		// Validation errors
		if err := cli.RevokeAgentKey(ctx, "", "ak-1"); err == nil {
			t.Fatal("expected error for empty agentID")
		}
		if err := cli.RevokeAgentKey(ctx, "ag-1", ""); err == nil {
			t.Fatal("expected error for empty keyID")
		}

		// Success
		if err := cli.RevokeAgentKey(ctx, "ag-1", "ak-active"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// 404 returns nil
		if err := cli.RevokeAgentKey(ctx, "ag-1", "ak-absent"); err != nil {
			t.Fatalf("expected nil on 404, got %v", err)
		}

		// 500 error
		if err := cli.RevokeAgentKey(ctx, "ag-1", "ak-error"); err == nil {
			t.Fatal("expected error on 500")
		}
	})
}

func TestClientCredentialRegistry(t *testing.T) {
	ctx := context.Background()
	revokedBoardKeys := []string{}
	revokedAgentKeys := []string{}

	stringPtr := func(s string) *string { return &s }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			if len(r.URL.Path) > len("/api/board-api-keys/") && r.URL.Path[:len("/api/board-api-keys/")] == "/api/board-api-keys/" {
				keyID := r.URL.Path[len("/api/board-api-keys/"):]
				revokedBoardKeys = append(revokedBoardKeys, keyID)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{}`))
				return
			}
			if len(r.URL.Path) > len("/api/agents/ag-1/keys/") && r.URL.Path[:len("/api/agents/ag-1/keys/")] == "/api/agents/ag-1/keys/" {
				keyID := r.URL.Path[len("/api/agents/ag-1/keys/"):]
				revokedAgentKeys = append(revokedAgentKeys, keyID)
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{}`))
				return
			}
		}
		if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/board-api-keys") {
			w.Header().Set("Content-Type", "application/json")
			past := time.Now().Add(-1 * time.Hour)
			_ = json.NewEncoder(w).Encode([]PaperclipBoardKey{
				{ID: "active-bk-find", Name: "my-board-key"},
				{ID: "expired-bk-find", Name: "my-expired-key", ExpiresAt: &past},
				{ID: "revoked-bk-find", Name: "my-revoked-key", RevokedAt: stringPtr("2026-01-01T00:00:00Z")},
			})
			return
		}
		if r.Method == http.MethodGet && (r.URL.Path == "/api/agents/ag-1/keys" || r.URL.Path == "/api/agents/_/keys") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]PaperclipAgentKey{
				{ID: "active-ak-find", Name: "my-agent-key", AgentID: "ag-1"},
				{ID: "empty-agent-id-key", Name: "my-empty-agent-id-key", AgentID: ""},
				{ID: "revoked-ak-find", Name: "my-revoked-key", RevokedAt: stringPtr("2026-01-01T00:00:00Z")},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	cli, _ := NewClient(server.URL, "test-token")

	// RecordSupersededKey guards
	cli.RecordSupersededKey("k1", "", true, "")
	cli.RecordSupersededKey("k1", "k1", true, "")
	cli.RecordSupersededKey("new-ak", "old-ak", false, "ag-test")

	// RegisterMintedToken and GetMintedToken
	cli.RegisterMintedToken("bk-1", "token-secret-1")
	tok, ok := cli.GetMintedToken("bk-1")
	if !ok || tok != "token-secret-1" {
		t.Fatalf("expected token-secret-1, got %s (ok=%v)", tok, ok)
	}

	// RegisterMintedAgentKey and GetAgentIDForKey
	cli.RegisterMintedAgentKey("ag-1", "ak-1", "agent-token-1")
	tok, ok = cli.GetMintedToken("ak-1")
	if !ok || tok != "agent-token-1" {
		t.Fatalf("expected agent-token-1, got %s", tok)
	}
	agID, ok := cli.GetAgentIDForKey("ak-1")
	if !ok || agID != "ag-1" {
		t.Fatalf("expected ag-1, got %s (ok=%v)", agID, ok)
	}

	// MarkTokenDelivered directly
	cli.MarkTokenDelivered("bk-1")
	if !cli.IsTokenDelivered("bk-1") {
		t.Fatal("expected bk-1 to be marked delivered")
	}

	// GetSupersededKey
	cli.RecordSupersededKey("new-1", "old-1", true, "")
	if old, ok := cli.GetSupersededKey("new-1"); !ok || old != "old-1" {
		t.Fatalf("expected old-1, got %s (ok=%v)", old, ok)
	}
	if _, ok := cli.GetSupersededKey("non-existent"); ok {
		t.Fatal("expected false for nonexistent superseded key")
	}

	// SetActiveEphemeralKey, RecordConsumerFailure, and IsDeliveryFailed
	cli.SetActiveEphemeralKey("eph-1")
	if cli.IsDeliveryFailed("eph-1") {
		t.Fatal("expected delivery not failed initially")
	}
	cli.RecordConsumerFailure()
	if !cli.IsDeliveryFailed("eph-1") {
		t.Fatal("expected delivery to be marked failed")
	}
	cli.SetActiveEphemeralKey("")
	cli.RecordConsumerFailure()

	// RecordConsumerFailure with agent key
	cli.RegisterMintedAgentKey("ag-1", "eph-ak-1", "token-ak-fail")
	cli.SetActiveEphemeralKey("eph-ak-1")
	cli.RecordConsumerFailure()
	if !cli.IsDeliveryFailed("eph-ak-1") {
		t.Fatal("expected eph-ak-1 delivery to be marked failed")
	}

	// ClearMintedToken
	cli.SetActiveEphemeralKey("bk-1")
	cli.ClearMintedToken("bk-1")
	if _, ok := cli.GetMintedToken("bk-1"); ok {
		t.Fatal("expected bk-1 to be cleared")
	}
	cli.SetActiveEphemeralKey("other-key")
	cli.ClearMintedToken("ak-1")
	if _, ok := cli.GetAgentIDForKey("ak-1"); ok {
		t.Fatal("expected ak-1 agentID to be cleared")
	}


	// FindActiveBoardKeyByName
	if _, err := cli.FindActiveBoardKeyByName(ctx, ""); err == nil {
		t.Fatal("expected error for empty name in FindActiveBoardKeyByName")
	}
	activeBK, err := cli.FindActiveBoardKeyByName(ctx, "my-board-key")
	if err != nil || activeBK.ID != "active-bk-find" {
		t.Fatalf("unexpected result: %v, err=%v", activeBK, err)
	}
	if _, err := cli.FindActiveBoardKeyByName(ctx, "my-expired-key"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound for expired key, got %v", err)
	}
	if _, err := cli.FindActiveBoardKeyByName(ctx, "my-revoked-key"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound for revoked key, got %v", err)
	}
	if _, err := cli.FindActiveBoardKeyByName(ctx, "non-existent"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound for nonexistent key, got %v", err)
	}

	// FindActiveAgentKeyByName
	if _, err := cli.FindActiveAgentKeyByName(ctx, "", "my-agent-key"); err == nil {
		t.Fatal("expected error for empty agentID in FindActiveAgentKeyByName")
	}
	if _, err := cli.FindActiveAgentKeyByName(ctx, "ag-1", ""); err == nil {
		t.Fatal("expected error for empty name in FindActiveAgentKeyByName")
	}
	activeAK, err := cli.FindActiveAgentKeyByName(ctx, "ag-1", "my-agent-key")
	if err != nil || activeAK.ID != "active-ak-find" {
		t.Fatalf("unexpected result: %v, err=%v", activeAK, err)
	}
	emptyAK, err := cli.FindActiveAgentKeyByName(ctx, "ag-1", "my-empty-agent-id-key")
	if err != nil || emptyAK.AgentID != "ag-1" {
		t.Fatalf("expected empty-agent-id-key to have agentID set to ag-1, got %v, err=%v", emptyAK, err)
	}
	if _, err := cli.FindActiveAgentKeyByName(ctx, "ag-1", "my-revoked-key"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound for revoked agent key, got %v", err)
	}
	if _, err := cli.FindActiveAgentKeyByName(ctx, "ag-1", "non-existent"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound for nonexistent agent key, got %v", err)
	}

	// Connection failure branches for FindActive*
	failCli, _ := NewClient("http://127.0.0.1:1", "test-token")
	if _, err := failCli.FindActiveBoardKeyByName(ctx, "my-board-key"); err == nil {
		t.Fatal("expected error on connection failure in FindActiveBoardKeyByName")
	}
	if _, err := failCli.FindActiveAgentKeyByName(ctx, "ag-1", "my-agent-key"); err == nil {
		t.Fatal("expected error on connection failure in FindActiveAgentKeyByName")
	}
}
