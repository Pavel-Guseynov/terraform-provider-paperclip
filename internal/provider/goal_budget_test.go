package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.guseynov.org/guseynov/paperclip/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestGoalResourceFull(t *testing.T) {
	ctx := context.Background()

	goalsDB := map[string]client.PaperclipGoal{
		"parent-goal": {
			ID:        "parent-goal",
			CompanyID: "comp-1",
			Title:     "Parent Goal",
		},
		"cycle-1": {
			ID:        "cycle-1",
			CompanyID: "comp-1",
			ParentID:  "cycle-2",
		},
		"cycle-2": {
			ID:        "cycle-2",
			CompanyID: "comp-1",
			ParentID:  "cycle-1",
		},
		"other-comp-goal": {
			ID:        "other-comp-goal",
			CompanyID: "comp-other",
			Title:     "Other Company Goal",
		},
	}

	agentsDB := map[string]client.PaperclipAgent{
		"agent-1": {
			ID:        "agent-1",
			CompanyID: "comp-1",
			Name:      "Agent 1",
		},
		"agent-other": {
			ID:        "agent-other",
			CompanyID: "comp-other",
			Name:      "Agent Other",
		},
	}

	var (
		failDelete     bool
		failUpdate     bool
		failList       bool
		childGoalCount int
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/comp-1/goals":
			var g client.PaperclipGoal
			_ = json.NewDecoder(r.Body).Decode(&g)
			g.ID = "goal-created"
			g.CompanyID = "comp-1"
			goalsDB[g.ID] = g
			_ = json.NewEncoder(w).Encode(g)
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/goals":
			if failList {
				http.Error(w, "server error", http.StatusInternalServerError)
				return
			}
			var list []client.PaperclipGoal
			for _, g := range goalsDB {
				list = append(list, g)
			}
			if childGoalCount > 0 {
				list = append(list, client.PaperclipGoal{
					ID:        "child-goal",
					CompanyID: "comp-1",
					ParentID:  "goal-created",
					Title:     "Child Goal",
				})
			}
			_ = json.NewEncoder(w).Encode(list)
		case r.Method == http.MethodGet && r.URL.Path == "/api/goals/goal-created":
			g := goalsDB["goal-created"]
			_ = json.NewEncoder(w).Encode(g)
		case r.Method == http.MethodGet && r.URL.Path == "/api/goals/parent-goal":
			_ = json.NewEncoder(w).Encode(goalsDB["parent-goal"])
		case r.Method == http.MethodGet && r.URL.Path == "/api/goals/cycle-1":
			_ = json.NewEncoder(w).Encode(goalsDB["cycle-1"])
		case r.Method == http.MethodGet && r.URL.Path == "/api/goals/cycle-2":
			_ = json.NewEncoder(w).Encode(goalsDB["cycle-2"])
		case r.Method == http.MethodGet && r.URL.Path == "/api/goals/other-comp-goal":
			_ = json.NewEncoder(w).Encode(goalsDB["other-comp-goal"])
		case r.Method == http.MethodGet && r.URL.Path == "/api/goals/missing-goal":
			http.NotFound(w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/api/goals/err-goal":
			http.Error(w, "server error", http.StatusInternalServerError)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/goals/goal-created":
			if failUpdate {
				http.Error(w, "update error", http.StatusInternalServerError)
				return
			}
			var patch map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&patch)
			g := goalsDB["goal-created"]
			if t, ok := patch["title"].(string); ok {
				g.Title = t
			}
			if d, ok := patch["description"].(string); ok {
				g.Description = d
			} else if patch["description"] == nil {
				g.Description = ""
			}
			if p, ok := patch["parentId"].(string); ok {
				g.ParentID = p
			} else if patch["parentId"] == nil {
				g.ParentID = ""
			}
			if o, ok := patch["ownerAgentId"].(string); ok {
				g.OwnerAgentID = o
			} else if patch["ownerAgentId"] == nil {
				g.OwnerAgentID = ""
			}
			if st, ok := patch["status"].(string); ok {
				g.Status = st
			}
			goalsDB["goal-created"] = g
			_ = json.NewEncoder(w).Encode(g)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/goals/goal-created":
			if failDelete {
				http.Error(w, "cannot delete goal", http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/agent-1":
			_ = json.NewEncoder(w).Encode(agentsDB["agent-1"])
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/agent-other":
			_ = json.NewEncoder(w).Encode(agentsDB["agent-other"])
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	r := NewPaperclipGoalResource().(*PaperclipGoalResource)
	schema := configureTestResource(t, server.URL, r)

	// 1. Metadata & Configure
	var meta resource.MetadataResponse
	r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "paperclip"}, &meta)
	if meta.TypeName != "paperclip_goal" {
		t.Fatalf("unexpected type name: %s", meta.TypeName)
	}

	var confNil resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: nil}, &confNil)
	if confNil.Diagnostics.HasError() {
		t.Fatal("expected no error on nil ProviderData")
	}

	var confBad resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: "invalid"}, &confBad)
	if !confBad.Diagnostics.HasError() {
		t.Fatal("expected error on invalid ProviderData")
	}

	// 2. Hierarchy validation tests
	// client is nil -> returns nil
	nilRes := &PaperclipGoalResource{}
	if err := nilRes.validateGoalHierarchy(ctx, "g1", "c1", "p1", "a1"); err != nil {
		t.Fatalf("expected nil error on nil client, got %v", err)
	}

	// parentID == goalID
	if err := r.validateGoalHierarchy(ctx, "g1", "comp-1", "g1", ""); err == nil {
		t.Fatal("expected error for self-parent")
	}

	// parent not found
	if err := r.validateGoalHierarchy(ctx, "g1", "comp-1", "missing-goal", ""); err == nil {
		t.Fatal("expected error for missing parent")
	}

	// parent cross-company
	if err := r.validateGoalHierarchy(ctx, "g1", "comp-1", "other-comp-goal", ""); err == nil {
		t.Fatal("expected error for cross-company parent")
	}

	// ancestor cycle (curr == goalID)
	if err := r.validateGoalHierarchy(ctx, "cycle-1", "comp-1", "cycle-2", ""); err == nil {
		t.Fatal("expected cycle error")
	}

	// ancestor cross-company
	goalsDB["ancestor-bad"] = client.PaperclipGoal{ID: "ancestor-bad", CompanyID: "comp-other"}
	goalsDB["parent-bad-ancestor"] = client.PaperclipGoal{ID: "parent-bad-ancestor", CompanyID: "comp-1", ParentID: "ancestor-bad"}
	if err := r.validateGoalHierarchy(ctx, "g1", "comp-1", "parent-bad-ancestor", ""); err == nil {
		t.Fatal("expected error for cross-company ancestor")
	}

	// owner agent not found
	if err := r.validateGoalHierarchy(ctx, "g1", "comp-1", "", "missing-agent"); err == nil {
		t.Fatal("expected error for missing agent")
	}

	// owner agent cross-company
	if err := r.validateGoalHierarchy(ctx, "g1", "comp-1", "", "agent-other"); err == nil {
		t.Fatal("expected error for cross-company agent")
	}

	// valid parent and agent
	if err := r.validateGoalHierarchy(ctx, "g1", "comp-1", "parent-goal", "agent-1"); err != nil {
		t.Fatalf("expected valid hierarchy, got %v", err)
	}

	// 3. ModifyPlan
	// Raw is null
	var modRespNull resource.ModifyPlanResponse
	modRespNull.Plan = tfsdk.Plan{Schema: schema}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: tfsdk.Plan{Schema: schema}}, &modRespNull)
	if modRespNull.Diagnostics.HasError() {
		t.Fatal(modRespNull.Diagnostics)
	}

	// Bad plan
	badPlan := tfsdk.Plan{Schema: schema, Raw: tftypes.NewValue(tftypes.String, "invalid")}
	var modRespBad resource.ModifyPlanResponse
	modRespBad.Plan = badPlan
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: badPlan}, &modRespBad)
	if !modRespBad.Diagnostics.HasError() {
		t.Fatal("expected error on bad plan in ModifyPlan")
	}

	// CancelOnDestroy true -> Retirement synced to cancel
	planModel := PaperclipGoalResourceModel{
		CompanyID:       types.StringValue("comp-1"),
		Title:           types.StringValue("Goal Title"),
		CancelOnDestroy: types.BoolValue(true),
		Retirement:      types.StringValue("fail"),
	}
	plan := tfsdk.Plan{Schema: schema}
	_ = plan.Set(ctx, &planModel)
	var modResp1 resource.ModifyPlanResponse
	modResp1.Plan = plan
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan}, &modResp1)
	if modResp1.Diagnostics.HasError() {
		t.Fatal(modResp1.Diagnostics)
	}
	var modOut1 PaperclipGoalResourceModel
	_ = modResp1.Plan.Get(ctx, &modOut1)
	if modOut1.Retirement.ValueString() != "cancel" {
		t.Fatalf("expected retirement synced to cancel, got %s", modOut1.Retirement.ValueString())
	}

	// Retirement cancel -> CancelOnDestroy synced to true
	planModel2 := PaperclipGoalResourceModel{
		CompanyID:       types.StringValue("comp-1"),
		Title:           types.StringValue("Goal Title"),
		CancelOnDestroy: types.BoolValue(false),
		Retirement:      types.StringValue("cancel"),
	}
	plan2 := tfsdk.Plan{Schema: schema}
	_ = plan2.Set(ctx, &planModel2)
	var modResp2 resource.ModifyPlanResponse
	modResp2.Plan = plan2
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan2}, &modResp2)
	if modResp2.Diagnostics.HasError() {
		t.Fatal(modResp2.Diagnostics)
	}
	var modOut2 PaperclipGoalResourceModel
	_ = modResp2.Plan.Get(ctx, &modOut2)
	if !modOut2.CancelOnDestroy.ValueBool() {
		t.Fatal("expected cancel_on_destroy synced to true")
	}

	// CompanyID is unknown
	planModelUnknownComp := PaperclipGoalResourceModel{
		CompanyID: types.StringUnknown(),
		Title:     types.StringValue("Goal Title"),
	}
	planUnknown := tfsdk.Plan{Schema: schema}
	_ = planUnknown.Set(ctx, &planModelUnknownComp)
	var modRespUnknown resource.ModifyPlanResponse
	modRespUnknown.Plan = planUnknown
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: planUnknown}, &modRespUnknown)
	if modRespUnknown.Diagnostics.HasError() {
		t.Fatal(modRespUnknown.Diagnostics)
	}

	// Hierarchy validation failure in ModifyPlan (cross-company parent)
	planModelInvalidParent := PaperclipGoalResourceModel{
		CompanyID: types.StringValue("comp-1"),
		Title:     types.StringValue("Goal Title"),
		ParentID:  types.StringValue("other-comp-goal"),
	}
	planInvalidParent := tfsdk.Plan{Schema: schema}
	_ = planInvalidParent.Set(ctx, &planModelInvalidParent)
	var modRespInvalid resource.ModifyPlanResponse
	modRespInvalid.Plan = planInvalidParent
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: planInvalidParent}, &modRespInvalid)
	if !modRespInvalid.Diagnostics.HasError() {
		t.Fatal("expected error in ModifyPlan for invalid parent")
	}

	// 4. Create
	// Bad plan
	var createBad resource.CreateResponse
	r.Create(ctx, resource.CreateRequest{Plan: badPlan}, &createBad)
	if !createBad.Diagnostics.HasError() {
		t.Fatal("expected error on bad plan in Create")
	}

	// Hierarchy validation error in Create
	var createInvalidHier resource.CreateResponse
	createInvalidHier.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: planInvalidParent}, &createInvalidHier)
	if !createInvalidHier.Diagnostics.HasError() {
		t.Fatal("expected hierarchy error in Create")
	}

	// CreateGoal error (comp-err)
	planModelErrComp := PaperclipGoalResourceModel{
		CompanyID: types.StringValue("comp-err"),
		Title:     types.StringValue("Err Goal"),
	}
	planErrComp := tfsdk.Plan{Schema: schema}
	_ = planErrComp.Set(ctx, &planModelErrComp)
	var createErrComp resource.CreateResponse
	createErrComp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: planErrComp}, &createErrComp)
	if !createErrComp.Diagnostics.HasError() {
		t.Fatal("expected error on failed CreateGoal")
	}

	// Create success with all fields
	fullPlanModel := PaperclipGoalResourceModel{
		CompanyID:       types.StringValue("comp-1"),
		Title:           types.StringValue("Full Goal"),
		Description:     types.StringValue("Full Goal Description"),
		Level:           types.StringValue("team"),
		Status:          types.StringValue("active"),
		ParentID:        types.StringValue("parent-goal"),
		OwnerAgentID:    types.StringValue("agent-1"),
		CancelOnDestroy: types.BoolValue(true),
		Retirement:      types.StringValue("cancel"),
	}
	fullPlan := tfsdk.Plan{Schema: schema}
	_ = fullPlan.Set(ctx, &fullPlanModel)
	var createResp resource.CreateResponse
	createResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: fullPlan}, &createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("Create failed: %v", createResp.Diagnostics)
	}
	var createdState PaperclipGoalResourceModel
	_ = createResp.State.Get(ctx, &createdState)
	if createdState.ID.ValueString() != "goal-created" {
		t.Fatalf("unexpected goal ID: %s", createdState.ID.ValueString())
	}

	// 5. Read
	// Bad state
	badState := tfsdk.State{Schema: schema, Raw: tftypes.NewValue(tftypes.String, "invalid")}
	var readBad resource.ReadResponse
	r.Read(ctx, resource.ReadRequest{State: badState}, &readBad)
	if !readBad.Diagnostics.HasError() {
		t.Fatal("expected error on bad state in Read")
	}

	// 404 removes resource
	state404Model := PaperclipGoalResourceModel{
		ID:        types.StringValue("missing-goal"),
		CompanyID: types.StringValue("comp-1"),
	}
	state404 := tfsdk.State{Schema: schema}
	_ = state404.Set(ctx, &state404Model)
	var read404 resource.ReadResponse
	read404.State = tfsdk.State{Schema: schema}
	r.Read(ctx, resource.ReadRequest{State: state404}, &read404)
	if read404.Diagnostics.HasError() || !read404.State.Raw.IsNull() {
		t.Fatal("expected state removed on 404 Read")
	}

	// Server error on Read
	stateErrModel := PaperclipGoalResourceModel{
		ID:        types.StringValue("err-goal"),
		CompanyID: types.StringValue("comp-1"),
	}
	stateErr := tfsdk.State{Schema: schema}
	_ = stateErr.Set(ctx, &stateErrModel)
	var readErr resource.ReadResponse
	readErr.State = tfsdk.State{Schema: schema}
	r.Read(ctx, resource.ReadRequest{State: stateErr}, &readErr)
	if !readErr.Diagnostics.HasError() {
		t.Fatal("expected diagnostic error on 500 Read")
	}

	// Read success
	var readSuccess resource.ReadResponse
	readSuccess.State = tfsdk.State{Schema: schema}
	r.Read(ctx, resource.ReadRequest{State: createResp.State}, &readSuccess)
	if readSuccess.Diagnostics.HasError() {
		t.Fatalf("Read failed: %v", readSuccess.Diagnostics)
	}

	// Read with null cancel_on_destroy and retirement (as after import)
	stateImportModel := PaperclipGoalResourceModel{
		ID:              types.StringValue("goal-created"),
		CompanyID:       types.StringValue("comp-1"),
		CancelOnDestroy: types.BoolNull(),
		Retirement:      types.StringNull(),
	}
	stateImport := tfsdk.State{Schema: schema}
	_ = stateImport.Set(ctx, &stateImportModel)
	var readImportSuccess resource.ReadResponse
	readImportSuccess.State = tfsdk.State{Schema: schema}
	r.Read(ctx, resource.ReadRequest{State: stateImport}, &readImportSuccess)
	if readImportSuccess.Diagnostics.HasError() {
		t.Fatalf("Read after import failed: %v", readImportSuccess.Diagnostics)
	}
	var readImportState PaperclipGoalResourceModel
	_ = readImportSuccess.State.Get(ctx, &readImportState)
	if !readImportState.CancelOnDestroy.ValueBool() || readImportState.Retirement.ValueString() != "cancel" {
		t.Fatalf("expected cancel_on_destroy and retirement defaulted, got %+v", readImportState)
	}

	// 6. Update
	// Bad plan
	var updateBad resource.UpdateResponse
	r.Update(ctx, resource.UpdateRequest{Plan: badPlan, State: createResp.State}, &updateBad)
	if !updateBad.Diagnostics.HasError() {
		t.Fatal("expected error on bad plan in Update")
	}

	// Hierarchy error in Update
	var updateInvalidHier resource.UpdateResponse
	r.Update(ctx, resource.UpdateRequest{Plan: planInvalidParent, State: createResp.State}, &updateInvalidHier)
	if !updateInvalidHier.Diagnostics.HasError() {
		t.Fatal("expected hierarchy error in Update")
	}

	// Update with cleared fields
	clearPlanModel := PaperclipGoalResourceModel{
		ID:           types.StringValue("goal-created"),
		CompanyID:    types.StringValue("comp-1"),
		Title:        types.StringValue("Goal Cleared"),
		Description:  types.StringNull(),
		Level:        types.StringValue("task"),
		Status:       types.StringValue("planned"),
		ParentID:     types.StringNull(),
		OwnerAgentID: types.StringNull(),
	}
	clearPlan := tfsdk.Plan{Schema: schema}
	_ = clearPlan.Set(ctx, &clearPlanModel)
	var updateClearResp resource.UpdateResponse
	updateClearResp.State = tfsdk.State{Schema: schema}
	r.Update(ctx, resource.UpdateRequest{Plan: clearPlan, State: createResp.State}, &updateClearResp)
	if updateClearResp.Diagnostics.HasError() {
		t.Fatalf("Update with clear fields failed: %v", updateClearResp.Diagnostics)
	}
	var clearState PaperclipGoalResourceModel
	_ = updateClearResp.State.Get(ctx, &clearState)
	if !clearState.Description.IsNull() || !clearState.ParentID.IsNull() || !clearState.OwnerAgentID.IsNull() {
		t.Fatalf("expected null cleared fields, got: %+v", clearState)
	}

	// Update failure (failUpdate = true)
	failUpdate = true
	var updateFailResp resource.UpdateResponse
	updateFailResp.State = tfsdk.State{Schema: schema}
	r.Update(ctx, resource.UpdateRequest{Plan: clearPlan, State: createResp.State}, &updateFailResp)
	if !updateFailResp.Diagnostics.HasError() {
		t.Fatal("expected error on failed UpdateGoal")
	}
	failUpdate = false

	// 7. Delete
	// Bad state
	var deleteBad resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: badState}, &deleteBad)
	if !deleteBad.Diagnostics.HasError() {
		t.Fatal("expected error on bad state in Delete")
	}

	// Referenced by child goal, shouldCancel = false -> error
	childGoalCount = 1
	stateNoCancel := PaperclipGoalResourceModel{
		ID:              types.StringValue("goal-created"),
		CompanyID:       types.StringValue("comp-1"),
		Title:           types.StringValue("Referenced Goal"),
		CancelOnDestroy: types.BoolValue(false),
		Retirement:      types.StringValue("fail"),
	}
	stateNoCancelState := tfsdk.State{Schema: schema}
	_ = stateNoCancelState.Set(ctx, &stateNoCancel)
	var delRefNoCancel resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: stateNoCancelState}, &delRefNoCancel)
	if !delRefNoCancel.Diagnostics.HasError() {
		t.Fatal("expected error deleting referenced goal without cancel")
	}

	// Referenced by child goal, shouldCancel = true, UpdateGoal succeeds
	stateCancel := PaperclipGoalResourceModel{
		ID:              types.StringValue("goal-created"),
		CompanyID:       types.StringValue("comp-1"),
		Title:           types.StringValue("Referenced Goal"),
		CancelOnDestroy: types.BoolValue(true),
		Retirement:      types.StringValue("cancel"),
	}
	stateCancelState := tfsdk.State{Schema: schema}
	_ = stateCancelState.Set(ctx, &stateCancel)
	var delRefCancel resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: stateCancelState}, &delRefCancel)
	if delRefCancel.Diagnostics.HasError() {
		t.Fatalf("expected successful cancel on destroy, got: %v", delRefCancel.Diagnostics)
	}

	// Referenced by child goal, shouldCancel = true, UpdateGoal fails
	failUpdate = true
	var delRefCancelFail resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: stateCancelState}, &delRefCancelFail)
	if !delRefCancelFail.Diagnostics.HasError() {
		t.Fatal("expected error when cancelling referenced goal fails")
	}
	failUpdate = false
	childGoalCount = 0

	// Not referenced, Delete fails, shouldCancel = true, UpdateGoal succeeds
	failDelete = true
	var delFailCancelSuccess resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: stateCancelState}, &delFailCancelSuccess)
	if delFailCancelSuccess.Diagnostics.HasError() {
		t.Fatalf("expected successful cancel fallback on delete failure, got: %v", delFailCancelSuccess.Diagnostics)
	}

	// Not referenced, Delete fails, shouldCancel = true, UpdateGoal fails
	failUpdate = true
	var delFailCancelFail resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: stateCancelState}, &delFailCancelFail)
	if !delFailCancelFail.Diagnostics.HasError() {
		t.Fatal("expected error when both delete and cancel fallback fail")
	}
	failUpdate = false

	// Not referenced, Delete fails, shouldCancel = false -> error
	var delFailNoCancel resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: stateNoCancelState}, &delFailNoCancel)
	if !delFailNoCancel.Diagnostics.HasError() {
		t.Fatal("expected error on failed DeleteGoal without cancel")
	}
	failDelete = false

	// ListGoals fails during delete -> proceeds to DeleteGoal and succeeds
	failList = true
	var delListFail resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: stateNoCancelState}, &delListFail)
	if delListFail.Diagnostics.HasError() {
		t.Fatalf("expected Delete to succeed even when ListGoals fails, got %v", delListFail.Diagnostics)
	}
	failList = false

	// 8. ImportState
	// "comp-1/goal-created"
	var impResp2 resource.ImportStateResponse
	impResp2.State = tfsdk.State{Schema: schema}
	_ = impResp2.State.Set(ctx, &PaperclipGoalResourceModel{})
	r.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/goal-created"}, &impResp2)
	if impResp2.Diagnostics.HasError() {
		t.Fatalf("Import comp/id failed: %v", impResp2.Diagnostics)
	}
	var impOut2 PaperclipGoalResourceModel
	_ = impResp2.State.Get(ctx, &impOut2)
	if impOut2.CompanyID.ValueString() != "comp-1" || impOut2.ID.ValueString() != "goal-created" {
		t.Fatalf("unexpected imported model: %+v", impOut2)
	}

	// "goal-created"
	var impResp1 resource.ImportStateResponse
	impResp1.State = tfsdk.State{Schema: schema}
	_ = impResp1.State.Set(ctx, &PaperclipGoalResourceModel{})
	r.ImportState(ctx, resource.ImportStateRequest{ID: "goal-created"}, &impResp1)
	if impResp1.Diagnostics.HasError() {
		t.Fatalf("Import single id failed: %v", impResp1.Diagnostics)
	}

	// Invalid import ID
	var impRespBad resource.ImportStateResponse
	r.ImportState(ctx, resource.ImportStateRequest{ID: ""}, &impRespBad)
	if !impRespBad.Diagnostics.HasError() {
		t.Fatal("expected error on empty import ID")
	}
}

