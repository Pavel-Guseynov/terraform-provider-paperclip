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

func TestSecretDeleteChecksLiveAgentReferencesBeforeWriting(t *testing.T) {
	for _, test := range []struct {
		name         string
		agents       string
		listStatus   int
		deleteStatus int
		wantError    string
		wantDeletes  int
	}{
		{name: "active and paused references", agents: `[{"id":"a1","name":"ZCoder","status":"active","adapterConfig":{"env":{"TOKEN":{"type":"secret_ref","secretId":"secret-1"},"OTHER":{"type":"secret_ref","secretId":"secret-1"}}}},{"id":"a2","name":"Reviewer","status":"paused","adapterConfig":{"env":{"TOKEN":{"type":"secret_ref","secretId":"secret-1"}}}}]`, wantError: `Reviewer (a2), ZCoder (a1)`},
		{name: "terminated reference", agents: `[{"id":"a1","name":"Retired","status":"terminated","adapterConfig":{"env":{"TOKEN":{"type":"secret_ref","secretId":"secret-1"}}}}]`, wantDeletes: 1},
		{name: "unrelated bindings", agents: `[{"id":"a1","status":"active","adapterConfig":{"env":{"OTHER":{"type":"secret_ref","secretId":"secret-2"},"PLAIN":"literal","USER":{"type":"user_secret_ref","key":"secret-1"}}}},{"id":"a2","status":"idle","adapterConfig":{}}]`, wantDeletes: 1},
		{name: "no agents", agents: `[]`, wantDeletes: 1},
		{name: "list failure fails closed", agents: `{"error":"cannot list agents"}`, listStatus: 403, wantError: "403"},
		{name: "delete failure", agents: `[]`, deleteStatus: 500, wantError: "500", wantDeletes: 1},
		{name: "already deleted", agents: `[]`, deleteStatus: 404, wantDeletes: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			deletes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch {
				case req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-1/agents":
					if test.listStatus != 0 {
						w.WriteHeader(test.listStatus)
					}
					_, _ = w.Write([]byte(test.agents))
				case req.Method == http.MethodDelete && req.URL.Path == "/api/secrets/secret-1":
					deletes++
					if test.deleteStatus != 0 {
						w.WriteHeader(test.deleteStatus)
					}
					_, _ = w.Write([]byte(`{"error":"not found"}`))
				default:
					t.Errorf("unexpected request: %s %s", req.Method, req.URL)
					http.NotFound(w, req)
				}
			}))
			defer server.Close()
			r := NewPaperclipSecretResource().(*PaperclipSecretResource)
			schema := configureTestResource(t, server.URL, r)
			state := tfsdk.State{Schema: schema}
			if diags := state.Set(context.Background(), &PaperclipSecretResourceModel{ID: types.StringValue("secret-1"), CompanyID: types.StringValue("comp-1")}); diags.HasError() {
				t.Fatal(diags)
			}
			response := resource.DeleteResponse{State: state}
			r.Delete(context.Background(), resource.DeleteRequest{State: state}, &response)
			if deletes != test.wantDeletes {
				t.Fatalf("DELETE writes: got %d, want %d", deletes, test.wantDeletes)
			}
			if test.wantError == "" {
				if response.Diagnostics.HasError() {
					t.Fatal(response.Diagnostics)
				}
			} else if !response.Diagnostics.HasError() || !strings.Contains(response.Diagnostics.Errors()[0].Detail(), test.wantError) {
				t.Fatalf("expected diagnostic containing %q, got %v", test.wantError, response.Diagnostics)
			}
		})
	}
}

func TestAgentReadSurfacesUndeclaredSecretBindings(t *testing.T) {
	for _, typed := range []bool{false, true} {
		t.Run(map[bool]string{false: "adapter_config env", true: "top-level env"}[typed], func(t *testing.T) {
			ctx := context.Background()
			remote := map[string]interface{}{"env": map[string]interface{}{
				"TOKEN": map[string]interface{}{"type": "secret_ref", "secretId": "deleted-secret", "version": "latest"},
				"USER": map[string]interface{}{"type": "user_secret_ref", "key": "user-token", "version": "latest"},
			}}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if req.Method != http.MethodGet || req.URL.Path != "/api/agents/agent-1" {
					t.Errorf("unexpected request: %s %s", req.Method, req.URL)
					http.NotFound(w, req)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "agent-1", "companyId": "comp-1", "name": "Coder", "status": "active", "adapterConfig": remote})
			}))
			defer server.Close()
			r := NewPaperclipAgentResource().(*PaperclipAgentResource)
			schema := configureTestResource(t, server.URL, r)
			model := PaperclipAgentResourceModel{ID: types.StringValue("agent-1"), CompanyID: types.StringValue("comp-1"), Name: types.StringValue("Coder"), AdapterConfig: types.StringValue(`{}`)}
			if typed {
				model.Env = map[string]EnvBindingModel{}
			}
			state := tfsdk.State{Schema: schema}
			if diags := state.Set(ctx, &model); diags.HasError() {
				t.Fatal(diags)
			}
			response := resource.ReadResponse{State: state}
			r.Read(ctx, resource.ReadRequest{State: state}, &response)
			if response.Diagnostics.HasError() {
				t.Fatal(response.Diagnostics)
			}
			var got PaperclipAgentResourceModel
			if diags := response.State.Get(ctx, &got); diags.HasError() {
				t.Fatal(diags)
			}
			if typed {
				if got.Env["TOKEN"].SecretID.ValueString() != "deleted-secret" || got.Env["USER"].Key.ValueString() != "user-token" {
					t.Fatalf("undeclared env disappeared: %+v", got.Env)
				}
				if strings.Contains(got.AdapterConfig.ValueString(), "TOKEN") {
					t.Fatal("binding has two state owners")
				}
			} else if !strings.Contains(got.AdapterConfig.ValueString(), "deleted-secret") || !strings.Contains(got.AdapterConfig.ValueString(), "user-token") {
				t.Fatalf("undeclared env disappeared: %s", got.AdapterConfig.ValueString())
			}
		})
	}
}

func TestAgentEnvRefreshKeepsAdapterOwnedBindingsSeparate(t *testing.T) {
	remote := map[string]interface{}{"env": map[string]interface{}{
		"RAW":          map[string]interface{}{"type": "secret_ref", "secretId": "raw-secret"},
		"TYPED":        map[string]interface{}{"type": "secret_ref", "secretId": "typed-secret"},
		"UNKNOWN_TYPE": map[string]interface{}{"type": "unknown"},
		"INVALID":      true,
	}}
	typed := refreshAgentEnvBindings(map[string]EnvBindingModel{}, remote, types.StringValue(`{"env":{"RAW":{"type":"secret_ref","secretId":"raw-secret"}}}`))
	if len(typed) != 1 || typed["TYPED"].SecretID.ValueString() != "typed-secret" {
		t.Fatalf("expected only the discovered typed binding, got %+v", typed)
	}
	adapter := refreshAgentAdapterState(types.StringValue(`{"env":{"RAW":{"type":"secret_ref","secretId":"raw-secret"}}}`), remote, typed)
	if strings.Contains(adapter.ValueString(), "typed-secret") || !strings.Contains(adapter.ValueString(), "raw-secret") {
		t.Fatalf("env ownership duplicated or lost: %s", adapter.ValueString())
	}
}
