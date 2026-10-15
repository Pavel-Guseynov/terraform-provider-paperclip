package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestPaperclipPluginResourceLifecycle(t *testing.T) {
	ctx := context.Background()

	var (
		serverFail       bool
		disableFail      bool
		upgradeFail      bool
		enableFail       bool
		deleteFail       bool
		fetchFail        bool
		upgradeStatus    = "ready"
		installedVersion = "1.0.0"
		installedStatus  = "ready"
		lastPurge        bool
		installedPath    *string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if serverFail {
			http.Error(w, `{"error":"server failure"}`, http.StatusInternalServerError)
			return
		}

		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/plugins/install":
			var body client.PaperclipPluginInstallInput
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.PackageName == "forbidden-non-admin" {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"Only instance administrators may install plugins","code":"forbidden_instance_admin"}`))
				return
			}
			if body.PackageName == "forbidden-operator-managed" {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"Plugin management is managed by the hosting operator on this instance","code":"settings_operator_managed"}`))
				return
			}
			installedVersion = body.Version
			if installedVersion == "" {
				installedVersion = "1.0.0"
			}
			installedStatus = "ready"
			installedPath = nil
			if body.IsLocalPath {
				pkgPath := body.PackageName
				installedPath = &pkgPath
			}
			_ = json.NewEncoder(w).Encode(client.PaperclipPlugin{
				ID:          "plug-1",
				PluginKey:   "test-plugin",
				PackageName: body.PackageName,
				Version:     installedVersion,
				Status:      installedStatus,
				PackagePath: installedPath,
				APIVersion:  1,
			})

		case r.Method == http.MethodGet && r.URL.Path == "/api/plugins/plug-1":
			if fetchFail {
				http.Error(w, `{"error":"fetch error"}`, http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(client.PaperclipPlugin{
				ID:          "plug-1",
				PluginKey:   "test-plugin",
				PackageName: "@paperclipai/plugin-test",
				PackagePath: installedPath,
				Version:     installedVersion,
				Status:      installedStatus,
				APIVersion:  1,
			})

		case r.Method == http.MethodGet && r.URL.Path == "/api/plugins/plug-missing":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))

		case r.Method == http.MethodPost && r.URL.Path == "/api/plugins/plug-1/disable":
			if disableFail {
				http.Error(w, `{"error":"disable error"}`, http.StatusBadRequest)
				return
			}
			installedStatus = "disabled"
			_ = json.NewEncoder(w).Encode(client.PaperclipPlugin{
				ID:     "plug-1",
				Status: "disabled",
			})

		case r.Method == http.MethodPost && r.URL.Path == "/api/plugins/plug-1/enable":
			if enableFail {
				http.Error(w, `{"error":"enable error"}`, http.StatusBadRequest)
				return
			}
			installedStatus = "ready"
			_ = json.NewEncoder(w).Encode(client.PaperclipPlugin{
				ID:     "plug-1",
				Status: "ready",
			})

		case r.Method == http.MethodPost && r.URL.Path == "/api/plugins/plug-1/upgrade":
			if upgradeFail {
				http.Error(w, `{"error":"upgrade error"}`, http.StatusBadRequest)
				return
			}
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if v, ok := body["version"].(string); ok && v != "" {
				installedVersion = v
			}
			installedStatus = upgradeStatus
			_ = json.NewEncoder(w).Encode(client.PaperclipPlugin{
				ID:      "plug-1",
				Version: installedVersion,
				Status:  installedStatus,
			})

		case r.Method == http.MethodDelete && r.URL.Path == "/api/plugins/plug-1":
			if deleteFail {
				http.Error(w, `{"error":"delete error"}`, http.StatusBadRequest)
				return
			}
			lastPurge = r.URL.Query().Get("purge") == "true"
			_ = json.NewEncoder(w).Encode(client.PaperclipPlugin{
				ID:     "plug-1",
				Status: "uninstalled",
			})

		case r.Method == http.MethodDelete && r.URL.Path == "/api/plugins/plug-missing":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	r := NewPaperclipPluginResource().(*PaperclipPluginResource)
	schema := configureTestResource(t, server.URL, r)

	// Configure error paths
	badConfReq := resource.ConfigureRequest{ProviderData: "invalid"}
	var badConfResp resource.ConfigureResponse
	r.Configure(ctx, badConfReq, &badConfResp)
	if !badConfResp.Diagnostics.HasError() {
		t.Fatal("expected error from Configure with invalid ProviderData")
	}
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: nil}, &badConfResp)

	// 1. Create with non-admin failure
	nonAdminPlan := PaperclipPluginResourceModel{
		PackageName: types.StringValue("forbidden-non-admin"),
		IsLocalPath: types.BoolValue(false),
	}
	var nonAdminCreateResp resource.CreateResponse
	nonAdminCreateResp.State = tfsdk.State{Schema: schema}
	planNonAdmin := tfsdk.Plan{Schema: schema}
	_ = planNonAdmin.Set(ctx, &nonAdminPlan)
	r.Create(ctx, resource.CreateRequest{Plan: planNonAdmin}, &nonAdminCreateResp)
	if !nonAdminCreateResp.Diagnostics.HasError() {
		t.Fatal("expected error on non-admin install")
	}

	// 2. Create with hidden setting failure
	hiddenPlan := PaperclipPluginResourceModel{
		PackageName: types.StringValue("forbidden-operator-managed"),
		IsLocalPath: types.BoolValue(false),
	}
	var hiddenCreateResp resource.CreateResponse
	hiddenCreateResp.State = tfsdk.State{Schema: schema}
	planHidden := tfsdk.Plan{Schema: schema}
	_ = planHidden.Set(ctx, &hiddenPlan)
	r.Create(ctx, resource.CreateRequest{Plan: planHidden}, &hiddenCreateResp)
	if !hiddenCreateResp.Diagnostics.HasError() {
		t.Fatal("expected error on hidden setting install")
	}

	// 3. Create with enabled = false but disable failure
	disableFail = true
	disabledErrPlan := PaperclipPluginResourceModel{
		PackageName: types.StringValue("@paperclipai/plugin-test"),
		Version:     types.StringValue("1.0.0"),
		IsLocalPath: types.BoolValue(true),
		Enabled:     types.BoolValue(false),
	}
	var disabledErrResp resource.CreateResponse
	disabledErrResp.State = tfsdk.State{Schema: schema}
	planDisabledErr := tfsdk.Plan{Schema: schema}
	_ = planDisabledErr.Set(ctx, &disabledErrPlan)
	r.Create(ctx, resource.CreateRequest{Plan: planDisabledErr}, &disabledErrResp)
	if !disabledErrResp.Diagnostics.HasError() {
		t.Fatal("expected error when disable fails after install")
	}
	disableFail = false

	// 3b. Create with enabled = false and null version/purge (success)
	planDisabledSuccess := PaperclipPluginResourceModel{
		PackageName: types.StringValue("@paperclipai/plugin-test"),
		Enabled:     types.BoolValue(false),
	}
	var disabledSuccessResp resource.CreateResponse
	disabledSuccessResp.State = tfsdk.State{Schema: schema}
	planDisabled := tfsdk.Plan{Schema: schema}
	_ = planDisabled.Set(ctx, &planDisabledSuccess)
	r.Create(ctx, resource.CreateRequest{Plan: planDisabled}, &disabledSuccessResp)
	if disabledSuccessResp.Diagnostics.HasError() {
		t.Fatalf("expected create with enabled=false to succeed: %v", disabledSuccessResp.Diagnostics)
	}

	// 4. Create successful (enabled = true)
	planModel := PaperclipPluginResourceModel{
		PackageName:    types.StringValue("@paperclipai/plugin-test"),
		Version:        types.StringValue("1.0.0"),
		IsLocalPath:    types.BoolValue(true),
		Enabled:        types.BoolValue(true),
		PurgeOnDestroy: types.BoolValue(true),
	}
	plan := tfsdk.Plan{Schema: schema}
	_ = plan.Set(ctx, &planModel)
	createResp := resource.CreateResponse{State: tfsdk.State{Schema: schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("Create failed: %v", createResp.Diagnostics)
	}

	var stateModel PaperclipPluginResourceModel
	_ = createResp.State.Get(ctx, &stateModel)
	if stateModel.ID.ValueString() != "plug-1" || stateModel.Status.ValueString() != "ready" {
		t.Fatalf("unexpected state after create: %v", stateModel)
	}

	// 5. Read successful
	readResp := resource.ReadResponse{State: createResp.State}
	r.Read(ctx, resource.ReadRequest{State: createResp.State}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("Read failed: %v", readResp.Diagnostics)
	}

	// 5b. Read with null is_local_path and purge_on_destroy
	nullsStateModel := PaperclipPluginResourceModel{
		ID: types.StringValue("plug-1"),
	}
	stateNulls := tfsdk.State{Schema: schema}
	_ = stateNulls.Set(ctx, &nullsStateModel)
	readNullsResp := resource.ReadResponse{State: stateNulls}
	r.Read(ctx, resource.ReadRequest{State: stateNulls}, &readNullsResp)
	if readNullsResp.Diagnostics.HasError() {
		t.Fatalf("Read with nulls failed: %v", readNullsResp.Diagnostics)
	}

	// 6. Read 404 removes resource
	missingStateModel := PaperclipPluginResourceModel{
		ID: types.StringValue("plug-missing"),
	}
	stateMissing := tfsdk.State{Schema: schema}
	_ = stateMissing.Set(ctx, &missingStateModel)
	readMissingResp := resource.ReadResponse{State: stateMissing}
	r.Read(ctx, resource.ReadRequest{State: stateMissing}, &readMissingResp)
	if readMissingResp.Diagnostics.HasError() || !readMissingResp.State.Raw.IsNull() {
		t.Fatal("expected missing resource to be removed from state")
	}

	// 7. Read fetch error
	fetchFail = true
	readErrResp := resource.ReadResponse{State: createResp.State}
	r.Read(ctx, resource.ReadRequest{State: createResp.State}, &readErrResp)
	if !readErrResp.Diagnostics.HasError() {
		t.Fatal("expected read error when fetch fails")
	}
	fetchFail = false

	// 8. Update: upgrade version
	updatePlanModel := PaperclipPluginResourceModel{
		ID:             types.StringValue("plug-1"),
		PackageName:    types.StringValue("@paperclipai/plugin-test"),
		Version:        types.StringValue("1.1.0"),
		IsLocalPath:    types.BoolValue(true),
		Enabled:        types.BoolValue(true),
		PurgeOnDestroy: types.BoolValue(true),
	}
	updatePlan := tfsdk.Plan{Schema: schema}
	_ = updatePlan.Set(ctx, &updatePlanModel)
	updateResp := resource.UpdateResponse{State: createResp.State}
	r.Update(ctx, resource.UpdateRequest{Plan: updatePlan, State: createResp.State}, &updateResp)
	if updateResp.Diagnostics.HasError() {
		t.Fatalf("Update failed: %v", updateResp.Diagnostics)
	}

	var updatedState PaperclipPluginResourceModel
	_ = updateResp.State.Get(ctx, &updatedState)
	if updatedState.Version.ValueString() != "1.1.0" || updatedState.Status.ValueString() != "ready" {
		t.Fatalf("unexpected state after update: %v", updatedState)
	}

	// 9. Update: upgrade to upgrade_pending
	upgradeStatus = "upgrade_pending"
	pendingPlanModel := PaperclipPluginResourceModel{
		ID:             types.StringValue("plug-1"),
		PackageName:    types.StringValue("@paperclipai/plugin-test"),
		Version:        types.StringValue("2.0.0"),
		IsLocalPath:    types.BoolValue(true),
		Enabled:        types.BoolValue(true),
		PurgeOnDestroy: types.BoolValue(true),
	}
	pendingPlan := tfsdk.Plan{Schema: schema}
	_ = pendingPlan.Set(ctx, &pendingPlanModel)
	pendingResp := resource.UpdateResponse{State: updateResp.State}
	r.Update(ctx, resource.UpdateRequest{Plan: pendingPlan, State: updateResp.State}, &pendingResp)
	if pendingResp.Diagnostics.HasError() {
		t.Fatalf("Update for pending failed: %v", pendingResp.Diagnostics)
	}

	var pendingState PaperclipPluginResourceModel
	_ = pendingResp.State.Get(ctx, &pendingState)
	if !pendingState.UpgradePending.ValueBool() || pendingState.Status.ValueString() != "upgrade_pending" {
		t.Fatalf("expected upgrade_pending = true, got: %v", pendingState)
	}
	upgradeStatus = "ready"

	// 9b. Update: disable plugin
	disablePlanModel := PaperclipPluginResourceModel{
		ID:             types.StringValue("plug-1"),
		PackageName:    types.StringValue("@paperclipai/plugin-test"),
		Version:        types.StringValue("2.0.0"),
		IsLocalPath:    types.BoolValue(true),
		Enabled:        types.BoolValue(false),
		PurgeOnDestroy: types.BoolValue(true),
	}
	disablePlan := tfsdk.Plan{Schema: schema}
	_ = disablePlan.Set(ctx, &disablePlanModel)
	disableResp := resource.UpdateResponse{State: pendingResp.State}
	r.Update(ctx, resource.UpdateRequest{Plan: disablePlan, State: pendingResp.State}, &disableResp)
	if disableResp.Diagnostics.HasError() {
		t.Fatalf("Update for disable failed: %v", disableResp.Diagnostics)
	}
	var disabledState PaperclipPluginResourceModel
	_ = disableResp.State.Get(ctx, &disabledState)
	if disabledState.Enabled.ValueBool() || disabledState.Status.ValueString() != "disabled" {
		t.Fatalf("expected disabled plugin, got: %v", disabledState)
	}

	// 9c. Update: re-enable plugin
	enablePlanModel := PaperclipPluginResourceModel{
		ID:             types.StringValue("plug-1"),
		PackageName:    types.StringValue("@paperclipai/plugin-test"),
		Version:        types.StringValue("2.0.0"),
		IsLocalPath:    types.BoolValue(true),
		Enabled:        types.BoolValue(true),
		PurgeOnDestroy: types.BoolValue(true),
	}
	enablePlan := tfsdk.Plan{Schema: schema}
	_ = enablePlan.Set(ctx, &enablePlanModel)
	enableResp := resource.UpdateResponse{State: disableResp.State}
	r.Update(ctx, resource.UpdateRequest{Plan: enablePlan, State: disableResp.State}, &enableResp)
	if enableResp.Diagnostics.HasError() {
		t.Fatalf("Update for enable failed: %v", enableResp.Diagnostics)
	}

	// 10. Update error branches: upgrade error, enable error, disable error, fetch error
	upgradeFail = true
	upgradeErrPlanModel := PaperclipPluginResourceModel{
		ID:          types.StringValue("plug-1"),
		PackageName: types.StringValue("@paperclipai/plugin-test"),
		Version:     types.StringValue("3.0.0"),
		Enabled:     types.BoolValue(true),
	}
	upgradeErrPlan := tfsdk.Plan{Schema: schema}
	_ = upgradeErrPlan.Set(ctx, &upgradeErrPlanModel)
	var badUpgradeResp resource.UpdateResponse
	r.Update(ctx, resource.UpdateRequest{Plan: upgradeErrPlan, State: enableResp.State}, &badUpgradeResp)
	if !badUpgradeResp.Diagnostics.HasError() {
		t.Fatal("expected update error when upgrade fails")
	}
	upgradeFail = false

	enableFail = true
	var badEnableResp resource.UpdateResponse
	r.Update(ctx, resource.UpdateRequest{Plan: enablePlan, State: disableResp.State}, &badEnableResp)
	if !badEnableResp.Diagnostics.HasError() {
		t.Fatal("expected update error when enable fails")
	}
	enableFail = false

	disableFail = true
	var badDisableResp resource.UpdateResponse
	r.Update(ctx, resource.UpdateRequest{Plan: disablePlan, State: enableResp.State}, &badDisableResp)
	if !badDisableResp.Diagnostics.HasError() {
		t.Fatal("expected update error when disable fails")
	}
	disableFail = false

	fetchFail = true
	var badFetchResp resource.UpdateResponse
	r.Update(ctx, resource.UpdateRequest{Plan: pendingPlan, State: pendingResp.State}, &badFetchResp)
	if !badFetchResp.Diagnostics.HasError() {
		t.Fatal("expected update error when fetch fails")
	}
	fetchFail = false

	// 11. Delete: purge = true, purge = false, then missing 404, then deleteFail error
	deleteResp := resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: pendingResp.State}, &deleteResp)
	if deleteResp.Diagnostics.HasError() || !lastPurge {
		t.Fatalf("Delete purge=true failed: %v, lastPurge=%v", deleteResp.Diagnostics, lastPurge)
	}

	delNoPurgeResp := resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: stateNulls}, &delNoPurgeResp)
	if delNoPurgeResp.Diagnostics.HasError() || lastPurge {
		t.Fatalf("Delete purge=false failed: %v, lastPurge=%v", delNoPurgeResp.Diagnostics, lastPurge)
	}

	deleteMissingResp := resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: stateMissing}, &deleteMissingResp)
	if deleteMissingResp.Diagnostics.HasError() {
		t.Fatalf("Delete missing failed: %v", deleteMissingResp.Diagnostics)
	}

	deleteFail = true
	deleteErrResp := resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: pendingResp.State}, &deleteErrResp)
	if !deleteErrResp.Diagnostics.HasError() {
		t.Fatal("expected delete error when server fails")
	}
	deleteFail = false

	// 12. ImportState
	importResp := resource.ImportStateResponse{State: tfsdk.State{Schema: schema}}
	_ = importResp.State.Set(ctx, &PaperclipPluginResourceModel{})
	r.ImportState(ctx, resource.ImportStateRequest{ID: "plug-1"}, &importResp)
	if importResp.Diagnostics.HasError() {
		t.Fatalf("ImportState failed: %v", importResp.Diagnostics)
	}
	var importState PaperclipPluginResourceModel
	_ = importResp.State.Get(ctx, &importState)
	if importState.ID.ValueString() != "plug-1" {
		t.Fatalf("expected imported ID plug-1, got: %s", importState.ID.ValueString())
	}
	importRead := resource.ReadResponse{State: importResp.State}
	r.Read(ctx, resource.ReadRequest{State: importResp.State}, &importRead)
	if importRead.Diagnostics.HasError() {
		t.Fatal(importRead.Diagnostics)
	}
	_ = importRead.State.Get(ctx, &importState)
	if !importState.IsLocalPath.ValueBool() || importState.PackageName.ValueString() != "@paperclipai/plugin-test" || importState.Version.ValueString() != "2.0.0" {
		t.Fatalf("import Read did not restore the installed local plugin: %v", importState)
	}
}

