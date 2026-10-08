package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBuildAgentCreatePayloadFull(t *testing.T) {
	agent := PaperclipAgent{
		Name:                 "agent-full",
		Role:                 "lead",
		Title:                "Lead Engineer",
		Icon:                 "robot",
		ReportsTo:            "mgr-1",
		Capabilities:         "fullstack",
		DefaultEnvironmentID: "env-1",
		AdapterType:          "antigravity_local",
		RuntimeConfig:        map[string]interface{}{"k1": "v1"},
		AdapterConfig:        map[string]interface{}{"m": "gpt-4"},
		Metadata:             map[string]interface{}{"tag": "prod"},
		Permissions: &PaperclipAgentPermissions{
			CanAssignTasks:  true,
			CanCreateAgents: true,
			CanCreateSkills: true,
		},
	}
	payload := buildAgentCreatePayload(agent)
	if payload["title"] != "Lead Engineer" || payload["icon"] != "robot" ||
		payload["reportsTo"] != "mgr-1" || payload["capabilities"] != "fullstack" ||
		payload["defaultEnvironmentId"] != "env-1" || payload["runtimeConfig"] == nil ||
		payload["adapterConfig"] == nil || payload["metadata"] == nil || payload["permissions"] == nil {
		t.Fatalf("buildAgentCreatePayload did not populate all fields: %+v", payload)
	}
}

func TestCreateAgentHire(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/companies/comp-1/agent-hires" && r.Method == http.MethodPost {
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			_ = json.NewEncoder(w).Encode(PaperclipAgentHireResponse{
				Agent: PaperclipAgent{
					ID:        "agent-hire-1",
					CompanyID: "", // test empty company ID backfill
					Name:      "hire-me",
				},
				Approval: &PaperclipApproval{
					ID:     "appr-1",
					Status: "pending",
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	cli, err := NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}

	agent, err := cli.CreateAgentHire(ctx, "comp-1", PaperclipAgent{Name: "hire-me"})
	if err != nil {
		t.Fatalf("CreateAgentHire failed: %v", err)
	}
	if agent.ID != "agent-hire-1" || agent.CompanyID != "comp-1" {
		t.Fatalf("unexpected agent: %+v", agent)
	}
	if agent.HireApproval == nil || agent.HireApproval.ID != "appr-1" {
		t.Fatalf("missing hire approval: %+v", agent)
	}

	_, err = cli.CreateAgentHire(ctx, "comp-notfound", PaperclipAgent{Name: "hire-me"})
	if err == nil {
		t.Fatal("expected error for not found route in CreateAgentHire")
	}
}

func TestCreateAgentHiringApprovalPaths(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/companies/comp-board" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(PaperclipCompany{
				ID:                              "comp-board",
				RequireBoardApprovalForNewAgents: true,
			})
		case r.URL.Path == "/api/companies/comp-board/agent-hires" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(PaperclipAgentHireResponse{
				Agent: PaperclipAgent{
					ID:        "agent-board-1",
					CompanyID: "comp-board",
					Name:      "board-agent",
				},
				Approval: &PaperclipApproval{
					ID:     "appr-board-1",
					Status: "pending",
				},
			})
		case r.URL.Path == "/api/companies/comp-err" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(PaperclipCompany{
				ID:                              "comp-err",
				RequireBoardApprovalForNewAgents: false,
			})
		case r.URL.Path == "/api/companies/comp-err/agents" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"message": "action requires board approval: use agent-hires route",
			})
		case r.URL.Path == "/api/companies/comp-err/agent-hires" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(PaperclipAgentHireResponse{
				Agent: PaperclipAgent{
					ID:        "agent-err-1",
					CompanyID: "comp-err",
					Name:      "fallback-hire",
				},
				Approval: &PaperclipApproval{
					ID:     "appr-err-1",
					Status: "pending",
				},
			})
		case r.URL.Path == "/api/companies/comp-fail" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(PaperclipCompany{ID: "comp-fail"})
		case r.URL.Path == "/api/companies/comp-fail/agents" && r.Method == http.MethodPost:
			http.Error(w, "internal server failure", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cli, err := NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}

	// 1. Company requires board approval via company config
	ag1, err := cli.CreateAgent(ctx, "comp-board", PaperclipAgent{Name: "board-agent"})
	if err != nil {
		t.Fatalf("CreateAgent via company approval setting failed: %v", err)
	}
	if ag1.HireApproval == nil || ag1.HireApproval.ID != "appr-board-1" {
		t.Fatalf("expected hire approval on ag1: %+v", ag1)
	}

	// 2. GetCompany fails -> returns error
	_, err = cli.CreateAgent(ctx, "comp-notfound", PaperclipAgent{Name: "fallback-hire"})
	if err == nil {
		t.Fatal("expected error when GetCompany fails")
	}

	// 3. Regular error is returned on agent create failure
	_, err = cli.CreateAgent(ctx, "comp-fail", PaperclipAgent{Name: "fail"})
	if err == nil {
		t.Fatal("expected error on comp-fail")
	}
}

