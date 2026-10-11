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
	"github.com/hashicorp/terraform-plugin-framework/diag"
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

func initUpdateResponsePrivate(resp *resource.UpdateResponse) any {
	f, ok := reflect.TypeOf(resource.UpdateResponse{}).FieldByName("Private")
	if !ok {
		return nil
	}
	val := reflect.New(f.Type.Elem())
	reflect.ValueOf(resp).Elem().FieldByName("Private").Set(val)
	return val.Interface()
}

func initUpdateRequestPrivate(req *resource.UpdateRequest, private any) {
	if private == nil {
		return
	}
	f, ok := reflect.TypeOf(resource.UpdateRequest{}).FieldByName("Private")
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
	mockState.keys["bk-1"] = client.PaperclipBoardKey{
		ID:        "bk-1",
		Name:      "initial",
		ExpiresAt: &expFuture,
	}
	r.client.RegisterMintedKey("bk-1", "tok-1", "initial", true, "")
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

	// Read verifies delivery_state is preserved
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

	// Create - with pre-registered key in client
	r.client.RegisterMintedKey("bk-prereg", "tok-prereg", "prereg-name", true, "")
	preregModel := PaperclipBoardKeyResourceModel{
		Name:       types.StringValue("prereg-name"),
		Generation: types.Int64Value(1),
		ExpiresAt:  types.StringNull(),
	}
	preregPlan := tfsdk.Plan{Schema: schema}
	_ = preregPlan.Set(ctx, &preregModel)
	var preregResp resource.CreateResponse
	preregResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: preregPlan}, &preregResp)
	if preregResp.Diagnostics.HasError() {
		t.Fatalf("create with preregistered key failed: %v", preregResp.Diagnostics)
	}

	// Create - without expires_at
	r.client.RegisterMintedKey("bk-noexp", "tok-noexp", "no-exp", true, "")
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

	// Create - with explicit KeyID and Unknown ExpiresAt
	r.client.RegisterMintedKey("bk-explicit-keyid", "tok-exp", "explicit-keyid-name", true, "")
	expKeyIDModel := PaperclipBoardKeyResourceModel{
		ID:         types.StringNull(),
		KeyID:      types.StringValue("bk-explicit-keyid"),
		Name:       types.StringValue("explicit-keyid-name"),
		Generation: types.Int64Value(1),
		ExpiresAt:  types.StringUnknown(),
	}
	expKeyIDPlan := tfsdk.Plan{Schema: schema}
	_ = expKeyIDPlan.Set(ctx, &expKeyIDModel)
	var expKeyIDResp resource.CreateResponse
	expKeyIDResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: expKeyIDPlan}, &expKeyIDResp)
	if expKeyIDResp.Diagnostics.HasError() {
		t.Fatalf("create with explicit key_id failed: %v", expKeyIDResp.Diagnostics)
	}

	// Read - Success
	var readResp resource.ReadResponse
	readResp.State = createResp.State
	r.Read(ctx, resource.ReadRequest{State: createResp.State}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("unexpected read error: %v", readResp.Diagnostics)
	}
	_ = readResp.State.Get(ctx, &createdModel)
	if createdModel.DeliveryState.ValueString() != "delivered" {
		t.Fatalf("expected delivery_state to be delivered, got %s", createdModel.DeliveryState.ValueString())
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

	// Read - Not found removes resource
	pendingModel := createdModel
	pendingModel.DeliveryState = types.StringValue("delivered")
	pendingModel.KeyID = types.StringValue("bk-pending-rev")
	mockState.revokedKeys["bk-pending-rev"] = true
	var pendingState tfsdk.State = tfsdk.State{Schema: schema}
	_ = pendingState.Set(ctx, &pendingModel)
	var pendingReadResp resource.ReadResponse
	pendingReadResp.State = pendingState
	r.Read(ctx, resource.ReadRequest{State: pendingState}, &pendingReadResp)
	var pendingCheck PaperclipBoardKeyResourceModel
	_ = pendingReadResp.State.Get(ctx, &pendingCheck)
	if !pendingCheck.ID.IsNull() {
		t.Fatal("expected resource removal on not found")
	}
	delete(mockState.revokedKeys, "bk-pending-rev")

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
	r.client.RegisterMintedKey("bk-2", "tok-2", "rotated", true, "")

	// Update - Changed generation: revoke superseded key failure
	mockState.failDelete = true
	var failSupRotResp resource.UpdateResponse
	failSupRotResp.State = tfsdk.State{Schema: schema}
	r.Update(ctx, resource.UpdateRequest{Plan: rotPlan, State: createResp.State}, &failSupRotResp)
	if !failSupRotResp.Diagnostics.HasError() {
		t.Fatal("expected error on superseded key revoke failure during update")
	}
	mockState.failDelete = false

	// Update - Changed generation: reconcile unconfirmed keys failure
	r.client.RegisterMintedKey("bk-unconf", "tok", "rotated", true, "")
	mockState.failDelete = true
	var failRecRotResp resource.UpdateResponse
	failRecRotResp.State = tfsdk.State{Schema: schema}
	noOldState := tfsdk.State{Schema: schema}
	noOldModel := rotModel
	noOldModel.KeyID = types.StringNull()
	noOldModel.ID = types.StringNull()
	noOldModel.Generation = types.Int64Value(1)
	_ = noOldState.Set(ctx, &noOldModel)
	r.Update(ctx, resource.UpdateRequest{Plan: rotPlan, State: noOldState}, &failRecRotResp)
	if !failRecRotResp.Diagnostics.HasError() {
		t.Fatal("expected error on reconcile unconfirmed keys failure during update")
	}
	mockState.failDelete = false
	r.client.ClearMintedToken("bk-unconf")

	// Update - Changed generation: Success (revokes predecessor immediately)
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
	// Simulate ephemeral Close confirming delivery of bk-2
	var closeResp ephemeral.CloseResponse
	ephRes := NewPaperclipBoardKeyEphemeralResource().(*PaperclipBoardKeyEphemeralResource)
	ephRes.client = r.client
	var closeReq ephemeral.CloseRequest
	var openResp ephemeral.OpenResponse
	priv := initOpenResponsePrivate(&openResp)
	b, _ := json.Marshal("bk-2")
	_ = openResp.Private.SetKey(ctx, "key_id", b)
	initCloseRequestPrivate(&closeReq, priv)
	ephRes.Close(ctx, closeReq, &closeResp)
	if closeResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on close: %v", closeResp.Diagnostics)
	}

	// Update - Changed generation without expires_at and with KeyID empty in state
	r.client.RegisterMintedKey("bk-no-exp-3", "tok", "rotated-no-exp", true, "")
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

	// Update - With Private state tracking superseded key across generations
	r.client.RegisterMintedKey("bk-with-priv-4", "tok", "rot-with-priv", true, "")
	var privResp resource.UpdateResponse
	privResp.State = tfsdk.State{Schema: schema}
	privData := initUpdateResponsePrivate(&privResp)
	rotWithPrivPlan := tfsdk.Plan{Schema: schema}
	rotWithPrivModel := createdModel
	rotWithPrivModel.Name = types.StringValue("rot-with-priv")
	rotWithPrivModel.Generation = types.Int64Value(4)
	_ = rotWithPrivPlan.Set(ctx, &rotWithPrivModel)
	r.Update(ctx, resource.UpdateRequest{Plan: rotWithPrivPlan, State: rotUpdateResp.State}, &privResp)
	if privResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on rot with priv: %v", privResp.Diagnostics)
	}

	// Update - When KeyID is null and ID is null, but req.Private provides superseded_key_id
	var nullState tfsdk.State = tfsdk.State{Schema: schema}
	nullModel := createdModel
	nullModel.KeyID = types.StringNull()
	nullModel.ID = types.StringNull()
	_ = nullState.Set(ctx, &nullModel)
	var recovPlan tfsdk.Plan = tfsdk.Plan{Schema: schema}
	recovModel := createdModel
	recovModel.Generation = types.Int64Value(5)
	r.client.RegisterMintedKey("bk-recov-5", "tok", recovModel.Name.ValueString(), true, "")
	_ = recovPlan.Set(ctx, &recovModel)
	var recovReq resource.UpdateRequest
	recovReq.Plan = recovPlan
	recovReq.State = nullState
	initUpdateRequestPrivate(&recovReq, privData)
	var recovResp resource.UpdateResponse
	recovResp.State = tfsdk.State{Schema: schema}
	r.Update(ctx, recovReq, &recovResp)
	if recovResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on recovery update with private: %v", recovResp.Diagnostics)
	}

	// Update - with explicit plan.KeyID and Unknown ExpiresAt
	r.client.RegisterMintedKey("bk-upd-explicit", "tok-upd", "upd-explicit-name", true, "")
	updExpModel := createdModel
	updExpModel.Name = types.StringValue("upd-explicit-name")
	updExpModel.Generation = types.Int64Value(6)
	updExpModel.KeyID = types.StringValue("bk-upd-explicit")
	updExpModel.ExpiresAt = types.StringUnknown()
	updExpPlan := tfsdk.Plan{Schema: schema}
	_ = updExpPlan.Set(ctx, &updExpModel)
	var updExpResp resource.UpdateResponse
	updExpResp.State = tfsdk.State{Schema: schema}
	r.Update(ctx, resource.UpdateRequest{Plan: updExpPlan, State: rotUpdateResp.State}, &updExpResp)
	if updExpResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on update with explicit key_id: %v", updExpResp.Diagnostics)
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
			if body["name"] == "no-scope-key" {
				keyID = "ak-no-scope"
			} else if body["name"] == "rotated-agent-key" {
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
	mockState.keys["ak-1"] = client.PaperclipAgentKey{
		ID:      "ak-1",
		AgentID: "ag-1",
		Name:    "initial-agent-key",
		Token:   "tok-ak-1",
		Scope: &client.AgentKeyScope{
			Kind:      "standard",
			ProjectID: "proj-1",
		},
	}
	r.client.RegisterMintedKey("ak-1", "tok-1", "initial-agent-key", false, "ag-1")
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

	// Read verifies delivery_state is preserved
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

	// Create - with pre-registered key in client
	r.client.RegisterMintedKey("ak-prereg", "tok-ak-prereg", "ak-prereg-name", false, "ag-1")
	preregAkModel := PaperclipAgentKeyResourceModel{
		AgentID:    types.StringValue("ag-1"),
		Name:       types.StringValue("ak-prereg-name"),
		Generation: types.Int64Value(1),
	}
	preregAkPlan := tfsdk.Plan{Schema: schema}
	_ = preregAkPlan.Set(ctx, &preregAkModel)
	var preregAkResp resource.CreateResponse
	preregAkResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: preregAkPlan}, &preregAkResp)
	if preregAkResp.Diagnostics.HasError() {
		t.Fatalf("create with preregistered agent key failed: %v", preregAkResp.Diagnostics)
	}

	// Create - without scope
	r.client.RegisterMintedKey("ak-noscope", "tok-noscope", "no-scope-key", false, "ag-1")
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

	// Create - with explicit KeyID
	r.client.RegisterMintedKey("ak-explicit-keyid", "tok-ak-exp", "explicit-ak-name", false, "ag-1")
	expAkKeyIDModel := PaperclipAgentKeyResourceModel{
		ID:         types.StringNull(),
		KeyID:      types.StringValue("ak-explicit-keyid"),
		AgentID:    types.StringValue("ag-1"),
		Name:       types.StringValue("explicit-ak-name"),
		Generation: types.Int64Value(1),
	}
	expAkKeyIDPlan := tfsdk.Plan{Schema: schema}
	_ = expAkKeyIDPlan.Set(ctx, &expAkKeyIDModel)
	var expAkKeyIDResp resource.CreateResponse
	expAkKeyIDResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: expAkKeyIDPlan}, &expAkKeyIDResp)
	if expAkKeyIDResp.Diagnostics.HasError() {
		t.Fatalf("create agent key with explicit key_id failed: %v", expAkKeyIDResp.Diagnostics)
	}

	// Read - Success
	var readResp resource.ReadResponse
	readResp.State = createResp.State
	r.Read(ctx, resource.ReadRequest{State: createResp.State}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("unexpected read error: %v", readResp.Diagnostics)
	}
	_ = readResp.State.Get(ctx, &createdModel)
	if createdModel.DeliveryState.ValueString() != "delivered" {
		t.Fatalf("expected delivery_state to be delivered, got %s", createdModel.DeliveryState.ValueString())
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

	// Read - Empty agentID with non-empty keyID removes resource
	emptyAgOnlyState := tfsdk.State{Schema: schema}
	emptyAgOnlyModel := PaperclipAgentKeyResourceModel{
		KeyID:   types.StringValue("ak-1"),
		AgentID: types.StringNull(),
	}
	_ = emptyAgOnlyState.Set(ctx, &emptyAgOnlyModel)
	var emptyAgOnlyReadResp resource.ReadResponse
	emptyAgOnlyReadResp.State = emptyAgOnlyState
	r.Read(ctx, resource.ReadRequest{State: emptyAgOnlyState}, &emptyAgOnlyReadResp)
	var emptyAgOnlyCheck PaperclipAgentKeyResourceModel
	_ = emptyAgOnlyReadResp.State.Get(ctx, &emptyAgOnlyCheck)
	if !emptyAgOnlyCheck.ID.IsNull() {
		t.Fatal("expected resource removal on empty agentID")
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

	// Read - Not found removes resource
	pendingAgentModel := createdModel
	pendingAgentModel.DeliveryState = types.StringValue("delivered")
	pendingAgentModel.KeyID = types.StringValue("ak-pending-rev")
	mockState.revokedKeys["ak-pending-rev"] = true
	var pendingAgentState tfsdk.State = tfsdk.State{Schema: schema}
	_ = pendingAgentState.Set(ctx, &pendingAgentModel)
	var pendingAgentReadResp resource.ReadResponse
	pendingAgentReadResp.State = pendingAgentState
	r.Read(ctx, resource.ReadRequest{State: pendingAgentState}, &pendingAgentReadResp)
	var pendingAgentCheck PaperclipAgentKeyResourceModel
	_ = pendingAgentReadResp.State.Get(ctx, &pendingAgentCheck)
	if !pendingAgentCheck.ID.IsNull() {
		t.Fatal("expected resource removal on not found")
	}
	delete(mockState.revokedKeys, "ak-pending-rev")

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
	r.client.RegisterMintedKey("ak-2", "tok-2", "rotated-agent-key", false, "ag-1")

	// Update - Changed generation: revoke superseded key failure
	mockState.failDelete = true
	var failSupAgentRotResp resource.UpdateResponse
	failSupAgentRotResp.State = tfsdk.State{Schema: schema}
	r.Update(ctx, resource.UpdateRequest{Plan: rotPlan, State: createResp.State}, &failSupAgentRotResp)
	if !failSupAgentRotResp.Diagnostics.HasError() {
		t.Fatal("expected error on superseded agent key revoke failure during update")
	}
	mockState.failDelete = false

	// Update - Changed generation: reconcile unconfirmed keys failure
	r.client.RegisterMintedKey("ak-unconf", "tok", "rotated-agent-key", false, "ag-1")
	mockState.failDelete = true
	var failRecAgentRotResp resource.UpdateResponse
	failRecAgentRotResp.State = tfsdk.State{Schema: schema}
	noOldAgState := tfsdk.State{Schema: schema}
	noOldAgModel := rotModel
	noOldAgModel.KeyID = types.StringNull()
	noOldAgModel.ID = types.StringNull()
	noOldAgModel.Generation = types.Int64Value(1)
	_ = noOldAgState.Set(ctx, &noOldAgModel)
	r.Update(ctx, resource.UpdateRequest{Plan: rotPlan, State: noOldAgState}, &failRecAgentRotResp)
	if !failRecAgentRotResp.Diagnostics.HasError() {
		t.Fatal("expected error on reconcile unconfirmed agent keys failure during update")
	}
	mockState.failDelete = false
	r.client.ClearMintedToken("ak-unconf")

	// Update - Changed generation: Success (revokes predecessor immediately)
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
	// Simulate ephemeral Close confirming delivery of ak-2
	var closeResp ephemeral.CloseResponse
	ephRes := NewPaperclipAgentKeyEphemeralResource().(*PaperclipAgentKeyEphemeralResource)
	ephRes.client = r.client
	var closeReq ephemeral.CloseRequest
	var openResp ephemeral.OpenResponse
	priv := initOpenResponsePrivate(&openResp)
	b, _ := json.Marshal("ak-2")
	_ = openResp.Private.SetKey(ctx, "key_id", b)
	agB, _ := json.Marshal("ag-1")
	_ = openResp.Private.SetKey(ctx, "agent_id", agB)
	initCloseRequestPrivate(&closeReq, priv)
	ephRes.Close(ctx, closeReq, &closeResp)
	if closeResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on close: %v", closeResp.Diagnostics)
	}

	// Update - Changed generation with KeyID empty in state
	r.client.RegisterMintedKey("ak-no-keyid-3", "tok", "rotated-ak-no-keyid", false, "ag-1")
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

	// Update - With Private state tracking superseded key across generations
	r.client.RegisterMintedKey("ak-with-priv-4", "tok", "rot-ak-with-priv", false, "ag-1")
	var privAgentResp resource.UpdateResponse
	privAgentResp.State = tfsdk.State{Schema: schema}
	privAgentData := initUpdateResponsePrivate(&privAgentResp)
	rotWithPrivAgentPlan := tfsdk.Plan{Schema: schema}
	rotWithPrivAgentModel := createdModel
	rotWithPrivAgentModel.Name = types.StringValue("rot-ak-with-priv")
	rotWithPrivAgentModel.Generation = types.Int64Value(4)
	_ = rotWithPrivAgentPlan.Set(ctx, &rotWithPrivAgentModel)
	r.Update(ctx, resource.UpdateRequest{Plan: rotWithPrivAgentPlan, State: rotUpdateResp.State}, &privAgentResp)
	if privAgentResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on rot ak with priv: %v", privAgentResp.Diagnostics)
	}

	// Update - When KeyID is null and ID is null, but req.Private provides superseded_key_id
	var nullAgentState tfsdk.State = tfsdk.State{Schema: schema}
	nullAgentModel := createdModel
	nullAgentModel.KeyID = types.StringNull()
	nullAgentModel.ID = types.StringNull()
	_ = nullAgentState.Set(ctx, &nullAgentModel)
	var recovAgentPlan tfsdk.Plan = tfsdk.Plan{Schema: schema}
	recovAgentModel := createdModel
	recovAgentModel.Generation = types.Int64Value(5)
	r.client.RegisterMintedKey("ak-recov-5", "tok", recovAgentModel.Name.ValueString(), false, "ag-1")
	_ = recovAgentPlan.Set(ctx, &recovAgentModel)
	var recovAgentReq resource.UpdateRequest
	recovAgentReq.Plan = recovAgentPlan
	recovAgentReq.State = nullAgentState
	initUpdateRequestPrivate(&recovAgentReq, privAgentData)
	var recovAgentResp resource.UpdateResponse
	recovAgentResp.State = tfsdk.State{Schema: schema}
	r.Update(ctx, recovAgentReq, &recovAgentResp)
	if recovAgentResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on recovery update with private: %v", recovAgentResp.Diagnostics)
	}

	// Update - with explicit plan.KeyID
	r.client.RegisterMintedKey("ak-upd-explicit", "tok-ak-upd", "upd-explicit-agent-name", false, "ag-1")
	updAkExpModel := createdModel
	updAkExpModel.AgentID = types.StringValue("ag-1")
	updAkExpModel.Name = types.StringValue("upd-explicit-agent-name")
	updAkExpModel.Generation = types.Int64Value(6)
	updAkExpModel.KeyID = types.StringValue("ak-upd-explicit")
	updAkExpPlan := tfsdk.Plan{Schema: schema}
	_ = updAkExpPlan.Set(ctx, &updAkExpModel)
	var updAkExpResp resource.UpdateResponse
	updAkExpResp.State = tfsdk.State{Schema: schema}
	r.Update(ctx, resource.UpdateRequest{Plan: updAkExpPlan, State: rotUpdateResp.State}, &updAkExpResp)
	if updAkExpResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on agent key update with explicit key_id: %v", updAkExpResp.Diagnostics)
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

		// Token present in client registry
		cli.RegisterMintedToken("bk-defer", "tok-defer-val")
		cli.AcknowledgeKeyDelivery("bk-defer")
		var hitResp ephemeral.OpenResponse
		priv := initOpenResponsePrivate(&hitResp)
		hitResp.Result = tfsdk.EphemeralResultData{Raw: state.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, req, &hitResp)
		if hitResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error when token present: %v", hitResp.Diagnostics)
		}

		// Existing delivered key in durable state: token set to null
		cli.RecordDeliveredGeneration("bk-existing", true, "", 1, "bk-existing")
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

		// Verify Close clears token with native Private state when delivery acknowledged
		var closeReq ephemeral.CloseRequest
		initCloseRequestPrivate(&closeReq, priv)
		var closeResp ephemeral.CloseResponse
		r.Close(ctx, closeReq, &closeResp)
		if closeResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error on close: %v", closeResp.Diagnostics)
		}
		if _, ok := cli.GetMintedToken("bk-defer"); ok {
			t.Fatal("expected bk-defer token to be cleared after Close")
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

		// Close with error on RevokeBoardKey
		failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "server error", http.StatusInternalServerError)
		}))
		defer failServer.Close()
		failCli, _ := client.NewClient(failServer.URL, "test-token")
		failRes := NewPaperclipBoardKeyEphemeralResource().(*PaperclipBoardKeyEphemeralResource)
		failRes.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: failCli}, &confResp)
		var failOpenResp ephemeral.OpenResponse
		privFail := initOpenResponsePrivate(&failOpenResp)
		failBkBytes, _ := json.Marshal("bk-fail-new")
		_ = failOpenResp.Private.SetKey(ctx, "key_id", failBkBytes)
		var failCloseReq ephemeral.CloseRequest
		initCloseRequestPrivate(&failCloseReq, privFail)
		var failCloseResp ephemeral.CloseResponse
		failRes.Close(ctx, failCloseReq, &failCloseResp)
		if !failCloseResp.Diagnostics.HasError() {
			t.Fatal("expected error diagnostic on failed close revoking unconfirmed board key")
		}

		// Close when delivery not acknowledged: unconfirmed newly-minted key is revoked
		goodServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete && r.URL.Path == "/api/board-api-keys/bk-del-fail" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			http.NotFound(w, r)
		}))
		defer goodServer.Close()
		goodCli, _ := client.NewClient(goodServer.URL, "test-token")
		goodCli.RegisterMintedKey("bk-del-fail", "tok", "bk-name", true, "")
		delFailRes := NewPaperclipBoardKeyEphemeralResource().(*PaperclipBoardKeyEphemeralResource)
		delFailRes.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: goodCli}, &confResp)
		var delFailOpenResp ephemeral.OpenResponse
		privDelFail := initOpenResponsePrivate(&delFailOpenResp)
		delBkBytes, _ := json.Marshal("bk-del-fail")
		_ = delFailOpenResp.Private.SetKey(ctx, "key_id", delBkBytes)
		var delFailCloseReq ephemeral.CloseRequest
		initCloseRequestPrivate(&delFailCloseReq, privDelFail)
		var delFailCloseResp ephemeral.CloseResponse
		delFailRes.Close(ctx, delFailCloseReq, &delFailCloseResp)
		if delFailCloseResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error on failed delivery close: %v", delFailCloseResp.Diagnostics)
		}
		if goodCli.IsKeyDeliveryAcknowledged("bk-del-fail") {
			t.Fatal("failed key should not be marked delivered")
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

		// Token present in client registry with agentID
		cli.RegisterMintedAgentKey("ag-defer", "ak-defer", "tok-agent-val")
		cli.AcknowledgeKeyDelivery("ak-defer")
		var hitResp ephemeral.OpenResponse
		priv := initOpenResponsePrivate(&hitResp)
		hitResp.Result = tfsdk.EphemeralResultData{Raw: state.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, req, &hitResp)
		if hitResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error when agent token present: %v", hitResp.Diagnostics)
		}

		// Existing delivered key in durable state: token set to null
		cli.RecordDeliveredGeneration("ak-existing", false, "ag-1", 1, "ak-existing")
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

		// Verify Close clears token with native Private state when delivery acknowledged
		var closeReq ephemeral.CloseRequest
		initCloseRequestPrivate(&closeReq, priv)
		var closeResp ephemeral.CloseResponse
		r.Close(ctx, closeReq, &closeResp)
		if closeResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error on close: %v", closeResp.Diagnostics)
		}
		if _, ok := cli.GetMintedToken("ak-defer"); ok {
			t.Fatal("expected ak-defer token to be cleared after Close")
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

		// Close with error on RevokeAgentKey
		failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "server error", http.StatusInternalServerError)
		}))
		defer failServer.Close()
		failCli, _ := client.NewClient(failServer.URL, "test-token")
		failCli.RegisterMintedAgentKey("agent-fail", "ak-fail-new", "tok-fail")
		failRes := NewPaperclipAgentKeyEphemeralResource().(*PaperclipAgentKeyEphemeralResource)
		failRes.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: failCli}, &confResp)
		var failOpenResp ephemeral.OpenResponse
		privFail := initOpenResponsePrivate(&failOpenResp)
		failAkBytes, _ := json.Marshal("ak-fail-new")
		_ = failOpenResp.Private.SetKey(ctx, "key_id", failAkBytes)
		failAgBytes, _ := json.Marshal("agent-fail")
		_ = failOpenResp.Private.SetKey(ctx, "agent_id", failAgBytes)
		var failCloseReq ephemeral.CloseRequest
		initCloseRequestPrivate(&failCloseReq, privFail)
		var failCloseResp ephemeral.CloseResponse
		failRes.Close(ctx, failCloseReq, &failCloseResp)
		if !failCloseResp.Diagnostics.HasError() {
			t.Fatal("expected error diagnostic on failed close revoking unconfirmed agent key")
		}

		// Close when delivery not acknowledged: unconfirmed newly-minted key is revoked
		goodAkServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodDelete && r.URL.Path == "/api/agents/agent-1/keys/ak-del-fail" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			http.NotFound(w, r)
		}))
		defer goodAkServer.Close()
		goodAkCli, _ := client.NewClient(goodAkServer.URL, "test-token")
		goodAkCli.RegisterMintedKey("ak-del-fail", "tok-1", "ak-name", false, "agent-1")
		delFailAkRes := NewPaperclipAgentKeyEphemeralResource().(*PaperclipAgentKeyEphemeralResource)
		delFailAkRes.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: goodAkCli}, &confResp)
		var delFailAkOpenResp ephemeral.OpenResponse
		privDelFailAk := initOpenResponsePrivate(&delFailAkOpenResp)
		delAkBytes, _ := json.Marshal("ak-del-fail")
		_ = delFailAkOpenResp.Private.SetKey(ctx, "key_id", delAkBytes)
		delAgBytes, _ := json.Marshal("agent-1")
		_ = delFailAkOpenResp.Private.SetKey(ctx, "agent_id", delAgBytes)
		var delFailAkCloseReq ephemeral.CloseRequest
		initCloseRequestPrivate(&delFailAkCloseReq, privDelFailAk)
		var delFailAkCloseResp ephemeral.CloseResponse
		delFailAkRes.Close(ctx, delFailAkCloseReq, &delFailAkCloseResp)
		if delFailAkCloseResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error on failed delivery agent close: %v", delFailAkCloseResp.Diagnostics)
		}
		if goodAkCli.IsKeyDeliveryAcknowledged("ak-del-fail") {
			t.Fatal("failed key should not be marked delivered")
		}

		// Close when delivery failed with empty agentID
		delNoAgentRes := NewPaperclipAgentKeyEphemeralResource().(*PaperclipAgentKeyEphemeralResource)
		delNoAgentRes.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: goodAkCli}, &confResp)
		var delNoAgentOpenResp ephemeral.OpenResponse
		privNoAg := initOpenResponsePrivate(&delNoAgentOpenResp)
		noAgBytes, _ := json.Marshal("ak-no-agent")
		_ = delNoAgentOpenResp.Private.SetKey(ctx, "key_id", noAgBytes)
		var noAgentCloseReq ephemeral.CloseRequest
		initCloseRequestPrivate(&noAgentCloseReq, privNoAg)
		var noAgentCloseResp ephemeral.CloseResponse
		delNoAgentRes.Close(ctx, noAgentCloseReq, &noAgentCloseResp)
		if noAgentCloseResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error on close without agentID: %v", noAgentCloseResp.Diagnostics)
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

