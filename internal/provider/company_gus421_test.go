package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestCompanyGUS421ResourceFullLifecycle(t *testing.T) {
	ctx := context.Background()

	var (
		lastCreateBody map[string]interface{}
		lastPatchBody  map[string]interface{}
		archiveCalls   int
		deleteCalls    int
		failServer     bool
		companyStatus  = "active"
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if failServer {
			http.Error(w, `{"error":"server error"}`, http.StatusInternalServerError)
			return
		}

		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/api/companies":
			_ = json.NewDecoder(req.Body).Decode(&lastCreateBody)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":                       "comp-gus421",
				"name":                     lastCreateBody["name"],
				"description":              lastCreateBody["description"],
				"status":                   "active",
				"defaultResponsibleUserId": lastCreateBody["defaultResponsibleUserId"],
			})

		case req.Method == http.MethodPatch && req.URL.Path == "/api/companies/comp-gus421":
			_ = json.NewDecoder(req.Body).Decode(&lastPatchBody)
			if st, ok := lastPatchBody["status"].(string); ok {
				companyStatus = st
			}
			resp := map[string]interface{}{
				"id":          "comp-gus421",
				"name":        "Acme Corp",
				"description": "Initial description",
				"status":      companyStatus,
			}
			for k, v := range lastPatchBody {
				resp[k] = v
			}
			_ = json.NewEncoder(w).Encode(resp)

		case req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-gus421":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":                               "comp-gus421",
				"name":                             "Acme Corp",
				"description":                      "Initial description",
				"status":                           companyStatus,
				"defaultResponsibleUserId":         "user-1",
				"requireBoardApprovalForNewAgents": true,
				"interactionResolverGovernance": map[string]interface{}{
					"suggest_tasks": map[string]interface{}{
						"defaultPolicy": "board_only",
						"cap":           "board_only",
					},
				},
				"feedbackDataSharingEnabled": true,
				"logoAssetId":                "asset-123",
			})

		case req.Method == http.MethodPost && req.URL.Path == "/api/companies/comp-gus421/archive":
			archiveCalls++
			w.WriteHeader(http.StatusNoContent)

		case req.Method == http.MethodDelete && req.URL.Path == "/api/companies/comp-gus421":
			deleteCalls++
			w.WriteHeader(http.StatusNoContent)

		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	r := NewPaperclipCompanyResource().(*PaperclipCompanyResource)
	schema := configureTestResource(t, server.URL, r)

	// 1. Create with full fields (non-default status, board approval, governance, etc.)
	planModel := PaperclipCompanyResourceModel{
		Name:                             types.StringValue("Acme Corp"),
		Description:                      types.StringValue("Initial description"),
		Status:                           types.StringValue("paused"),
		DefaultResponsibleUserID:         types.StringValue("user-1"),
		RequireBoardApprovalForNewAgents: types.BoolValue(true),
		InteractionResolverGovernance:    types.StringValue(`{"suggest_tasks":{"defaultPolicy":"board_only","cap":"board_only"}}`),
		FeedbackDataSharingEnabled:       types.BoolValue(true),
		LogoAssetID:                      types.StringValue("asset-123"),
		ArchiveOnDestroy:                 types.BoolValue(true),
	}

	plan := tfsdk.Plan{Schema: schema}
	if diags := plan.Set(ctx, &planModel); diags.HasError() {
		t.Fatal(diags)
	}

	created := resource.CreateResponse{State: tfsdk.State{Schema: schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
	if created.Diagnostics.HasError() {
		t.Fatalf("Create failed: %v", created.Diagnostics)
	}

	// Verify post-create patch was executed
	if lastPatchBody == nil {
		t.Fatal("expected post-create patch for non-default company attributes")
	}
	if lastPatchBody["status"] != "paused" {
		t.Fatalf("expected patch status paused, got %v", lastPatchBody["status"])
	}
	if lastPatchBody["requireBoardApprovalForNewAgents"] != true {
		t.Fatalf("expected patch requireBoardApprovalForNewAgents true")
	}
	if lastPatchBody["feedbackDataSharingEnabled"] != true {
		t.Fatalf("expected patch feedbackDataSharingEnabled true")
	}
	if lastPatchBody["logoAssetId"] != "asset-123" {
		t.Fatalf("expected patch logoAssetId asset-123")
	}

	var createdState PaperclipCompanyResourceModel
	if diags := created.State.Get(ctx, &createdState); diags.HasError() {
		t.Fatal(diags)
	}
	if createdState.ID.ValueString() != "comp-gus421" {
		t.Fatalf("unexpected company ID: %s", createdState.ID.ValueString())
	}
	if createdState.Status.ValueString() != "paused" {
		t.Fatalf("unexpected status: %s", createdState.Status.ValueString())
	}

	// 2. Read
	readResp := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("Read failed: %v", readResp.Diagnostics)
	}

	var readState PaperclipCompanyResourceModel
	if diags := readResp.State.Get(ctx, &readState); diags.HasError() {
		t.Fatal(diags)
	}
	if readState.DefaultResponsibleUserID.ValueString() != "user-1" {
		t.Fatalf("expected default_responsible_user_id user-1, got %s", readState.DefaultResponsibleUserID.ValueString())
	}

	// 3. Update: modify fields and explicitly clear others to null
	updatePlanModel := PaperclipCompanyResourceModel{
		ID:                               types.StringValue("comp-gus421"),
		Name:                             types.StringValue("Acme Corp Updated"),
		Description:                      types.StringNull(), // clear to null
		Status:                           types.StringValue("archived"),
		DefaultResponsibleUserID:         types.StringNull(), // clear to null
		RequireBoardApprovalForNewAgents: types.BoolValue(false),
		InteractionResolverGovernance:    types.StringNull(), // clear to null
		FeedbackDataSharingEnabled:       types.BoolValue(false),
		LogoAssetID:                      types.StringNull(), // clear to null
		ArchiveOnDestroy:                 types.BoolValue(false),
	}

	updatePlan := tfsdk.Plan{Schema: schema}
	if diags := updatePlan.Set(ctx, &updatePlanModel); diags.HasError() {
		t.Fatal(diags)
	}

	updateResp := resource.UpdateResponse{State: created.State}
	r.Update(ctx, resource.UpdateRequest{Plan: updatePlan, State: created.State}, &updateResp)
	if updateResp.Diagnostics.HasError() {
		t.Fatalf("Update failed: %v", updateResp.Diagnostics)
	}

	// Verify patch contains explicit null clears and empty map for governance
	if lastPatchBody["description"] != nil {
		t.Fatalf("expected nil description patch, got %v", lastPatchBody["description"])
	}
	if lastPatchBody["defaultResponsibleUserId"] != nil {
		t.Fatalf("expected nil defaultResponsibleUserId patch, got %v", lastPatchBody["defaultResponsibleUserId"])
	}
	if lastPatchBody["logoAssetId"] != nil {
		t.Fatalf("expected nil logoAssetId patch, got %v", lastPatchBody["logoAssetId"])
	}
	govMap, ok := lastPatchBody["interactionResolverGovernance"].(map[string]interface{})
	if !ok || len(govMap) != 0 {
		t.Fatalf("expected empty map for cleared governance, got %#v", lastPatchBody["interactionResolverGovernance"])
	}
	if lastPatchBody["status"] != "archived" {
		t.Fatalf("expected status archived, got %v", lastPatchBody["status"])
	}

	// 4. Delete with archive_on_destroy = false -> invokes DELETE /api/companies/:id
	deleteResp := resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: updateResp.State}, &deleteResp)
	if deleteResp.Diagnostics.HasError() {
		t.Fatalf("Delete failed: %v", deleteResp.Diagnostics)
	}
	if deleteCalls != 1 || archiveCalls != 0 {
		t.Fatalf("expected 1 delete call and 0 archive calls, got delete=%d archive=%d", deleteCalls, archiveCalls)
	}

	// 5. Delete with archive_on_destroy = true -> invokes POST /api/companies/:id/archive
	archivePlanModel := PaperclipCompanyResourceModel{
		ID:               types.StringValue("comp-gus421"),
		Name:             types.StringValue("Acme Corp"),
		ArchiveOnDestroy: types.BoolValue(true),
	}
	archiveState := tfsdk.State{Schema: schema}
	if diags := archiveState.Set(ctx, &archivePlanModel); diags.HasError() {
		t.Fatal(diags)
	}
	r.Delete(ctx, resource.DeleteRequest{State: archiveState}, &deleteResp)
	if deleteResp.Diagnostics.HasError() {
		t.Fatalf("Delete with archive failed: %v", deleteResp.Diagnostics)
	}
	if archiveCalls != 1 {
		t.Fatalf("expected 1 archive call, got %d", archiveCalls)
	}

	// 6. Delete error handling
	failServer = true
	r.Delete(ctx, resource.DeleteRequest{State: archiveState}, &deleteResp)
	if !deleteResp.Diagnostics.HasError() {
		t.Fatal("expected error on failed archive")
	}

	var delErrResp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: updateResp.State}, &delErrResp)
	if !delErrResp.Diagnostics.HasError() {
		t.Fatal("expected error on failed delete")
	}
}

