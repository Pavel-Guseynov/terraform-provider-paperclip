package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"unsafe"

	"git.guseynov.org/guseynov/paperclip/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestSemanticJSONHelpersBehaviorAndPlanModification(t *testing.T) {
	ctx := context.Background()

	// 1. semanticJSONEqual
	if !semanticJSONEqual("", "") {
		t.Fatal("expected empty strings to equal")
	}
	if semanticJSONEqual("", "{}") || semanticJSONEqual("{}", "") {
		t.Fatal("expected empty string vs non-empty to not equal")
	}
	if semanticJSONEqual("{invalid", "{}") || semanticJSONEqual("{}", "{invalid") {
		t.Fatal("expected invalid json to not equal")
	}
	if !semanticJSONEqual(`{"a":1,"b":2}`, `{"b":2,"a":1}`) {
		t.Fatal("expected semantic equality")
	}
	if semanticJSONEqual(`{"a":1}`, `{"a":2}`) {
		t.Fatal("expected semantic inequality")
	}

	// 2. isNilInterface
	var nilPtr *int
	var nilSlice []string
	var nilMap map[string]int
	var nilChan chan int
	var nilFunc func()
	var nilUnsafe unsafe.Pointer
	if !isNilInterface(nil) || !isNilInterface(nilPtr) || !isNilInterface(nilSlice) || !isNilInterface(nilMap) || !isNilInterface(nilChan) || !isNilInterface(nilFunc) || !isNilInterface(nilUnsafe) {
		t.Fatal("expected typed and untyped nils to be true")
	}
	val := 123
	if isNilInterface(&val) || isNilInterface([]string{"a"}) || isNilInterface(map[string]int{"a": 1}) || isNilInterface(123) {
		t.Fatal("expected non-nils to be false")
	}

	// 3. refreshSemanticJSON
	if !refreshSemanticJSON(types.StringNull(), nil).IsNull() {
		t.Fatal("expected nil remote to return null")
	}
	if !refreshSemanticJSON(types.StringNull(), map[string]interface{}{}).IsNull() {
		t.Fatal("expected empty map with null current to return null")
	}
	if !refreshSemanticJSON(types.StringNull(), []interface{}{}).IsNull() {
		t.Fatal("expected empty slice with null current to return null")
	}
	if !refreshSemanticJSON(types.StringNull(), [0]int{}).IsNull() {
		t.Fatal("expected empty array with null current to return null")
	}
	badJSON := make(chan int)
	if !refreshSemanticJSON(types.StringValue("{}"), badJSON).IsNull() {
		t.Fatal("expected unmarshal error to return null")
	}
	preserved := types.StringValue(`{ "a": 1 }`)
	if got := refreshSemanticJSON(preserved, map[string]interface{}{"a": float64(1)}); got != preserved {
		t.Fatal("expected semantic equality to preserve current string")
	}
	refreshed := refreshSemanticJSON(preserved, map[string]interface{}{"a": float64(2)})
	if refreshed.ValueString() != `{"a":2}` {
		t.Fatalf("expected refreshed value, got %s", refreshed.ValueString())
	}

	// 4. jsonAttributeValidator
	v := JSONAttributeValidator()
	_ = v.Description(ctx)
	_ = v.MarkdownDescription(ctx)

	var vResp validator.StringResponse
	v.ValidateString(ctx, validator.StringRequest{ConfigValue: types.StringNull()}, &vResp)
	if vResp.Diagnostics.HasError() {
		t.Fatal("expected null config value to pass validation")
	}
	v.ValidateString(ctx, validator.StringRequest{ConfigValue: types.StringUnknown()}, &vResp)
	if vResp.Diagnostics.HasError() {
		t.Fatal("expected unknown config value to pass validation")
	}
	v.ValidateString(ctx, validator.StringRequest{Path: path.Root("test"), ConfigValue: types.StringValue("")}, &vResp)
	if !vResp.Diagnostics.HasError() {
		t.Fatal("expected empty string to fail validation")
	}
	vResp.Diagnostics = nil
	v.ValidateString(ctx, validator.StringRequest{Path: path.Root("test"), ConfigValue: types.StringValue("{invalid")}, &vResp)
	if !vResp.Diagnostics.HasError() {
		t.Fatal("expected invalid json to fail validation")
	}
	vResp.Diagnostics = nil
	v.ValidateString(ctx, validator.StringRequest{Path: path.Root("test"), ConfigValue: types.StringValue(`{"ok":true}`)}, &vResp)
	if vResp.Diagnostics.HasError() {
		t.Fatal("expected valid json to pass validation")
	}

	// 5. semanticJSONPlanModifier
	pm := SemanticJSONPlanModifier()
	_ = pm.Description(ctx)
	_ = pm.MarkdownDescription(ctx)

	var pmResp planmodifier.StringResponse
	pm.PlanModifyString(ctx, planmodifier.StringRequest{ConfigValue: types.StringNull()}, &pmResp)
	pm.PlanModifyString(ctx, planmodifier.StringRequest{ConfigValue: types.StringUnknown()}, &pmResp)
	pm.PlanModifyString(ctx, planmodifier.StringRequest{ConfigValue: types.StringValue("{}"), StateValue: types.StringNull()}, &pmResp)
	pm.PlanModifyString(ctx, planmodifier.StringRequest{ConfigValue: types.StringValue("{}"), StateValue: types.StringUnknown()}, &pmResp)
	pm.PlanModifyString(ctx, planmodifier.StringRequest{ConfigValue: types.StringValue("{}"), StateValue: types.StringValue("{}"), PlanValue: types.StringUnknown()}, &pmResp)
	// semantically unequal
	pm.PlanModifyString(ctx, planmodifier.StringRequest{
		ConfigValue: types.StringValue(`{"a":1}`),
		StateValue:  types.StringValue(`{"a":2}`),
		PlanValue:   types.StringValue(`{"a":1}`),
	}, &pmResp)
	if pmResp.PlanValue.ValueString() == `{"a":2}` {
		t.Fatal("expected unequal plan to remain unchanged")
	}
	// semantically equal
	pmResp.PlanValue = types.StringValue(`{"a":1}`)
	pm.PlanModifyString(ctx, planmodifier.StringRequest{
		ConfigValue: types.StringValue(`{"a":1}`),
		StateValue:  types.StringValue(`{ "a": 1 }`),
		PlanValue:   types.StringValue(`{"a":1}`),
	}, &pmResp)
	if pmResp.PlanValue.ValueString() != `{ "a": 1 }` {
		t.Fatalf("expected plan value to be replaced with state value, got %s", pmResp.PlanValue.ValueString())
	}

	// 6. validateJSONString helper
	if err := validateJSONString(path.Root("test"), `{"valid":true}`); err != nil {
		t.Fatal(err)
	}
	if err := validateJSONString(path.Root("test"), ""); err == nil {
		t.Fatal("expected error on empty json")
	}
	if err := validateJSONString(path.Root("test"), `{bad`); err == nil {
		t.Fatal("expected error on bad json")
	}
}