func TestGenerationRotationAndRevocationLifecycle(t *testing.T) {
	t.Setenv("PAPERCLIP_SCRATCH_DIR", t.TempDir())
	ctx := context.Background()

	var activeKeys = make(map[string]bool)
	var revokedKeys = make(map[string]bool)
	var createdCount = 1

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
	bkRes.client.RegisterMintedKey("bk-attempt-1", "tok-attempt-1", "delivery-reconcile-board-key", true, "")
	activeKeys["bk-attempt-1"] = true
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

	// 3. Ephemeral Close clears token after acknowledged delivery
	var ephCloseResp ephemeral.CloseResponse
	ephRes.Close(ctx, ephemeral.CloseRequest{}, &ephCloseResp)
	if ephCloseResp.Diagnostics.HasError() {
		t.Fatalf("eph Close failed: %v", ephCloseResp.Diagnostics)
	}

	// 4. Read confirms delivered state
	var readResp resource.ReadResponse
	readResp.State = createResp.State
	bkRes.Read(ctx, resource.ReadRequest{State: createResp.State}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("read failed: %v", readResp.Diagnostics)
	}
	var readModel PaperclipBoardKeyResourceModel
	_ = readResp.State.Get(ctx, &readModel)
	if readModel.DeliveryState.ValueString() != "delivered" {
		t.Fatalf("expected delivered after read, got %s", readModel.DeliveryState.ValueString())
	}

	// 5. Ephemeral Open for Generation 2 mints new key during apply
	ephModel2 := PaperclipBoardKeyEphemeralResourceModel{
		Name:       types.StringValue("delivery-reconcile-board-key"),
		Generation: types.Int64Value(2),
	}
	var ephState2 tfsdk.State = tfsdk.State{Schema: ephSchema}
	_ = ephState2.Set(ctx, &ephModel2)
	ephReq2 := ephemeral.OpenRequest{
		Config: tfsdk.Config{Schema: ephSchema, Raw: ephState2.Raw},
	}
	var ephOpenResp2 ephemeral.OpenResponse
	ephOpenResp2.Result = tfsdk.EphemeralResultData{Raw: ephState2.Raw, Schema: ephSchema}
	ephRes.Open(ctx, ephReq2, &ephOpenResp2)
	if ephOpenResp2.Diagnostics.HasError() {
		t.Fatalf("eph Open 2 failed: %v", ephOpenResp2.Diagnostics)
	}
	var ephResultModel2 PaperclipBoardKeyEphemeralResourceModel
	_ = ephOpenResp2.Result.Get(ctx, &ephResultModel2)
	if ephResultModel2.KeyID.ValueString() != "bk-attempt-2" {
		t.Fatalf("expected bk-attempt-2 minted, got %s", ephResultModel2.KeyID.ValueString())
	}

	// 6. Managed resource Update executes after consumer apply: revokes predecessor bk-attempt-1
	rotModel := readModel
	rotModel.Generation = types.Int64Value(2)
	var rotPlan tfsdk.Plan = tfsdk.Plan{Schema: schema}
	_ = rotPlan.Set(ctx, &rotModel)
	var rotUpdateResp resource.UpdateResponse
	rotUpdateResp.State = tfsdk.State{Schema: schema}
	bkRes.Update(ctx, resource.UpdateRequest{Plan: rotPlan, State: readResp.State}, &rotUpdateResp)
	if rotUpdateResp.Diagnostics.HasError() {
		t.Fatalf("rotation update failed: %v", rotUpdateResp.Diagnostics)
	}
	var rotState PaperclipBoardKeyResourceModel
	_ = rotUpdateResp.State.Get(ctx, &rotState)
	if rotState.KeyID.ValueString() != "bk-attempt-2" || rotState.DeliveryState.ValueString() != "delivered" {
		t.Fatalf("unexpected state after rotation update: %+v", rotState)
	}
	if !revokedKeys["bk-attempt-1"] {
		t.Fatal("expected bk-attempt-1 revoked immediately by Update")
	}

	// 7. Ephemeral Close marks bk-attempt-2 delivered and clears token
	var ephCloseResp2 ephemeral.CloseResponse
	ephRes.Close(ctx, ephemeral.CloseRequest{}, &ephCloseResp2)
	if ephCloseResp2.Diagnostics.HasError() {
		t.Fatalf("eph Close 2 failed: %v", ephCloseResp2.Diagnostics)
	}
	if !bkRes.client.IsKeyDeliveryAcknowledged("bk-attempt-2") {
		t.Fatal("expected bk-attempt-2 delivery acknowledged")
	}

	// 8. Read verifies bk-attempt-2 is active and delivered
	var finalReadResp resource.ReadResponse
	finalReadResp.State = rotUpdateResp.State
	bkRes.Read(ctx, resource.ReadRequest{State: rotUpdateResp.State}, &finalReadResp)
	if finalReadResp.Diagnostics.HasError() {
		t.Fatalf("final read failed: %v", finalReadResp.Diagnostics)
	}
	var finalModel PaperclipBoardKeyResourceModel
	_ = finalReadResp.State.Get(ctx, &finalModel)
	if finalModel.DeliveryState.ValueString() != "delivered" || finalModel.KeyID.ValueString() != "bk-attempt-2" {
		t.Fatalf("unexpected final model: %+v", finalModel)
	}

	// 9. Server state: exactly bk-attempt-2 active, bk-attempt-1 revoked
	if len(activeKeys) != 1 || !activeKeys["bk-attempt-2"] {
		t.Fatalf("unexpected active keys on server: %v", activeKeys)
	}
}

