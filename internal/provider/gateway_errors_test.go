package provider

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestGatewayResourcesRejectMalformedProtocolValuesWithoutMutation(t *testing.T) {
	ctx := context.Background()
	for _, r := range []resource.Resource{NewPaperclipMCPGatewayResource(), NewPaperclipMCPGatewayTokenResource()} {
		var s resource.SchemaResponse
		r.Schema(ctx, resource.SchemaRequest{}, &s)
		badPlan := tfsdk.Plan{Schema: s.Schema, Raw: tftypes.NewValue(tftypes.String, "invalid")}
		badState := tfsdk.State{Schema: s.Schema, Raw: badPlan.Raw}
		create := resource.CreateResponse{}
		r.Create(ctx, resource.CreateRequest{Plan: badPlan}, &create)
		read := resource.ReadResponse{}
		r.Read(ctx, resource.ReadRequest{State: badState}, &read)
		update := resource.UpdateResponse{}
		r.Update(ctx, resource.UpdateRequest{Plan: badPlan, State: badState}, &update)
		del := resource.DeleteResponse{}
		r.Delete(ctx, resource.DeleteRequest{State: badState}, &del)
		modify := resource.ModifyPlanResponse{}
		r.(resource.ResourceWithModifyPlan).ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: badPlan}, &modify)
		for _, d := range []diag.Diagnostics{create.Diagnostics, read.Diagnostics, update.Diagnostics, del.Diagnostics, modify.Diagnostics} {
			if !d.HasError() {
				t.Fatal("malformed protocol value accepted")
			}
		}
		empty := resource.ModifyPlanResponse{}
		r.(resource.ResourceWithModifyPlan).ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: tfsdk.Plan{Schema: s.Schema}}, &empty)
		requireToolDiagnostics(t, empty.Diagnostics)
		for _, data := range []interface{}{nil, "invalid"} {
			resp := resource.ConfigureResponse{}
			r.(resource.ResourceWithConfigure).Configure(ctx, resource.ConfigureRequest{ProviderData: data}, &resp)
			if resp.Diagnostics.HasError() != (data != nil) {
				t.Fatal("configure accepted invalid provider identity")
			}
		}
	}
}
func TestGatewayAPIErrorsPreserveStateAndOriginalCause(t *testing.T) {
	ctx := context.Background()
	for _, operation := range []string{"create", "read", "update-read", "update-write", "delete-read", "delete-write"} {
		t.Run(operation, func(t *testing.T) {
			f := newGatewayFixture(t)
			r, s, m, state := createGateway(t, f)
			switch operation {
			case "create":
				f.fail = "POST /api/companies/comp-1/tools/gateways"
				m.Slug = types.StringValue("second")
				m.OwnerID = types.StringNull()
			case "update-write", "delete-write":
				f.fail = "PATCH /api/tool-gateway/gateways/gateway-1"
			default:
				f.fail = "GET /api/companies/comp-1/tools/gateways"
			}
			var d diag.Diagnostics
			switch operation {
			case "create":
				resp := resource.CreateResponse{State: state}
				r.Create(ctx, resource.CreateRequest{Plan: toolPlan(t, s, &m)}, &resp)
				d = resp.Diagnostics
			case "read":
				resp := resource.ReadResponse{State: state}
				r.Read(ctx, resource.ReadRequest{State: state}, &resp)
				d = resp.Diagnostics
				if !resp.State.Raw.Equal(state.Raw) {
					t.Fatal("read error removed state")
				}
			case "update-read", "update-write":
				resp := resource.UpdateResponse{State: state}
				r.Update(ctx, resource.UpdateRequest{Plan: toolPlan(t, s, &m), State: state}, &resp)
				d = resp.Diagnostics
			default:
				resp := resource.DeleteResponse{}
				r.Delete(ctx, resource.DeleteRequest{State: state}, &resp)
				d = resp.Diagnostics
			}
			if !d.HasError() || !strings.Contains(d[0].Detail(), "fixture original cause") || strings.Contains(d[0].Detail(), "pcgw_private") {
				t.Fatal("lost original cause or leaked token", d)
			}
		})
	}
}
func TestGatewayOwnershipAndMissingUpdateFailBeforeWrite(t *testing.T) {
	f := newGatewayFixture(t)
	r, s, m, state := createGateway(t, f)
	ctx := context.Background()
	before := len(f.events)
	duplicate := m
	duplicate.OwnerID = types.StringValue("another-owner")
	resp := resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{Plan: toolPlan(t, s, &duplicate), State: state}, &resp)
	if !resp.Diagnostics.HasError() || len(f.events) != before {
		t.Fatal("duplicate gateway owner mutated")
	}
	read := resource.ReadResponse{State: toolState(t, s, &duplicate)}
	r.Read(ctx, resource.ReadRequest{State: read.State}, &read)
	if !read.Diagnostics.HasError() {
		t.Fatal("read silently claimed another owner")
	}
	m.ID = types.StringValue("missing")
	resp = resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{Plan: toolPlan(t, s, &m), State: state}, &resp)
	if !resp.Diagnostics.HasError() || len(f.events) != before {
		t.Fatal("missing gateway was replaced")
	}
	for _, id := range []string{"bad", "/g", "c/"} {
		imp := resource.ImportStateResponse{}
		r.ImportState(ctx, resource.ImportStateRequest{ID: id}, &imp)
		if !imp.Diagnostics.HasError() {
			t.Fatal("invalid import accepted")
		}
	}
}
func TestGatewayTokenInventoryFailuresRetainLifecycleRecord(t *testing.T) {
	for _, mode := range []string{"denied", "archived", "invalid-expiry", "drift", "missing", "revoke-denied"} {
		t.Run(mode, func(t *testing.T) {
			f := newGatewayFixture(t)
			_, _, _, _ = createGateway(t, f)
			ctx := context.Background()
			c, _ := client.NewClient(f.server.URL, "")
			r := &PaperclipMCPGatewayTokenResource{client: c}
			e := &PaperclipMCPGatewayTokenEphemeralResource{client: c}
			m := gatewayTokenTestModel()
			var s resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &s)
			_, open := openGatewayToken(t, e, gatewayEphemeralModel(m), false, true)
			requireToolDiagnostics(t, open.Diagnostics)
			created := resource.CreateResponse{State: tfsdk.State{Schema: s.Schema}}
			r.Create(ctx, resource.CreateRequest{Plan: toolPlan(t, s.Schema, &m)}, &created)
			requireToolDiagnostics(t, created.Diagnostics)
			switch mode {
			case "denied":
				f.fail = "GET /api/companies/comp-1/tools/gateways"
			case "archived":
				f.gateways["gateway-1"]["status"] = "archived"
			case "invalid-expiry":
				f.tokens["token-1"]["expiresAt"] = nil
			case "drift":
				f.tokens["token-1"]["expiresAt"] = "2101-01-01T00:00:00Z"
			case "missing":
				delete(f.tokens, "token-1")
			case "revoke-denied":
				f.fail = "POST /api/tool-gateway/gateway-tokens/token-1/revoke"
			}
			if mode == "revoke-denied" {
				del := resource.DeleteResponse{}
				r.Delete(ctx, resource.DeleteRequest{State: created.State}, &del)
				if !del.Diagnostics.HasError() {
					t.Fatal("revoke failure hidden")
				}
				return
			}
			read := resource.ReadResponse{State: created.State}
			r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
			if mode == "missing" {
				requireToolDiagnostics(t, read.Diagnostics)
				if !read.State.Raw.IsNull() {
					t.Fatal("verified absent token retained")
				}
				return
			}
			if mode == "drift" {
				requireToolDiagnostics(t, read.Diagnostics)
				requireToolDiagnostics(t, read.State.Get(ctx, &m))
				if m.ExpiresAt.ValueString() != "2101-01-01T00:00:00Z" {
					t.Fatal("expiry drift masked")
				}
				return
			}
			if !read.Diagnostics.HasError() || !read.State.Raw.Equal(created.State.Raw) {
				t.Fatal("unreadable token lost its durable record")
			}
		})
	}
}
func TestGatewayEphemeralErrorsCleanUnconfirmedTokens(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []string{"unconfigured", "invalid-config", "invalid-reference", "private-error", "private-error-revoke-denied"} {
		t.Run(mode, func(t *testing.T) {
			f := newGatewayFixture(t)
			_, _, _, _ = createGateway(t, f)
			c, _ := client.NewClient(f.server.URL, "")
			r := &PaperclipMCPGatewayTokenEphemeralResource{client: c}
			m := gatewayEphemeralModel(gatewayTokenTestModel())
			switch mode {
			case "unconfigured":
				r.client = nil
			case "invalid-config":
				m.ExpiresAt = types.StringValue("bad")
			case "invalid-reference":
				m.GatewayID = types.StringValue("missing")
			default:
				old := jsonMarshal
				jsonMarshal = func(interface{}) ([]byte, error) { return nil, errors.New("private encoding failed") }
				t.Cleanup(func() { jsonMarshal = old })
				if mode == "private-error-revoke-denied" {
					f.fail = "POST /api/tool-gateway/gateway-tokens/token-1/revoke"
				}
			}
			_, open := openGatewayToken(t, r, m, false, true)
			if !open.Diagnostics.HasError() {
				t.Fatal("ephemeral failure hidden")
			}
			if mode == "private-error" && f.tokens["token-1"]["revokedAt"] == nil {
				t.Fatal("private-state error leaked mint")
			}
		})
	}
	r := &PaperclipMCPGatewayTokenEphemeralResource{}
	var s ephemeral.SchemaResponse
	r.Schema(ctx, ephemeral.SchemaRequest{}, &s)
	resp := ephemeral.OpenResponse{}
	r.Open(ctx, ephemeral.OpenRequest{Config: tfsdk.Config{Schema: s.Schema, Raw: tftypes.NewValue(tftypes.String, "bad")}}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("malformed ephemeral config accepted")
	}
	for _, data := range []interface{}{nil, "invalid"} {
		configure := ephemeral.ConfigureResponse{}
		r.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: data}, &configure)
		if configure.Diagnostics.HasError() != (data != nil) {
			t.Fatal("ephemeral configure identity mismatch")
		}
	}
	c, _ := client.NewClient("http://fixture", "")
	configure := ephemeral.ConfigureResponse{}
	r.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: c}, &configure)
	requireToolDiagnostics(t, configure.Diagnostics)
	close := ephemeral.CloseResponse{}
	r.Close(ctx, ephemeral.CloseRequest{}, &close)
	requireToolDiagnostics(t, close.Diagnostics)
	open := ephemeral.OpenResponse{}
	initOpenResponsePrivate(&open)
	setPrivateStateKey(ctx, open.Private, "token_id", false, &open.Diagnostics)
	closed := closeGatewayToken(t, r, open)
	if !closed.Diagnostics.HasError() {
		t.Fatal("malformed cleanup identity accepted")
	}
	empty := ephemeral.OpenResponse{}
	initOpenResponsePrivate(&empty)
	closed = closeGatewayToken(t, r, empty)
	requireToolDiagnostics(t, closed.Diagnostics)
}

