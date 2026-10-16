package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func gatewayTokenTestModel() gatewayTokenModel {
	return gatewayTokenModel{CompanyID: types.StringValue("comp-1"), GatewayID: types.StringValue("gateway-1"), Name: types.StringValue("client"), ClientLabel: types.StringValue("Automation"), Generation: types.Int64Value(1), ExpiresAt: types.StringValue("2100-01-01T00:00:00Z"), AllowedActions: types.StringValue(`["tools/list","tools/call"]`)}
}
func gatewayEphemeralModel(m gatewayTokenModel) gatewayTokenEphemeralModel {
	return gatewayTokenEphemeralModel{CompanyID: m.CompanyID, GatewayID: m.GatewayID, Name: m.Name, ClientLabel: m.ClientLabel, Generation: m.Generation, ExpiresAt: m.ExpiresAt, AllowedActions: m.AllowedActions, Applying: types.BoolValue(true)}
}
func openGatewayToken(t *testing.T, r *PaperclipMCPGatewayTokenEphemeralResource, m gatewayTokenEphemeralModel, deferral bool, private bool) (ephemeral.OpenRequest, ephemeral.OpenResponse) {
	t.Helper()
	ctx := context.Background()
	var s ephemeral.SchemaResponse
	r.Schema(ctx, ephemeral.SchemaRequest{}, &s)
	state := tfsdk.State{Schema: s.Schema}
	requireToolDiagnostics(t, state.Set(ctx, &m))
	req := ephemeral.OpenRequest{Config: tfsdk.Config{Schema: s.Schema, Raw: state.Raw}, ClientCapabilities: ephemeral.OpenClientCapabilities{DeferralAllowed: deferral}}
	resp := ephemeral.OpenResponse{Result: tfsdk.EphemeralResultData{Schema: s.Schema}}
	if private {
		initOpenResponsePrivate(&resp)
	}
	r.Open(ctx, req, &resp)
	return req, resp
}
func closeGatewayToken(t *testing.T, r *PaperclipMCPGatewayTokenEphemeralResource, open ephemeral.OpenResponse) ephemeral.CloseResponse {
	t.Helper()
	req := ephemeral.CloseRequest{}
	initCloseRequestPrivate(&req, open.Private)
	resp := ephemeral.CloseResponse{}
	r.Close(context.Background(), req, &resp)
	return resp
}
func TestGatewayTokenPlanDefersAndApplyReusesMint(t *testing.T) {
	f := newGatewayFixture(t)
	_, _, _, _ = createGateway(t, f)
	c, _ := client.NewClient(f.server.URL, "")
	r := &PaperclipMCPGatewayTokenEphemeralResource{client: c}
	model := gatewayEphemeralModel(gatewayTokenTestModel())
	_, plan := openGatewayToken(t, r, model, true, true)
	requireToolDiagnostics(t, plan.Diagnostics)
	if plan.Deferred == nil || len(f.tokens) != 0 {
		t.Fatal("plan minted a token")
	}
	model.Applying = types.BoolValue(false)
	_, noApply := openGatewayToken(t, r, model, false, true)
	requireToolDiagnostics(t, noApply.Diagnostics)
	if len(f.tokens) != 0 {
		t.Fatal("non-apply minted")
	}
	model.Applying = types.BoolValue(true)
	_, open := openGatewayToken(t, r, model, false, true)
	requireToolDiagnostics(t, open.Diagnostics)
	var result gatewayTokenEphemeralModel
	requireToolDiagnostics(t, open.Result.Get(context.Background(), &result))
	if result.Token.ValueString() != "pcgw_private_token-1" || result.TokenID.ValueString() != "token-1" {
		t.Fatal("one-time token missing")
	}
	_, again := openGatewayToken(t, r, model, false, true)
	requireToolDiagnostics(t, again.Diagnostics)
	if len(f.tokens) != 1 {
		t.Fatal("same apply minted twice")
	}
	closed := closeGatewayToken(t, r, open)
	requireToolDiagnostics(t, closed.Diagnostics)
	if f.tokens["token-1"]["revokedAt"] == nil {
		t.Fatal("failed delivery left live token")
	}
}
func TestGatewayTokenDeliveryRotationAndUnchangedGeneration(t *testing.T) {
	f := newGatewayFixture(t)
	_, _, _, _ = createGateway(t, f)
	ctx := context.Background()
	c, _ := client.NewClient(f.server.URL, "")
	e := &PaperclipMCPGatewayTokenEphemeralResource{client: c}
	r := &PaperclipMCPGatewayTokenResource{client: c}
	var s resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &s)
	m := gatewayTokenTestModel()
	_, open := openGatewayToken(t, e, gatewayEphemeralModel(m), false, true)
	requireToolDiagnostics(t, open.Diagnostics)
	create := resource.CreateResponse{State: tfsdk.State{Schema: s.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: toolPlan(t, s.Schema, &m)}, &create)
	requireToolDiagnostics(t, create.Diagnostics)
	requireToolDiagnostics(t, create.State.Get(ctx, &m))
	if m.ID.ValueString() != "token-1" || strings.Contains(create.State.Raw.String(), "pcgw_private") {
		t.Fatal("lifecycle identity missing or token stored")
	}
	closed := closeGatewayToken(t, e, open)
	requireToolDiagnostics(t, closed.Diagnostics)
	if f.tokens["token-1"]["revokedAt"] != nil {
		t.Fatal("delivered token revoked on close")
	}
	// A fresh provider process learns the delivered generation from durable state.
	c2, _ := client.NewClient(f.server.URL, "")
	r.client, e.client = c2, c2
	read := resource.ReadResponse{State: create.State}
	r.Read(ctx, resource.ReadRequest{State: create.State}, &read)
	requireToolDiagnostics(t, read.Diagnostics)
	_, same := openGatewayToken(t, e, gatewayEphemeralModel(m), false, true)
	requireToolDiagnostics(t, same.Diagnostics)
	var unchanged gatewayTokenEphemeralModel
	requireToolDiagnostics(t, same.Result.Get(ctx, &unchanged))
	if !unchanged.Token.IsNull() || len(f.tokens) != 1 {
		t.Fatal("unchanged generation minted/delivered a token")
	}
	m.Generation = types.Int64Value(2)
	plan := resource.ModifyPlanResponse{Plan: toolPlan(t, s.Schema, &m)}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan.Plan, State: read.State}, &plan)
	requireToolDiagnostics(t, plan.Diagnostics)
	_, rotation := openGatewayToken(t, e, gatewayEphemeralModel(m), false, true)
	requireToolDiagnostics(t, rotation.Diagnostics)
	if f.tokens["token-1"]["revokedAt"] != nil {
		t.Fatal("predecessor revoked before delivery")
	}
	update := resource.UpdateResponse{State: read.State}
	r.Update(ctx, resource.UpdateRequest{Plan: plan.Plan, State: read.State}, &update)
	requireToolDiagnostics(t, update.Diagnostics)
	if f.tokens["token-1"]["revokedAt"] == nil || f.tokens["token-2"]["revokedAt"] != nil {
		t.Fatal("rotation did not retain only successor")
	}
	closed = closeGatewayToken(t, e, rotation)
	requireToolDiagnostics(t, closed.Diagnostics)
	requireToolDiagnostics(t, update.State.Get(ctx, &m))
	stable := resource.UpdateResponse{State: update.State}
	r.Update(ctx, resource.UpdateRequest{Plan: toolPlan(t, s.Schema, &m), State: update.State}, &stable)
	requireToolDiagnostics(t, stable.Diagnostics)
	del := resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: stable.State}, &del)
	requireToolDiagnostics(t, del.Diagnostics)
	read = resource.ReadResponse{State: stable.State}
	r.Read(ctx, resource.ReadRequest{State: stable.State}, &read)
	requireToolDiagnostics(t, read.Diagnostics)
	if !read.State.Raw.IsNull() {
		t.Fatal("revoked token retained")
	}
}
func TestGatewayTokenFailurePreservesPredecessorAndCleansMint(t *testing.T) {
	for _, mode := range []string{"missing-mint", "mint-denied", "revoke-denied", "close-denied", "interrupted", "private-missing"} {
		t.Run(mode, func(t *testing.T) {
			f := newGatewayFixture(t)
			_, _, _, _ = createGateway(t, f)
			ctx := context.Background()
			c, _ := client.NewClient(f.server.URL, "")
			e := &PaperclipMCPGatewayTokenEphemeralResource{client: c}
			r := &PaperclipMCPGatewayTokenResource{client: c}
			var s resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &s)
			m := gatewayTokenTestModel()
			if mode == "missing-mint" {
				resp := resource.CreateResponse{State: tfsdk.State{Schema: s.Schema}}
				r.Create(ctx, resource.CreateRequest{Plan: toolPlan(t, s.Schema, &m)}, &resp)
				if !resp.Diagnostics.HasError() || len(f.tokens) != 0 {
					t.Fatal("record invented delivery")
				}
				return
			}
			if mode == "mint-denied" {
				f.fail = "POST /api/tool-gateway/gateways/gateway-1/tokens"
			}
			_, open := openGatewayToken(t, e, gatewayEphemeralModel(m), false, mode != "private-missing")
			if mode == "mint-denied" || mode == "private-missing" {
				if !open.Diagnostics.HasError() || len(f.tokens) != 0 {
					t.Fatal("failed mint mutated or succeeded")
				}
				return
			}
			requireToolDiagnostics(t, open.Diagnostics)
			if mode == "interrupted" {
				if err := c.CleanupUnconfirmedKeys(ctx); err != nil {
					t.Fatal(err)
				}
				if f.tokens["token-1"]["revokedAt"] == nil {
					t.Fatal("exit cleanup left live token")
				}
				return
			}
			f.fail = "POST /api/tool-gateway/gateway-tokens/token-1/revoke"
			if mode == "close-denied" {
				closed := closeGatewayToken(t, e, open)
				if !closed.Diagnostics.HasError() {
					t.Fatal("revocation error lost")
				}
				return
			}
			create := resource.CreateResponse{State: tfsdk.State{Schema: s.Schema}}
			r.Create(ctx, resource.CreateRequest{Plan: toolPlan(t, s.Schema, &m)}, &create)
			requireToolDiagnostics(t, create.Diagnostics)
			m.Generation = types.Int64Value(2)
			_, next := openGatewayToken(t, e, gatewayEphemeralModel(m), false, true)
			requireToolDiagnostics(t, next.Diagnostics)
			update := resource.UpdateResponse{State: create.State}
			r.Update(ctx, resource.UpdateRequest{Plan: toolPlan(t, s.Schema, &m), State: create.State}, &update)
			if !update.Diagnostics.HasError() || f.tokens["token-1"]["revokedAt"] != nil {
				t.Fatal("failed rotation lost predecessor")
			}
			closed := closeGatewayToken(t, e, next)
			requireToolDiagnostics(t, closed.Diagnostics)
			if f.tokens["token-2"]["revokedAt"] == nil {
				t.Fatal("failed rotation left successor unrecorded")
			}
		})
	}
}