func TestEphemeralMintingApplyAndErrorCoverage(t *testing.T) {
	ctx := context.Background()

	var failCreateBoard bool
	var failCreateAgent bool
	expFuture := time.Now().Add(24 * time.Hour).Truncate(time.Second)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/board-api-keys"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]client.PaperclipBoardKey{})
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/api/agents/") && strings.HasSuffix(r.URL.Path, "/keys"):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode([]client.PaperclipAgentKey{})
		case r.Method == http.MethodPost && r.URL.Path == "/api/board-api-keys":
			if failCreateBoard {
				http.Error(w, "server error", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(client.PaperclipBoardKey{
				ID:        "bk-minted-new",
				Name:      "test-bk",
				Token:     "tok-bk-new",
				ExpiresAt: &expFuture,
			})
		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/agents/") && strings.HasSuffix(r.URL.Path, "/keys"):
			if failCreateAgent {
				http.Error(w, "server error", http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(client.PaperclipAgentKey{
				ID:      "ak-minted-new",
				AgentID: "ag-test",
				Name:    "test-ak",
				Token:   "tok-ak-new",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cli, err := client.NewClient(server.URL, "test-token")
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	// 1. Board Key Ephemeral Resource Coverage
	t.Run("BoardKey Ephemeral Open coverage", func(t *testing.T) {
		r := NewPaperclipBoardKeyEphemeralResource().(*PaperclipBoardKeyEphemeralResource)
		var schemaResp ephemeral.SchemaResponse
		r.Schema(ctx, ephemeral.SchemaRequest{}, &schemaResp)
		var confResp ephemeral.ConfigureResponse
		r.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: cli}, &confResp)

		// A. Generation already delivered
		cli.RecordDeliveredGeneration("bk-del-gen", true, "", 1, "bk-del-k1")
		cli.RegisterMintedKey("bk-del-k1", "tok", "bk-del-gen", true, "")
		delModel := PaperclipBoardKeyEphemeralResourceModel{
			Name:       types.StringValue("bk-del-gen"),
			Generation: types.Int64Value(1),
		}
		delState := tfsdk.State{Schema: schemaResp.Schema}
		_ = delState.Set(ctx, &delModel)
		var delResp ephemeral.OpenResponse
		delResp.Result = tfsdk.EphemeralResultData{Raw: delState.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, ephemeral.OpenRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: delState.Raw}}, &delResp)
		if delResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error on delivered generation: %v", delResp.Diagnostics)
		}
		var delResModel PaperclipBoardKeyEphemeralResourceModel
		_ = delResp.Result.Get(ctx, &delResModel)
		if !delResModel.Token.IsNull() {
			t.Fatal("expected token to be null for delivered generation")
		}
		if delResModel.KeyID.ValueString() != "bk-del-k1" {
			t.Fatalf("expected keyID bk-del-k1, got %s", delResModel.KeyID.ValueString())
		}

		// B. Defer during plan with Name set
		planModel := PaperclipBoardKeyEphemeralResourceModel{
			Name:       types.StringValue("bk-plan"),
			Generation: types.Int64Value(2),
		}
		planState := tfsdk.State{Schema: schemaResp.Schema}
		_ = planState.Set(ctx, &planModel)
		var planResp ephemeral.OpenResponse
		planResp.Result = tfsdk.EphemeralResultData{Raw: planState.Raw, Schema: schemaResp.Schema}
		planReq := ephemeral.OpenRequest{
			Config:             tfsdk.Config{Schema: schemaResp.Schema, Raw: planState.Raw},
			ClientCapabilities: ephemeral.OpenClientCapabilities{DeferralAllowed: true},
		}
		r.Open(ctx, planReq, &planResp)
		if planResp.Deferred == nil {
			t.Fatal("expected deferred response during plan for board key")
		}

		// B2. Defer when applying is false (e.g. tofu.applying)
		planApplyingFalseModel := PaperclipBoardKeyEphemeralResourceModel{
			Name:       types.StringValue("bk-plan-false"),
			Generation: types.Int64Value(2),
			Applying:   types.BoolValue(false),
		}
		planApplyingFalseState := tfsdk.State{Schema: schemaResp.Schema}
		_ = planApplyingFalseState.Set(ctx, &planApplyingFalseModel)
		var planApplyingFalseResp ephemeral.OpenResponse
		planApplyingFalseResp.Result = tfsdk.EphemeralResultData{Raw: planApplyingFalseState.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, ephemeral.OpenRequest{
			Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: planApplyingFalseState.Raw},
		}, &planApplyingFalseResp)
		var planApplyingFalseResModel PaperclipBoardKeyEphemeralResourceModel
		_ = planApplyingFalseResp.Result.Get(ctx, &planApplyingFalseResModel)
		if !planApplyingFalseResModel.Token.IsNull() {
			t.Fatalf("expected token to be null when applying is false and deferral disallowed, got %q", planApplyingFalseResModel.Token.ValueString())
		}
		var planApplyingFalseDefResp ephemeral.OpenResponse
		planApplyingFalseDefResp.Result = tfsdk.EphemeralResultData{Raw: planApplyingFalseState.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, ephemeral.OpenRequest{
			Config:             tfsdk.Config{Schema: schemaResp.Schema, Raw: planApplyingFalseState.Raw},
			ClientCapabilities: ephemeral.OpenClientCapabilities{DeferralAllowed: true},
		}, &planApplyingFalseDefResp)
		if planApplyingFalseDefResp.Deferred == nil {
			t.Fatal("expected deferred response when applying is false and deferral allowed")
		}

		// C. Apply time: invalid expires_at
		invExpModel := PaperclipBoardKeyEphemeralResourceModel{
			Name:       types.StringValue("bk-inv-exp"),
			Generation: types.Int64Value(1),
			ExpiresAt:  types.StringValue("not-rfc3339"),
		}
		invExpState := tfsdk.State{Schema: schemaResp.Schema}
		_ = invExpState.Set(ctx, &invExpModel)
		var invExpResp ephemeral.OpenResponse
		invExpResp.Result = tfsdk.EphemeralResultData{Raw: invExpState.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, ephemeral.OpenRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: invExpState.Raw}}, &invExpResp)
		if !invExpResp.Diagnostics.HasError() {
			t.Fatal("expected error diagnostic for invalid expires_at format")
		}

		// D. Apply time: server error on CreateBoardKey
		failCreateBoard = true
		validModel := PaperclipBoardKeyEphemeralResourceModel{
			Name:               types.StringValue("test-bk"),
			Generation:         types.Int64Value(1),
			ExpiresAt:          types.StringValue(expFuture.Format(time.RFC3339)),
			RequestedCompanyID: types.StringValue("comp-1"),
		}
		validState := tfsdk.State{Schema: schemaResp.Schema}
		_ = validState.Set(ctx, &validModel)
		var failResp ephemeral.OpenResponse
		failResp.Result = tfsdk.EphemeralResultData{Raw: validState.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, ephemeral.OpenRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: validState.Raw}}, &failResp)
		if !failResp.Diagnostics.HasError() {
			t.Fatal("expected error diagnostic on server failure during board key minting")
		}
		failCreateBoard = false

		// E. Apply time: success
		var succResp ephemeral.OpenResponse
		initOpenResponsePrivate(&succResp)
		succResp.Result = tfsdk.EphemeralResultData{Raw: validState.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, ephemeral.OpenRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: validState.Raw}}, &succResp)
		if succResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error on successful board key minting: %v", succResp.Diagnostics)
		}
		var succModel PaperclipBoardKeyEphemeralResourceModel
		_ = succResp.Result.Get(ctx, &succModel)
		if succModel.KeyID.ValueString() != "bk-minted-new" || succModel.Token.ValueString() != "tok-bk-new" {
			t.Fatalf("unexpected minted model: %+v", succModel)
		}
	})

	// 2. Agent Key Ephemeral Resource Coverage
	t.Run("AgentKey Ephemeral Open coverage", func(t *testing.T) {
		r := NewPaperclipAgentKeyEphemeralResource().(*PaperclipAgentKeyEphemeralResource)
		var schemaResp ephemeral.SchemaResponse
		r.Schema(ctx, ephemeral.SchemaRequest{}, &schemaResp)
		var confResp ephemeral.ConfigureResponse
		r.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: cli}, &confResp)

		// A. Empty name and agent_id without deferral -> error diagnostic
		emptyModel := PaperclipAgentKeyEphemeralResourceModel{
			Name:    types.StringNull(),
			AgentID: types.StringNull(),
		}
		emptyState := tfsdk.State{Schema: schemaResp.Schema}
		_ = emptyState.Set(ctx, &emptyModel)
		var emptyResp ephemeral.OpenResponse
		emptyResp.Result = tfsdk.EphemeralResultData{Raw: emptyState.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, ephemeral.OpenRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: emptyState.Raw}}, &emptyResp)
		if !emptyResp.Diagnostics.HasError() {
			t.Fatal("expected error diagnostic when name and agent_id empty without deferral")
		}

		// Empty name and agent_id with deferral -> deferred
		var emptyDefResp ephemeral.OpenResponse
		emptyDefResp.Result = tfsdk.EphemeralResultData{Raw: emptyState.Raw, Schema: schemaResp.Schema}
		emptyDefReq := ephemeral.OpenRequest{
			Config:             tfsdk.Config{Schema: schemaResp.Schema, Raw: emptyState.Raw},
			ClientCapabilities: ephemeral.OpenClientCapabilities{DeferralAllowed: true},
		}
		r.Open(ctx, emptyDefReq, &emptyDefResp)
		if emptyDefResp.Deferred == nil {
			t.Fatal("expected deferred response when name and agent_id empty with deferral allowed")
		}

		// B. Generation already delivered
		cli.RecordDeliveredGeneration("ak-del-gen", false, "ag-test", 1, "ak-del-k1")
		cli.RegisterMintedKey("ak-del-k1", "tok", "ak-del-gen", false, "ag-test")
		delModel := PaperclipAgentKeyEphemeralResourceModel{
			Name:       types.StringValue("ak-del-gen"),
			AgentID:    types.StringValue("ag-test"),
			Generation: types.Int64Value(1),
		}
		delState := tfsdk.State{Schema: schemaResp.Schema}
		_ = delState.Set(ctx, &delModel)
		var delResp ephemeral.OpenResponse
		delResp.Result = tfsdk.EphemeralResultData{Raw: delState.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, ephemeral.OpenRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: delState.Raw}}, &delResp)
		if delResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error on delivered agent key generation: %v", delResp.Diagnostics)
		}
		var delResModel PaperclipAgentKeyEphemeralResourceModel
		_ = delResp.Result.Get(ctx, &delResModel)
		if !delResModel.Token.IsNull() {
			t.Fatal("expected token to be null for delivered agent key generation")
		}
		if delResModel.KeyID.ValueString() != "ak-del-k1" {
			t.Fatalf("expected keyID ak-del-k1, got %s", delResModel.KeyID.ValueString())
		}

		// C. Defer during plan with Name and AgentID set
		planModel := PaperclipAgentKeyEphemeralResourceModel{
			Name:       types.StringValue("ak-plan"),
			AgentID:    types.StringValue("ag-test"),
			Generation: types.Int64Value(2),
		}
		planState := tfsdk.State{Schema: schemaResp.Schema}
		_ = planState.Set(ctx, &planModel)
		var planResp ephemeral.OpenResponse
		planResp.Result = tfsdk.EphemeralResultData{Raw: planState.Raw, Schema: schemaResp.Schema}
		planReq := ephemeral.OpenRequest{
			Config:             tfsdk.Config{Schema: schemaResp.Schema, Raw: planState.Raw},
			ClientCapabilities: ephemeral.OpenClientCapabilities{DeferralAllowed: true},
		}
		r.Open(ctx, planReq, &planResp)
		if planResp.Deferred == nil {
			t.Fatal("expected deferred response during plan for agent key")
		}

		// B2. Defer when applying is false (e.g. tofu.applying)
		planApplyingFalseModel := PaperclipAgentKeyEphemeralResourceModel{
			AgentID:    types.StringValue("agent-1"),
			Name:       types.StringValue("ak-plan-false"),
			Generation: types.Int64Value(2),
			Applying:   types.BoolValue(false),
		}
		planApplyingFalseState := tfsdk.State{Schema: schemaResp.Schema}
		_ = planApplyingFalseState.Set(ctx, &planApplyingFalseModel)
		var planApplyingFalseResp ephemeral.OpenResponse
		planApplyingFalseResp.Result = tfsdk.EphemeralResultData{Raw: planApplyingFalseState.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, ephemeral.OpenRequest{
			Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: planApplyingFalseState.Raw},
		}, &planApplyingFalseResp)
		var planApplyingFalseResModel PaperclipAgentKeyEphemeralResourceModel
		_ = planApplyingFalseResp.Result.Get(ctx, &planApplyingFalseResModel)
		if !planApplyingFalseResModel.Token.IsNull() {
			t.Fatalf("expected token to be null when applying is false and deferral disallowed, got %q", planApplyingFalseResModel.Token.ValueString())
		}
		var planApplyingFalseDefResp ephemeral.OpenResponse
		planApplyingFalseDefResp.Result = tfsdk.EphemeralResultData{Raw: planApplyingFalseState.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, ephemeral.OpenRequest{
			Config:             tfsdk.Config{Schema: schemaResp.Schema, Raw: planApplyingFalseState.Raw},
			ClientCapabilities: ephemeral.OpenClientCapabilities{DeferralAllowed: true},
		}, &planApplyingFalseDefResp)
		if planApplyingFalseDefResp.Deferred == nil {
			t.Fatal("expected deferred response when applying is false and deferral allowed")
		}

		makeScopeModel := func(kind string) *AgentKeyScopeModel {
			return &AgentKeyScopeModel{
				Kind:                    types.StringValue(kind),
				ProjectID:               types.StringNull(),
				ProjectIDs:              types.ListNull(types.StringType),
				ParentIssueID:           types.StringNull(),
				ParentIssueIDs:          types.ListNull(types.StringType),
				AllowedAssigneeAgentIDs: types.ListNull(types.StringType),
				IssueID:                 types.StringNull(),
			}
		}

		// D. Apply time: invalid scope (task_bridge without boundary)
		invScopeModel := PaperclipAgentKeyEphemeralResourceModel{
			Name:       types.StringValue("ak-inv-scope"),
			AgentID:    types.StringValue("ag-test"),
			Generation: types.Int64Value(1),
			Scope:      makeScopeModel("task_bridge"),
		}
		invScopeState := tfsdk.State{Schema: schemaResp.Schema}
		_ = invScopeState.Set(ctx, &invScopeModel)
		var invScopeResp ephemeral.OpenResponse
		invScopeResp.Result = tfsdk.EphemeralResultData{Raw: invScopeState.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, ephemeral.OpenRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: invScopeState.Raw}}, &invScopeResp)
		if !invScopeResp.Diagnostics.HasError() {
			t.Fatal("expected error diagnostic for invalid scope")
		}

		// E. Apply time: server error on CreateAgentKey
		failCreateAgent = true
		validModel := PaperclipAgentKeyEphemeralResourceModel{
			Name:       types.StringValue("test-ak"),
			AgentID:    types.StringValue("ag-test"),
			Generation: types.Int64Value(1),
		}
		validState := tfsdk.State{Schema: schemaResp.Schema}
		_ = validState.Set(ctx, &validModel)
		var failResp ephemeral.OpenResponse
		failResp.Result = tfsdk.EphemeralResultData{Raw: validState.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, ephemeral.OpenRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: validState.Raw}}, &failResp)
		if !failResp.Diagnostics.HasError() {
			t.Fatal("expected error diagnostic on server failure during agent key minting")
		}
		failCreateAgent = false

		// F. Apply time: success
		var succResp ephemeral.OpenResponse
		initOpenResponsePrivate(&succResp)
		succResp.Result = tfsdk.EphemeralResultData{Raw: validState.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, ephemeral.OpenRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: validState.Raw}}, &succResp)
		if succResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error on successful agent key minting: %v", succResp.Diagnostics)
		}
		var succModel PaperclipAgentKeyEphemeralResourceModel
		_ = succResp.Result.Get(ctx, &succModel)
		if succModel.KeyID.ValueString() != "ak-minted-new" || succModel.Token.ValueString() != "tok-ak-new" {
			t.Fatalf("unexpected minted model: %+v", succModel)
		}

		// G. Empty agent_id with name present
		onlyNameModel := PaperclipAgentKeyEphemeralResourceModel{
			Name:    types.StringValue("has-name"),
			AgentID: types.StringNull(),
		}
		onlyNameState := tfsdk.State{Schema: schemaResp.Schema}
		_ = onlyNameState.Set(ctx, &onlyNameModel)
		var onlyNameResp ephemeral.OpenResponse
		onlyNameResp.Result = tfsdk.EphemeralResultData{Raw: onlyNameState.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, ephemeral.OpenRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: onlyNameState.Raw}}, &onlyNameResp)
		if !onlyNameResp.Diagnostics.HasError() {
			t.Fatal("expected error diagnostic when agent_id empty with name present")
		}

		// H. Lookup mode without agent ID in client cache
		cli.RegisterMintedToken("ak-no-ag", "tok-no-ag")
		noAgModel := PaperclipAgentKeyEphemeralResourceModel{
			KeyID: types.StringValue("ak-no-ag"),
		}
		noAgState := tfsdk.State{Schema: schemaResp.Schema}
		_ = noAgState.Set(ctx, &noAgModel)
		var noAgResp ephemeral.OpenResponse
		initOpenResponsePrivate(&noAgResp)
		noAgResp.Result = tfsdk.EphemeralResultData{Raw: noAgState.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, ephemeral.OpenRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: noAgState.Raw}}, &noAgResp)
		if noAgResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error on lookup without agent ID: %v", noAgResp.Diagnostics)
		}

		// I. Generation delivered when not registered in mintedKeysByName
		cli.RecordDeliveredGeneration("ak-del-unreg", false, "ag-test", 1, "")
		unregModel := PaperclipAgentKeyEphemeralResourceModel{
			Name:       types.StringValue("ak-del-unreg"),
			AgentID:    types.StringValue("ag-test"),
			Generation: types.Int64Value(1),
		}
		unregState := tfsdk.State{Schema: schemaResp.Schema}
		_ = unregState.Set(ctx, &unregModel)
		var unregResp ephemeral.OpenResponse
		unregResp.Result = tfsdk.EphemeralResultData{Raw: unregState.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, ephemeral.OpenRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: unregState.Raw}}, &unregResp)
		if unregResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error on delivered unreg generation: %v", unregResp.Diagnostics)
		}

		// J. Mint with null generation defaults to 1
		nullGenModel := PaperclipAgentKeyEphemeralResourceModel{
			Name:       types.StringValue("test-ak-null-gen"),
			AgentID:    types.StringValue("ag-test"),
			Generation: types.Int64Null(),
		}
		nullGenState := tfsdk.State{Schema: schemaResp.Schema}
		_ = nullGenState.Set(ctx, &nullGenModel)
		var nullGenResp ephemeral.OpenResponse
		nullGenResp.Result = tfsdk.EphemeralResultData{Raw: nullGenState.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, ephemeral.OpenRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: nullGenState.Raw}}, &nullGenResp)
		if nullGenResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error with null generation: %v", nullGenResp.Diagnostics)
		}

		// K. Mint with custom standard scope
		scopeModel := PaperclipAgentKeyEphemeralResourceModel{
			Name:       types.StringValue("test-ak-scope"),
			AgentID:    types.StringValue("ag-test"),
			Generation: types.Int64Value(1),
			Scope:      makeScopeModel("standard"),
		}
		scopeState := tfsdk.State{Schema: schemaResp.Schema}
		_ = scopeState.Set(ctx, &scopeModel)
		var scopeResp ephemeral.OpenResponse
		scopeResp.Result = tfsdk.EphemeralResultData{Raw: scopeState.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, ephemeral.OpenRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: scopeState.Raw}}, &scopeResp)
		if scopeResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error with valid scope: %v", scopeResp.Diagnostics)
		}
	})
}