func TestAppPermissionReconstructToolSelectionBehavior(t *testing.T) {
	catalog := []map[string]interface{}{
		{"id": "entry-1", "toolName": "read_file", "isReadOnly": true},
		{"id": "entry-2", "toolName": "write_file", "isReadOnly": false, "isWrite": true},
		{"id": "entry-3", "toolName": "delete_file", "isReadOnly": false, "isDestructive": true},
		{"missing_id": true},
		{"id": "entry-no-name"},
	}

	// 1. Default ask-first behavior: write and delete require askFirst
	selStr := reconstructToolSelection(catalog, []string{"entry-1", "entry-2", "entry-3"}, []string{"entry-2", "entry-3"})
	if selStr == "" {
		t.Fatal("expected non-empty reconstructed selection")
	}

	// 2. Overrides: read_file in askFirst (override writes), write_file not in askFirst (override auto)
	selStr2 := reconstructToolSelection(catalog, []string{"entry-1", "entry-2", "unknown-entry"}, []string{"entry-1"})
	if selStr2 == "" {
		t.Fatal("expected non-empty reconstructed selection with overrides")
	}
}

func TestAppPermissionReadReconstructsToolSelectionFromProfiles(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.URL.Path == "/api/tool-connections/conn-reconstruct":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":            "conn-reconstruct",
				"companyId":     "comp-1",
				"applicationId": "app-1",
				"name":          "test-conn",
				"status":        "ready",
			})
		case req.URL.Path == "/api/companies/comp-1/tools/profiles":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{
					"profileKey":    "app:conn-reconstruct",
					"defaultAction": "deny",
					"status":        "active",
					"entries": []map[string]interface{}{
						{
							"selectorType":   "catalog_entry",
							"effect":         "include",
							"connectionId":   "conn-reconstruct",
							"catalogEntryId": "entry-1",
						},
					},
					"bindings": []map[string]interface{}{
						{
							"targetType": "agent",
							"targetId":   "agent-1",
						},
					},
				},
			})
		case req.URL.Path == "/api/tool-connections/conn-reconstruct/catalog":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "entry-1", "toolName": "read_file", "isReadOnly": true},
			})
		case req.URL.Path == "/api/companies/comp-1/tools/policies":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cli, err := client.NewClient(server.URL, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	r := NewPaperclipAppPermissionResource().(*PaperclipAppPermissionResource)
	r.client = cli

	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	// State without ToolSelection (simulate state after ImportState)
	st := tfsdk.State{Schema: schemaResp.Schema}
	model := PaperclipAppPermissionResourceModel{
		ID:                      types.StringValue("conn-reconstruct"),
		ConnectionID:            types.StringValue("conn-reconstruct"),
		CompanyID:               types.StringValue("comp-1"),
		AgentIDs:                types.ListNull(types.StringType),
		EnabledCatalogEntryIDs:  types.ListNull(types.StringType),
		AskFirstCatalogEntryIDs: types.ListNull(types.StringType),
		ReviewedCatalogEntryIDs: types.ListNull(types.StringType),
	}
	if diags := st.Set(ctx, &model); diags.HasError() {
		t.Fatal(diags)
	}

	var readResp resource.ReadResponse
	readResp.State = st
	r.Read(ctx, resource.ReadRequest{State: st}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatal(readResp.Diagnostics)
	}

	var readModel PaperclipAppPermissionResourceModel
	if diags := readResp.State.Get(ctx, &readModel); diags.HasError() {
		t.Fatal(diags)
	}
	if readModel.ToolSelection.IsNull() || readModel.ToolSelection.ValueString() == "" {
		t.Fatal("expected ToolSelection to be reconstructed in Read")
	}
}

