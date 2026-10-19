package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestPluginLocalInstallSource(t *testing.T) {
	for _, tc := range []struct {
		name       string
		operation  string
		enabled    bool
		wantSource string
		requests   []string
		wantError  bool
	}{
		{"create retains stable source", "create", true, "/run/current-system/sw/lib/node_modules/@paperclipai/plugin-ci-wake", []string{"POST /api/plugins/install"}, false},
		{"disabled create retains stable source", "create", false, "/run/current-system/sw/lib/node_modules/@paperclipai/plugin-ci-wake", []string{"POST /api/plugins/install", "POST /api/plugins/plug-local/disable"}, false},
		{"refresh retains stable source", "read", true, "/run/current-system/sw/lib/node_modules/@paperclipai/plugin-ci-wake", []string{"GET /api/plugins/plug-local"}, false},
		{"update retains stable source", "update", true, "/run/current-system/sw/lib/node_modules/@paperclipai/plugin-ci-wake", []string{"GET /api/plugins/plug-local"}, false},
		{"recovery upgrades the existing registration source", "recover", true, "/run/current-system/sw/lib/node_modules/@paperclipai/plugin-ci-wake", []string{"POST /api/plugins/plug-local/upgrade", "GET /api/plugins/plug-local"}, false},
		{"ignored recovery preserves prior state", "ignored", true, "@paperclipai/plugin-ci-wake", []string{"POST /api/plugins/plug-local/upgrade", "GET /api/plugins/plug-local"}, true},
		{"missing recovery path preserves prior state", "missing", true, "@paperclipai/plugin-ci-wake", []string{"POST /api/plugins/plug-local/upgrade", "GET /api/plugins/plug-local"}, true},
		{"import restores installed path", "import", true, "/resolved/plugins/ci-wake", []string{"GET /api/plugins/plug-local"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			installedPath := "/resolved/plugins/ci-wake"
			remote := client.PaperclipPlugin{
				ID: "plug-local", PluginKey: "ci-wake", PackageName: "@paperclipai/plugin-ci-wake",
				PackagePath: &installedPath, Version: "1.0.0", Status: "ready", APIVersion: 1,
			}
			var requests []string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				requests = append(requests, req.Method+" "+req.URL.Path)
				switch req.Method + " " + req.URL.Path {
				case "POST /api/plugins/install":
					var input client.PaperclipPluginInstallInput
					if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
						t.Error(err)
					}
					if input.PackageName != "/run/current-system/sw/lib/node_modules/@paperclipai/plugin-ci-wake" || !input.IsLocalPath || input.Version != "1.0.0" {
						t.Errorf("unexpected install request: %+v", input)
					}
				case "POST /api/plugins/plug-local/disable":
					remote.Status = "disabled"
				case "GET /api/plugins/plug-local":
				case "POST /api/plugins/plug-local/upgrade":
					var body map[string]string
					if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					if !reflect.DeepEqual(body, map[string]string{"packagePath": "/run/current-system/sw/lib/node_modules/@paperclipai/plugin-ci-wake"}) {
						t.Errorf("unexpected recovery request: %v", body)
					}
					if tc.operation == "recover" {
						installedPath = body["packagePath"]
					}
					if tc.operation == "missing" {
						remote.PackagePath = nil
					}
				default:
					t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if err := json.NewEncoder(w).Encode(remote); err != nil {
					t.Error(err)
				}
			}))
			defer server.Close()
			r := NewPaperclipPluginResource().(*PaperclipPluginResource)
			schema := configureTestResource(t, server.URL, r)
			model := PaperclipPluginResourceModel{
				ID: types.StringValue("plug-local"),
				PackageName: types.StringValue("/run/current-system/sw/lib/node_modules/@paperclipai/plugin-ci-wake"),
				Version: types.StringValue("1.0.0"), IsLocalPath: types.BoolValue(true),
				Enabled: types.BoolValue(tc.enabled), PurgeOnDestroy: types.BoolValue(false),
			}
			state := tfsdk.State{Schema: schema}
			if d := state.Set(ctx, &model); d.HasError() {
				t.Fatal(d)
			}
			plan := tfsdk.Plan{Schema: schema}
			if tc.operation == "recover" || tc.wantError {
				old := model
				old.PackageName = types.StringValue("@paperclipai/plugin-ci-wake")
				if d := state.Set(ctx, &old); d.HasError() {
					t.Fatal(d)
				}
			}
			model.PurgeOnDestroy = types.BoolValue(true)
			if d := plan.Set(ctx, &model); d.HasError() {
				t.Fatal(d)
			}
			if tc.operation == "recover" {
				var replacement planmodifier.StringResponse
				schema.Attributes["package_name"].(resourceschema.StringAttribute).PlanModifiers[0].PlanModifyString(ctx,
					planmodifier.StringRequest{State: state, Plan: plan, StateValue: types.StringValue("@paperclipai/plugin-ci-wake"), PlanValue: model.PackageName}, &replacement)
				if replacement.Diagnostics.HasError() || replacement.RequiresReplace {
					t.Fatalf("recovery must update in place: %+v", replacement)
				}
			}
			switch tc.operation {
			case "create":
				resp := resource.CreateResponse{State: tfsdk.State{Schema: schema}}
				r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
				if resp.Diagnostics.HasError() {
					t.Fatal(resp.Diagnostics)
				}
				state = resp.State
			case "update", "recover", "ignored", "missing":
				resp := resource.UpdateResponse{State: state}
				r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &resp)
				if resp.Diagnostics.HasError() != tc.wantError {
					t.Fatal(resp.Diagnostics)
				}
				if tc.wantError && resp.Diagnostics[0].Summary() != "Native plugin source recovery was not retained" {
					t.Fatal(resp.Diagnostics)
				}
				state = resp.State
			case "import":
				resp := resource.ImportStateResponse{State: tfsdk.State{Schema: schema}}
				if d := resp.State.Set(ctx, &PaperclipPluginResourceModel{}); d.HasError() {
					t.Fatal(d)
				}
				r.ImportState(ctx, resource.ImportStateRequest{ID: "plug-local"}, &resp)
				if resp.Diagnostics.HasError() {
					t.Fatal(resp.Diagnostics)
				}
				state = resp.State
			}
			if tc.operation == "read" || tc.operation == "import" {
				resp := resource.ReadResponse{State: state}
				r.Read(ctx, resource.ReadRequest{State: state}, &resp)
				if resp.Diagnostics.HasError() {
					t.Fatal(resp.Diagnostics)
				}
				state = resp.State
			}
			var got PaperclipPluginResourceModel
			if d := state.Get(ctx, &got); d.HasError() {
				t.Fatal(d)
			}
			if got.PackageName.ValueString() != tc.wantSource || !got.IsLocalPath.ValueBool() || got.Enabled.ValueBool() != tc.enabled || got.ID.ValueString() != "plug-local" {
				t.Fatalf("local plugin source or lifecycle changed: %+v", got)
			}
			if !reflect.DeepEqual(requests, tc.requests) {
				t.Fatalf("unexpected API operations: %v; want %v", requests, tc.requests)
			}
		})
	}
}
