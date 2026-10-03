package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegressionIDReadDirectGETErrorDoesNotFallBack(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/agent-1":
			http.Error(w, `{"error":"internal database failure"}`, http.StatusInternalServerError)
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies":
			_ = json.NewEncoder(w).Encode([]PaperclipCompany{
				{ID: "comp-2", Name: "Company Two"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-2/agents":
			_ = json.NewEncoder(w).Encode([]PaperclipAgent{
				{ID: "agent-wrong", CompanyID: "comp-2", Name: "agent-1"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cli, err := NewClient(server.URL, "test-token")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	agent, err := cli.GetAgent(ctx, "agent-1")
	if err == nil {
		t.Fatalf("expected error on 500 direct GET, but got agent: %+v", agent)
	}
	if !strings.Contains(err.Error(), "500") {
		t.Fatalf("expected 500 in error, got: %v", err)
	}
	if agent != nil {
		t.Fatalf("expected nil agent, got: %+v", agent)
	}
}

func TestRegressionUnknownRoute404FailsRatherThanNotFound(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"API route not found"}`))
	}))
	defer server.Close()

	cli, err := NewClient(server.URL, "test-token")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	err = cli.paperclipRequest(ctx, http.MethodGet, "/api/unsupported-endpoint", nil, nil)
	if err == nil {
		t.Fatal("expected error on unknown route 404, got nil")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("expected unknown route 404 to NOT be ErrNotFound, got: %v", err)
	}
}

func TestFullCoverageClientRemaining(t *testing.T) {
	ctx := context.Background()

	// 1. APIError.Is and Error()
	ae := &APIError{StatusCode: 404, Method: "GET", Path: "/api/test", CorrelationID: "cid-1", Body: "not found", IsUnknownRoute: true}
	if errors.Is(ae, ErrNotFound) {
		t.Fatal("unknown route 404 should not match ErrNotFound")
	}
	if errors.Is(ae, ErrConflict) {
		t.Fatal("404 should not match ErrConflict")
	}
	if errors.Is(ae, errors.New("other")) {
		t.Fatal("should not match other error")
	}
	aeConflict := &APIError{StatusCode: 409, Method: "POST", Path: "/api/test", CorrelationID: "cid-2", Body: "conflict"}
	if !errors.Is(aeConflict, ErrConflict) {
		t.Fatal("409 should match ErrConflict")
	}
	aeOther := &APIError{StatusCode: 500, Method: "GET", Path: "/api/test", CorrelationID: "cid-3", Body: "err"}
	if errors.Is(aeOther, ErrNotFound) || errors.Is(aeOther, ErrConflict) {
		t.Fatal("500 should not match ErrNotFound or ErrConflict")
	}

	// 2. sanitizeJSONValue and sanitizeResponseBody
	s1 := sanitizeJSONValue([]interface{}{map[string]interface{}{"password": "secret", "other": "val"}})
	sSlice, ok := s1.([]interface{})
	if !ok || len(sSlice) != 1 {
		t.Fatalf("unexpected sanitized slice: %v", s1)
	}
	sMap, ok := sSlice[0].(map[string]interface{})
	if !ok || sMap["password"] != "[REDACTED]" || sMap["other"] != "val" {
		t.Fatalf("unexpected sanitized map: %v", sSlice[0])
	}
	if sanitizeJSONValue("primitive") != "primitive" {
		t.Fatal("expected primitive to pass through")
	}

	if sanitizeResponseBody(nil) != "" {
		t.Fatal("expected empty string for nil body")
	}
	nonJSON := []byte("plain text with token: supersecret and key=othersecret and bearer mytoken")
	sanitizedNonJSON := sanitizeResponseBody(nonJSON, "extraSecret")
	if strings.Contains(sanitizedNonJSON, "supersecret") || strings.Contains(sanitizedNonJSON, "othersecret") || strings.Contains(sanitizedNonJSON, "mytoken") {
		t.Fatalf("sensitive tokens not redacted: %s", sanitizedNonJSON)
	}
	longBody := []byte(strings.Repeat("a", 3000))
	sanitizedLong := sanitizeResponseBody(longBody)
	if len(sanitizedLong) > 2055 || !strings.HasSuffix(sanitizedLong, "...") {
		t.Fatalf("expected long body to be truncated: len=%d", len(sanitizedLong))
	}
	extraSec := sanitizeResponseBody([]byte(`{"msg": "secret-value"}`), "secret-value")
	if strings.Contains(extraSec, "secret-value") {
		t.Fatalf("extra secret was not redacted: %s", extraSec)
	}

	// 3. extractCorrelationID
	req, _ := http.NewRequest(http.MethodGet, "http://localhost", nil)
	resp := &http.Response{Header: http.Header{}, Request: req}
	resp.Header.Set("X-Correlation-Id", "corr-header-1")
	if extractCorrelationID(resp, nil) != "corr-header-1" {
		t.Fatal("expected corr-header-1")
	}
	resp.Header.Del("X-Correlation-Id")
	resp.Header.Set("X-Request-Id", "req-id-1")
	if extractCorrelationID(resp, nil) != "req-id-1" {
		t.Fatal("expected req-id-1")
	}
	if extractCorrelationID(nil, []byte(`{"correlationId": "body-corr-1"}`)) != "body-corr-1" {
		t.Fatal("expected body-corr-1")
	}
	if extractCorrelationID(nil, nil) != "none" {
		t.Fatal("expected none")
	}

	// 4. isUnknownRoute
	if isUnknownRoute(200, "ok") {
		t.Fatal("expected false for 200")
	}
	if isUnknownRoute(404, "") {
		t.Fatal("expected false for empty body")
	}
	for _, text := range []string{
		"API route not found", "route not found", "Cannot GET /foo", "Cannot POST /bar",
		"Cannot PATCH /baz", "Cannot DELETE /qux", "Cannot PUT /xyz", "<!DOCTYPE html>404", "<html>not found</html>",
	} {
		if !isUnknownRoute(404, text) {
			t.Fatalf("expected isUnknownRoute to be true for %q", text)
		}
	}
	if isUnknownRoute(404, `{"error": "Agent not found"}`) {
		t.Fatal("expected isUnknownRoute to be false for resource 404")
	}

	// 5. Empty ID validation on client methods
	cli, _ := NewClient("http://127.0.0.1:9999", "tok")
	if _, err := cli.GetCompany(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Fatal("expected ErrNotFound for empty company ID")
	}
	if _, err := cli.GetAgent(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Fatal("expected ErrNotFound for empty agent ID")
	}
	if _, err := cli.GetSkill(ctx, "", "s1"); !errors.Is(err, ErrNotFound) {
		t.Fatal("expected ErrNotFound for empty company ID in GetSkill")
	}
	if _, err := cli.GetSkill(ctx, "c1", ""); !errors.Is(err, ErrNotFound) {
		t.Fatal("expected ErrNotFound for empty skill ID in GetSkill")
	}
	if _, err := cli.GetSecret(ctx, "", "sec1"); !errors.Is(err, ErrNotFound) {
		t.Fatal("expected ErrNotFound for empty company ID in GetSecret")
	}
	if _, err := cli.GetSecret(ctx, "c1", ""); !errors.Is(err, ErrNotFound) {
		t.Fatal("expected ErrNotFound for empty secret ID in GetSecret")
	}
	if _, err := cli.GetToolApp(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Fatal("expected ErrNotFound for empty app ID")
	}
	if _, err := cli.GetToolConnection(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Fatal("expected ErrNotFound for empty conn ID")
	}
	if _, err := cli.GetWorkspaceInProject(ctx, "", "ws1"); !errors.Is(err, ErrNotFound) {
		t.Fatal("expected ErrNotFound for empty project ID in GetWorkspaceInProject")
	}
	if _, err := cli.GetWorkspaceInProject(ctx, "p1", ""); !errors.Is(err, ErrNotFound) {
		t.Fatal("expected ErrNotFound for empty workspace ID in GetWorkspaceInProject")
	}
	if _, err := cli.GetStdioTemplate(ctx, "", "tpl1"); !errors.Is(err, ErrNotFound) {
		t.Fatal("expected ErrNotFound for empty company ID in GetStdioTemplate")
	}
	if _, err := cli.GetStdioTemplate(ctx, "c1", ""); !errors.Is(err, ErrNotFound) {
		t.Fatal("expected ErrNotFound for empty template ID in GetStdioTemplate")
	}
	if _, err := cli.FindCompanyByName(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Fatal("expected ErrNotFound for empty name in FindCompanyByName")
	}

	// 6. Server for capability check, FindCompanyByName ambiguity, GetStdioTemplate, ListSkills, ListToolConnections
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/instance/settings/experimental":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"enableEnvironments": true,
				"customFeature":      true,
				"enableDisabled":     false,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies":
			_ = json.NewEncoder(w).Encode([]PaperclipCompany{
				{ID: "c1", Name: "DuplicateCorp"},
				{ID: "c2", Name: "DuplicateCorp"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-test/tools/stdio-templates":
			_ = json.NewEncoder(w).Encode([]PaperclipStdioTemplate{
				{ID: "tpl-id-1", TemplateID: "gh-template", Name: "GitHub"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-test/skills":
			_ = json.NewEncoder(w).Encode([]PaperclipSkill{
				{ID: "sk-1", CompanyID: "comp-test", Name: "S1", Content: "AlreadySet"},
				{ID: "sk-2", CompanyID: "", Name: "S2", Markdown: "FromMarkdown"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-test/tools/connections":
			_ = json.NewEncoder(w).Encode([]PaperclipToolConnection{
				{ID: "conn-1", CompanyID: "comp-test", Name: "C1"},
				{ID: "conn-2", CompanyID: "", Name: "C2"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/tool-applications/app-with-type":
			_ = json.NewEncoder(w).Encode(PaperclipToolApp{ID: "app-with-type", Type: "mcp_http", CompanyID: "comp-test"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/tool-applications/app-no-type-no-list-match":
			_ = json.NewEncoder(w).Encode(PaperclipToolApp{ID: "app-no-type-no-list-match", CompanyID: "comp-test"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-test/tools/applications":
			_ = json.NewEncoder(w).Encode([]PaperclipToolApp{
				{ID: "other-app", Type: "mcp_stdio"},
			})
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/environments"):
			_ = json.NewEncoder(w).Encode([]PaperclipEnvironment{
				{ID: "env-1", Driver: "local"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	cliMock, _ := NewClient(srv.URL, "tok")

	// Test GetExperimentalSettings caching
	settings1, err := cliMock.GetExperimentalSettings(ctx)
	if err != nil {
		t.Fatalf("first GetExperimentalSettings failed: %v", err)
	}
	settings2, err := cliMock.GetExperimentalSettings(ctx)
	if err != nil || settings2["enableEnvironments"] != true {
		t.Fatalf("cached GetExperimentalSettings failed: %v", err)
	}
	_ = settings1

	// Test CheckCapability
	if err := cliMock.CheckCapability(ctx, "enableEnvironments"); err != nil {
		t.Fatalf("expected CheckCapability enableEnvironments to succeed: %v", err)
	}
	if err := cliMock.CheckCapability(ctx, "environments"); err != nil {
		t.Fatalf("expected CheckCapability environments to succeed: %v", err)
	}
	if err := cliMock.CheckCapability(ctx, "customFeature"); err != nil {
		t.Fatalf("expected CheckCapability customFeature to succeed: %v", err)
	}
	if err := cliMock.CheckCapability(ctx, "enableDisabled"); err == nil {
		t.Fatal("expected CheckCapability enableDisabled to fail")
	}
	if err := cliMock.CheckCapability(ctx, "nonExistentCapability"); err == nil {
		t.Fatal("expected CheckCapability nonExistentCapability to fail")
	}

	// Test CheckEnvironmentCapability branches
	if err := cliMock.CheckEnvironmentCapability(ctx, "comp-test"); err != nil {
		t.Fatalf("expected CheckEnvironmentCapability to succeed: %v", err)
	}

	// Test CheckEnvironmentCapability with 404 company not found (route exists, returns nil)
	srvCompanyNotFound := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"company not found"}`))
	}))
	defer srvCompanyNotFound.Close()
	cliComp404, _ := NewClient(srvCompanyNotFound.URL, "tok")
	if err := cliComp404.CheckEnvironmentCapability(ctx, ""); err != nil {
		t.Fatalf("expected CheckEnvironmentCapability with company not found to succeed: %v", err)
	}

	// Test CheckEnvironmentCapability with unknown route 404
	srvEnvRoute404 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"Cannot GET /api/companies/none/environments"}`))
	}))
	defer srvEnvRoute404.Close()
	cliEnv404, _ := NewClient(srvEnvRoute404.URL, "tok")
	if err := cliEnv404.CheckEnvironmentCapability(ctx, ""); err == nil || !strings.Contains(err.Error(), "route not found") {
		t.Fatalf("expected route not found error from CheckEnvironmentCapability, got: %v", err)
	}

	// Test CheckEnvironmentCapability with server 500
	srvEnv500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"db failure"}`))
	}))
	defer srvEnv500.Close()
	cliEnv500, _ := NewClient(srvEnv500.URL, "tok")
	if err := cliEnv500.CheckEnvironmentCapability(ctx, "comp-test"); err == nil || !strings.Contains(err.Error(), "failed to verify capability") {
		t.Fatalf("expected failed to verify capability error, got: %v", err)
	}

	// Test CheckEnvironmentCapability with canceled context (non-APIError)
	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()
	if err := cliMock.CheckEnvironmentCapability(canceledCtx, "comp-test"); err == nil || !strings.Contains(err.Error(), "failed to verify capability") {
		t.Fatalf("expected non-APIError to fail CheckEnvironmentCapability, got: %v", err)
	}

	// Test CheckCapability when route is 404
	srv404 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"API route not found"}`))
	}))
	defer srv404.Close()
	cli404, _ := NewClient(srv404.URL, "tok")
	if err := cli404.CheckCapability(ctx, "enableEnvironments"); err == nil || !strings.Contains(err.Error(), "route not found") {
		t.Fatalf("expected route not found error, got: %v", err)
	}

	// Test CheckCapability when server returns 500
	srv500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal error"}`))
	}))
	defer srv500.Close()
	cli500, _ := NewClient(srv500.URL, "tok")
	if err := cli500.CheckCapability(ctx, "enableEnvironments"); err == nil || !strings.Contains(err.Error(), "failed to verify capability") {
		t.Fatalf("expected failed to verify capability error, got: %v", err)
	}

	// Test FindCompanyByName multiple matches error
	if _, err := cliMock.FindCompanyByName(ctx, "DuplicateCorp"); err == nil || !strings.Contains(err.Error(), "candidate IDs: c1, c2") {
		t.Fatalf("expected candidate IDs in FindCompanyByName error, got: %v", err)
	}

	// Test GetStdioTemplate matches
	tplMatchID, err := cliMock.GetStdioTemplate(ctx, "comp-test", "tpl-id-1")
	if err != nil || tplMatchID.ID != "tpl-id-1" {
		t.Fatalf("expected tpl-id-1 by ID, got: %v, %v", tplMatchID, err)
	}
	tplMatchTemplateID, err := cliMock.GetStdioTemplate(ctx, "comp-test", "gh-template")
	if err != nil || tplMatchTemplateID.TemplateID != "gh-template" {
		t.Fatalf("expected gh-template by TemplateID, got: %v, %v", tplMatchTemplateID, err)
	}
	if _, err := cliMock.GetStdioTemplate(ctx, "comp-test", "missing-template"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing template, got: %v", err)
	}

	// Test ListSkills branches
	skills, err := cliMock.ListSkills(ctx, "comp-test")
	if err != nil || len(skills) != 2 {
		t.Fatalf("expected 2 skills, got: %v, %v", skills, err)
	}
	if skills[1].Content != "FromMarkdown" || skills[1].CompanyID != "comp-test" {
		t.Fatalf("skill markdown/company backfill failed: %+v", skills[1])
	}

	// Test ListToolConnections branches
	conns, err := cliMock.ListToolConnections(ctx, "comp-test")
	if err != nil || len(conns) != 2 {
		t.Fatalf("expected 2 connections, got: %v, %v", conns, err)
	}
	if conns[1].CompanyID != "comp-test" {
		t.Fatalf("connection company backfill failed: %+v", conns[1])
	}

	// Test GetToolApp branches
	appWithType, err := cliMock.GetToolApp(ctx, "app-with-type")
	if err != nil || appWithType.Type != "mcp_http" {
		t.Fatalf("expected app-with-type to have mcp_http, got: %v, %v", appWithType, err)
	}
	appNoMatch, err := cliMock.GetToolApp(ctx, "app-no-type-no-list-match")
	if err != nil || appNoMatch.Type != "" {
		t.Fatalf("expected app with empty type when not in list, got: %v, %v", appNoMatch, err)
	}
}