func TestUpdateAgentGUS413(t *testing.T) {
	ctx := context.Background()
	var receivedPayload map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/agents/agent-1" && r.Method == http.MethodPatch {
			_ = json.NewDecoder(r.Body).Decode(&receivedPayload)
			_ = json.NewEncoder(w).Encode(PaperclipAgent{
				ID:   "agent-1",
				Name: "updated",
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	cli, err := NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}

	// Test with values set
	_, err = cli.UpdateAgent(ctx, "agent-1", PaperclipAgent{
		Name:                 "updated",
		Title:                "Lead",
		Icon:                 "star",
		ReportsTo:            "mgr-1",
		Capabilities:         "full",
		DefaultEnvironmentID: "env-1",
		RuntimeConfig:        map[string]interface{}{"k": "v"},
		AdapterConfig:        map[string]interface{}{"a": "b"},
		ReplaceAdapterConfig: true,
		Metadata:             map[string]interface{}{"m": "n"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if receivedPayload["title"] != "Lead" || receivedPayload["icon"] != "star" ||
		receivedPayload["reportsTo"] != "mgr-1" || receivedPayload["capabilities"] != "full" ||
		receivedPayload["defaultEnvironmentId"] != "env-1" || receivedPayload["replaceAdapterConfig"] != true {
		t.Fatalf("unexpected update payload: %+v", receivedPayload)
	}

	// Test with cleared fields
	_, err = cli.UpdateAgent(ctx, "agent-1", PaperclipAgent{
		Name:                 "cleared",
		ReplaceAdapterConfig: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if receivedPayload["title"] != nil || receivedPayload["icon"] != nil ||
		receivedPayload["reportsTo"] != nil || receivedPayload["capabilities"] != nil ||
		receivedPayload["defaultEnvironmentId"] != nil {
		t.Fatalf("expected nil for cleared fields, got: %+v", receivedPayload)
	}
}

func TestPauseResumeTerminateAgent(t *testing.T) {
	ctx := context.Background()
	called := make(map[string]bool)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		called[fmt.Sprintf("%s %s", r.Method, r.URL.Path)] = true
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cli, err := NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}

	if err := cli.PauseAgent(ctx, "agent-1"); err != nil {
		t.Fatal(err)
	}
	if !called["POST /api/agents/agent-1/pause"] {
		t.Fatal("PauseAgent did not call POST /api/agents/agent-1/pause")
	}

	if err := cli.ResumeAgent(ctx, "agent-1"); err != nil {
		t.Fatal(err)
	}
	if !called["POST /api/agents/agent-1/resume"] {
		t.Fatal("ResumeAgent did not call POST /api/agents/agent-1/resume")
	}

	if err := cli.TerminateAgent(ctx, "agent-1"); err != nil {
		t.Fatal(err)
	}
	if !called["POST /api/agents/agent-1/terminate"] {
		t.Fatal("TerminateAgent did not call POST /api/agents/agent-1/terminate")
	}
}

func TestUpdateAgentPermissionsGUS413(t *testing.T) {
	ctx := context.Background()
	var payload map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/agents/agent-1/permissions" && r.Method == http.MethodPatch {
			_ = json.NewDecoder(r.Body).Decode(&payload)
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	cli, err := NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}

	err = cli.UpdateAgentPermissions(ctx, "agent-1", PaperclipAgentPermissions{
		CanCreateAgents:     false,
		CanCreateSkills:     true,
		CanAssignTasks:      true,
		TrustPreset:         "standard",
		AuthorizationPolicy: map[string]interface{}{"protectedAgent": true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if payload["trustPreset"] != "standard" || payload["authorizationPolicy"] == nil {
		t.Fatalf("unexpected permissions payload: %+v", payload)
	}

	// Also verify empty AuthorizationPolicy sends empty map to clear policy
	err = cli.UpdateAgentPermissions(ctx, "agent-1", PaperclipAgentPermissions{
		CanCreateAgents:     false,
		CanCreateSkills:     true,
		CanAssignTasks:      true,
		TrustPreset:         "standard",
		AuthorizationPolicy: map[string]interface{}{},
	})
	if err != nil {
		t.Fatal(err)
	}
	authPol, ok := payload["authorizationPolicy"].(map[string]interface{})
	if !ok || len(authPol) != 0 {
		t.Fatalf("expected empty map for empty authorizationPolicy, got: %+v", payload["authorizationPolicy"])
	}
}