func TestResourceImportStateParsingAndValidationAcrossResources(t *testing.T) {
	ctx := context.Background()

	// 1. Company
	rComp := NewPaperclipCompanyResource().(resource.ResourceWithImportState)
	var respComp resource.ImportStateResponse
	rComp.ImportState(ctx, resource.ImportStateRequest{ID: ""}, &respComp)
	if !respComp.Diagnostics.HasError() {
		t.Fatal("expected error on empty company import ID")
	}

	// 2. Project
	rProj := NewPaperclipProjectResource().(resource.ResourceWithImportState)
	var respProj resource.ImportStateResponse
	rProj.ImportState(ctx, resource.ImportStateRequest{ID: ""}, &respProj)
	if !respProj.Diagnostics.HasError() {
		t.Fatal("expected error on empty project import ID")
	}
	respProj.Diagnostics = nil
	rProj.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/"}, &respProj)
	if !respProj.Diagnostics.HasError() {
		t.Fatal("expected error on invalid project compound import ID")
	}

	// Project lead
	rLead := NewPaperclipProjectLeadResource().(resource.ResourceWithImportState)
	var respLead resource.ImportStateResponse
	rLead.ImportState(ctx, resource.ImportStateRequest{ID: ""}, &respLead)
	if !respLead.Diagnostics.HasError() {
		t.Fatal("expected error on empty project lead import ID")
	}

	// 3. Workspace
	rWs := NewPaperclipWorkspaceResource().(resource.ResourceWithImportState)
	var respWs resource.ImportStateResponse
	rWs.ImportState(ctx, resource.ImportStateRequest{ID: "bare-id"}, &respWs)
	if !respWs.Diagnostics.HasError() {
		t.Fatal("expected error on bare workspace import ID")
	}
	respWs.Diagnostics = nil
	rWs.ImportState(ctx, resource.ImportStateRequest{ID: "proj-1/"}, &respWs)
	if !respWs.Diagnostics.HasError() {
		t.Fatal("expected error on invalid workspace compound import ID")
	}

	// 4. Local Environment
	rEnv := NewPaperclipLocalEnvironmentResource().(resource.ResourceWithImportState)
	var respEnv resource.ImportStateResponse
	rEnv.ImportState(ctx, resource.ImportStateRequest{ID: ""}, &respEnv)
	if !respEnv.Diagnostics.HasError() {
		t.Fatal("expected error on empty local env import ID")
	}
	respEnv.Diagnostics = nil
	rEnv.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/"}, &respEnv)
	if !respEnv.Diagnostics.HasError() {
		t.Fatal("expected error on invalid local env compound import ID")
	}
	var envSchemaResp resource.SchemaResponse
	rEnv.(resource.Resource).Schema(ctx, resource.SchemaRequest{}, &envSchemaResp)
	respEnv.State = tfsdk.State{Schema: envSchemaResp.Schema}
	_ = respEnv.State.Set(ctx, &PaperclipLocalEnvironmentModel{})
	respEnv.Diagnostics = nil
	rEnv.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/env-1"}, &respEnv)
	if respEnv.Diagnostics.HasError() {
		t.Fatal(respEnv.Diagnostics)
	}
	respEnv.Diagnostics = nil
	rEnv.ImportState(ctx, resource.ImportStateRequest{ID: "env-1"}, &respEnv)
	if respEnv.Diagnostics.HasError() {
		t.Fatal(respEnv.Diagnostics)
	}

	// 5. Skill
	rSkill := NewPaperclipSkillResource().(resource.ResourceWithImportState)
	var respSkill resource.ImportStateResponse
	rSkill.ImportState(ctx, resource.ImportStateRequest{ID: "bare-skill"}, &respSkill)
	if !respSkill.Diagnostics.HasError() {
		t.Fatal("expected error on bare skill import ID")
	}
	respSkill.Diagnostics = nil
	rSkill.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/"}, &respSkill)
	if !respSkill.Diagnostics.HasError() {
		t.Fatal("expected error on invalid skill compound import ID")
	}

	// 6. Secret
	rSec := NewPaperclipSecretResource().(resource.ResourceWithImportState)
	var respSec resource.ImportStateResponse
	rSec.ImportState(ctx, resource.ImportStateRequest{ID: "bare-sec"}, &respSec)
	if !respSec.Diagnostics.HasError() {
		t.Fatal("expected error on bare secret import ID")
	}
	respSec.Diagnostics = nil
	rSec.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/"}, &respSec)
	if !respSec.Diagnostics.HasError() {
		t.Fatal("expected error on invalid secret compound import ID")
	}

	// 7. Agent
	rAgent := NewPaperclipAgentResource().(resource.ResourceWithImportState)
	var respAgent resource.ImportStateResponse
	rAgent.ImportState(ctx, resource.ImportStateRequest{ID: ""}, &respAgent)
	if !respAgent.Diagnostics.HasError() {
		t.Fatal("expected error on empty agent import ID")
	}
	respAgent.Diagnostics = nil
	rAgent.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/"}, &respAgent)
	if !respAgent.Diagnostics.HasError() {
		t.Fatal("expected error on invalid agent compound import ID")
	}

	// 8. App
	rApp := NewPaperclipAppResource().(resource.ResourceWithImportState)
	var respApp resource.ImportStateResponse
	rApp.ImportState(ctx, resource.ImportStateRequest{ID: ""}, &respApp)
	if !respApp.Diagnostics.HasError() {
		t.Fatal("expected error on empty app import ID")
	}
	respApp.Diagnostics = nil
	rApp.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/"}, &respApp)
	if !respApp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid app compound import ID")
	}

	// 9. Connection
	rConn := NewPaperclipConnectionResource().(resource.ResourceWithImportState)
	var respConn resource.ImportStateResponse
	rConn.ImportState(ctx, resource.ImportStateRequest{ID: ""}, &respConn)
	if !respConn.Diagnostics.HasError() {
		t.Fatal("expected error on empty connection import ID")
	}
	respConn.Diagnostics = nil
	rConn.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/"}, &respConn)
	if !respConn.Diagnostics.HasError() {
		t.Fatal("expected error on invalid connection compound import ID")
	}

	// 10. Connection install
	rConnInst := NewPaperclipConnectionInstallResource().(resource.ResourceWithImportState)
	var respConnInst resource.ImportStateResponse
	rConnInst.ImportState(ctx, resource.ImportStateRequest{ID: ""}, &respConnInst)
	if !respConnInst.Diagnostics.HasError() {
		t.Fatal("expected error on empty connection install import ID")
	}
	respConnInst.Diagnostics = nil
	rConnInst.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/"}, &respConnInst)
	if !respConnInst.Diagnostics.HasError() {
		t.Fatal("expected error on invalid connection install compound import ID")
	}

	// 11. Policy
	rPol := NewPaperclipPolicyResource().(resource.ResourceWithImportState)
	var respPol resource.ImportStateResponse
	rPol.ImportState(ctx, resource.ImportStateRequest{ID: "bare-pol"}, &respPol)
	if !respPol.Diagnostics.HasError() {
		t.Fatal("expected error on bare policy import ID")
	}
	respPol.Diagnostics = nil
	rPol.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/"}, &respPol)
	if !respPol.Diagnostics.HasError() {
		t.Fatal("expected error on invalid policy compound import ID")
	}

	// 12. Stdio template
	rTpl := NewPaperclipStdioTemplateResource().(resource.ResourceWithImportState)
	var respTpl resource.ImportStateResponse
	rTpl.ImportState(ctx, resource.ImportStateRequest{ID: "bare-tpl"}, &respTpl)
	if !respTpl.Diagnostics.HasError() {
		t.Fatal("expected error on bare template import ID")
	}
	respTpl.Diagnostics = nil
	rTpl.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/"}, &respTpl)
	if !respTpl.Diagnostics.HasError() {
		t.Fatal("expected error on invalid template compound import ID")
	}
}

