package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestRegressionGUS407UndeclaredAdapterConfigDriftAndPruning(t *testing.T) {
	ctx := context.Background()

	liveAdapterConfig := map[string]interface{}{
		"confinement":                "agy_sandbox",
		"dangerouslySkipPermissions": true,
		"sandbox":                    true,
		"printTimeoutSec":            float64(1800),
		"instructionsFilePath":       "/agents/harness/instructions/AGENTS.md",
		"instructionsRootPath":       "/agents/harness/instructions",
		"instructionsEntryFile":      "AGENTS.md",
		"instructionsBundleMode":     "managed",
		"env": map[string]interface{}{
			"FOO_SECRET": map[string]interface{}{
				"type":     "user_secret_ref",
				"key":      "foo_secret",
				"version":  "latest",
				"required": true,
			},
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/adapters/antigravity_local/config-schema":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"fields": []interface{}{}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/agent-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":            "agent-1",
				"companyId":     "comp-1",
				"name":          "harness",
				"role":          "engineer",
				"title":         "Harness Agent",
				"adapterType":   "antigravity_local",
				"status":        "active",
				"adapterConfig": liveAdapterConfig,
				"permissions": map[string]interface{}{
					"canAssignTasks":  true,
					"canCreateAgents": false,
					"canCreateSkills": false,
				},
			})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/agents/agent-1":
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)

			replaceAdapterConfig, _ := body["replaceAdapterConfig"].(bool)
			if requestedAdapterConfig, ok := body["adapterConfig"].(map[string]interface{}); ok {
				if replaceAdapterConfig {
					liveAdapterConfig = requestedAdapterConfig
				} else {
					// Default Paperclip server behavior: merge existing adapterConfig
					merged := make(map[string]interface{})
					for k, v := range liveAdapterConfig {
						merged[k] = v
					}
					for k, v := range requestedAdapterConfig {
						merged[k] = v
					}
					liveAdapterConfig = merged
				}
			}

			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":            "agent-1",
				"companyId":     "comp-1",
				"name":          "harness",
				"role":          "engineer",
				"title":         "Harness Agent",
				"adapterType":   "antigravity_local",
				"status":        "active",
				"adapterConfig": liveAdapterConfig,
				"permissions": map[string]interface{}{
					"canAssignTasks":  true,
					"canCreateAgents": false,
					"canCreateSkills": false,
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/agent-1/permissions":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"canAssignTasks":  true,
				"canCreateAgents": false,
				"canCreateSkills": false,
			})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/agents/agent-1/permissions":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"canAssignTasks":  true,
				"canCreateAgents": false,
				"canCreateSkills": false,
			})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/agents/agent-1/instructions-bundle/file"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"content": "# Instructions"})
		case r.Method == http.MethodPut && r.URL.Path == "/api/agents/agent-1/instructions-bundle/file":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	r := NewPaperclipAgentResource().(*PaperclipAgentResource)
	schema := configureTestResource(t, server.URL, r)

	// 1. Initial State: declared configuration in state has only confinement and dangerouslySkipPermissions.
	declaredConfig := `{"confinement":"agy_sandbox","dangerouslySkipPermissions":true}`
	stateModel := PaperclipAgentResourceModel{
		ID:              types.StringValue("agent-1"),
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("harness"),
		Role:            types.StringValue("engineer"),
		Title:           types.StringValue("Harness Agent"),
		AdapterType:     types.StringValue("antigravity_local"),
		AdapterConfig:   types.StringValue(declaredConfig),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(false),
	}

	state := tfsdk.State{Schema: schema}
	if diags := state.Set(ctx, &stateModel); diags.HasError() {
		t.Fatalf("failed setting initial state: %v", diags)
	}

	// 2. Read should detect drift: the live agent has undeclared keys "sandbox" and "printTimeoutSec".
	// The refreshed state MUST reflect the undeclared keys so a plan shows the drift/update,
	// while Paperclip-managed keys (instructions bundle, secret refs) are excluded from state.
	readResp := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("Read failed: %v", readResp.Diagnostics)
	}

	var readModel PaperclipAgentResourceModel
	if diags := readResp.State.Get(ctx, &readModel); diags.HasError() {
		t.Fatalf("failed getting read state: %v", diags)
	}

	refreshedJSON := readModel.AdapterConfig.ValueString()
	if !strings.Contains(refreshedJSON, "sandbox") || !strings.Contains(refreshedJSON, "printTimeoutSec") {
		t.Fatalf("expected Read to detect drift from undeclared keys (sandbox, printTimeoutSec), got: %s", refreshedJSON)
	}
	if strings.Contains(refreshedJSON, "instructionsFilePath") || strings.Contains(refreshedJSON, "instructionsBundleMode") {
		t.Fatalf("expected Read to exclude Paperclip-managed instructions keys from state, got: %s", refreshedJSON)
	}
	if strings.Contains(refreshedJSON, "FOO_SECRET") {
		t.Fatalf("expected Read to exclude Paperclip-managed env secret references from state, got: %s", refreshedJSON)
	}

	// 3. Plan an update to reconcile back to declared configuration (without sandbox/printTimeoutSec).
	planModel := stateModel
	planModel.AdapterConfig = types.StringValue(declaredConfig)
	plan := tfsdk.Plan{Schema: schema}
	if diags := plan.Set(ctx, &planModel); diags.HasError() {
		t.Fatalf("failed setting plan: %v", diags)
	}

	// 4. Apply the update: Update should send replaceAdapterConfig=true and preserve Paperclip-managed keys.
	updateResp := resource.UpdateResponse{State: readResp.State}
	r.Update(ctx, resource.UpdateRequest{
		Plan:  plan,
		State: readResp.State,
	}, &updateResp)
	if updateResp.Diagnostics.HasError() {
		t.Fatalf("Update failed: %v", updateResp.Diagnostics)
	}

	// Verify live agent on the server:
	// - Undeclared keys ("sandbox", "printTimeoutSec") MUST be pruned.
	// - Declared keys ("confinement", "dangerouslySkipPermissions") MUST be present.
	// - Paperclip-managed keys ("instructionsFilePath", "instructionsRootPath", "instructionsEntryFile", "instructionsBundleMode", env secret refs) MUST survive.
	if _, ok := liveAdapterConfig["sandbox"]; ok {
		t.Fatalf("expected undeclared key 'sandbox' to be pruned from live adapterConfig after apply, but it was retained: %+v", liveAdapterConfig)
	}
	if _, ok := liveAdapterConfig["printTimeoutSec"]; ok {
		t.Fatalf("expected undeclared key 'printTimeoutSec' to be pruned from live adapterConfig after apply, but it was retained: %+v", liveAdapterConfig)
	}
	if liveAdapterConfig["confinement"] != "agy_sandbox" || liveAdapterConfig["dangerouslySkipPermissions"] != true {
		t.Fatalf("expected declared keys to be retained, got: %+v", liveAdapterConfig)
	}
	if liveAdapterConfig["instructionsFilePath"] != "/agents/harness/instructions/AGENTS.md" ||
		liveAdapterConfig["instructionsBundleMode"] != "managed" ||
		liveAdapterConfig["instructionsRootPath"] != "/agents/harness/instructions" ||
		liveAdapterConfig["instructionsEntryFile"] != "AGENTS.md" {
		t.Fatalf("expected Paperclip-managed instructions bundle keys to survive apply, got: %+v", liveAdapterConfig)
	}
	envMap, ok := liveAdapterConfig["env"].(map[string]interface{})
	if !ok || envMap["FOO_SECRET"] == nil {
		t.Fatalf("expected Paperclip-managed env secret reference to survive apply, got: %+v", liveAdapterConfig)
	}

	// 5. Subsequent Read should read back cleanly matching the declared configuration without drift.
	postReadResp := resource.ReadResponse{State: updateResp.State}
	r.Read(ctx, resource.ReadRequest{State: updateResp.State}, &postReadResp)
	if postReadResp.Diagnostics.HasError() {
		t.Fatalf("post-update Read failed: %v", postReadResp.Diagnostics)
	}

	var postReadModel PaperclipAgentResourceModel
	if diags := postReadResp.State.Get(ctx, &postReadModel); diags.HasError() {
		t.Fatalf("failed getting post-update read state: %v", diags)
	}

	if postReadModel.AdapterConfig.ValueString() != declaredConfig {
		t.Fatalf("expected post-update state to equal declared config %q, got %q", declaredConfig, postReadModel.AdapterConfig.ValueString())
	}
}