func TestCompanyGUS421ResourceErrorBranches(t *testing.T) {
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/api/companies":
			// returns created company
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":   "comp-err",
				"name": "Error Company",
			})
		case req.Method == http.MethodPatch && req.URL.Path == "/api/companies/comp-err":
			// patch fails
			http.Error(w, `{"error":"patch failed"}`, http.StatusInternalServerError)
		case req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-404":
			http.NotFound(w, req)
		case req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-500":
			http.Error(w, `{"error":"server error"}`, http.StatusInternalServerError)
		case req.Method == http.MethodDelete && req.URL.Path == "/api/companies/comp-404":
			http.NotFound(w, req)
		case req.Method == http.MethodPost && req.URL.Path == "/api/companies/comp-404/archive":
			http.NotFound(w, req)
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	r := NewPaperclipCompanyResource().(*PaperclipCompanyResource)
	schema := configureTestResource(t, server.URL, r)

	// Create with needsUpdate that fails in UpdateCompany
	planModel := PaperclipCompanyResourceModel{
		Name:                             types.StringValue("Error Company"),
		RequireBoardApprovalForNewAgents: types.BoolValue(true),
	}
	plan := tfsdk.Plan{Schema: schema}
	_ = plan.Set(ctx, &planModel)

	var createResp resource.CreateResponse
	createResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &createResp)
	if !createResp.Diagnostics.HasError() {
		t.Fatal("expected error when post-create UpdateCompany fails")
	}

	// Create with invalid JSON in governance (bypassing plan validator)
	invalidGovPlanModel := PaperclipCompanyResourceModel{
		Name:                          types.StringValue("Invalid Gov"),
		InteractionResolverGovernance: types.StringValue("{invalid-json"),
	}
	invalidGovPlan := tfsdk.Plan{Schema: schema}
	_ = invalidGovPlan.Set(ctx, &invalidGovPlanModel)
	var invalidGovResp resource.CreateResponse
	invalidGovResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: invalidGovPlan}, &invalidGovResp)
	if !invalidGovResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid governance JSON during create")
	}

	// Update with invalid JSON in governance
	stateModel := PaperclipCompanyResourceModel{
		ID:   types.StringValue("comp-err"),
		Name: types.StringValue("Error Company"),
	}
	state := tfsdk.State{Schema: schema}
	_ = state.Set(ctx, &stateModel)
	var updateInvalidGovResp resource.UpdateResponse
	r.Update(ctx, resource.UpdateRequest{Plan: invalidGovPlan, State: state}, &updateInvalidGovResp)
	if !updateInvalidGovResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid governance JSON during update")
	}

	// Update fails in UpdateCompany (comp-err returns 500 on PATCH)
	var updateErrResp resource.UpdateResponse
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &updateErrResp)
	if !updateErrResp.Diagnostics.HasError() {
		t.Fatal("expected error on failed UpdateCompany during update")
	}

	// Read 404 removes resource
	state404Model := PaperclipCompanyResourceModel{
		ID: types.StringValue("comp-404"),
	}
	state404 := tfsdk.State{Schema: schema}
	_ = state404.Set(ctx, &state404Model)
	read404Resp := resource.ReadResponse{State: tfsdk.State{Schema: schema}}
	r.Read(ctx, resource.ReadRequest{State: state404}, &read404Resp)
	if read404Resp.Diagnostics.HasError() || !read404Resp.State.Raw.IsNull() {
		t.Fatal("expected state to be removed on 404 read")
	}

	// Read 500 reports diagnostic error
	state500Model := PaperclipCompanyResourceModel{
		ID: types.StringValue("comp-500"),
	}
	state500 := tfsdk.State{Schema: schema}
	_ = state500.Set(ctx, &state500Model)
	read500Resp := resource.ReadResponse{State: tfsdk.State{Schema: schema}}
	r.Read(ctx, resource.ReadRequest{State: state500}, &read500Resp)
	if !read500Resp.Diagnostics.HasError() {
		t.Fatal("expected diagnostic error on 500 read")
	}

	// Delete 404 does not error (idempotent delete)
	var del404ArchiveResp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: state404}, &del404ArchiveResp)
	if del404ArchiveResp.Diagnostics.HasError() {
		t.Fatalf("expected 404 archive delete to be ignored, got %v", del404ArchiveResp.Diagnostics)
	}

	state404Hard := PaperclipCompanyResourceModel{
		ID:               types.StringValue("comp-404"),
		ArchiveOnDestroy: types.BoolValue(false),
	}
	state404HardState := tfsdk.State{Schema: schema}
	_ = state404HardState.Set(ctx, &state404Hard)
	var del404HardResp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: state404HardState}, &del404HardResp)
	if del404HardResp.Diagnostics.HasError() {
		t.Fatalf("expected 404 hard delete to be ignored, got %v", del404HardResp.Diagnostics)
	}

	// ImportState validation
	var importEmptyResp resource.ImportStateResponse
	r.ImportState(ctx, resource.ImportStateRequest{ID: ""}, &importEmptyResp)
	if !importEmptyResp.Diagnostics.HasError() {
		t.Fatal("expected error on empty import ID")
	}
}

