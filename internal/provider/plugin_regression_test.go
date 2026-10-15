package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"git.guseynov.org/guseynov/paperclip/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestPluginConfigPlansPreserveRefreshedErrorOnlyWithoutChanges(t *testing.T) {
	ctx := context.Background()
	r := NewPaperclipPluginConfigResource()
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	for _, tc := range []struct {
		name          string
		change        string
		lastError     types.String
		wantPreserved bool
	}{
		{"imported null error", "", types.StringNull(), true},
		{"refreshed error", "", types.StringValue("worker validation failed"), true},
		{"config update", "config", types.StringNull(), false},
		{"company replacement", "company", types.StringNull(), false},
		{"plugin replacement", "plugin", types.StringNull(), false},
		{"create", "create", types.StringNull(), false},
		{"destroy", "destroy", types.StringNull(), false},
		{"invalid plan", "bad plan", types.StringNull(), false},
		{"invalid state", "bad state", types.StringNull(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := PaperclipPluginConfigResourceModel{
				ID: types.StringValue("comp-1/plug-1"), CompanyID: types.StringValue("comp-1"),
				PluginID: types.StringValue("plug-1"), Config: types.StringValue(`{"timeout":10}`), LastError: tc.lastError,
			}
			state := tfsdk.State{Schema: schemaResp.Schema}
			if d := state.Set(ctx, &model); d.HasError() {
				t.Fatal(d)
			}
			model.LastError = types.StringUnknown()
			switch tc.change {
			case "config":
				model.Config = types.StringValue(`{"timeout":20}`)
			case "company":
				model.CompanyID = types.StringValue("comp-2")
			case "plugin":
				model.PluginID = types.StringValue("plug-2")
			case "create":
				state = tfsdk.State{Schema: schemaResp.Schema}
			case "bad state":
				state.Raw = tftypes.NewValue(tftypes.String, "invalid")
			}
			plan := tfsdk.Plan{Schema: schemaResp.Schema}
			if tc.change != "destroy" {
				if d := plan.Set(ctx, &model); d.HasError() {
					t.Fatal(d)
				}
			}
			if tc.change == "bad plan" {
				plan.Raw = tftypes.NewValue(tftypes.String, "invalid")
			}
			resp := resource.ModifyPlanResponse{Plan: plan}
			if modifier, ok := r.(resource.ResourceWithModifyPlan); ok {
				modifier.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: state}, &resp)
			}
			if tc.change == "bad plan" || tc.change == "bad state" {
				if !resp.Diagnostics.HasError() {
					t.Fatal("malformed framework data must produce diagnostics")
				}
				return
			}
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}
			if tc.change == "destroy" {
				if !resp.Plan.Raw.IsNull() {
					t.Fatal("destroy plan must remain null")
				}
				return
			}
			var got types.String
			if d := resp.Plan.GetAttribute(ctx, path.Root("last_error"), &got); d.HasError() {
				t.Fatal(d)
			}
			if tc.wantPreserved {
				if !got.Equal(tc.lastError) {
					t.Fatalf("unchanged config must preserve refreshed last_error, got %s", got)
				}
			} else if !got.IsUnknown() {
				t.Fatalf("changed config must recompute last_error, got %s", got)
			}
		})
	}
}

func TestPluginConfigRejectsUnclassifiedAllowlist(t *testing.T) {
	for _, raw := range []string{
		`{"token":{"type":"secret_ref","secretId":"sec-1","projectionAllowlistKey":"slack.bot_token"}}`,
		`{"token":{"type":"secret_ref","secretId":"sec-1","projectionClass":"unclassified","projectionAllowlistKey":"slack.bot_token"}}`,
		`{"token":{"type":"secret_ref","secret_id":"sec-1","projection_class":"unclassified","projection_allowlist_key":"slack.bot_token"}}`,
	} {
		t.Run(raw, func(t *testing.T) {
			d := ValidatePluginConfigJSONPlan(path.Root("config"), raw)
			if !d.HasError() {
				t.Fatal("unclassified secret references must reject projection allowlist keys")
			}
			if d := ValidateEnvBindingsJSONPlan(path.Root("env"), raw, "agent"); !d.HasError() {
				t.Fatal("JSON environment bindings must enforce the same projection rule")
			}
		})
	}
}

func TestPluginConfigPreservesEquivalentSecretReferenceSpelling(t *testing.T) {
	ctx := context.Background()
	snake := types.StringValue(`{"nested":[{"type":"user_secret_ref","key":"token","allow_missing_override":false},{"type":"secret_ref","secret_id":"sec-1","projection_class":"class_3_static_lease","projection_allowlist_key":"slack.bot_token","version":"2"}],"secret_id":"ordinary_config_key"}`)
	remote := NormalizePluginConfigForAPI(snake.ValueString())
	if got := refreshSemanticJSONNormalized(snake, remote, normalizePluginConfigValue); !got.Equal(snake) {
		t.Fatalf("refresh changed equivalent planned configuration: %s", got)
	}
	data, err := json.Marshal(remote)
	if err != nil {
		t.Fatal(err)
	}
	modifier := semanticJSONPlanModifier{normalize: normalizePluginConfigValue}
	response := planmodifier.StringResponse{PlanValue: snake}
	state := types.StringValue(string(data))
	modifier.PlanModifyString(ctx, planmodifier.StringRequest{PlanValue: snake, StateValue: state}, &response)
	if !response.PlanValue.Equal(state) {
		t.Fatalf("imported camelCase references produced a plan diff: %s", response.PlanValue)
	}
	if semanticJSONEqualNormalized(snake.ValueString(), `{"nested":[{"type":"secret_ref","secretId":"different"}]}`, normalizePluginConfigValue) {
		t.Fatal("changed secret identity must remain visible as drift")
	}
}

func TestPluginReadRefreshesKnownLocalPath(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet || req.URL.Path != "/api/plugins/plug-1" {
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(client.PaperclipPlugin{ID: "plug-1", PackageName: "@paperclip/plugin-test", Version: "1.0.0", Status: "ready"})
	}))
	defer server.Close()
	r := NewPaperclipPluginResource().(*PaperclipPluginResource)
	schema := configureTestResource(t, server.URL, r)
	state := tfsdk.State{Schema: schema}
	d := state.Set(ctx, &PaperclipPluginResourceModel{ID: types.StringValue("plug-1"), IsLocalPath: types.BoolValue(true)})
	if d.HasError() {
		t.Fatal(d)
	}
	resp := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	var got PaperclipPluginResourceModel
	if d := resp.State.Get(ctx, &got); d.HasError() {
		t.Fatal(d)
	}
	if got.IsLocalPath.ValueBool() {
		t.Fatal("remote packagePath is absent, but Read retained is_local_path=true")
	}
}