func TestAdapterConfigHelperBranches(t *testing.T) {
	// 1. isSecretRefBinding branches
	if isSecretRefBinding("not-a-map") {
		t.Fatal("expected non-map to return false")
	}
	if isSecretRefBinding(map[string]interface{}{}) {
		t.Fatal("expected map without type to return false")
	}
	if isSecretRefBinding(map[string]interface{}{"type": 123}) {
		t.Fatal("expected non-string type to return false")
	}
	if isSecretRefBinding(map[string]interface{}{"type": "plain"}) {
		t.Fatal("expected plain binding to return false")
	}
	if !isSecretRefBinding(map[string]interface{}{"type": "secret_ref"}) {
		t.Fatal("expected secret_ref to return true")
	}
	if !isSecretRefBinding(map[string]interface{}{"type": "user_secret_ref"}) {
		t.Fatal("expected user_secret_ref to return true")
	}

	// 2. filterPaperclipManagedAdapterConfig branches
	if filterPaperclipManagedAdapterConfig(nil, nil) != nil {
		t.Fatal("expected nil remote to return nil")
	}

	// managed == nil
	filtered := filterPaperclipManagedAdapterConfig(map[string]interface{}{
		"instructionsFilePath": "/a/b",
		"other":                "val",
	}, nil)
	if _, ok := filtered["instructionsFilePath"]; ok || filtered["other"] != "val" {
		t.Fatalf("expected instructionsFilePath to be deleted when managed == nil: %+v", filtered)
	}

	// managed != nil with declared instructionsFilePath
	filteredDeclared := filterPaperclipManagedAdapterConfig(map[string]interface{}{
		"instructionsFilePath": "/a/b",
	}, map[string]interface{}{
		"instructionsFilePath": "/a/b",
	})
	if filteredDeclared["instructionsFilePath"] != "/a/b" {
		t.Fatalf("expected declared instructionsFilePath to be kept: %+v", filteredDeclared)
	}

	// remote env is not a map
	filteredNonMapEnv := filterPaperclipManagedAdapterConfig(map[string]interface{}{
		"env": "invalid",
	}, nil)
	if filteredNonMapEnv["env"] != "invalid" {
		t.Fatalf("expected non-map env to be preserved as-is: %+v", filteredNonMapEnv)
	}

	// remote env where managedHasEnv is true and declares a key, plus non-secret undeclared env var
	filteredEnvManaged := filterPaperclipManagedAdapterConfig(map[string]interface{}{
		"env": map[string]interface{}{
			"DECLARED":   "val1",
			"UNDECLARED": "val2",
			"SECRET":     map[string]interface{}{"type": "secret_ref"},
		},
	}, map[string]interface{}{
		"env": map[string]interface{}{
			"DECLARED": "val1",
		},
	})
	envMap := filteredEnvManaged["env"].(map[string]interface{})
	if envMap["DECLARED"] != "val1" || envMap["UNDECLARED"] != "val2" || envMap["SECRET"] != nil {
		t.Fatalf("expected declared and undeclared plain env to be kept, secret ref dropped: %+v", envMap)
	}

	// remote env where managedHasEnv is true and all keys in remote were secret refs
	filteredEnvEmptyManaged := filterPaperclipManagedAdapterConfig(map[string]interface{}{
		"env": map[string]interface{}{
			"SECRET": map[string]interface{}{"type": "secret_ref"},
		},
	}, map[string]interface{}{
		"env": map[string]interface{}{},
	})
	if filteredEnvEmptyManaged["env"] == nil {
		t.Fatalf("expected empty env map to be kept when managedHasEnv is true: %+v", filteredEnvEmptyManaged)
	}

	// 3. preservePaperclipManagedAdapterConfig branches
	preservePaperclipManagedAdapterConfig(nil, map[string]interface{}{})
	preservePaperclipManagedAdapterConfig(map[string]interface{}{}, nil)

	// existing has key, target already declared it
	targetWithDecl := map[string]interface{}{"instructionsFilePath": "/custom/path"}
	preservePaperclipManagedAdapterConfig(map[string]interface{}{
		"instructionsFilePath": "/default/path",
		"env":                  "not-a-map",
	}, targetWithDecl)
	if targetWithDecl["instructionsFilePath"] != "/custom/path" {
		t.Fatalf("expected declared key not to be overwritten: %+v", targetWithDecl)
	}

	// existing has env map and target has no env map
	targetNoEnv := map[string]interface{}{}
	preservePaperclipManagedAdapterConfig(map[string]interface{}{
		"env": map[string]interface{}{
			"SEC":   map[string]interface{}{"type": "user_secret_ref"},
			"PLAIN": "ignore",
		},
	}, targetNoEnv)
	targetEnv := targetNoEnv["env"].(map[string]interface{})
	if targetEnv["SEC"] == nil || targetEnv["PLAIN"] != nil {
		t.Fatalf("expected secret ref copied and plain ignored: %+v", targetEnv)
	}

	// existing has secret ref already declared in target env
	targetWithSecret := map[string]interface{}{
		"env": map[string]interface{}{
			"SEC": "user-value",
		},
	}
	preservePaperclipManagedAdapterConfig(map[string]interface{}{
		"env": map[string]interface{}{
			"SEC": map[string]interface{}{"type": "secret_ref"},
		},
	}, targetWithSecret)
	if targetWithSecret["env"].(map[string]interface{})["SEC"] != "user-value" {
		t.Fatalf("expected declared env key not to be overwritten: %+v", targetWithSecret)
	}

	// existing env has only non-secret-refs (len(targetEnv) remains 0 when starting empty)
	targetEmptyEnv := map[string]interface{}{}
	preservePaperclipManagedAdapterConfig(map[string]interface{}{
		"env": map[string]interface{}{
			"PLAIN": "ignore",
		},
	}, targetEmptyEnv)
	if targetEmptyEnv["env"] != nil {
		t.Fatalf("expected no env key in target when no secret refs exist: %+v", targetEmptyEnv)
	}

	// 4. refreshAgentAdapterConfig branches
	if !refreshAgentAdapterConfig(types.StringNull(), nil).IsNull() {
		t.Fatal("expected nil remote to return null")
	}

	// current is null with empty filtered
	if !refreshAgentAdapterConfig(types.StringNull(), map[string]interface{}{
		"instructionsBundleMode": "managed",
	}).IsNull() {
		t.Fatal("expected null current with empty filtered to return null")
	}

	// current is unknown with empty filtered
	gotUnknownEmpty := refreshAgentAdapterConfig(types.StringUnknown(), map[string]interface{}{
		"instructionsBundleMode": "managed",
	})
	if gotUnknownEmpty.IsNull() || gotUnknownEmpty.IsUnknown() {
		t.Fatalf("expected unknown current with empty filtered to return valid json, got: %#v", gotUnknownEmpty)
	}

	// current is null with non-empty filtered
	gotNullNonEmpty := refreshAgentAdapterConfig(types.StringNull(), map[string]interface{}{
		"customKey": "val",
	})
	if gotNullNonEmpty.IsNull() || !strings.Contains(gotNullNonEmpty.ValueString(), "customKey") {
		t.Fatalf("expected null current with non-empty filtered to return json with customKey, got: %#v", gotNullNonEmpty)
	}

	// current has invalid JSON
	gotInvalid := refreshAgentAdapterConfig(types.StringValue("not-json"), map[string]interface{}{"foo": "bar"})
	if gotInvalid.IsNull() || !strings.Contains(gotInvalid.ValueString(), "foo") {
		t.Fatalf("expected invalid JSON to return remote JSON, got: %#v", gotInvalid)
	}
}

func TestAgentUpdatePreReadError(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/agents/agent-err" {
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	r := NewPaperclipAgentResource().(*PaperclipAgentResource)
	schema := configureTestResource(t, server.URL, r)

	plan := tfsdk.Plan{Schema: schema}
	_ = plan.Set(ctx, &PaperclipAgentResourceModel{
		ID:            types.StringValue("agent-err"),
		AdapterConfig: types.StringValue(`{"confinement":"agy_sandbox"}`),
	})
	state := tfsdk.State{Schema: schema}
	_ = state.Set(ctx, &PaperclipAgentResourceModel{
		ID:            types.StringValue("agent-err"),
		AdapterConfig: types.StringValue(`{"confinement":"agy_sandbox"}`),
	})

	var updateResp resource.UpdateResponse
	updateResp.State = state
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &updateResp)
	if !updateResp.Diagnostics.HasError() {
		t.Fatal("expected error when GetAgent fails during Update with adapterConfig")
	}
}

