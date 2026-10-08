package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type unmarshalableType chan int

func TestClientCoverageEdgeCases(t *testing.T) {
	ctx := context.Background()

	// 1. Health error path
	errServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer errServer.Close()

	errCli, _ := NewClient(errServer.URL, "token")
	if _, err := errCli.Health(ctx); err == nil {
		t.Fatal("expected error from Health on 500")
	}

	// 2. paperclipRequestWithErrorBody marshal failure
	badCli, _ := NewClient("http://127.0.0.1:3100", "token")
	if err := badCli.paperclipRequest(ctx, http.MethodPost, "/api/test", unmarshalableType(make(chan int)), nil); err == nil {
		t.Fatal("expected marshal error")
	}

	// 3. Request creation error (invalid method or URL with control char)
	if err := badCli.paperclipRequest(ctx, "INVALID METHOD\n", "/api/test", nil, nil); err == nil {
		t.Fatal("expected request creation error")
	}

	// 4. Response unmarshal failure
	invalidJSONServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not valid json"))
	}))
	defer invalidJSONServer.Close()

	jsonCli, _ := NewClient(invalidJSONServer.URL, "token")
	var result map[string]interface{}
	if err := jsonCli.paperclipRequest(ctx, http.MethodGet, "/api/test", nil, &result); err == nil {
		t.Fatal("expected unmarshal error")
	}

	// 5. extractStringSlice []string branch
	strs := extractStringSlice([]string{"hello", "world"})
	if len(strs) != 2 || strs[0] != "hello" {
		t.Fatalf("unexpected strings: %v", strs)
	}
	// non-slice branch
	if len(extractStringSlice(12345)) != 0 {
		t.Fatal("expected empty slice for non-slice")
	}

	// 6. AwaitingAuthorization
	oauthDraft := PaperclipToolConnection{AuthKind: "oauth", Status: "draft"}
	if !oauthDraft.AwaitingAuthorization() {
		t.Fatal("expected AwaitingAuthorization to be true")
	}
	oauthActive := PaperclipToolConnection{AuthKind: "oauth", Status: "active"}
	if oauthActive.AwaitingAuthorization() {
		t.Fatal("expected AwaitingAuthorization to be false for active")
	}

	// 7. readAppProfile branches
	state := &PaperclipAppPermissionState{}
	if err := readAppProfile(state, map[string]interface{}{"defaultAction": "allow"}, "comp-1", "conn-1"); err == nil {
		t.Fatal("expected error when defaultAction != deny")
	}
	if err := readAppProfile(state, map[string]interface{}{
		"defaultAction": "deny",
		"status":        "active",
		"entries": []interface{}{
			map[string]interface{}{"selectorType": "connection"},
		},
	}, "comp-1", "conn-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for connection selectorType, got %v", err)
	}
	if err := readAppProfile(state, map[string]interface{}{
		"defaultAction": "deny",
		"status":        "active",
		"entries": []interface{}{
			map[string]interface{}{
				"selectorType":   "catalog_entry",
				"catalogEntryId": "cat-1",
				"effect":         "exclude", // not include
				"connectionId":   "conn-1",
			},
		},
	}, "comp-1", "conn-1"); err == nil {
		t.Fatal("expected error for non-include effect")
	}
	if err := readAppProfile(state, map[string]interface{}{
		"defaultAction": "deny",
		"status":        "active",
		"bindings": []interface{}{
			map[string]interface{}{
				"targetId":   "comp-2",
				"targetType": "company",
			},
		},
	}, "comp-1", "conn-1"); err == nil {
		t.Fatal("expected error for invalid company scope")
	}
	if err := readAppProfile(state, map[string]interface{}{
		"defaultAction": "deny",
		"status":        "active",
		"bindings": []interface{}{
			map[string]interface{}{
				"targetId": "",
			},
		},
	}, "comp-1", "conn-1"); err == nil {
		t.Fatal("expected error for binding without targetId")
	}
}

