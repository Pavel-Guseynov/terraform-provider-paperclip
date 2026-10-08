package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"git.guseynov.org/guseynov/paperclip/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestStringOneOfValidator(t *testing.T) {
	ctx := context.Background()
	v := stringOneOfValidator{allowed: []string{"a", "b"}}
	if v.Description(ctx) != "value must be one of: a, b" {
		t.Fatalf("unexpected description: %s", v.Description(ctx))
	}
	if v.MarkdownDescription(ctx) != "value must be one of: a, b" {
		t.Fatalf("unexpected markdown description: %s", v.MarkdownDescription(ctx))
	}

	// Null string
	var resp1 validator.StringResponse
	v.ValidateString(ctx, validator.StringRequest{ConfigValue: types.StringNull()}, &resp1)
	if resp1.Diagnostics.HasError() {
		t.Fatal("unexpected error on null")
	}

	// Unknown string
	var resp2 validator.StringResponse
	v.ValidateString(ctx, validator.StringRequest{ConfigValue: types.StringUnknown()}, &resp2)
	if resp2.Diagnostics.HasError() {
		t.Fatal("unexpected error on unknown")
	}

	// Valid string
	var resp3 validator.StringResponse
	v.ValidateString(ctx, validator.StringRequest{ConfigValue: types.StringValue("a")}, &resp3)
	if resp3.Diagnostics.HasError() {
		t.Fatal("unexpected error on valid")
	}

	// Invalid string
	var resp4 validator.StringResponse
	v.ValidateString(ctx, validator.StringRequest{ConfigValue: types.StringValue("c"), Path: path.Root("test")}, &resp4)
	if !resp4.Diagnostics.HasError() {
		t.Fatal("expected error on invalid")
	}
}

func TestAgentUpgradeStateV0ToV1(t *testing.T) {
	ctx := context.Background()
	r := NewPaperclipAgentResource().(*PaperclipAgentResource)
	upgraders := r.UpgradeState(ctx)
	u, ok := upgraders[0]
	if !ok {
		t.Fatal("missing v0 state upgrader")
	}

	priorState := tfsdk.State{Schema: *u.PriorSchema}
	_ = priorState.Set(ctx, &struct {
		ID              types.String `tfsdk:"id"`
		CompanyID       types.String `tfsdk:"company_id"`
		Name            types.String `tfsdk:"name"`
		Role            types.String `tfsdk:"role"`
		Title           types.String `tfsdk:"title"`
		AdapterType     types.String `tfsdk:"adapter_type"`
		Status          types.String `tfsdk:"status"`
		RuntimeConfig   types.String `tfsdk:"runtime_config"`
		AdapterConfig   types.String `tfsdk:"adapter_config"`
		Metadata        types.String `tfsdk:"metadata"`
		Instructions    types.String `tfsdk:"instructions"`
		CanAssignTasks  types.Bool   `tfsdk:"can_assign_tasks"`
		CanCreateAgents types.Bool   `tfsdk:"can_create_agents"`
	}{
		ID:              types.StringValue("ag-v0"),
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("agent-0"),
		Role:            types.StringValue("engineer"),
		Title:           types.StringValue("Eng"),
		AdapterType:     types.StringValue("antigravity"),
		Status:          types.StringValue("paused"),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(false),
	})

	var sResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sResp)

	var upgResp resource.UpgradeStateResponse
	upgResp.State = tfsdk.State{Schema: sResp.Schema}
	u.StateUpgrader(ctx, resource.UpgradeStateRequest{State: &priorState}, &upgResp)
	if upgResp.Diagnostics.HasError() {
		t.Fatalf("unexpected upgrader error: %v", upgResp.Diagnostics)
	}

	var upgraded PaperclipAgentResourceModel
	_ = upgResp.State.Get(ctx, &upgraded)
	if upgraded.ID.ValueString() != "ag-v0" ||
		!upgraded.Icon.IsNull() ||
		!upgraded.ReportsTo.IsNull() ||
		!upgraded.Capabilities.IsNull() ||
		!upgraded.DefaultEnvironmentID.IsNull() ||
		!upgraded.CanCreateSkills.ValueBool() ||
		!upgraded.Paused.ValueBool() ||
		upgraded.Retirement.ValueString() != "delete" {
		t.Fatalf("unexpected upgraded state: %+v", upgraded)
	}

	// Error path: state cannot decode to prior struct
	badState := tfsdk.State{
		Schema: *u.PriorSchema,
		Raw:    tftypes.NewValue(tftypes.String, "invalid"),
	}
	var badResp resource.UpgradeStateResponse
	badResp.State = tfsdk.State{Schema: sResp.Schema}
	u.StateUpgrader(ctx, resource.UpgradeStateRequest{State: &badState}, &badResp)
	if !badResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid state in UpgradeState")
	}
}

func TestValidateAuthorizationPolicy(t *testing.T) {
	if _, err := validateAuthorizationPolicy("invalid-json"); err == nil {
		t.Fatal("expected error on invalid json")
	}
	if _, err := validateAuthorizationPolicy(`{"unknownKey": "val"}`); err == nil {
		t.Fatal("expected error on unknown key")
	}
	if p, err := validateAuthorizationPolicy(""); err != nil || p != nil {
		t.Fatalf("unexpected result for empty policy: %v, %v", p, err)
	}
	if p, err := validateAuthorizationPolicy("null"); err != nil || p != nil {
		t.Fatalf("unexpected result for null policy: %v, %v", p, err)
	}
	valid := `{"agentVisibility": "public", "assignmentPolicy": {}, "protectedAgent": true, "managedBy": "lead"}`
	if _, err := validateAuthorizationPolicy(valid); err != nil {
		t.Fatalf("unexpected error on valid policy: %v", err)
	}
}

