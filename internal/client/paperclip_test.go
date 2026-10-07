package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

func TestConnectionCredentialGrantLifecycle(t *testing.T) {
	refs, err := ConnectionCredentialSecretRefs([]map[string]interface{}{{"name": "authorization", "secretId": "protected-id"}})
	if err != nil || !reflect.DeepEqual(refs, []map[string]interface{}{{"configPath": "credentials.authorization", "secretId": "protected-id", "versionSelector": "latest"}}) {
		t.Fatalf("incorrect protected reference projection: %v %v", refs, err)
	}
	for _, invalid := range [][]map[string]interface{}{{{"name": ""}}, {{"name": "a", "secretId": "id"}, {"name": "a", "secretId": "id"}}} {
		if _, err := ConnectionCredentialSecretRefs(invalid); err == nil {
			t.Fatal("invalid credential projection accepted")
		}
	}
	versioned, err := ConnectionCredentialSecretRefs([]map[string]interface{}{{"name": "authorization", "secretId": "protected-id", "version": float64(3)}})
	if err != nil || versioned[0]["versionSelector"] != float64(3) {
		t.Fatal("credential version was lost")
	}
	qualified, err := ConnectionCredentialSecretRefs([]map[string]interface{}{{"name": "credentials.authorization", "secretId": "protected-id"}})
	if err != nil || qualified[0]["configPath"] != "credentials.authorization" {
		t.Fatal("qualified credential path was prefixed again")
	}
	if _, err := ConnectionCredentialSecretRefs([]map[string]interface{}{{"name": "authorization", "secretId": "first"}, {"name": "credentials.authorization", "secretId": "second"}}); err == nil {
		t.Fatal("colliding native credential paths were accepted")
	}
	for _, mode := range []string{"success", "read_error", "install_error", "stale", "revoke_error", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			grant := PaperclipConnectionGrant{ID: "old", Kind: "organization", Status: "active", IsDefault: true, CredentialSecretRefs: []map[string]interface{}{}}
			var superseded *PaperclipConnectionGrant
			installs, revokes := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/api/tool-connections/conn/grants":
					if mode == "read_error" {
						http.Error(w, "fixture read failure", 500)
						return
					}
					grants := []PaperclipConnectionGrant{grant}
					if superseded != nil {
						grants = append(grants, *superseded)
					}
					if mode == "duplicate" {
						grants = append(grants, grant)
					}
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"grants": grants})
				case r.Method == http.MethodPost && r.URL.Path == "/api/tool-connections/conn/grants/installations":
					if mode == "install_error" {
						http.Error(w, "fixture install failure", 500)
						return
					}
					var input PaperclipConnectionGrant
					if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
						t.Error(err)
					}
					if !input.IsDefault || !reflect.DeepEqual(input.CredentialSecretRefs, refs) {
						t.Error("installation did not preserve exact protected references")
					}
					installs++
					if mode != "stale" {
						old := grant
						old.IsDefault = false
						superseded = &old
						grant.ID = "new"
						grant.CredentialSecretRefs = input.CredentialSecretRefs
					}
					_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "new"})
				case r.Method == http.MethodDelete && r.URL.Path == "/api/tool-connections/conn/grants/old":
					if mode == "revoke_error" {
						http.Error(w, "fixture revoke failure", 500)
						return
					}
					revokes++
					superseded.Status = "revoked"
					_, _ = w.Write([]byte(`{}`))
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			cli, _ := NewClient(server.URL, "fixture-token")
			err := cli.EnsureConnectionCredentialGrant(context.Background(), "conn", refs)
			if mode != "success" {
				if err == nil {
					t.Fatal("native grant failure was ignored")
				}
				if mode == "revoke_error" {
					mode = "success"
					if err := cli.EnsureConnectionCredentialGrant(context.Background(), "conn", refs); err != nil || installs != 1 || revokes != 1 {
						t.Fatal("retry did not finish revocation without creating another grant")
					}
				}
				return
			}
			if err != nil || installs != 1 || revokes != 1 {
				t.Fatalf("grant was not replaced: %v %d %d", err, installs, revokes)
			}
			if err := cli.EnsureConnectionCredentialGrant(context.Background(), "conn", refs); err != nil || installs != 1 || revokes != 1 {
				t.Fatal("converged grant was changed again")
			}
		})
	}
}

func TestAppPermissionSelection(t *testing.T) {
	catalog := []map[string]interface{}{
		{"id": "c-read", "toolName": "read", "isReadOnly": true, "status": "active"},
		{"id": "c-write", "toolName": "write", "isWrite": true, "status": "quarantined"},
		{"id": "c-unknown", "toolName": "unknown", "status": "active"},
		{"id": "c-conflict", "toolName": "conflict", "isReadOnly": true, "isDestructive": true, "status": "active"},
	}
	for _, test := range []struct {
		name         string
		policy       string
		enabled, ask []string
	}{
		{"default", `{"approval_mode":"writes"}`, []string{"c-conflict", "c-read", "c-unknown", "c-write"}, []string{"c-conflict", "c-unknown", "c-write"}},
		{"selection", `{"approval_mode":"writes","enabled_tools":["read","write"]}`, []string{"c-read", "c-write"}, []string{"c-write"}},
		{"empty", `{"approval_mode":"writes","enabled_tools":[]}`, []string{}, []string{}},
		{"overrides", `{"approval_mode":"writes","tool_approval_modes":{"read":"prompt","write":"auto","unknown":"approve","conflict":"writes"}}`, []string{"c-conflict", "c-read", "c-unknown", "c-write"}, []string{"c-conflict", "c-read"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			finish, err := SelectAppPermissions(catalog, test.policy, []string{"agent-1"})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(finish.EnabledCatalogEntryIDs, test.enabled) || !reflect.DeepEqual(finish.AskFirstCatalogEntryIDs, test.ask) || !reflect.DeepEqual(finish.ReviewedCatalogEntryIDs, []string{"c-write"}) {
				t.Fatalf("unexpected permissions: %#v", finish)
			}
			body, err := json.Marshal(finish)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(body), "null") {
				t.Fatalf("null permission array: %s", body)
			}
		})
	}
	for _, policy := range []string{
		`broken`, `{"approval_mode":2}`, `{"approval_mode":"auto"}`,
		`{"approval_mode":"writes","enabled_toolz":[]}`,
		`{"approval_mode":"writes","enabled_tools":[""]}`,
		`{"approval_mode":"writes","enabled_tools":["read","read"]}`,
		`{"approval_mode":"writes","enabled_tools":["absent"]}`,
		`{"approval_mode":"writes","tool_approval_modes":{"read":"invalid"}}`,
		`{"approval_mode":"writes","tool_approval_modes":{"absent":"auto"}}`,
	} {
		if _, err := SelectAppPermissions(catalog, policy, nil); err == nil {
			t.Fatalf("accepted %s", policy)
		}
	}
	for _, malformed := range [][]map[string]interface{}{
		nil,
		{{"id": "c", "toolName": "read", "status": "disabled"}},
		{{"id": "", "toolName": "read"}},
		{{"id": "c"}},
		{{"id": "c", "toolName": "read"}, {"id": "c", "toolName": "other"}},
		{{"id": "c", "toolName": "read"}, {"id": "other", "toolName": "read"}},
	} {
		if _, err := SelectAppPermissions(malformed, `{"approval_mode":"writes"}`, nil); err == nil {
			t.Fatalf("accepted malformed catalog: %#v", malformed)
		}
	}
}

