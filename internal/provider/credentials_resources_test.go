package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"git.guseynov.org/guseynov/paperclip/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func initOpenResponsePrivate(resp *ephemeral.OpenResponse) any {
	f, ok := reflect.TypeOf(ephemeral.OpenResponse{}).FieldByName("Private")
	if !ok {
		return nil
	}
	val := reflect.New(f.Type.Elem())
	reflect.ValueOf(resp).Elem().FieldByName("Private").Set(val)
	return val.Interface()
}

func initCloseRequestPrivate(req *ephemeral.CloseRequest, private any) {
	if private == nil {
		return
	}
	f, ok := reflect.TypeOf(ephemeral.CloseRequest{}).FieldByName("Private")
	if !ok {
		return
	}
	val := reflect.ValueOf(private)
	if val.Type() == f.Type {
		reflect.ValueOf(req).Elem().FieldByName("Private").Set(val)
	}
}

func TestPaperclipBoardKeyResourceFullLifecycle(t *testing.T) {
	t.Setenv("PAPERCLIP_SCRATCH_DIR", t.TempDir())
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
			} else if body["name"] == "pre-existing-bk" {
				keyID = "bk-new-pred"
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

	// Create - with pre-existing key of same name
	mockState.keys["bk-pre-existing"] = client.PaperclipBoardKey{
		ID:   "bk-pre-existing",
		Name: "pre-existing-bk",
	}
	prePlanModel := PaperclipBoardKeyResourceModel{
		Name:       types.StringValue("pre-existing-bk"),
		Generation: types.Int64Value(1),
	}
	var prePlan tfsdk.Plan = tfsdk.Plan{Schema: schema}
	_ = prePlan.Set(ctx, &prePlanModel)
	var preCreateResp resource.CreateResponse
	preCreateResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: prePlan}, &preCreateResp)
	if preCreateResp.Diagnostics.HasError() {
		t.Fatalf("unexpected preCreate error: %v", preCreateResp.Diagnostics)
	}
	var preCreatedModel PaperclipBoardKeyResourceModel
	_ = preCreateResp.State.Get(ctx, &preCreatedModel)
	if old, ok := r.client.GetSupersededKey(preCreatedModel.KeyID.ValueString()); !ok || old != "bk-pre-existing" {
		t.Fatalf("expected bk-pre-existing superseded, got %s (ok=%v)", old, ok)
	}

	// Read verifies delivery_state is delivered
	var preDelReadResp resource.ReadResponse
	preDelReadResp.State = createResp.State
	r.Read(ctx, resource.ReadRequest{State: createResp.State}, &preDelReadResp)
	if preDelReadResp.Diagnostics.HasError() {
		t.Fatalf("unexpected read error: %v", preDelReadResp.Diagnostics)
	}
	var preDelModel PaperclipBoardKeyResourceModel
	_ = preDelReadResp.State.Get(ctx, &preDelModel)
	if preDelModel.DeliveryState.ValueString() != "delivered" {
		t.Fatalf("expected delivery_state to be delivered, got %s", preDelModel.DeliveryState.ValueString())
	}

	// Confirm delivery
	r.client.MarkTokenDelivered("bk-1")

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

	// Read - Success (transitions to delivered)
	var readResp resource.ReadResponse
	readResp.State = createResp.State
	r.Read(ctx, resource.ReadRequest{State: createResp.State}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("unexpected read error: %v", readResp.Diagnostics)
	}
	_ = readResp.State.Get(ctx, &createdModel)
	if createdModel.DeliveryState.ValueString() != "delivered" {
		t.Fatalf("expected delivery_state to transition to delivered, got %s", createdModel.DeliveryState.ValueString())
	}
	createResp.State = readResp.State

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

	// Update - Unchanged generation when token not delivered
	undeliveredModel := createdModel
	undeliveredModel.KeyID = types.StringValue("bk-undelivered")
	undeliveredModel.ID = types.StringValue("bk-undelivered")
	undeliveredPlan := tfsdk.Plan{Schema: schema}
	_ = undeliveredPlan.Set(ctx, &undeliveredModel)
	var undeliveredState tfsdk.State = tfsdk.State{Schema: schema}
	_ = undeliveredState.Set(ctx, &undeliveredModel)
	var undeliveredUpdateResp resource.UpdateResponse
	undeliveredUpdateResp.State = tfsdk.State{Schema: schema}
	r.Update(ctx, resource.UpdateRequest{Plan: undeliveredPlan, State: undeliveredState}, &undeliveredUpdateResp)
	if undeliveredUpdateResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on undelivered update: %v", undeliveredUpdateResp.Diagnostics)
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
	if rotState.ID.ValueString() != "bk-2" || mockState.revokedKeys["bk-1"] {
		t.Fatalf("expected bk-2 active and bk-1 not revoked yet, got %+v (revoked: %v)", rotState, mockState.revokedKeys)
	}
	// Simulate ephemeral Close confirming delivery of bk-2
	if err := r.client.ConfirmDelivery(ctx, "bk-2"); err != nil {
		t.Fatalf("ConfirmDelivery failed: %v", err)
	}
	if !mockState.revokedKeys["bk-1"] {
		t.Fatal("expected bk-1 revoked after ConfirmDelivery")
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
	t.Setenv("PAPERCLIP_SCRATCH_DIR", t.TempDir())
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
			} else if body["name"] == "pre-existing-ak" {
				keyID = "ak-new-pred"
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

	// Create - with pre-existing agent key of same name
	mockState.keys["ak-pre-existing"] = client.PaperclipAgentKey{
		ID:      "ak-pre-existing",
		AgentID: "ag-1",
		Name:    "pre-existing-ak",
	}
	preAgentPlanModel := PaperclipAgentKeyResourceModel{
		AgentID:    types.StringValue("ag-1"),
		Name:       types.StringValue("pre-existing-ak"),
		Generation: types.Int64Value(1),
	}
	var preAgentPlan tfsdk.Plan = tfsdk.Plan{Schema: schema}
	_ = preAgentPlan.Set(ctx, &preAgentPlanModel)
	var preAgentCreateResp resource.CreateResponse
	preAgentCreateResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: preAgentPlan}, &preAgentCreateResp)
	if preAgentCreateResp.Diagnostics.HasError() {
		t.Fatalf("unexpected preAgentCreate error: %v", preAgentCreateResp.Diagnostics)
	}
	var preCreatedAgentModel PaperclipAgentKeyResourceModel
	_ = preAgentCreateResp.State.Get(ctx, &preCreatedAgentModel)
	if old, ok := r.client.GetSupersededKey(preCreatedAgentModel.KeyID.ValueString()); !ok || old != "ak-pre-existing" {
		t.Fatalf("expected ak-pre-existing superseded, got %s (ok=%v)", old, ok)
	}

	// Read verifies delivery_state is delivered
	var preDelReadResp resource.ReadResponse
	preDelReadResp.State = createResp.State
	r.Read(ctx, resource.ReadRequest{State: createResp.State}, &preDelReadResp)
	if preDelReadResp.Diagnostics.HasError() {
		t.Fatalf("unexpected pre-delivery read error: %v", preDelReadResp.Diagnostics)
	}
	var preDelModel PaperclipAgentKeyResourceModel
	_ = preDelReadResp.State.Get(ctx, &preDelModel)
	if preDelModel.DeliveryState.ValueString() != "delivered" {
		t.Fatalf("expected delivery_state to be delivered, got %s", preDelModel.DeliveryState.ValueString())
	}

	// Confirm delivery
	r.client.MarkTokenDelivered("ak-1")

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

	// Read - Success (transitions to delivered)
	var readResp resource.ReadResponse
	readResp.State = createResp.State
	r.Read(ctx, resource.ReadRequest{State: createResp.State}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("unexpected read error: %v", readResp.Diagnostics)
	}
	_ = readResp.State.Get(ctx, &createdModel)
	if createdModel.DeliveryState.ValueString() != "delivered" {
		t.Fatalf("expected delivery_state to transition to delivered, got %s", createdModel.DeliveryState.ValueString())
	}
	createResp.State = readResp.State

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

	// Update - Unchanged generation when token not delivered
	undeliveredAgentModel := createdModel
	undeliveredAgentModel.KeyID = types.StringValue("ak-undelivered")
	undeliveredAgentModel.ID = types.StringValue("ak-undelivered")
	undeliveredAgentPlan := tfsdk.Plan{Schema: schema}
	_ = undeliveredAgentPlan.Set(ctx, &undeliveredAgentModel)
	var undeliveredAgentState tfsdk.State = tfsdk.State{Schema: schema}
	_ = undeliveredAgentState.Set(ctx, &undeliveredAgentModel)
	var undeliveredAgentUpdateResp resource.UpdateResponse
	undeliveredAgentUpdateResp.State = tfsdk.State{Schema: schema}
	r.Update(ctx, resource.UpdateRequest{Plan: undeliveredAgentPlan, State: undeliveredAgentState}, &undeliveredAgentUpdateResp)
	if undeliveredAgentUpdateResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on undelivered update: %v", undeliveredAgentUpdateResp.Diagnostics)
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
	if rotState.ID.ValueString() != "ak-2" || mockState.revokedKeys["ak-1"] {
		t.Fatalf("expected ak-2 active and ak-1 not revoked yet, got %+v (revoked: %v)", rotState, mockState.revokedKeys)
	}
	// Simulate ephemeral Close confirming delivery of ak-2
	if err := r.client.ConfirmDelivery(ctx, "ak-2"); err != nil {
		t.Fatalf("ConfirmDelivery failed: %v", err)
	}
	if !mockState.revokedKeys["ak-1"] {
		t.Fatal("expected ak-1 revoked after ConfirmDelivery")
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
	t.Setenv("PAPERCLIP_SCRATCH_DIR", t.TempDir())
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/board-api-keys":
			_ = json.NewEncoder(w).Encode([]client.PaperclipBoardKey{
				{ID: "bk-existing", Name: "Existing Board Key"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/ag-1/keys":
			_ = json.NewEncoder(w).Encode([]client.PaperclipAgentKey{
				{ID: "ak-existing", AgentID: "ag-1", Name: "Existing Agent Key"},
			})
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

	t.Run("PaperclipBoardKeyEphemeralResource deferral, missing token, unconfigured, and close", func(t *testing.T) {
		r := NewPaperclipBoardKeyEphemeralResource().(*PaperclipBoardKeyEphemeralResource)
		var schemaResp ephemeral.SchemaResponse
		r.Schema(ctx, ephemeral.SchemaRequest{}, &schemaResp)

		// Unconfigured client
		model := PaperclipBoardKeyEphemeralResourceModel{
			KeyID: types.StringValue("bk-defer"),
		}
		state := tfsdk.State{Schema: schemaResp.Schema}
		_ = state.Set(ctx, &model)
		req := ephemeral.OpenRequest{
			Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: state.Raw},
		}
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

		// Configured client, missing token with DeferralAllowed == true -> deferred
		req.ClientCapabilities.DeferralAllowed = true
		var openResp ephemeral.OpenResponse
		openResp.Result = tfsdk.EphemeralResultData{Raw: state.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, req, &openResp)
		if openResp.Deferred == nil {
			t.Fatal("expected deferred response when DeferralAllowed is true and token missing")
		}

		// DeferralAllowed == false but token missing from client registry -> error diagnostic
		req.ClientCapabilities.DeferralAllowed = false
		var missingResp ephemeral.OpenResponse
		missingResp.Result = tfsdk.EphemeralResultData{Raw: state.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, req, &missingResp)
		if !missingResp.Diagnostics.HasError() {
			t.Fatal("expected error when token is unavailable")
		}

		// Token present in client registry with superseded key
		cli.RegisterMintedToken("bk-defer", "tok-defer-val")
		cli.RecordSupersededKey("bk-defer", "bk-old-super", true, "")
		var hitResp ephemeral.OpenResponse
		priv := initOpenResponsePrivate(&hitResp)
		hitResp.Result = tfsdk.EphemeralResultData{Raw: state.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, req, &hitResp)
		if hitResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error when token present: %v", hitResp.Diagnostics)
		}

		// Existing active key on server: token set to null
		existModel := PaperclipBoardKeyEphemeralResourceModel{
			KeyID: types.StringValue("bk-existing"),
		}
		existState := tfsdk.State{Schema: schemaResp.Schema}
		_ = existState.Set(ctx, &existModel)
		existReq := ephemeral.OpenRequest{
			Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: existState.Raw},
		}
		var existResp ephemeral.OpenResponse
		existResp.Result = tfsdk.EphemeralResultData{Raw: existState.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, existReq, &existResp)
		if existResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error for existing board key: %v", existResp.Diagnostics)
		}

		// Verify Close confirms delivery with native Private state
		cli.RecordTokenDeliverySuccess("tok-defer-val")
		var closeReq ephemeral.CloseRequest
		initCloseRequestPrivate(&closeReq, priv)
		var closeResp ephemeral.CloseResponse
		r.Close(ctx, closeReq, &closeResp)
		if closeResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error on close: %v", closeResp.Diagnostics)
		}
		if !cli.IsTokenDelivered("bk-defer") {
			t.Fatal("expected bk-defer to be marked delivered after Close")
		}

		// Close with unconfigured client is no-op
		unconfRes := NewPaperclipBoardKeyEphemeralResource().(*PaperclipBoardKeyEphemeralResource)
		var unconfCloseResp ephemeral.CloseResponse
		unconfRes.Close(ctx, ephemeral.CloseRequest{}, &unconfCloseResp)
		if unconfCloseResp.Diagnostics.HasError() {
			t.Fatal("expected no error on unconfigured close")
		}

		// Close with empty keyID is no-op
		emptyRes := NewPaperclipBoardKeyEphemeralResource().(*PaperclipBoardKeyEphemeralResource)
		emptyRes.client = cli
		var emptyCloseResp ephemeral.CloseResponse
		emptyRes.Close(ctx, ephemeral.CloseRequest{}, &emptyCloseResp)
		if emptyCloseResp.Diagnostics.HasError() {
			t.Fatal("expected no error on empty close")
		}

		// Close with error on ConfirmDelivery
		failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "server error", http.StatusInternalServerError)
		}))
		defer failServer.Close()
		failCli, _ := client.NewClient(failServer.URL, "test-token")
		failCli.MarkTokenDelivered("bk-fail-new")
		failCli.RecordSupersededKey("bk-fail-new", "bk-fail-old", true, "")
		failRes := NewPaperclipBoardKeyEphemeralResource().(*PaperclipBoardKeyEphemeralResource)
		failRes.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: failCli}, &confResp)
		failRes.keyID = "bk-fail-new"
		var failCloseResp ephemeral.CloseResponse
		failRes.Close(ctx, ephemeral.CloseRequest{}, &failCloseResp)
		if !failCloseResp.Diagnostics.HasError() {
			t.Fatal("expected error diagnostic on failed close confirmation")
		}

		// Close with error on ReconcileFailedDelivery (unconfirmed)
		failRes.keyID = "bk-fail-unconf"
		var failUnconfResp ephemeral.CloseResponse
		failRes.Close(ctx, ephemeral.CloseRequest{}, &failUnconfResp)
		if !failUnconfResp.Diagnostics.HasError() {
			t.Fatal("expected error diagnostic on failed close unconfirmed reconciliation")
		}

		// KeyID empty / null
		emptyKeyModel := PaperclipBoardKeyEphemeralResourceModel{
			KeyID: types.StringNull(),
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
			t.Fatal("expected error when key_id is empty")
		}

		// Empty KeyID with DeferralAllowed
		emptyReq.ClientCapabilities.DeferralAllowed = true
		var emptyDefResp ephemeral.OpenResponse
		emptyDefResp.Result = tfsdk.EphemeralResultData{Raw: emptyState.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, emptyReq, &emptyDefResp)
		if emptyDefResp.Deferred == nil {
			t.Fatal("expected deferred response when key_id empty and DeferralAllowed true")
		}
	})

	t.Run("PaperclipAgentKeyEphemeralResource deferral, missing token, unconfigured, and close", func(t *testing.T) {
		r := NewPaperclipAgentKeyEphemeralResource().(*PaperclipAgentKeyEphemeralResource)
		var schemaResp ephemeral.SchemaResponse
		r.Schema(ctx, ephemeral.SchemaRequest{}, &schemaResp)

		// Unconfigured client
		model := PaperclipAgentKeyEphemeralResourceModel{
			KeyID: types.StringValue("ak-defer"),
		}
		state := tfsdk.State{Schema: schemaResp.Schema}
		_ = state.Set(ctx, &model)
		req := ephemeral.OpenRequest{
			Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: state.Raw},
		}
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

		// Configured client, missing token with DeferralAllowed == true -> deferred
		req.ClientCapabilities.DeferralAllowed = true
		var openResp ephemeral.OpenResponse
		openResp.Result = tfsdk.EphemeralResultData{Raw: state.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, req, &openResp)
		if openResp.Deferred == nil {
			t.Fatal("expected deferred response when DeferralAllowed is true and token missing")
		}

		// DeferralAllowed == false but token missing from client registry -> error diagnostic
		req.ClientCapabilities.DeferralAllowed = false
		var missingResp ephemeral.OpenResponse
		missingResp.Result = tfsdk.EphemeralResultData{Raw: state.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, req, &missingResp)
		if !missingResp.Diagnostics.HasError() {
			t.Fatal("expected error when agent token is unavailable")
		}

		// Token present in client registry with agentID and superseded key
		cli.RegisterMintedAgentKey("ag-defer", "ak-defer", "tok-agent-val")
		cli.RecordSupersededKey("ak-defer", "ak-old-super", false, "ag-defer")
		var hitResp ephemeral.OpenResponse
		priv := initOpenResponsePrivate(&hitResp)
		hitResp.Result = tfsdk.EphemeralResultData{Raw: state.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, req, &hitResp)
		if hitResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error when agent token present: %v", hitResp.Diagnostics)
		}

		// Existing active key on server: token set to null
		cli.RegisterMintedAgentKey("ag-1", "ak-existing", "")
		existModel := PaperclipAgentKeyEphemeralResourceModel{
			KeyID: types.StringValue("ak-existing"),
		}
		existState := tfsdk.State{Schema: schemaResp.Schema}
		_ = existState.Set(ctx, &existModel)
		existReq := ephemeral.OpenRequest{
			Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: existState.Raw},
		}
		var existResp ephemeral.OpenResponse
		existResp.Result = tfsdk.EphemeralResultData{Raw: existState.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, existReq, &existResp)
		if existResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error for existing agent key: %v", existResp.Diagnostics)
		}

		// Verify Close confirms delivery with native Private state
		cli.RecordTokenDeliverySuccess("tok-agent-val")
		var closeReq ephemeral.CloseRequest
		initCloseRequestPrivate(&closeReq, priv)
		var closeResp ephemeral.CloseResponse
		r.Close(ctx, closeReq, &closeResp)
		if closeResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error on close: %v", closeResp.Diagnostics)
		}
		if !cli.IsTokenDelivered("ak-defer") {
			t.Fatal("expected ak-defer to be marked delivered after Close")
		}

		// Close with unconfigured client is no-op
		unconfRes := NewPaperclipAgentKeyEphemeralResource().(*PaperclipAgentKeyEphemeralResource)
		var unconfCloseResp ephemeral.CloseResponse
		unconfRes.Close(ctx, ephemeral.CloseRequest{}, &unconfCloseResp)
		if unconfCloseResp.Diagnostics.HasError() {
			t.Fatal("expected no error on unconfigured close")
		}

		// Close with empty keyID is no-op
		emptyRes := NewPaperclipAgentKeyEphemeralResource().(*PaperclipAgentKeyEphemeralResource)
		emptyRes.client = cli
		var emptyCloseResp ephemeral.CloseResponse
		emptyRes.Close(ctx, ephemeral.CloseRequest{}, &emptyCloseResp)
		if emptyCloseResp.Diagnostics.HasError() {
			t.Fatal("expected no error on empty close")
		}

		// Close with error on ConfirmDelivery
		failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "server error", http.StatusInternalServerError)
		}))
		defer failServer.Close()
		failCli, _ := client.NewClient(failServer.URL, "test-token")
		failCli.MarkTokenDelivered("ak-fail-new")
		failCli.RecordSupersededKey("ak-fail-new", "ak-fail-old", false, "agent-fail")
		failRes := NewPaperclipAgentKeyEphemeralResource().(*PaperclipAgentKeyEphemeralResource)
		failRes.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: failCli}, &confResp)
		failRes.keyID = "ak-fail-new"
		var failCloseResp ephemeral.CloseResponse
		failRes.Close(ctx, ephemeral.CloseRequest{}, &failCloseResp)
		if !failCloseResp.Diagnostics.HasError() {
			t.Fatal("expected error diagnostic on failed close confirmation")
		}

		// Close with error on ReconcileFailedAgentKeyDelivery (unconfirmed)
		failCli.RegisterMintedAgentKey("agent-fail", "ak-fail-unconf", "tok-unconf")
		failRes.keyID = "ak-fail-unconf"
		var failUnconfResp ephemeral.CloseResponse
		failRes.Close(ctx, ephemeral.CloseRequest{}, &failUnconfResp)
		if !failUnconfResp.Diagnostics.HasError() {
			t.Fatal("expected error diagnostic on failed close unconfirmed reconciliation")
		}

		// KeyID is empty / null
		emptyKeyModel := PaperclipAgentKeyEphemeralResourceModel{
			KeyID: types.StringNull(),
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
			t.Fatal("expected error when key_id is empty")
		}

		// Empty KeyID with DeferralAllowed
		emptyReq.ClientCapabilities.DeferralAllowed = true
		var emptyDefResp ephemeral.OpenResponse
		emptyDefResp.Result = tfsdk.EphemeralResultData{Raw: emptyState.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, emptyReq, &emptyDefResp)
		if emptyDefResp.Deferred == nil {
			t.Fatal("expected deferred response when key_id empty and DeferralAllowed true")
		}
	})
}