func TestValidateReportsToCycle(t *testing.T) {
	ctx := context.Background()
	agents := map[string]*client.PaperclipAgent{
		"mgr-1": {ID: "mgr-1", CompanyID: "comp-1", ReportsTo: "mgr-2"},
		"mgr-2": {ID: "mgr-2", CompanyID: "comp-1", ReportsTo: ""},
		"mgr-cycle": {ID: "mgr-cycle", CompanyID: "comp-1", ReportsTo: "ag-cycle"},
		"mgr-loop-a": {ID: "mgr-loop-a", CompanyID: "comp-1", ReportsTo: "mgr-loop-b"},
		"mgr-loop-b": {ID: "mgr-loop-b", CompanyID: "comp-1", ReportsTo: "mgr-loop-a"},
		"mgr-other-comp": {ID: "mgr-other-comp", CompanyID: "comp-2", ReportsTo: ""},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		for id, ag := range agents {
			if r.URL.Path == "/api/agents/"+id && r.Method == http.MethodGet {
				_ = json.NewEncoder(w).Encode(ag)
				return
			}
		}
		if r.URL.Path == "/api/agents/mgr-500" {
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	cli, _ := client.NewClient(server.URL, "token")
	r := &PaperclipAgentResource{client: cli}

	// 1. Empty reports_to -> valid
	if err := r.validateReportsToCycle(ctx, "ag-1", "comp-1", ""); err != nil {
		t.Fatal(err)
	}

	// 2. Self reporting
	if err := r.validateReportsToCycle(ctx, "ag-1", "comp-1", "ag-1"); err == nil || err.Error() != "agent cannot report to itself" {
		t.Fatalf("expected self-reporting error, got: %v", err)
	}

	// 3. Valid chain
	if err := r.validateReportsToCycle(ctx, "ag-1", "comp-1", "mgr-1"); err != nil {
		t.Fatalf("unexpected error for valid chain: %v", err)
	}

	// 4. Cycle back to agent
	if err := r.validateReportsToCycle(ctx, "ag-cycle", "comp-1", "mgr-cycle"); err == nil || err.Error() != "reporting relationship would create cycle: ag-cycle" {
		t.Fatalf("expected cycle error back to agent, got: %v", err)
	}

	// 5. Cycle between managers
	if err := r.validateReportsToCycle(ctx, "ag-x", "comp-1", "mgr-loop-a"); err == nil || err.Error() != "reporting relationship cycle detected: mgr-loop-a" {
		t.Fatalf("expected manager cycle error, got: %v", err)
	}

	// 6. Manager not found
	if err := r.validateReportsToCycle(ctx, "ag-1", "comp-1", "mgr-missing"); err == nil || err.Error() != "manager agent mgr-missing not found" {
		t.Fatalf("expected manager not found error, got: %v", err)
	}

	// 7. Manager 500 error
	if err := r.validateReportsToCycle(ctx, "ag-1", "comp-1", "mgr-500"); err == nil {
		t.Fatal("expected error on 500")
	}

	// 8. Cross-company manager
	if err := r.validateReportsToCycle(ctx, "ag-1", "comp-1", "mgr-other-comp"); err == nil {
		t.Fatal("expected cross company error")
	}
}

func TestAuthPolicyEqual(t *testing.T) {
	if !authPolicyEqual(nil, nil) {
		t.Fatal("expected true for both nil")
	}
	if !authPolicyEqual(map[string]interface{}{}, nil) {
		t.Fatal("expected true for empty and nil")
	}
	if authPolicyEqual(map[string]interface{}{"a": 1}, map[string]interface{}{}) {
		t.Fatal("expected false for diff length")
	}
	if !authPolicyEqual(map[string]interface{}{"a": 1}, map[string]interface{}{"a": 1}) {
		t.Fatal("expected true for equal maps")
	}
	if authPolicyEqual(map[string]interface{}{"a": 1}, map[string]interface{}{"a": 2}) {
		t.Fatal("expected false for unequal values")
	}
}

func TestAgentModifyPlan(t *testing.T) {
	ctx := context.Background()
	r := NewPaperclipAgentResource().(*PaperclipAgentResource)
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	// 1. Destroy plan (Plan is null)
	var modResp1 resource.ModifyPlanResponse
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{
		Plan: tfsdk.Plan{Schema: schemaResp.Schema},
	}, &modResp1)
	if modResp1.Diagnostics.HasError() {
		t.Fatal(modResp1.Diagnostics)
	}

	// 2. Create plan default values
	plan2 := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = plan2.Set(ctx, &PaperclipAgentResourceModel{
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("ag"),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(false),
	})
	var modResp2 resource.ModifyPlanResponse
	modResp2.Plan = plan2
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{
		Plan:  plan2,
		State: tfsdk.State{Schema: schemaResp.Schema},
	}, &modResp2)
	if modResp2.Diagnostics.HasError() {
		t.Fatal(modResp2.Diagnostics)
	}
	var resulting2 PaperclipAgentResourceModel
	_ = modResp2.Plan.Get(ctx, &resulting2)
	if !resulting2.CanCreateSkills.ValueBool() || resulting2.Retirement.ValueString() != "delete" || resulting2.Paused.ValueBool() {
		t.Fatalf("expected defaults, got: %+v", resulting2)
	}

	// 3. Invalid auth policy in plan
	plan3 := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = plan3.Set(ctx, &PaperclipAgentResourceModel{
		CompanyID:           types.StringValue("comp-1"),
		Name:                types.StringValue("ag"),
		CanAssignTasks:      types.BoolValue(true),
		CanCreateAgents:     types.BoolValue(false),
		AuthorizationPolicy: types.StringValue(`{"badKey": true}`),
	})
	var modResp3 resource.ModifyPlanResponse
	modResp3.Plan = plan3
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{
		Plan: plan3,
	}, &modResp3)
	if !modResp3.Diagnostics.HasError() {
		t.Fatal("expected diagnostic error on bad auth policy in ModifyPlan")
	}

	// 4. ReportsTo cycle in ModifyPlan
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/agents/mgr-1" {
			_ = json.NewEncoder(w).Encode(client.PaperclipAgent{
				ID:        "mgr-1",
				CompanyID: "comp-1",
				ReportsTo: "ag-state",
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	cli, _ := client.NewClient(server.URL, "token")
	r.client = cli

	plan4 := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = plan4.Set(ctx, &PaperclipAgentResourceModel{
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("ag"),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(false),
		ReportsTo:       types.StringValue("mgr-1"),
	})
	state4 := tfsdk.State{Schema: schemaResp.Schema}
	_ = state4.Set(ctx, &PaperclipAgentResourceModel{
		ID:        types.StringValue("ag-state"),
		CompanyID: types.StringValue("comp-1"),
	})
	var modResp4 resource.ModifyPlanResponse
	modResp4.Plan = plan4
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{
		Plan:  plan4,
		State: state4,
	}, &modResp4)
	if !modResp4.Diagnostics.HasError() {
		t.Fatal("expected cycle diagnostic error in ModifyPlan")
	}

	// 5. Clearing trust_preset in ModifyPlan
	plan5 := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = plan5.Set(ctx, &PaperclipAgentResourceModel{
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("ag"),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(false),
		TrustPreset:     types.StringNull(),
	})
	state5 := tfsdk.State{Schema: schemaResp.Schema}
	_ = state5.Set(ctx, &PaperclipAgentResourceModel{
		ID:          types.StringValue("ag-state"),
		CompanyID:   types.StringValue("comp-1"),
		TrustPreset: types.StringValue("standard"),
	})
	var modResp5 resource.ModifyPlanResponse
	modResp5.Plan = plan5
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{
		Plan:  plan5,
		State: state5,
	}, &modResp5)
	if !modResp5.Diagnostics.HasError() {
		t.Fatal("expected diagnostic error when clearing trust_preset in ModifyPlan")
	}

	// 6. Clearing authorization_policy in ModifyPlan
	plan6 := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = plan6.Set(ctx, &PaperclipAgentResourceModel{
		CompanyID:           types.StringValue("comp-1"),
		Name:                types.StringValue("ag"),
		CanAssignTasks:      types.BoolValue(true),
		CanCreateAgents:     types.BoolValue(false),
		AuthorizationPolicy: types.StringNull(),
	})
	state6 := tfsdk.State{Schema: schemaResp.Schema}
	_ = state6.Set(ctx, &PaperclipAgentResourceModel{
		ID:                  types.StringValue("ag-state"),
		CompanyID:           types.StringValue("comp-1"),
		AuthorizationPolicy: types.StringValue(`{"protectedAgent":true}`),
	})
	var modResp6 resource.ModifyPlanResponse
	modResp6.Plan = plan6
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{
		Plan:  plan6,
		State: state6,
	}, &modResp6)
	if !modResp6.Diagnostics.HasError() {
		t.Fatal("expected diagnostic error when clearing authorization_policy in ModifyPlan")
	}
}