func TestCompanyGUS421DataSourceAllFieldsAndErrors(t *testing.T) {
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-ds":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":                               "comp-ds",
				"name":                             "Data Company",
				"description":                      "Description of Data Company",
				"status":                           "paused",
				"defaultResponsibleUserId":         "user-responsible",
				"requireBoardApprovalForNewAgents": true,
				"interactionResolverGovernance": map[string]interface{}{
					"request_confirmation": map[string]interface{}{
						"defaultPolicy": "human_only",
					},
				},
				"feedbackDataSharingEnabled": true,
				"logoAssetId":                "asset-logo-99",
			})
		case req.Method == http.MethodGet && req.URL.Path == "/api/companies":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{
					"id":                               "comp-ds",
					"name":                             "Data Company",
					"description":                      "Description of Data Company",
					"status":                           "paused",
					"defaultResponsibleUserId":         "user-responsible",
					"requireBoardApprovalForNewAgents": true,
					"interactionResolverGovernance": map[string]interface{}{
						"request_confirmation": map[string]interface{}{
							"defaultPolicy": "human_only",
						},
					},
					"feedbackDataSharingEnabled": true,
					"logoAssetId":                "asset-logo-99",
				},
			})
		case req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-err":
			http.Error(w, `{"error":"server error"}`, http.StatusInternalServerError)
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	ds := NewPaperclipCompanyDataSource().(*PaperclipCompanyDataSource)
	dsSchema := configureTestDataSource(t, server.URL, ds)

	// Configure edge case
	var confResp datasource.ConfigureResponse
	ds.Configure(ctx, datasource.ConfigureRequest{ProviderData: "invalid"}, &confResp)
	if !confResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid datasource configure")
	}

	// 1. Read by ID with full fields populated
	idModel := PaperclipCompanyDataSourceModel{
		ID: types.StringValue("comp-ds"),
	}
	idState := tfsdk.State{Schema: dsSchema}
	_ = idState.Set(ctx, &idModel)
	idConfig := tfsdk.Config{Schema: dsSchema, Raw: idState.Raw}
	var readIDResp datasource.ReadResponse
	readIDResp.State = tfsdk.State{Schema: dsSchema}
	ds.Read(ctx, datasource.ReadRequest{Config: idConfig}, &readIDResp)
	if readIDResp.Diagnostics.HasError() {
		t.Fatalf("Read by ID failed: %v", readIDResp.Diagnostics)
	}

	var resState PaperclipCompanyDataSourceModel
	_ = readIDResp.State.Get(ctx, &resState)
	if resState.Name.ValueString() != "Data Company" {
		t.Fatalf("expected name Data Company, got %s", resState.Name.ValueString())
	}
	if resState.Status.ValueString() != "paused" {
		t.Fatalf("expected status paused, got %s", resState.Status.ValueString())
	}
	if resState.DefaultResponsibleUserID.ValueString() != "user-responsible" {
		t.Fatalf("expected default_responsible_user_id user-responsible, got %s", resState.DefaultResponsibleUserID.ValueString())
	}
	if !resState.RequireBoardApprovalForNewAgents.ValueBool() {
		t.Fatal("expected require_board_approval_for_new_agents true")
	}
	if !resState.FeedbackDataSharingEnabled.ValueBool() {
		t.Fatal("expected feedback_data_sharing_enabled true")
	}
	if resState.LogoAssetID.ValueString() != "asset-logo-99" {
		t.Fatalf("expected logo_asset_id asset-logo-99, got %s", resState.LogoAssetID.ValueString())
	}

	// 2. Read by Name
	nameModel := PaperclipCompanyDataSourceModel{
		Name: types.StringValue("Data Company"),
	}
	nameState := tfsdk.State{Schema: dsSchema}
	_ = nameState.Set(ctx, &nameModel)
	nameConfig := tfsdk.Config{Schema: dsSchema, Raw: nameState.Raw}
	var readNameResp datasource.ReadResponse
	readNameResp.State = tfsdk.State{Schema: dsSchema}
	ds.Read(ctx, datasource.ReadRequest{Config: nameConfig}, &readNameResp)
	if readNameResp.Diagnostics.HasError() {
		t.Fatalf("Read by Name failed: %v", readNameResp.Diagnostics)
	}

	// 3. Name mismatch when both ID and Name provided
	mismatchModel := PaperclipCompanyDataSourceModel{
		ID:   types.StringValue("comp-ds"),
		Name: types.StringValue("Different Name"),
	}
	mismatchState := tfsdk.State{Schema: dsSchema}
	_ = mismatchState.Set(ctx, &mismatchModel)
	mismatchConfig := tfsdk.Config{Schema: dsSchema, Raw: mismatchState.Raw}
	var mismatchResp datasource.ReadResponse
	mismatchResp.State = tfsdk.State{Schema: dsSchema}
	ds.Read(ctx, datasource.ReadRequest{Config: mismatchConfig}, &mismatchResp)
	if !mismatchResp.Diagnostics.HasError() {
		t.Fatal("expected error on name mismatch")
	}

	// 4. Server error on read by ID
	errModel := PaperclipCompanyDataSourceModel{
		ID: types.StringValue("comp-err"),
	}
	errState := tfsdk.State{Schema: dsSchema}
	_ = errState.Set(ctx, &errModel)
	errConfig := tfsdk.Config{Schema: dsSchema, Raw: errState.Raw}
	var errResp datasource.ReadResponse
	errResp.State = tfsdk.State{Schema: dsSchema}
	ds.Read(ctx, datasource.ReadRequest{Config: errConfig}, &errResp)
	if !errResp.Diagnostics.HasError() {
		t.Fatal("expected error on server 500")
	}

	// 5. Server error on read by Name
	errNameModel := PaperclipCompanyDataSourceModel{
		Name: types.StringValue("Missing Company"),
	}
	errNameState := tfsdk.State{Schema: dsSchema}
	_ = errNameState.Set(ctx, &errNameModel)
	errNameConfig := tfsdk.Config{Schema: dsSchema, Raw: errNameState.Raw}
	var errNameResp datasource.ReadResponse
	errNameResp.State = tfsdk.State{Schema: dsSchema}
	ds.Read(ctx, datasource.ReadRequest{Config: errNameConfig}, &errNameResp)
	if !errNameResp.Diagnostics.HasError() {
		t.Fatal("expected error on missing company by name")
	}
}