func TestPaperclipBoardKeyEphemeralTwoInstances(t *testing.T) {
	t.Setenv("PAPERCLIP_SCRATCH_DIR", t.TempDir())
	ctx := context.Background()

	var revokedKeys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/board-api-keys":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":    "bk-inst1",
				"name":  "bk-inst1",
				"token": "tok-inst1",
			})
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/board-api-keys/"):
			keyID := strings.TrimPrefix(r.URL.Path, "/api/board-api-keys/")
			revokedKeys = append(revokedKeys, keyID)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cli, err := client.NewClient(server.URL, "test-token")
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	t.Run("delivery acknowledged: instance 2 close does not affect instance 1", func(t *testing.T) {
		revokedKeys = nil
		r1 := NewPaperclipBoardKeyEphemeralResource().(*PaperclipBoardKeyEphemeralResource)
		r2 := NewPaperclipBoardKeyEphemeralResource().(*PaperclipBoardKeyEphemeralResource)

		var confResp1, confResp2 ephemeral.ConfigureResponse
		r1.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: cli}, &confResp1)
		r2.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: cli}, &confResp2)

		var schemaResp ephemeral.SchemaResponse
		r1.Schema(ctx, ephemeral.SchemaRequest{}, &schemaResp)

		// Instance 1: Open and mint key
		m1 := PaperclipBoardKeyEphemeralResourceModel{
			Name:       types.StringValue("bk-inst1"),
			Generation: types.Int64Value(1),
		}
		s1 := tfsdk.State{Schema: schemaResp.Schema}
		_ = s1.Set(ctx, &m1)
		var openResp1 ephemeral.OpenResponse
		priv1 := initOpenResponsePrivate(&openResp1)
		openResp1.Result = tfsdk.EphemeralResultData{Raw: s1.Raw, Schema: schemaResp.Schema}
		r1.Open(ctx, ephemeral.OpenRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: s1.Raw}}, &openResp1)
		if openResp1.Diagnostics.HasError() {
			t.Fatalf("instance 1 open failed: %v", openResp1.Diagnostics)
		}

		var res1 PaperclipBoardKeyEphemeralResourceModel
		_ = openResp1.Result.Get(ctx, &res1)
		if res1.KeyID.ValueString() != "bk-inst1" || res1.Token.ValueString() != "tok-inst1" {
			t.Fatalf("unexpected res1: %+v", res1)
		}

		// Instance 2: Generation already delivered (no mint)
		cli.RecordDeliveredGeneration("bk-inst2", true, "", 1, "bk-prior2")
		m2 := PaperclipBoardKeyEphemeralResourceModel{
			Name:       types.StringValue("bk-inst2"),
			Generation: types.Int64Value(1),
		}
		s2 := tfsdk.State{Schema: schemaResp.Schema}
		_ = s2.Set(ctx, &m2)
		var openResp2 ephemeral.OpenResponse
		priv2 := initOpenResponsePrivate(&openResp2)
		openResp2.Result = tfsdk.EphemeralResultData{Raw: s2.Raw, Schema: schemaResp.Schema}
		r2.Open(ctx, ephemeral.OpenRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: s2.Raw}}, &openResp2)
		if openResp2.Diagnostics.HasError() {
			t.Fatalf("instance 2 open failed: %v", openResp2.Diagnostics)
		}

		// Instance 2 Close: Private state has no key_id, must not touch instance 1's key
		var closeReq2 ephemeral.CloseRequest
		initCloseRequestPrivate(&closeReq2, priv2)
		var closeResp2 ephemeral.CloseResponse
		r2.Close(ctx, closeReq2, &closeResp2)
		if closeResp2.Diagnostics.HasError() {
			t.Fatalf("instance 2 close failed: %v", closeResp2.Diagnostics)
		}

		// Verify instance 1 key is completely intact
		if tok, ok := cli.GetMintedToken("bk-inst1"); !ok || tok != "tok-inst1" {
			t.Fatalf("instance 1 key was affected by instance 2 close: ok=%v, tok=%v", ok, tok)
		}
		if len(revokedKeys) != 0 {
			t.Fatalf("keys revoked prematurely: %v", revokedKeys)
		}

		// Instance 1 delivered and closes
		cli.AcknowledgeKeyDelivery("bk-inst1")
		var closeReq1 ephemeral.CloseRequest
		initCloseRequestPrivate(&closeReq1, priv1)
		var closeResp1 ephemeral.CloseResponse
		r1.Close(ctx, closeReq1, &closeResp1)
		if closeResp1.Diagnostics.HasError() {
			t.Fatalf("instance 1 close failed: %v", closeResp1.Diagnostics)
		}

		// Token is cleared from cache on success, but key is NOT revoked
		if _, ok := cli.GetMintedToken("bk-inst1"); ok {
			t.Fatal("expected bk-inst1 token to be cleared after acknowledged close")
		}
		if len(revokedKeys) != 0 {
			t.Fatalf("expected no keys revoked, got %v", revokedKeys)
		}
	})

	t.Run("delivery failed: instance 2 close does not affect instance 1, unconfirmed instance 1 is revoked", func(t *testing.T) {
		revokedKeys = nil
		r1 := NewPaperclipBoardKeyEphemeralResource().(*PaperclipBoardKeyEphemeralResource)
		r2 := NewPaperclipBoardKeyEphemeralResource().(*PaperclipBoardKeyEphemeralResource)

		var confResp1, confResp2 ephemeral.ConfigureResponse
		r1.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: cli}, &confResp1)
		r2.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: cli}, &confResp2)

		var schemaResp ephemeral.SchemaResponse
		r1.Schema(ctx, ephemeral.SchemaRequest{}, &schemaResp)

		// Instance 1: Open and mint key
		m1 := PaperclipBoardKeyEphemeralResourceModel{
			Name:       types.StringValue("bk-unconf1"),
			Generation: types.Int64Value(1),
		}
		s1 := tfsdk.State{Schema: schemaResp.Schema}
		_ = s1.Set(ctx, &m1)
		var openResp1 ephemeral.OpenResponse
		priv1 := initOpenResponsePrivate(&openResp1)
		openResp1.Result = tfsdk.EphemeralResultData{Raw: s1.Raw, Schema: schemaResp.Schema}
		r1.Open(ctx, ephemeral.OpenRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: s1.Raw}}, &openResp1)
		if openResp1.Diagnostics.HasError() {
			t.Fatalf("instance 1 open failed: %v", openResp1.Diagnostics)
		}

		// Instance 2: Generation already delivered (no mint)
		cli.RecordDeliveredGeneration("bk-inst2-unconf", true, "", 1, "bk-prior2")
		m2 := PaperclipBoardKeyEphemeralResourceModel{
			Name:       types.StringValue("bk-inst2-unconf"),
			Generation: types.Int64Value(1),
		}
		s2 := tfsdk.State{Schema: schemaResp.Schema}
		_ = s2.Set(ctx, &m2)
		var openResp2 ephemeral.OpenResponse
		priv2 := initOpenResponsePrivate(&openResp2)
		openResp2.Result = tfsdk.EphemeralResultData{Raw: s2.Raw, Schema: schemaResp.Schema}
		r2.Open(ctx, ephemeral.OpenRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: s2.Raw}}, &openResp2)
		if openResp2.Diagnostics.HasError() {
			t.Fatalf("instance 2 open failed: %v", openResp2.Diagnostics)
		}

		// Instance 2 Close: Private state has no key_id, must not touch instance 1's key
		var closeReq2 ephemeral.CloseRequest
		initCloseRequestPrivate(&closeReq2, priv2)
		var closeResp2 ephemeral.CloseResponse
		r2.Close(ctx, closeReq2, &closeResp2)
		if closeResp2.Diagnostics.HasError() {
			t.Fatalf("instance 2 close failed: %v", closeResp2.Diagnostics)
		}

		// Verify instance 1 key is intact before its own close
		if len(revokedKeys) != 0 {
			t.Fatalf("instance 1 key revoked by instance 2 close: %v", revokedKeys)
		}

		// Instance 1 Close without delivery acknowledgement -> must revoke bk-inst1
		var closeReq1 ephemeral.CloseRequest
		initCloseRequestPrivate(&closeReq1, priv1)
		var closeResp1 ephemeral.CloseResponse
		r1.Close(ctx, closeReq1, &closeResp1)
		if closeResp1.Diagnostics.HasError() {
			t.Fatalf("instance 1 close failed: %v", closeResp1.Diagnostics)
		}

		if len(revokedKeys) != 1 || revokedKeys[0] != "bk-inst1" {
			t.Fatalf("expected bk-inst1 to be revoked, got %v", revokedKeys)
		}
	})
}