func TestAgentCreatePendingApprovalAndLifecycle(t *testing.T) {
	ctx := context.Background()
	pausedCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/companies/comp-1" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(client.PaperclipCompany{ID: "comp-1", RequireBoardApprovalForNewAgents: true})
		case r.URL.Path == "/api/companies/comp-1/agent-hires" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(client.PaperclipAgentHireResponse{
				Agent: client.PaperclipAgent{
					ID:        "agent-pending",
					CompanyID: "comp-1",
					Name:      "pending-ag",
					Status:    "pending_approval",
				},
				Approval: &client.PaperclipApproval{ID: "appr-hire-1"},
			})
		case r.URL.Path == "/api/companies/comp-2" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(client.PaperclipCompany{ID: "comp-2", RequireBoardApprovalForNewAgents: true})
		case r.URL.Path == "/api/companies/comp-2/agent-hires" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(client.PaperclipAgentHireResponse{
				Agent: client.PaperclipAgent{
					ID:        "agent-pending-2",
					CompanyID: "comp-2",
					Name:      "pending-ag-2",
					Status:    "pending_approval",
				},
				Approval: nil, // test with nil Approval
			})
		case r.URL.Path == "/api/companies/comp-3" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(client.PaperclipCompany{ID: "comp-3"})
		case r.URL.Path == "/api/companies/comp-3/agents" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(client.PaperclipAgent{
				ID:          "agent-pause-create",
				CompanyID:   "comp-3",
				Name:        "pause-ag",
				Status:      "idle",
				Permissions: &client.PaperclipAgentPermissions{CanAssignTasks: true, CanCreateAgents: false, CanCreateSkills: true},
			})
		case r.URL.Path == "/api/agents/agent-pause-create/pause" && r.Method == http.MethodPost:
			pausedCalled = true
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/api/agents/agent-pause-create" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(client.PaperclipAgent{
				ID:          "agent-pause-create",
				CompanyID:   "comp-3",
				Name:        "pause-ag",
				Status:      "paused",
				Permissions: &client.PaperclipAgentPermissions{CanAssignTasks: true, CanCreateAgents: false, CanCreateSkills: true},
			})
		case r.URL.Path == "/api/companies/comp-pause-err" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(client.PaperclipCompany{ID: "comp-pause-err"})
		case r.URL.Path == "/api/companies/comp-pause-err/agents" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(client.PaperclipAgent{
				ID:          "agent-pause-err",
				CompanyID:   "comp-pause-err",
				Name:        "pause-err-ag",
				Status:      "idle",
				Permissions: &client.PaperclipAgentPermissions{CanAssignTasks: true, CanCreateAgents: false, CanCreateSkills: true},
			})
		case r.URL.Path == "/api/agents/agent-pause-err/pause" && r.Method == http.MethodPost:
			http.Error(w, `{"error":"pause failed"}`, http.StatusInternalServerError)
		case r.URL.Path == "/api/agents/agent-pause-err" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(client.PaperclipAgent{
				ID:          "agent-pause-err",
				CompanyID:   "comp-pause-err",
				Name:        "pause-err-ag",
				Status:      "idle",
				Permissions: &client.PaperclipAgentPermissions{CanAssignTasks: true, CanCreateAgents: false, CanCreateSkills: true},
			})
		case r.URL.Path == "/api/companies/comp-trust" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(client.PaperclipCompany{ID: "comp-trust"})
		case r.URL.Path == "/api/companies/comp-trust/agents" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(client.PaperclipAgent{
				ID:          "agent-trust",
				CompanyID:   "comp-trust",
				Name:        "trust-ag",
				Status:      "idle",
				Permissions: &client.PaperclipAgentPermissions{CanAssignTasks: true, CanCreateAgents: false, CanCreateSkills: true, TrustPreset: "trusted"},
			})
		case r.URL.Path == "/api/agents/agent-trust" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(client.PaperclipAgent{
				ID:          "agent-trust",
				CompanyID:   "comp-trust",
				Name:        "trust-ag",
				Status:      "idle",
				Permissions: &client.PaperclipAgentPermissions{CanAssignTasks: true, CanCreateAgents: false, CanCreateSkills: true, TrustPreset: "trusted"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cli, _ := client.NewClient(server.URL, "token")
	r := NewPaperclipAgentResource().(*PaperclipAgentResource)
	r.client = cli
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	// Case 1: Pending approval with HireApproval
	plan1 := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = plan1.Set(ctx, &PaperclipAgentResourceModel{
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("pending-ag"),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(false),
		CanCreateSkills: types.BoolValue(true),
		Paused:          types.BoolValue(false),
		Retirement:      types.StringValue("delete"),
	})
	var createResp1 resource.CreateResponse
	createResp1.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan1}, &createResp1)
	if createResp1.Diagnostics.HasError() {
		t.Fatal(createResp1.Diagnostics)
	}
	hasWarning := false
	for _, d := range createResp1.Diagnostics {
		if d.Summary() == "Agent Pending Approval" {
			hasWarning = true
		}
	}
	if !hasWarning {
		t.Fatal("expected warning for pending approval")
	}

	// Case 2: Pending approval with nil HireApproval
	plan2 := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = plan2.Set(ctx, &PaperclipAgentResourceModel{
		CompanyID:       types.StringValue("comp-2"),
		Name:            types.StringValue("pending-ag-2"),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(false),
		CanCreateSkills: types.BoolValue(true),
		Paused:          types.BoolValue(false),
		Retirement:      types.StringValue("delete"),
	})
	var createResp2 resource.CreateResponse
	createResp2.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan2}, &createResp2)
	if createResp2.Diagnostics.HasError() {
		t.Fatal(createResp2.Diagnostics)
	}

	// Case 3: Paused = true on Create calls PauseAgent
	plan3 := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = plan3.Set(ctx, &PaperclipAgentResourceModel{
		CompanyID:       types.StringValue("comp-3"),
		Name:            types.StringValue("pause-ag"),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(false),
		CanCreateSkills: types.BoolValue(true),
		Paused:          types.BoolValue(true),
		Retirement:      types.StringValue("delete"),
	})
	var createResp3 resource.CreateResponse
	createResp3.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan3}, &createResp3)
	if createResp3.Diagnostics.HasError() {
		t.Fatal(createResp3.Diagnostics)
	}
	if !pausedCalled {
		t.Fatal("expected PauseAgent to be called on create when paused=true")
	}

	// Case 4: Invalid authorization policy on Create
	plan4 := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = plan4.Set(ctx, &PaperclipAgentResourceModel{
		CompanyID:           types.StringValue("comp-1"),
		Name:                types.StringValue("inv-policy"),
		AuthorizationPolicy: types.StringValue("{invalid-json"),
	})
	var createResp4 resource.CreateResponse
	createResp4.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan4}, &createResp4)
	if !createResp4.Diagnostics.HasError() {
		t.Fatal("expected error on invalid authorization_policy in Create")
	}

	// Case 5: ReportsTo cycle/missing on Create
	plan5 := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = plan5.Set(ctx, &PaperclipAgentResourceModel{
		CompanyID: types.StringValue("comp-1"),
		Name:      types.StringValue("inv-reports-to"),
		ReportsTo: types.StringValue("missing-mgr"),
	})
	var createResp5 resource.CreateResponse
	createResp5.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan5}, &createResp5)
	if !createResp5.Diagnostics.HasError() {
		t.Fatal("expected error on missing reports_to in Create")
	}

	// Case 6: PauseAgent error on Create
	plan6 := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = plan6.Set(ctx, &PaperclipAgentResourceModel{
		CompanyID:       types.StringValue("comp-pause-err"),
		Name:            types.StringValue("pause-err-ag"),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(false),
		CanCreateSkills: types.BoolValue(true),
		Paused:          types.BoolValue(true),
	})
	var createResp6 resource.CreateResponse
	createResp6.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan6}, &createResp6)
	if !createResp6.Diagnostics.HasError() {
		t.Fatal("expected error on pause failure in Create")
	}

	// Case 7: TrustPreset on Create
	plan7 := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = plan7.Set(ctx, &PaperclipAgentResourceModel{
		CompanyID:       types.StringValue("comp-trust"),
		Name:            types.StringValue("trust-ag"),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(false),
		CanCreateSkills: types.BoolValue(true),
		TrustPreset:     types.StringValue("trusted"),
		Paused:          types.BoolValue(false),
		Retirement:      types.StringValue("delete"),
	})
	var createResp7 resource.CreateResponse
	createResp7.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan7}, &createResp7)
	if createResp7.Diagnostics.HasError() {
		t.Fatal(createResp7.Diagnostics)
	}
	var res7 PaperclipAgentResourceModel
	_ = createResp7.State.Get(ctx, &res7)
	if res7.TrustPreset.ValueString() != "trusted" {
		t.Fatalf("expected trust preset trusted, got: %s", res7.TrustPreset.ValueString())
	}
}