func TestCompanyGUS421StateUpgradeV0ToV1(t *testing.T) {
	ctx := context.Background()
	r := NewPaperclipCompanyResource().(*PaperclipCompanyResource)

	upgraders := r.UpgradeState(ctx)
	upgrader, ok := upgraders[0]
	if !ok {
		t.Fatal("missing v0 state upgrader")
	}

	v0Model := struct {
		ID                           types.String `tfsdk:"id"`
		Name                         types.String `tfsdk:"name"`
		Description                  types.String `tfsdk:"description"`
		RequireTaskStartConfirmation types.Bool   `tfsdk:"require_task_start_confirmation"`
	}{
		ID:                           types.StringValue("comp-legacy"),
		Name:                         types.StringValue("Legacy Corp"),
		Description:                  types.StringValue("Legacy Description"),
		RequireTaskStartConfirmation: types.BoolValue(true),
	}

	v0State := tfsdk.State{Schema: *upgrader.PriorSchema}
	if diags := v0State.Set(ctx, &v0Model); diags.HasError() {
		t.Fatal(diags)
	}

	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	var upgradeResp resource.UpgradeStateResponse
	upgradeResp.State = tfsdk.State{Schema: schemaResp.Schema}
	upgrader.StateUpgrader(ctx, resource.UpgradeStateRequest{State: &v0State}, &upgradeResp)
	if upgradeResp.Diagnostics.HasError() {
		t.Fatalf("UpgradeState failed: %v", upgradeResp.Diagnostics)
	}

	// Test error path when prior state cannot be decoded
	var errUpgradeResp resource.UpgradeStateResponse
	errUpgradeResp.State = tfsdk.State{Schema: schemaResp.Schema}
	badState := tfsdk.State{Schema: *upgrader.PriorSchema}
	upgrader.StateUpgrader(ctx, resource.UpgradeStateRequest{State: &badState}, &errUpgradeResp)
	if !errUpgradeResp.Diagnostics.HasError() {
		t.Fatal("expected error on uninitialized state upgrade")
	}

	var upgraded PaperclipCompanyResourceModel
	if diags := upgradeResp.State.Get(ctx, &upgraded); diags.HasError() {
		t.Fatal(diags)
	}

	if upgraded.ID.ValueString() != "comp-legacy" {
		t.Fatalf("expected ID comp-legacy, got %s", upgraded.ID.ValueString())
	}
	if upgraded.Name.ValueString() != "Legacy Corp" {
		t.Fatalf("expected Name Legacy Corp, got %s", upgraded.Name.ValueString())
	}
	if upgraded.Description.ValueString() != "Legacy Description" {
		t.Fatalf("expected Description Legacy Description, got %s", upgraded.Description.ValueString())
	}
	if upgraded.Status.ValueString() != "active" {
		t.Fatalf("expected default status active, got %s", upgraded.Status.ValueString())
	}
	if upgraded.RequireBoardApprovalForNewAgents.ValueBool() != false {
		t.Fatal("expected default require_board_approval_for_new_agents false")
	}
	if upgraded.FeedbackDataSharingEnabled.ValueBool() != false {
		t.Fatal("expected default feedback_data_sharing_enabled false")
	}
	if upgraded.ArchiveOnDestroy.ValueBool() != true {
		t.Fatal("expected default archive_on_destroy true")
	}
	if !upgraded.DefaultResponsibleUserID.IsNull() {
		t.Fatal("expected default_responsible_user_id null")
	}
	if !upgraded.LogoAssetID.IsNull() {
		t.Fatal("expected logo_asset_id null")
	}
	if !upgraded.InteractionResolverGovernance.IsNull() {
		t.Fatal("expected interaction_resolver_governance null")
	}
}