func TestPolicyReadUsesCompanyInventoryAndPreservesFailures(t *testing.T) {
	for _, mode := range []string{"present", "absent", "namesake", "wrong-company", "server-error", "forbidden"} {
		t.Run(mode, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != http.MethodGet || r.URL.Path != "/api/companies/company/tools/policies" {
					t.Fatalf("unexpected policy request: %s %s", r.Method, r.URL.Path)
				}
				if mode == "server-error" || mode == "forbidden" {
					status := http.StatusInternalServerError
					if mode == "forbidden" {
						status = http.StatusForbidden
					}
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"error":"unavailable"}`))
					return
				}
				policies := []map[string]interface{}{}
				if mode != "absent" {
					policy := map[string]interface{}{"id": "policy", "companyId": "company", "name": "policy"}
					if mode == "namesake" {
						policy["id"] = "other"
					}
					if mode == "wrong-company" {
						policy["companyId"] = "other"
					}
					policies = append(policies, policy)
				}
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"policies": policies})
			}))
			defer server.Close()
			cli, _ := NewClient(server.URL, "fixture-token")
			policy, err := cli.GetToolPolicy(context.Background(), "company", "policy")
			if requests != 1 {
				t.Fatalf("expected one authoritative read, got %d", requests)
			}
			switch mode {
			case "present":
				if err != nil || policy.ID != "policy" {
					t.Fatalf("policy read failed: %v %v", policy, err)
				}
			case "absent", "namesake":
				if !errors.Is(err, ErrNotFound) {
					t.Fatalf("missing exact policy ID was not absent: %v", err)
				}
			default:
				if err == nil || errors.Is(err, ErrNotFound) {
					t.Fatalf("read failure was mistaken for absence: %v", err)
				}
			}
			for _, ids := range [][2]string{{"", "policy"}, {"company", ""}} {
				if _, err := cli.GetToolPolicy(context.Background(), ids[0], ids[1]); err == nil {
					t.Fatal("incomplete policy identity accepted")
				}
			}
		})
	}
}

func TestAppPermissionNativeReadAndWireArrays(t *testing.T) {
	profile := map[string]interface{}{
		"profileKey": "app:conn", "defaultAction": "deny", "status": "active",
		"entries":  []map[string]interface{}{{"catalogEntryId": "cat-read", "selectorType": "catalog_entry", "effect": "include", "connectionId": "conn"}},
		"bindings": []map[string]interface{}{{"targetType": "agent", "targetId": "agent-1"}},
	}
	profiles := []map[string]interface{}{profile}
	policies := []map[string]interface{}{
		{
			"id":         "pol-read",
			"enabled":    true,
			"policyType": "require_approval",
			"selectors":  map[string]interface{}{"catalogEntryIds": []interface{}{"cat-read"}, "agentIds": []interface{}{"agent-1"}},
		},
		{"id": "pol-off", "enabled": false, "policyType": "require_approval", "selectors": map[string]interface{}{"catalogEntryId": "cat-read"}},
		{"id": "pol-foreign", "enabled": true, "policyType": "require_approval", "selectors": map[string]interface{}{"catalogEntryIds": []interface{}{"cat-other-connection"}}},
	}
	failPath := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if req.URL.Path == failPath {
			http.Error(w, "unavailable", 503)
			return
		}
		switch req.URL.Path {
		case "/api/tool-connections/conn/catalog":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"catalog": []map[string]interface{}{{"id": "cat-read", "toolName": "read"}}})
		case "/api/companies/company/tools/profiles":
			if req.Method != http.MethodGet {
				t.Error("profile mutation")
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"profiles": profiles})
		case "/api/companies/company/tools/policies":
			if req.Method != http.MethodGet {
				t.Error("policy mutation")
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"policies": policies})
		case "/api/tool-connections/conn/catalog/refresh":
			if req.Method != http.MethodPost {
				t.Error("invalid refresh method")
			}
			_, _ = w.Write([]byte(`{}`))
		case "/api/companies/company/tools/apps/conn/finish":
			var body map[string]interface{}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			for _, key := range []string{"enabledCatalogEntryIds", "askFirstCatalogEntryIds", "reviewedCatalogEntryIds"} {
				if _, ok := body[key].([]interface{}); !ok {
					t.Errorf("%s is not an array", key)
				}
			}
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()
	cli, _ := NewClient(server.URL, "fixture-token")
	ctx := context.Background()
	actual, err := cli.GetAppPermissionState(ctx, "company", "conn")
	if err != nil {
		t.Fatal(err)
	}
	if actual.AccessType != "agents" || !reflect.DeepEqual(actual.EnabledIDs, []string{"cat-read"}) || !reflect.DeepEqual(actual.AskFirstIDs, []string{"cat-read"}) || len(actual.StrayPolicyIDs) != 0 || !reflect.DeepEqual(actual.AgentIDs, []string{"agent-1"}) {
		t.Fatalf("unexpected native state: %#v", actual)
	}
	// Any other enabled approval policy on the connection's entries offers the
	// tool beyond the granted agents or duplicates the connection's one policy,
	// so it is stray rather than ask-first.
	for name, stray := range map[string]map[string]interface{}{
		"unscoped":       {"selectors": map[string]interface{}{"catalogEntryIds": []interface{}{"cat-read"}}},
		"other agents":   {"selectors": map[string]interface{}{"catalogEntryIds": []interface{}{"cat-read"}, "agentIds": []interface{}{"other-agent"}}},
		"extra":          {"selectors": map[string]interface{}{"catalogEntryIds": []interface{}{"cat-read"}, "agentIds": []interface{}{"agent-1"}, "projectId": "project-1"}},
		"per entry":      {"selectors": map[string]interface{}{"catalogEntryId": "cat-read", "agentIds": []interface{}{"agent-1"}}},
		"foreign entry":  {"selectors": map[string]interface{}{"catalogEntryIds": []interface{}{"cat-read", "cat-other-connection"}, "agentIds": []interface{}{"agent-1"}}},
		"repeated entry": {"selectors": map[string]interface{}{"catalogEntryIds": []interface{}{"cat-read", "cat-read"}, "agentIds": []interface{}{"agent-1"}}},
		"finish-owned":   {"config": map[string]interface{}{"source": "app_gallery_finish", "connectionId": "conn", "catalogEntryId": "cat-read"}},
	} {
		saved := map[string]interface{}{"selectors": policies[0]["selectors"], "config": policies[0]["config"]}
		for key, value := range stray {
			policies[0][key] = value
		}
		actual, err = cli.GetAppPermissionState(ctx, "company", "conn")
		if err != nil || len(actual.AskFirstIDs) != 0 || !reflect.DeepEqual(actual.StrayPolicyIDs, []string{"pol-read"}) {
			t.Fatalf("%s policy was not stray: %#v %v", name, actual, err)
		}
		policies[0]["selectors"], policies[0]["config"] = saved["selectors"], saved["config"]
	}
	duplicate := map[string]interface{}{"id": "pol-duplicate", "enabled": true, "policyType": "require_approval", "selectors": policies[0]["selectors"]}
	policies = append(policies, duplicate)
	actual, err = cli.GetAppPermissionState(ctx, "company", "conn")
	if err != nil || !reflect.DeepEqual(actual.AskFirstIDs, []string{"cat-read"}) || !reflect.DeepEqual(actual.StrayPolicyIDs, []string{"pol-duplicate"}) {
		t.Fatalf("duplicate ask-first policy was not stray: %#v %v", actual, err)
	}
	policies = policies[:len(policies)-1]

	agentBindings := profile["bindings"]
	profile["bindings"] = []map[string]interface{}{{"targetType": "company", "targetId": "company"}}
	actual, err = cli.GetAppPermissionState(ctx, "company", "conn")
	if err != nil || actual.AccessType != "all_agents" || len(actual.AgentIDs) != 0 || !reflect.DeepEqual(actual.StrayPolicyIDs, []string{"pol-read"}) {
		t.Fatalf("company scope not read truthfully: %#v %v", actual, err)
	}
	policies[0]["selectors"] = map[string]interface{}{"catalogEntryIds": []interface{}{"cat-read"}}
	actual, err = cli.GetAppPermissionState(ctx, "company", "conn")
	if err != nil || !reflect.DeepEqual(actual.AskFirstIDs, []string{"cat-read"}) || len(actual.StrayPolicyIDs) != 0 {
		t.Fatalf("company-wide ask-first policy was not read: %#v %v", actual, err)
	}
	policies[0]["selectors"] = map[string]interface{}{"catalogEntryIds": []interface{}{"cat-read"}, "agentIds": []interface{}{"agent-1"}}
	for _, bindings := range [][]map[string]interface{}{
		{{"targetType": "company", "targetId": "company"}, {"targetType": "agent", "targetId": "agent-1"}},
		{{"targetType": "agent", "targetId": "agent-1"}, {"targetType": "company", "targetId": "company"}},
		{{"targetType": "gateway", "targetId": "gateway-1"}, {"targetType": "company", "targetId": "company"}, {"targetType": "agent", "targetId": "agent-1"}},
	} {
		profile["bindings"] = bindings
		actual, err = cli.GetAppPermissionState(ctx, "company", "conn")
		if err != nil || actual.AccessType != "all_agents" || !reflect.DeepEqual(actual.AgentIDs, []string{"agent-1"}) {
			t.Fatalf("mixed native scope not read truthfully: %#v %v", actual, err)
		}
	}
	for _, bindings := range [][]map[string]interface{}{
		{{"targetType": "gateway", "targetId": "gateway-1", "metadata": map[string]interface{}{"source": "named_mcp_gateway"}}},
		{{"targetType": "gateway", "targetId": "gateway-1"}, {"targetType": "agent", "targetId": "agent-1"}},
	} {
		profile["bindings"] = bindings
		actual, err = cli.GetAppPermissionState(ctx, "company", "conn")
		if err != nil || actual.AccessType != "agents" || len(actual.AgentIDs) != len(bindings)-1 {
			t.Fatalf("runtime gateway affected managed agent access: %#v %v", actual, err)
		}
		if len(actual.AgentIDs) == 1 && actual.AgentIDs[0] != "agent-1" {
			t.Fatalf("runtime gateway was read as an agent: %#v", actual)
		}
	}
	profile["bindings"] = agentBindings
	if err := cli.RefreshConnectionCatalog(ctx, "conn"); err != nil {
		t.Fatal(err)
	}
	finish, _ := SelectAppPermissions([]map[string]interface{}{{"id": "c", "toolName": "read"}}, `{"approval_mode":"writes","enabled_tools":[]}`, nil)
	if _, err := cli.FinishAppPermissions(ctx, "company", "conn", finish); err != nil {
		t.Fatal(err)
	}
	// Without a native profile no agent has App access, yet an approval policy
	// on the connection's entries still offers its tool and stays visible.
	profiles = nil
	actual, err = cli.GetAppPermissionState(ctx, "company", "conn")
	if err != nil || actual.Finished || len(actual.EnabledIDs) != 0 || len(actual.AgentIDs) != 0 || !reflect.DeepEqual(actual.StrayPolicyIDs, []string{"pol-read"}) {
		t.Fatalf("missing profile not read as zero access: %#v %v", actual, err)
	}
	profiles = []map[string]interface{}{profile, profile}
	if _, err := cli.GetAppPermissionState(ctx, "company", "conn"); err == nil {
		t.Fatal("duplicate profile accepted")
	}
	profiles = []map[string]interface{}{profile}
	for _, change := range []struct {
		key   string
		value interface{}
	}{
		{"defaultAction", "allow"},
		{"entries", []map[string]interface{}{{"selectorType": "connection"}}},
		{"bindings", []map[string]interface{}{{"targetType": "company", "targetId": "other-company"}}},
		{"bindings", []map[string]interface{}{{"targetType": "agent"}}},
		{"bindings", []map[string]interface{}{{"targetType": "gateway"}}},
		{"bindings", []map[string]interface{}{{"targetType": "project", "targetId": "project-1"}}},
	} {
		old := profile[change.key]
		profile[change.key] = change.value
		if _, err := cli.GetAppPermissionState(ctx, "company", "conn"); err == nil {
			t.Fatalf("invalid %s accepted", change.key)
		}
		profile[change.key] = old
	}
	profile["entries"] = []map[string]interface{}{{"selectorType": "connection"}}
	if _, err := cli.GetAppPermissionState(ctx, "company", "conn"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unfinished connection profile was not reported as ErrNotFound: %v", err)
	}
	profile["entries"] = []map[string]interface{}{{"catalogEntryId": "cat-read", "selectorType": "catalog_entry", "effect": "include", "connectionId": "conn"}}
	profile["status"] = "archived"
	actual, err = cli.GetAppPermissionState(ctx, "company", "conn")
	if err != nil || len(actual.EnabledIDs) != 0 {
		t.Fatalf("inactive profile grants tools: %#v %v", actual, err)
	}
	for _, endpoint := range []string{"/api/companies/company/tools/profiles", "/api/tool-connections/conn/catalog", "/api/companies/company/tools/policies"} {
		failPath = endpoint
		if _, err := cli.GetAppPermissionState(ctx, "company", "conn"); err == nil {
			t.Fatalf("ignored read failure: %s", endpoint)
		}
	}
}

func TestExtractItems(t *testing.T) {
	// Array case
	arr := []interface{}{
		map[string]interface{}{"id": "1"},
		map[string]interface{}{"id": "2"},
	}
	items := extractItems(arr, "items")
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}

	// Map with key case
	m1 := map[string]interface{}{
		"companies": []interface{}{
			map[string]interface{}{"id": "c1"},
		},
	}
	items = extractItems(m1, "companies")
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	// Map with items case
	m2 := map[string]interface{}{
		"items": []interface{}{
			map[string]interface{}{"id": "s1"},
		},
	}
	items = extractItems(m2, "skills")
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	// Map with data case
	m3 := map[string]interface{}{
		"data": []interface{}{
			map[string]interface{}{"id": "d1"},
		},
	}
	items = extractItems(m3, "anything")
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
}

func TestCreateBoardKey(t *testing.T) {
	t.Run("creates and verifies through unauthenticated local challenge", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch r.URL.Path {
			case "/api/cli-auth/challenges":
				if r.Header.Get("Authorization") != "" {
					t.Fatal("challenge must omit a stale bearer")
				}
				var body map[string]interface{}
				_ = json.NewDecoder(r.Body).Decode(&body)
				if body["clientName"] != "Harness OpenTofu" || body["requestedAccess"] != "board" {
					t.Fatalf("unexpected challenge body: %#v", body)
				}
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"id": "challenge-1", "token": "challenge-secret", "boardApiToken": "pcp_board_secret",
				})
			case "/api/cli-auth/challenges/challenge-1/approve":
				if r.Header.Get("Authorization") != "" {
					t.Fatal("approval must omit a stale bearer")
				}
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"approved": true, "status": "approved", "keyId": "key-1",
				})
			case "/api/cli-auth/me":
				if r.Header.Get("Authorization") != "Bearer pcp_board_secret" {
					t.Fatalf("generated key was not verified: %q", r.Header.Get("Authorization"))
				}
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"source": "board_key", "keyId": "key-1",
				})
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer server.Close()

		cli, _ := NewClient(server.URL, "stale-token")
		key, err := cli.CreateBoardKey(context.Background(), "Harness OpenTofu")
		if err != nil {
			t.Fatal(err)
		}
		if key.ID != "key-1" || key.Name != "Harness OpenTofu" || key.Token != "pcp_board_secret" {
			t.Fatalf("unexpected board key: %#v", key)
		}
	})

	tests := map[string]struct {
		challenge string
		approval  string
		identity  string
	}{
		"incomplete challenge": {challenge: `{}`},
		"rejected approval": {
			challenge: `{"id":"challenge-1","token":"secret","boardApiToken":"board"}`,
			approval:  `{"approved":false,"status":"pending"}`,
		},
		"wrong identity": {
			challenge: `{"id":"challenge-1","token":"secret","boardApiToken":"board"}`,
			approval:  `{"approved":true,"status":"approved","keyId":"key-1"}`,
			identity:  `{"source":"session","keyId":"key-1"}`,
		},
	}
	for name, fixture := range tests {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/api/cli-auth/challenges":
					_, _ = w.Write([]byte(fixture.challenge))
				case "/api/cli-auth/challenges/challenge-1/approve":
					_, _ = w.Write([]byte(fixture.approval))
				case "/api/cli-auth/me":
					_, _ = w.Write([]byte(fixture.identity))
				}
			}))
			defer server.Close()
			cli, _ := NewClient(server.URL, "")
			if _, err := cli.CreateBoardKey(context.Background(), "Harness OpenTofu"); err == nil {
				t.Fatal("expected board key creation failure")
			}
		})
	}
}

func TestPaperclipClientCRUD(t *testing.T) {
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		// Auth header check
		if r.Header.Get("Authorization") != "Bearer test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error": "unauthorized"}`))
			return
		}

		switch {
		case r.URL.Path == "/api/companies" && r.Method == http.MethodPost:
			var req map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&req)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":                           "comp-123",
				"name":                         req["name"],
				"description":                  req["description"],
				"requireTaskStartConfirmation": req["requireTaskStartConfirmation"],
			})
		case r.URL.Path == "/api/companies/comp-123" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":                           "comp-123",
				"name":                         "guseynov",
				"description":                  "desc",
				"requireTaskStartConfirmation": true,
			})
		case r.URL.Path == "/api/companies/comp-123" && r.Method == http.MethodPatch:
			var req map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&req)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":                           "comp-123",
				"name":                         req["name"],
				"description":                  req["description"],
				"requireTaskStartConfirmation": req["requireTaskStartConfirmation"],
			})
		case r.URL.Path == "/api/companies/comp-123/archive" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/api/companies" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"companies": []map[string]interface{}{
					{"id": "comp-123", "name": "guseynov", "requireTaskStartConfirmation": true},
				},
			})
		case r.URL.Path == "/api/companies/comp-123/projects" && r.Method == http.MethodPost:
			var req map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if _, exists := req["leadAgentId"]; exists {
				t.Error("project creation must not own the lead assignment")
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":                       "proj-123",
				"companyId":                "comp-123",
				"name":                     req["name"],
				"description":              req["description"],
				"leadAgentId":              req["leadAgentId"],
				"executionWorkspacePolicy": req["executionWorkspacePolicy"],
			})
		case r.URL.Path == "/api/projects/proj-123" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":          "proj-123",
				"companyId":   "comp-123",
				"name":        "harness",
				"description": "desc",
				"executionWorkspacePolicy": map[string]interface{}{
					"enabled": true,
				},
			})
		case r.URL.Path == "/api/projects/proj-123" && r.Method == http.MethodPatch:
			var req map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if _, exists := req["leadAgentId"]; exists {
				t.Error("project settings update must preserve the separately owned lead")
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":                       "proj-123",
				"companyId":                "comp-123",
				"name":                     req["name"],
				"description":              req["description"],
				"executionWorkspacePolicy": req["executionWorkspacePolicy"],
			})
		case r.URL.Path == "/api/projects/proj-123" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/api/companies/comp-123/projects" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "proj-123", "companyId": "comp-123", "name": "harness"},
			})
		case r.URL.Path == "/api/projects/proj-123/workspaces" && r.Method == http.MethodPost:
			var req map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&req)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":         "ws-123",
				"projectId":  "proj-123",
				"name":       req["name"],
				"repoUrl":    req["repoUrl"],
				"repoRef":    req["repoRef"],
				"defaultRef": req["defaultRef"],
				"isPrimary":  req["isPrimary"],
				"sourceType": req["sourceType"],
			})
		case r.URL.Path == "/api/projects/proj-123/workspaces/ws-123" && r.Method == http.MethodPatch:
			var req map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&req)
			_ = json.NewEncoder(w).Encode(req)
		case r.URL.Path == "/api/projects/proj-123/workspaces/ws-123" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/api/projects/proj-123/workspaces" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "ws-123", "projectId": "proj-123", "name": "primary"},
			})
		case r.URL.Path == "/api/companies/comp-123/skills" && r.Method == http.MethodPost:
			var req map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&req)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":          "skill-123",
				"companyId":   "comp-123",
				"name":        req["name"],
				"slug":        req["slug"],
				"description": req["description"],
				"markdown":    req["content"],
			})
		case r.URL.Path == "/api/companies/comp-123/skills/skill-123" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":        "skill-123",
				"companyId": "comp-123",
				"name":      "fleet-task",
				"slug":      "fleet-task",
				"markdown":  "# Fleet Task",
			})
		case r.URL.Path == "/api/companies/comp-123/skills/skill-123" && r.Method == http.MethodPatch:
			var req map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if _, ok := req["description"]; !ok {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "skill-123", "description": req["description"]})
		case r.URL.Path == "/api/companies/comp-123/skills/skill-123/files" && r.Method == http.MethodPatch:
			var req map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req["path"] != "SKILL.md" || req["content"] == nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"skillId": "skill-123", "path": "SKILL.md", "content": req["content"]})
		case r.URL.Path == "/api/companies/comp-123/skills/skill-123" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/api/companies/comp-123/skills" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "skill-123", "companyId": "comp-123", "name": "fleet-task", "slug": "fleet-task"},
			})
		case r.URL.Path == "/api/companies/comp-123/secrets" && r.Method == http.MethodPost:
			var req map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&req)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":          "sec-123",
				"companyId":   "comp-123",
				"name":        req["name"],
				"description": req["description"],
			})
		case r.URL.Path == "/api/secrets/sec-123" && r.Method == http.MethodPatch:
			var req map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if _, present := req["value"]; present {
				t.Fatal("secret metadata update must not carry a value")
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "sec-123", "companyId": "comp-123", "name": req["name"], "description": req["description"],
			})
		case r.URL.Path == "/api/secrets/sec-123/rotate" && r.Method == http.MethodPost:
			var req map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if len(req) != 1 || req["value"] != "val2" {
				t.Fatalf("secret rotation must carry only the new value: %#v", req)
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "sec-123", "companyId": "comp-123", "name": "test-sec", "description": "desc2",
			})
		case r.URL.Path == "/api/secrets/sec-123" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/api/companies/comp-123/secrets" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "sec-123", "companyId": "comp-123", "name": "test-sec"},
			})
		case r.URL.Path == "/api/companies/comp-123/agents" && r.Method == http.MethodPost:
			var req map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&req)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":          "agent-123",
				"companyId":   "comp-123",
				"name":        req["name"],
				"role":        req["role"],
				"adapterType": req["adapterType"],
			})
		case r.URL.Path == "/api/agents/agent-123" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":          "agent-123",
				"companyId":   "comp-123",
				"name":        "engineer",
				"role":        "engineer",
				"adapterType": "antigravity_local",
				"permissions": map[string]interface{}{"canCreateAgents": false, "canCreateSkills": true, "canAssignTasks": true},
			})
		case r.URL.Path == "/api/agents/agent-123/permissions" && r.Method == http.MethodPatch:
			var req map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&req)
			// The route replaces every flag, so the write carries all three.
			if len(req) != 3 || req["canCreateAgents"] != false || req["canCreateSkills"] != true || req["canAssignTasks"] != false {
				t.Errorf("permissions update must carry every flag with the declared task assignment: %#v", req)
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "agent-123"})
		case r.URL.Path == "/api/agents/agent-123" && r.Method == http.MethodPatch:
			var req map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&req)
			_ = json.NewEncoder(w).Encode(req)
		case r.URL.Path == "/api/agents/agent-123" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/api/companies/comp-123/agents" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "agent-123", "companyId": "comp-123", "name": "engineer", "role": "engineer"},
			})
		case r.URL.Path == "/api/agents/agent-123/instructions-bundle/file" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"path":    "AGENTS.md",
				"content": "# Instructions",
			})
		case r.URL.Path == "/api/agents/agent-123/instructions-bundle/file" && r.Method == http.MethodPut:
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/api/agents/agent-123/keys" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":        "key-123",
				"agentId":   "agent-123",
				"name":      "harness-mcp",
				"token":     "tok-12345",
				"createdAt": "2026-08-22T00:00:00Z",
			})
		case r.URL.Path == "/api/companies/comp-123/tools/applications" && r.Method == http.MethodPost:
			var req map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&req)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":        "app-123",
				"companyId": "comp-123",
				"name":      req["name"],
				"type":      req["type"],
			})
		case r.URL.Path == "/api/tool-applications/app-123" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":        "app-123",
				"companyId": "comp-123",
				"name":      "harness.mcp.openobserve",
				"type":      "mcp_http",
			})
		case r.URL.Path == "/api/tool-applications/app-123" && r.Method == http.MethodPatch:
			var req map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&req)
			_ = json.NewEncoder(w).Encode(req)
		case r.URL.Path == "/api/companies/comp-123/tools/applications" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "app-123", "companyId": "comp-123", "name": "harness.mcp.openobserve", "type": "mcp_http"},
			})
		case r.URL.Path == "/api/companies/comp-123/tools/connections" && r.Method == http.MethodPost:
			var req map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if req["applicationId"] != "app-123" {
				t.Errorf("connection create applicationId = %v, want app-123", req["applicationId"])
			}
			if _, exists := req["appId"]; exists {
				t.Errorf("connection create sent obsolete appId: %v", req["appId"])
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":            "conn-123",
				"companyId":     "comp-123",
				"applicationId": req["applicationId"],
				"name":          req["name"],
			})
		case r.URL.Path == "/api/tool-connections/conn-123" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":            "conn-123",
				"companyId":     "comp-123",
				"applicationId": "app-123",
				"name":          "openobserve-connection",
			})
		case r.URL.Path == "/api/tool-connections/conn-123" && r.Method == http.MethodPatch:
			var req map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&req)
			if value, exists := req["applicationId"]; exists {
				t.Errorf("connection update sent immutable applicationId: %v", value)
			}
			if value, exists := req["appId"]; exists {
				t.Errorf("connection update sent obsolete appId: %v", value)
			}
			_ = json.NewEncoder(w).Encode(req)
		case r.URL.Path == "/api/tool-connections/conn-123" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/api/companies/comp-123/tools/connections" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "conn-123", "companyId": "comp-123", "applicationId": "app-123", "name": "openobserve-connection"},
			})
		case r.URL.Path == "/api/companies/comp-123/tools/policies" && r.Method == http.MethodPost:
			var req map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&req)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":        "pol-123",
				"companyId": "comp-123",
				"name":      req["name"],
			})
		case r.URL.Path == "/api/tool-policies/pol-123" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":        "pol-123",
				"companyId": "comp-123",
				"name":      "default-policy",
			})
		case r.URL.Path == "/api/tool-policies/pol-123" && r.Method == http.MethodPatch:
			var req map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&req)
			_ = json.NewEncoder(w).Encode(req)
		case r.URL.Path == "/api/tool-policies/pol-123" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/api/companies/comp-123/tools/policies" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "pol-123", "companyId": "comp-123", "name": "default-policy"},
			})
		case r.URL.Path == "/api/companies/comp-123/tools/policies/pol-123" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":        "pol-123",
				"companyId": "comp-123",
				"name":      "default-policy",
			})
		case r.URL.Path == "/api/companies/comp-123/tools/policies/pol-123" && r.Method == http.MethodPatch:
			var req map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&req)
			_ = json.NewEncoder(w).Encode(req)
		case r.URL.Path == "/api/companies/comp-123/tools/policies/pol-123" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/api/companies/comp-123/tools/stdio-templates" && r.Method == http.MethodPost:
			var req map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&req)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":          "tpl-123",
				"companyId":   "comp-123",
				"templateId":  req["templateId"],
				"name":        req["name"],
				"description": req["description"],
				"command":     req["command"],
				"args":        req["args"],
				"envKeys":     req["envKeys"],
				"status":      "active",
			})
		case r.URL.Path == "/api/companies/comp-123/tools/stdio-templates" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{
					"id":          "tpl-123",
					"companyId":   "comp-123",
					"templateId":  "harness.mcp.devenv",
					"name":        "devenv",
					"description": "Devenv MCP",
					"command":     "nix",
					"args":        []string{"run"},
					"envKeys":     []string{"DEVENV_TOKEN"},
					"status":      "active",
				},
			})
		case r.URL.Path == "/api/companies/comp-123/tools/stdio-templates/harness.mcp.devenv/disable" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/api/tool-connections/conn-123/installs" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"installs": []map[string]interface{}{
					{"targetType": "agent", "targetId": "agent-123"},
				},
			})
		case r.URL.Path == "/api/tool-connections/conn-123/installs" && r.Method == http.MethodPut:
			var req map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&req)
			_ = json.NewEncoder(w).Encode(req)
		case r.URL.Path == "/api/tool-connections/conn-123/catalog" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "cat-1", "name": "run_qa"},
			})
		case r.URL.Path == "/api/companies/comp-123/tools/apps/conn-123/finish" && r.Method == http.MethodPost:
			var req map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&req)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"status": "ok",
			})
		case r.URL.Path == "/api/not-found":
			w.WriteHeader(http.StatusNotFound)
		case r.URL.Path == "/api/conflict":
			w.WriteHeader(http.StatusConflict)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cli, err := NewClient(server.URL, "test-token")
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	// 1. Company
	comp, err := cli.CreateCompany(ctx, "guseynov", "desc", true)
	if err != nil || comp.ID != "comp-123" || !comp.RequireTaskStartConfirmation {
		t.Fatalf("CreateCompany failed: %v", err)
	}
	comp, err = cli.GetCompany(ctx, "comp-123")
	if err != nil || comp.Name != "guseynov" || !comp.RequireTaskStartConfirmation {
		t.Fatalf("GetCompany failed: %v", err)
	}
	comp, err = cli.FindCompanyByName(ctx, "guseynov")
	if err != nil || comp.ID != "comp-123" || !comp.RequireTaskStartConfirmation {
		t.Fatalf("FindCompanyByName failed: %v", err)
	}
	comp, err = cli.UpdateCompany(ctx, "comp-123", "guseynov2", "desc2", true)
	if err != nil || !comp.RequireTaskStartConfirmation {
		t.Fatalf("UpdateCompany failed: %v", err)
	}
	comps, err := cli.ListCompanies(ctx)
	if err != nil || len(comps) != 1 {
		t.Fatalf("ListCompanies failed: %v", err)
	}
	if err := cli.ArchiveCompany(ctx, "comp-123"); err != nil {
		t.Fatalf("ArchiveCompany failed: %v", err)
	}

	// 2. Project
	policy := map[string]interface{}{
		"enabled":     true,
		"defaultMode": "shared_workspace",
		"authorizationPolicy": map[string]interface{}{
			"assignmentPolicy": map[string]interface{}{"mode": "protected"},
		},
	}
	proj, err := cli.CreateProject(ctx, "comp-123", "harness", "desc", policy)
	if err != nil || proj.ID != "proj-123" {
		t.Fatalf("CreateProject failed: %v", err)
	}
	if proj.ExecutionWorkspacePolicy["defaultMode"] != "shared_workspace" {
		t.Fatalf("CreateProject omitted the execution workspace policy")
	}
	if !reflect.DeepEqual(proj.ExecutionWorkspacePolicy, policy) {
		t.Fatalf("CreateProject changed the native authorization policy: %#v", proj.ExecutionWorkspacePolicy)
	}
	proj, err = cli.GetProject(ctx, "proj-123")
	if err != nil || proj.Name != "harness" {
		t.Fatalf("GetProject failed: %v", err)
	}
	proj, err = cli.UpdateProject(ctx, "proj-123", "harness2", "desc2", policy)
	if err != nil {
		t.Fatalf("UpdateProject failed: %v", err)
	}
	if !reflect.DeepEqual(proj.ExecutionWorkspacePolicy, policy) {
		t.Fatalf("UpdateProject changed the native authorization policy: %#v", proj.ExecutionWorkspacePolicy)
	}
	projs, err := cli.ListProjects(ctx, "comp-123")
	if err != nil || len(projs) != 1 {
		t.Fatalf("ListProjects failed: %v", err)
	}
	if err := cli.DeleteProject(ctx, "proj-123"); err != nil {
		t.Fatalf("DeleteProject failed: %v", err)
	}

	// 3. Workspace
	ws, err := cli.CreateWorkspace(ctx, "proj-123", PaperclipWorkspace{Name: "primary", RepoURL: "https://git.example.com/repo.git", RepoRef: "develop", DefaultRef: "develop"})
	if err != nil || ws.ID != "ws-123" || ws.RepoRef != "develop" || ws.DefaultRef != "develop" {
		t.Fatalf("CreateWorkspace failed: %v %#v", err, ws)
	}
	ws, err = cli.GetWorkspaceInProject(ctx, "proj-123", "ws-123")
	if err != nil || ws.Name != "primary" {
		t.Fatalf("GetWorkspaceInProject failed: %v", err)
	}
	ws, err = cli.UpdateWorkspace(ctx, "proj-123", "ws-123", *ws)
	if err != nil {
		t.Fatalf("UpdateWorkspace failed: %v", err)
	}
	wss, err := cli.ListWorkspaces(ctx, "proj-123")
	if err != nil || len(wss) != 1 {
		t.Fatalf("ListWorkspaces failed: %v", err)
	}
	if err := cli.DeleteWorkspace(ctx, "proj-123", "ws-123"); err != nil {
		t.Fatalf("DeleteWorkspace failed: %v", err)
	}

	// 5. Skill
	skill, err := cli.CreateSkill(ctx, "comp-123", PaperclipSkill{Name: "fleet-task", Slug: "fleet-task", Content: "# Fleet Task"})
	if err != nil || skill.ID != "skill-123" {
		t.Fatalf("CreateSkill failed: %v", err)
	}
	skill, err = cli.GetSkill(ctx, "comp-123", "skill-123")
	if err != nil || skill.Slug != "fleet-task" {
		t.Fatalf("GetSkill failed: %v", err)
	}
	if skill.Content != "# Fleet Task" {
		t.Fatalf("GetSkill did not map markdown into content: %q", skill.Content)
	}
	skill, err = cli.UpdateSkill(ctx, "comp-123", "skill-123", *skill)
	if err != nil || skill.Content != "# Fleet Task" {
		t.Fatalf("UpdateSkill failed: %v (content %q)", err, skill.Content)
	}
	metadataOnly := *skill
	metadataOnly.Content = ""
	if _, err = cli.UpdateSkill(ctx, "comp-123", "skill-123", metadataOnly); err != nil {
		t.Fatalf("UpdateSkill without content failed: %v", err)
	}
	metadataWithVal := *skill
	metadataWithVal.Metadata = map[string]interface{}{"author": "antigravity"}
	if _, err = cli.UpdateSkill(ctx, "comp-123", "skill-123", metadataWithVal); err != nil {
		t.Fatalf("UpdateSkill with metadata failed: %v", err)
	}
	skills, err := cli.ListSkills(ctx, "comp-123")
	if err != nil || len(skills) != 1 {
		t.Fatalf("ListSkills failed: %v", err)
	}
	if err := cli.DeleteSkill(ctx, "comp-123", "skill-123"); err != nil {
		t.Fatalf("DeleteSkill failed: %v", err)
	}

	// 6. Secret
	sec, err := cli.CreateSecret(ctx, "comp-123", "test-sec", "desc", "val")
	if err != nil || sec.ID != "sec-123" {
		t.Fatalf("CreateSecret failed: %v", err)
	}
	sec, err = cli.GetSecret(ctx, "comp-123", "sec-123")
	if err != nil || sec.Name != "test-sec" {
		t.Fatalf("GetSecret failed: %v", err)
	}
	sec, err = cli.UpdateSecret(ctx, "sec-123", "test-sec", "desc2")
	if err != nil {
		t.Fatalf("UpdateSecret failed: %v", err)
	}
	sec, err = cli.RotateSecret(ctx, "sec-123", "val2")
	if err != nil {
		t.Fatalf("RotateSecret failed: %v", err)
	}
	secs, err := cli.ListSecrets(ctx, "comp-123")
	if err != nil || len(secs) != 1 {
		t.Fatalf("ListSecrets failed: %v", err)
	}
	if err := cli.DeleteSecret(ctx, "sec-123"); err != nil {
		t.Fatalf("DeleteSecret failed: %v", err)
	}

	// 7. Agent
	agent, err := cli.CreateAgent(ctx, "comp-123", PaperclipAgent{Name: "engineer", Role: "engineer", AdapterType: "antigravity_local"})
	if err != nil || agent.ID != "agent-123" {
		t.Fatalf("CreateAgent failed: %v", err)
	}
	agent, err = cli.GetAgent(ctx, "agent-123")
	if err != nil || agent.Name != "engineer" {
		t.Fatalf("GetAgent failed: %v", err)
	}
	if agent.Permissions == nil || !agent.Permissions.CanAssignTasks || !agent.Permissions.CanCreateSkills || agent.Permissions.CanCreateAgents {
		t.Fatalf("GetAgent must expose the board-reported permission flags, got %+v", agent.Permissions)
	}
	if err := cli.UpdateAgentPermissions(ctx, "agent-123", PaperclipAgentPermissions{CanCreateSkills: true}); err != nil {
		t.Fatalf("UpdateAgentPermissions failed: %v", err)
	}
	agent, err = cli.UpdateAgent(ctx, "agent-123", *agent)
	if err != nil {
		t.Fatalf("UpdateAgent failed: %v", err)
	}
	agentWithConfigs := *agent
	agentWithConfigs.RuntimeConfig = map[string]interface{}{"env": "prod"}
	agentWithConfigs.AdapterConfig = map[string]interface{}{"model": "gpt-4"}
	agentWithConfigs.Metadata = map[string]interface{}{"team": "core"}
	if _, err = cli.UpdateAgent(ctx, "agent-123", agentWithConfigs); err != nil {
		t.Fatalf("UpdateAgent with configs failed: %v", err)
	}
	agentWithReset := *agent
	agentWithReset.AdapterConfig = nil
	agentWithReset.ReplaceAdapterConfig = true
	if _, err = cli.UpdateAgent(ctx, "agent-123", agentWithReset); err != nil {
		t.Fatalf("UpdateAgent with reset failed: %v", err)
	}
	agents, err := cli.ListAgents(ctx, "comp-123")
	if err != nil || len(agents) != 1 {
		t.Fatalf("ListAgents failed: %v", err)
	}
	inst, err := cli.GetAgentInstructionsFile(ctx, "agent-123", "AGENTS.md")
	if err != nil || inst != "# Instructions" {
		t.Fatalf("GetAgentInstructionsFile failed: %v, got %s", err, inst)
	}
	if err := cli.UpdateAgentInstructionsFile(ctx, "agent-123", "AGENTS.md", "# New"); err != nil {
		t.Fatalf("UpdateAgentInstructionsFile failed: %v", err)
	}
	if err := cli.DeleteAgent(ctx, "agent-123"); err != nil {
		t.Fatalf("DeleteAgent failed: %v", err)
	}

	// 8. Agent Key
	key, err := cli.CreateAgentKey(ctx, "agent-123", "harness-mcp")
	if err != nil || key.ID != "key-123" {
		t.Fatalf("CreateAgentKey failed: %v", err)
	}

	// 9. Tool App
	app, err := cli.CreateToolApp(ctx, "comp-123", PaperclipToolApp{Name: "harness.mcp.openobserve", Type: "mcp_http"})
	if err != nil || app.ID != "app-123" || app.Type != "mcp_http" {
		t.Fatalf("CreateToolApp failed: %v", err)
	}
	app, err = cli.GetToolApp(ctx, "app-123")
	if err != nil || app.Name != "harness.mcp.openobserve" || app.Type != "mcp_http" {
		t.Fatalf("GetToolApp failed: %v", err)
	}
	app, err = cli.UpdateToolApp(ctx, "app-123", *app)
	if err != nil || app.Type != "mcp_http" {
		t.Fatalf("UpdateToolApp failed: %v", err)
	}
	apps, err := cli.ListToolApps(ctx, "comp-123")
	if err != nil || len(apps) != 1 || apps[0].Type != "mcp_http" {
		t.Fatalf("ListToolApps failed: %v", err)
	}
	if err := cli.ArchiveToolApp(ctx, "app-123"); err != nil {
		t.Fatalf("ArchiveToolApp failed: %v", err)
	}

	// 10. Tool Connection
	conn, err := cli.CreateToolConnection(ctx, "comp-123", PaperclipToolConnection{AppID: "app-123", Name: "openobserve-connection"})
	if err != nil || conn.ID != "conn-123" || conn.AppID != "app-123" {
		t.Fatalf("CreateToolConnection failed: %v", err)
	}
	conn, err = cli.GetToolConnection(ctx, "conn-123")
	if err != nil || conn.Name != "openobserve-connection" || conn.AppID != "app-123" {
		t.Fatalf("GetToolConnection failed: %v", err)
	}
	conn, err = cli.UpdateToolConnection(ctx, "conn-123", *conn)
	if err != nil || conn.AppID != "app-123" {
		t.Fatalf("UpdateToolConnection failed: %v", err)
	}
	conns, err := cli.ListToolConnections(ctx, "comp-123")
	if err != nil || len(conns) != 1 || conns[0].AppID != "app-123" {
		t.Fatalf("ListToolConnections failed: %v", err)
	}
	if err := cli.DeleteToolConnection(ctx, "conn-123"); err != nil {
		t.Fatalf("DeleteToolConnection failed: %v", err)
	}

	// 11. Tool Policy
	pol, err := cli.CreateToolPolicy(ctx, "comp-123", PaperclipToolPolicy{Name: "default-policy"})
	if err != nil || pol.ID != "pol-123" {
		t.Fatalf("CreateToolPolicy failed: %v", err)
	}
	pol, err = cli.GetToolPolicy(ctx, "comp-123", "pol-123")
	if err != nil || pol.Name != "default-policy" {
		t.Fatalf("GetToolPolicy failed: %v", err)
	}
	pol, err = cli.UpdateToolPolicy(ctx, "comp-123", "pol-123", *pol)
	if err != nil {
		t.Fatalf("UpdateToolPolicy failed: %v", err)
	}
	pols, err := cli.ListToolPolicies(ctx, "comp-123")
	if err != nil || len(pols) != 1 {
		t.Fatalf("ListToolPolicies failed: %v", err)
	}
	if err := cli.DeleteToolPolicy(ctx, "comp-123", "pol-123"); err != nil {
		t.Fatalf("DeleteToolPolicy failed: %v", err)
	}

	// 12. Stdio Template
	tpl, err := cli.CreateStdioTemplate(ctx, "comp-123", PaperclipStdioTemplate{
		TemplateID:  "harness.mcp.devenv",
		Name:        "devenv",
		Description: "Devenv MCP",
		Command:     "nix",
	})
	if err != nil || tpl.ID != "tpl-123" {
		t.Fatalf("CreateStdioTemplate failed: %v", err)
	}
	tpl, err = cli.GetStdioTemplate(ctx, "comp-123", "harness.mcp.devenv")
	if err != nil || tpl.Name != "devenv" || tpl.Status != "active" || len(tpl.Args) != 1 || len(tpl.EnvKeys) != 1 {
		t.Fatalf("GetStdioTemplate failed: %v %#v", err, tpl)
	}
	tpls, err := cli.ListStdioTemplates(ctx, "comp-123")
	if err != nil || len(tpls) != 1 {
		t.Fatalf("ListStdioTemplates failed: %v", err)
	}
	if err := cli.DisableStdioTemplate(ctx, "comp-123", "harness.mcp.devenv"); err != nil {
		t.Fatalf("DisableStdioTemplate failed: %v", err)
	}

	// 13. Connection Installs
	installs, err := cli.GetConnectionInstalls(ctx, "conn-123")
	if err != nil || len(installs) != 1 {
		t.Fatalf("GetConnectionInstalls failed: %v", err)
	}
	installs, err = cli.UpdateConnectionInstalls(ctx, "conn-123", []map[string]interface{}{
		{"targetType": "agent", "targetId": "agent-123"},
	})
	if err != nil {
		t.Fatalf("UpdateConnectionInstalls failed: %v", err)
	}

	// 14. Catalog and Finish App Permissions
	catalog, err := cli.GetConnectionCatalog(ctx, "conn-123")
	if err != nil || len(catalog) != 1 {
		t.Fatalf("GetConnectionCatalog failed: %v", err)
	}
	finishResp, err := cli.FinishAppPermissions(ctx, "comp-123", "conn-123", PaperclipAppFinish{
		EnabledCatalogEntryIDs: []string{"cat-1"},
		Access:                 map[string]interface{}{"agentIds": []string{"agent-123"}},
	})
	if err != nil || finishResp["status"] != "ok" {
		t.Fatalf("FinishAppPermissions failed: %v", err)
	}

	// 15. Exact Lookups vs Name Lookups
	foundComp, err := cli.FindCompanyByName(ctx, "guseynov")
	if err != nil || foundComp.ID != "comp-123" {
		t.Fatalf("FindCompanyByName failed: %v", err)
	}
	if _, err := cli.GetCompany(ctx, "guseynov"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound when looking up company by name via GetCompany, got: %v", err)
	}
	if _, err := cli.GetSkill(ctx, "comp-123", "fleet-task"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound when looking up skill by slug via GetSkill, got: %v", err)
	}
	if _, err := cli.GetSecret(ctx, "comp-123", "test-sec"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound when looking up secret by name via GetSecret, got: %v", err)
	}
	if _, err := cli.GetAgent(ctx, "engineer"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound when looking up agent by name via GetAgent, got: %v", err)
	}

	// Error handling
	err = cli.paperclipRequest(ctx, http.MethodGet, "/api/not-found", nil, nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}
	err = cli.paperclipRequest(ctx, http.MethodGet, "/api/conflict", nil, nil)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("expected ErrConflict, got: %v", err)
	}
}

