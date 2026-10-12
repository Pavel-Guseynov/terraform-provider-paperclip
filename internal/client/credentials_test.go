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
				// No expiry requested: the field must be an explicit null so Paperclip
				// mints a key that never expires instead of applying its 30-day default.
				if value, present := body["expiresAt"]; !present || value != nil {
					http.Error(w, "expiresAt must be an explicit null", http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(PaperclipBoardKey{
					ID:    "bk-min",
					Name:  "minimal",
					Token: "secret-min",
				})
				return
			}
			if body["name"] != "test-board-key" || body["requestedCompanyId"] != "comp-1" ||
				body["expiresAt"] != exp.Format(time.RFC3339) {
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
	var failDelete bool

	stringPtr := func(s string) *string { return &s }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			if failDelete {
				http.Error(w, "delete failure", http.StatusInternalServerError)
				return
			}
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

	// RegisterMintedKey, GetMintedKeyForName, AcknowledgeKeyDelivery, IsKeyDeliveryAcknowledged
	cli.RegisterMintedKey("m-bk-1", "tok-1", "board-test", true, "")
	if kID, tok, ok := cli.GetMintedKeyForName("board-test", true, ""); !ok || kID != "m-bk-1" || tok != "tok-1" {
		t.Fatalf("expected m-bk-1 and tok-1, got %s, %s, %v", kID, tok, ok)
	}
	if cli.IsKeyDeliveryAcknowledged("m-bk-1") {
		t.Fatal("expected delivery not acknowledged initially")
	}
	cli.AcknowledgeKeyDelivery("m-bk-1")
	if !cli.IsKeyDeliveryAcknowledged("m-bk-1") {
		t.Fatal("expected delivery to be acknowledged")
	}

	// Agent key variant
	cli.RegisterMintedKey("m-ak-1", "tok-ak-1", "agent-test", false, "ag-1")
	if kID, tok, ok := cli.GetMintedKeyForName("agent-test", false, "ag-1"); !ok || kID != "m-ak-1" || tok != "tok-ak-1" {
		t.Fatalf("expected m-ak-1 and tok-ak-1, got %s, %s, %v", kID, tok, ok)
	}
	if _, _, ok := cli.GetMintedKeyForName("nonexistent", true, ""); ok {
		t.Fatal("expected false for nonexistent key name")
	}

	// RecordDeliveredGeneration and IsGenerationDelivered
	if cli.IsGenerationDelivered("board-test", true, "", 1) {
		t.Fatal("expected generation 1 not delivered initially")
	}
	cli.RecordDeliveredGeneration("board-test", true, "", 1, "m-bk-1")
	if !cli.IsGenerationDelivered("board-test", true, "", 1) {
		t.Fatal("expected generation 1 to be delivered")
	}

	// ListBoardAPIKeys and ListAgentKeys
	bKeys, err := cli.ListBoardAPIKeys(ctx)
	if err != nil || len(bKeys) == 0 {
		t.Fatalf("expected active board keys, got %v, err=%v", bKeys, err)
	}
	aKeys, err := cli.ListAgentKeys(ctx, "ag-1")
	if err != nil || len(aKeys) == 0 {
		t.Fatalf("expected active agent keys, got %v, err=%v", aKeys, err)
	}
	if _, err := cli.ListAgentKeys(ctx, ""); err == nil {
		t.Fatal("expected error for empty agentID in ListAgentKeys")
	}

	// ReconcileUnconfirmedBoardKeys and ReconcileUnconfirmedAgentKeys
	cli.RegisterMintedKey("m-bk-unconf", "tok", "my-board-key", true, "")
	cli.RegisterMintedKey("m-ak-unconf", "tok", "my-agent-key", false, "ag-1")
	if err := cli.ReconcileUnconfirmedBoardKeys(ctx, "my-board-key", "different-bk-id"); err != nil {
		t.Fatalf("unexpected error in ReconcileUnconfirmedBoardKeys: %v", err)
	}
	if err := cli.ReconcileUnconfirmedAgentKeys(ctx, "ag-1", "my-agent-key", "different-ak-id"); err != nil {
		t.Fatalf("unexpected error in ReconcileUnconfirmedAgentKeys: %v", err)
	}
	cli.RegisterMintedKey("m-bk-fail", "tok", "my-board-key", true, "")
	cli.RegisterMintedKey("m-ak-fail", "tok", "my-agent-key", false, "ag-1")
	failDelete = true
	if err := cli.ReconcileUnconfirmedBoardKeys(ctx, "my-board-key", "different-bk-id"); err == nil {
		t.Fatal("expected error in ReconcileUnconfirmedBoardKeys when delete fails")
	}
	if err := cli.ReconcileUnconfirmedAgentKeys(ctx, "ag-1", "my-agent-key", "different-ak-id"); err == nil {
		t.Fatal("expected error in ReconcileUnconfirmedAgentKeys when delete fails")
	}
	failDelete = false

	// ClearMintedToken
	cli.ClearMintedToken("m-bk-1")
	if _, ok := cli.GetMintedToken("m-bk-1"); ok {
		t.Fatal("expected m-bk-1 to be cleared")
	}
	cli.ClearMintedToken("m-ak-1")
	if _, ok := cli.GetAgentIDForKey("m-ak-1"); ok {
		t.Fatal("expected m-ak-1 agentID to be cleared")
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

	// Connection failure branches for FindActive* and List/Reconcile
	failCli, _ := NewClient("http://127.0.0.1:1", "test-token")
	if _, err := failCli.FindActiveBoardKeyByName(ctx, "my-board-key"); err == nil {
		t.Fatal("expected error on connection failure in FindActiveBoardKeyByName")
	}
	if _, err := failCli.FindActiveAgentKeyByName(ctx, "ag-1", "my-agent-key"); err == nil {
		t.Fatal("expected error on connection failure in FindActiveAgentKeyByName")
	}
	if _, err := failCli.ListBoardAPIKeys(ctx); err == nil {
		t.Fatal("expected error on connection failure in ListBoardAPIKeys")
	}
	if _, err := failCli.ListAgentKeys(ctx, "ag-1"); err == nil {
		t.Fatal("expected error on connection failure in ListAgentKeys")
	}
	failCli.RegisterMintedKey("bk-unconfirmed-fail", "tok", "my-board-key", true, "")
	failCli.RegisterMintedKey("ak-unconfirmed-fail", "tok", "my-agent-key", false, "ag-1")
	if err := failCli.ReconcileUnconfirmedBoardKeys(ctx, "my-board-key", "active-bk-find"); err == nil {
		t.Fatal("expected error on connection failure in ReconcileUnconfirmedBoardKeys")
	}
	if err := failCli.ReconcileUnconfirmedAgentKeys(ctx, "ag-1", "my-agent-key", "active-ak-find"); err == nil {
		t.Fatal("expected error on connection failure in ReconcileUnconfirmedAgentKeys")
	}
	failCli.ClearMintedToken("bk-unconfirmed-fail")
	failCli.ClearMintedToken("ak-unconfirmed-fail")

	// ClearMintedToken restore branch: duplicate key for same name
	emptyCli, _ := NewClient("http://127.0.0.1:8080", "tok")
	emptyCli.RegisterMintedKey("bk-dup-1", "tok1", "same-board-name", true, "")
	emptyCli.RegisterMintedKey("bk-dup-2", "tok2", "same-board-name", true, "")
	emptyCli.ClearMintedToken("bk-dup-2")
	if k, _, ok := emptyCli.GetMintedKeyForName("same-board-name", true, ""); !ok || k != "bk-dup-1" {
		t.Fatalf("expected restored key bk-dup-1 for same-board-name, got %s, ok=%v", k, ok)
	}
	emptyCli.ClearMintedToken("bk-dup-1")

	// Test CleanupUnconfirmedKeys and CleanupAllUnconfirmedKeys with revoke failure injection
	var shouldFailRevoke bool
	cleanupServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if shouldFailRevoke && r.Method == http.MethodDelete {
			http.Error(w, "injected revocation error", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]bool{"success": true})
	}))
	defer cleanupServer.Close()

	cleanCli, _ := NewClient(cleanupServer.URL, "tok")
	cleanCli.RegisterMintedKey("bk-clean-1", "tok", "clean-bk", true, "")
	cleanCli.RegisterMintedKey("ak-clean-1", "tok", "clean-ak", false, "ag-clean")
	cleanCli.RegisterMintedKey("ak-clean-noag", "tok", "clean-ak2", false, "")
	cleanCli.agentKeyAgents["ak-clean-noag"] = "ag-fallback"
	cleanCli.RegisterMintedKey("bk-ack-1", "tok", "clean-ack", true, "")
	cleanCli.AcknowledgeKeyDelivery("bk-ack-1")

	// Inject revocation failure: must return error, emit structured diagnostic, and preserve record
	shouldFailRevoke = true
	if err := CleanupAllUnconfirmedKeys(ctx); err == nil {
		t.Fatal("expected error on failed CleanupAllUnconfirmedKeys")
	}
	if _, ok := cleanCli.GetMintedToken("bk-clean-1"); !ok {
		t.Fatal("expected bk-clean-1 to be retained in client records after revocation failure")
	}
	if _, ok := cleanCli.GetMintedToken("ak-clean-1"); !ok {
		t.Fatal("expected ak-clean-1 to be retained in client records after revocation failure")
	}

	// Recover with successful revocation: now must clear records
	shouldFailRevoke = false
	if err := CleanupAllUnconfirmedKeys(ctx); err != nil {
		t.Fatalf("unexpected error on successful CleanupAllUnconfirmedKeys: %v", err)
	}
	if _, ok := cleanCli.GetMintedToken("bk-clean-1"); ok {
		t.Fatal("expected bk-clean-1 to be cleared after successful CleanupAllUnconfirmedKeys")
	}
	if _, ok := cleanCli.GetMintedToken("ak-clean-1"); ok {
		t.Fatal("expected ak-clean-1 to be cleared after successful CleanupAllUnconfirmedKeys")
	}
}
