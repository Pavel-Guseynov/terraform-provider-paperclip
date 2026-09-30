package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestLocalEnvironmentResourceLifecycle(t *testing.T) {
	ctx := context.Background()
	bindings := map[string]interface{}{"PATH": "/old/bin", "EDITOR": "vi"}
	fail, writes := false, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if fail {
			w.WriteHeader(403)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		env := map[string]interface{}{"id": "local-id", "driver": "local", "envVars": bindings}
		if req.URL.Path == "/api/companies/company/environments" {
			_ = json.NewEncoder(w).Encode([]interface{}{env})
		} else if req.URL.Path == "/api/environments/local-id" {
			if req.Method == http.MethodPatch {
				var body struct {
					EnvVars map[string]interface{} `json:"envVars"`
				}
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				bindings = body.EnvVars
				writes++
			}
			_ = json.NewEncoder(w).Encode(env)
		} else {
			t.Fatalf("unexpected request %s %s", req.Method, req.URL.Path)
		}
	}))
	defer server.Close()
	r := NewPaperclipLocalEnvironmentResource().(*PaperclipLocalEnvironmentResource)
	schema := configureTestResource(t, server.URL, r)
	var meta resource.MetadataResponse
	r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "paperclip"}, &meta)
	if meta.TypeName != "paperclip_local_environment" {
		t.Fatal(meta.TypeName)
	}
	model := PaperclipLocalEnvironmentModel{CompanyID: types.StringValue("company"), ID: types.StringUnknown(), InheritServicePath: types.BoolValue(true)}
	plan := tfsdk.Plan{Schema: schema}
	if diags := plan.Set(ctx, &model); diags.HasError() {
		t.Fatal(diags)
	}
	created := resource.CreateResponse{State: tfsdk.State{Schema: schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
	if created.Diagnostics.HasError() || writes != 1 {
		t.Fatalf("create failed: %v", created.Diagnostics)
	}
	if diags := created.State.Get(ctx, &model); diags.HasError() {
		t.Fatal(diags)
	}
	if model.ID.ValueString() != "local-id" || !model.InheritServicePath.ValueBool() {
		t.Fatal("invalid adopted state")
	}
	bindings["PATH"] = "/stale/bin"
	read := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	var drift PaperclipLocalEnvironmentModel
	if diags := read.State.Get(ctx, &drift); diags.HasError() {
		t.Fatal(diags)
	}
	if drift.InheritServicePath.ValueBool() {
		t.Fatal("persisted override was hidden from plan")
	}
	if diags := plan.Set(ctx, &model); diags.HasError() {
		t.Fatal(diags)
	}
	updated := resource.UpdateResponse{State: created.State}
	r.Update(ctx, resource.UpdateRequest{Plan: plan}, &updated)
	if updated.Diagnostics.HasError() || writes != 2 {
		t.Fatalf("update failed: %v", updated.Diagnostics)
	}
	r.Delete(ctx, resource.DeleteRequest{State: updated.State}, &resource.DeleteResponse{})
	if writes != 2 {
		t.Fatal("delete mutated producer environment")
	}
	for _, value := range []types.Bool{types.BoolValue(true), types.BoolValue(false), types.BoolUnknown()} {
		model.InheritServicePath = value
		if diags := plan.Set(ctx, &model); diags.HasError() {
			t.Fatal(diags)
		}
		config := tfsdk.Config{Schema: schema, Raw: plan.Raw}
		validation := resource.ValidateConfigResponse{}
		r.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: config}, &validation)
		if validation.Diagnostics.HasError() != value.Equal(types.BoolValue(false)) {
			t.Fatal("invalid inheritance validation")
		}
	}
	model.InheritServicePath = types.BoolValue(true)
	if diags := plan.Set(ctx, &model); diags.HasError() {
		t.Fatal(diags)
	}
	fail = true
	read = resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	if !read.Diagnostics.HasError() {
		t.Fatal("read failure hidden")
	}
	updated = resource.UpdateResponse{State: created.State}
	r.Update(ctx, resource.UpdateRequest{Plan: plan}, &updated)
	if !updated.Diagnostics.HasError() {
		t.Fatal("update failure hidden")
	}
}