func TestCredentialsResourcesModifyPlan(t *testing.T) {
	t.Setenv("PAPERCLIP_SCRATCH_DIR", t.TempDir())
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

		// KeyID empty falls back to ID
		mFallback := PaperclipBoardKeyResourceModel{
			ID:         types.StringValue("bk-fallback"),
			KeyID:      types.StringValue(""),
			Name:       types.StringValue("bk"),
			Generation: types.Int64Value(1),
		}
		var planFallback tfsdk.Plan = tfsdk.Plan{Schema: schema}
		_ = planFallback.Set(ctx, &mFallback)
		var stateFallback tfsdk.State = tfsdk.State{Schema: schema}
		_ = stateFallback.Set(ctx, &mFallback)
		var respFallback resource.ModifyPlanResponse
		respFallback.Plan = planFallback
		r.ModifyPlan(ctx, resource.ModifyPlanRequest{
			Plan:  planFallback,
			State: stateFallback,
		}, &respFallback)
		if respFallback.Diagnostics.HasError() {
			t.Fatal(respFallback.Diagnostics)
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

		// KeyID empty falls back to ID
		mAgentFallback := PaperclipAgentKeyResourceModel{
			ID:         types.StringValue("ak-fallback"),
			KeyID:      types.StringValue(""),
			AgentID:    types.StringValue("ag-1"),
			Name:       types.StringValue("ak"),
			Generation: types.Int64Value(1),
		}
		var planAgentFallback tfsdk.Plan = tfsdk.Plan{Schema: schema}
		_ = planAgentFallback.Set(ctx, &mAgentFallback)
		var stateAgentFallback tfsdk.State = tfsdk.State{Schema: schema}
		_ = stateAgentFallback.Set(ctx, &mAgentFallback)
		var respAgentFallback resource.ModifyPlanResponse
		respAgentFallback.Plan = planAgentFallback
		r.ModifyPlan(ctx, resource.ModifyPlanRequest{
			Plan:  planAgentFallback,
			State: stateAgentFallback,
		}, &respAgentFallback)
		if respAgentFallback.Diagnostics.HasError() {
			t.Fatal(respAgentFallback.Diagnostics)
		}
	})
}