func TestBudgetPolicyResourceFull(t *testing.T) {
	ctx := context.Background()

	policiesDB := map[string]client.PaperclipBudgetPolicySummary{
		"pol-1": {
			PolicyID:        "pol-1",
			CompanyID:       "comp-1",
			ScopeType:       "company",
			ScopeID:         "comp-1",
			Metric:          "billed_cents",
			WindowKind:      "calendar_month_utc",
			Amount:          10000,
			WarnPercent:     80,
			HardStopEnabled: true,
			NotifyEnabled:   true,
			IsActive:        true,
		},
		"pol-inactive": {
			PolicyID:        "pol-inactive",
			CompanyID:       "comp-1",
			ScopeType:       "company",
			ScopeID:         "comp-1",
			Metric:          "billed_cents",
			WindowKind:      "lifetime",
			Amount:          0,
			WarnPercent:     80,
			HardStopEnabled: true,
			NotifyEnabled:   true,
			IsActive:        false,
		},
	}

	var (
		failUpsert bool
		failList   bool
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/comp-1/budgets/policies":
			if failUpsert {
				http.Error(w, "upsert error", http.StatusInternalServerError)
				return
			}
			var p map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&p)
			amt, _ := p["amount"].(float64)
			wp, _ := p["warnPercent"].(float64)
			hardStop, _ := p["hardStopEnabled"].(bool)
			notify, _ := p["notifyEnabled"].(bool)
			active, _ := p["isActive"].(bool)
			st := p["scopeType"].(string)
			sid := p["scopeId"].(string)
			wk := p["windowKind"].(string)
			pol := client.PaperclipBudgetPolicySummary{
				PolicyID:        "pol-upserted",
				CompanyID:       "comp-1",
				ScopeType:       st,
				ScopeID:         sid,
				Metric:          p["metric"].(string),
				WindowKind:      wk,
				Amount:          int64(amt),
				WarnPercent:     int64(wp),
				HardStopEnabled: hardStop,
				NotifyEnabled:   notify,
				IsActive:        active,
			}
			policiesDB[pol.PolicyID] = pol
			_ = json.NewEncoder(w).Encode(pol)
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/budgets/overview":
			if failList {
				http.Error(w, "server error", http.StatusInternalServerError)
				return
			}
			var list []client.PaperclipBudgetPolicySummary
			for _, pol := range policiesDB {
				list = append(list, pol)
			}
			_ = json.NewEncoder(w).Encode(client.PaperclipBudgetOverview{Policies: list})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1":
			_ = json.NewEncoder(w).Encode(client.PaperclipCompany{ID: "comp-1", Name: "Comp 1"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies":
			_ = json.NewEncoder(w).Encode([]client.PaperclipCompany{{ID: "comp-1", Name: "Comp 1"}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/agent-1":
			_ = json.NewEncoder(w).Encode(client.PaperclipAgent{ID: "agent-1", CompanyID: "comp-1"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/agent-other":
			_ = json.NewEncoder(w).Encode(client.PaperclipAgent{ID: "agent-other", CompanyID: "comp-other"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/projects/proj-1":
			_ = json.NewEncoder(w).Encode(client.PaperclipProject{ID: "proj-1", CompanyID: "comp-1"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/projects/proj-other":
			_ = json.NewEncoder(w).Encode(client.PaperclipProject{ID: "proj-other", CompanyID: "comp-other"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	r := NewPaperclipBudgetPolicyResource().(*PaperclipBudgetPolicyResource)
	schema := configureTestResource(t, server.URL, r)

	// 1. Metadata & Configure
	var meta resource.MetadataResponse
	r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "paperclip"}, &meta)
	if meta.TypeName != "paperclip_budget_policy" {
		t.Fatalf("unexpected metadata type name: %s", meta.TypeName)
	}

	var confNil resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: nil}, &confNil)
	if confNil.Diagnostics.HasError() {
		t.Fatal("expected no error on nil ProviderData")
	}

	var confBad resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: "invalid"}, &confBad)
	if !confBad.Diagnostics.HasError() {
		t.Fatal("expected error on invalid ProviderData")
	}

	// 2. Alias sync testing
	m1 := PaperclipBudgetPolicyResourceModel{Window: types.StringValue("lifetime")}
	syncBudgetPolicyAliases(&m1)
	if m1.WindowKind.ValueString() != "lifetime" {
		t.Fatal("expected window_kind synced from window")
	}

	m2 := PaperclipBudgetPolicyResourceModel{WindowKind: types.StringValue("calendar_month_utc")}
	syncBudgetPolicyAliases(&m2)
	if m2.Window.ValueString() != "calendar_month_utc" {
		t.Fatal("expected window synced from window_kind")
	}

	m3 := PaperclipBudgetPolicyResourceModel{}
	syncBudgetPolicyAliases(&m3)
	if m3.WindowKind.ValueString() != "calendar_month_utc" || !m3.HardStop.ValueBool() || !m3.Notify.ValueBool() || !m3.Active.ValueBool() {
		t.Fatalf("expected defaults from syncBudgetPolicyAliases, got: %+v", m3)
	}

	m4 := PaperclipBudgetPolicyResourceModel{
		HardStopEnabled: types.BoolValue(false),
		NotifyEnabled:   types.BoolValue(false),
		IsActive:        types.BoolValue(false),
	}
	syncBudgetPolicyAliases(&m4)
	if m4.HardStop.ValueBool() || m4.Notify.ValueBool() || m4.Active.ValueBool() {
		t.Fatal("expected false values synced from aliases")
	}

	m5 := PaperclipBudgetPolicyResourceModel{
		HardStop: types.BoolValue(false),
		Notify:   types.BoolValue(false),
		Active:   types.BoolValue(false),
	}
	syncBudgetPolicyAliases(&m5)
	if m5.HardStopEnabled.ValueBool() || m5.NotifyEnabled.ValueBool() || m5.IsActive.ValueBool() {
		t.Fatal("expected false values synced to aliases")
	}

	// 3. validateBudgetScope
	nilRes := &PaperclipBudgetPolicyResource{}
	if err := nilRes.validateBudgetScope(ctx, "comp-1", "company", "comp-1"); err != nil {
		t.Fatal(err)
	}

	// company scope mismatch
	if err := r.validateBudgetScope(ctx, "comp-1", "company", "comp-2"); err == nil {
		t.Fatal("expected error for company scope mismatch")
	}
	// company scope not found
	if err := r.validateBudgetScope(ctx, "comp-missing", "company", "comp-missing"); err == nil {
		t.Fatal("expected error for missing company")
	}
	// company scope valid
	if err := r.validateBudgetScope(ctx, "comp-1", "company", "comp-1"); err != nil {
		t.Fatal(err)
	}

	// agent scope not found
	if err := r.validateBudgetScope(ctx, "comp-1", "agent", "agent-missing"); err == nil {
		t.Fatal("expected error for missing agent scope")
	}
	// agent scope cross-company
	if err := r.validateBudgetScope(ctx, "comp-1", "agent", "agent-other"); err == nil {
		t.Fatal("expected error for cross-company agent scope")
	}
	// agent scope valid
	if err := r.validateBudgetScope(ctx, "comp-1", "agent", "agent-1"); err != nil {
		t.Fatal(err)
	}

	// project scope not found
	if err := r.validateBudgetScope(ctx, "comp-1", "project", "proj-missing"); err == nil {
		t.Fatal("expected error for missing project scope")
	}
	// project scope cross-company
	if err := r.validateBudgetScope(ctx, "comp-1", "project", "proj-other"); err == nil {
		t.Fatal("expected error for cross-company project scope")
	}
	// project scope valid
	if err := r.validateBudgetScope(ctx, "comp-1", "project", "proj-1"); err != nil {
		t.Fatal(err)
	}

	// 4. checkDuplicatePolicyIdentity
	if err := nilRes.checkDuplicatePolicyIdentity(ctx, "comp-1", "company", "comp-1", "calendar_month_utc", ""); err != nil {
		t.Fatal(err)
	}

	// duplicate active policy found with different ID
	if err := r.checkDuplicatePolicyIdentity(ctx, "comp-1", "company", "comp-1", "calendar_month_utc", "different-id"); err == nil {
		t.Fatal("expected duplicate policy identity error")
	}

	// duplicate active policy found with same ID -> permitted
	if err := r.checkDuplicatePolicyIdentity(ctx, "comp-1", "company", "comp-1", "calendar_month_utc", "pol-1"); err != nil {
		t.Fatalf("expected same ID to be permitted, got %v", err)
	}

	// distinct windowKind -> permitted
	if err := r.checkDuplicatePolicyIdentity(ctx, "comp-1", "company", "comp-1", "lifetime", ""); err != nil {
		t.Fatalf("expected distinct windowKind to be permitted, got %v", err)
	}

	// failList -> returns nil
	failList = true
	if err := r.checkDuplicatePolicyIdentity(ctx, "comp-1", "company", "comp-1", "calendar_month_utc", ""); err != nil {
		t.Fatal("expected nil when ListBudgetPolicies fails in duplicate check")
	}
	failList = false

	// 5. ModifyPlan
	// Raw is null
	var modRespNull resource.ModifyPlanResponse
	modRespNull.Plan = tfsdk.Plan{Schema: schema}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: tfsdk.Plan{Schema: schema}}, &modRespNull)
	if modRespNull.Diagnostics.HasError() {
		t.Fatal(modRespNull.Diagnostics)
	}

	// Bad plan
	badPlan := tfsdk.Plan{Schema: schema, Raw: tftypes.NewValue(tftypes.String, "invalid")}
	var modRespBad resource.ModifyPlanResponse
	modRespBad.Plan = badPlan
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: badPlan}, &modRespBad)
	if !modRespBad.Diagnostics.HasError() {
		t.Fatal("expected error on bad plan in ModifyPlan")
	}

	// Negative amount
	planModelNegAmt := PaperclipBudgetPolicyResourceModel{
		CompanyID: types.StringValue("comp-1"),
		ScopeType: types.StringValue("company"),
		ScopeID:   types.StringValue("comp-1"),
		Amount:    types.Int64Value(-100),
	}
	planNegAmt := tfsdk.Plan{Schema: schema}
	_ = planNegAmt.Set(ctx, &planModelNegAmt)
	var modRespNegAmt resource.ModifyPlanResponse
	modRespNegAmt.Plan = planNegAmt
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: planNegAmt}, &modRespNegAmt)
	if !modRespNegAmt.Diagnostics.HasError() {
		t.Fatal("expected error on negative amount")
	}

	// Invalid warn percent (< 1 or > 99)
	planModelWarnLow := PaperclipBudgetPolicyResourceModel{
		CompanyID:   types.StringValue("comp-1"),
		ScopeType:   types.StringValue("company"),
		ScopeID:     types.StringValue("comp-1"),
		Amount:      types.Int64Value(100),
		WarnPercent: types.Int64Value(0),
	}
	planWarnLow := tfsdk.Plan{Schema: schema}
	_ = planWarnLow.Set(ctx, &planModelWarnLow)
	var modRespWarnLow resource.ModifyPlanResponse
	modRespWarnLow.Plan = planWarnLow
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: planWarnLow}, &modRespWarnLow)
	if !modRespWarnLow.Diagnostics.HasError() {
		t.Fatal("expected error on warn percent < 1")
	}

	planModelWarnHigh := PaperclipBudgetPolicyResourceModel{
		CompanyID:   types.StringValue("comp-1"),
		ScopeType:   types.StringValue("company"),
		ScopeID:     types.StringValue("comp-1"),
		Amount:      types.Int64Value(100),
		WarnPercent: types.Int64Value(100),
	}
	planWarnHigh := tfsdk.Plan{Schema: schema}
	_ = planWarnHigh.Set(ctx, &planModelWarnHigh)
	var modRespWarnHigh resource.ModifyPlanResponse
	modRespWarnHigh.Plan = planWarnHigh
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: planWarnHigh}, &modRespWarnHigh)
	if !modRespWarnHigh.Diagnostics.HasError() {
		t.Fatal("expected error on warn percent > 99")
	}

	// Duplicate identity in ModifyPlan
	planModelDup := PaperclipBudgetPolicyResourceModel{
		CompanyID:   types.StringValue("comp-1"),
		ScopeType:   types.StringValue("company"),
		ScopeID:     types.StringValue("comp-1"),
		WindowKind:  types.StringValue("calendar_month_utc"),
		Amount:      types.Int64Value(100),
		WarnPercent: types.Int64Value(80),
	}
	planDup := tfsdk.Plan{Schema: schema}
	_ = planDup.Set(ctx, &planModelDup)
	var modRespDup resource.ModifyPlanResponse
	modRespDup.Plan = planDup
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: planDup}, &modRespDup)
	if !modRespDup.Diagnostics.HasError() {
		t.Fatal("expected duplicate identity error in ModifyPlan")
	}

	// Valid plan in ModifyPlan
	planModelValidModify := PaperclipBudgetPolicyResourceModel{
		CompanyID:   types.StringValue("comp-1"),
		ScopeType:   types.StringValue("agent"),
		ScopeID:     types.StringValue("agent-1"),
		WindowKind:  types.StringValue("lifetime"),
		Amount:      types.Int64Value(5000),
		WarnPercent: types.Int64Value(80),
	}
	planValidModify := tfsdk.Plan{Schema: schema}
	_ = planValidModify.Set(ctx, &planModelValidModify)
	var modRespValid resource.ModifyPlanResponse
	modRespValid.Plan = planValidModify
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: planValidModify}, &modRespValid)
	if modRespValid.Diagnostics.HasError() {
		t.Fatalf("unexpected error on valid plan in ModifyPlan: %v", modRespValid.Diagnostics)
	}

	// 6. Create
	// Bad plan
	var createBad resource.CreateResponse
	r.Create(ctx, resource.CreateRequest{Plan: badPlan}, &createBad)
	if !createBad.Diagnostics.HasError() {
		t.Fatal("expected error on bad plan in Create")
	}

	// Scope validation error in Create
	planModelBadScope := PaperclipBudgetPolicyResourceModel{
		CompanyID: types.StringValue("comp-1"),
		ScopeType: types.StringValue("company"),
		ScopeID:   types.StringValue("comp-wrong"),
		Amount:    types.Int64Value(100),
	}
	planBadScope := tfsdk.Plan{Schema: schema}
	_ = planBadScope.Set(ctx, &planModelBadScope)
	var createBadScope resource.CreateResponse
	createBadScope.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: planBadScope}, &createBadScope)
	if !createBadScope.Diagnostics.HasError() {
		t.Fatal("expected error on invalid scope in Create")
	}

	// Duplicate identity error in Create
	var createDup resource.CreateResponse
	createDup.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: planDup}, &createDup)
	if !createDup.Diagnostics.HasError() {
		t.Fatal("expected duplicate identity error in Create")
	}

	// Upsert failure in Create
	failUpsert = true
	planModelValid := PaperclipBudgetPolicyResourceModel{
		CompanyID:   types.StringValue("comp-1"),
		ScopeType:   types.StringValue("agent"),
		ScopeID:     types.StringValue("agent-1"),
		WindowKind:  types.StringValue("calendar_month_utc"),
		Amount:      types.Int64Value(5000),
		WarnPercent: types.Int64Value(80),
	}
	planValid := tfsdk.Plan{Schema: schema}
	_ = planValid.Set(ctx, &planModelValid)
	var createUpsertFail resource.CreateResponse
	createUpsertFail.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: planValid}, &createUpsertFail)
	if !createUpsertFail.Diagnostics.HasError() {
		t.Fatal("expected error on failed upsert in Create")
	}
	failUpsert = false

	// Create success
	var createSuccess resource.CreateResponse
	createSuccess.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: planValid}, &createSuccess)
	if createSuccess.Diagnostics.HasError() {
		t.Fatalf("Create failed: %v", createSuccess.Diagnostics)
	}
	var createdState PaperclipBudgetPolicyResourceModel
	_ = createSuccess.State.Get(ctx, &createdState)
	if createdState.ID.ValueString() != "pol-upserted" {
		t.Fatalf("unexpected created policy ID: %s", createdState.ID.ValueString())
	}

	// 7. Read
	// Bad state
	badState := tfsdk.State{Schema: schema, Raw: tftypes.NewValue(tftypes.String, "invalid")}
	var readBad resource.ReadResponse
	r.Read(ctx, resource.ReadRequest{State: badState}, &readBad)
	if !readBad.Diagnostics.HasError() {
		t.Fatal("expected error on bad state in Read")
	}

	// 404 removes resource
	state404Model := PaperclipBudgetPolicyResourceModel{
		ID:        types.StringValue("missing-pol"),
		CompanyID: types.StringValue("comp-1"),
	}
	state404 := tfsdk.State{Schema: schema}
	_ = state404.Set(ctx, &state404Model)
	var read404 resource.ReadResponse
	read404.State = tfsdk.State{Schema: schema}
	r.Read(ctx, resource.ReadRequest{State: state404}, &read404)
	if read404.Diagnostics.HasError() || !read404.State.Raw.IsNull() {
		t.Fatal("expected state removed on 404 Read")
	}

	// Server error on Read
	failList = true
	var readErr resource.ReadResponse
	readErr.State = tfsdk.State{Schema: schema}
	r.Read(ctx, resource.ReadRequest{State: createSuccess.State}, &readErr)
	if !readErr.Diagnostics.HasError() {
		t.Fatal("expected diagnostic error on Read failure")
	}
	failList = false

	// Inactive policy removes resource (deactivation as retirement)
	stateInactiveModel := PaperclipBudgetPolicyResourceModel{
		ID:        types.StringValue("pol-inactive"),
		CompanyID: types.StringValue("comp-1"),
	}
	stateInactive := tfsdk.State{Schema: schema}
	_ = stateInactive.Set(ctx, &stateInactiveModel)
	var readInactive resource.ReadResponse
	readInactive.State = tfsdk.State{Schema: schema}
	r.Read(ctx, resource.ReadRequest{State: stateInactive}, &readInactive)
	if readInactive.Diagnostics.HasError() || !readInactive.State.Raw.IsNull() {
		t.Fatal("expected inactive policy to be removed from state on Read")
	}

	// Read success
	var readSuccess resource.ReadResponse
	readSuccess.State = tfsdk.State{Schema: schema}
	r.Read(ctx, resource.ReadRequest{State: createSuccess.State}, &readSuccess)
	if readSuccess.Diagnostics.HasError() {
		t.Fatalf("Read failed: %v", readSuccess.Diagnostics)
	}

	// 8. Update
	// Bad plan
	var updateBad resource.UpdateResponse
	r.Update(ctx, resource.UpdateRequest{Plan: badPlan, State: createSuccess.State}, &updateBad)
	if !updateBad.Diagnostics.HasError() {
		t.Fatal("expected error on bad plan in Update")
	}

	// Scope validation error in Update
	var updateBadScope resource.UpdateResponse
	r.Update(ctx, resource.UpdateRequest{Plan: planBadScope, State: createSuccess.State}, &updateBadScope)
	if !updateBadScope.Diagnostics.HasError() {
		t.Fatal("expected scope error in Update")
	}

	// Upsert failure in Update
	failUpsert = true
	var updateUpsertFail resource.UpdateResponse
	r.Update(ctx, resource.UpdateRequest{Plan: planValid, State: createSuccess.State}, &updateUpsertFail)
	if !updateUpsertFail.Diagnostics.HasError() {
		t.Fatal("expected error on failed upsert in Update")
	}
	failUpsert = false

	// Update success
	var updateSuccess resource.UpdateResponse
	updateSuccess.State = tfsdk.State{Schema: schema}
	r.Update(ctx, resource.UpdateRequest{Plan: planValid, State: createSuccess.State}, &updateSuccess)
	if updateSuccess.Diagnostics.HasError() {
		t.Fatalf("Update failed: %v", updateSuccess.Diagnostics)
	}

	// 9. Delete
	// Bad state
	var deleteBad resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: badState}, &deleteBad)
	if !deleteBad.Diagnostics.HasError() {
		t.Fatal("expected error on bad state in Delete")
	}

	// Upsert failure in Delete
	failUpsert = true
	var deleteUpsertFail resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: createSuccess.State}, &deleteUpsertFail)
	if !deleteUpsertFail.Diagnostics.HasError() {
		t.Fatal("expected error on failed deactivation in Delete")
	}
	failUpsert = false

	// Delete success
	var deleteSuccess resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: createSuccess.State}, &deleteSuccess)
	if deleteSuccess.Diagnostics.HasError() {
		t.Fatalf("Delete failed: %v", deleteSuccess.Diagnostics)
	}

	// 10. ImportState
	// 4 parts "comp-1/company/comp-1/calendar_month_utc" success
	var imp4Success resource.ImportStateResponse
	imp4Success.State = tfsdk.State{Schema: schema}
	_ = imp4Success.State.Set(ctx, &PaperclipBudgetPolicyResourceModel{})
	r.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/company/comp-1/calendar_month_utc"}, &imp4Success)
	if imp4Success.Diagnostics.HasError() {
		t.Fatalf("Import 4 parts failed: %v", imp4Success.Diagnostics)
	}

	// 4 parts missing
	var imp4Fail resource.ImportStateResponse
	imp4Fail.State = tfsdk.State{Schema: schema}
	_ = imp4Fail.State.Set(ctx, &PaperclipBudgetPolicyResourceModel{})
	r.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/company/comp-missing/calendar_month_utc"}, &imp4Fail)
	if !imp4Fail.Diagnostics.HasError() {
		t.Fatal("expected error on 4 parts import not found")
	}

	// 2 parts "comp-1/pol-1"
	var imp2Success resource.ImportStateResponse
	imp2Success.State = tfsdk.State{Schema: schema}
	_ = imp2Success.State.Set(ctx, &PaperclipBudgetPolicyResourceModel{})
	r.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/pol-1"}, &imp2Success)
	if imp2Success.Diagnostics.HasError() {
		t.Fatalf("Import 2 parts failed: %v", imp2Success.Diagnostics)
	}

	// 1 part found by ID
	var imp1Success resource.ImportStateResponse
	imp1Success.State = tfsdk.State{Schema: schema}
	_ = imp1Success.State.Set(ctx, &PaperclipBudgetPolicyResourceModel{})
	r.ImportState(ctx, resource.ImportStateRequest{ID: "pol-1"}, &imp1Success)
	if imp1Success.Diagnostics.HasError() {
		t.Fatalf("Import 1 part failed: %v", imp1Success.Diagnostics)
	}

	// 1 part not found -> passthrough
	var imp1Passthrough resource.ImportStateResponse
	imp1Passthrough.State = tfsdk.State{Schema: schema}
	_ = imp1Passthrough.State.Set(ctx, &PaperclipBudgetPolicyResourceModel{})
	r.ImportState(ctx, resource.ImportStateRequest{ID: "pol-missing"}, &imp1Passthrough)
	if imp1Passthrough.Diagnostics.HasError() {
		t.Fatalf("Import 1 part passthrough failed: %v", imp1Passthrough.Diagnostics)
	}

	// Invalid import ID
	var impInvalid resource.ImportStateResponse
	r.ImportState(ctx, resource.ImportStateRequest{ID: ""}, &impInvalid)
	if !impInvalid.Diagnostics.HasError() {
		t.Fatal("expected error on empty import ID")
	}
}