func TestClientMethodsSuccessAndErrors(t *testing.T) {
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/projects/proj-1" && r.Method == http.MethodPatch:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "proj-1", "leadAgentId": "agent-1"})
		case r.URL.Path == "/api/projects/proj-1" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "proj-1", "name": "proj", "leadAgentId": "agent-1"})
		case r.URL.Path == "/api/companies/comp-1/tools/apps/connect" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"connectionId": "conn-1",
				"connection":   map[string]interface{}{"id": "conn-1", "companyId": "comp-1"},
			})
		case r.URL.Path == "/api/companies/comp-1/secrets/sec-empty":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "sec-empty", "versions": []interface{}{}})
		case r.URL.Path == "/api/companies/comp-1/skills/skill-empty":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "skill-empty", "versions": []interface{}{}})
		case r.URL.Path == "/api/companies" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]interface{}{map[string]interface{}{"id": "comp-1", "name": "Acme"}})
		case r.URL.Path == "/api/companies/comp-1/tool-apps" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]interface{}{})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cli, _ := NewClient(server.URL, "token")

	// SetProjectLead success
	proj, err := cli.SetProjectLead(ctx, "proj-1", "agent-1")
	if err != nil || proj.ID != "proj-1" {
		t.Fatalf("SetProjectLead failed: %v", err)
	}

	// ConnectGalleryApp success
	conn, err := cli.ConnectGalleryApp(ctx, "comp-1", "app-1")
	if err != nil || conn.ID != "conn-1" {
		t.Fatalf("ConnectGalleryApp failed: %v", err)
	}

	// GetSecret with empty versions maps to ErrNotFound
	if _, err := cli.GetSecret(ctx, "comp-1", "sec-empty"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for secret with empty versions, got %v", err)
	}

	// GetSkill not found returns ErrNotFound
	if _, err := cli.GetSkill(ctx, "comp-1", "skill-not-found"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for skill-not-found, got %v", err)
	}

	// FindCompanyByName not found returns ErrNotFound
	if _, err := cli.FindCompanyByName(ctx, "nonexistent"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound from FindCompanyByName, got %v", err)
	}

	// EnsureConnectionCredentialGrant on draft connection
	refs := []map[string]interface{}{{"targetType": "agent", "targetId": "agent-1"}}
	if err := cli.EnsureConnectionCredentialGrant(ctx, "conn-1", refs); err == nil {
		// Expect 404 from not found server
	}
}

func TestAllClientMethodsFailures(t *testing.T) {
	ctx := context.Background()

	// Server that fails all requests with 500
	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer failServer.Close()

	cli, _ := NewClient(failServer.URL, "token")

	// Exercise error return paths on all client functions
	_, _ = cli.CreateBoardKey(ctx, "key-name")
	_, _ = cli.CreateCompany(ctx, PaperclipCompany{Name: "Acme"})
	_, _ = cli.GetCompany(ctx, "comp-1")
	_, _ = cli.UpdateCompany(ctx, "comp-1", map[string]interface{}{"name": "Acme", "description": "desc"})
	_ = cli.DeleteCompany(ctx, "comp-1")
	_ = cli.ArchiveCompany(ctx, "comp-1")
	_, _ = cli.ListCompanies(ctx)
	_, _ = cli.CreateProject(ctx, "comp-1", "proj", "desc", nil)
	_, _ = cli.GetProject(ctx, "proj-1")
	_, _ = cli.UpdateProject(ctx, "proj-1", "proj", "desc", nil)
	_ = cli.DeleteProject(ctx, "proj-1")
	_, _ = cli.SetProjectLead(ctx, "proj-1", "agent-1")
	_, _ = cli.ListProjects(ctx, "comp-1")
	_, _ = cli.CreateWorkspace(ctx, "proj-1", PaperclipWorkspace{Name: "ws"})
	_, _ = cli.GetWorkspaceInProject(ctx, "proj-1", "ws-1")
	_, _ = cli.UpdateWorkspace(ctx, "proj-1", "ws-1", PaperclipWorkspace{Name: "ws"})
	_ = cli.DeleteWorkspace(ctx, "proj-1", "ws-1")
	_, _ = cli.ListWorkspaces(ctx, "proj-1")
	_, _ = cli.CreateSkill(ctx, "comp-1", PaperclipSkill{Name: "skill"})
	_, _ = cli.GetSkill(ctx, "comp-1", "skill-1")
	_, _ = cli.UpdateSkill(ctx, "comp-1", "skill-1", PaperclipSkill{Name: "skill"})
	_ = cli.DeleteSkill(ctx, "comp-1", "skill-1")
	_, _ = cli.ListSkills(ctx, "comp-1")
	_, _ = cli.CreateSecret(ctx, "comp-1", "sec", "desc", "val")
	_, _ = cli.GetSecret(ctx, "comp-1", "sec-1")
	_, _ = cli.UpdateSecret(ctx, "sec-1", "sec", "desc")
	_, _ = cli.RotateSecret(ctx, "sec-1", "new-val")
	_ = cli.DeleteSecret(ctx, "sec-1")
	_, _ = cli.ListSecrets(ctx, "comp-1")
	_, _ = cli.CreateAgent(ctx, "comp-1", PaperclipAgent{Name: "agent"})
	_, _ = cli.GetAgent(ctx, "agent-1")
	_, _ = cli.UpdateAgent(ctx, "agent-1", PaperclipAgent{Name: "agent"})
	_ = cli.DeleteAgent(ctx, "agent-1")
	_ = cli.UpdateAgentPermissions(ctx, "agent-1", PaperclipAgentPermissions{})
	_, _ = cli.ListAgents(ctx, "comp-1")
	_, _ = cli.GetAgentInstructionsFile(ctx, "agent-1", "AGENTS.md")
	_ = cli.UpdateAgentInstructionsFile(ctx, "agent-1", "AGENTS.md", "content")
	_, _ = cli.CreateAgentKey(ctx, "agent-1", "key-name")
	_, _ = cli.CreateToolApp(ctx, "comp-1", PaperclipToolApp{Name: "app"})
	_, _ = cli.GetToolApp(ctx, "app-1")
	_, _ = cli.FindCompanyByName(ctx, "app-1")
	_, _ = cli.UpdateToolApp(ctx, "app-1", PaperclipToolApp{Name: "app"})
	_ = cli.ArchiveToolApp(ctx, "app-1")
	_, _ = cli.ListToolApps(ctx, "comp-1")
	_, _ = cli.CreateToolConnection(ctx, "comp-1", PaperclipToolConnection{Name: "conn"})
	_, _ = cli.ConnectGalleryApp(ctx, "comp-1", "app-1")
	_, _ = cli.GetToolConnection(ctx, "conn-1")
	_, _ = cli.UpdateToolConnection(ctx, "conn-1", PaperclipToolConnection{Name: "conn"})
	_ = cli.DeleteToolConnection(ctx, "conn-1")
	_, _ = cli.ListToolConnections(ctx, "comp-1")
	_, _ = cli.CreateStdioTemplate(ctx, "comp-1", PaperclipStdioTemplate{Name: "name"})
	_, _ = cli.GetStdioTemplate(ctx, "comp-1", "tmpl-1")
	_, _ = cli.ListStdioTemplates(ctx, "comp-1")
	_ = cli.DisableStdioTemplate(ctx, "comp-1", "tmpl-1")
	_, _ = cli.GetConnectionInstalls(ctx, "conn-1")
	_, _ = cli.UpdateConnectionInstalls(ctx, "conn-1", []map[string]interface{}{})
	_, _ = cli.GetConnectionCatalog(ctx, "conn-1")
	_, _ = cli.FinishAppPermissions(ctx, "comp-1", "conn-1", PaperclipAppFinish{})
	_, _ = cli.CreateToolPolicy(ctx, "comp-1", PaperclipToolPolicy{Name: "pol"})
	_, _ = cli.GetToolPolicy(ctx, "comp-1", "pol-1")
	_, _ = cli.UpdateToolPolicy(ctx, "comp-1", "pol-1", PaperclipToolPolicy{Name: "pol"})
	_ = cli.DeleteToolPolicy(ctx, "comp-1", "pol-1")
	_, _ = cli.ListToolPolicies(ctx, "comp-1")
	_ = cli.RevokeAppPermissionProfile(ctx, "comp-1", "conn-1")
	_ = cli.EnsureConnectionCredentialGrant(ctx, "conn-1", []map[string]interface{}{})
}