func TestInteractionResolverGovernanceValidatorAndParser(t *testing.T) {
	ctx := context.Background()
	val := InteractionResolverGovernanceValidator()

	// Description
	if val.Description(ctx) == "" {
		t.Fatal("expected non-empty description")
	}
	if mdVal, ok := val.(interface{ MarkdownDescription(context.Context) string }); ok {
		if mdVal.MarkdownDescription(ctx) == "" {
			t.Fatal("expected non-empty markdown description")
		}
	}

	// Valid cases
	validCases := []string{
		`{"suggest_tasks":{"defaultPolicy":"anyone"}}`,
		`{"ask_user_questions":{"defaultPolicy":"not_creator","cap":"human_only"}}`,
		`{"request_confirmation":{"defaultPolicy":"human_only"}}`,
		`{"request_checkbox_confirmation":{"defaultPolicy":"board_or_agents","cap":"anyone"}}`,
		`{"request_item_verdicts":{"defaultPolicy":"board_only"}}`,
	}

	for _, tc := range validCases {
		var resp validator.StringResponse
		val.ValidateString(ctx, validator.StringRequest{
			Path:        path.Root("interaction_resolver_governance"),
			ConfigValue: types.StringValue(tc),
		}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("expected valid for %s, got diags: %v", tc, resp.Diagnostics)
		}
	}

	// Null / Unknown / Empty
	var resp validator.StringResponse
	val.ValidateString(ctx, validator.StringRequest{
		Path:        path.Root("interaction_resolver_governance"),
		ConfigValue: types.StringNull(),
	}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal("expected no error on null")
	}

	val.ValidateString(ctx, validator.StringRequest{
		Path:        path.Root("interaction_resolver_governance"),
		ConfigValue: types.StringUnknown(),
	}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal("expected no error on unknown")
	}

	val.ValidateString(ctx, validator.StringRequest{
		Path:        path.Root("interaction_resolver_governance"),
		ConfigValue: types.StringValue(""),
	}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal("expected no error on empty string in governance validator (handled by JSON validator)")
	}

	// Invalid cases
	invalidCases := []struct {
		name string
		json string
	}{
		{"invalid kind", `{"unknown_kind":{"defaultPolicy":"anyone"}}`},
		{"non-object kind value", `{"suggest_tasks":"anyone"}`},
		{"invalid key", `{"suggest_tasks":{"invalid_key":"anyone"}}`},
		{"default_policy alias rejected", `{"suggest_tasks":{"default_policy":"anyone"}}`},
		{"invalid policy", `{"suggest_tasks":{"defaultPolicy":"superadmin"}}`},
		{"non-string policy", `{"suggest_tasks":{"defaultPolicy":123}}`},
		{"invalid cap policy", `{"suggest_tasks":{"cap":"invalid_cap"}}`},
		{"non-string cap", `{"suggest_tasks":{"cap":5}}`},
	}

	for _, tc := range invalidCases {
		t.Run(tc.name, func(t *testing.T) {
			var resp validator.StringResponse
			val.ValidateString(ctx, validator.StringRequest{
				Path:        path.Root("interaction_resolver_governance"),
				ConfigValue: types.StringValue(tc.json),
			}, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatalf("expected error for case %s: %s", tc.name, tc.json)
			}
		})
	}

	// Parser tests
	parsed, err := parseInteractionResolverGovernance(`{"suggest_tasks":{"defaultPolicy":"board_only","cap":"board_only"}}`)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}
	suggestObj, ok := parsed["suggest_tasks"].(map[string]interface{})
	if !ok {
		t.Fatal("expected suggest_tasks map")
	}
	if suggestObj["defaultPolicy"] != "board_only" {
		t.Fatalf("expected defaultPolicy to be board_only, got %v", suggestObj["defaultPolicy"])
	}
	if suggestObj["cap"] != "board_only" {
		t.Fatalf("expected cap to be board_only, got %v", suggestObj["cap"])
	}

	// Parser with empty string
	emptyParsed, err := parseInteractionResolverGovernance("")
	if err != nil || len(emptyParsed) != 0 {
		t.Fatalf("expected empty map from empty string, got %v, %v", emptyParsed, err)
	}

	// Parser with invalid JSON
	_, err = parseInteractionResolverGovernance("not-json")
	if err == nil {
		t.Fatal("expected error on invalid json")
	}

	// Parser with non-map value
	nonMapParsed, err := parseInteractionResolverGovernance(`{"suggest_tasks":"invalid"}`)
	if err != nil || nonMapParsed["suggest_tasks"] != "invalid" {
		t.Fatalf("unexpected nonMapParsed: %v, %v", nonMapParsed, err)
	}
}