func TestGoalBudgetRemainingCoverageBranches(t *testing.T) {
	ctx := context.Background()

	// 1. Company Update edge cases
	// a. Empty status returned by server
	compServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPatch && r.URL.Path == "/api/companies/comp-test" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":                              "comp-test",
				"name":                            "Company No Status",
				"status":                          "",
				"requireBoardApprovalForNewAgents": false,
				"feedbackDataSharingEnabled":      false,
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer compServer.Close()

	rComp := NewPaperclipCompanyResource().(*PaperclipCompanyResource)
	compSchema := configureTestResource(t, compServer.URL, rComp)

	compPlan := tfsdk.Plan{Schema: compSchema}
	_ = compPlan.Set(ctx, &PaperclipCompanyResourceModel{
		ID:   types.StringValue("comp-test"),
		Name: types.StringValue("Company No Status"),
	})
	compState := tfsdk.State{Schema: compSchema}
	_ = compState.Set(ctx, &PaperclipCompanyResourceModel{
		ID:   types.StringValue("comp-test"),
		Name: types.StringValue("Old Company"),
	})
	var compUpdResp resource.UpdateResponse
	compUpdResp.State = compState
	rComp.Update(ctx, resource.UpdateRequest{Plan: compPlan, State: compState}, &compUpdResp)
	if compUpdResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error in comp update: %v", compUpdResp.Diagnostics)
	}
	var compOut PaperclipCompanyResourceModel
	_ = compUpdResp.State.Get(ctx, &compOut)
	if compOut.Status.ValueString() != "active" {
		t.Fatalf("expected status 'active', got %q", compOut.Status.ValueString())
	}

	// b. Invalid interaction resolver governance in Update
	compPlanInvalidGov := tfsdk.Plan{Schema: compSchema}
	_ = compPlanInvalidGov.Set(ctx, &PaperclipCompanyResourceModel{
		ID:                            types.StringValue("comp-test"),
		Name:                          types.StringValue("Invalid Gov"),
		InteractionResolverGovernance: types.StringValue("{invalid-json"),
	})
	var compUpdRespInvalidGov resource.UpdateResponse
	compUpdRespInvalidGov.State = compState
	rComp.Update(ctx, resource.UpdateRequest{Plan: compPlanInvalidGov, State: compState}, &compUpdRespInvalidGov)
	if !compUpdRespInvalidGov.Diagnostics.HasError() {
		t.Fatal("expected error on invalid governance JSON in company Update")
	}

	// c. UpdateCompany failure (server returns 500)
	compFailServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer compFailServer.Close()
	rCompFail := NewPaperclipCompanyResource().(*PaperclipCompanyResource)
	_ = configureTestResource(t, compFailServer.URL, rCompFail)
	var compUpdFailResp resource.UpdateResponse
	compUpdFailResp.State = compState
	rCompFail.Update(ctx, resource.UpdateRequest{Plan: compPlan, State: compState}, &compUpdFailResp)
	if !compUpdFailResp.Diagnostics.HasError() {
		t.Fatal("expected error on UpdateCompany server failure")
	}

	// 2. Mock server for goal and budget policy edge cases
	goalsDB := map[string]client.PaperclipGoal{
		"anc-1": {
			ID:        "anc-1",
			CompanyID: "comp-1",
			ParentID:  "anc-2",
		},
		"anc-2": {
			ID:        "anc-2",
			CompanyID: "comp-1",
			ParentID:  "anc-1",
		},
		"anc-with-missing": {
			ID:        "anc-with-missing",
			CompanyID: "comp-1",
			ParentID:  "anc-nonexistent",
		},
		"parent-valid": {
			ID:        "parent-valid",
			CompanyID: "comp-1",
		},
		"parent-chain": {
			ID:        "parent-chain",
			CompanyID: "comp-1",
			ParentID:  "anc-mid",
		},
		"anc-mid": {
			ID:        "anc-mid",
			CompanyID: "comp-1",
			ParentID:  "anc-root",
		},
		"anc-root": {
			ID:        "anc-root",
			CompanyID: "comp-1",
			ParentID:  "",
		},
		"parent-diff-comp-ancestor": {
			ID:        "parent-diff-comp-ancestor",
			CompanyID: "comp-1",
			ParentID:  "anc-diff-comp",
		},
		"anc-diff-comp": {
			ID:        "anc-diff-comp",
			CompanyID: "comp-other",
			ParentID:  "",
		},
		"goal-target": {
			ID:        "goal-target",
			CompanyID: "comp-1",
			Title:     "Target",
		},
	}
	agentsDB := map[string]client.PaperclipAgent{
		"agent-valid": {
			ID:        "agent-valid",
			CompanyID: "comp-1",
			Name:      "Valid Agent",
		},
		"agent-other": {
			ID:        "agent-other",
			CompanyID: "comp-other",
			Name:      "Other Agent",
		},
	}

	goalServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/goals/anc-1":
			_ = json.NewEncoder(w).Encode(goalsDB["anc-1"])
		case r.Method == http.MethodGet && r.URL.Path == "/api/goals/anc-2":
			_ = json.NewEncoder(w).Encode(goalsDB["anc-2"])
		case r.Method == http.MethodGet && r.URL.Path == "/api/goals/anc-with-missing":
			_ = json.NewEncoder(w).Encode(goalsDB["anc-with-missing"])
		case r.Method == http.MethodGet && r.URL.Path == "/api/goals/parent-valid":
			_ = json.NewEncoder(w).Encode(goalsDB["parent-valid"])
		case r.Method == http.MethodGet && r.URL.Path == "/api/goals/parent-chain":
			_ = json.NewEncoder(w).Encode(goalsDB["parent-chain"])
		case r.Method == http.MethodGet && r.URL.Path == "/api/goals/parent-diff-comp-ancestor":
			_ = json.NewEncoder(w).Encode(goalsDB["parent-diff-comp-ancestor"])
		case r.Method == http.MethodGet && r.URL.Path == "/api/goals/anc-diff-comp":
			_ = json.NewEncoder(w).Encode(goalsDB["anc-diff-comp"])
		case r.Method == http.MethodGet && r.URL.Path == "/api/goals/anc-mid":
			_ = json.NewEncoder(w).Encode(goalsDB["anc-mid"])
		case r.Method == http.MethodGet && r.URL.Path == "/api/goals/anc-root":
			_ = json.NewEncoder(w).Encode(goalsDB["anc-root"])
		case r.Method == http.MethodGet && r.URL.Path == "/api/goals/goal-target":
			_ = json.NewEncoder(w).Encode(goalsDB["goal-target"])
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/agent-valid":
			_ = json.NewEncoder(w).Encode(agentsDB["agent-valid"])
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/agent-other":
			_ = json.NewEncoder(w).Encode(agentsDB["agent-other"])
		case r.Method == http.MethodPatch && r.URL.Path == "/api/goals/goal-target":
			var patch map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&patch)
			g := goalsDB["goal-target"]
			if t, ok := patch["title"].(string); ok {
				g.Title = t
			}
			if d, ok := patch["description"].(string); ok {
				g.Description = d
			}
			if l, ok := patch["level"].(string); ok {
				g.Level = l
			}
			if s, ok := patch["status"].(string); ok {
				g.Status = s
			}
			if p, ok := patch["parentId"].(string); ok {
				g.ParentID = p
			}
			if o, ok := patch["ownerAgentId"].(string); ok {
				g.OwnerAgentID = o
			}
			goalsDB["goal-target"] = g
			_ = json.NewEncoder(w).Encode(g)
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/budgets/policies":
			_ = json.NewEncoder(w).Encode([]client.PaperclipBudgetPolicySummary{
				{
					PolicyID:   "pol-existing",
					CompanyID:  "comp-1",
					ScopeType:  "company",
					ScopeID:    "comp-1",
					WindowKind: "calendar_month_utc",
					Amount:     1000,
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer goalServer.Close()

	rGoal := NewPaperclipGoalResource().(*PaperclipGoalResource)
	goalSchema := configureTestResource(t, goalServer.URL, rGoal)

	rBudget := NewPaperclipBudgetPolicyResource().(*PaperclipBudgetPolicyResource)
	budgetSchema := configureTestResource(t, goalServer.URL, rBudget)

	// Test validateGoalHierarchy: cycle among ancestors when goalID == ""
	if err := rGoal.validateGoalHierarchy(ctx, "", "comp-1", "anc-1", ""); err == nil {
		t.Fatal("expected cycle error among ancestors with empty goalID")
	}

	// Test validateGoalHierarchy: ancestor error (break on ancestor not found)
	if err := rGoal.validateGoalHierarchy(ctx, "", "comp-1", "anc-with-missing", ""); err != nil {
		t.Fatalf("expected break on missing ancestor, got %v", err)
	}

	// Test validateGoalHierarchy: valid parent with empty goalID
	if err := rGoal.validateGoalHierarchy(ctx, "", "comp-1", "parent-valid", ""); err != nil {
		t.Fatalf("expected valid hierarchy for empty goalID, got %v", err)
	}

	// Test validateGoalHierarchy: multi-step valid ancestor chain that cleanly exits the loop
	if err := rGoal.validateGoalHierarchy(ctx, "", "comp-1", "parent-chain", ""); err != nil {
		t.Fatalf("expected valid multi-step ancestor chain, got %v", err)
	}

	// Test validateGoalHierarchy: ancestor belonging to different company
	if err := rGoal.validateGoalHierarchy(ctx, "", "comp-1", "parent-diff-comp-ancestor", ""); err == nil {
		t.Fatal("expected error when ancestor belongs to different company")
	}

	// Test Goal ModifyPlan:
	// a. CancelOnDestroy true with unknown Retirement
	planGoalUnknownRet := PaperclipGoalResourceModel{
		CompanyID:       types.StringValue("comp-1"),
		Title:           types.StringValue("Goal Title"),
		CancelOnDestroy: types.BoolValue(true),
		Retirement:      types.StringUnknown(),
	}
	p1 := tfsdk.Plan{Schema: goalSchema}
	_ = p1.Set(ctx, &planGoalUnknownRet)
	var respP1 resource.ModifyPlanResponse
	respP1.Plan = p1
	rGoal.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: p1}, &respP1)
	if respP1.Diagnostics.HasError() {
		t.Fatal(respP1.Diagnostics)
	}

	// b. CancelOnDestroy true with null Retirement
	planGoalNullRet := PaperclipGoalResourceModel{
		CompanyID:       types.StringValue("comp-1"),
		Title:           types.StringValue("Goal Title"),
		CancelOnDestroy: types.BoolValue(true),
		Retirement:      types.StringNull(),
	}
	p2 := tfsdk.Plan{Schema: goalSchema}
	_ = p2.Set(ctx, &planGoalNullRet)
	var respP2 resource.ModifyPlanResponse
	respP2.Plan = p2
	rGoal.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: p2}, &respP2)
	if respP2.Diagnostics.HasError() {
		t.Fatal(respP2.Diagnostics)
	}

	// c. Plan with known ID, null ParentID, known OwnerAgentID that fails hierarchy validation
	planGoalWithIDAndAgent := PaperclipGoalResourceModel{
		ID:           types.StringValue("goal-target"),
		CompanyID:    types.StringValue("comp-1"),
		Title:        types.StringValue("Goal Title"),
		OwnerAgentID: types.StringValue("agent-other"),
	}
	p3 := tfsdk.Plan{Schema: goalSchema}
	_ = p3.Set(ctx, &planGoalWithIDAndAgent)
	var respP3 resource.ModifyPlanResponse
	respP3.Plan = p3
	rGoal.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: p3}, &respP3)
	if !respP3.Diagnostics.HasError() {
		t.Fatal("expected hierarchy error for cross-company agent in ModifyPlan")
	}

	// Test Goal Update:
	// a. Plan with non-null ParentID and non-null OwnerAgentID, null Level, null Status,
	// and server returning empty Level and Status.
	updatePlanModelWithParentAndAgent := PaperclipGoalResourceModel{
		ID:           types.StringValue("goal-target"),
		CompanyID:    types.StringValue("comp-1"),
		Title:        types.StringValue("Goal Updated"),
		Level:        types.StringNull(),
		Status:       types.StringNull(),
		ParentID:     types.StringValue("parent-valid"),
		OwnerAgentID: types.StringValue("agent-valid"),
	}
	planUpd := tfsdk.Plan{Schema: goalSchema}
	_ = planUpd.Set(ctx, &updatePlanModelWithParentAndAgent)
	stateUpd := tfsdk.State{Schema: goalSchema}
	_ = stateUpd.Set(ctx, &PaperclipGoalResourceModel{
		ID:        types.StringValue("goal-target"),
		CompanyID: types.StringValue("comp-1"),
		Title:     types.StringValue("Target"),
	})
	var updRespGoal resource.UpdateResponse
	updRespGoal.State = stateUpd
	rGoal.Update(ctx, resource.UpdateRequest{Plan: planUpd, State: stateUpd}, &updRespGoal)
	if updRespGoal.Diagnostics.HasError() {
		t.Fatalf("unexpected error in goal update with parent and agent: %v", updRespGoal.Diagnostics)
	}

	// b. Plan with non-null Description, Level, and Status, and server returning them
	updatePlanModelFullFields := PaperclipGoalResourceModel{
		ID:           types.StringValue("goal-target"),
		CompanyID:    types.StringValue("comp-1"),
		Title:        types.StringValue("Goal Updated Full"),
		Description:  types.StringValue("Updated Description"),
		Level:        types.StringValue("task"),
		Status:       types.StringValue("in_progress"),
		ParentID:     types.StringValue("parent-valid"),
		OwnerAgentID: types.StringValue("agent-valid"),
	}
	planUpdFull := tfsdk.Plan{Schema: goalSchema}
	_ = planUpdFull.Set(ctx, &updatePlanModelFullFields)
	var updRespGoalFull resource.UpdateResponse
	updRespGoalFull.State = stateUpd
	rGoal.Update(ctx, resource.UpdateRequest{Plan: planUpdFull, State: stateUpd}, &updRespGoalFull)
	if updRespGoalFull.Diagnostics.HasError() {
		t.Fatalf("unexpected error in goal full update: %v", updRespGoalFull.Diagnostics)
	}
	var goalFullOut PaperclipGoalResourceModel
	_ = updRespGoalFull.State.Get(ctx, &goalFullOut)
	if goalFullOut.Level.ValueString() != "task" || goalFullOut.Status.ValueString() != "in_progress" {
		t.Fatalf("unexpected goal update full fields: %+v", goalFullOut)
	}

	// Test Budget Policy ModifyPlan:
	// a. Known ID matching existing policy (so checkDuplicatePolicyIdentity passes because curID == existing.PolicyID)
	planBudgetWithID := PaperclipBudgetPolicyResourceModel{
		ID:          types.StringValue("pol-existing"),
		CompanyID:   types.StringValue("comp-1"),
		ScopeType:   types.StringValue("company"),
		ScopeID:     types.StringValue("comp-1"),
		WindowKind:  types.StringValue("calendar_month_utc"),
		Amount:      types.Int64Value(2000),
		WarnPercent: types.Int64Value(85),
	}
	bp1 := tfsdk.Plan{Schema: budgetSchema}
	_ = bp1.Set(ctx, &planBudgetWithID)
	var respBP1 resource.ModifyPlanResponse
	respBP1.Plan = bp1
	rBudget.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: bp1}, &respBP1)
	if respBP1.Diagnostics.HasError() {
		t.Fatalf("unexpected error in budget ModifyPlan with matching ID: %v", respBP1.Diagnostics)
	}

	// b. Unknown ScopeType
	planBudgetUnknownScope := PaperclipBudgetPolicyResourceModel{
		CompanyID:   types.StringValue("comp-1"),
		ScopeType:   types.StringUnknown(),
		ScopeID:     types.StringValue("comp-1"),
		WindowKind:  types.StringValue("calendar_month_utc"),
		Amount:      types.Int64Value(2000),
		WarnPercent: types.Int64Value(85),
	}
	bp2 := tfsdk.Plan{Schema: budgetSchema}
	_ = bp2.Set(ctx, &planBudgetUnknownScope)
	var respBP2 resource.ModifyPlanResponse
	respBP2.Plan = bp2
	rBudget.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: bp2}, &respBP2)
	if respBP2.Diagnostics.HasError() {
		t.Fatalf("unexpected error in budget ModifyPlan with unknown scope: %v", respBP2.Diagnostics)
	}

	// c. Unknown ScopeID
	planBudgetUnknownScopeID := PaperclipBudgetPolicyResourceModel{
		CompanyID:   types.StringValue("comp-1"),
		ScopeType:   types.StringValue("company"),
		ScopeID:     types.StringUnknown(),
		WindowKind:  types.StringValue("calendar_month_utc"),
		Amount:      types.Int64Value(2000),
		WarnPercent: types.Int64Value(85),
	}
	bp3 := tfsdk.Plan{Schema: budgetSchema}
	_ = bp3.Set(ctx, &planBudgetUnknownScopeID)
	var respBP3 resource.ModifyPlanResponse
	respBP3.Plan = bp3
	rBudget.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: bp3}, &respBP3)
	if respBP3.Diagnostics.HasError() {
		t.Fatalf("unexpected error in budget ModifyPlan with unknown scope ID: %v", respBP3.Diagnostics)
	}

	// d. Unknown WindowKind
	planBudgetUnknownWindow := PaperclipBudgetPolicyResourceModel{
		CompanyID:   types.StringValue("comp-1"),
		ScopeType:   types.StringValue("company"),
		ScopeID:     types.StringValue("comp-1"),
		WindowKind:  types.StringUnknown(),
		Amount:      types.Int64Value(2000),
		WarnPercent: types.Int64Value(85),
	}
	bp4 := tfsdk.Plan{Schema: budgetSchema}
	_ = bp4.Set(ctx, &planBudgetUnknownWindow)
	var respBP4 resource.ModifyPlanResponse
	respBP4.Plan = bp4
	rBudget.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: bp4}, &respBP4)
	if respBP4.Diagnostics.HasError() {
		t.Fatalf("unexpected error in budget ModifyPlan with unknown window: %v", respBP4.Diagnostics)
	}
}