func TestGatewayTokenRecordRejectsMismatchedProducerConfiguration(t *testing.T) {
	f := newGatewayFixture(t)
	_, _, _, _ = createGateway(t, f)
	ctx := context.Background()
	c, _ := client.NewClient(f.server.URL, "")
	e := &PaperclipMCPGatewayTokenEphemeralResource{client: c}
	r := &PaperclipMCPGatewayTokenResource{client: c}
	var s resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &s)
	m := gatewayTokenTestModel()
	_, open := openGatewayToken(t, e, gatewayEphemeralModel(m), false, true)
	requireToolDiagnostics(t, open.Diagnostics)
	m.ClientLabel = types.StringValue("different consumer")
	create := resource.CreateResponse{State: tfsdk.State{Schema: s.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: toolPlan(t, s.Schema, &m)}, &create)
	if !create.Diagnostics.HasError() || !strings.Contains(create.Diagnostics[0].Detail(), "identical attributes") {
		t.Fatal("mismatched token configuration recorded as converged", create.Diagnostics)
	}
	closed := closeGatewayToken(t, e, open)
	requireToolDiagnostics(t, closed.Diagnostics)
	if f.tokens["token-1"]["revokedAt"] == nil {
		t.Fatal("mismatched producer left live unrecorded token")
	}
}