func TestPaperclipPluginConfigResourceLifecycle(t *testing.T) {
	ctx := context.Background()

	var (
		serverFail    bool
		invalidSchema bool
		nonAdminFail  bool
		fetchFail     bool
		secretFail    bool
		savedConfig   map[string]interface{}
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if serverFail {
			http.Error(w, `{"error":"server error"}`, http.StatusInternalServerError)
			return
		}

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/secrets":
			if secretFail {
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":"internal secret error"}`))
				return
			}
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{
					"id":        "sec-1",
					"companyId": "comp-1",
					"name":      "api-key",
				},
			})

		case r.Method == http.MethodPost && r.URL.Path == "/api/plugins/plug-1/config":
			if nonAdminFail {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":"Only instance administrators may configure plugins","code":"forbidden_instance_admin"}`))
				return
			}
			if invalidSchema {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(client.PluginConfigValidationError{
					Error: "Configuration does not match the plugin's instanceConfigSchema",
					FieldErrors: []client.PluginConfigFieldError{
						{Field: "/apiKey", Message: "must have required property 'apiKey'"},
					},
				})
				return
			}
			var body client.PaperclipPluginConfigInput
			_ = json.NewDecoder(r.Body).Decode(&body)
			savedConfig = body.ConfigJSON
			_ = json.NewEncoder(w).Encode(client.PaperclipPluginConfig{
				ID:         "cfg-1",
				PluginID:   "plug-1",
				CompanyID:  body.CompanyID,
				ConfigJSON: body.ConfigJSON,
			})

		case r.Method == http.MethodGet && r.URL.Path == "/api/plugins/plug-1/config":
			if fetchFail {
				http.Error(w, `{"error":"read error"}`, http.StatusInternalServerError)
				return
			}
			compID := r.URL.Query().Get("companyId")
			if compID == "comp-missing" {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"error":"not found"}`))
				return
			}
			cfg := savedConfig
			if cfg == nil {
				cfg = map[string]interface{}{"apiKey": map[string]interface{}{"type": "secret_ref", "secretId": "sec-1"}}
			}
			_ = json.NewEncoder(w).Encode(client.PaperclipPluginConfig{
				ID:         "cfg-1",
				PluginID:   "plug-1",
				CompanyID:  compID,
				ConfigJSON: cfg,
			})

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	r := NewPaperclipPluginConfigResource().(*PaperclipPluginConfigResource)
	schema := configureTestResource(t, server.URL, r)

	// Configure error paths
	badConfReq := resource.ConfigureRequest{ProviderData: "invalid"}
	var badConfResp resource.ConfigureResponse
	r.Configure(ctx, badConfReq, &badConfResp)
	if !badConfResp.Diagnostics.HasError() {
		t.Fatal("expected error from Configure with invalid ProviderData")
	}
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: nil}, &badConfResp)

	// 1. ValidateConfig tests
	valModel := PaperclipPluginConfigResourceModel{
		Config: types.StringValue(`{"test": "[REDACTED]"}`),
	}
	valPlan := tfsdk.Plan{Schema: schema}
	_ = valPlan.Set(ctx, &valModel)
	valReq := resource.ValidateConfigRequest{
		Config: tfsdk.Config{
			Schema: schema,
			Raw:    valPlan.Raw,
		},
	}
	var valResp resource.ValidateConfigResponse
	r.ValidateConfig(ctx, valReq, &valResp)
	if !valResp.Diagnostics.HasError() {
		t.Fatal("expected error on redacted sentinel in ValidateConfig")
	}

	badValReq := resource.ValidateConfigRequest{
		Config: tfsdk.Config{
			Schema: schema,
			Raw:    tftypes.NewValue(tftypes.String, "bad"),
		},
	}
	var badValResp resource.ValidateConfigResponse
	r.ValidateConfig(ctx, badValReq, &badValResp)
	if !badValResp.Diagnostics.HasError() {
		t.Fatal("expected error on bad config decode in ValidateConfig")
	}

	// 2. Create validation errors:
	// 2a. Invalid JSON
	badJSONPlan := PaperclipPluginConfigResourceModel{
		CompanyID: types.StringValue("comp-1"),
		PluginID:  types.StringValue("plug-1"),
		Config:    types.StringValue("not json"),
	}
	plan1 := tfsdk.Plan{Schema: schema}
	_ = plan1.Set(ctx, &badJSONPlan)
	var resp1 resource.CreateResponse
	resp1.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan1}, &resp1)
	if !resp1.Diagnostics.HasError() {
		t.Fatal("expected error on invalid JSON in Create")
	}

	// 2b. Missing secret in company (cross-company check)
	missingSecPlan := PaperclipPluginConfigResourceModel{
		CompanyID: types.StringValue("comp-1"),
		PluginID:  types.StringValue("plug-1"),
		Config:    types.StringValue(`{"apiKey":{"type":"secret_ref","secretId":"sec-missing"}}`),
	}
	plan2 := tfsdk.Plan{Schema: schema}
	_ = plan2.Set(ctx, &missingSecPlan)
	var resp2 resource.CreateResponse
	resp2.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan2}, &resp2)
	if !resp2.Diagnostics.HasError() {
		t.Fatal("expected error on missing secret in company in Create")
	}

	// 2c. Secret lookup server error
	secretFail = true
	errSecPlan := PaperclipPluginConfigResourceModel{
		CompanyID: types.StringValue("comp-1"),
		PluginID:  types.StringValue("plug-1"),
		Config:    types.StringValue(`{"apiKey":{"type":"secret_ref","secretId":"sec-err"}}`),
	}
	plan2c := tfsdk.Plan{Schema: schema}
	_ = plan2c.Set(ctx, &errSecPlan)
	var resp2c resource.CreateResponse
	resp2c.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan2c}, &resp2c)
	if !resp2c.Diagnostics.HasError() {
		t.Fatal("expected error on secret lookup failure in Create")
	}
	secretFail = false

	// 2d. Non-admin write error
	nonAdminFail = true
	validPlan := PaperclipPluginConfigResourceModel{
		CompanyID: types.StringValue("comp-1"),
		PluginID:  types.StringValue("plug-1"),
		Config:    types.StringValue(`{"apiKey":{"type":"secret_ref","secret_id":"sec-1"}}`),
	}
	plan3 := tfsdk.Plan{Schema: schema}
	_ = plan3.Set(ctx, &validPlan)
	var resp3 resource.CreateResponse
	resp3.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan3}, &resp3)
	if !resp3.Diagnostics.HasError() {
		t.Fatal("expected error on non-admin config write")
	}
	nonAdminFail = false

	// 2e. Schema validation error with fieldErrors
	invalidSchema = true
	var resp4 resource.CreateResponse
	resp4.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan3}, &resp4)
	if !resp4.Diagnostics.HasError() {
		t.Fatal("expected error on schema validation failure")
	}
	// Verify field errors were surfaced
	foundFieldErr := false
	for _, err := range resp4.Diagnostics.Errors() {
		if err.Summary() == "Plugin Configuration Validation Error" {
			foundFieldErr = true
			break
		}
	}
	if !foundFieldErr {
		t.Fatal("expected field error surfaced in diagnostics")
	}
	invalidSchema = false

	// 3. Create success (secret_ref round-trips without secret value)
	var createSuccessResp resource.CreateResponse
	createSuccessResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan3}, &createSuccessResp)
	if createSuccessResp.Diagnostics.HasError() {
		t.Fatalf("Create failed: %v", createSuccessResp.Diagnostics)
	}

	var stateSuccess PaperclipPluginConfigResourceModel
	_ = createSuccessResp.State.Get(ctx, &stateSuccess)
	if stateSuccess.ID.ValueString() != "comp-1/plug-1" {
		t.Fatalf("unexpected ID: %s", stateSuccess.ID.ValueString())
	}
	if !stateSuccess.Config.Equal(validPlan.Config) {
		t.Fatalf("Create changed the planned config: got %s, planned %s", stateSuccess.Config, validPlan.Config)
	}
	// Verify savedConfig in server normalized secret_id to secretId
	if apiKeyMap, ok := savedConfig["apiKey"].(map[string]interface{}); !ok || apiKeyMap["secretId"] != "sec-1" {
		t.Fatalf("expected normalized secretId in server payload, got: %v", savedConfig)
	}

	// 4. Read success
	readResp := resource.ReadResponse{State: createSuccessResp.State}
	r.Read(ctx, resource.ReadRequest{State: createSuccessResp.State}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("Read failed: %v", readResp.Diagnostics)
	}

	// 5. Read 404 removes resource
	missingStateModel := PaperclipPluginConfigResourceModel{
		CompanyID: types.StringValue("comp-missing"),
		PluginID:  types.StringValue("plug-1"),
		ID:        types.StringValue("comp-missing/plug-1"),
	}
	stateMissing := tfsdk.State{Schema: schema}
	_ = stateMissing.Set(ctx, &missingStateModel)
	readMissingResp := resource.ReadResponse{State: stateMissing}
	r.Read(ctx, resource.ReadRequest{State: stateMissing}, &readMissingResp)
	if readMissingResp.Diagnostics.HasError() || !readMissingResp.State.Raw.IsNull() {
		t.Fatal("expected 404 to remove config from state")
	}

	// 6. Read fetch error
	fetchFail = true
	readErrResp := resource.ReadResponse{State: createSuccessResp.State}
	r.Read(ctx, resource.ReadRequest{State: createSuccessResp.State}, &readErrResp)
	if !readErrResp.Diagnostics.HasError() {
		t.Fatal("expected error on read failure")
	}
	fetchFail = false

	// 7. Update success
	updatePlanModel := PaperclipPluginConfigResourceModel{
		ID:        types.StringValue("comp-1/plug-1"),
		CompanyID: types.StringValue("comp-1"),
		PluginID:  types.StringValue("plug-1"),
		Config:    types.StringValue(`{"apiKey":{"type":"secret_ref","secretId":"sec-1"},"timeout":30}`),
	}
	updatePlan := tfsdk.Plan{Schema: schema}
	_ = updatePlan.Set(ctx, &updatePlanModel)
	updateResp := resource.UpdateResponse{State: createSuccessResp.State}
	r.Update(ctx, resource.UpdateRequest{Plan: updatePlan, State: createSuccessResp.State}, &updateResp)
	if updateResp.Diagnostics.HasError() {
		t.Fatalf("Update failed: %v", updateResp.Diagnostics)
	}

	// 8. Update error paths (invalid JSON, validation failure)
	badUpdatePlan := PaperclipPluginConfigResourceModel{
		ID:        types.StringValue("comp-1/plug-1"),
		CompanyID: types.StringValue("comp-1"),
		PluginID:  types.StringValue("plug-1"),
		Config:    types.StringValue("invalid"),
	}
	badUpdate := tfsdk.Plan{Schema: schema}
	_ = badUpdate.Set(ctx, &badUpdatePlan)
	var badUpdateResp resource.UpdateResponse
	badUpdateResp.State = updateResp.State
	r.Update(ctx, resource.UpdateRequest{Plan: badUpdate, State: updateResp.State}, &badUpdateResp)
	if !badUpdateResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid update config")
	}

	invalidSchema = true
	var badSchemaUpdateResp resource.UpdateResponse
	badSchemaUpdateResp.State = updateResp.State
	r.Update(ctx, resource.UpdateRequest{Plan: updatePlan, State: updateResp.State}, &badSchemaUpdateResp)
	if !badSchemaUpdateResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid schema during update")
	}
	invalidSchema = false

	serverFail = true
	var badServerUpdateResp resource.UpdateResponse
	badServerUpdateResp.State = updateResp.State
	r.Update(ctx, resource.UpdateRequest{Plan: updatePlan, State: updateResp.State}, &badServerUpdateResp)
	if !badServerUpdateResp.Diagnostics.HasError() {
		t.Fatal("expected error on server 500 during update")
	}
	serverFail = false

	secretFail = true
	var badSecretUpdateResp resource.UpdateResponse
	badSecretUpdateResp.State = updateResp.State
	r.Update(ctx, resource.UpdateRequest{Plan: updatePlan, State: updateResp.State}, &badSecretUpdateResp)
	if !badSecretUpdateResp.Diagnostics.HasError() {
		t.Fatal("expected error on secret verification failure during update")
	}
	secretFail = false

	// 9. Delete (relinquishes)
	deleteResp := resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: updateResp.State}, &deleteResp)
	if deleteResp.Diagnostics.HasError() {
		t.Fatalf("Delete failed: %v", deleteResp.Diagnostics)
	}

	// 10. ImportState
	importResp := resource.ImportStateResponse{State: tfsdk.State{Schema: schema}}
	_ = importResp.State.Set(ctx, &PaperclipPluginConfigResourceModel{})
	r.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/plug-1"}, &importResp)
	if importResp.Diagnostics.HasError() {
		t.Fatalf("ImportState failed: %v", importResp.Diagnostics)
	}
	var importedState PaperclipPluginConfigResourceModel
	_ = importResp.State.Get(ctx, &importedState)
	if importedState.CompanyID.ValueString() != "comp-1" || importedState.PluginID.ValueString() != "plug-1" {
		t.Fatalf("unexpected imported state: %v", importedState)
	}
	importRead := resource.ReadResponse{State: importResp.State}
	r.Read(ctx, resource.ReadRequest{State: importResp.State}, &importRead)
	if importRead.Diagnostics.HasError() {
		t.Fatal(importRead.Diagnostics)
	}
	_ = importRead.State.Get(ctx, &importedState)
	if !semanticJSONEqualNormalized(importedState.Config.ValueString(), updatePlanModel.Config.ValueString(), normalizePluginConfigValue) {
		t.Fatalf("import Read changed the configuration: %s", importedState.Config)
	}

	badImportResp := resource.ImportStateResponse{State: tfsdk.State{Schema: schema}}
	_ = badImportResp.State.Set(ctx, &PaperclipPluginConfigResourceModel{})
	r.ImportState(ctx, resource.ImportStateRequest{ID: "invalid-id"}, &badImportResp)
	if !badImportResp.Diagnostics.HasError() {
		t.Fatal("expected error on bad import ID")
	}
}

func TestPluginConfigValidationHelpers(t *testing.T) {
	ctx := context.Background()

	// 1. ValidatePluginConfigJSONPlan branches
	p := path.Root("config")
	if d := ValidatePluginConfigJSONPlan(p, ""); d.HasError() {
		t.Fatal("empty string should not error in plan")
	}
	if d := ValidatePluginConfigJSONPlan(p, "null"); d.HasError() {
		t.Fatal("null string should not error in plan")
	}
	if d := ValidatePluginConfigJSONPlan(p, "not json"); !d.HasError() {
		t.Fatal("invalid json should error in plan")
	}

	// plain binding errors
	d := ValidatePluginConfigJSONPlan(p, `{"p": {"type": "plain"}}`)
	if !d.HasError() {
		t.Fatal("plain with empty value should error")
	}
	d = ValidatePluginConfigJSONPlan(p, `{"p": {"type": "plain", "value": "[redacted]"}}`)
	if !d.HasError() {
		t.Fatal("plain with redacted sentinel should error")
	}
	d = ValidatePluginConfigJSONPlan(p, `{"p": {"type": "plain", "value": "ok", "secretId": "sec-1"}}`)
	if !d.HasError() {
		t.Fatal("plain with secretId should error")
	}
	d = ValidatePluginConfigJSONPlan(p, `{"p": {"type": "plain", "value": "ok", "secret_id": "sec-1"}}`)
	if !d.HasError() {
		t.Fatal("plain with secret_id should error")
	}

	// secret_ref binding errors
	d = ValidatePluginConfigJSONPlan(p, `{"s": {"type": "secret_ref"}}`)
	if !d.HasError() {
		t.Fatal("secret_ref without secretId should error")
	}
	d = ValidatePluginConfigJSONPlan(p, `{"s": {"type": "secret_ref", "secretId": "s", "value": "val"}}`)
	if !d.HasError() {
		t.Fatal("secret_ref with value should error")
	}
	d = ValidatePluginConfigJSONPlan(p, `{"s": {"type": "secret_ref", "secretId": "s", "key": "k"}}`)
	if !d.HasError() {
		t.Fatal("secret_ref with key should error")
	}
	d = ValidatePluginConfigJSONPlan(p, `{"s": {"type": "secret_ref", "secretId": "s", "projectionClass": "bad"}}`)
	if !d.HasError() {
		t.Fatal("secret_ref with bad projectionClass should error")
	}
	d = ValidatePluginConfigJSONPlan(p, `{"s": {"type": "secret_ref", "secretId": "s", "projectionClass": "class_3_static_lease"}}`)
	if !d.HasError() {
		t.Fatal("secret_ref class_3 without allowlist key should error")
	}
	d = ValidatePluginConfigJSONPlan(p, `{"s": {"type": "secret_ref", "secretId": "s", "projectionClass": "class_3_static_lease", "projectionAllowlistKey": "bad.key"}}`)
	if !d.HasError() {
		t.Fatal("secret_ref class_3 with unauthorized allowlist key should error")
	}
	d = ValidatePluginConfigJSONPlan(p, `{"s": {"type": "secret_ref", "secretId": "s", "projectionClass": "class_3_static_lease", "projectionAllowlistKey": "slack.bot_token"}}`)
	if d.HasError() {
		t.Fatalf("valid class_3 secret_ref should pass, got: %v", d)
	}

	// user_secret_ref binding errors
	d = ValidatePluginConfigJSONPlan(p, `{"u": {"type": "user_secret_ref"}}`)
	if !d.HasError() {
		t.Fatal("user_secret_ref without key should error")
	}
	d = ValidatePluginConfigJSONPlan(p, `{"u": {"type": "user_secret_ref", "key": "valid", "value": "val"}}`)
	if !d.HasError() {
		t.Fatal("user_secret_ref with value should error")
	}

	// nested slices and maps
	d = ValidatePluginConfigJSONPlan(p, `{"arr": [{"nested": "[REDACTED]"}]}`)
	if !d.HasError() {
		t.Fatal("nested redacted sentinel should error")
	}

	// 2. ValidatePluginConfigJSONApply branches
	if d := ValidatePluginConfigJSONApply(ctx, nil, "comp-1", "{}"); d.HasError() {
		t.Fatal("nil client should not error")
	}
	if d := ValidatePluginConfigJSONApply(ctx, &client.Client{}, "", "{}"); d.HasError() {
		t.Fatal("empty companyID should not error")
	}
	if d := ValidatePluginConfigJSONApply(ctx, &client.Client{}, "comp-1", ""); d.HasError() {
		t.Fatal("empty json should not error")
	}
	if d := ValidatePluginConfigJSONApply(ctx, &client.Client{}, "comp-1", "null"); d.HasError() {
		t.Fatal("null json should not error")
	}
	if d := ValidatePluginConfigJSONApply(ctx, &client.Client{}, "comp-1", "not json"); d.HasError() {
		t.Fatal("invalid json should not error in apply")
	}

	secServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/companies/comp-1/secrets" {
			_ = json.NewEncoder(w).Encode([]client.PaperclipSecret{
				{ID: "sec-1", Name: "SECRET_ONE"},
			})
			return
		}
		if r.URL.Path == "/api/companies/comp-err/secrets" {
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}
		http.NotFound(w, r)
	}))
	defer secServer.Close()
	secCli, _ := client.NewClient(secServer.URL, "token")

	// Empty secret_id
	d = ValidatePluginConfigJSONApply(ctx, secCli, "comp-1", `{"s": {"type": "secret_ref", "secret_id": ""}}`)
	if d.HasError() {
		t.Fatal("empty secret_id should not error in apply")
	}

	// Secret in array with secretId
	d = ValidatePluginConfigJSONApply(ctx, secCli, "comp-1", `{"nested":{"arr": [{"type": "secret_ref", "secretId": "sec-1"}]}}`)
	if d.HasError() {
		t.Fatalf("valid secretId in array should succeed, got: %v", d)
	}

	// Secret reference verification error (server error)
	d = ValidatePluginConfigJSONApply(ctx, secCli, "comp-err", `{"s": {"type": "secret_ref", "secret_id": "sec-1"}}`)
	if !d.HasError() {
		t.Fatal("expected error when secret lookup fails with server error")
	}

	// 3. NormalizePluginConfigForAPI branches
	emptyNorm := NormalizePluginConfigForAPI("not json")
	if len(emptyNorm) != 0 {
		t.Fatal("expected empty map for invalid json")
	}
	norm := NormalizePluginConfigForAPI(`{
		"s": {
			"type": "secret_ref",
			"secret_id": "sec-1",
			"projection_class": "unclassified",
			"projection_allowlist_key": "key"
		},
		"list": [
			{"item": true}
		]
	}`)
	sMap := norm["s"].(map[string]interface{})
	if sMap["secretId"] != "sec-1" || sMap["projectionClass"] != "unclassified" || sMap["projectionAllowlistKey"] != "key" {
		t.Fatalf("unexpected normalized map: %v", sMap)
	}
	if _, hasSnake := sMap["secret_id"]; hasSnake {
		t.Fatal("expected secret_id to be removed")
	}

	// Preserve existing camelCase keys and traverse scalars
	norm2 := NormalizePluginConfigForAPI(`{
		"s": {
			"type": "secret_ref",
			"secret_id": "sec-1",
			"secretId": "already-id",
			"projection_class": "unclassified",
			"projectionClass": "already-class",
			"projection_allowlist_key": "key",
			"projectionAllowlistKey": "already-key"
		},
		"scalar": 123,
		"arr": ["a", "b"]
	}`)
	sMap2 := norm2["s"].(map[string]interface{})
	if sMap2["secretId"] != "already-id" || sMap2["projectionClass"] != "already-class" || sMap2["projectionAllowlistKey"] != "already-key" {
		t.Fatalf("expected existing camelCase to be preserved, got: %v", sMap2)
	}

	// 4. boolRequiresReplaceModifier coverage
	bm := boolRequiresReplaceModifier{}
	_ = bm.Description(ctx)
	_ = bm.MarkdownDescription(ctx)
	var boolResp planmodifier.BoolResponse
	bm.PlanModifyBool(ctx, planmodifier.BoolRequest{
		State: tfsdk.State{Raw: tftypes.NewValue(tftypes.Bool, nil)},
		Plan:  tfsdk.Plan{Raw: tftypes.NewValue(tftypes.Bool, nil)},
	}, &boolResp)
	bm.PlanModifyBool(ctx, planmodifier.BoolRequest{
		State:      tfsdk.State{Raw: tftypes.NewValue(tftypes.Bool, false)},
		Plan:       tfsdk.Plan{Raw: tftypes.NewValue(tftypes.Bool, true)},
		StateValue: types.BoolValue(false),
		PlanValue:  types.BoolValue(true),
	}, &boolResp)
	if !boolResp.RequiresReplace {
		t.Fatal("expected RequiresReplace true")
	}
	boolResp.RequiresReplace = false
	bm.PlanModifyBool(ctx, planmodifier.BoolRequest{
		State:      tfsdk.State{Raw: tftypes.NewValue(tftypes.Bool, true)},
		Plan:       tfsdk.Plan{Raw: tftypes.NewValue(tftypes.Bool, true)},
		StateValue: types.BoolValue(true),
		PlanValue:  types.BoolValue(true),
	}, &boolResp)
	if boolResp.RequiresReplace {
		t.Fatal("expected RequiresReplace false when values match")
	}

	// 5. stringPointerOrNull coverage
	if !stringPointerOrNull(nil).IsNull() {
		t.Fatal("expected null for nil string pointer")
	}
	emptyStr := ""
	if !stringPointerOrNull(&emptyStr).IsNull() {
		t.Fatal("expected null for empty string pointer")
	}
	nonEmptyStr := "error"
	if stringPointerOrNull(&nonEmptyStr).ValueString() != "error" {
		t.Fatal("expected error string value")
	}
}