func TestPaperclipAgentKeyEphemeralTwoInstances(t *testing.T) {
	t.Setenv("PAPERCLIP_SCRATCH_DIR", t.TempDir())
	ctx := context.Background()

	var revokedKeys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/agents/ag-1/keys":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":       "ak-inst1",
				"agent_id": "ag-1",
				"name":     "ak-inst1",
				"token":    "tok-ak-inst1",
			})
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/agents/ag-1/keys/"):
			keyID := strings.TrimPrefix(r.URL.Path, "/api/agents/ag-1/keys/")
			revokedKeys = append(revokedKeys, keyID)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cli, err := client.NewClient(server.URL, "test-token")
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	t.Run("delivery acknowledged: instance 2 close does not affect instance 1", func(t *testing.T) {
		revokedKeys = nil
		r1 := NewPaperclipAgentKeyEphemeralResource().(*PaperclipAgentKeyEphemeralResource)
		r2 := NewPaperclipAgentKeyEphemeralResource().(*PaperclipAgentKeyEphemeralResource)

		var confResp1, confResp2 ephemeral.ConfigureResponse
		r1.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: cli}, &confResp1)
		r2.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: cli}, &confResp2)

		var schemaResp ephemeral.SchemaResponse
		r1.Schema(ctx, ephemeral.SchemaRequest{}, &schemaResp)

		// Instance 1: Open and mint agent key
		m1 := PaperclipAgentKeyEphemeralResourceModel{
			AgentID:    types.StringValue("ag-1"),
			Name:       types.StringValue("ak-inst1"),
			Generation: types.Int64Value(1),
		}
		s1 := tfsdk.State{Schema: schemaResp.Schema}
		_ = s1.Set(ctx, &m1)
		var openResp1 ephemeral.OpenResponse
		priv1 := initOpenResponsePrivate(&openResp1)
		openResp1.Result = tfsdk.EphemeralResultData{Raw: s1.Raw, Schema: schemaResp.Schema}
		r1.Open(ctx, ephemeral.OpenRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: s1.Raw}}, &openResp1)
		if openResp1.Diagnostics.HasError() {
			t.Fatalf("instance 1 open failed: %v", openResp1.Diagnostics)
		}

		var res1 PaperclipAgentKeyEphemeralResourceModel
		_ = openResp1.Result.Get(ctx, &res1)
		if res1.KeyID.ValueString() != "ak-inst1" || res1.Token.ValueString() != "tok-ak-inst1" {
			t.Fatalf("unexpected res1: %+v", res1)
		}

		// Instance 2: Generation already delivered (no mint)
		cli.RecordDeliveredGeneration("ak-inst2", false, "ag-1", 1, "ak-prior2")
		m2 := PaperclipAgentKeyEphemeralResourceModel{
			AgentID:    types.StringValue("ag-1"),
			Name:       types.StringValue("ak-inst2"),
			Generation: types.Int64Value(1),
		}
		s2 := tfsdk.State{Schema: schemaResp.Schema}
		_ = s2.Set(ctx, &m2)
		var openResp2 ephemeral.OpenResponse
		priv2 := initOpenResponsePrivate(&openResp2)
		openResp2.Result = tfsdk.EphemeralResultData{Raw: s2.Raw, Schema: schemaResp.Schema}
		r2.Open(ctx, ephemeral.OpenRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: s2.Raw}}, &openResp2)
		if openResp2.Diagnostics.HasError() {
			t.Fatalf("instance 2 open failed: %v", openResp2.Diagnostics)
		}

		// Instance 2 Close: Private state has no key_id, must not touch instance 1's key
		var closeReq2 ephemeral.CloseRequest
		initCloseRequestPrivate(&closeReq2, priv2)
		var closeResp2 ephemeral.CloseResponse
		r2.Close(ctx, closeReq2, &closeResp2)
		if closeResp2.Diagnostics.HasError() {
			t.Fatalf("instance 2 close failed: %v", closeResp2.Diagnostics)
		}

		// Verify instance 1 key is completely intact
		if tok, ok := cli.GetMintedToken("ak-inst1"); !ok || tok != "tok-ak-inst1" {
			t.Fatalf("instance 1 key was affected by instance 2 close: ok=%v, tok=%v", ok, tok)
		}
		if len(revokedKeys) != 0 {
			t.Fatalf("keys revoked prematurely: %v", revokedKeys)
		}

		// Instance 1 delivered and closes
		cli.AcknowledgeKeyDelivery("ak-inst1")
		var closeReq1 ephemeral.CloseRequest
		initCloseRequestPrivate(&closeReq1, priv1)
		var closeResp1 ephemeral.CloseResponse
		r1.Close(ctx, closeReq1, &closeResp1)
		if closeResp1.Diagnostics.HasError() {
			t.Fatalf("instance 1 close failed: %v", closeResp1.Diagnostics)
		}

		// Token is cleared from cache on success, but key is NOT revoked
		if _, ok := cli.GetMintedToken("ak-inst1"); ok {
			t.Fatal("expected ak-inst1 token to be cleared after acknowledged close")
		}
		if len(revokedKeys) != 0 {
			t.Fatalf("expected no keys revoked, got %v", revokedKeys)
		}
	})

	t.Run("delivery failed: instance 2 close does not affect instance 1, unconfirmed instance 1 is revoked", func(t *testing.T) {
		revokedKeys = nil
		r1 := NewPaperclipAgentKeyEphemeralResource().(*PaperclipAgentKeyEphemeralResource)
		r2 := NewPaperclipAgentKeyEphemeralResource().(*PaperclipAgentKeyEphemeralResource)

		var confResp1, confResp2 ephemeral.ConfigureResponse
		r1.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: cli}, &confResp1)
		r2.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: cli}, &confResp2)

		var schemaResp ephemeral.SchemaResponse
		r1.Schema(ctx, ephemeral.SchemaRequest{}, &schemaResp)

		// Instance 1: Open and mint agent key
		m1 := PaperclipAgentKeyEphemeralResourceModel{
			AgentID:    types.StringValue("ag-1"),
			Name:       types.StringValue("ak-unconf1"),
			Generation: types.Int64Value(1),
		}
		s1 := tfsdk.State{Schema: schemaResp.Schema}
		_ = s1.Set(ctx, &m1)
		var openResp1 ephemeral.OpenResponse
		priv1 := initOpenResponsePrivate(&openResp1)
		openResp1.Result = tfsdk.EphemeralResultData{Raw: s1.Raw, Schema: schemaResp.Schema}
		r1.Open(ctx, ephemeral.OpenRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: s1.Raw}}, &openResp1)
		if openResp1.Diagnostics.HasError() {
			t.Fatalf("instance 1 open failed: %v", openResp1.Diagnostics)
		}

		// Instance 2: Generation already delivered (no mint)
		cli.RecordDeliveredGeneration("ak-inst2-unconf", false, "ag-1", 1, "ak-prior2")
		m2 := PaperclipAgentKeyEphemeralResourceModel{
			AgentID:    types.StringValue("ag-1"),
			Name:       types.StringValue("ak-inst2-unconf"),
			Generation: types.Int64Value(1),
		}
		s2 := tfsdk.State{Schema: schemaResp.Schema}
		_ = s2.Set(ctx, &m2)
		var openResp2 ephemeral.OpenResponse
		priv2 := initOpenResponsePrivate(&openResp2)
		openResp2.Result = tfsdk.EphemeralResultData{Raw: s2.Raw, Schema: schemaResp.Schema}
		r2.Open(ctx, ephemeral.OpenRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: s2.Raw}}, &openResp2)
		if openResp2.Diagnostics.HasError() {
			t.Fatalf("instance 2 open failed: %v", openResp2.Diagnostics)
		}

		// Instance 2 Close: Private state has no key_id, must not touch instance 1's key
		var closeReq2 ephemeral.CloseRequest
		initCloseRequestPrivate(&closeReq2, priv2)
		var closeResp2 ephemeral.CloseResponse
		r2.Close(ctx, closeReq2, &closeResp2)
		if closeResp2.Diagnostics.HasError() {
			t.Fatalf("instance 2 close failed: %v", closeResp2.Diagnostics)
		}

		if len(revokedKeys) != 0 {
			t.Fatalf("instance 1 key revoked by instance 2 close: %v", revokedKeys)
		}

		// Instance 1 Close without delivery acknowledgement -> must revoke ak-inst1
		var closeReq1 ephemeral.CloseRequest
		initCloseRequestPrivate(&closeReq1, priv1)
		var closeResp1 ephemeral.CloseResponse
		r1.Close(ctx, closeReq1, &closeResp1)
		if closeResp1.Diagnostics.HasError() {
			t.Fatalf("instance 1 close failed: %v", closeResp1.Diagnostics)
		}

		if len(revokedKeys) != 1 || revokedKeys[0] != "ak-inst1" {
			t.Fatalf("expected ak-inst1 to be revoked, got %v", revokedKeys)
		}
	})
}