func TestGatewayTokenRejectsDuplicateLifecycleOwners(t *testing.T) {
	f := newGatewayFixture(t)
	_, _, _, _ = createGateway(t, f)
	ctx := context.Background()
	c, _ := client.NewClient(f.server.URL, "")
	e := &PaperclipMCPGatewayTokenEphemeralResource{client: c}
	r := &PaperclipMCPGatewayTokenResource{client: c}
	var s resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &s)
	m := gatewayTokenTestModel()
	_, open := openGatewayToken(t, e, gatewayEphemeralModel(m), false, true)
	requireToolDiagnostics(t, open.Diagnostics)
	first := resource.CreateResponse{State: tfsdk.State{Schema: s.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: toolPlan(t, s.Schema, &m)}, &first)
	requireToolDiagnostics(t, first.Diagnostics)
	second := resource.CreateResponse{State: tfsdk.State{Schema: s.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: toolPlan(t, s.Schema, &m)}, &second)
	if !second.Diagnostics.HasError() {
		t.Fatal("two lifecycle records claimed the same native gateway token")
	}
	var conflicting gatewayTokenModel
	requireToolDiagnostics(t, first.State.Get(ctx, &conflicting))
	conflicting.OwnerID = types.StringValue("another-owner")
	conflictState := toolState(t, s.Schema, &conflicting)
	read := resource.ReadResponse{State: conflictState}
	r.Read(ctx, resource.ReadRequest{State: conflictState}, &read)
	if !read.Diagnostics.HasError() || !read.State.Raw.Equal(conflictState.Raw) {
		t.Fatal("refresh silently claimed a conflicting lifecycle owner")
	}
}
