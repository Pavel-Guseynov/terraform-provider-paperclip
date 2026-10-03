package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"git.guseynov.org/guseynov/paperclip/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestPaperclipBoardKeyResourceFullLifecycle(t *testing.T) {
	ctx := context.Background()
	expFuture := time.Now().Add(24 * time.Hour).Truncate(time.Second)

	mockState := struct {
		keys        map[string]client.PaperclipBoardKey
		revokedKeys map[string]bool
		failCreate  bool
		failRead    bool
		failDelete  bool
	}{
		keys:        make(map[string]client.PaperclipBoardKey),
		revokedKeys: make(map[string]bool),
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/board-api-keys":
			if mockState.failCreate {
				http.Error(w, "create failure", http.StatusInternalServerError)
				return
			}
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			keyID := "bk-1"
			if body["name"] == "no-exp" {
				keyID = "bk-no-exp"
			} else if body["name"] == "rotated" || body["name"] == "rotated-no-exp" {
				keyID = "bk-2"
			}
			var bkExpires *time.Time
			if body["expiresAt"] != nil && body["expiresAt"] != "" {
				bkExpires = &expFuture
			}
			bk := client.PaperclipBoardKey{
				ID:        keyID,
				Name:      body["name"].(string),
				Token:     "token-" + keyID,
				ExpiresAt: bkExpires,
			}
			mockState.keys[keyID] = bk
			_ = json.NewEncoder(w).Encode(bk)
		case r.Method == http.MethodGet && r.URL.Path == "/api/board-api-keys":
			if mockState.failRead {
				http.Error(w, "read failure", http.StatusInternalServerError)
				return
			}
			var list []client.PaperclipBoardKey
			for _, k := range mockState.keys {
				if !mockState.revokedKeys[k.ID] {
					list = append(list, k)
				}
			}
			_ = json.NewEncoder(w).Encode(list)
		case r.Method == http.MethodDelete:
			if mockState.failDelete {
				http.Error(w, "delete failure", http.StatusInternalServerError)
				return
			}
			id := r.URL.Path[len("/api/board-api-keys/"):]
			mockState.revokedKeys[id] = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	r := NewPaperclipBoardKeyResource().(*PaperclipBoardKeyResource)

	// Metadata & Schema
	var meta resource.MetadataResponse
	r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "paperclip"}, &meta)
	if meta.TypeName != "paperclip_board_key" {
		t.Fatalf("unexpected metadata typename: %s", meta.TypeName)
	}

	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	if len(schemaResp.Schema.Attributes) == 0 {
		t.Fatal("empty schema attributes")
	}

	// Configure
	var confResp resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: nil}, &confResp)
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: "invalid"}, &confResp)
	if !confResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid provider data")
	}
	confResp.Diagnostics = nil

	schema := configureTestResource(t, server.URL, r)

	// Bad plan and state
	badPlan := tfsdk.Plan{Schema: schema, Raw: tftypes.NewValue(tftypes.String, "invalid")}
	badState := tfsdk.State{Schema: schema, Raw: tftypes.NewValue(tftypes.String, "invalid")}
	var badRespCreate resource.CreateResponse
	r.Create(ctx, resource.CreateRequest{Plan: badPlan}, &badRespCreate)
	if !badRespCreate.Diagnostics.HasError() {
		t.Fatal("expected error on bad plan")
	}
	var badRespRead resource.ReadResponse
	r.Read(ctx, resource.ReadRequest{State: badState}, &badRespRead)
	if !badRespRead.Diagnostics.HasError() {
		t.Fatal("expected error on bad state")
	}
	var badRespUpdate resource.UpdateResponse
	r.Update(ctx, resource.UpdateRequest{Plan: badPlan, State: badState}, &badRespUpdate)
	if !badRespUpdate.Diagnostics.HasError() {
		t.Fatal("expected error on bad plan/state update")
	}
	var badRespDelete resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: badState}, &badRespDelete)
	if !badRespDelete.Diagnostics.HasError() {
		t.Fatal("expected error on bad state delete")
	}

	// Create - invalid expires_at
	invalidModel := PaperclipBoardKeyResourceModel{
		Name:       types.StringValue("test"),
		Generation: types.Int64Value(1),
		ExpiresAt:  types.StringValue("invalid-date"),
	}
	invPlan := tfsdk.Plan{Schema: schema}
	_ = invPlan.Set(ctx, &invalidModel)
	var invCreateResp resource.CreateResponse
	invCreateResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: invPlan}, &invCreateResp)
	if !invCreateResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid expires_at")
	}

	// Create - API failure
	mockState.failCreate = true
	validModel := PaperclipBoardKeyResourceModel{
		Name:               types.StringValue("initial"),
		Generation:         types.Int64Value(1),
		ExpiresAt:          types.StringValue(expFuture.Format(time.RFC3339)),
		RequestedCompanyID: types.StringValue("comp-1"),
	}
	validPlan := tfsdk.Plan{Schema: schema}
	_ = validPlan.Set(ctx, &validModel)
	var failCreateResp resource.CreateResponse
	failCreateResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: validPlan}, &failCreateResp)
	if !failCreateResp.Diagnostics.HasError() {
		t.Fatal("expected error on create failure")
	}
	mockState.failCreate = false

	// Create - Success
	var createResp resource.CreateResponse
	createResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: validPlan}, &createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("unexpected create error: %v", createResp.Diagnostics)
	}
	var createdModel PaperclipBoardKeyResourceModel
	_ = createResp.State.Get(ctx, &createdModel)
	if createdModel.ID.ValueString() != "bk-1" || createdModel.DeliveryState.ValueString() != "delivered" {
		t.Fatalf("unexpected created model: %+v", createdModel)
	}

	// Create - without expires_at
	noExpModel := PaperclipBoardKeyResourceModel{
		Name:       types.StringValue("no-exp"),
		Generation: types.Int64Value(1),
		ExpiresAt:  types.StringNull(),
	}
	noExpPlan := tfsdk.Plan{Schema: schema}
	_ = noExpPlan.Set(ctx, &noExpModel)
	var noExpResp resource.CreateResponse
	noExpResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: noExpPlan}, &noExpResp)
	if noExpResp.Diagnostics.HasError() {
		t.Fatalf("create without expires_at failed: %v", noExpResp.Diagnostics)
	}

	// Read - Success
	var readResp resource.ReadResponse
	readResp.State = createResp.State
	r.Read(ctx, resource.ReadRequest{State: createResp.State}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("unexpected read error: %v", readResp.Diagnostics)
	}

	// Read - ID only and nil expires_at
	mockState.keys["bk-no-exp"] = client.PaperclipBoardKey{ID: "bk-no-exp", Name: "No Exp"}
	idOnlyState := tfsdk.State{Schema: schema}
	idOnlyModel := PaperclipBoardKeyResourceModel{
		ID:    types.StringValue("bk-no-exp"),
		KeyID: types.StringNull(),
	}
	_ = idOnlyState.Set(ctx, &idOnlyModel)
	var idOnlyReadResp resource.ReadResponse
	idOnlyReadResp.State = idOnlyState
	r.Read(ctx, resource.ReadRequest{State: idOnlyState}, &idOnlyReadResp)
	if idOnlyReadResp.Diagnostics.HasError() {
		t.Fatalf("read with ID only failed: %v", idOnlyReadResp.Diagnostics)
	}

	// Read - Empty keyID removes resource
	emptyState := tfsdk.State{Schema: schema}
	emptyModel := PaperclipBoardKeyResourceModel{Name: types.StringValue("none")}
	_ = emptyState.Set(ctx, &emptyModel)
	var emptyReadResp resource.ReadResponse
	emptyReadResp.State = emptyState
	r.Read(ctx, resource.ReadRequest{State: emptyState}, &emptyReadResp)
	var emptyReadCheck PaperclipBoardKeyResourceModel
	_ = emptyReadResp.State.Get(ctx, &emptyReadCheck)
	if !emptyReadCheck.ID.IsNull() {
		t.Fatal("expected resource removal on empty keyID")
	}

	// Read - API failure
	mockState.failRead = true
	var failReadResp resource.ReadResponse
	failReadResp.State = createResp.State
	r.Read(ctx, resource.ReadRequest{State: createResp.State}, &failReadResp)
	if !failReadResp.Diagnostics.HasError() {
		t.Fatal("expected error on read failure")
	}
	mockState.failRead = false

	// Read - Not found removes resource
	mockState.revokedKeys["bk-1"] = true
	var nfReadResp resource.ReadResponse
	nfReadResp.State = createResp.State
	r.Read(ctx, resource.ReadRequest{State: createResp.State}, &nfReadResp)
	var nfCheck PaperclipBoardKeyResourceModel
	_ = nfReadResp.State.Get(ctx, &nfCheck)
	if !nfCheck.ID.IsNull() {
		t.Fatal("expected resource removal on not found")
	}
	delete(mockState.revokedKeys, "bk-1")

	// Update - Unchanged generation (no rotation)
	unchangedModel := createdModel
	unchangedModel.Name = types.StringValue("renamed-no-rot")
	unchangedPlan := tfsdk.Plan{Schema: schema}
	_ = unchangedPlan.Set(ctx, &unchangedModel)
	var unchangedUpdateResp resource.UpdateResponse
	unchangedUpdateResp.State = tfsdk.State{Schema: schema}
	r.Update(ctx, resource.UpdateRequest{Plan: unchangedPlan, State: createResp.State}, &unchangedUpdateResp)
	if unchangedUpdateResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on unchanged update: %v", unchangedUpdateResp.Diagnostics)
	}

	// Update - Changed generation: invalid date
	rotInvalidModel := createdModel
	rotInvalidModel.Generation = types.Int64Value(2)
	rotInvalidModel.ExpiresAt = types.StringValue("invalid-date")
	rotInvPlan := tfsdk.Plan{Schema: schema}
	_ = rotInvPlan.Set(ctx, &rotInvalidModel)
	var rotInvUpdateResp resource.UpdateResponse
	rotInvUpdateResp.State = tfsdk.State{Schema: schema}
	r.Update(ctx, resource.UpdateRequest{Plan: rotInvPlan, State: createResp.State}, &rotInvUpdateResp)
	if !rotInvUpdateResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid date during update")
	}

	// Update - Changed generation: create failure
	mockState.failCreate = true
	rotModel := createdModel
	rotModel.Name = types.StringValue("rotated")
	rotModel.Generation = types.Int64Value(2)
	rotPlan := tfsdk.Plan{Schema: schema}
	_ = rotPlan.Set(ctx, &rotModel)
	var failRotUpdateResp resource.UpdateResponse
	failRotUpdateResp.State = tfsdk.State{Schema: schema}
	r.Update(ctx, resource.UpdateRequest{Plan: rotPlan, State: createResp.State}, &failRotUpdateResp)
	if !failRotUpdateResp.Diagnostics.HasError() {
		t.Fatal("expected error on update mint failure")
	}
	mockState.failCreate = false

	// Update - Changed generation: Success
	var rotUpdateResp resource.UpdateResponse
	rotUpdateResp.State = tfsdk.State{Schema: schema}
	r.Update(ctx, resource.UpdateRequest{Plan: rotPlan, State: createResp.State}, &rotUpdateResp)
	if rotUpdateResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on rotation update: %v", rotUpdateResp.Diagnostics)
	}
	var rotState PaperclipBoardKeyResourceModel
	_ = rotUpdateResp.State.Get(ctx, &rotState)
	if rotState.ID.ValueString() != "bk-2" || !mockState.revokedKeys["bk-1"] {
		t.Fatalf("expected bk-2 active and bk-1 revoked, got %+v (revoked: %v)", rotState, mockState.revokedKeys)
	}

	// Update - Changed generation without expires_at and with KeyID empty in state
	rotNoExpModel := createdModel
	rotNoExpModel.Name = types.StringValue("rotated-no-exp")
	rotNoExpModel.Generation = types.Int64Value(3)
	rotNoExpModel.ExpiresAt = types.StringNull()
	rotNoExpPlan := tfsdk.Plan{Schema: schema}
	_ = rotNoExpPlan.Set(ctx, &rotNoExpModel)

	rotNoExpState := tfsdk.State{Schema: schema}
	rotNoExpStateModel := createdModel
	rotNoExpStateModel.KeyID = types.StringNull()
	rotNoExpStateModel.ID = types.StringValue("bk-2")
	_ = rotNoExpState.Set(ctx, &rotNoExpStateModel)

	var rotNoExpUpdateResp resource.UpdateResponse
	rotNoExpUpdateResp.State = tfsdk.State{Schema: schema}
	r.Update(ctx, resource.UpdateRequest{Plan: rotNoExpPlan, State: rotNoExpState}, &rotNoExpUpdateResp)
	if rotNoExpUpdateResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on rotation update without expires_at: %v", rotNoExpUpdateResp.Diagnostics)
	}

	// Delete - API failure
	mockState.failDelete = true
	var failDelResp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: rotUpdateResp.State}, &failDelResp)
	if !failDelResp.Diagnostics.HasError() {
		t.Fatal("expected error on delete failure")
	}
	mockState.failDelete = false

	// Delete - Success
	var delResp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: rotUpdateResp.State}, &delResp)
	if delResp.Diagnostics.HasError() {
		t.Fatalf("unexpected delete error: %v", delResp.Diagnostics)
	}
	if !mockState.revokedKeys["bk-2"] {
		t.Fatal("expected bk-2 to be revoked on delete")
	}

	// Delete - with ID only
	mockState.keys["bk-id-only"] = client.PaperclipBoardKey{ID: "bk-id-only", Name: "ID Only"}
	idOnlyDelState := tfsdk.State{Schema: schema}
	idOnlyDelModel := PaperclipBoardKeyResourceModel{
		ID:    types.StringValue("bk-id-only"),
		KeyID: types.StringNull(),
	}
	_ = idOnlyDelState.Set(ctx, &idOnlyDelModel)
	var idOnlyDelResp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: idOnlyDelState}, &idOnlyDelResp)
	if idOnlyDelResp.Diagnostics.HasError() {
		t.Fatalf("delete with ID only failed: %v", idOnlyDelResp.Diagnostics)
	}

	// Delete - Empty keyID no-op
	var emptyDelResp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: emptyState}, &emptyDelResp)
	if emptyDelResp.Diagnostics.HasError() {
		t.Fatal("expected no error deleting empty state")
	}

	// ImportState
	var impResp resource.ImportStateResponse
	impResp.State = tfsdk.State{Schema: schema}
	_ = impResp.State.Set(ctx, &PaperclipBoardKeyResourceModel{})
	r.ImportState(ctx, resource.ImportStateRequest{ID: "bk-imported"}, &impResp)
	if impResp.Diagnostics.HasError() {
		t.Fatalf("unexpected import error: %v", impResp.Diagnostics)
	}
}