func TestGetWorkspaceInProjectUsesProjectScopedRoute(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/projects/proj-1/workspaces" {
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "ws-1", "projectId": "proj-1", "name": "primary"},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "API route not found"})
	}))
	defer server.Close()

	cli, err := NewClient(server.URL, "test-token")
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	ctx := context.Background()
	ws, err := cli.GetWorkspaceInProject(ctx, "proj-1", "ws-1")
	if err != nil || ws.ID != "ws-1" {
		t.Fatalf("GetWorkspaceInProject failed: %v", err)
	}
	if _, err := cli.GetWorkspaceInProject(ctx, "", "ws-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound fallback, got: %v", err)
	}
}

func TestSecretValueOmittedWhenEmpty(t *testing.T) {
	var bodies []map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var payload map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		bodies = append(bodies, payload)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":        "sec-9",
			"companyId": "comp-1",
			"name":      "declared",
		})
	}))
	defer server.Close()

	cli, err := NewClient(server.URL, "test-token")
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	ctx := context.Background()
	if _, err := cli.CreateSecret(ctx, "comp-1", "declared", "desc", ""); err != nil {
		t.Fatalf("CreateSecret failed: %v", err)
	}
	if _, err := cli.UpdateSecret(ctx, "sec-9", "declared", "desc"); err != nil {
		t.Fatalf("UpdateSecret failed: %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("expected 2 captured requests, got %d", len(bodies))
	}
	if val, ok := bodies[0]["value"].(string); !ok || val != "" {
		t.Fatalf("expected create request to provide empty string value, got: %v", bodies[0]["value"])
	}
	if _, present := bodies[1]["value"]; present {
		t.Fatalf("update request must omit the secret value")
	}
}