func TestResourceValidationAndHandlingOfMalformedJSON(t *testing.T) {
	ctx := context.Background()

	// 1. Agent Create & Update invalid JSON
	rAgent := NewPaperclipAgentResource().(*PaperclipAgentResource)
	var agentSchemaResp resource.SchemaResponse
	rAgent.Schema(ctx, resource.SchemaRequest{}, &agentSchemaResp)

	for _, field := range []string{"runtime_config", "adapter_config", "metadata"} {
		planModel := PaperclipAgentResourceModel{
			CompanyID: types.StringValue("comp-1"),
			Name:      types.StringValue("agent-test"),
		}
		switch field {
		case "runtime_config":
			planModel.RuntimeConfig = types.StringValue("{invalid")
		case "adapter_config":
			planModel.AdapterConfig = types.StringValue("{invalid")
		case "metadata":
			planModel.Metadata = types.StringValue("{invalid")
		}
		plan := tfsdk.Plan{Schema: agentSchemaResp.Schema}
		if diags := plan.Set(ctx, &planModel); diags.HasError() {
			t.Fatal(diags)
		}
		var createResp resource.CreateResponse
		rAgent.Create(ctx, resource.CreateRequest{Plan: plan}, &createResp)
		if !createResp.Diagnostics.HasError() {
			t.Fatalf("expected error on invalid JSON in agent create for %s", field)
		}

		stateModel := PaperclipAgentResourceModel{
			ID:        types.StringValue("agent-1"),
			CompanyID: types.StringValue("comp-1"),
			Name:      types.StringValue("agent-test"),
			Role:      types.StringValue("engineer"),
		}
		state := tfsdk.State{Schema: agentSchemaResp.Schema}
		_ = state.Set(ctx, &stateModel)
		var updateResp resource.UpdateResponse
		rAgent.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &updateResp)
		if !updateResp.Diagnostics.HasError() {
			t.Fatalf("expected error on invalid JSON in agent update for %s", field)
		}
	}

	// Agent update adapter_config clear with GetAgent error
	serverErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer serverErr.Close()
	cliErr, _ := client.NewClient(serverErr.URL, "token")
	rAgent.client = cliErr

	planClear := tfsdk.Plan{Schema: agentSchemaResp.Schema}
	planModelClear := PaperclipAgentResourceModel{
		CompanyID:     types.StringValue("comp-1"),
		Name:          types.StringValue("agent-test"),
		AdapterConfig: types.StringNull(),
	}
	_ = planClear.Set(ctx, &planModelClear)
	stateClear := tfsdk.State{Schema: agentSchemaResp.Schema}
	stateModelClear := PaperclipAgentResourceModel{
		ID:            types.StringValue("agent-1"),
		CompanyID:     types.StringValue("comp-1"),
		Name:          types.StringValue("agent-test"),
		Role:          types.StringValue("engineer"),
		AdapterConfig: types.StringValue(`{"test":true}`),
	}
	_ = stateClear.Set(ctx, &stateModelClear)
	var updClearResp resource.UpdateResponse
	rAgent.Update(ctx, resource.UpdateRequest{Plan: planClear, State: stateClear}, &updClearResp)
	if !updClearResp.Diagnostics.HasError() {
		t.Fatal("expected error on agent adapter_config clear with GetAgent failure")
	}

	// 2. App createAppState and updateAppState invalid metadata JSON
	rApp := &PaperclipAppResource{}
	badAppModel := PaperclipAppResourceModel{
		CompanyID: types.StringValue("comp-1"),
		Name:      types.StringValue("app-1"),
		Type:      types.StringValue("mcp_http"),
		Metadata:  types.StringValue("{invalid"),
	}
	if _, err := rApp.createAppState(ctx, badAppModel); err == nil {
		t.Fatal("expected error on invalid metadata JSON in createAppState")
	}
	if _, err := rApp.updateAppState(ctx, badAppModel, badAppModel); err == nil {
		t.Fatal("expected error on invalid metadata JSON in updateAppState")
	}

	// 3. Policy Create & Update invalid JSON
	rPol := NewPaperclipPolicyResource().(*PaperclipPolicyResource)
	var polSchemaResp resource.SchemaResponse
	rPol.Schema(ctx, resource.SchemaRequest{}, &polSchemaResp)

	for _, field := range []string{"rules", "metadata"} {
		polModel := PaperclipPolicyResourceModel{
			CompanyID: types.StringValue("comp-1"),
			Name:      types.StringValue("pol-test"),
		}
		if field == "rules" {
			polModel.Rules = types.StringValue("{invalid")
		} else {
			polModel.Metadata = types.StringValue("{invalid")
		}
		plan := tfsdk.Plan{Schema: polSchemaResp.Schema}
		_ = plan.Set(ctx, &polModel)
		var cResp resource.CreateResponse
		rPol.Create(ctx, resource.CreateRequest{Plan: plan}, &cResp)
		if !cResp.Diagnostics.HasError() {
			t.Fatalf("expected error on invalid %s JSON in policy create", field)
		}

		stModel := PaperclipPolicyResourceModel{
			ID:        types.StringValue("pol-1"),
			CompanyID: types.StringValue("comp-1"),
		}
		state := tfsdk.State{Schema: polSchemaResp.Schema}
		_ = state.Set(ctx, &stModel)
		var uResp resource.UpdateResponse
		rPol.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &uResp)
		if !uResp.Diagnostics.HasError() {
			t.Fatalf("expected error on invalid %s JSON in policy update", field)
		}
	}
}