func TestCompanyUpdateNullBoth(t *testing.T) {
	ctx := context.Background()
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPatch && strings.HasPrefix(r.URL.Path, "/api/companies/") {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":   "comp-1",
				"name": "Updated Corp",
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	serverURL = server.URL

	rComp := NewPaperclipCompanyResource().(*PaperclipCompanyResource)
	compSchema := configureTestResource(t, serverURL, rComp)

	stateModel := PaperclipCompanyResourceModel{
		ID:                       types.StringValue("comp-1"),
		Name:                     types.StringValue("Corp"),
		DefaultResponsibleUserID: types.StringNull(),
		LogoAssetID:              types.StringNull(),
	}
	planModel := PaperclipCompanyResourceModel{
		ID:                       types.StringValue("comp-1"),
		Name:                     types.StringValue("Updated Corp"),
		DefaultResponsibleUserID: types.StringNull(),
		LogoAssetID:              types.StringNull(),
	}

	state := tfsdk.State{Schema: compSchema}
	_ = state.Set(ctx, &stateModel)
	plan := tfsdk.Plan{Schema: compSchema}
	_ = plan.Set(ctx, &planModel)

	var updateResp resource.UpdateResponse
	updateResp.State = tfsdk.State{Schema: compSchema}
	rComp.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &updateResp)
	if updateResp.Diagnostics.HasError() {
		t.Fatalf("Update failed: %v", updateResp.Diagnostics)
	}
}