func TestPaperclipAgentKeyResourceFullLifecycle(t *testing.T) {
	ctx := context.Background()

	mockState := struct {
		keys        map[string]client.PaperclipAgentKey
		revokedKeys map[string]bool
		failCreate  bool
		failRead    bool
		failDelete  bool
	}{
		keys:        make(map[string]client.PaperclipAgentKey),
		revokedKeys: make(map[string]bool),
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/agents/ag-1/keys":
			if mockState.failCreate {
				http.Error(w, "create failure", http.StatusInternalServerError)
				return
			}
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			keyID := "ak-1"
			if body["name"] == "rotated-agent-key" {
				keyID = "ak-2"
			}
			ak := client.PaperclipAgentKey{
				ID:      keyID,
				AgentID: "ag-1",
				Name:    body["name"].(string),
				Token:   "tok-" + keyID,
			}
			if s, ok := body["scope"].(map[string]interface{}); ok {
				ak.Scope = &client.AgentKeyScope{
					Kind:      s["kind"].(string),
					ProjectID: "proj-1",
				}
			}
			mockState.keys[keyID] = ak
			_ = json.NewEncoder(w).Encode(ak)
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/ag-1/keys":
			if mockState.failRead {
				http.Error(w, "read failure", http.StatusInternalServerError)
				return
			}
			var list []client.PaperclipAgentKey
			for _, k := range mockState.keys {
				if !mockState.revokedKeys[k.ID] {
					list = append(list, k)
				}
			}
			_ = json.NewEncoder(w).Encode(list)
		case r.Method == http.MethodDelete:
			if mockState.failDelete {
				http.Error(w, "delete failure", http.StatusInternalServerError)
				return
			}
			parts := r.URL.Path[len("/api/agents/ag-1/keys/"):]
			mockState.revokedKeys[parts] = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	r := NewPaperclipAgentKeyResource().(*PaperclipAgentKeyResource)

	// Metadata & Schema
	var meta resource.MetadataResponse
	r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "paperclip"}, &meta)
	if meta.TypeName != "paperclip_agent_key" {
		t.Fatalf("unexpected metadata typename: %s", meta.TypeName)
	}

	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	if len(schemaResp.Schema.Attributes) == 0 {
		t.Fatal("empty schema attributes")
	}

	// Configure
	var confResp resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: nil}, &confResp)
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: "invalid"}, &confResp)
	if !confResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid provider data")
	}

	schema := configureTestResource(t, server.URL, r)

	// Bad plan and state
	badPlan := tfsdk.Plan{Schema: schema, Raw: tftypes.NewValue(tftypes.String, "invalid")}
	badState := tfsdk.State{Schema: schema, Raw: tftypes.NewValue(tftypes.String, "invalid")}
	var badRespCreate resource.CreateResponse
	r.Create(ctx, resource.CreateRequest{Plan: badPlan}, &badRespCreate)
	if !badRespCreate.Diagnostics.HasError() {
		t.Fatal("expected error on bad plan")
	}
	var badRespRead resource.ReadResponse
	r.Read(ctx, resource.ReadRequest{State: badState}, &badRespRead)
	if !badRespRead.Diagnostics.HasError() {
		t.Fatal("expected error on bad state")
	}
	var badRespUpdate resource.UpdateResponse
	r.Update(ctx, resource.UpdateRequest{Plan: badPlan, State: badState}, &badRespUpdate)
	if !badRespUpdate.Diagnostics.HasError() {
		t.Fatal("expected error on bad update")
	}
	var badRespDelete resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: badState}, &badRespDelete)
	if !badRespDelete.Diagnostics.HasError() {
		t.Fatal("expected error on bad delete")
	}

	// Scope conversion tests
	t.Run("scopeModelToClient branches", func(t *testing.T) {
		// nil model
		sc, err := scopeModelToClient(ctx, nil)
		if err != nil || sc.Kind != "standard" {
			t.Fatalf("unexpected nil model scope: %v, %v", sc, err)
		}

		// task_bridge missing boundaries
		tbBad := &AgentKeyScopeModel{
			Kind: types.StringValue("task_bridge"),
		}
		if _, err := scopeModelToClient(ctx, tbBad); err == nil {
			t.Fatal("expected error for task_bridge without boundaries")
		}

		// task_bridge valid with all lists
		projIDs, _ := types.ListValueFrom(ctx, types.StringType, []string{"p1", "p2"})
		parentIDs, _ := types.ListValueFrom(ctx, types.StringType, []string{"parent1"})
		assigneeIDs, _ := types.ListValueFrom(ctx, types.StringType, []string{"ag2"})
		tbGood := &AgentKeyScopeModel{
			Kind:                    types.StringValue("task_bridge"),
			ProjectID:               types.StringValue("p1"),
			ProjectIDs:              projIDs,
			ParentIssueID:           types.StringValue("parent1"),
			ParentIssueIDs:          parentIDs,
			AllowedAssigneeAgentIDs: assigneeIDs,
		}
		scGood, err := scopeModelToClient(ctx, tbGood)
		if err != nil || len(scGood.ProjectIDs) != 2 || len(scGood.AllowedAssigneeAgentIDs) != 1 {
			t.Fatalf("unexpected task_bridge scope: %+v, err: %v", scGood, err)
		}

		// skill_test missing issue_id
		stBad := &AgentKeyScopeModel{
			Kind: types.StringValue("skill_test"),
		}
		if _, err := scopeModelToClient(ctx, stBad); err == nil {
			t.Fatal("expected error for skill_test without issue_id")
		}

		// skill_test valid
		stGood := &AgentKeyScopeModel{
			Kind:    types.StringValue("skill_test"),
			IssueID: types.StringValue("iss-1"),
		}
		scSt, err := scopeModelToClient(ctx, stGood)
		if err != nil || scSt.IssueID != "iss-1" {
			t.Fatalf("unexpected skill_test scope: %+v, err: %v", scSt, err)
		}
	})

	t.Run("clientScopeToModel branches", func(t *testing.T) {
		// nil scope
		m, d := clientScopeToModel(ctx, nil, nil)
		if d.HasError() || m != nil {
			t.Fatalf("unexpected nil scope: %v, %v", m, d)
		}

		// full scope
		fullClientScope := &client.AgentKeyScope{
			Kind:                    "task_bridge",
			ProjectID:               "p1",
			ProjectIDs:              []string{"p1", "p2"},
			ParentIssueID:           "par1",
			ParentIssueIDs:          []string{"par1"},
			AllowedAssigneeAgentIDs: []string{"ag2"},
			IssueID:                 "iss1",
		}
		resModel, diags := clientScopeToModel(ctx, fullClientScope, nil)
		if diags.HasError() || resModel.Kind.ValueString() != "task_bridge" {
			t.Fatalf("unexpected scope model: %+v, diags: %v", resModel, diags)
		}

		// empty scope fields
		emptyClientScope := &client.AgentKeyScope{
			Kind: "standard",
		}
		resEmpty, dEmpty := clientScopeToModel(ctx, emptyClientScope, nil)
		if dEmpty.HasError() || !resEmpty.ProjectID.IsNull() {
			t.Fatalf("unexpected empty scope model: %+v, diags: %v", resEmpty, dEmpty)
		}
	})

	testScopeModel := func(kind string, projID string) *AgentKeyScopeModel {
		pID := types.StringNull()
		if projID != "" {
			pID = types.StringValue(projID)
		}
		return &AgentKeyScopeModel{
			Kind:                    types.StringValue(kind),
			ProjectID:               pID,
			ProjectIDs:              types.ListNull(types.StringType),
			ParentIssueID:           types.StringNull(),
			ParentIssueIDs:          types.ListNull(types.StringType),
			AllowedAssigneeAgentIDs: types.ListNull(types.StringType),
			IssueID:                 types.StringNull(),
		}
	}

	// Create - Invalid scope error
	invModel := PaperclipAgentKeyResourceModel{
		AgentID:    types.StringValue("ag-1"),
		Name:       types.StringValue("bad-scope"),
		Generation: types.Int64Value(1),
		Scope:      testScopeModel("task_bridge", ""), // missing boundaries
	}
	invPlan := tfsdk.Plan{Schema: schema}
	if diags := invPlan.Set(ctx, &invModel); diags.HasError() {
		t.Fatalf("failed setting invPlan: %v", diags)
	}
	var invCreateResp resource.CreateResponse
	invCreateResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: invPlan}, &invCreateResp)
	if !invCreateResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid scope")
	}

	// Create - API failure
	mockState.failCreate = true
	validModel := PaperclipAgentKeyResourceModel{
		AgentID:    types.StringValue("ag-1"),
		Name:       types.StringValue("initial-agent-key"),
		Generation: types.Int64Value(1),
		Scope:      testScopeModel("standard", "proj-1"),
	}
	validPlan := tfsdk.Plan{Schema: schema}
	if diags := validPlan.Set(ctx, &validModel); diags.HasError() {
		t.Fatalf("failed setting validPlan: %v", diags)
	}
	var failCreateResp resource.CreateResponse
	failCreateResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: validPlan}, &failCreateResp)
	if !failCreateResp.Diagnostics.HasError() {
		t.Fatal("expected error on API create failure")
	}
	mockState.failCreate = false

	// Create - Success
	var createResp resource.CreateResponse
	createResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: validPlan}, &createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("unexpected create error: %v", createResp.Diagnostics)
	}
	var createdModel PaperclipAgentKeyResourceModel
	_ = createResp.State.Get(ctx, &createdModel)
	if createdModel.ID.ValueString() != "ak-1" || createdModel.DeliveryState.ValueString() != "delivered" {
		t.Fatalf("unexpected created agent key model: %+v", createdModel)
	}

	// Create - without scope
	noScopeModel := PaperclipAgentKeyResourceModel{
		AgentID:    types.StringValue("ag-1"),
		Name:       types.StringValue("no-scope-key"),
		Generation: types.Int64Value(1),
		Scope:      nil,
	}
	noScopePlan := tfsdk.Plan{Schema: schema}
	if diags := noScopePlan.Set(ctx, &noScopeModel); diags.HasError() {
		t.Fatal(diags)
	}
	var noScopeResp resource.CreateResponse
	noScopeResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: noScopePlan}, &noScopeResp)
	if noScopeResp.Diagnostics.HasError() {
		t.Fatalf("create without scope failed: %v", noScopeResp.Diagnostics)
	}

	// Read - Success
	var readResp resource.ReadResponse
	readResp.State = createResp.State
	r.Read(ctx, resource.ReadRequest{State: createResp.State}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("unexpected read error: %v", readResp.Diagnostics)
	}

	// Read - ID only
	idOnlyAgentState := tfsdk.State{Schema: schema}
	idOnlyAgentModel := PaperclipAgentKeyResourceModel{
		AgentID: types.StringValue("ag-1"),
		ID:      types.StringValue("ak-1"),
		KeyID:   types.StringNull(),
	}
	_ = idOnlyAgentState.Set(ctx, &idOnlyAgentModel)
	var idOnlyAgentReadResp resource.ReadResponse
	idOnlyAgentReadResp.State = idOnlyAgentState
	r.Read(ctx, resource.ReadRequest{State: idOnlyAgentState}, &idOnlyAgentReadResp)
	if idOnlyAgentReadResp.Diagnostics.HasError() {
		t.Fatalf("read agent key with ID only failed: %v", idOnlyAgentReadResp.Diagnostics)
	}

	// Read - Empty keyID or agentID removes resource
	emptyState := tfsdk.State{Schema: schema}
	emptyModel := PaperclipAgentKeyResourceModel{Name: types.StringValue("none")}
	_ = emptyState.Set(ctx, &emptyModel)
	var emptyReadResp resource.ReadResponse
	emptyReadResp.State = emptyState
	r.Read(ctx, resource.ReadRequest{State: emptyState}, &emptyReadResp)
	var emptyCheck PaperclipAgentKeyResourceModel
	_ = emptyReadResp.State.Get(ctx, &emptyCheck)
	if !emptyCheck.ID.IsNull() {
		t.Fatal("expected resource removal on empty keyID/agentID")
	}

	// Read - API failure
	mockState.failRead = true
	var failReadResp resource.ReadResponse
	failReadResp.State = createResp.State
	r.Read(ctx, resource.ReadRequest{State: createResp.State}, &failReadResp)
	if !failReadResp.Diagnostics.HasError() {
		t.Fatal("expected error on read failure")
	}
	mockState.failRead = false

	// Read - Not found removes resource
	mockState.revokedKeys["ak-1"] = true
	var nfReadResp resource.ReadResponse
	nfReadResp.State = createResp.State
	r.Read(ctx, resource.ReadRequest{State: createResp.State}, &nfReadResp)
	var nfCheck PaperclipAgentKeyResourceModel
	_ = nfReadResp.State.Get(ctx, &nfCheck)
	if !nfCheck.ID.IsNull() {
		t.Fatal("expected resource removal on not found")
	}
	delete(mockState.revokedKeys, "ak-1")

	// Update - Unchanged generation (no rotation)
	unchangedModel := createdModel
	unchangedModel.Name = types.StringValue("renamed-no-rot")
	unchangedPlan := tfsdk.Plan{Schema: schema}
	_ = unchangedPlan.Set(ctx, &unchangedModel)
	var unchangedUpdateResp resource.UpdateResponse
	unchangedUpdateResp.State = tfsdk.State{Schema: schema}
	r.Update(ctx, resource.UpdateRequest{Plan: unchangedPlan, State: createResp.State}, &unchangedUpdateResp)
	if unchangedUpdateResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on unchanged update: %v", unchangedUpdateResp.Diagnostics)
	}

	// Update - Changed generation: invalid scope
	rotInvModel := createdModel
	rotInvModel.Generation = types.Int64Value(2)
	rotInvModel.Scope = testScopeModel("task_bridge", "") // invalid
	rotInvPlan := tfsdk.Plan{Schema: schema}
	if diags := rotInvPlan.Set(ctx, &rotInvModel); diags.HasError() {
		t.Fatalf("failed setting rotInvPlan: %v", diags)
	}
	var rotInvUpdateResp resource.UpdateResponse
	rotInvUpdateResp.State = tfsdk.State{Schema: schema}
	r.Update(ctx, resource.UpdateRequest{Plan: rotInvPlan, State: createResp.State}, &rotInvUpdateResp)
	if !rotInvUpdateResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid scope during update")
	}

	// Update - Changed generation: API create failure
	mockState.failCreate = true
	rotModel := createdModel
	rotModel.Name = types.StringValue("rotated-agent-key")
	rotModel.Generation = types.Int64Value(2)
	rotPlan := tfsdk.Plan{Schema: schema}
	_ = rotPlan.Set(ctx, &rotModel)
	var failRotUpdateResp resource.UpdateResponse
	failRotUpdateResp.State = tfsdk.State{Schema: schema}
	r.Update(ctx, resource.UpdateRequest{Plan: rotPlan, State: createResp.State}, &failRotUpdateResp)
	if !failRotUpdateResp.Diagnostics.HasError() {
		t.Fatal("expected error on rotation mint failure")
	}
	mockState.failCreate = false

	// Update - Changed generation: Success
	var rotUpdateResp resource.UpdateResponse
	rotUpdateResp.State = tfsdk.State{Schema: schema}
	r.Update(ctx, resource.UpdateRequest{Plan: rotPlan, State: createResp.State}, &rotUpdateResp)
	if rotUpdateResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on rotation update: %v", rotUpdateResp.Diagnostics)
	}
	var rotState PaperclipAgentKeyResourceModel
	_ = rotUpdateResp.State.Get(ctx, &rotState)
	if rotState.ID.ValueString() != "ak-2" || !mockState.revokedKeys["ak-1"] {
		t.Fatalf("expected ak-2 active and ak-1 revoked, got %+v (revoked: %v)", rotState, mockState.revokedKeys)
	}

	// Update - Changed generation with KeyID empty in state
	rotAkNoKeyIDModel := createdModel
	rotAkNoKeyIDModel.Name = types.StringValue("rotated-ak-no-keyid")
	rotAkNoKeyIDModel.Generation = types.Int64Value(3)
	rotAkNoKeyIDPlan := tfsdk.Plan{Schema: schema}
	_ = rotAkNoKeyIDPlan.Set(ctx, &rotAkNoKeyIDModel)

	rotAkState := tfsdk.State{Schema: schema}
	rotAkStateModel := createdModel
	rotAkStateModel.KeyID = types.StringNull()
	rotAkStateModel.ID = types.StringValue("ak-2")
	_ = rotAkState.Set(ctx, &rotAkStateModel)

	var rotAkUpdateResp resource.UpdateResponse
	rotAkUpdateResp.State = tfsdk.State{Schema: schema}
	r.Update(ctx, resource.UpdateRequest{Plan: rotAkNoKeyIDPlan, State: rotAkState}, &rotAkUpdateResp)
	if rotAkUpdateResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on rotation update with empty KeyID: %v", rotAkUpdateResp.Diagnostics)
	}

	// Delete - API failure
	mockState.failDelete = true
	var failDelResp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: rotUpdateResp.State}, &failDelResp)
	if !failDelResp.Diagnostics.HasError() {
		t.Fatal("expected error on delete failure")
	}
	mockState.failDelete = false

	// Delete - Success
	var delResp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: rotUpdateResp.State}, &delResp)
	if delResp.Diagnostics.HasError() {
		t.Fatalf("unexpected delete error: %v", delResp.Diagnostics)
	}
	if !mockState.revokedKeys["ak-2"] {
		t.Fatal("expected ak-2 to be revoked on delete")
	}

	// Delete - with ID only
	mockState.keys["ak-id-only"] = client.PaperclipAgentKey{ID: "ak-id-only", AgentID: "ag-1", Name: "ID Only"}
	idOnlyAgentDelState := tfsdk.State{Schema: schema}
	idOnlyAgentDelModel := PaperclipAgentKeyResourceModel{
		AgentID: types.StringValue("ag-1"),
		ID:      types.StringValue("ak-id-only"),
		KeyID:   types.StringNull(),
	}
	_ = idOnlyAgentDelState.Set(ctx, &idOnlyAgentDelModel)
	var idOnlyAgentDelResp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: idOnlyAgentDelState}, &idOnlyAgentDelResp)
	if idOnlyAgentDelResp.Diagnostics.HasError() {
		t.Fatalf("delete agent key with ID only failed: %v", idOnlyAgentDelResp.Diagnostics)
	}

	// Delete - Empty keyID no-op
	var emptyDelResp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: emptyState}, &emptyDelResp)
	if emptyDelResp.Diagnostics.HasError() {
		t.Fatal("expected no error deleting empty state")
	}

	// ImportState - Invalid ID format
	var invImpResp resource.ImportStateResponse
	invImpResp.State = tfsdk.State{Schema: schema}
	_ = invImpResp.State.Set(ctx, &PaperclipAgentKeyResourceModel{})
	r.ImportState(ctx, resource.ImportStateRequest{ID: "no-separator"}, &invImpResp)
	if !invImpResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid import ID format")
	}

	// ImportState - With slash
	var slashImpResp resource.ImportStateResponse
	slashImpResp.State = tfsdk.State{Schema: schema}
	_ = slashImpResp.State.Set(ctx, &PaperclipAgentKeyResourceModel{})
	r.ImportState(ctx, resource.ImportStateRequest{ID: "ag-1/ak-imported"}, &slashImpResp)
	if slashImpResp.Diagnostics.HasError() {
		t.Fatalf("unexpected slash import error: %v", slashImpResp.Diagnostics)
	}

	// ImportState - With colon
	var colonImpResp resource.ImportStateResponse
	colonImpResp.State = tfsdk.State{Schema: schema}
	_ = colonImpResp.State.Set(ctx, &PaperclipAgentKeyResourceModel{})
	r.ImportState(ctx, resource.ImportStateRequest{ID: "ag-1:ak-imported"}, &colonImpResp)
	if colonImpResp.Diagnostics.HasError() {
		t.Fatalf("unexpected colon import error: %v", colonImpResp.Diagnostics)
	}
}