func TestAgentReadTerminatedAndPermissions(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/agents/ag-term":
			_ = json.NewEncoder(w).Encode(client.PaperclipAgent{
				ID:        "ag-term",
				CompanyID: "comp-1",
				Name:      "terminated-agent",
				Status:    "terminated",
			})
		case "/api/agents/ag-full-perms":
			_ = json.NewEncoder(w).Encode(client.PaperclipAgent{
				ID:        "ag-full-perms",
				CompanyID: "comp-1",
				Name:      "full-perms",
				Status:    "idle",
				Permissions: &client.PaperclipAgentPermissions{
					CanAssignTasks:      true,
					CanCreateAgents:     false,
					CanCreateSkills:     true,
					TrustPreset:         "standard",
					AuthorizationPolicy: map[string]interface{}{"protectedAgent": true},
				},
			})
		case "/api/agents/ag-nil-perms":
			_ = json.NewEncoder(w).Encode(client.PaperclipAgent{
				ID:          "ag-nil-perms",
				CompanyID:   "comp-1",
				Name:        "nil-perms",
				Status:      "idle",
				Permissions: nil,
			})
		case "/api/agents/ag-empty-inst":
			_ = json.NewEncoder(w).Encode(client.PaperclipAgent{
				ID:        "ag-empty-inst",
				CompanyID: "comp-1",
				Name:      "empty-inst",
				Status:    "idle",
			})
		case "/api/agents/ag-empty-inst/instructions-bundle/file":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"content": ""})
		case "/api/agents/ag-err-inst":
			_ = json.NewEncoder(w).Encode(client.PaperclipAgent{
				ID:        "ag-err-inst",
				CompanyID: "comp-1",
				Name:      "err-inst",
				Status:    "idle",
			})
		case "/api/agents/ag-err-inst/instructions-bundle/file":
			http.Error(w, `{"error":"instructions error"}`, http.StatusInternalServerError)
		case "/api/agents/ag-err-ag":
			http.Error(w, `{"error":"agent error"}`, http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cli, _ := client.NewClient(server.URL, "token")
	r := NewPaperclipAgentResource().(*PaperclipAgentResource)
	r.client = cli
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	// 1. Terminated agent is removed from state
	state1 := tfsdk.State{Schema: schemaResp.Schema}
	_ = state1.Set(ctx, &PaperclipAgentResourceModel{
		ID:        types.StringValue("ag-term"),
		CompanyID: types.StringValue("comp-1"),
	})
	var readResp1 resource.ReadResponse
	readResp1.State = state1
	r.Read(ctx, resource.ReadRequest{State: state1}, &readResp1)
	if readResp1.Diagnostics.HasError() {
		t.Fatal(readResp1.Diagnostics)
	}
	if !readResp1.State.Raw.IsNull() {
		t.Fatal("expected terminated agent to be removed from state")
	}

	// 2. Full permissions read
	state2 := tfsdk.State{Schema: schemaResp.Schema}
	_ = state2.Set(ctx, &PaperclipAgentResourceModel{
		ID:        types.StringValue("ag-full-perms"),
		CompanyID: types.StringValue("comp-1"),
	})
	var readResp2 resource.ReadResponse
	readResp2.State = state2
	r.Read(ctx, resource.ReadRequest{State: state2}, &readResp2)
	if readResp2.Diagnostics.HasError() {
		t.Fatal(readResp2.Diagnostics)
	}
	var res2 PaperclipAgentResourceModel
	_ = readResp2.State.Get(ctx, &res2)
	if res2.TrustPreset.ValueString() != "standard" || res2.AuthorizationPolicy.IsNull() {
		t.Fatalf("expected trust preset and auth policy: %+v", res2)
	}

	// 3. Nil permissions read
	state3 := tfsdk.State{Schema: schemaResp.Schema}
	_ = state3.Set(ctx, &PaperclipAgentResourceModel{
		ID:        types.StringValue("ag-nil-perms"),
		CompanyID: types.StringValue("comp-1"),
	})
	var readResp3 resource.ReadResponse
	readResp3.State = state3
	r.Read(ctx, resource.ReadRequest{State: state3}, &readResp3)
	if readResp3.Diagnostics.HasError() {
		t.Fatal(readResp3.Diagnostics)
	}
	var res3 PaperclipAgentResourceModel
	_ = readResp3.State.Get(ctx, &res3)
	if !res3.TrustPreset.IsNull() || !res3.AuthorizationPolicy.IsNull() || !res3.CanCreateSkills.IsNull() {
		t.Fatalf("expected nulls for nil permissions: %+v", res3)
	}

	// 4. Instructions empty string sets null
	state4 := tfsdk.State{Schema: schemaResp.Schema}
	_ = state4.Set(ctx, &PaperclipAgentResourceModel{
		ID:        types.StringValue("ag-empty-inst"),
		CompanyID: types.StringValue("comp-1"),
	})
	var readResp4 resource.ReadResponse
	readResp4.State = state4
	r.Read(ctx, resource.ReadRequest{State: state4}, &readResp4)
	if readResp4.Diagnostics.HasError() {
		t.Fatal(readResp4.Diagnostics)
	}
	var res4 PaperclipAgentResourceModel
	_ = readResp4.State.Get(ctx, &res4)
	if !res4.Instructions.IsNull() {
		t.Fatalf("expected null instructions: %+v", res4)
	}

	// 5. Instructions 500 error reports diagnostic error
	state5 := tfsdk.State{Schema: schemaResp.Schema}
	_ = state5.Set(ctx, &PaperclipAgentResourceModel{
		ID:        types.StringValue("ag-err-inst"),
		CompanyID: types.StringValue("comp-1"),
	})
	var readResp5 resource.ReadResponse
	readResp5.State = state5
	r.Read(ctx, resource.ReadRequest{State: state5}, &readResp5)
	if !readResp5.Diagnostics.HasError() {
		t.Fatal("expected error on instructions read failure")
	}

	// 6. GetAgent 500 error reports diagnostic error
	state6 := tfsdk.State{Schema: schemaResp.Schema}
	_ = state6.Set(ctx, &PaperclipAgentResourceModel{
		ID:        types.StringValue("ag-err-ag"),
		CompanyID: types.StringValue("comp-1"),
	})
	var readResp6 resource.ReadResponse
	readResp6.State = state6
	r.Read(ctx, resource.ReadRequest{State: state6}, &readResp6)
	if !readResp6.Diagnostics.HasError() {
		t.Fatal("expected error on agent read failure")
	}
}