func TestSecretWriteErrorsNeverEchoResponseBodies(t *testing.T) {
	const protected = "protected-secret-value"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, protected, http.StatusBadRequest)
	}))
	defer server.Close()

	cli, err := NewClient(server.URL, "test-token")
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	ctx := context.Background()
	_, createErr := cli.CreateSecret(ctx, "comp-1", "declared", "desc", protected)
	_, rotateErr := cli.RotateSecret(ctx, "sec-9", protected)
	for _, requestErr := range []error{createErr, rotateErr} {
		if requestErr == nil {
			t.Fatal("expected secret write failure")
		}
		if strings.Contains(requestErr.Error(), protected) {
			t.Fatalf("secret response body leaked through error: %v", requestErr)
		}
	}
}

func TestToolAppValidation(t *testing.T) {
	ctx := context.Background()
	var serverHit bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverHit = true
		w.Header().Set("Content-Type", "application/json")
		var payload map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":        "app-1",
			"companyId": "comp-1",
			"name":      payload["name"],
			"type":      payload["type"],
		})
	}))
	defer server.Close()

	cli, err := NewClient(server.URL, "test-token")
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	// 1. Missing type should fail before mutation
	serverHit = false
	if _, err := cli.CreateToolApp(ctx, "comp-1", PaperclipToolApp{Name: "app-untyped"}); err == nil {
		t.Fatal("expected error creating tool app with empty type")
	}
	if serverHit {
		t.Fatal("expected no HTTP mutation request when type is missing on create")
	}

	if _, err := cli.UpdateToolApp(ctx, "app-1", PaperclipToolApp{Name: "app-untyped"}); err == nil {
		t.Fatal("expected error updating tool app with empty type")
	}
	if serverHit {
		t.Fatal("expected no HTTP mutation request when type is missing on update")
	}

	// 2. Invalid type should fail before mutation
	serverHit = false
	if _, err := cli.CreateToolApp(ctx, "comp-1", PaperclipToolApp{Name: "app-bad", Type: "unknown_type"}); err == nil {
		t.Fatal("expected error creating tool app with invalid type")
	}
	if serverHit {
		t.Fatal("expected no HTTP mutation request when type is invalid on create")
	}

	if _, err := cli.UpdateToolApp(ctx, "app-1", PaperclipToolApp{Name: "app-bad", Type: "unknown_type"}); err == nil {
		t.Fatal("expected error updating tool app with invalid type")
	}
	if serverHit {
		t.Fatal("expected no HTTP mutation request when type is invalid on update")
	}

	// 3. Valid types mcp_http and mcp_stdio should succeed
	serverHit = false
	httpApp, err := cli.CreateToolApp(ctx, "comp-1", PaperclipToolApp{Name: "app-http", Type: "mcp_http"})
	if err != nil || !serverHit || httpApp.Type != "mcp_http" {
		t.Fatalf("CreateToolApp mcp_http failed: %v", err)
	}

	serverHit = false
	stdioApp, err := cli.CreateToolApp(ctx, "comp-1", PaperclipToolApp{Name: "app-stdio", Type: "mcp_stdio"})
	if err != nil || !serverHit || stdioApp.Type != "mcp_stdio" {
		t.Fatalf("CreateToolApp mcp_stdio failed: %v", err)
	}

	serverHit = false
	updatedApp, err := cli.UpdateToolApp(ctx, "app-1", PaperclipToolApp{Name: "app-http-updated", Type: "mcp_http"})
	if err != nil || !serverHit || updatedApp.Type != "mcp_http" {
		t.Fatalf("UpdateToolApp mcp_http failed: %v", err)
	}

	// 4. The validation error names the allowed set so callers can self-correct
	if err := ValidateToolAppType("unknown_type"); err == nil || !strings.Contains(err.Error(), "mcp_http") || !strings.Contains(err.Error(), "mcp_stdio") {
		t.Fatalf("expected invalid-type error to list the allowed types, got: %v", err)
	}
}