func TestEphemeralResourcesDeferralAndClose(t *testing.T) {
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodDelete && r.URL.Path == "/api/board-api-keys/bk-reconcile":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodDelete && r.URL.Path == "/api/agents/ag-1/keys/ak-reconcile":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cli, _ := client.NewClient(server.URL, "test-token")

	t.Run("PaperclipBoardKeyEphemeralResource deferral, missing token, and unconfigured", func(t *testing.T) {
		r := NewPaperclipBoardKeyEphemeralResource().(*PaperclipBoardKeyEphemeralResource)
		var schemaResp ephemeral.SchemaResponse
		r.Schema(ctx, ephemeral.SchemaRequest{}, &schemaResp)

		// DeferralAllowed == true
		model := PaperclipBoardKeyEphemeralResourceModel{
			Name:       types.StringValue("deferred-key"),
			Activation: types.StringValue("act-1"),
			KeyID:      types.StringValue("bk-defer"),
		}
		state := tfsdk.State{Schema: schemaResp.Schema}
		_ = state.Set(ctx, &model)
		req := ephemeral.OpenRequest{
			Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: state.Raw},
			ClientCapabilities: ephemeral.OpenClientCapabilities{
				DeferralAllowed: true,
			},
		}
		var openResp ephemeral.OpenResponse
		openResp.Result = tfsdk.EphemeralResultData{Raw: state.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, req, &openResp)
		if openResp.Deferred == nil {
			t.Fatal("expected deferred response when DeferralAllowed is true")
		}

		// Unconfigured client
		req.ClientCapabilities.DeferralAllowed = false
		var unconfResp ephemeral.OpenResponse
		unconfResp.Result = tfsdk.EphemeralResultData{Raw: state.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, req, &unconfResp)
		if !unconfResp.Diagnostics.HasError() {
			t.Fatal("expected error with unconfigured client")
		}

		// Configure
		var confResp ephemeral.ConfigureResponse
		r.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: cli}, &confResp)
		if confResp.Diagnostics.HasError() {
			t.Fatal(confResp.Diagnostics)
		}

		// DeferralAllowed == false but token missing from client registry
		var missingResp ephemeral.OpenResponse
		missingResp.Result = tfsdk.EphemeralResultData{Raw: state.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, req, &missingResp)
		var missingModel PaperclipBoardKeyEphemeralResourceModel
		_ = missingResp.Result.Get(ctx, &missingModel)
		if missingModel.Token.ValueString() != "token-already-delivered" {
			t.Fatalf("expected token to be token-already-delivered, got %s", missingModel.Token.ValueString())
		}

		// Token present in client registry
		cli.RegisterMintedToken("bk-defer", "tok-defer-val")
		var hitResp ephemeral.OpenResponse
		hitResp.Result = tfsdk.EphemeralResultData{Raw: state.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, req, &hitResp)
		if hitResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error when token present: %v", hitResp.Diagnostics)
		}
	})

	t.Run("PaperclipAgentKeyEphemeralResource deferral, missing token, and unconfigured", func(t *testing.T) {
		r := NewPaperclipAgentKeyEphemeralResource().(*PaperclipAgentKeyEphemeralResource)
		var schemaResp ephemeral.SchemaResponse
		r.Schema(ctx, ephemeral.SchemaRequest{}, &schemaResp)

		// DeferralAllowed == true
		model := PaperclipAgentKeyEphemeralResourceModel{
			AgentID:    types.StringValue("ag-1"),
			Name:       types.StringValue("deferred-agent-key"),
			Activation: types.StringValue("act-1"),
			KeyID:      types.StringValue("ak-defer"),
		}
		state := tfsdk.State{Schema: schemaResp.Schema}
		_ = state.Set(ctx, &model)
		req := ephemeral.OpenRequest{
			Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: state.Raw},
			ClientCapabilities: ephemeral.OpenClientCapabilities{
				DeferralAllowed: true,
			},
		}
		var openResp ephemeral.OpenResponse
		openResp.Result = tfsdk.EphemeralResultData{Raw: state.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, req, &openResp)
		if openResp.Deferred == nil {
			t.Fatal("expected deferred response when DeferralAllowed is true")
		}

		// Unconfigured client
		req.ClientCapabilities.DeferralAllowed = false
		var unconfResp ephemeral.OpenResponse
		unconfResp.Result = tfsdk.EphemeralResultData{Raw: state.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, req, &unconfResp)
		if !unconfResp.Diagnostics.HasError() {
			t.Fatal("expected error with unconfigured client")
		}

		// Configure
		var confResp ephemeral.ConfigureResponse
		r.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: cli}, &confResp)
		if confResp.Diagnostics.HasError() {
			t.Fatal(confResp.Diagnostics)
		}

		// DeferralAllowed == false but token missing from client registry
		var missingResp ephemeral.OpenResponse
		missingResp.Result = tfsdk.EphemeralResultData{Raw: state.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, req, &missingResp)
		var missingModel PaperclipAgentKeyEphemeralResourceModel
		_ = missingResp.Result.Get(ctx, &missingModel)
		if missingModel.Token.ValueString() != "token-already-delivered" {
			t.Fatalf("expected token to be token-already-delivered, got %s", missingModel.Token.ValueString())
		}

		// Token present in client registry
		cli.RegisterMintedToken("ak-defer", "tok-agent-val")
		var hitResp ephemeral.OpenResponse
		hitResp.Result = tfsdk.EphemeralResultData{Raw: state.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, req, &hitResp)
		if hitResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error when agent token present: %v", hitResp.Diagnostics)
		}

		// KeyID is empty, missing agent_id
		emptyKeyModel := PaperclipAgentKeyEphemeralResourceModel{
			Name: types.StringValue("no-agent"),
		}
		emptyState := tfsdk.State{Schema: schemaResp.Schema}
		_ = emptyState.Set(ctx, &emptyKeyModel)
		emptyReq := ephemeral.OpenRequest{
			Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: emptyState.Raw},
		}
		var emptyKeyResp ephemeral.OpenResponse
		emptyKeyResp.Result = tfsdk.EphemeralResultData{Raw: emptyState.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, emptyReq, &emptyKeyResp)
		if !emptyKeyResp.Diagnostics.HasError() {
			t.Fatal("expected error when agent_id is missing and key_id is empty")
		}
	})
}