func TestGatewayTokenPlanAndApplyRejectUnsupportedChanges(t *testing.T) {
	f := newGatewayFixture(t)
	_, _, _, _ = createGateway(t, f)
	ctx := context.Background()
	r := &PaperclipMCPGatewayTokenResource{}
	s := configureTestResource(t, f.server.URL, r)
	m := gatewayTokenTestModel()
	m.ID = types.StringValue("prior-token")
	state := toolState(t, s, &m)
	for _, mode := range []string{"invalid-config", "invalid-reference", "unsupported", "bad-prior", "unchanged", "new"} {
		t.Run(mode, func(t *testing.T) {
			next := m
			prior := state
			switch mode {
			case "invalid-config":
				next.ExpiresAt = types.StringValue("bad")
			case "invalid-reference":
				next.GatewayID = types.StringValue("missing")
			case "unsupported":
				next.ClientLabel = types.StringValue("new label")
			case "bad-prior":
				prior.Raw = tftypes.NewValue(tftypes.String, "bad")
			case "new":
				prior = tfsdk.State{Schema: s}
			}
			plan := toolPlan(t, s, &next)
			resp := resource.ModifyPlanResponse{Plan: plan}
			r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: prior}, &resp)
			if resp.Diagnostics.HasError() != (mode != "unchanged" && mode != "new") {
				t.Fatal("token plan accepted unsupported change or rejected supported metadata", resp.Diagnostics)
			}
			if mode == "unsupported" {
				update := resource.UpdateResponse{State: state}
				r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &update)
				if !update.Diagnostics.HasError() {
					t.Fatal("apply ignored unsupported attribute change")
				}
			}
			if mode == "invalid-reference" {
				create := resource.CreateResponse{State: tfsdk.State{Schema: s}}
				r.Create(ctx, resource.CreateRequest{Plan: plan}, &create)
				if !create.Diagnostics.HasError() {
					t.Fatal("apply did not validate gateway before delivery record")
				}
			}
		})
	}
	f.finishProfile = true
	gateway := &PaperclipMCPGatewayResource{}
	configureTestResource(t, f.server.URL, gateway)
	if err := gateway.validate(ctx, gatewayTestModel(), true); err == nil {
		t.Fatal("finish-owned profile claimed by gateway")
	}
}

func TestGatewayCompanyChangesPreserveIdentityAndSendNoMutation(t *testing.T) {
	f := newGatewayFixture(t)
	r, s, m, state := createGateway(t, f)
	ctx := context.Background()
	before := len(f.events)
	m.CompanyID = types.StringValue("another-company")
	plan := toolPlan(t, s, &m)
	modify := resource.ModifyPlanResponse{Plan: plan}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: state}, &modify)
	update := resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &update)
	if !modify.Diagnostics.HasError() || !update.Diagnostics.HasError() || len(f.events) != before || !update.State.Raw.Equal(state.Raw) {
		t.Fatal("company move replaced or mutated gateway identity")
	}
	m.CompanyID = types.StringValue("comp-1")
	plan = toolPlan(t, s, &m)
	bad := state
	bad.Raw = tftypes.NewValue(tftypes.String, "invalid")
	modify = resource.ModifyPlanResponse{Plan: plan}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: bad}, &modify)
	if !modify.Diagnostics.HasError() {
		t.Fatal("invalid prior gateway state accepted")
	}
	modify = resource.ModifyPlanResponse{Plan: plan}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: state}, &modify)
	requireToolDiagnostics(t, modify.Diagnostics)
}