func TestGetToolAppBackfillsType(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/tool-applications/app-7":
			// The direct GET route omits the type field.
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":        "app-7",
				"companyId": "comp-1",
				"name":      "harness.mcp.gitea-committer",
			})
		case "/api/tool-applications/app-8":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":   "app-8",
				"name": "harness.mcp.unlisted",
			})
		case "/api/companies":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "comp-1", "name": "guseynov"},
			})
		case "/api/companies/comp-1/tools/applications":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "app-7", "companyId": "comp-1", "name": "harness.mcp.gitea-committer", "type": "mcp_http"},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cli, err := NewClient(server.URL, "test-token")
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	app, err := cli.GetToolApp(ctx, "app-7")
	if err != nil {
		t.Fatalf("GetToolApp failed: %v", err)
	}
	if app.Type != "mcp_http" {
		t.Fatalf("expected type mcp_http backfilled from the company list, got %q", app.Type)
	}

	// An app absent from every company list keeps an empty type rather than failing
	app, err = cli.GetToolApp(ctx, "app-8")
	if err != nil || app.ID != "app-8" || app.Type != "" {
		t.Fatalf("expected unlisted app to resolve with empty type, got %+v, %v", app, err)
	}
}

func TestPaperclipConnectionClientBindingRegression(t *testing.T) {
	ctx := context.Background()
	var createBody map[string]interface{}
	var patchBody map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/companies/comp-test/tools/connections" && r.Method == http.MethodPost:
			_ = json.NewDecoder(r.Body).Decode(&createBody)
			if _, exists := createBody["appId"]; exists {
				t.Errorf("POST sent obsolete appId: %v", createBody["appId"])
			}
			if createBody["applicationId"] != "app-target" {
				t.Errorf("POST applicationId = %v, want app-target", createBody["applicationId"])
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":            "conn-1",
				"companyId":     "comp-test",
				"applicationId": createBody["applicationId"],
				"name":          createBody["name"],
			})
		case r.URL.Path == "/api/tool-connections/conn-1" && r.Method == http.MethodPatch:
			_ = json.NewDecoder(r.Body).Decode(&patchBody)
			if _, exists := patchBody["appId"]; exists {
				t.Errorf("PATCH sent obsolete appId: %v", patchBody["appId"])
			}
			if _, exists := patchBody["applicationId"]; exists {
				t.Errorf("PATCH sent immutable applicationId: %v", patchBody["applicationId"])
			}
			// Server returns patch response without applicationId
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":        "conn-1",
				"companyId": "comp-test",
				"name":      patchBody["name"],
			})
		case r.URL.Path == "/api/companies/comp-test/tools/connections" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "conn-1", "companyId": "comp-test", "applicationId": "app-target", "name": "conn-renamed"},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cli, err := NewClient(server.URL, "test-token")
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	// 1. CreateToolConnection
	created, err := cli.CreateToolConnection(ctx, "comp-test", PaperclipToolConnection{
		AppID: "app-target",
		Name:  "conn-orig",
	})
	if err != nil {
		t.Fatalf("CreateToolConnection failed: %v", err)
	}
	if created.AppID != "app-target" {
		t.Fatalf("created AppID = %v, want app-target", created.AppID)
	}

	// 2. UpdateToolConnection
	updated, err := cli.UpdateToolConnection(ctx, "conn-1", PaperclipToolConnection{
		AppID: "app-target",
		Name:  "conn-renamed",
	})
	if err != nil {
		t.Fatalf("UpdateToolConnection failed: %v", err)
	}
	if updated.AppID != "app-target" {
		t.Fatalf("updated AppID must be preserved when PATCH response omits applicationId, got %v", updated.AppID)
	}

	// 3. ListToolConnections
	list, err := cli.ListToolConnections(ctx, "comp-test")
	if err != nil || len(list) != 1 {
		t.Fatalf("ListToolConnections failed: %v", err)
	}
	if list[0].AppID != "app-target" {
		t.Fatalf("listed AppID = %v, want app-target", list[0].AppID)
	}
}