func TestAgentUpdatePauseResumeAndRetirement(t *testing.T) {
	ctx := context.Background()
	pausedCalled := false
	resumeCalled := false
	instructionsCleared := false
	agUpdNilPermsGetCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/agents/ag-upd" && r.Method == http.MethodPatch:
			status := "idle"
			if pausedCalled && !resumeCalled {
				status = "paused"
			}
			_ = json.NewEncoder(w).Encode(client.PaperclipAgent{
				ID:        "ag-upd",
				CompanyID: "comp-1",
				Name:      "ag",
				Status:    status,
				Permissions: &client.PaperclipAgentPermissions{
					CanAssignTasks:  true,
					CanCreateAgents: false,
					CanCreateSkills: true,
				},
			})
		case r.URL.Path == "/api/agents/ag-upd" && r.Method == http.MethodGet:
			status := "idle"
			if pausedCalled && !resumeCalled {
				status = "paused"
			}
			_ = json.NewEncoder(w).Encode(client.PaperclipAgent{
				ID:        "ag-upd",
				CompanyID: "comp-1",
				Name:      "ag",
				Status:    status,
				Permissions: &client.PaperclipAgentPermissions{
					CanAssignTasks:  true,
					CanCreateAgents: false,
					CanCreateSkills: true,
				},
			})
		case r.URL.Path == "/api/agents/ag-upd/pause" && r.Method == http.MethodPost:
			pausedCalled = true
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/api/agents/ag-upd/resume" && r.Method == http.MethodPost:
			resumeCalled = true
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/api/agents/ag-upd/instructions-bundle/file" && r.Method == http.MethodPut:
			instructionsCleared = true
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/api/agents/ag-upd-full" && r.Method == http.MethodPatch:
			_ = json.NewEncoder(w).Encode(client.PaperclipAgent{
				ID:                   "ag-upd-full",
				CompanyID:            "comp-1",
				Name:                 "ag-full",
				Icon:                 "icon-new",
				Capabilities:         "caps-new",
				DefaultEnvironmentID: "env-new",
				Status:               "idle",
				Permissions: &client.PaperclipAgentPermissions{
					CanAssignTasks:      true,
					CanCreateAgents:     false,
					CanCreateSkills:     true,
					TrustPreset:         "trusted",
					AuthorizationPolicy: map[string]interface{}{"protectedAgent": true},
				},
			})
		case r.URL.Path == "/api/agents/ag-upd-full" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(client.PaperclipAgent{
				ID:                   "ag-upd-full",
				CompanyID:            "comp-1",
				Name:                 "ag-full",
				Icon:                 "icon-new",
				Capabilities:         "caps-new",
				DefaultEnvironmentID: "env-new",
				Status:               "idle",
				Permissions: &client.PaperclipAgentPermissions{
					CanAssignTasks:      true,
					CanCreateAgents:     false,
					CanCreateSkills:     true,
					TrustPreset:         "trusted",
					AuthorizationPolicy: map[string]interface{}{"protectedAgent": true},
				},
			})
		case r.URL.Path == "/api/agents/ag-upd-nil-perms" && r.Method == http.MethodPatch:
			_ = json.NewEncoder(w).Encode(client.PaperclipAgent{
				ID:        "ag-upd-nil-perms",
				CompanyID: "comp-1",
				Name:      "ag-nil",
				Status:    "idle",
				Permissions: &client.PaperclipAgentPermissions{
					CanAssignTasks:  true,
					CanCreateAgents: false,
					CanCreateSkills: true,
				},
			})
		case r.URL.Path == "/api/agents/ag-upd-nil-perms" && r.Method == http.MethodGet:
			agUpdNilPermsGetCount++
			perms := &client.PaperclipAgentPermissions{
				CanAssignTasks:  true,
				CanCreateAgents: false,
				CanCreateSkills: true,
			}
			if agUpdNilPermsGetCount > 1 {
				perms = nil
			}
			_ = json.NewEncoder(w).Encode(client.PaperclipAgent{
				ID:          "ag-upd-nil-perms",
				CompanyID:   "comp-1",
				Name:        "ag-nil",
				Status:      "idle",
				Permissions: perms,
			})
		case r.URL.Path == "/api/agents/ag-upd-full/permissions" && r.Method == http.MethodPatch:
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/api/agents/ag-upd-full/permissions" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(client.PaperclipAgentPermissions{
				CanAssignTasks:      true,
				CanCreateAgents:     false,
				CanCreateSkills:     true,
				TrustPreset:         "trusted",
				AuthorizationPolicy: map[string]interface{}{"protectedAgent": true},
			})
		case r.URL.Path == "/api/agents/ag-upd-nil-perms/permissions" && r.Method == http.MethodPatch:
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/api/agents/ag-upd-nil-perms/permissions" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(client.PaperclipAgentPermissions{
				CanAssignTasks:  true,
				CanCreateAgents: false,
				CanCreateSkills: true,
			})
		case r.URL.Path == "/api/agents/ag-pause-fail" && r.Method == http.MethodPatch:
			_ = json.NewEncoder(w).Encode(client.PaperclipAgent{
				ID:          "ag-pause-fail",
				CompanyID:   "comp-1",
				Name:        "ag-pf",
				Status:      "idle",
				Permissions: &client.PaperclipAgentPermissions{CanAssignTasks: true, CanCreateAgents: false, CanCreateSkills: true},
			})
		case r.URL.Path == "/api/agents/ag-pause-fail" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(client.PaperclipAgent{
				ID:          "ag-pause-fail",
				CompanyID:   "comp-1",
				Name:        "ag-pf",
				Status:      "idle",
				Permissions: &client.PaperclipAgentPermissions{CanAssignTasks: true, CanCreateAgents: false, CanCreateSkills: true},
			})
		case r.URL.Path == "/api/agents/ag-pause-fail/pause" && r.Method == http.MethodPost:
			http.Error(w, `{"error":"pause failed"}`, http.StatusInternalServerError)
		case r.URL.Path == "/api/agents/ag-resume-fail" && r.Method == http.MethodPatch:
			_ = json.NewEncoder(w).Encode(client.PaperclipAgent{
				ID:          "ag-resume-fail",
				CompanyID:   "comp-1",
				Name:        "ag-rf",
				Status:      "paused",
				Permissions: &client.PaperclipAgentPermissions{CanAssignTasks: true, CanCreateAgents: false, CanCreateSkills: true},
			})
		case r.URL.Path == "/api/agents/ag-resume-fail" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(client.PaperclipAgent{
				ID:          "ag-resume-fail",
				CompanyID:   "comp-1",
				Name:        "ag-rf",
				Status:      "paused",
				Permissions: &client.PaperclipAgentPermissions{CanAssignTasks: true, CanCreateAgents: false, CanCreateSkills: true},
			})
		case r.URL.Path == "/api/agents/ag-resume-fail/resume" && r.Method == http.MethodPost:
			http.Error(w, `{"error":"resume failed"}`, http.StatusInternalServerError)
		case r.URL.Path == "/api/agents/ag-upd-perms-fallback" && r.Method == http.MethodPatch:
			_ = json.NewEncoder(w).Encode(client.PaperclipAgent{
				ID:          "ag-upd-perms-fallback",
				CompanyID:   "comp-1",
				Name:        "ag-fb",
				Status:      "idle",
				Permissions: &client.PaperclipAgentPermissions{CanAssignTasks: true, CanCreateAgents: false, CanCreateSkills: true},
			})
		case r.URL.Path == "/api/agents/ag-upd-perms-fallback" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(client.PaperclipAgent{
				ID:          "ag-upd-perms-fallback",
				CompanyID:   "comp-1",
				Name:        "ag-fb",
				Status:      "idle",
				Permissions: &client.PaperclipAgentPermissions{CanAssignTasks: true, CanCreateAgents: false, CanCreateSkills: true},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cli, _ := client.NewClient(server.URL, "token")
	r := NewPaperclipAgentResource().(*PaperclipAgentResource)
	r.client = cli
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	// Update calling Pause
	planPause := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = planPause.Set(ctx, &PaperclipAgentResourceModel{
		ID:              types.StringValue("ag-upd"),
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("ag"),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(false),
		CanCreateSkills: types.BoolValue(true),
		Paused:          types.BoolValue(true),
	})
	stateIdle := tfsdk.State{Schema: schemaResp.Schema}
	_ = stateIdle.Set(ctx, &PaperclipAgentResourceModel{
		ID:              types.StringValue("ag-upd"),
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("ag"),
		Status:          types.StringValue("idle"),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(false),
		CanCreateSkills: types.BoolValue(true),
		Paused:          types.BoolValue(false),
		Instructions:    types.StringValue("old instructions"),
		Retirement:      types.StringValue("terminate"),
	})

	var updResp1 resource.UpdateResponse
	updResp1.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Update(ctx, resource.UpdateRequest{Plan: planPause, State: stateIdle}, &updResp1)
	if updResp1.Diagnostics.HasError() {
		t.Fatal(updResp1.Diagnostics)
	}
	if !pausedCalled {
		t.Fatal("expected PauseAgent to be called")
	}
	if !instructionsCleared {
		t.Fatal("expected instructions to be cleared")
	}
	var res1 PaperclipAgentResourceModel
	_ = updResp1.State.Get(ctx, &res1)
	if res1.Retirement.ValueString() != "terminate" {
		t.Fatalf("expected retirement to be preserved from state: %s", res1.Retirement.ValueString())
	}

	// Update calling Resume
	planResume := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = planResume.Set(ctx, &PaperclipAgentResourceModel{
		ID:              types.StringValue("ag-upd"),
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("ag"),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(false),
		CanCreateSkills: types.BoolValue(true),
		Paused:          types.BoolValue(false),
	})
	statePaused := tfsdk.State{Schema: schemaResp.Schema}
	_ = statePaused.Set(ctx, &PaperclipAgentResourceModel{
		ID:              types.StringValue("ag-upd"),
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("ag"),
		Status:          types.StringValue("paused"),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(false),
		CanCreateSkills: types.BoolValue(true),
		Paused:          types.BoolValue(true),
	})

	var updResp2 resource.UpdateResponse
	updResp2.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Update(ctx, resource.UpdateRequest{Plan: planResume, State: statePaused}, &updResp2)
	if updResp2.Diagnostics.HasError() {
		t.Fatal(updResp2.Diagnostics)
	}
	if !resumeCalled {
		t.Fatal("expected ResumeAgent to be called")
	}

	// 3. Update setting Icon, Capabilities, DefaultEnvironmentID, and TrustPreset
	planFull := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = planFull.Set(ctx, &PaperclipAgentResourceModel{
		ID:                   types.StringValue("ag-upd-full"),
		CompanyID:            types.StringValue("comp-1"),
		Name:                 types.StringValue("ag-full"),
		Icon:                 types.StringValue("icon-new"),
		Capabilities:         types.StringValue("caps-new"),
		DefaultEnvironmentID: types.StringValue("env-new"),
		CanAssignTasks:       types.BoolValue(true),
		CanCreateAgents:      types.BoolValue(false),
		CanCreateSkills:      types.BoolValue(true),
		TrustPreset:          types.StringValue("trusted"),
		AuthorizationPolicy:  types.StringValue("{\"protectedAgent\":true}"),
		Paused:               types.BoolValue(false),
	})
	stateFull := tfsdk.State{Schema: schemaResp.Schema}
	_ = stateFull.Set(ctx, &PaperclipAgentResourceModel{
		ID:              types.StringValue("ag-upd-full"),
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("ag-full"),
		Status:          types.StringValue("idle"),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(false),
		CanCreateSkills: types.BoolValue(true),
		Paused:          types.BoolValue(false),
	})
	var updResp3 resource.UpdateResponse
	updResp3.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Update(ctx, resource.UpdateRequest{Plan: planFull, State: stateFull}, &updResp3)
	if updResp3.Diagnostics.HasError() {
		t.Fatal(updResp3.Diagnostics)
	}
	var resFull PaperclipAgentResourceModel
	_ = updResp3.State.Get(ctx, &resFull)
	if resFull.TrustPreset.ValueString() != "trusted" || resFull.Icon.ValueString() != "icon-new" || resFull.Capabilities.ValueString() != "caps-new" || resFull.DefaultEnvironmentID.ValueString() != "env-new" {
		t.Fatalf("unexpected resFull: %+v", resFull)
	}

	// 4. Update with nil permissions returned by server
	planNil := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = planNil.Set(ctx, &PaperclipAgentResourceModel{
		ID:              types.StringValue("ag-upd-nil-perms"),
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("ag-nil"),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(false),
		CanCreateSkills: types.BoolValue(true),
		Paused:          types.BoolValue(false),
	})
	stateNil := tfsdk.State{Schema: schemaResp.Schema}
	_ = stateNil.Set(ctx, &PaperclipAgentResourceModel{
		ID:              types.StringValue("ag-upd-nil-perms"),
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("ag-nil"),
		Status:          types.StringValue("idle"),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(false),
		CanCreateSkills: types.BoolValue(true),
		Paused:          types.BoolValue(false),
	})
	var updResp4 resource.UpdateResponse
	updResp4.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Update(ctx, resource.UpdateRequest{Plan: planNil, State: stateNil}, &updResp4)
	if updResp4.Diagnostics.HasError() {
		t.Fatal(updResp4.Diagnostics)
	}
	var resNil PaperclipAgentResourceModel
	_ = updResp4.State.Get(ctx, &resNil)
	if !resNil.TrustPreset.IsNull() || !resNil.AuthorizationPolicy.IsNull() {
		t.Fatalf("expected nulls for nil permissions in update: %+v", resNil)
	}

	// 5. Update with invalid authorization policy
	planInvAuth := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = planInvAuth.Set(ctx, &PaperclipAgentResourceModel{
		ID:                  types.StringValue("ag-upd"),
		CompanyID:           types.StringValue("comp-1"),
		Name:                types.StringValue("ag"),
		AuthorizationPolicy: types.StringValue("{invalid-json"),
	})
	var updResp5 resource.UpdateResponse
	updResp5.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Update(ctx, resource.UpdateRequest{Plan: planInvAuth, State: stateIdle}, &updResp5)
	if !updResp5.Diagnostics.HasError() {
		t.Fatal("expected error on invalid authorization_policy in Update")
	}

	// 6. Update with reports_to cycle (self cycle)
	planCycle := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = planCycle.Set(ctx, &PaperclipAgentResourceModel{
		ID:        types.StringValue("ag-upd"),
		CompanyID: types.StringValue("comp-1"),
		Name:      types.StringValue("ag"),
		ReportsTo: types.StringValue("ag-upd"),
	})
	var updResp6 resource.UpdateResponse
	updResp6.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Update(ctx, resource.UpdateRequest{Plan: planCycle, State: stateIdle}, &updResp6)
	if !updResp6.Diagnostics.HasError() {
		t.Fatal("expected error on reports_to cycle in Update")
	}

	// 7. Update pause error
	planPauseErr := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = planPauseErr.Set(ctx, &PaperclipAgentResourceModel{
		ID:              types.StringValue("ag-pause-fail"),
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("ag-pf"),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(false),
		CanCreateSkills: types.BoolValue(true),
		Paused:          types.BoolValue(true),
	})
	statePauseErr := tfsdk.State{Schema: schemaResp.Schema}
	_ = statePauseErr.Set(ctx, &PaperclipAgentResourceModel{
		ID:              types.StringValue("ag-pause-fail"),
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("ag-pf"),
		Status:          types.StringValue("idle"),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(false),
		CanCreateSkills: types.BoolValue(true),
		Paused:          types.BoolValue(false),
	})
	var updResp7 resource.UpdateResponse
	updResp7.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Update(ctx, resource.UpdateRequest{Plan: planPauseErr, State: statePauseErr}, &updResp7)
	if !updResp7.Diagnostics.HasError() {
		t.Fatal("expected error on pause failure in Update")
	}

	// 8. Update resume error
	planResumeErr := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = planResumeErr.Set(ctx, &PaperclipAgentResourceModel{
		ID:              types.StringValue("ag-resume-fail"),
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("ag-rf"),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(false),
		CanCreateSkills: types.BoolValue(true),
		Paused:          types.BoolValue(false),
	})
	stateResumeErr := tfsdk.State{Schema: schemaResp.Schema}
	_ = stateResumeErr.Set(ctx, &PaperclipAgentResourceModel{
		ID:              types.StringValue("ag-resume-fail"),
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("ag-rf"),
		Status:          types.StringValue("paused"),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(false),
		CanCreateSkills: types.BoolValue(true),
		Paused:          types.BoolValue(true),
	})
	var updResp8 resource.UpdateResponse
	updResp8.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Update(ctx, resource.UpdateRequest{Plan: planResumeErr, State: stateResumeErr}, &updResp8)
	if !updResp8.Diagnostics.HasError() {
		t.Fatal("expected error on resume failure in Update")
	}

	// 9. Update where plan and state have null can_create_skills and updated.Permissions != nil
	planFB := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = planFB.Set(ctx, &PaperclipAgentResourceModel{
		ID:              types.StringValue("ag-upd-perms-fallback"),
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("ag-fb"),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(false),
		CanCreateSkills: types.BoolNull(),
		Paused:          types.BoolValue(false),
	})
	stateFB := tfsdk.State{Schema: schemaResp.Schema}
	_ = stateFB.Set(ctx, &PaperclipAgentResourceModel{
		ID:              types.StringValue("ag-upd-perms-fallback"),
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("ag-fb"),
		Status:          types.StringValue("idle"),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(false),
		CanCreateSkills: types.BoolNull(),
		Paused:          types.BoolValue(false),
	})
	var updResp9 resource.UpdateResponse
	updResp9.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Update(ctx, resource.UpdateRequest{Plan: planFB, State: stateFB}, &updResp9)
	if updResp9.Diagnostics.HasError() {
		t.Fatal(updResp9.Diagnostics)
	}

	// 10. Attempting to clear trust_preset in Update returns diagnostic error
	planClearTP := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = planClearTP.Set(ctx, &PaperclipAgentResourceModel{
		ID:              types.StringValue("ag-upd"),
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("ag"),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(false),
		TrustPreset:     types.StringNull(),
	})
	stateClearTP := tfsdk.State{Schema: schemaResp.Schema}
	_ = stateClearTP.Set(ctx, &PaperclipAgentResourceModel{
		ID:          types.StringValue("ag-upd"),
		CompanyID:   types.StringValue("comp-1"),
		Name:        types.StringValue("ag"),
		TrustPreset: types.StringValue("standard"),
	})
	var updResp10 resource.UpdateResponse
	updResp10.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Update(ctx, resource.UpdateRequest{Plan: planClearTP, State: stateClearTP}, &updResp10)
	if !updResp10.Diagnostics.HasError() {
		t.Fatal("expected diagnostic error when clearing trust_preset in Update")
	}

	// 11. Attempting to clear authorization_policy in Update returns diagnostic error
	planClearAP := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = planClearAP.Set(ctx, &PaperclipAgentResourceModel{
		ID:                  types.StringValue("ag-upd"),
		CompanyID:           types.StringValue("comp-1"),
		Name:                types.StringValue("ag"),
		CanAssignTasks:      types.BoolValue(true),
		CanCreateAgents:     types.BoolValue(false),
		AuthorizationPolicy: types.StringNull(),
	})
	stateClearAP := tfsdk.State{Schema: schemaResp.Schema}
	_ = stateClearAP.Set(ctx, &PaperclipAgentResourceModel{
		ID:                  types.StringValue("ag-upd"),
		CompanyID:           types.StringValue("comp-1"),
		Name:                types.StringValue("ag"),
		AuthorizationPolicy: types.StringValue(`{"protectedAgent":true}`),
	})
	var updResp11 resource.UpdateResponse
	updResp11.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Update(ctx, resource.UpdateRequest{Plan: planClearAP, State: stateClearAP}, &updResp11)
	if !updResp11.Diagnostics.HasError() {
		t.Fatal("expected diagnostic error when clearing authorization_policy in Update")
	}
}

func TestAgentDeleteTerminate(t *testing.T) {
	ctx := context.Background()
	terminateCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/agents/ag-term/terminate" && r.Method == http.MethodPost:
			terminateCalled = true
			w.WriteHeader(http.StatusOK)
		case r.URL.Path == "/api/agents/ag-term-404/terminate" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusNotFound)
		case r.URL.Path == "/api/agents/ag-term-err/terminate" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cli, _ := client.NewClient(server.URL, "token")
	r := NewPaperclipAgentResource().(*PaperclipAgentResource)
	r.client = cli
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	// 1. Terminate success
	state1 := tfsdk.State{Schema: schemaResp.Schema}
	_ = state1.Set(ctx, &PaperclipAgentResourceModel{
		ID:         types.StringValue("ag-term"),
		Retirement: types.StringValue("terminate"),
	})
	var delResp1 resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: state1}, &delResp1)
	if delResp1.Diagnostics.HasError() {
		t.Fatal(delResp1.Diagnostics)
	}
	if !terminateCalled {
		t.Fatal("expected TerminateAgent to be called")
	}

	// 2. Terminate 404 is ignored
	state2 := tfsdk.State{Schema: schemaResp.Schema}
	_ = state2.Set(ctx, &PaperclipAgentResourceModel{
		ID:         types.StringValue("ag-term-404"),
		Retirement: types.StringValue("terminate"),
	})
	var delResp2 resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: state2}, &delResp2)
	if delResp2.Diagnostics.HasError() {
		t.Fatal("expected 404 to be ignored on terminate")
	}

	// 3. Terminate 500 reports error
	state3 := tfsdk.State{Schema: schemaResp.Schema}
	_ = state3.Set(ctx, &PaperclipAgentResourceModel{
		ID:         types.StringValue("ag-term-err"),
		Retirement: types.StringValue("terminate"),
	})
	var delResp3 resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: state3}, &delResp3)
	if !delResp3.Diagnostics.HasError() {
		t.Fatal("expected error on terminate 500")
	}
}