func TestConnectionAndInstallResourceLifecycleBranches(t *testing.T) {
	ctx := context.Background()

	// 1. Connection install Delete error & 404
	serverErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer serverErr.Close()
	cliErr, _ := client.NewClient(serverErr.URL, "token")

	server404 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server404.Close()
	cli404, _ := client.NewClient(server404.URL, "token")

	rInst := NewPaperclipConnectionInstallResource().(*PaperclipConnectionInstallResource)
	rInst.client = cliErr
	var instSchemaResp resource.SchemaResponse
	rInst.Schema(ctx, resource.SchemaRequest{}, &instSchemaResp)

	st := tfsdk.State{Schema: instSchemaResp.Schema}
	_ = st.Set(ctx, &PaperclipConnectionInstallResourceModel{
		ID:           types.StringValue("conn-1"),
		ConnectionID: types.StringValue("conn-1"),
		AgentIDs:     types.ListNull(types.StringType),
		TargetType:   types.StringNull(),
		CompanyID:    types.StringNull(),
	})
	var delResp resource.DeleteResponse
	rInst.Delete(ctx, resource.DeleteRequest{State: st}, &delResp)
	if !delResp.Diagnostics.HasError() {
		t.Fatal("expected error on connection install delete failure")
	}

	// Delete 404 (does nothing without error)
	rInst.client = cli404
	var del404Resp resource.DeleteResponse
	rInst.Delete(ctx, resource.DeleteRequest{State: st}, &del404Resp)
	if del404Resp.Diagnostics.HasError() {
		t.Fatal("expected no error on connection install delete 404")
	}

	// 2. Connection install Read 404 and 500
	var readResp resource.ReadResponse
	readResp.State = st
	rInst.Read(ctx, resource.ReadRequest{State: st}, &readResp)
	if !readResp.State.Raw.IsNull() {
		t.Fatal("expected state removed on connection install read 404")
	}

	rInst.client = cliErr
	var readErrResp resource.ReadResponse
	readErrResp.State = st
	rInst.Read(ctx, resource.ReadRequest{State: st}, &readErrResp)
	if !readErrResp.Diagnostics.HasError() {
		t.Fatal("expected error on connection install read 500")
	}

	// 3. Local environment Read 404 and 500
	rLocal := NewPaperclipLocalEnvironmentResource().(*PaperclipLocalEnvironmentResource)
	rLocal.client = cli404
	var envSchemaResp resource.SchemaResponse
	rLocal.Schema(ctx, resource.SchemaRequest{}, &envSchemaResp)

	stLocal := tfsdk.State{Schema: envSchemaResp.Schema}
	_ = stLocal.Set(ctx, &PaperclipLocalEnvironmentModel{
		ID:        types.StringValue("env-1"),
		CompanyID: types.StringValue("comp-1"),
	})
	var readLocalResp resource.ReadResponse
	readLocalResp.State = stLocal
	rLocal.Read(ctx, resource.ReadRequest{State: stLocal}, &readLocalResp)
	if !readLocalResp.State.Raw.IsNull() {
		t.Fatal("expected state removed on local environment read 404")
	}

	rLocal.client = cliErr
	var readLocalErrResp resource.ReadResponse
	readLocalErrResp.State = stLocal
	rLocal.Read(ctx, resource.ReadRequest{State: stLocal}, &readLocalErrResp)
	if !readLocalErrResp.Diagnostics.HasError() {
		t.Fatal("expected error on local environment read 500")
	}

	// Local environment Read with non-empty CompanyID
	serverEnvOk := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":        "env-1",
			"companyId": "comp-1",
			"driver":    "local",
			"mode":      "local_service",
		})
	}))
	defer serverEnvOk.Close()
	cliEnvOk, _ := client.NewClient(serverEnvOk.URL, "token")
	rLocal.client = cliEnvOk
	var readLocalOkResp resource.ReadResponse
	readLocalOkResp.State = stLocal
	rLocal.Read(ctx, resource.ReadRequest{State: stLocal}, &readLocalOkResp)
	if readLocalOkResp.Diagnostics.HasError() {
		t.Fatal(readLocalOkResp.Diagnostics)
	}
	var envModel PaperclipLocalEnvironmentModel
	_ = readLocalOkResp.State.Get(ctx, &envModel)
	if envModel.CompanyID.ValueString() != "comp-1" {
		t.Fatalf("expected company ID comp-1, got %s", envModel.CompanyID.ValueString())
	}
}