func TestCredentialsResourcesModifyPlan(t *testing.T) {
	ctx := context.Background()

	t.Run("board_key_modify_plan", func(t *testing.T) {
		r := NewPaperclipBoardKeyResource().(*PaperclipBoardKeyResource)
		schema := configureTestResource(t, "http://127.0.0.1:9999", r)

		// Null plan or null state
		var resp1 resource.ModifyPlanResponse
		r.ModifyPlan(ctx, resource.ModifyPlanRequest{
			Plan:  tfsdk.Plan{Schema: schema, Raw: tftypes.NewValue(schema.Type().TerraformType(ctx), nil)},
			State: tfsdk.State{Schema: schema, Raw: tftypes.NewValue(schema.Type().TerraformType(ctx), nil)},
		}, &resp1)
		if resp1.Diagnostics.HasError() {
			t.Fatal(resp1.Diagnostics)
		}

		var respNullPlan resource.ModifyPlanResponse
		var stOk tfsdk.State = tfsdk.State{Schema: schema}
		_ = stOk.Set(ctx, &PaperclipBoardKeyResourceModel{Name: types.StringValue("bk"), Generation: types.Int64Value(1)})
		r.ModifyPlan(ctx, resource.ModifyPlanRequest{
			Plan:  tfsdk.Plan{Schema: schema, Raw: tftypes.NewValue(schema.Type().TerraformType(ctx), nil)},
			State: stOk,
		}, &respNullPlan)
		if respNullPlan.Diagnostics.HasError() {
			t.Fatal(respNullPlan.Diagnostics)
		}

		// Bad plan type
		badPlan := tfsdk.Plan{Schema: schema, Raw: tftypes.NewValue(tftypes.String, "invalid")}
		var respBad resource.ModifyPlanResponse
		r.ModifyPlan(ctx, resource.ModifyPlanRequest{
			Plan:  badPlan,
			State: stOk,
		}, &respBad)
		if !respBad.Diagnostics.HasError() {
			t.Fatal("expected error with bad plan")
		}

		// Unchanged generation
		mSame := PaperclipBoardKeyResourceModel{
			ID:         types.StringValue("bk-1"),
			KeyID:      types.StringValue("bk-1"),
			Name:       types.StringValue("bk"),
			Generation: types.Int64Value(1),
		}
		var planSame tfsdk.Plan = tfsdk.Plan{Schema: schema}
		_ = planSame.Set(ctx, &mSame)
		var stateSame tfsdk.State = tfsdk.State{Schema: schema}
		_ = stateSame.Set(ctx, &mSame)
		var respSame resource.ModifyPlanResponse
		respSame.Plan = planSame
		r.ModifyPlan(ctx, resource.ModifyPlanRequest{
			Plan:  planSame,
			State: stateSame,
		}, &respSame)
		if respSame.Diagnostics.HasError() {
			t.Fatal(respSame.Diagnostics)
		}
		var outSame PaperclipBoardKeyResourceModel
		_ = respSame.Plan.Get(ctx, &outSame)
		if outSame.KeyID.IsUnknown() {
			t.Fatal("expected key_id to remain known when generation unchanged")
		}

		// Changed generation
		mChangedPlan := PaperclipBoardKeyResourceModel{
			ID:         types.StringValue("bk-1"),
			KeyID:      types.StringValue("bk-1"),
			Name:       types.StringValue("bk"),
			Generation: types.Int64Value(2),
		}
		var planChanged tfsdk.Plan = tfsdk.Plan{Schema: schema}
		_ = planChanged.Set(ctx, &mChangedPlan)
		var respChanged resource.ModifyPlanResponse
		respChanged.Plan = planChanged
		r.ModifyPlan(ctx, resource.ModifyPlanRequest{
			Plan:  planChanged,
			State: stateSame,
		}, &respChanged)
		if respChanged.Diagnostics.HasError() {
			t.Fatal(respChanged.Diagnostics)
		}
		var outChanged PaperclipBoardKeyResourceModel
		_ = respChanged.Plan.Get(ctx, &outChanged)
		if !outChanged.KeyID.IsUnknown() || !outChanged.ID.IsUnknown() || !outChanged.DeliveryState.IsUnknown() {
			t.Fatal("expected key_id, id, and delivery_state to be unknown when generation changed")
		}
	})

	t.Run("agent_key_modify_plan", func(t *testing.T) {
		r := NewPaperclipAgentKeyResource().(*PaperclipAgentKeyResource)
		schema := configureTestResource(t, "http://127.0.0.1:9999", r)

		// Null plan or null state
		var resp1 resource.ModifyPlanResponse
		r.ModifyPlan(ctx, resource.ModifyPlanRequest{
			Plan:  tfsdk.Plan{Schema: schema, Raw: tftypes.NewValue(schema.Type().TerraformType(ctx), nil)},
			State: tfsdk.State{Schema: schema, Raw: tftypes.NewValue(schema.Type().TerraformType(ctx), nil)},
		}, &resp1)
		if resp1.Diagnostics.HasError() {
			t.Fatal(resp1.Diagnostics)
		}

		var stOk tfsdk.State = tfsdk.State{Schema: schema}
		_ = stOk.Set(ctx, &PaperclipAgentKeyResourceModel{AgentID: types.StringValue("ag-1"), Name: types.StringValue("ak"), Generation: types.Int64Value(1)})
		var respNullPlan resource.ModifyPlanResponse
		r.ModifyPlan(ctx, resource.ModifyPlanRequest{
			Plan:  tfsdk.Plan{Schema: schema, Raw: tftypes.NewValue(schema.Type().TerraformType(ctx), nil)},
			State: stOk,
		}, &respNullPlan)
		if respNullPlan.Diagnostics.HasError() {
			t.Fatal(respNullPlan.Diagnostics)
		}

		// Bad plan type
		badPlan := tfsdk.Plan{Schema: schema, Raw: tftypes.NewValue(tftypes.String, "invalid")}
		var respBad resource.ModifyPlanResponse
		r.ModifyPlan(ctx, resource.ModifyPlanRequest{
			Plan:  badPlan,
			State: stOk,
		}, &respBad)
		if !respBad.Diagnostics.HasError() {
			t.Fatal("expected error with bad plan")
		}

		// Unchanged generation
		mSame := PaperclipAgentKeyResourceModel{
			ID:         types.StringValue("ak-1"),
			KeyID:      types.StringValue("ak-1"),
			AgentID:    types.StringValue("ag-1"),
			Name:       types.StringValue("ak"),
			Generation: types.Int64Value(1),
		}
		var planSame tfsdk.Plan = tfsdk.Plan{Schema: schema}
		_ = planSame.Set(ctx, &mSame)
		var stateSame tfsdk.State = tfsdk.State{Schema: schema}
		_ = stateSame.Set(ctx, &mSame)
		var respSame resource.ModifyPlanResponse
		respSame.Plan = planSame
		r.ModifyPlan(ctx, resource.ModifyPlanRequest{
			Plan:  planSame,
			State: stateSame,
		}, &respSame)
		if respSame.Diagnostics.HasError() {
			t.Fatal(respSame.Diagnostics)
		}
		var outSame PaperclipAgentKeyResourceModel
		_ = respSame.Plan.Get(ctx, &outSame)
		if outSame.KeyID.IsUnknown() {
			t.Fatal("expected key_id to remain known when generation unchanged")
		}

		// Changed generation
		mChangedPlan := PaperclipAgentKeyResourceModel{
			ID:         types.StringValue("ak-1"),
			KeyID:      types.StringValue("ak-1"),
			AgentID:    types.StringValue("ag-1"),
			Name:       types.StringValue("ak"),
			Generation: types.Int64Value(2),
		}
		var planChanged tfsdk.Plan = tfsdk.Plan{Schema: schema}
		_ = planChanged.Set(ctx, &mChangedPlan)
		var respChanged resource.ModifyPlanResponse
		respChanged.Plan = planChanged
		r.ModifyPlan(ctx, resource.ModifyPlanRequest{
			Plan:  planChanged,
			State: stateSame,
		}, &respChanged)
		if respChanged.Diagnostics.HasError() {
			t.Fatal(respChanged.Diagnostics)
		}
		var outChanged PaperclipAgentKeyResourceModel
		_ = respChanged.Plan.Get(ctx, &outChanged)
		if !outChanged.KeyID.IsUnknown() || !outChanged.ID.IsUnknown() || !outChanged.DeliveryState.IsUnknown() {
			t.Fatal("expected key_id, id, and delivery_state to be unknown when generation changed")
		}
	})
}