func TestCompanyStatusValidator(t *testing.T) {
	ctx := context.Background()
	val := CompanyStatusValidator()

	if val.Description(ctx) == "" {
		t.Fatal("expected non-empty description")
	}
	if mdVal, ok := val.(interface{ MarkdownDescription(context.Context) string }); ok {
		if mdVal.MarkdownDescription(ctx) == "" {
			t.Fatal("expected non-empty markdown description")
		}
	}

	validStatuses := []string{"active", "paused", "archived"}
	for _, status := range validStatuses {
		var resp validator.StringResponse
		val.ValidateString(ctx, validator.StringRequest{
			Path:        path.Root("status"),
			ConfigValue: types.StringValue(status),
		}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("expected status %s to be valid, got %v", status, resp.Diagnostics)
		}
	}

	// Null and Unknown
	var resp validator.StringResponse
	val.ValidateString(ctx, validator.StringRequest{
		Path:        path.Root("status"),
		ConfigValue: types.StringNull(),
	}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal("expected null status to be valid")
	}

	val.ValidateString(ctx, validator.StringRequest{
		Path:        path.Root("status"),
		ConfigValue: types.StringUnknown(),
	}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal("expected unknown status to be valid")
	}

	// Invalid status
	val.ValidateString(ctx, validator.StringRequest{
		Path:        path.Root("status"),
		ConfigValue: types.StringValue("invalid_status"),
	}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid status")
	}
}