func TestEphemeralResourcesPrivateDecodeErrors(t *testing.T) {
	t.Setenv("PAPERCLIP_SCRATCH_DIR", t.TempDir())
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	cli, err := client.NewClient(server.URL, "test-token")
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	t.Run("board_key private decode errors", func(t *testing.T) {
		r := NewPaperclipBoardKeyEphemeralResource().(*PaperclipBoardKeyEphemeralResource)
		var confResp ephemeral.ConfigureResponse
		r.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: cli}, &confResp)

		// Incompatible key_id type (number instead of string)
		var openResp1 ephemeral.OpenResponse
		priv1 := initOpenResponsePrivate(&openResp1)
		_ = openResp1.Private.SetKey(ctx, "key_id", []byte("123"))
		var closeReq1 ephemeral.CloseRequest
		initCloseRequestPrivate(&closeReq1, priv1)
		var closeResp1 ephemeral.CloseResponse
		r.Close(ctx, closeReq1, &closeResp1)
		if !closeResp1.Diagnostics.HasError() {
			t.Fatal("expected error on incompatible key_id private state")
		}

		// Incompatible is_lookup type (string instead of bool)
		var openResp2 ephemeral.OpenResponse
		priv2 := initOpenResponsePrivate(&openResp2)
		bValid, _ := json.Marshal("bk-test")
		_ = openResp2.Private.SetKey(ctx, "key_id", bValid)
		_ = openResp2.Private.SetKey(ctx, "is_lookup", []byte("\"not-a-bool\""))
		var closeReq2 ephemeral.CloseRequest
		initCloseRequestPrivate(&closeReq2, priv2)
		var closeResp2 ephemeral.CloseResponse
		r.Close(ctx, closeReq2, &closeResp2)
		if !closeResp2.Diagnostics.HasError() {
			t.Fatal("expected error on incompatible is_lookup private state")
		}
	})

	t.Run("agent_key private decode errors", func(t *testing.T) {
		r := NewPaperclipAgentKeyEphemeralResource().(*PaperclipAgentKeyEphemeralResource)
		var confResp ephemeral.ConfigureResponse
		r.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: cli}, &confResp)

		// Incompatible key_id type (number instead of string)
		var openResp1 ephemeral.OpenResponse
		priv1 := initOpenResponsePrivate(&openResp1)
		_ = openResp1.Private.SetKey(ctx, "key_id", []byte("123"))
		var closeReq1 ephemeral.CloseRequest
		initCloseRequestPrivate(&closeReq1, priv1)
		var closeResp1 ephemeral.CloseResponse
		r.Close(ctx, closeReq1, &closeResp1)
		if !closeResp1.Diagnostics.HasError() {
			t.Fatal("expected error on incompatible key_id private state")
		}

		// Incompatible is_lookup type (string instead of bool)
		var openResp2 ephemeral.OpenResponse
		priv2 := initOpenResponsePrivate(&openResp2)
		bValid, _ := json.Marshal("ak-test")
		_ = openResp2.Private.SetKey(ctx, "key_id", bValid)
		_ = openResp2.Private.SetKey(ctx, "is_lookup", []byte("\"not-a-bool\""))
		var closeReq2 ephemeral.CloseRequest
		initCloseRequestPrivate(&closeReq2, priv2)
		var closeResp2 ephemeral.CloseResponse
		r.Close(ctx, closeReq2, &closeResp2)
		if !closeResp2.Diagnostics.HasError() {
			t.Fatal("expected error on incompatible is_lookup private state")
		}

		// Incompatible agent_id type (number instead of string)
		var openResp3 ephemeral.OpenResponse
		priv3 := initOpenResponsePrivate(&openResp3)
		_ = openResp3.Private.SetKey(ctx, "key_id", bValid)
		bLookup, _ := json.Marshal(false)
		_ = openResp3.Private.SetKey(ctx, "is_lookup", bLookup)
		_ = openResp3.Private.SetKey(ctx, "agent_id", []byte("123"))
		var closeReq3 ephemeral.CloseRequest
		initCloseRequestPrivate(&closeReq3, priv3)
		var closeResp3 ephemeral.CloseResponse
		r.Close(ctx, closeReq3, &closeResp3)
		if !closeResp3.Diagnostics.HasError() {
			t.Fatal("expected error on incompatible agent_id private state")
		}
	})
}