func TestFailedDeliveryReconciliation(t *testing.T) {
	t.Setenv("PAPERCLIP_SCRATCH_DIR", t.TempDir())
	ctx := context.Background()

	var activeKeys = make(map[string]bool)
	var revokedKeys = make(map[string]bool)
	var createdCount int

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/board-api-keys":
			createdCount++
			keyID := fmt.Sprintf("bk-attempt-%d", createdCount)
			activeKeys[keyID] = true
			bk := client.PaperclipBoardKey{
				ID:    keyID,
				Name:  "delivery-reconcile-board-key",
				Token: fmt.Sprintf("secret-token-%s", keyID),
			}
			_ = json.NewEncoder(w).Encode(bk)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/board-api-keys/"):
			id := r.URL.Path[len("/api/board-api-keys/"):]
			delete(activeKeys, id)
			revokedKeys[id] = true
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/board-api-keys/"):
			id := r.URL.Path[len("/api/board-api-keys/"):]
			if !activeKeys[id] {
				http.NotFound(w, r)
				return
			}
			bk := client.PaperclipBoardKey{
				ID:   id,
				Name: "delivery-reconcile-board-key",
			}
			_ = json.NewEncoder(w).Encode(bk)
		case r.Method == http.MethodGet && r.URL.Path == "/api/board-api-keys":
			var list []client.PaperclipBoardKey
			for id := range activeKeys {
				list = append(list, client.PaperclipBoardKey{
					ID:   id,
					Name: "delivery-reconcile-board-key",
				})
			}
			_ = json.NewEncoder(w).Encode(list)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	// 1. Initial Create: mints key bk-attempt-1
	bkRes := NewPaperclipBoardKeyResource().(*PaperclipBoardKeyResource)
	schema := configureTestResource(t, server.URL, bkRes)

	model := PaperclipBoardKeyResourceModel{
		Name:       types.StringValue("delivery-reconcile-board-key"),
		Generation: types.Int64Value(1),
	}
	var plan tfsdk.Plan = tfsdk.Plan{Schema: schema}
	_ = plan.Set(ctx, &model)
	var createResp resource.CreateResponse
	createResp.State = tfsdk.State{Schema: schema}
	bkRes.Create(ctx, resource.CreateRequest{Plan: plan}, &createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("create failed: %v", createResp.Diagnostics)
	}

	var stateModel PaperclipBoardKeyResourceModel
	_ = createResp.State.Get(ctx, &stateModel)
	if stateModel.KeyID.ValueString() != "bk-attempt-1" || stateModel.DeliveryState.ValueString() != "delivered" {
		t.Fatalf("unexpected state after create: %+v", stateModel)
	}
	if !activeKeys["bk-attempt-1"] {
		t.Fatal("expected bk-attempt-1 active on server")
	}

	// 2. Ephemeral resource opens to deliver token
	ephRes := NewPaperclipBoardKeyEphemeralResource().(*PaperclipBoardKeyEphemeralResource)
	var ephSchemaResp ephemeral.SchemaResponse
	ephRes.Schema(ctx, ephemeral.SchemaRequest{}, &ephSchemaResp)
	ephSchema := ephSchemaResp.Schema
	var ephConfResp ephemeral.ConfigureResponse
	ephRes.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: bkRes.client}, &ephConfResp)

	ephModel := PaperclipBoardKeyEphemeralResourceModel{
		KeyID: types.StringValue("bk-attempt-1"),
	}
	var ephState tfsdk.State = tfsdk.State{Schema: ephSchema}
	_ = ephState.Set(ctx, &ephModel)
	ephReq := ephemeral.OpenRequest{
		Config: tfsdk.Config{Schema: ephSchema, Raw: ephState.Raw},
	}
	var ephOpenResp ephemeral.OpenResponse
	ephOpenResp.Result = tfsdk.EphemeralResultData{Raw: ephState.Raw, Schema: ephSchema}
	ephRes.Open(ctx, ephReq, &ephOpenResp)
	if ephOpenResp.Diagnostics.HasError() {
		t.Fatalf("eph Open failed: %v", ephOpenResp.Diagnostics)
	}

	// 3. Simulated downstream consumer failure! (e.g. Secret write failure)
	// Consumer records token delivery failure
	bkRes.client.RecordTokenDeliveryFailure("secret-token-bk-attempt-1")

	// 4. Ephemeral Close is invoked upon failure:
	// Because delivery failed / unconfirmed, Close revokes bk-attempt-1 on the server!
	var ephCloseResp ephemeral.CloseResponse
	ephRes.Close(ctx, ephemeral.CloseRequest{}, &ephCloseResp)
	if ephCloseResp.Diagnostics.HasError() {
		t.Fatalf("eph Close failed: %v", ephCloseResp.Diagnostics)
	}

	if !revokedKeys["bk-attempt-1"] || activeKeys["bk-attempt-1"] {
		t.Fatal("expected unconfirmed bk-attempt-1 to be revoked during failed delivery reconciliation")
	}
	if !bkRes.client.IsDeliveryFailed("bk-attempt-1") {
		t.Fatal("expected bk-attempt-1 marked as failed delivery")
	}

	// 5. Next execution / retry apply:
	// Read discovers that bk-attempt-1 was revoked on the server (404), so it removes the resource from state.
	var readResp resource.ReadResponse
	readResp.State = createResp.State
	bkRes.Read(ctx, resource.ReadRequest{State: createResp.State}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("read failed: %v", readResp.Diagnostics)
	}
	if !readResp.State.Raw.IsNull() {
		t.Fatal("expected state resource removed after key was revoked")
	}

	// 6. Because resource was removed, OpenTofu applies Create again -> mints replacement key bk-attempt-2
	var retryCreateResp resource.CreateResponse
	retryCreateResp.State = tfsdk.State{Schema: schema}
	bkRes.Create(ctx, resource.CreateRequest{Plan: plan}, &retryCreateResp)
	if retryCreateResp.Diagnostics.HasError() {
		t.Fatalf("retry create failed: %v", retryCreateResp.Diagnostics)
	}

	var stateAfterRetry PaperclipBoardKeyResourceModel
	_ = retryCreateResp.State.Get(ctx, &stateAfterRetry)
	if stateAfterRetry.KeyID.ValueString() != "bk-attempt-2" || stateAfterRetry.DeliveryState.ValueString() != "delivered" {
		t.Fatalf("unexpected state after retry create: %+v", stateAfterRetry)
	}
	if !activeKeys["bk-attempt-2"] {
		t.Fatal("expected bk-attempt-2 active on server")
	}

	// 7. Ephemeral Open delivers new token
	retryEphModel := PaperclipBoardKeyEphemeralResourceModel{
		KeyID: types.StringValue("bk-attempt-2"),
	}
	var retryEphState tfsdk.State = tfsdk.State{Schema: ephSchema}
	_ = retryEphState.Set(ctx, &retryEphModel)
	retryEphReq := ephemeral.OpenRequest{
		Config: tfsdk.Config{Schema: ephSchema, Raw: retryEphState.Raw},
	}
	var retryEphOpenResp ephemeral.OpenResponse
	retryEphOpenResp.Result = tfsdk.EphemeralResultData{Raw: retryEphState.Raw, Schema: ephSchema}
	ephRes.Open(ctx, retryEphReq, &retryEphOpenResp)
	if retryEphOpenResp.Diagnostics.HasError() {
		t.Fatalf("retry eph Open failed: %v", retryEphOpenResp.Diagnostics)
	}

	// 8. Downstream consumer succeeds:
	bkRes.client.RecordTokenDeliverySuccess("secret-token-bk-attempt-2")

	// 9. Ephemeral Close confirms delivery:
	var retryEphCloseResp ephemeral.CloseResponse
	ephRes.Close(ctx, ephemeral.CloseRequest{}, &retryEphCloseResp)
	if retryEphCloseResp.Diagnostics.HasError() {
		t.Fatalf("retry eph Close failed: %v", retryEphCloseResp.Diagnostics)
	}
	if !bkRes.client.IsTokenDelivered("bk-attempt-2") {
		t.Fatal("expected bk-attempt-2 marked delivered")
	}

	// 10. Read verifies bk-attempt-2 is active and delivered
	var finalReadResp resource.ReadResponse
	finalReadResp.State = retryCreateResp.State
	bkRes.Read(ctx, resource.ReadRequest{State: retryCreateResp.State}, &finalReadResp)
	if finalReadResp.Diagnostics.HasError() {
		t.Fatalf("final read failed: %v", finalReadResp.Diagnostics)
	}
	var finalModel PaperclipBoardKeyResourceModel
	_ = finalReadResp.State.Get(ctx, &finalModel)
	if finalModel.DeliveryState.ValueString() != "delivered" || finalModel.KeyID.ValueString() != "bk-attempt-2" {
		t.Fatalf("unexpected final model: %+v", finalModel)
	}

	// 11. Verify server state: exactly bk-attempt-2 active, bk-attempt-1 revoked, 0 orphaned keys
	if len(activeKeys) != 1 || !activeKeys["bk-attempt-2"] {
		t.Fatalf("unexpected active keys on server: %v", activeKeys)
	}
}