func TestCompanyGUS421RemainingCoverage(t *testing.T) {
	ctx := context.Background()

	// 1. json_helpers.go:156 ValidateString with invalid JSON
	govVal := InteractionResolverGovernanceValidator()
	var valResp validator.StringResponse
	govVal.ValidateString(ctx, validator.StringRequest{
		Path:        path.Root("interaction_resolver_governance"),
		ConfigValue: types.StringValue("{invalid-json"),
	}, &valResp)
	if valResp.Diagnostics.HasError() {
		t.Fatal("expected no validator diagnostics for unmarshal error branch in governance validator")
	}

	// 2. Company Read with empty status and null ArchiveOnDestroy in state
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-empty-status":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":     "comp-empty-status",
				"name":   "No Status Co",
				"status": "",
			})
		case req.Method == http.MethodPatch && req.URL.Path == "/api/companies/comp-empty-status":
			var patch map[string]interface{}
			_ = json.NewDecoder(req.Body).Decode(&patch)
			resp := map[string]interface{}{
				"id":     "comp-empty-status",
				"name":   "No Status Co",
				"status": "",
			}
			for k, v := range patch {
				resp[k] = v
			}
			resp["status"] = ""
			_ = json.NewEncoder(w).Encode(resp)
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	r := NewPaperclipCompanyResource().(*PaperclipCompanyResource)
	schema := configureTestResource(t, server.URL, r)

	// Read: state has ArchiveOnDestroy = Null, remote returns Status = ""
	readStateModel := PaperclipCompanyResourceModel{
		ID:               types.StringValue("comp-empty-status"),
		ArchiveOnDestroy: types.BoolNull(),
	}
	readState := tfsdk.State{Schema: schema}
	if diags := readState.Set(ctx, &readStateModel); diags.HasError() {
		t.Fatal(diags)
	}
	readResp := resource.ReadResponse{State: tfsdk.State{Schema: schema}}
	r.Read(ctx, resource.ReadRequest{State: readState}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("Read failed: %v", readResp.Diagnostics)
	}
	var outReadState PaperclipCompanyResourceModel
	if diags := readResp.State.Get(ctx, &outReadState); diags.HasError() {
		t.Fatal(diags)
	}
	if outReadState.Status.ValueString() != "active" {
		t.Fatalf("expected fallback active status, got %s", outReadState.Status.ValueString())
	}
	if !outReadState.ArchiveOnDestroy.ValueBool() {
		t.Fatal("expected default archive_on_destroy to be true")
	}

	// Update: plan has non-null description, responsible user, logo, governance, null ArchiveOnDestroy,
	// and remote returns Status = ""
	updatePlanModel := PaperclipCompanyResourceModel{
		ID:                               types.StringValue("comp-empty-status"),
		Name:                             types.StringValue("No Status Co Updated"),
		Description:                      types.StringValue("New Description"),
		DefaultResponsibleUserID:         types.StringValue("user-responsible-421"),
		RequireBoardApprovalForNewAgents: types.BoolValue(true),
		InteractionResolverGovernance:    types.StringValue(`{"suggest_tasks":{"defaultPolicy":"anyone"}}`),
		FeedbackDataSharingEnabled:       types.BoolValue(true),
		LogoAssetID:                      types.StringValue("logo-asset-421"),
		ArchiveOnDestroy:                 types.BoolNull(),
	}
	updatePlan := tfsdk.Plan{Schema: schema}
	if diags := updatePlan.Set(ctx, &updatePlanModel); diags.HasError() {
		t.Fatal(diags)
	}
	updateResp := resource.UpdateResponse{State: readResp.State}
	r.Update(ctx, resource.UpdateRequest{Plan: updatePlan, State: readResp.State}, &updateResp)
	if updateResp.Diagnostics.HasError() {
		t.Fatalf("Update failed: %v", updateResp.Diagnostics)
	}
	var outUpdateState PaperclipCompanyResourceModel
	if diags := updateResp.State.Get(ctx, &outUpdateState); diags.HasError() {
		t.Fatal(diags)
	}
	if outUpdateState.Status.ValueString() != "active" {
		t.Fatalf("expected fallback active status on update, got %s", outUpdateState.Status.ValueString())
	}
	if !outUpdateState.ArchiveOnDestroy.ValueBool() {
		t.Fatal("expected default archive_on_destroy to be true on update")
	}
	if outUpdateState.Description.ValueString() != "New Description" {
		t.Fatalf("expected updated description, got %s", outUpdateState.Description.ValueString())
	}
	if outUpdateState.DefaultResponsibleUserID.ValueString() != "user-responsible-421" {
		t.Fatalf("expected updated responsible user, got %s", outUpdateState.DefaultResponsibleUserID.ValueString())
	}
	if outUpdateState.LogoAssetID.ValueString() != "logo-asset-421" {
		t.Fatalf("expected updated logo asset ID, got %s", outUpdateState.LogoAssetID.ValueString())
	}

	// 3. Agent instructions error and empty instructions coverage during agent Read
	var agentInstrStatus int = http.StatusInternalServerError
	var agentInstrContent string = ""
	agentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/agents/agent-cov-test":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":          "agent-cov-test",
				"companyId":   "comp-1",
				"name":        "Coverage Agent",
				"role":        "tester",
				"adapterType": "antigravity",
				"permissions": map[string]interface{}{"canAssignTasks": false, "canCreateAgents": false},
			})
		case req.Method == http.MethodGet && strings.HasPrefix(req.URL.Path, "/api/agents/agent-cov-test/instructions-bundle/file"):
			if agentInstrStatus != http.StatusOK {
				http.Error(w, "internal server error", agentInstrStatus)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"content": agentInstrContent})
		default:
			http.NotFound(w, req)
		}
	}))
	defer agentServer.Close()

	rAgent := NewPaperclipAgentResource().(*PaperclipAgentResource)
	agentSchema := configureTestResource(t, agentServer.URL, rAgent)

	agentStateModel := PaperclipAgentResourceModel{
		ID:        types.StringValue("agent-cov-test"),
		CompanyID: types.StringValue("comp-1"),
	}
	agentState := tfsdk.State{Schema: agentSchema}
	if diags := agentState.Set(ctx, &agentStateModel); diags.HasError() {
		t.Fatal(diags)
	}

	// Error path (500 from instructions endpoint)
	var agentErrReadResp resource.ReadResponse
	agentErrReadResp.State = tfsdk.State{Schema: agentSchema}
	rAgent.Read(ctx, resource.ReadRequest{State: agentState}, &agentErrReadResp)
	if !agentErrReadResp.Diagnostics.HasError() {
		t.Fatal("expected error reading agent instructions on 500")
	}

	// Empty instructions path (200 with empty content)
	agentInstrStatus = http.StatusOK
	agentInstrContent = ""
	var agentEmptyReadResp resource.ReadResponse
	agentEmptyReadResp.State = tfsdk.State{Schema: agentSchema}
	rAgent.Read(ctx, resource.ReadRequest{State: agentState}, &agentEmptyReadResp)
	if agentEmptyReadResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on empty instructions: %v", agentEmptyReadResp.Diagnostics)
	}
	var outAgentState PaperclipAgentResourceModel
	if diags := agentEmptyReadResp.State.Get(ctx, &outAgentState); diags.HasError() {
		t.Fatal(diags)
	}
	if !outAgentState.Instructions.IsNull() {
		t.Fatalf("expected null instructions when content is empty, got %v", outAgentState.Instructions)
	}
}