type mockPrivateStateSetter struct {
	diags diag.Diagnostics
}

func (m *mockPrivateStateSetter) SetKey(ctx context.Context, key string, val []byte) diag.Diagnostics {
	return m.diags
}

type mockPrivateStateGetter struct {
	data  []byte
	diags diag.Diagnostics
}

func (m *mockPrivateStateGetter) GetKey(ctx context.Context, key string) ([]byte, diag.Diagnostics) {
	return m.data, m.diags
}

func TestPrivateStateHelpers(t *testing.T) {
	ctx := context.Background()

	t.Run("setPrivateStateKey", func(t *testing.T) {
		var diags diag.Diagnostics
		setPrivateStateKey(ctx, nil, "key", "val", &diags)
		if diags.HasError() {
			t.Fatal("expected no error for nil private setter")
		}

		origMarshal := jsonMarshal
		defer func() { jsonMarshal = origMarshal }()
		jsonMarshal = func(v any) ([]byte, error) {
			return nil, fmt.Errorf("injected marshal error")
		}
		setter := &mockPrivateStateSetter{}
		setPrivateStateKey(ctx, setter, "key", "val", &diags)
		if !diags.HasError() {
			t.Fatal("expected error on marshal failure")
		}

		jsonMarshal = origMarshal
		diags = nil
		failingSetter := &mockPrivateStateSetter{
			diags: diag.Diagnostics{diag.NewErrorDiagnostic("SetKey error", "disk full")},
		}
		setPrivateStateKey(ctx, failingSetter, "key", "val", &diags)
		if !diags.HasError() {
			t.Fatal("expected error from failing SetKey")
		}
	})

	t.Run("getPrivateStateKey", func(t *testing.T) {
		var diags diag.Diagnostics
		var val string
		getPrivateStateKey(ctx, nil, "key", &val, &diags)
		if diags.HasError() {
			t.Fatal("expected no error for nil private getter")
		}

		getterWithDiags := &mockPrivateStateGetter{
			diags: diag.Diagnostics{diag.NewErrorDiagnostic("GetKey error", "read error")},
		}
		getPrivateStateKey(ctx, getterWithDiags, "key", &val, &diags)
		if !diags.HasError() {
			t.Fatal("expected error from failing GetKey")
		}

		diags = nil
		getterEmpty := &mockPrivateStateGetter{data: []byte{}}
		getPrivateStateKey(ctx, getterEmpty, "key", &val, &diags)
		if diags.HasError() {
			t.Fatal("expected no error on empty data")
		}

		diags = nil
		getterBadJSON := &mockPrivateStateGetter{data: []byte("not-json{")}
		getPrivateStateKey(ctx, getterBadJSON, "key", &val, &diags)
		if !diags.HasError() {
			t.Fatal("expected error on unmarshal failure")
		}

		diags = nil
		getterOK := &mockPrivateStateGetter{data: []byte(`"good"`)}
		getPrivateStateKey(ctx, getterOK, "key", &val, &diags)
		if diags.HasError() || val != "good" {
			t.Fatalf("expected good value, got %s (diags=%v)", val, diags)
		}
	})
}