func TestRegressionSecretExclusionEndToEndClientCall(t *testing.T) {
	ctx := context.Background()
	providerToken := "paperclip-provider-bearer-token-xyz-12345"
	topLevelSecret := "super-top-level-secret-value-999"
	nestedSecret := "sk-nested-agent-env-token-88888"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Correlation-Id", "cid-secret-echo")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(fmt.Sprintf(`{"error":"failed operation: bearer=%s, secret=%s, nested=%s"}`,
			providerToken, topLevelSecret, nestedSecret)))
	}))
	defer srv.Close()

	cli, err := NewClient(srv.URL, providerToken)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	_, err = cli.CreateAgent(ctx, "comp-1", PaperclipAgent{
		Name: "agent-with-secrets",
		AdapterConfig: map[string]interface{}{
			"env": map[string]interface{}{
				"ANTHROPIC_API_KEY": nestedSecret,
			},
			"token": topLevelSecret,
		},
	})
	if err == nil {
		t.Fatal("expected CreateAgent to fail with 500 error")
	}

	errMsg := err.Error()
	if strings.Contains(errMsg, providerToken) {
		t.Fatalf("provider token leaked in error message: %s", errMsg)
	}
	if strings.Contains(errMsg, topLevelSecret) {
		t.Fatalf("top-level secret leaked in error message: %s", errMsg)
	}
	if strings.Contains(errMsg, nestedSecret) {
		t.Fatalf("nested secret leaked in error message: %s", errMsg)
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected error to be *APIError, got: %T", err)
	}
	if strings.Contains(apiErr.Body, providerToken) {
		t.Fatalf("provider token leaked in apiErr.Body: %s", apiErr.Body)
	}
	if strings.Contains(apiErr.Body, topLevelSecret) {
		t.Fatalf("top-level secret leaked in apiErr.Body: %s", apiErr.Body)
	}
	if strings.Contains(apiErr.Body, nestedSecret) {
		t.Fatalf("nested secret leaked in apiErr.Body: %s", apiErr.Body)
	}
	if !strings.Contains(apiErr.Body, "[REDACTED]") {
		t.Fatalf("expected [REDACTED] in apiErr.Body, got: %s", apiErr.Body)
	}
}

func TestRegressionSecretRequestUnknownRoute404IsNotErrNotFound(t *testing.T) {
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message": "API route not found"}`))
	}))
	defer srv.Close()

	cli, err := NewClient(srv.URL, "prov-token")
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	_, err = cli.RotateSecret(ctx, "sec-1", "new-secret-val")
	if err == nil {
		t.Fatal("expected error on RotateSecret unknown route 404")
	}
	if errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown route 404 on secret request must NOT be misclassified as ErrNotFound, got: %v", err)
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got: %T", err)
	}
	if !apiErr.IsUnknownRoute {
		t.Fatal("expected apiErr.IsUnknownRoute to be true")
	}
	if apiErr.Body != "[REDACTED]" {
		t.Fatalf("expected apiErr.Body to be [REDACTED], got: %s", apiErr.Body)
	}
}