func TestCompanyStatusDrift(t *testing.T) {
	ctx := context.Background()

	var currentRemoteStatus = "paused"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-drift" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":     "comp-drift",
				"name":   "Drift Corp",
				"status": currentRemoteStatus,
			})
			return
		}
		http.NotFound(w, req)
	}))
	defer server.Close()

	r := NewPaperclipCompanyResource().(*PaperclipCompanyResource)
	schema := configureTestResource(t, server.URL, r)

	// State currently active
	stateModel := PaperclipCompanyResourceModel{
		ID:     types.StringValue("comp-drift"),
		Name:   types.StringValue("Drift Corp"),
		Status: types.StringValue("active"),
	}
	state := tfsdk.State{Schema: schema}
	if diags := state.Set(ctx, &stateModel); diags.HasError() {
		t.Fatal(diags)
	}

	// 1. Remote drifted to "paused"
	readResp1 := resource.ReadResponse{State: tfsdk.State{Schema: schema}}
	r.Read(ctx, resource.ReadRequest{State: state}, &readResp1)
	if readResp1.Diagnostics.HasError() {
		t.Fatalf("Read failed: %v", readResp1.Diagnostics)
	}
	var outState1 PaperclipCompanyResourceModel
	if diags := readResp1.State.Get(ctx, &outState1); diags.HasError() {
		t.Fatal(diags)
	}
	if outState1.Status.ValueString() != "paused" {
		t.Fatalf("expected status paused, got %s", outState1.Status.ValueString())
	}

	// 2. Remote drifted to "archived"
	currentRemoteStatus = "archived"
	readResp2 := resource.ReadResponse{State: tfsdk.State{Schema: schema}}
	r.Read(ctx, resource.ReadRequest{State: readResp1.State}, &readResp2)
	if readResp2.Diagnostics.HasError() {
		t.Fatalf("Read failed: %v", readResp2.Diagnostics)
	}
	var outState2 PaperclipCompanyResourceModel
	if diags := readResp2.State.Get(ctx, &outState2); diags.HasError() {
		t.Fatal(diags)
	}
	if outState2.Status.ValueString() != "archived" {
		t.Fatalf("expected status archived, got %s", outState2.Status.ValueString())
	}
}

func TestCompanyImportZeroChangePlan(t *testing.T) {
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-zero" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":                               "comp-zero",
				"name":                             "Zero Change Corp",
				"description":                      "Zero change description",
				"status":                           "paused",
				"defaultResponsibleUserId":         "user-zero",
				"requireBoardApprovalForNewAgents": true,
				"interactionResolverGovernance": map[string]interface{}{
					"suggest_tasks": map[string]interface{}{
						"defaultPolicy": "human_only",
						"cap":           "board_only",
					},
				},
				"feedbackDataSharingEnabled": true,
				"logoAssetId":                "logo-asset-zero",
			})
			return
		}
		http.NotFound(w, req)
	}))
	defer server.Close()

	r := NewPaperclipCompanyResource().(*PaperclipCompanyResource)
	schema := configureTestResource(t, server.URL, r)

	// 1. Import
	var importResp resource.ImportStateResponse
	importResp.State = tfsdk.State{Schema: schema}
	_ = importResp.State.Set(ctx, &PaperclipCompanyResourceModel{})
	r.ImportState(ctx, resource.ImportStateRequest{ID: "comp-zero"}, &importResp)
	if importResp.Diagnostics.HasError() {
		t.Fatalf("ImportState failed: %v", importResp.Diagnostics)
	}

	// 2. Read after import
	readResp := resource.ReadResponse{State: importResp.State}
	r.Read(ctx, resource.ReadRequest{State: importResp.State}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("Read failed: %v", readResp.Diagnostics)
	}

	var state PaperclipCompanyResourceModel
	if diags := readResp.State.Get(ctx, &state); diags.HasError() {
		t.Fatal(diags)
	}

	// Verify all imported attributes match remote
	if state.ID.ValueString() != "comp-zero" {
		t.Fatalf("expected comp-zero, got %s", state.ID.ValueString())
	}
	if state.Name.ValueString() != "Zero Change Corp" {
		t.Fatalf("expected Zero Change Corp, got %s", state.Name.ValueString())
	}
	if state.Description.ValueString() != "Zero change description" {
		t.Fatalf("expected Zero change description, got %s", state.Description.ValueString())
	}
	if state.Status.ValueString() != "paused" {
		t.Fatalf("expected paused, got %s", state.Status.ValueString())
	}
	if state.DefaultResponsibleUserID.ValueString() != "user-zero" {
		t.Fatalf("expected user-zero, got %s", state.DefaultResponsibleUserID.ValueString())
	}
	if !state.RequireBoardApprovalForNewAgents.ValueBool() {
		t.Fatal("expected require_board_approval_for_new_agents true")
	}
	if !state.FeedbackDataSharingEnabled.ValueBool() {
		t.Fatal("expected feedback_data_sharing_enabled true")
	}
	if state.LogoAssetID.ValueString() != "logo-asset-zero" {
		t.Fatalf("expected logo-asset-zero, got %s", state.LogoAssetID.ValueString())
	}
	if !state.ArchiveOnDestroy.ValueBool() {
		t.Fatal("expected default archive_on_destroy true")
	}

	// 3. Plan with matching configuration (semantically equivalent governance with reversed key order)
	matchingPlanModel := PaperclipCompanyResourceModel{
		ID:                               types.StringValue("comp-zero"),
		Name:                             types.StringValue("Zero Change Corp"),
		Description:                      types.StringValue("Zero change description"),
		Status:                           types.StringValue("paused"),
		DefaultResponsibleUserID:         types.StringValue("user-zero"),
		RequireBoardApprovalForNewAgents: types.BoolValue(true),
		InteractionResolverGovernance:    types.StringValue(`{"suggest_tasks":{"cap":"board_only","defaultPolicy":"human_only"}}`),
		FeedbackDataSharingEnabled:       types.BoolValue(true),
		LogoAssetID:                      types.StringValue("logo-asset-zero"),
		ArchiveOnDestroy:                 types.BoolValue(true),
	}

	refreshedGov := refreshSemanticJSON(matchingPlanModel.InteractionResolverGovernance, map[string]interface{}{
		"suggest_tasks": map[string]interface{}{
			"defaultPolicy": "human_only",
			"cap":           "board_only",
		},
	})
	if refreshedGov.ValueString() != matchingPlanModel.InteractionResolverGovernance.ValueString() {
		t.Fatalf("expected semantic equality, got %s vs %s", refreshedGov.ValueString(), matchingPlanModel.InteractionResolverGovernance.ValueString())
	}
}