func TestDataSourceAgentReadGUS413(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/agents/ag-full":
			_ = json.NewEncoder(w).Encode(client.PaperclipAgent{
				ID:                   "ag-full",
				CompanyID:            "comp-1",
				Name:                 "full",
				Role:                 "lead",
				Title:                "Lead",
				Icon:                 "icon-val",
				ReportsTo:            "mgr-val",
				Capabilities:         "cap-val",
				DefaultEnvironmentID: "env-val",
				AdapterType:          "antigravity",
				Status:               "paused",
				Permissions: &client.PaperclipAgentPermissions{
					CanAssignTasks:      true,
					CanCreateAgents:     false,
					CanCreateSkills:     true,
					TrustPreset:         "low_trust_review",
					AuthorizationPolicy: map[string]interface{}{"protectedAgent": true},
				},
			})
		case "/api/agents/ag-full/instructions-bundle/file":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"content": "# Instructions"})
		case "/api/agents/ag-empty-perms":
			_ = json.NewEncoder(w).Encode(client.PaperclipAgent{
				ID:          "ag-empty-perms",
				CompanyID:   "comp-1",
				Name:        "empty",
				Permissions: nil,
			})
		case "/api/agents/ag-nil-auth-policy":
			_ = json.NewEncoder(w).Encode(client.PaperclipAgent{
				ID:        "ag-nil-auth-policy",
				CompanyID: "comp-1",
				Name:      "nil-auth",
				Permissions: &client.PaperclipAgentPermissions{
					CanAssignTasks:      true,
					CanCreateAgents:     false,
					CanCreateSkills:     true,
					AuthorizationPolicy: nil,
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cli, _ := client.NewClient(server.URL, "token")
	d := NewPaperclipAgentDataSource().(*PaperclipAgentDataSource)
	d.client = cli
	var schemaResp datasource.SchemaResponse
	d.Schema(ctx, datasource.SchemaRequest{}, &schemaResp)

	// 1. Read with full attributes and permissions
	plan1 := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = plan1.Set(ctx, &PaperclipAgentDataSourceModel{
		ID: types.StringValue("ag-full"),
	})
	cfg1 := tfsdk.Config{Schema: schemaResp.Schema, Raw: plan1.Raw}
	var readResp1 datasource.ReadResponse
	readResp1.State = tfsdk.State{Schema: schemaResp.Schema}
	d.Read(ctx, datasource.ReadRequest{Config: cfg1}, &readResp1)
	if readResp1.Diagnostics.HasError() {
		t.Fatal(readResp1.Diagnostics)
	}
	var res1 PaperclipAgentDataSourceModel
	_ = readResp1.State.Get(ctx, &res1)
	if res1.Icon.ValueString() != "icon-val" ||
		res1.ReportsTo.ValueString() != "mgr-val" ||
		res1.Capabilities.ValueString() != "cap-val" ||
		res1.DefaultEnvironmentID.ValueString() != "env-val" ||
		!res1.Paused.ValueBool() ||
		res1.TrustPreset.ValueString() != "low_trust_review" ||
		res1.AuthorizationPolicy.IsNull() ||
		!res1.CanCreateSkills.ValueBool() ||
		res1.Instructions.ValueString() != "# Instructions" {
		t.Fatalf("unexpected data source read result: %+v", res1)
	}

	// 2. Read with nil permissions
	plan2 := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = plan2.Set(ctx, &PaperclipAgentDataSourceModel{
		ID: types.StringValue("ag-empty-perms"),
	})
	cfg2 := tfsdk.Config{Schema: schemaResp.Schema, Raw: plan2.Raw}
	var readResp2 datasource.ReadResponse
	readResp2.State = tfsdk.State{Schema: schemaResp.Schema}
	d.Read(ctx, datasource.ReadRequest{Config: cfg2}, &readResp2)
	if readResp2.Diagnostics.HasError() {
		t.Fatal(readResp2.Diagnostics)
	}
	var res2 PaperclipAgentDataSourceModel
	_ = readResp2.State.Get(ctx, &res2)
	if !res2.CanCreateSkills.IsNull() || !res2.TrustPreset.IsNull() || !res2.AuthorizationPolicy.IsNull() {
		t.Fatalf("expected nulls for nil permissions in data source: %+v", res2)
	}

	// 3. Read with non-nil permissions but nil AuthorizationPolicy
	plan3 := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = plan3.Set(ctx, &PaperclipAgentDataSourceModel{
		ID: types.StringValue("ag-nil-auth-policy"),
	})
	cfg3 := tfsdk.Config{Schema: schemaResp.Schema, Raw: plan3.Raw}
	var readResp3 datasource.ReadResponse
	readResp3.State = tfsdk.State{Schema: schemaResp.Schema}
	d.Read(ctx, datasource.ReadRequest{Config: cfg3}, &readResp3)
	if readResp3.Diagnostics.HasError() {
		t.Fatal(readResp3.Diagnostics)
	}
	var res3 PaperclipAgentDataSourceModel
	_ = readResp3.State.Get(ctx, &res3)
	if !res3.AuthorizationPolicy.IsNull() {
		t.Fatalf("expected null authorization_policy: %+v", res3)
	}
}