func TestEphemeralOpenPrivateWriteFailure(t *testing.T) {
	t.Setenv("PAPERCLIP_SCRATCH_DIR", t.TempDir())
	ctx := context.Background()

	var revokedBoardKeys []string
	var revokedAgentKeys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/board-api-keys":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":    "bk-fail-pw",
				"name":  "bk-fail-pw",
				"token": "tok-fail-pw",
			})
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/board-api-keys/"):
			keyID := strings.TrimPrefix(r.URL.Path, "/api/board-api-keys/")
			revokedBoardKeys = append(revokedBoardKeys, keyID)
			w.WriteHeader(http.StatusNoContent)
		case r.Method == http.MethodPost && r.URL.Path == "/api/agents/ag-pw/keys":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":       "ak-fail-pw",
				"agent_id": "ag-pw",
				"name":     "ak-fail-pw",
				"token":    "tok-ak-fail-pw",
			})
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/agents/ag-pw/keys/"):
			keyID := strings.TrimPrefix(r.URL.Path, "/api/agents/ag-pw/keys/")
			revokedAgentKeys = append(revokedAgentKeys, keyID)
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cli, err := client.NewClient(server.URL, "test-token")
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	t.Run("board_key mint private write failure revokes key and surfaces error", func(t *testing.T) {
		r := NewPaperclipBoardKeyEphemeralResource().(*PaperclipBoardKeyEphemeralResource)
		var confResp ephemeral.ConfigureResponse
		r.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: cli}, &confResp)
		var schemaResp ephemeral.SchemaResponse
		r.Schema(ctx, ephemeral.SchemaRequest{}, &schemaResp)

		origMarshal := jsonMarshal
		defer func() { jsonMarshal = origMarshal }()
		// Inject invalid JSON to cause SetKey validation failure
		jsonMarshal = func(v any) ([]byte, error) {
			return []byte("invalid-json{"), nil
		}

		m := PaperclipBoardKeyEphemeralResourceModel{
			Name:       types.StringValue("bk-fail-pw"),
			Generation: types.Int64Value(1),
		}
		s := tfsdk.State{Schema: schemaResp.Schema}
		_ = s.Set(ctx, &m)
		var openResp ephemeral.OpenResponse
		initOpenResponsePrivate(&openResp)
		openResp.Result = tfsdk.EphemeralResultData{Raw: s.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, ephemeral.OpenRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: s.Raw}}, &openResp)
		if !openResp.Diagnostics.HasError() {
			t.Fatal("expected error on failed private state write during board key Open")
		}
		if len(revokedBoardKeys) != 1 || revokedBoardKeys[0] != "bk-fail-pw" {
			t.Fatalf("expected bk-fail-pw to be revoked on private write failure, got %v", revokedBoardKeys)
		}
		if _, ok := cli.GetMintedToken("bk-fail-pw"); ok {
			t.Fatal("expected bk-fail-pw token to be cleared after failed private write")
		}
	})

	t.Run("board_key lookup private write failure surfaces error", func(t *testing.T) {
		r := NewPaperclipBoardKeyEphemeralResource().(*PaperclipBoardKeyEphemeralResource)
		var confResp ephemeral.ConfigureResponse
		r.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: cli}, &confResp)
		var schemaResp ephemeral.SchemaResponse
		r.Schema(ctx, ephemeral.SchemaRequest{}, &schemaResp)

		cli.RegisterMintedToken("bk-lookup-fail", "tok-lookup")
		origMarshal := jsonMarshal
		defer func() { jsonMarshal = origMarshal }()
		jsonMarshal = func(v any) ([]byte, error) {
			return []byte("invalid-json{"), nil
		}

		m := PaperclipBoardKeyEphemeralResourceModel{
			KeyID: types.StringValue("bk-lookup-fail"),
		}
		s := tfsdk.State{Schema: schemaResp.Schema}
		_ = s.Set(ctx, &m)
		var openResp ephemeral.OpenResponse
		initOpenResponsePrivate(&openResp)
		openResp.Result = tfsdk.EphemeralResultData{Raw: s.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, ephemeral.OpenRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: s.Raw}}, &openResp)
		if !openResp.Diagnostics.HasError() {
			t.Fatal("expected error on failed private state write during board key lookup Open")
		}
	})

	t.Run("agent_key mint private write failure revokes key and surfaces error", func(t *testing.T) {
		r := NewPaperclipAgentKeyEphemeralResource().(*PaperclipAgentKeyEphemeralResource)
		var confResp ephemeral.ConfigureResponse
		r.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: cli}, &confResp)
		var schemaResp ephemeral.SchemaResponse
		r.Schema(ctx, ephemeral.SchemaRequest{}, &schemaResp)

		origMarshal := jsonMarshal
		defer func() { jsonMarshal = origMarshal }()
		jsonMarshal = func(v any) ([]byte, error) {
			return []byte("invalid-json{"), nil
		}

		m := PaperclipAgentKeyEphemeralResourceModel{
			AgentID:    types.StringValue("ag-pw"),
			Name:       types.StringValue("ak-fail-pw"),
			Generation: types.Int64Value(1),
		}
		s := tfsdk.State{Schema: schemaResp.Schema}
		_ = s.Set(ctx, &m)
		var openResp ephemeral.OpenResponse
		initOpenResponsePrivate(&openResp)
		openResp.Result = tfsdk.EphemeralResultData{Raw: s.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, ephemeral.OpenRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: s.Raw}}, &openResp)
		if !openResp.Diagnostics.HasError() {
			t.Fatal("expected error on failed private state write during agent key Open")
		}
		if len(revokedAgentKeys) != 1 || revokedAgentKeys[0] != "ak-fail-pw" {
			t.Fatalf("expected ak-fail-pw to be revoked on private write failure, got %v", revokedAgentKeys)
		}
		if _, ok := cli.GetMintedToken("ak-fail-pw"); ok {
			t.Fatal("expected ak-fail-pw token to be cleared after failed private write")
		}
	})

	t.Run("agent_key lookup private write failure surfaces error", func(t *testing.T) {
		r := NewPaperclipAgentKeyEphemeralResource().(*PaperclipAgentKeyEphemeralResource)
		var confResp ephemeral.ConfigureResponse
		r.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: cli}, &confResp)
		var schemaResp ephemeral.SchemaResponse
		r.Schema(ctx, ephemeral.SchemaRequest{}, &schemaResp)

		cli.RegisterMintedAgentKey("ag-pw", "ak-lookup-fail", "tok-lookup")
		origMarshal := jsonMarshal
		defer func() { jsonMarshal = origMarshal }()
		jsonMarshal = func(v any) ([]byte, error) {
			return []byte("invalid-json{"), nil
		}

		m := PaperclipAgentKeyEphemeralResourceModel{
			KeyID: types.StringValue("ak-lookup-fail"),
		}
		s := tfsdk.State{Schema: schemaResp.Schema}
		_ = s.Set(ctx, &m)
		var openResp ephemeral.OpenResponse
		initOpenResponsePrivate(&openResp)
		openResp.Result = tfsdk.EphemeralResultData{Raw: s.Raw, Schema: schemaResp.Schema}
		r.Open(ctx, ephemeral.OpenRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: s.Raw}}, &openResp)
		if !openResp.Diagnostics.HasError() {
			t.Fatal("expected error on failed private state write during agent key lookup Open")
		}
	})
}