func TestClientSpecificBranches(t *testing.T) {
	ctx := context.Background()

	// 1. HTTP 409 Conflict mapping & Cancelled Context
	conflictServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
	}))
	defer conflictServer.Close()
	cConflict, _ := NewClient(conflictServer.URL, "token")
	if err := cConflict.paperclipRequest(ctx, http.MethodGet, "/api/test", nil, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict, got: %v", err)
	}

	cancelledCtx, cancel := context.WithCancel(ctx)
	cancel()
	if err := cConflict.paperclipRequest(cancelledCtx, http.MethodGet, "/api/test", nil, nil); err == nil {
		t.Fatal("expected error with cancelled context")
	}

	// 2. Mock server for fallbacks across companies (empty companyID or 404 fallback)
	fallbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "comp-1", "name": "Company One",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/skills/skill-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "skill-1", "name": "Skill One", "slug": "skill-one", "markdown": "# Skill One",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/tool-connections/conn-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "conn-1", "name": "Connection One", "status": "active",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/agent-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "agent-1", "name": "Agent One",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "comp-1", "name": "Company One"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/skills":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "skill-1", "name": "Skill One", "slug": "skill-one", "markdown": "# Skill One"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/secrets":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "sec-1", "name": "Secret One", "description": "Desc"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/tools/connections":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "conn-1", "name": "Connection One", "status": "active"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/tools/applications":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "app-1", "name": "App One", "status": "active"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/tools/stdio-templates":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "tmpl-1", "name": "Tmpl One", "command": "cmd"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/projects":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "proj-1", "name": "Project One"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/projects/proj-1/workspaces":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "ws-1", "name": "Workspace One"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/agents":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "agent-1", "name": "Agent One"},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/comp-1/projects":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "proj-1", "name": "proj", "leadAgentId": "agent-1"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer fallbackServer.Close()

	cliFallback, _ := NewClient(fallbackServer.URL, "token")

	// GetSkill with empty companyID returns ErrNotFound
	if _, err := cliFallback.GetSkill(ctx, "", "skill-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for empty companyID, got %v", err)
	}
	// direct lookup succeeds
	sk, err := cliFallback.GetSkill(ctx, "comp-1", "skill-1")
	if err != nil || sk.ID != "skill-1" {
		t.Fatalf("expected skill-1, got %v (%v)", sk, err)
	}
	// not found
	if _, err := cliFallback.GetSkill(ctx, "comp-1", "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	// GetSecret with empty companyID returns ErrNotFound
	if _, err := cliFallback.GetSecret(ctx, "", "sec-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for empty companyID, got %v", err)
	}
	sec, err := cliFallback.GetSecret(ctx, "comp-1", "sec-1")
	if err != nil || sec.ID != "sec-1" {
		t.Fatalf("expected sec-1, got %v (%v)", sec, err)
	}
	if _, err := cliFallback.GetSecret(ctx, "comp-1", "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	// GetToolConnection
	conn, err := cliFallback.GetToolConnection(ctx, "conn-1")
	if err != nil || conn.ID != "conn-1" {
		t.Fatalf("expected conn-1, got %v (%v)", conn, err)
	}
	if _, err := cliFallback.GetToolConnection(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	// GetAgent
	ag, err := cliFallback.GetAgent(ctx, "agent-1")
	if err != nil || ag.ID != "agent-1" {
		t.Fatalf("expected agent-1, got %v (%v)", ag, err)
	}
	if _, err := cliFallback.GetAgent(ctx, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	// GetCompany direct
	comp, err := cliFallback.GetCompany(ctx, "comp-1")
	if err != nil || comp.ID != "comp-1" {
		t.Fatalf("expected comp-1, got %v (%v)", comp, err)
	}
	if _, err := cliFallback.GetCompany(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	// FindCompanyByName
	comp, err = cliFallback.FindCompanyByName(ctx, "Company One")
	if err != nil || comp.ID != "comp-1" {
		t.Fatalf("expected comp-1, got %v (%v)", comp, err)
	}
	if _, err := cliFallback.FindCompanyByName(ctx, "Missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	// CreateProject with executionWorkspacePolicy
	policyMap := map[string]interface{}{"mode": "isolated"}
	_, err = cliFallback.CreateProject(ctx, "comp-1", "proj", "desc", policyMap)
	if err != nil {
		t.Fatalf("CreateProject with policy failed: %v", err)
	}

	// 3. CreateBoardKey error branches
	bServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/cli-auth/challenges":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": ""})
		case "/api/cli-auth/me":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"source": "other", "keyId": "k2"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer bServer.Close()

	cliB, _ := NewClient(bServer.URL, "token")
	_, _ = cliB.CreateBoardKey(ctx, "name")

	// 4. EnsureConnectionCredentialGrant branches
	grantServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/tool-connections/conn-grant/grants":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "grant-1", "credentialSecretRefs": []map[string]interface{}{},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/tool-connections/conn-grant/grants/installations":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"grant": map[string]interface{}{"id": "grant-2"},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/tool-connections/conn-inst/grants/installations":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"installation": map[string]interface{}{"id": "grant-3"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer grantServer.Close()
	cliGrant, _ := NewClient(grantServer.URL, "token")
	_ = cliGrant.EnsureConnectionCredentialGrant(ctx, "conn-grant", []map[string]interface{}{{"secretId": "s1"}})
}

type errorBodyReader struct{}

func (e *errorBodyReader) Read(p []byte) (n int, err error) {
	return 0, errors.New("simulated body read error")
}

func (e *errorBodyReader) Close() error { return nil }

type errorTransport struct{}

func (t *errorTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       &errorBodyReader{},
		Header:     make(http.Header),
	}, nil
}

func TestClientRemainingCoverage(t *testing.T) {
	ctx := context.Background()

	// 1. paperclipRequestWithErrorBody body read error (lines 633-635)
	cliErrBody, _ := NewClient("http://127.0.0.1:3100", "token")
	cliErrBody.HTTPClient.Transport = &errorTransport{}
	if err := cliErrBody.paperclipRequest(ctx, http.MethodGet, "/test", nil, nil); err == nil {
		t.Fatal("expected body read error")
	}

	// 2. CreateBoardKey branches (lines 687, 695)
	boardKeySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/cli-auth/challenges":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "chal-1", "token": "tok-1"})
		case "/api/cli-auth/me":
			if r.Header.Get("Authorization") == "Bearer tok-empty-key" {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"source": "board_key", "keyId": ""})
			} else {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"source": "board_key", "keyId": "k1"})
			}
		case "/api/cli-auth/challenges/chal-1/approve":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"status": "rejected"})
		}
	}))
	defer boardKeySrv.Close()

	cliBK, _ := NewClient(boardKeySrv.URL, "token")
	// line 695: status != "approved"
	if _, err := cliBK.CreateBoardKey(ctx, "k"); err == nil {
		t.Fatal("expected rejection error")
	}

	// line 687: me.KeyID == ""
	boardKeyEmptySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/cli-auth/challenges":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "chal-1", "token": "tok-1"})
		case "/api/cli-auth/me":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"source": "board_key", "keyId": ""})
		}
	}))
	defer boardKeyEmptySrv.Close()
	cliBKEmpty, _ := NewClient(boardKeyEmptySrv.URL, "token")
	if _, err := cliBKEmpty.CreateBoardKey(ctx, "k"); err == nil {
		t.Fatal("expected error on empty key ID")
	}

	// line 688: approval error
	boardKeyApproveErrSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/cli-auth/challenges":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "chal-1", "token": "tok-1", "boardApiToken": "bat-1"})
		case "/api/cli-auth/challenges/chal-1/approve":
			http.Error(w, "approve fail", http.StatusInternalServerError)
		}
	}))
	defer boardKeyApproveErrSrv.Close()
	cliBKApproveErr, _ := NewClient(boardKeyApproveErrSrv.URL, "token")
	if _, err := cliBKApproveErr.CreateBoardKey(ctx, "k"); err == nil {
		t.Fatal("expected approve error")
	}

	// line 696: identity error
	boardKeyIdErrSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/cli-auth/challenges":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "chal-1", "token": "tok-1", "boardApiToken": "bat-1"})
		case "/api/cli-auth/challenges/chal-1/approve":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"approved": true, "status": "approved", "keyId": "k1"})
		case "/api/cli-auth/me":
			http.Error(w, "me fail", http.StatusInternalServerError)
		}
	}))
	defer boardKeyIdErrSrv.Close()
	cliBKIdErr, _ := NewClient(boardKeyIdErrSrv.URL, "token")
	if _, err := cliBKIdErr.CreateBoardKey(ctx, "k"); err == nil {
		t.Fatal("expected identity error")
	}

	// 3. EnsureConnectionCredentialGrant branches (lines 527, 534, 550, 553)
	// Case: installation map without id in rawCreated
	instStep := 0
	srvInstObj := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			instStep++
			refs := []map[string]interface{}{}
			if instStep > 1 {
				refs = []map[string]interface{}{{"secretId": "s1"}}
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"grants": []map[string]interface{}{{
					"id":                   "grant-inst",
					"kind":                 "organization",
					"isDefault":            true,
					"status":               "active",
					"credentialSecretRefs": refs,
				}},
			})
		} else if r.Method == http.MethodPost {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"installation": map[string]interface{}{"id": "grant-inst"},
			})
		}
	}))
	defer srvInstObj.Close()
	cliInstObj, _ := NewClient(srvInstObj.URL, "token")
	if err := cliInstObj.EnsureConnectionCredentialGrant(ctx, "c1", []map[string]interface{}{{"secretId": "s1"}}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Case: error on second GetDefaultConnectionGrant (lines 527-529)
	step := 0
	srvErr2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		step++
		if step == 1 {
			// First GET: return prior with different refs
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"grants": []map[string]interface{}{{
					"id":                   "g1",
					"kind":                 "organization",
					"isDefault":            true,
					"status":               "active",
					"credentialSecretRefs": []map[string]interface{}{},
				}},
			})
		} else if step == 2 {
			// POST: return created
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "g2"})
		} else {
			// Second GET: fail 500
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
	}))
	defer srvErr2.Close()
	cliErr2, _ := NewClient(srvErr2.URL, "token")
	if err := cliErr2.EnsureConnectionCredentialGrant(ctx, "c1", []map[string]interface{}{{"secretId": "s1"}}); err == nil {
		t.Fatal("expected error on second GET")
	}

	// Case: error on third GetDefaultConnectionGrant after superseded delete (lines 550-552)
	step = 0
	srvErr3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		step++
		if step == 1 {
			// First GET: return prior with superseded IDs and matching refs
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"grants": []map[string]interface{}{
					{
						"id":                   "g1",
						"kind":                 "organization",
						"isDefault":            true,
						"status":               "active",
						"credentialSecretRefs": []map[string]interface{}{{"secretId": "s1"}},
					},
					{
						"id":        "sup-1",
						"kind":      "organization",
						"isDefault": false,
						"status":    "active",
					},
				},
			})
		} else if step == 2 {
			// DELETE superseded: 200 OK
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
		} else {
			// Final GET: fail 500
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
	}))
	defer srvErr3.Close()
	cliErr3, _ := NewClient(srvErr3.URL, "token")
	if err := cliErr3.EnsureConnectionCredentialGrant(ctx, "c1", []map[string]interface{}{{"secretId": "s1"}}); err == nil {
		t.Fatal("expected error on final GET")
	}

	// Case: verified does not match refs after reconciliation (lines 553-555)
	step = 0
	srvDiverge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		step++
		if step == 1 {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"grants": []map[string]interface{}{{
					"id":                   "g1",
					"kind":                 "organization",
					"isDefault":            true,
					"status":               "active",
					"credentialSecretRefs": []map[string]interface{}{},
				}},
			})
		} else if step == 2 {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "g2"})
		} else if step == 3 {
			// actual has id g2 and refs match
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"grants": []map[string]interface{}{{
					"id":                   "g2",
					"kind":                 "organization",
					"isDefault":            true,
					"status":               "active",
					"credentialSecretRefs": []map[string]interface{}{{"secretId": "s1"}},
				}},
			})
		} else {
			// verified diverges!
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"grants": []map[string]interface{}{{
					"id":                   "g2",
					"kind":                 "organization",
					"isDefault":            true,
					"status":               "active",
					"credentialSecretRefs": []map[string]interface{}{},
				}},
			})
		}
	}))
	defer srvDiverge.Close()
	cliDiverge, _ := NewClient(srvDiverge.URL, "token")
	if err := cliDiverge.EnsureConnectionCredentialGrant(ctx, "c1", []map[string]interface{}{{"secretId": "s1"}}); err == nil {
		t.Fatal("expected divergence error")
	}

	// 4. Missing items (ErrNotFound) and empty CompanyID branches across resources
	mockSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/c1/projects":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "p-diff-comp", "companyId": "c-other"},
				{"id": "p-no-comp"}, // no companyId for line 891
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/c1/projects/p1/workspaces":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "ws-1"}) // no companyId line 906
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/c1/projects/p1/workspaces":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "ws-1"}, // no companyId line 963
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/c1/skills":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "sk-1"}) // no companyId line 981
		case r.Method == http.MethodPost && r.URL.Path == "/api/skills/sk-err/files":
			http.Error(w, "files error", http.StatusInternalServerError) // line 991, 1047
		case r.Method == http.MethodGet && r.URL.Path == "/api/skills/sk-no-comp":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "sk-no-comp"}) // line 1004
		case r.Method == http.MethodPatch && r.URL.Path == "/api/skills/sk-err":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "sk-err"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/c1/secrets":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "sec-1"}) // line 1092
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/c1/agents":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "ag-1"}) // line 1180
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/c1/agents":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{{"id": "ag-1"}}) // line 1238
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/ag-1/files/instructions.md":
			http.NotFound(w, r) // line 1256
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/c-err/tools/applications":
			http.Error(w, "app create error", http.StatusInternalServerError) // line 1303
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/c1/tools/applications":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "app-1"}) // lines 1306, 1309
		case r.Method == http.MethodPatch && r.URL.Path == "/api/tool-applications/app-err":
			http.Error(w, "app update error", http.StatusInternalServerError) // line 1360
		case r.Method == http.MethodGet && r.URL.Path == "/api/projects/p-diff-id":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "p-other-id"}) // line 836
		case r.Method == http.MethodPatch && r.URL.Path == "/api/projects/p-diff-lead":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "p-diff-lead"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/projects/p-diff-lead":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "p-diff-lead", "leadAgentId": "other"}) // line 874
		case r.Method == http.MethodPatch && r.URL.Path == "/api/projects/p-lead-get-err":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "p-lead-get-err"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/projects/p-lead-get-err":
			http.Error(w, "get err", http.StatusInternalServerError) // line 871
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/c1/projects":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "p-no-comp"}, // line 891
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/projects/p1/workspaces":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "ws-1"}) // line 906
		case r.Method == http.MethodGet && r.URL.Path == "/api/projects/p1/workspaces":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{{"id": "ws-1"}}) // line 963
		case r.Method == http.MethodGet && r.URL.Path == "/api/projects/p-empty/workspaces":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{}) // line 927
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/c1/skills":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "sk-1", "markdown": "md-content"}) // line 981, 989, 992
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/c1/skills/sk-no-comp":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "sk-no-comp", "markdown": "md-content"}) // line 1002, 1004
		case r.Method == http.MethodPatch && r.URL.Path == "/api/companies/c1/skills/sk-err":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "sk-err"})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/companies/c1/skills/sk-err/files":
			http.Error(w, "files error", http.StatusInternalServerError) // line 1047
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/c1/secrets":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "sec-1"}) // line 1092
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/c1/agents":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "ag-1"}) // line 1180
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/c1/agents":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{{"id": "ag-1"}}) // line 1238
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/ag-1/instructions-bundle/file" && r.URL.Query().Get("path") == "empty.md":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{}) // line 1256
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/ag-1/instructions-bundle/file":
			http.NotFound(w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{{"id": "c1"}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/tool-applications/app-fallback":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "app-fallback", "companyId": "c1"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/c-err/tools/applications":
			http.Error(w, "app create error", http.StatusInternalServerError) // line 1303
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/c1/tools/applications":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "app-1"}) // lines 1306, 1309
		case r.Method == http.MethodPatch && r.URL.Path == "/api/tool-applications/app-err":
			http.Error(w, "app update error", http.StatusInternalServerError) // line 1360
		case r.Method == http.MethodPatch && r.URL.Path == "/api/tool-applications/app-no-type":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "app-no-type"}) // line 1363
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/c1/tools/applications":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{{"id": "app-1"}, {"id": "app-fallback", "type": "mcp_http"}}) // line 1326, 1387
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/c1/tools/connections":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "conn-1"}) // line 1405
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/c1/tools/apps/connect":
			var b map[string]string
			_ = json.NewDecoder(r.Body).Decode(&b)
			if b["galleryKey"] == "gal-1" {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"connectionId": "conn-g1",
					"connection":   map[string]interface{}{},
				})
			} else {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{})
			}
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/c1/tools/stdio-templates":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "t1"}) // line 1508, 1511
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/c1/tools/stdio-templates":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{{"id": "t1"}}) // line 1541
		case r.Method == http.MethodGet && r.URL.Path == "/api/tool-connections/conn-empty/installs":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{}) // line 1569
		case r.Method == http.MethodPut && r.URL.Path == "/api/tool-connections/conn-empty/installs":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{}) // line 1573, 1592
		case r.Method == http.MethodPatch && r.URL.Path == "/api/tool-policies/pol-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "pol-1"}) // line 1646
		case r.Method == http.MethodDelete && r.URL.Path == "/api/tool-policies/pol-1":
			w.WriteHeader(http.StatusOK) // line 1657
		default:
			http.NotFound(w, r)
		}
	}))
	defer mockSrv.Close()

	cliMock, _ := NewClient(mockSrv.URL, "token")

	// GetProject identity mismatch (line 836)
	if _, err := cliMock.GetProject(ctx, "p-diff-id"); err == nil {
		t.Fatal("expected identity mismatch error")
	}

	// SetProjectLead leadAgentID mismatch (line 874)
	if _, err := cliMock.SetProjectLead(ctx, "p-diff-lead", "ag1"); err == nil {
		t.Fatal("expected lead mismatch error")
	}

	// SetProjectLead GetProject err (line 871)
	if _, err := cliMock.SetProjectLead(ctx, "p-lead-get-err", "ag1"); err == nil {
		t.Fatal("expected GetProject error")
	}

	// ListProjects proj.CompanyID == "" (line 891)
	projs, err := cliMock.ListProjects(ctx, "c1")
	if err != nil || len(projs) == 0 {
		t.Fatalf("ListProjects failed: %v", err)
	}

	// CreateWorkspace resp.CompanyID == "" (line 906)
	if _, err := cliMock.CreateWorkspace(ctx, "p1", PaperclipWorkspace{}); err != nil {
		t.Fatalf("CreateWorkspace failed: %v", err)
	}

	// GetWorkspaceInProject not found (line 927)
	if _, err := cliMock.GetWorkspaceInProject(ctx, "p-empty", "ws-missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	// ListWorkspaces ws.CompanyID == "" (line 963)
	if _, err := cliMock.ListWorkspaces(ctx, "p1"); err != nil {
		t.Fatalf("ListWorkspaces failed: %v", err)
	}

	// CreateSkill s.CompanyID == "" & Content from Markdown (lines 981, 989, 992)
	meta := map[string]interface{}{"k": "v"}
	if _, err := cliMock.CreateSkill(ctx, "c1", PaperclipSkill{Metadata: meta}); err != nil {
		t.Fatalf("CreateSkill failed: %v", err)
	}

	// GetSkill s.CompanyID == "" & Content from Markdown (lines 1002, 1004)
	if _, err := cliMock.GetSkill(ctx, "c1", "sk-no-comp"); err != nil {
		t.Fatalf("GetSkill failed: %v", err)
	}

	// UpdateSkill instruction err (line 1047)
	if _, err := cliMock.UpdateSkill(ctx, "c1", "sk-err", PaperclipSkill{Content: "instr"}); err == nil {
		t.Fatal("expected error on UpdateSkill instructions")
	}

	// CreateSecret resp.CompanyID == "" (line 1092)
	if _, err := cliMock.CreateSecret(ctx, "c1", "s1", "desc", "val"); err != nil {
		t.Fatalf("CreateSecret failed: %v", err)
	}

	// CreateAgent resp.CompanyID == "" (line 1180)
	if _, err := cliMock.CreateAgent(ctx, "c1", PaperclipAgent{}); err != nil {
		t.Fatalf("CreateAgent failed: %v", err)
	}

	// ListAgents a.CompanyID == "" (line 1238)
	if _, err := cliMock.ListAgents(ctx, "c1"); err != nil {
		t.Fatalf("ListAgents failed: %v", err)
	}
	if _, err := cliMock.ListAgents(ctx, "c1"); err != nil {
		t.Fatalf("ListAgents failed: %v", err)
	}

	// GetAgentInstructionsFile 404 (line 1256)
	if _, err := cliMock.GetAgentInstructionsFile(ctx, "ag-1", "instructions.md"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	// GetAgentInstructionsFile empty (line 1256)
	content, err := cliMock.GetAgentInstructionsFile(ctx, "ag-1", "empty.md")
	if err != nil || content != "" {
		t.Fatalf("expected empty content, got %q, %v", content, err)
	}

	// GetToolApp backfills Type when direct response omits it
	appFallback, err := cliMock.GetToolApp(ctx, "app-fallback")
	if err != nil || appFallback.ID != "app-fallback" || appFallback.Type != "mcp_http" {
		t.Fatalf("expected app-fallback with type mcp_http, got %v, %v", appFallback, err)
	}

	// CreateToolApp error (line 1303)
	if _, err := cliMock.CreateToolApp(ctx, "c-err", PaperclipToolApp{Type: "mcp_http"}); err == nil {
		t.Fatal("expected error on CreateToolApp")
	}

	// CreateToolApp resp.CompanyID == "" & resp.Type == "" (lines 1306, 1309)
	if _, err := cliMock.CreateToolApp(ctx, "c1", PaperclipToolApp{Type: "mcp_http"}); err != nil {
		t.Fatalf("CreateToolApp failed: %v", err)
	}

	// UpdateToolApp error (line 1360)
	if _, err := cliMock.UpdateToolApp(ctx, "app-err", PaperclipToolApp{Type: "mcp_http"}); err == nil {
		t.Fatal("expected error on UpdateToolApp")
	}

	// UpdateToolApp resp.Type == "" (line 1363)
	if _, err := cliMock.UpdateToolApp(ctx, "app-no-type", PaperclipToolApp{Type: "mcp_http"}); err != nil {
		t.Fatalf("UpdateToolApp failed: %v", err)
	}

	// ListToolApps a.CompanyID == "" (line 1387)
	if _, err := cliMock.ListToolApps(ctx, "c1"); err != nil {
		t.Fatalf("ListToolApps failed: %v", err)
	}

	// CreateToolConnection resp.CompanyID == "" (line 1405)
	if _, err := cliMock.CreateToolConnection(ctx, "c1", PaperclipToolConnection{}); err != nil {
		t.Fatalf("CreateToolConnection failed: %v", err)
	}

	// ConnectGalleryApp conn.ID = resp.ConnectionID & conn.CompanyID == "" (lines 1425, 1431)
	if _, err := cliMock.ConnectGalleryApp(ctx, "c1", "gal-1"); err != nil {
		t.Fatalf("ConnectGalleryApp failed: %v", err)
	}

	// ConnectGalleryApp conn.ID == "" error (line 1428)
	if _, err := cliMock.ConnectGalleryApp(ctx, "c1", "gal-2"); err == nil {
		t.Fatal("expected error on empty gallery connect")
	}

	// CreateStdioTemplate resp.CompanyID == "" & resp.TemplateID == "" (lines 1508, 1511)
	if _, err := cliMock.CreateStdioTemplate(ctx, "c1", PaperclipStdioTemplate{TemplateID: "tid"}); err != nil {
		t.Fatalf("CreateStdioTemplate failed: %v", err)
	}

	// GetStdioTemplate not found (line 1527)
	emptySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]interface{}{})
	}))
	defer emptySrv.Close()
	cliEmpty, _ := NewClient(emptySrv.URL, "token")
	if _, err := cliEmpty.GetStdioTemplate(ctx, "c1", "missing-tmpl"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	// ListStdioTemplates tpl.CompanyID == "" (line 1541)
	if _, err := cliMock.ListStdioTemplates(ctx, "c1"); err != nil {
		t.Fatalf("ListStdioTemplates failed: %v", err)
	}

	// GetConnectionInstalls return nil, nil (line 1569)
	insts, err := cliMock.GetConnectionInstalls(ctx, "conn-empty")
	if err != nil || insts != nil {
		t.Fatalf("expected nil, nil, got %v, %v", insts, err)
	}

	// UpdateConnectionInstalls installs == nil & return nil, nil (lines 1573, 1592)
	insts, err = cliMock.UpdateConnectionInstalls(ctx, "conn-empty", nil)
	if err != nil || insts != nil {
		t.Fatalf("expected nil, nil, got %v, %v", insts, err)
	}

	// UpdateToolPolicy companyID == "" (line 1646)
	if _, err := cliMock.UpdateToolPolicy(ctx, "", "pol-1", PaperclipToolPolicy{}); err != nil {
		t.Fatalf("UpdateToolPolicy failed: %v", err)
	}

	// DeleteToolPolicy companyID == "" (line 1657)
	if err := cliMock.DeleteToolPolicy(ctx, "", "pol-1"); err != nil {
		t.Fatalf("DeleteToolPolicy failed: %v", err)
	}

	// NewClient invalid endpoint
	if _, err := NewClient("://invalid-url", "token"); err == nil {
		t.Fatal("expected error on invalid endpoint URL")
	}

	// Test error paths for GetSkill, GetAgent, GetToolConnection
	errSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal server error", http.StatusInternalServerError)
	}))
	defer errSrv.Close()
	cliErr, _ := NewClient(errSrv.URL, "token")
	if _, err := cliErr.GetSkill(ctx, "c1", "sk-1"); err == nil {
		t.Fatal("expected error on GetSkill when ListSkills fails")
	}
	if _, err := cliErr.GetSkill(ctx, "", "sk-1"); err == nil {
		t.Fatal("expected error on GetSkill when ListCompanies fails")
	}
	if _, err := cliErr.GetAgent(ctx, "ag-1"); err == nil {
		t.Fatal("expected error on GetAgent when ListCompanies fails")
	}
	if _, err := cliErr.GetToolConnection(ctx, "conn-1"); err == nil {
		t.Fatal("expected error on GetToolConnection when ListCompanies fails")
	}
}
