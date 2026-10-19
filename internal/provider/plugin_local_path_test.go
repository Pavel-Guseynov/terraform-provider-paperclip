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
	}{
		{"create retains stable source", "create", true, "/run/current-system/sw/lib/node_modules/@paperclipai/plugin-ci-wake", []string{"POST /api/plugins/install"}},
		{"disabled create retains stable source", "create", false, "/run/current-system/sw/lib/node_modules/@paperclipai/plugin-ci-wake", []string{"POST /api/plugins/install", "POST /api/plugins/plug-local/disable"}},
		{"refresh retains stable source", "read", true, "/run/current-system/sw/lib/node_modules/@paperclipai/plugin-ci-wake", []string{"GET /api/plugins/plug-local"}},
		{"update retains stable source", "update", true, "/run/current-system/sw/lib/node_modules/@paperclipai/plugin-ci-wake", []string{"GET /api/plugins/plug-local"}},
		{"import restores installed path", "import", true, "/resolved/plugins/ci-wake", []string{"GET /api/plugins/plug-local"}},
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
			model.PurgeOnDestroy = types.BoolValue(true)
			if d := plan.Set(ctx, &model); d.HasError() {
				t.Fatal(d)
			}
			switch tc.operation {
			case "create":
				resp := resource.CreateResponse{State: tfsdk.State{Schema: schema}}
				r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
				if resp.Diagnostics.HasError() {
					t.Fatal(resp.Diagnostics)
				}
				state = resp.State
			case "update":
				resp := resource.UpdateResponse{State: state}
				r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &resp)
				if resp.Diagnostics.HasError() {
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