func TestResourceLifecycleWithUnknownAndNullAttributes(t *testing.T) {
	ctx := context.Background()

	// 1. Agent Update with plan.AdapterConfig.IsNull() && !state.AdapterConfig.IsNull() and plan.Instructions.IsUnknown()
	serverAgent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.URL.Path == "/api/agents/agent-1" && req.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":            "agent-1",
				"companyId":     "comp-1",
				"name":          "agent-test",
				"role":          "engineer",
				"adapterType":   "antigravity_local",
				"adapterConfig": map[string]interface{}{"model": "gpt-4"},
				"permissions":   map[string]interface{}{"canAssignTasks": true, "canCreateAgents": false},
			})
		case req.URL.Path == "/api/agents/agent-1" && req.Method == http.MethodPatch:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":          "agent-1",
				"companyId":   "comp-1",
				"name":        "agent-test",
				"role":        "engineer",
				"adapterType": "antigravity_local",
			})
		case req.URL.Path == "/api/agents/agent-1/permissions" && req.Method == http.MethodPatch:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "agent-1"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer serverAgent.Close()
	cliAgent, _ := client.NewClient(serverAgent.URL, "token")

	rAgent := NewPaperclipAgentResource().(*PaperclipAgentResource)
	rAgent.client = cliAgent
	var agentSchemaResp resource.SchemaResponse
	rAgent.Schema(ctx, resource.SchemaRequest{}, &agentSchemaResp)

	planAgent := tfsdk.Plan{Schema: agentSchemaResp.Schema}
	_ = planAgent.Set(ctx, &PaperclipAgentResourceModel{
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("agent-test"),
		AdapterConfig:   types.StringNull(),
		Instructions:    types.StringUnknown(),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(false),
	})
	stateAgent := tfsdk.State{Schema: agentSchemaResp.Schema}
	_ = stateAgent.Set(ctx, &PaperclipAgentResourceModel{
		ID:              types.StringValue("agent-1"),
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("agent-test"),
		Role:            types.StringValue("engineer"),
		AdapterType:     types.StringValue("antigravity_local"),
		AdapterConfig:   types.StringValue(`{"model":"gpt-4"}`),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(false),
	})
	updAgentResp := resource.UpdateResponse{State: tfsdk.State{Schema: agentSchemaResp.Schema}}
	rAgent.Update(ctx, resource.UpdateRequest{Plan: planAgent, State: stateAgent}, &updAgentResp)
	if updAgentResp.Diagnostics.HasError() {
		t.Fatal(updAgentResp.Diagnostics)
	}

	// 2. Connection: Create with plan.Config.IsUnknown() and server config = nil
	//    and Update with updated.Config != nil and updated.CredentialRefs != nil
	serverConn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.URL.Path == "/api/companies/comp-1/tools/connections" && req.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":        "conn-1",
				"companyId": "comp-1",
				"name":      "conn-test",
				"appId":     "app-1",
				"config":    nil,
			})
		case req.URL.Path == "/api/tool-connections/conn-1" && req.Method == http.MethodPatch:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":             "conn-1",
				"companyId":      "comp-1",
				"name":           "conn-test",
				"config":         map[string]interface{}{"url": "https://test"},
				"credentialRefs": []map[string]interface{}{{"secretId": "sec-1"}},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer serverConn.Close()
	cliConn, _ := client.NewClient(serverConn.URL, "token")

	rConn := NewPaperclipConnectionResource().(*PaperclipConnectionResource)
	rConn.client = cliConn
	var connSchemaResp resource.SchemaResponse
	rConn.Schema(ctx, resource.SchemaRequest{}, &connSchemaResp)

	planConn := tfsdk.Plan{Schema: connSchemaResp.Schema}
	_ = planConn.Set(ctx, &PaperclipConnectionResourceModel{
		CompanyID:        types.StringValue("comp-1"),
		Name:             types.StringValue("conn-test"),
		Config:           types.StringUnknown(),
		AppID:            types.StringValue("app-1"),
		CredentialRefs:   types.StringUnknown(),
		CredentialPolicy: types.StringNull(),
	})
	createConnResp := resource.CreateResponse{State: tfsdk.State{Schema: connSchemaResp.Schema}}
	rConn.Create(ctx, resource.CreateRequest{Plan: planConn}, &createConnResp)
	if createConnResp.Diagnostics.HasError() {
		t.Fatal(createConnResp.Diagnostics)
	}

	stateConn := tfsdk.State{Schema: connSchemaResp.Schema}
	_ = stateConn.Set(ctx, &PaperclipConnectionResourceModel{
		ID:        types.StringValue("conn-1"),
		CompanyID: types.StringValue("comp-1"),
		Name:      types.StringValue("conn-test"),
	})
	planConnUpdate := tfsdk.Plan{Schema: connSchemaResp.Schema}
	_ = planConnUpdate.Set(ctx, &PaperclipConnectionResourceModel{
		ID:               types.StringValue("conn-1"),
		CompanyID:        types.StringValue("comp-1"),
		Name:             types.StringValue("conn-test"),
		Config:           types.StringNull(),
		CredentialRefs:   types.StringNull(),
		CredentialPolicy: types.StringNull(),
	})
	updConnResp := resource.UpdateResponse{State: tfsdk.State{Schema: connSchemaResp.Schema}}
	rConn.Update(ctx, resource.UpdateRequest{Plan: planConnUpdate, State: stateConn}, &updConnResp)
	if updConnResp.Diagnostics.HasError() {
		t.Fatal(updConnResp.Diagnostics)
	}

	// 3. Connection install: Create and Update with unknown AgentIDs, TargetType, CompanyID
	//    and Read with empty agent list and null prior state
	serverInst := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.URL.Path == "/api/tool-connections/conn-1/installs" && (req.Method == http.MethodPut || req.Method == http.MethodGet):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"installs": []map[string]interface{}{},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer serverInst.Close()
	cliInst, _ := client.NewClient(serverInst.URL, "token")
	rInst := NewPaperclipConnectionInstallResource().(*PaperclipConnectionInstallResource)
	var instSchemaResp resource.SchemaResponse
	rInst.Schema(ctx, resource.SchemaRequest{}, &instSchemaResp)
	rInst.client = cliInst

	planInstUnk := tfsdk.Plan{Schema: instSchemaResp.Schema}
	_ = planInstUnk.Set(ctx, &PaperclipConnectionInstallResourceModel{
		ConnectionID: types.StringValue("conn-1"),
		AgentIDs:     types.ListUnknown(types.StringType),
		TargetType:   types.StringUnknown(),
		CompanyID:    types.StringUnknown(),
	})
	createInstResp := resource.CreateResponse{State: tfsdk.State{Schema: instSchemaResp.Schema}}
	rInst.Create(ctx, resource.CreateRequest{Plan: planInstUnk}, &createInstResp)
	if createInstResp.Diagnostics.HasError() {
		t.Fatal(createInstResp.Diagnostics)
	}

	stateInst := tfsdk.State{Schema: instSchemaResp.Schema}
	_ = stateInst.Set(ctx, &PaperclipConnectionInstallResourceModel{
		ID:           types.StringValue("conn-1"),
		ConnectionID: types.StringValue("conn-1"),
		AgentIDs:     types.ListNull(types.StringType),
		TargetType:   types.StringValue("agent"),
		CompanyID:    types.StringNull(),
	})
	updInstResp := resource.UpdateResponse{State: tfsdk.State{Schema: instSchemaResp.Schema}}
	rInst.Update(ctx, resource.UpdateRequest{Plan: planInstUnk, State: stateInst}, &updInstResp)
	if updInstResp.Diagnostics.HasError() {
		t.Fatal(updInstResp.Diagnostics)
	}

	readInstEmptyResp := resource.ReadResponse{State: stateInst}
	rInst.Read(ctx, resource.ReadRequest{State: stateInst}, &readInstEmptyResp)
	if readInstEmptyResp.Diagnostics.HasError() {
		t.Fatal(readInstEmptyResp.Diagnostics)
	}

	// 4. Project lead ImportState branches
	rLead := NewPaperclipProjectLeadResource()
	var leadSchemaResp resource.SchemaResponse
	rLead.Schema(ctx, resource.SchemaRequest{}, &leadSchemaResp)
	rLeadImport := rLead.(resource.ResourceWithImportState)

	impResp1 := resource.ImportStateResponse{State: tfsdk.State{Schema: leadSchemaResp.Schema}}
	_ = impResp1.State.Set(ctx, &PaperclipProjectLeadModel{})
	rLeadImport.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/proj-1"}, &impResp1)
	if impResp1.Diagnostics.HasError() {
		t.Fatal(impResp1.Diagnostics)
	}
	var leadModel1 PaperclipProjectLeadModel
	_ = impResp1.State.Get(ctx, &leadModel1)
	if leadModel1.ProjectID.ValueString() != "proj-1" {
		t.Fatalf("expected proj-1, got %s", leadModel1.ProjectID.ValueString())
	}

	impResp2 := resource.ImportStateResponse{State: tfsdk.State{Schema: leadSchemaResp.Schema}}
	_ = impResp2.State.Set(ctx, &PaperclipProjectLeadModel{})
	rLeadImport.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/"}, &impResp2)
	// comp-1/ keeps id as comp-1/ which is non-empty
	if impResp2.Diagnostics.HasError() {
		t.Fatal(impResp2.Diagnostics)
	}

	impResp3 := resource.ImportStateResponse{State: tfsdk.State{Schema: leadSchemaResp.Schema}}
	rLeadImport.ImportState(ctx, resource.ImportStateRequest{ID: ""}, &impResp3)
	if !impResp3.Diagnostics.HasError() {
		t.Fatal("expected error on empty import ID")
	}

	// 5. Secret: Create, Read, Update with Rotation = types.StringUnknown()
	serverSec := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.URL.Path == "/api/companies/comp-1/secrets" && req.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":        "sec-1",
				"companyId": "comp-1",
				"name":      "sec-test",
			})
		case req.URL.Path == "/api/companies/comp-1/secrets/sec-1" && req.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":        "sec-1",
				"companyId": "comp-1",
				"name":      "sec-test",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer serverSec.Close()
	cliSec, _ := client.NewClient(serverSec.URL, "token")

	rSec := NewPaperclipSecretResource().(*PaperclipSecretResource)
	rSec.client = cliSec
	var secSchemaResp resource.SchemaResponse
	rSec.Schema(ctx, resource.SchemaRequest{}, &secSchemaResp)

	planSec := tfsdk.Plan{Schema: secSchemaResp.Schema}
	_ = planSec.Set(ctx, &PaperclipSecretResourceModel{
		CompanyID: types.StringValue("comp-1"),
		Name:      types.StringValue("sec-test"),
		Value:     types.StringValue("secret-val"),
		Rotation:  types.StringUnknown(),
	})
	configSec := tfsdk.Config{Schema: secSchemaResp.Schema, Raw: planSec.Raw}
	createSecResp := resource.CreateResponse{State: tfsdk.State{Schema: secSchemaResp.Schema}}
	rSec.Create(ctx, resource.CreateRequest{Plan: planSec, Config: configSec}, &createSecResp)
	if createSecResp.Diagnostics.HasError() {
		t.Fatal(createSecResp.Diagnostics)
	}

	stateSec := tfsdk.State{Schema: secSchemaResp.Schema}
	_ = stateSec.Set(ctx, &PaperclipSecretResourceModel{
		ID:        types.StringValue("sec-1"),
		CompanyID: types.StringValue("comp-1"),
		Name:      types.StringValue("sec-test"),
		Rotation:  types.StringUnknown(),
	})
	readSecResp := resource.ReadResponse{State: stateSec}
	rSec.Read(ctx, resource.ReadRequest{State: stateSec}, &readSecResp)
	if readSecResp.Diagnostics.HasError() {
		t.Fatal(readSecResp.Diagnostics)
	}

	stateSecKnown := tfsdk.State{Schema: secSchemaResp.Schema}
	_ = stateSecKnown.Set(ctx, &PaperclipSecretResourceModel{
		ID:        types.StringValue("sec-1"),
		CompanyID: types.StringValue("comp-1"),
		Name:      types.StringValue("sec-test"),
		Rotation:  types.StringValue("v1"),
	})
	planSecUpd := tfsdk.Plan{Schema: secSchemaResp.Schema}
	_ = planSecUpd.Set(ctx, &PaperclipSecretResourceModel{
		ID:        types.StringValue("sec-1"),
		CompanyID: types.StringValue("comp-1"),
		Name:      types.StringValue("sec-test"),
		Rotation:  types.StringUnknown(),
	})
	configSecUpd := tfsdk.Config{Schema: secSchemaResp.Schema, Raw: planSecUpd.Raw}
	updSecResp := resource.UpdateResponse{State: tfsdk.State{Schema: secSchemaResp.Schema}}
	rSec.Update(ctx, resource.UpdateRequest{Plan: planSecUpd, State: stateSecKnown, Config: configSecUpd}, &updSecResp)
	if updSecResp.Diagnostics.HasError() {
		t.Fatal(updSecResp.Diagnostics)
	}
}