func TestRevokeAppPermissionProfile(t *testing.T) {
	var requests []string
	failMethod := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == failMethod {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/tools/profiles":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"profiles": []map[string]interface{}{
				{
					"id": "profile-app", "profileKey": "app:conn-1",
					"entries": []map[string]interface{}{{"id": "entry-1"}},
					"bindings": []map[string]interface{}{
						{"targetType": "agent", "targetId": "agent-1"},
						{"targetType": "company", "targetId": "comp-1"},
						{"targetType": "gateway", "targetId": "gateway-1"},
					},
				},
				{"id": "profile-other", "profileKey": "app:conn-2", "entries": []map[string]interface{}{{"id": "entry-other"}}},
			}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/comp-1/tools/profiles/profile-app/unbind":
			var target map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&target); err != nil {
				t.Error(err)
			}
			requests = append(requests, fmt.Sprintf("unbind %s:%s", target["targetType"], target["targetId"]))
			_, _ = w.Write([]byte(`{"unbound":1}`))
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/tool-profile-entries/"):
			requests = append(requests, "delete "+strings.TrimPrefix(r.URL.Path, "/api/tool-profile-entries/"))
			_, _ = w.Write([]byte(`{}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	cli, _ := NewClient(server.URL, "test-token")
	ctx := context.Background()
	// Agent and company bindings go before the tools; runtime gateway bindings stay.
	if err := cli.RevokeAppPermissionProfile(ctx, "comp-1", "conn-1"); err != nil || !reflect.DeepEqual(requests, []string{"unbind agent:agent-1", "unbind company:comp-1", "delete entry-1"}) {
		t.Fatalf("connection profile was not emptied alone: %v %v", err, requests)
	}
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodDelete} {
		failMethod = method
		if err := cli.RevokeAppPermissionProfile(ctx, "comp-1", "conn-1"); err == nil {
			t.Fatalf("ignored %s failure", method)
		}
	}
}

func TestReconcileAskFirstPolicies(t *testing.T) {
	catalog := []map[string]interface{}{{"id": "cat-write", "toolName": "write"}, {"id": "cat-push", "toolName": "push"}, {"id": "cat-read", "toolName": "read"}}
	policies := []map[string]interface{}{
		{"id": "pol-finish", "enabled": true, "policyType": "require_approval", "selectors": map[string]interface{}{"catalogEntryId": "cat-write"}, "config": map[string]interface{}{"source": "app_gallery_finish", "connectionId": "conn-1", "catalogEntryId": "cat-write"}},
		{"id": "pol-read", "enabled": true, "policyType": "require_approval", "selectors": map[string]interface{}{"catalogEntryId": "cat-read", "agentIds": []interface{}{"agent-a", "agent-b"}}},
		{"id": "pol-disabled", "enabled": false, "policyType": "require_approval", "selectors": map[string]interface{}{"catalogEntryId": "cat-write"}},
		{"id": "pol-foreign", "enabled": true, "policyType": "require_approval", "selectors": map[string]interface{}{"catalogEntryId": "cat-other-connection"}},
	}
	// events records each mutation in order: a replacement must be created
	// before the policy it replaces is deleted.
	var events []string
	var created []map[string]interface{}
	next := 0
	failMethod := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == failMethod {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/tools/policies":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"policies": policies})
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/comp-1/tools/policies":
			var body map[string]interface{}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			for _, policy := range policies {
				if policy["name"] == body["name"] {
					http.Error(w, "duplicate key value violates unique constraint \"tool_policies_company_name_uq\"", http.StatusInternalServerError)
					return
				}
			}
			if name, _ := body["name"].(string); len(name) > 160 {
				http.Error(w, "name too long", http.StatusBadRequest)
				return
			}
			created = append(created, body)
			next++
			id := fmt.Sprintf("pol-new-%d", next)
			events = append(events, "create "+id)
			policies = append(policies, map[string]interface{}{"id": id, "name": body["name"], "enabled": true, "policyType": body["policyType"], "selectors": body["selectors"]})
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": id})
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/companies/comp-1/tools/policies/"):
			id := strings.TrimPrefix(r.URL.Path, "/api/companies/comp-1/tools/policies/")
			events = append(events, "delete "+id)
			for i, policy := range policies {
				if policy["id"] == id {
					policies = append(policies[:i], policies[i+1:]...)
					break
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": id})
		default:
			// Paperclip refuses a PATCH of a finish-owned policy with 422.
			http.Error(w, "unexpected request", http.StatusUnprocessableEntity)
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	cli, _ := NewClient(server.URL, "test-token")
	ctx := context.Background()
	agents := []string{"agent-b", "agent-a"}
	askFirst := func(connection string, entries ...interface{}) map[string]interface{} {
		return map[string]interface{}{
			"description": fmt.Sprintf("Ask first before running any of the %d ask-first tools of %s.", len(entries), connection),
			"policyType":  "require_approval", "priority": float64(50),
			"selectors": map[string]interface{}{"catalogEntryIds": entries, "agentIds": []interface{}{"agent-a", "agent-b"}},
		}
	}
	// withoutName returns the created bodies without their digest-bearing names.
	withoutName := func() []map[string]interface{} {
		bodies := []map[string]interface{}{}
		for _, body := range created {
			trimmed := map[string]interface{}{}
			for key, value := range body {
				if key != "name" {
					trimmed[key] = value
				}
			}
			bodies = append(bodies, trimmed)
		}
		return bodies
	}

	// One policy gates every ask-first entry, and it exists before the
	// per-entry and finish-owned policies it supersedes are deleted.
	if err := cli.ReconcileAskFirstPolicies(ctx, "comp-1", "gitea (demo)", catalog, []string{"cat-write", "cat-push"}, agents); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(events, []string{"create pol-new-1", "delete pol-finish", "delete pol-read"}) || !reflect.DeepEqual(withoutName(), []map[string]interface{}{askFirst("gitea (demo)", "cat-push", "cat-write")}) {
		t.Fatalf("ask-first policy not reconciled: events %v, created %#v", events, created)
	}
	if name, _ := created[0]["name"].(string); !strings.HasPrefix(name, "Ask first: gitea (demo) (") {
		t.Fatalf("policy name does not identify its connection: %q", name)
	}

	events, created = nil, nil
	if err := cli.ReconcileAskFirstPolicies(ctx, "comp-1", "gitea (demo)", catalog, []string{"cat-push", "cat-write"}, agents); err != nil || events != nil {
		t.Fatalf("converged policy was rewritten: %v %v", err, events)
	}

	// Per-project connections share names up to their qualifier; each gets its
	// own company-unique policy.
	sibling := []map[string]interface{}{{"id": "cat-write-sibling", "toolName": "write"}}
	if err := cli.ReconcileAskFirstPolicies(ctx, "comp-1", "gitea (sibling)", sibling, []string{"cat-write-sibling"}, agents); err != nil || !reflect.DeepEqual(withoutName(), []map[string]interface{}{askFirst("gitea (sibling)", "cat-write-sibling")}) {
		t.Fatalf("sibling connection policy was not created: %v %#v", err, created)
	}
	policies = policies[:len(policies)-1]

	// A changed selection keeps the connection's name, yet its replacement never
	// collides with the policy it replaces.
	events, created = nil, nil
	if err := cli.ReconcileAskFirstPolicies(ctx, "comp-1", "gitea (demo)", catalog, []string{"cat-write"}, agents); err != nil || !reflect.DeepEqual(events, []string{"create pol-new-3", "delete pol-new-1"}) || !reflect.DeepEqual(withoutName(), []map[string]interface{}{askFirst("gitea (demo)", "cat-write")}) {
		t.Fatalf("changed selection not replaced: %v %v %#v", err, events, created)
	}

	events, created = nil, nil
	if err := cli.ReconcileAskFirstPolicies(ctx, "comp-1", "", catalog, nil, nil); err != nil || !reflect.DeepEqual(events, []string{"delete pol-new-3"}) {
		t.Fatalf("removal left the ask-first policy: %v %v", err, events)
	}

	// A connection name longer than Paperclip's policy-name limit is shortened.
	long := strings.Repeat("g", 200)
	if err := cli.ReconcileAskFirstPolicies(ctx, "comp-1", long, catalog, []string{"cat-write"}, agents); err != nil {
		t.Fatalf("long connection name was not shortened: %v", err)
	}
	if name, _ := created[0]["name"].(string); len(name) != 160 {
		t.Fatalf("policy name length %d", len(name))
	}

	policies = append(policies, map[string]interface{}{"id": "pol-stray", "enabled": true, "policyType": "require_approval", "selectors": map[string]interface{}{"catalogEntryId": "cat-read"}})
	for _, method := range []string{http.MethodGet, http.MethodDelete, http.MethodPost} {
		failMethod = method
		if err := cli.ReconcileAskFirstPolicies(ctx, "comp-1", "gitea (demo)", catalog, []string{"cat-read"}, agents); err == nil {
			t.Fatalf("ignored %s failure", method)
		}
	}
}

func TestSecretProviderConfigAndUserSecretDefinitionCRUD(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		p := r.URL.Path

		switch {
		case r.Method == http.MethodPost && p == "/api/companies/comp-1/secret-provider-configs":
			var input PaperclipSecretProviderConfig
			_ = json.NewDecoder(r.Body).Decode(&input)
			input.ID = "spc-1"
			_ = json.NewEncoder(w).Encode(input)
		case r.Method == http.MethodGet && p == "/api/secret-provider-configs/spc-1":
			_ = json.NewEncoder(w).Encode(PaperclipSecretProviderConfig{ID: "spc-1", CompanyID: "comp-1", Provider: "vault", DisplayName: "HashiCorp Vault"})
		case r.Method == http.MethodPatch && p == "/api/secret-provider-configs/spc-1":
			var input PaperclipSecretProviderConfig
			_ = json.NewDecoder(r.Body).Decode(&input)
			input.ID = "spc-1"
			input.CompanyID = "comp-1"
			_ = json.NewEncoder(w).Encode(input)
		case r.Method == http.MethodDelete && p == "/api/secret-provider-configs/spc-1":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && p == "/api/companies/comp-1/secret-provider-configs":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"providerConfigs": []map[string]interface{}{
					{"id": "spc-1", "provider": "vault", "displayName": "Vault 1"},
					{"id": "spc-2", "companyId": "comp-1", "provider": "aws_secrets_manager", "displayName": "AWS 2"},
				},
			})
		case r.Method == http.MethodPost && p == "/api/companies/comp-1/user-secret-definitions":
			var input PaperclipUserSecretDefinition
			_ = json.NewDecoder(r.Body).Decode(&input)
			input.ID = "usd-1"
			_ = json.NewEncoder(w).Encode(input)
		case r.Method == http.MethodPatch && p == "/api/companies/comp-1/user-secret-definitions/usd-1":
			var input PaperclipUserSecretDefinition
			_ = json.NewDecoder(r.Body).Decode(&input)
			input.ID = "usd-1"
			_ = json.NewEncoder(w).Encode(input)
		case r.Method == http.MethodDelete && p == "/api/companies/comp-1/user-secret-definitions/usd-1":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && p == "/api/companies/comp-1/user-secret-definitions":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"userSecretDefinitions": []map[string]interface{}{
					{"id": "usd-1", "key": "openai_api_key", "name": "OpenAI Key"},
					{"id": "usd-2", "companyId": "comp-1", "key": "anthropic_api_key", "name": "Anthropic Key"},
				},
			})
		case r.Method == http.MethodGet && p == "/api/secret-provider-configs/not-found":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		default:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"internal error"}`))
		}
	}))
	defer server.Close()

	cli, err := NewClient(server.URL, "test-token")
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	// SecretProviderConfig Tests
	createdSpc, err := cli.CreateSecretProviderConfig(ctx, "comp-1", PaperclipSecretProviderConfig{
		Provider:    "vault",
		DisplayName: "HashiCorp Vault",
	})
	if err != nil || createdSpc.ID != "spc-1" || createdSpc.CompanyID != "comp-1" {
		t.Fatalf("CreateSecretProviderConfig failed: %v, %#v", err, createdSpc)
	}

	gotSpc, err := cli.GetSecretProviderConfig(ctx, "spc-1")
	if err != nil || gotSpc.ID != "spc-1" {
		t.Fatalf("GetSecretProviderConfig failed: %v, %#v", err, gotSpc)
	}

	updatedSpc, err := cli.UpdateSecretProviderConfig(ctx, "spc-1", PaperclipSecretProviderConfig{
		DisplayName: "Updated Vault",
	})
	if err != nil || updatedSpc.DisplayName != "Updated Vault" {
		t.Fatalf("UpdateSecretProviderConfig failed: %v, %#v", err, updatedSpc)
	}

	if err := cli.DeleteSecretProviderConfig(ctx, "spc-1"); err != nil {
		t.Fatalf("DeleteSecretProviderConfig failed: %v", err)
	}

	spcList, err := cli.ListSecretProviderConfigs(ctx, "comp-1")
	if err != nil || len(spcList) != 2 || spcList[0].CompanyID != "comp-1" {
		t.Fatalf("ListSecretProviderConfigs failed: %v, %#v", err, spcList)
	}

	// UserSecretDefinition Tests
	createdUsd, err := cli.CreateUserSecretDefinition(ctx, "comp-1", PaperclipUserSecretDefinition{
		Key:  "openai_api_key",
		Name: "OpenAI Key",
	})
	if err != nil || createdUsd.ID != "usd-1" || createdUsd.CompanyID != "comp-1" {
		t.Fatalf("CreateUserSecretDefinition failed: %v, %#v", err, createdUsd)
	}

	gotUsd, err := cli.GetUserSecretDefinition(ctx, "comp-1", "usd-1")
	if err != nil || gotUsd.ID != "usd-1" {
		t.Fatalf("GetUserSecretDefinition by ID failed: %v, %#v", err, gotUsd)
	}

	gotUsdByKey, err := cli.GetUserSecretDefinition(ctx, "comp-1", "openai_api_key")
	if err != nil || gotUsdByKey.ID != "usd-1" {
		t.Fatalf("GetUserSecretDefinition by Key failed: %v, %#v", err, gotUsdByKey)
	}

	if _, err := cli.GetUserSecretDefinition(ctx, "comp-1", "non-existent"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}

	updatedUsd, err := cli.UpdateUserSecretDefinition(ctx, "comp-1", "usd-1", PaperclipUserSecretDefinition{
		Name: "Updated Key",
	})
	if err != nil || updatedUsd.Name != "Updated Key" {
		t.Fatalf("UpdateUserSecretDefinition failed: %v, %#v", err, updatedUsd)
	}

	if err := cli.DeleteUserSecretDefinition(ctx, "comp-1", "usd-1"); err != nil {
		t.Fatalf("DeleteUserSecretDefinition failed: %v", err)
	}

	usdList, err := cli.ListUserSecretDefinitions(ctx, "comp-1")
	if err != nil || len(usdList) != 2 || usdList[0].CompanyID != "comp-1" {
		t.Fatalf("ListUserSecretDefinitions failed: %v, %#v", err, usdList)
	}

	// Empty ID checks
	if _, err := cli.GetSecretProviderConfig(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for empty id, got: %v", err)
	}
	if _, err := cli.GetUserSecretDefinition(ctx, "", "usd-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for empty companyID, got: %v", err)
	}
	if _, err := cli.GetUserSecretDefinition(ctx, "comp-1", ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for empty id, got: %v", err)
	}

	// Alternate envelope tests
	serverEnv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "secret-provider-configs") {
			if r.URL.Query().Get("fmt") == "spc" {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"secretProviderConfigs": []map[string]interface{}{{"id": "spc-3", "provider": "vault"}},
				})
			} else {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"configs": []interface{}{map[string]interface{}{"id": "spc-4", "provider": "vault"}, "not-a-map"},
				})
			}
		} else {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"definitions": []interface{}{map[string]interface{}{"id": "usd-3", "key": "key3"}, "not-a-map"},
			})
		}
	}))
	defer serverEnv.Close()
	cliEnv, _ := NewClient(serverEnv.URL, "token")
	spcEnvList1, err := cliEnv.ListSecretProviderConfigs(ctx, "comp-1?fmt=spc")
	if err != nil || len(spcEnvList1) != 1 {
		t.Fatalf("ListSecretProviderConfigs spc envelope failed: %v", err)
	}
	spcEnvList2, err := cliEnv.ListSecretProviderConfigs(ctx, "comp-1")
	if err != nil || len(spcEnvList2) != 1 {
		t.Fatalf("ListSecretProviderConfigs configs envelope failed: %v", err)
	}
	usdEnvList, err := cliEnv.ListUserSecretDefinitions(ctx, "comp-1")
	if err != nil || len(usdEnvList) != 1 {
		t.Fatalf("ListUserSecretDefinitions definitions envelope failed: %v", err)
	}

	// Error paths
	badCli, _ := NewClient("http://127.0.0.1:1", "token")
	if _, err := badCli.CreateSecretProviderConfig(ctx, "comp-1", PaperclipSecretProviderConfig{}); err == nil {
		t.Fatal("expected error on bad host CreateSecretProviderConfig")
	}
	if _, err := badCli.GetSecretProviderConfig(ctx, "spc-1"); err == nil {
		t.Fatal("expected error on bad host GetSecretProviderConfig")
	}
	if _, err := badCli.UpdateSecretProviderConfig(ctx, "spc-1", PaperclipSecretProviderConfig{}); err == nil {
		t.Fatal("expected error on bad host UpdateSecretProviderConfig")
	}
	if err := badCli.DeleteSecretProviderConfig(ctx, "spc-1"); err == nil {
		t.Fatal("expected error on bad host DeleteSecretProviderConfig")
	}
	if _, err := badCli.ListSecretProviderConfigs(ctx, "comp-1"); err == nil {
		t.Fatal("expected error on bad host ListSecretProviderConfigs")
	}
	if _, err := badCli.CreateUserSecretDefinition(ctx, "comp-1", PaperclipUserSecretDefinition{}); err == nil {
		t.Fatal("expected error on bad host CreateUserSecretDefinition")
	}
	if _, err := badCli.GetUserSecretDefinition(ctx, "comp-1", "usd-1"); err == nil {
		t.Fatal("expected error on bad host GetUserSecretDefinition")
	}
	if _, err := badCli.UpdateUserSecretDefinition(ctx, "comp-1", "usd-1", PaperclipUserSecretDefinition{}); err == nil {
		t.Fatal("expected error on bad host UpdateUserSecretDefinition")
	}
	if err := badCli.DeleteUserSecretDefinition(ctx, "comp-1", "usd-1"); err == nil {
		t.Fatal("expected error on bad host DeleteUserSecretDefinition")
	}
	if _, err := badCli.ListUserSecretDefinitions(ctx, "comp-1"); err == nil {
		t.Fatal("expected error on bad host ListUserSecretDefinitions")
	}
}

