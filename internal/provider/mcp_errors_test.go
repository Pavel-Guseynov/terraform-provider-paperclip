package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestGrantRejectsForeignCompanySecretInventoryBeforeMutation(t *testing.T) {
	f, c := newMCPFixture(t)
	f.secretCompany = "other-company"
	r := &PaperclipConnectionGrantsResource{client: c}
	model := initialGrantModel(t)
	if err := r.reconcile(context.Background(), &model, connectionGrantsModel{}); err == nil || !strings.Contains(err.Error(), "different company") || len(f.mutations) != 0 {
		t.Fatalf("foreign secret inventory must fail without mutation: error=%v writes=%v", err, f.mutations)
	}
}

func TestGrantReconciliationRecoversNativeBoundaryFailuresWithoutDuplicateGrants(t *testing.T) {
	for _, tc := range []struct {
		name, route string
		at          int
	}{
		{"connection read", "GET /api/tool-connections/conn-1", 0},
		{"inventory read", "GET /api/tool-connections/conn-1/grants", 1},
		{"installation write", "POST /api/tool-connections/conn-1/grants/installations", 0},
		{"old grant revocation", "DELETE /api/tool-connections/conn-1/grants/grant-0", 0},
		{"audience write", "PUT /api/tool-connections/conn-1/grants/grant-2/members", 0},
		{"delegation write", "POST /api/tool-connections/conn-1/grants/personal-1/delegations", 0},
		{"post-rotation read", "GET /api/tool-connections/conn-1/grants", 5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, c := newMCPFixture(t)
			r := &PaperclipConnectionGrantsResource{client: c}
			model := initialGrantModel(t)
			f.fail = tc.route
			f.failAt = tc.at
			if err := r.reconcile(context.Background(), &model, connectionGrantsModel{}); err == nil || !strings.Contains(err.Error(), "injected boundary failure") {
				t.Fatalf("boundary failure was not retained: %v", err)
			}
			f.fail = ""
			if err := r.reconcile(context.Background(), &model, connectionGrantsModel{}); err != nil {
				t.Fatal(err)
			}
			active := 0
			for _, grant := range f.grants {
				if grant["kind"] == "organization" && grant["status"] == "active" {
					active++
				}
			}
			if active != 1 || !model.Ready.ValueBool() {
				t.Fatalf("recovery left multiple active grants: %d", active)
			}
		})
	}
}

func TestGrantPlanValidatesReferencesAndRejectsSecondOwner(t *testing.T) {
	ctx := context.Background()
	_, c := newMCPFixture(t)
	r := &PaperclipConnectionGrantsResource{client: c}
	var s resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &s)
	model := initialGrantModel(t)
	plan := grantPlan(t, s, model)
	first := resource.ModifyPlanResponse{Plan: plan}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan}, &first)
	if first.Diagnostics.HasError() {
		t.Fatal(first.Diagnostics)
	}
	second := resource.ModifyPlanResponse{Plan: plan}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan}, &second)
	if !second.Diagnostics.HasError() || second.Diagnostics.Errors()[0].Summary() != "Connection grant ownership conflict" {
		t.Fatalf("second owner accepted: %v", second.Diagnostics)
	}
	model.ConnectionID = types.StringUnknown()
	unknown := resource.ModifyPlanResponse{Plan: grantPlan(t, s, model)}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: unknown.Plan}, &unknown)
	if unknown.Diagnostics.HasError() {
		t.Fatal(unknown.Diagnostics)
	}
	model.ConnectionID = types.StringValue("conn-1")
	model.OwnerID = types.StringValue("invalid-reference-owner")
	model.CompanyID = types.StringValue("other-company")
	model.SecretRefs = types.MapNull(secretRefType())
	_, other := newMCPFixture(t)
	r.client = other
	invalid := resource.ModifyPlanResponse{Plan: grantPlan(t, s, model)}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: invalid.Plan}, &invalid)
	if !invalid.Diagnostics.HasError() || invalid.Diagnostics.Errors()[0].Summary() != "Invalid connection grant reference" {
		t.Fatal("known cross-company reference did not fail at plan")
	}
}

func TestGrantDestroyPlanDoesNotReadOrWrite(t *testing.T) {
	f, c := newMCPFixture(t)
	r := &PaperclipConnectionGrantsResource{client: c}
	var s resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &s)
	plan := tfsdk.Plan{Schema: s.Schema, Raw: tftypes.NewValue(s.Schema.Type().TerraformType(context.Background()), nil)}
	resp := resource.ModifyPlanResponse{Plan: plan}
	r.ModifyPlan(context.Background(), resource.ModifyPlanRequest{Plan: plan}, &resp)
	if resp.Diagnostics.HasError() || !resp.Plan.Raw.IsNull() || len(f.mutations) != 0 {
		t.Fatal("destroy plan changed aggregate state")
	}
}

func TestGrantCreateAcceptsUnknownComputedGrantIdentity(t *testing.T) {
	f, c := newMCPFixture(t)
	r := &PaperclipConnectionGrantsResource{client: c}
	model := initialGrantModel(t)
	model.GrantID = types.StringUnknown()
	if err := r.reconcile(context.Background(), &model, connectionGrantsModel{}); err != nil || model.GrantID.ValueString() != "grant-2" || !model.Ready.ValueBool() || f.grants[0]["status"] != "revoked" {
		t.Fatalf("grant create failed to resolve native identity: error=%v model=%v", err, model)
	}
}

func TestConnectionRejectsInvalidApplicationAndTemplateReferencesBeforeMutation(t *testing.T) {
	for _, kind := range []string{"application", "template", "configuration"} {
		t.Run(kind, func(t *testing.T) {
			f, c := newMCPFixture(t)
			r := &PaperclipConnectionResource{client: c}
			model := legacyConnectionModel(t)
			model.ID = types.StringNull()
			switch kind {
			case "application":
				model.AppID = types.StringValue("missing-app")
			case "template":
				model.Transport = types.StringValue("local_stdio")
				model.Config = types.StringValue(`{"templateId":"missing-template"}`)
			case "configuration":
				model.Config = types.StringValue(`{"headers":{"Authorization":"must-not-echo"}}`)
			}
			d := r.validateConnection(context.Background(), model, PaperclipConnectionResourceModel{})
			if !d.HasError() || len(f.mutations) != 0 {
				t.Fatalf("invalid %s reference was accepted: %v", kind, d)
			}
		})
	}
}

func TestConnectionCreationReportsNativeFailureWithoutInventingIdentity(t *testing.T) {
	f, c := newMCPFixture(t)
	f.fail = "POST /api/companies/company-1/tools/connections"
	r := &PaperclipConnectionResource{client: c}
	var s resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &s)
	model := legacyConnectionModel(t)
	model.ID = types.StringNull()
	plan := tfsdk.Plan{Schema: s.Schema}
	if d := plan.Set(context.Background(), &model); d.HasError() {
		t.Fatal(d)
	}
	resp := resource.CreateResponse{State: tfsdk.State{Schema: s.Schema}}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &resp)
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "injected boundary failure") || !resp.State.Raw.IsNull() {
		t.Fatalf("native creation failure was lost: %v", resp.Diagnostics)
	}
}

func createGrantState(t *testing.T, r *PaperclipConnectionGrantsResource) (resource.SchemaResponse, tfsdk.State) {
	t.Helper()
	var s resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &s)
	response := resource.CreateResponse{State: tfsdk.State{Schema: s.Schema}}
	r.Create(context.Background(), resource.CreateRequest{Plan: grantPlan(t, s, initialGrantModel(t))}, &response)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	return s, response.State
}

func TestGrantReadDetectsSupersededCredentialsAndAudienceDrift(t *testing.T) {
	ctx := context.Background()
	f, c := newMCPFixture(t)
	r := &PaperclipConnectionGrantsResource{client: c}
	_, state := createGrantState(t, r)
	f.grants[0]["status"] = "active"
	f.grants[2]["members"] = []interface{}{}
	response := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &response)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	var model connectionGrantsModel
	if d := response.State.Get(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	if model.Ready.ValueBool() || len(model.MemberUserIDs.Elements()) != 0 {
		t.Fatal("read masked active superseded grant or native audience drift")
	}
}

func TestGrantReadPreservesStateOnAmbiguousOrFailedInventory(t *testing.T) {
	for _, kind := range []string{"ambiguous", "failed", "missing"} {
		t.Run(kind, func(t *testing.T) {
			f, c := newMCPFixture(t)
			r := &PaperclipConnectionGrantsResource{client: c}
			_, state := createGrantState(t, r)
			if kind == "ambiguous" {
				f.grants = append(f.grants, map[string]interface{}{"id": "extra", "kind": "organization", "status": "active", "isDefault": true})
			} else {
				f.fail = "GET /api/tool-connections/conn-1/grants"
				if kind == "missing" {
					f.failStatus = 404
				}
			}
			response := resource.ReadResponse{State: state}
			r.Read(context.Background(), resource.ReadRequest{State: state}, &response)
			if kind == "missing" {
				if response.Diagnostics.HasError() || !response.State.Raw.IsNull() {
					t.Fatal("verified missing inventory retained binding")
				}
				return
			}
			if !response.Diagnostics.HasError() || !response.State.Raw.Equal(state.Raw) {
				t.Fatal("failed inventory changed native binding")
			}
		})
	}
}

func TestGrantDeleteRevokesManagedDelegationsAndReportsFailures(t *testing.T) {
	for _, kind := range []string{"delegation", "grant", "inventory", "missing", "success"} {
		t.Run(kind, func(t *testing.T) {
			f, c := newMCPFixture(t)
			r := &PaperclipConnectionGrantsResource{client: c}
			_, state := createGrantState(t, r)
			switch kind {
			case "delegation":
				f.fail = "DELETE /api/tool-connections/conn-1/grants/personal-1/delegations/delegation-1"
			case "grant":
				f.fail = "DELETE /api/tool-connections/conn-1/grants/grant-2"
			case "inventory", "missing":
				f.fail = "GET /api/tool-connections/conn-1/grants"
				if kind == "missing" {
					f.failStatus = 404
				}
			}
			response := resource.DeleteResponse{}
			r.Delete(context.Background(), resource.DeleteRequest{State: state}, &response)
			if response.Diagnostics.HasError() != (kind != "success" && kind != "missing") {
				t.Fatalf("incorrect deletion failure result: %v", response.Diagnostics)
			}
			if kind == "success" && (f.grants[2]["status"] != "revoked" || len(f.grants[1]["delegations"].([]interface{})) != 0) {
				t.Fatal("destroy left managed access active")
			}
		})
	}
}

func TestGrantImportUsesCompanyAndConnectionWithoutRecreatingIdentity(t *testing.T) {
	ctx := context.Background()
	_, c := newMCPFixture(t)
	r := &PaperclipConnectionGrantsResource{client: c}
	s, state := createGrantState(t, r)
	for _, id := range []string{"", "conn-1", "company-1/", "/conn-1", "company-1/conn-1/extra", "company-1/conn-1"} {
		t.Run(id, func(t *testing.T) {
			response := resource.ImportStateResponse{State: tfsdk.State{Schema: s.Schema, Raw: state.Raw}}
			r.ImportState(ctx, resource.ImportStateRequest{ID: id}, &response)
			if id != "company-1/conn-1" {
				if !response.Diagnostics.HasError() {
					t.Fatal("incomplete import accepted")
				}
				return
			}
			if response.Diagnostics.HasError() {
				t.Fatal(response.Diagnostics)
			}
			read := resource.ReadResponse{State: response.State}
			r.Read(ctx, resource.ReadRequest{State: response.State}, &read)
			if read.Diagnostics.HasError() {
				t.Fatal(read.Diagnostics)
			}
			var imported types.String
			if d := read.State.GetAttribute(ctx, path.Root("id"), &imported); d.HasError() {
				t.Fatal(d)
			}
			if imported.ValueString() != "conn-1" {
				t.Fatal("import replaced identity")
			}
		})
	}
}

func TestGrantImportAdoptsExistingPersonalDelegationsWithoutMutation(t *testing.T) {
	ctx := context.Background()
	f, c := newMCPFixture(t)
	f.grants[1]["delegations"] = []interface{}{map[string]interface{}{"id": "existing-delegation", "agentId": "agent-1"}}
	r := &PaperclipConnectionGrantsResource{client: c}
	var s resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &s)
	imported := resource.ImportStateResponse{State: tfsdk.State{Schema: s.Schema}}
	empty := connectionGrantsModel{SecretRefs: types.MapNull(secretRefType()), MemberUserIDs: types.SetNull(types.StringType), Delegations: types.MapNull(types.SetType{ElemType: types.StringType})}
	if d := imported.State.Set(ctx, &empty); d.HasError() {
		t.Fatal(d)
	}
	r.ImportState(ctx, resource.ImportStateRequest{ID: "company-1/conn-1"}, &imported)
	read := resource.ReadResponse{State: imported.State}
	r.Read(ctx, resource.ReadRequest{State: imported.State}, &read)
	var state connectionGrantsModel
	d := read.State.Get(ctx, &state)
	if imported.Diagnostics.HasError() || read.Diagnostics.HasError() || d.HasError() || !containsString(delegationMap(ctx, state.Delegations)["personal-1"], "agent-1") || len(f.mutations) != 0 {
		t.Fatalf("import did not adopt native delegations without mutation: state=%v diagnostics=%v/%v/%v", state, imported.Diagnostics, read.Diagnostics, d)
	}
}

func TestApplicationOwnerReferencesAreVerifiedBeforeMutation(t *testing.T) {
	for _, kind := range []string{"agent failure", "agent company", "plugin failure"} {
		t.Run(kind, func(t *testing.T) {
			f, c := newMCPFixture(t)
			r := &PaperclipAppResource{client: c}
			plan := PaperclipAppResourceModel{CompanyID: types.StringValue("company-1"), Type: types.StringValue("mcp_http"), OwnerAgentID: types.StringValue("agent-1")}
			switch kind {
			case "agent failure":
				f.fail = "GET /api/agents/agent-1"
			case "agent company":
				plan.CompanyID = types.StringValue("other-company")
			case "plugin failure":
				plan.OwnerAgentID = types.StringNull()
				plan.PluginID = types.StringValue("plugin-1")
				f.fail = "GET /api/plugins/plugin-1"
			}
			if _, err := r.createAppState(context.Background(), plan); err == nil || len(f.mutations) != 0 {
				t.Fatalf("invalid owner caused a write: %v", err)
			}
			if _, err := r.updateAppState(context.Background(), plan, plan); err == nil {
				t.Fatal("update did not revalidate owner before writing")
			}
		})
	}
}

func TestNativeConfigurationValidatorsRejectUnsafeProjectionAndDocumentTheirContract(t *testing.T) {
	ctx := context.Background()
	for _, v := range []validator.String{nativeTransportConfigValidator{}, nativeCredentialProjectionValidator{}, applicationMetadataValidator{}} {
		if v.Description(ctx) == "" || v.MarkdownDescription(ctx) == "" {
			t.Fatal("validator omitted its native input contract")
		}
	}
	for _, raw := range []string{`{"safe":true}`, `{"token":"never-print-this"}`} {
		response := validator.StringResponse{}
		nativeTransportConfigValidator{}.ValidateString(ctx, validator.StringRequest{ConfigValue: types.StringValue(raw)}, &response)
		if response.Diagnostics.HasError() != strings.Contains(raw, "token") {
			t.Fatal("transport sensitive-key validation was ineffective")
		}
	}
	base := `{"name":"api","secretId":"secret-1","key":"Authorization","placement":"header"}`
	for _, tc := range []struct {
		name, raw string
		valid     bool
	}{
		{"base", "[" + base + "]", true}, {"latest", `[{"name":"api","secretId":"secret-1","key":"x","placement":"env","version":"latest"}]`, true},
		{"pinned", `[{"name":"api","secretId":"secret-1","key":"x","placement":"query","version":2}]`, true},
		{"zero", `[{"name":"api","secretId":"secret-1","key":"x","placement":"env","version":0}]`, false},
		{"string pin", `[{"name":"api","secretId":"secret-1","key":"x","placement":"env","version":"2"}]`, false},
		{"boolean version", `[{"name":"api","secretId":"secret-1","key":"x","placement":"env","version":false}]`, false},
		{"placement", `[{"name":"api","secretId":"secret-1","key":"x","placement":"body"}]`, false},
		{"missing key", `[{"name":"api","secretId":"secret-1","placement":"env"}]`, false},
		{"plaintext", `[{"value":"never-print-this"}]`, false}, {"null", "null", false}, {"empty", "[]", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := validator.StringResponse{}
			nativeCredentialProjectionValidator{}.ValidateString(ctx, validator.StringRequest{ConfigValue: types.StringValue(tc.raw)}, &response)
			if response.Diagnostics.HasError() == tc.valid {
				t.Fatalf("unexpected native projection diagnostic: %v", response.Diagnostics)
			}
			for _, d := range response.Diagnostics {
				if strings.Contains(d.Detail(), "never-print-this") {
					t.Fatal("plaintext appeared in diagnostic")
				}
			}
		})
	}
}

func TestGrantPreflightRejectsInactiveAudienceAndCrossCompanyDelegatedAgent(t *testing.T) {
	for _, kind := range []string{"inactive member", "agent company"} {
		t.Run(kind, func(t *testing.T) {
			f, c := newMCPFixture(t)
			r := &PaperclipConnectionGrantsResource{client: c}
			if kind == "inactive member" {
				f.memberStatus = "suspended"
			} else {
				f.agentCompany = "other"
			}
			model := initialGrantModel(t)
			if err := r.reconcile(context.Background(), &model, connectionGrantsModel{}); err == nil || len(f.mutations) != 0 {
				t.Fatal("native reference preflight allowed an invalid principal")
			}
		})
	}
}

func TestGrantRepeatedApplyPreservesExistingDelegationIdentity(t *testing.T) {
	f, c := newMCPFixture(t)
	r := &PaperclipConnectionGrantsResource{client: c}
	model := initialGrantModel(t)
	if err := r.reconcile(context.Background(), &model, connectionGrantsModel{}); err != nil {
		t.Fatal(err)
	}
	before := len(f.mutations)
	if err := r.reconcile(context.Background(), &model, model); err != nil {
		t.Fatal(err)
	}
	for _, mutation := range f.mutations[before:] {
		if strings.Contains(mutation, "/delegations") {
			t.Fatal("converged delegation was recreated")
		}
	}
}

func TestGrantFailurePreservesConnectionIdentityForRecovery(t *testing.T) {
	for _, stage := range []string{"create", "update"} {
		t.Run(stage, func(t *testing.T) {
			ctx := context.Background()
			f, c := newMCPFixture(t)
			r := &PaperclipConnectionGrantsResource{}
			r.Configure(ctx, resource.ConfigureRequest{ProviderData: c}, &resource.ConfigureResponse{})
			var s resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &s)
			f.fail = "PUT /api/tool-connections/conn-1/grants/grant-2/members"
			plan := grantPlan(t, s, initialGrantModel(t))
			var state tfsdk.State
			if stage == "create" {
				response := resource.CreateResponse{State: tfsdk.State{Schema: s.Schema}}
				r.Create(ctx, resource.CreateRequest{Plan: plan}, &response)
				if !response.Diagnostics.HasError() {
					t.Fatal("partial transaction reported success")
				}
				state = response.State
			} else {
				f.fail = ""
				_, prior := createGrantState(t, r)
				var model connectionGrantsModel
				if d := prior.Get(ctx, &model); d.HasError() {
					t.Fatal(d)
				}
				model.SecretRefs = protectedRefs(t, "2")
				model.OwnerID = types.StringNull()
				f.fail = "PUT /api/tool-connections/conn-1/grants/grant-3/members"
				response := resource.UpdateResponse{State: prior}
				r.Update(ctx, resource.UpdateRequest{Plan: grantPlan(t, s, model), State: prior}, &response)
				if !response.Diagnostics.HasError() {
					t.Fatal("failed update reported convergence")
				}
				state = response.State
			}
			var model connectionGrantsModel
			if d := state.Get(ctx, &model); d.HasError() {
				t.Fatal(d)
			}
			if model.ID.ValueString() != "conn-1" || model.Ready.ValueBool() {
				t.Fatal("partial transaction lost its connection identity or pending status")
			}
		})
	}
}

func TestGrantRequestsRejectMalformedStateWithoutMutation(t *testing.T) {
	ctx := context.Background()
	f, c := newMCPFixture(t)
	r := &PaperclipConnectionGrantsResource{client: c}
	var s resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &s)
	broken := tftypes.NewValue(tftypes.String, "invalid-state")
	plan := tfsdk.Plan{Schema: s.Schema, Raw: broken}
	state := tfsdk.State{Schema: s.Schema, Raw: broken}
	created := resource.CreateResponse{State: state}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
	if !created.Diagnostics.HasError() {
		t.Fatal("create accepted malformed state")
	}
	updated := resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &updated)
	if !updated.Diagnostics.HasError() {
		t.Fatal("update accepted malformed state")
	}
	read := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &read)
	if !read.Diagnostics.HasError() {
		t.Fatal("read accepted malformed state")
	}
	deleted := resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: state}, &deleted)
	if !deleted.Diagnostics.HasError() {
		t.Fatal("delete accepted malformed state")
	}
	planned := resource.ModifyPlanResponse{Plan: plan}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan}, &planned)
	if !planned.Diagnostics.HasError() {
		t.Fatal("plan accepted malformed state")
	}
	nilConfig := resource.ConfigureResponse{}
	r.Configure(ctx, resource.ConfigureRequest{}, &nilConfig)
	if nilConfig.Diagnostics.HasError() {
		t.Fatal("unknown provider configuration must defer")
	}
	invalidConfig := resource.ConfigureResponse{}
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: "invalid"}, &invalidConfig)
	if !invalidConfig.Diagnostics.HasError() {
		t.Fatal("invalid provider configuration accepted")
	}
	if len(f.mutations) != 0 {
		t.Fatal("malformed state caused a native mutation")
	}
}

func TestGrantInventoryRejectsMalformedEnvelopeAndRemovesRetiredBinding(t *testing.T) {
	for _, kind := range []string{"malformed", "revoked"} {
		t.Run(kind, func(t *testing.T) {
			f, c := newMCPFixture(t)
			r := &PaperclipConnectionGrantsResource{client: c}
			_, state := createGrantState(t, r)
			if kind == "malformed" {
				f.malformedGrants = true
			} else {
				for _, g := range f.grants {
					g["status"] = "revoked"
				}
			}
			response := resource.ReadResponse{State: state}
			r.Read(context.Background(), resource.ReadRequest{State: state}, &response)
			if kind == "malformed" {
				if !response.Diagnostics.HasError() || !response.State.Raw.Equal(state.Raw) {
					t.Fatal("malformed grant inventory changed state")
				}
			} else if response.Diagnostics.HasError() || !response.State.Raw.IsNull() {
				t.Fatal("retired native grant was retained")
			}
		})
	}
}

func TestRevokingConnectionGrantsPropagatesInventoryFailureAndAcceptsAbsentGrant(t *testing.T) {
	f, c := newMCPFixture(t)
	f.fail = "GET /api/tool-connections/conn-1/grants"
	if err := c.RevokeConnectionCredentialGrants(context.Background(), "conn-1"); err == nil {
		t.Fatal("inventory failure was ignored")
	}
	f.fail = ""
	f.grants = []map[string]interface{}{}
	if err := c.RevokeConnectionCredentialGrants(context.Background(), "conn-1"); err != nil {
		t.Fatal(err)
	}
}

func TestNestedPluginReferencesUseTheSameProtectedSecretValidation(t *testing.T) {
	f, c := newMCPFixture(t)
	raw := `{"nested":{"references":[{"type":"secret_ref","secret_id":"secret-1"}]}}`
	if d := ValidatePluginConfigJSONApply(context.Background(), c, "company-1", raw); d.HasError() {
		t.Fatal(d)
	}
	if len(f.mutations) != 0 {
		t.Fatal("reference validation mutated native secrets")
	}
}

func TestApplicationPlanHandlesDestroyUnknownAndMalformedStateWithoutMutation(t *testing.T) {
	ctx := context.Background()
	f, c := newMCPFixture(t)
	r := &PaperclipAppResource{client: c}
	var s resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &s)
	plan := tfsdk.Plan{Schema: s.Schema}
	model := PaperclipAppResourceModel{CompanyID: types.StringValue("company-1"), Name: types.StringValue("app"), Type: types.StringValue("mcp_http")}
	if d := plan.Set(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	empty := tfsdk.State{Schema: s.Schema, Raw: tftypes.NewValue(s.Schema.Type().TerraformType(ctx), nil)}
	created := resource.ModifyPlanResponse{Plan: plan}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: empty}, &created)
	if created.Diagnostics.HasError() {
		t.Fatal(created.Diagnostics)
	}
	destroyPlan := tfsdk.Plan{Schema: s.Schema, Raw: empty.Raw}
	destroy := resource.ModifyPlanResponse{Plan: destroyPlan}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: destroyPlan}, &destroy)
	if destroy.Diagnostics.HasError() {
		t.Fatal(destroy.Diagnostics)
	}
	broken := tfsdk.State{Schema: s.Schema, Raw: tftypes.NewValue(tftypes.String, "invalid")}
	bad := resource.ModifyPlanResponse{Plan: plan}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: broken}, &bad)
	if !bad.Diagnostics.HasError() {
		t.Fatal("malformed state was ignored")
	}
	model.OwnerAgentID = types.StringValue("agent-1")
	f.fail = "GET /api/agents/agent-1"
	if d := plan.Set(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	owner := resource.ModifyPlanResponse{Plan: plan}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: empty}, &owner)
	if !owner.Diagnostics.HasError() {
		t.Fatal("known invalid application owner accepted at plan")
	}
	if len(f.mutations) != 0 {
		t.Fatal("plan mutated native application")
	}
}

func TestGrantAggregateRejectsDuplicateCreateAndUpdateBeforeNativeMutation(t *testing.T) {
	ctx := context.Background()
	f, c := newMCPFixture(t)
	r := &PaperclipConnectionGrantsResource{client: c}
	s, state := createGrantState(t, r)
	count := len(f.mutations)
	create := resource.CreateResponse{State: tfsdk.State{Schema: s.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: grantPlan(t, s, initialGrantModel(t))}, &create)
	if !create.Diagnostics.HasError() {
		t.Fatal("duplicate grant owner accepted")
	}
	var model connectionGrantsModel
	if d := state.Get(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	model.OwnerID = types.StringValue("different-owner")
	update := resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{Plan: grantPlan(t, s, model), State: state}, &update)
	if !update.Diagnostics.HasError() || len(f.mutations) != count {
		t.Fatal("conflicting grant update mutated native access")
	}
}

func legacyConnectionModel(t *testing.T) PaperclipConnectionResourceModel {
	return PaperclipConnectionResourceModel{ID: types.StringValue("conn-1"), CompanyID: types.StringValue("company-1"), AppID: types.StringValue("app-1"), Name: types.StringValue("connection"), Transport: types.StringValue("mcp_remote"), AuthKind: types.StringValue("api_key"), Config: types.StringValue(`{"url":"https://example.test/mcp"}`), CredentialRefs: types.StringValue(`[{"name":"api","secretId":"secret-1","placement":"header","key":"Authorization"}]`), CredentialSecretRefs: types.MapNull(secretRefType()), CredentialGrantReady: types.BoolValue(true)}
}

func TestRemovedCredentialProjectionRevocationFailureRemainsDriftUntilRecovered(t *testing.T) {
	ctx := context.Background()
	f, c := newMCPFixture(t)
	r := &PaperclipConnectionResource{client: c}
	var s resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &s)
	model := legacyConnectionModel(t)
	state := tfsdk.State{Schema: s.Schema}
	if d := state.Set(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	plan := tfsdk.Plan{Schema: s.Schema}
	model.CredentialRefs = types.StringNull()
	if d := plan.Set(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	f.fail = "DELETE /api/tool-connections/conn-1/grants/grant-0"
	failed := resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &failed)
	if !failed.Diagnostics.HasError() || f.grants[0]["status"] != "active" {
		t.Fatal("revocation failure was hidden")
	}
	f.fail = "GET /api/tool-connections/conn-1/grants"
	failedRead := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &failedRead)
	if !failedRead.Diagnostics.HasError() || !failedRead.State.Raw.Equal(state.Raw) {
		t.Fatal("grant inventory error changed prior state")
	}
	f.fail = ""
	read := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &read)
	if read.Diagnostics.HasError() {
		t.Fatal(read.Diagnostics)
	}
	var pending PaperclipConnectionResourceModel
	if d := read.State.Get(ctx, &pending); d.HasError() {
		t.Fatal(d)
	}
	if pending.CredentialGrantReady.ValueBool() {
		t.Fatal("orphaned active credentials were reported converged")
	}
	if d := plan.Set(ctx, &pending); d.HasError() {
		t.Fatal(d)
	}
	retried := resource.UpdateResponse{State: read.State}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: read.State}, &retried)
	if retried.Diagnostics.HasError() || f.grants[0]["status"] != "revoked" {
		t.Fatalf("retry did not revoke orphaned credentials: %v", retried.Diagnostics)
	}
}

func TestConnectionProjectionRejectsDuplicatePathsBeforeCreateOrUpdate(t *testing.T) {
	ctx := context.Background()
	f, c := newMCPFixture(t)
	r := &PaperclipConnectionResource{client: c}
	var s resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &s)
	model := legacyConnectionModel(t)
	model.CredentialRefs = types.StringValue(`[{"name":"api","secretId":"secret-1","placement":"header","key":"Authorization"},{"name":"credentials.api","secretId":"secret-1","placement":"header","key":"Other"}]`)
	plan := tfsdk.Plan{Schema: s.Schema}
	if d := plan.Set(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	state := tfsdk.State{Schema: s.Schema}
	if d := state.Set(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	created := resource.CreateResponse{State: state}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
	updated := resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &updated)
	if !created.Diagnostics.HasError() || !updated.Diagnostics.HasError() || len(f.mutations) != 0 {
		t.Fatal("duplicate native credential projection paths reached a write")
	}
}

func TestConnectionProjectionReportsGrantReconciliationFailure(t *testing.T) {
	for _, operation := range []string{"create", "update", "owner conflict"} {
		t.Run(operation, func(t *testing.T) {
			ctx := context.Background()
			f, c := newMCPFixture(t)
			r := &PaperclipConnectionResource{client: c}
			var s resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &s)
			model := legacyConnectionModel(t)
			plan := tfsdk.Plan{Schema: s.Schema}
			if d := plan.Set(ctx, &model); d.HasError() {
				t.Fatal(d)
			}
			state := tfsdk.State{Schema: s.Schema}
			if d := state.Set(ctx, &model); d.HasError() {
				t.Fatal(d)
			}
			if operation == "owner conflict" {
				if err := c.ClaimConnectionAggregate("grants", "conn-1", "other-owner"); err != nil {
					t.Fatal(err)
				}
			} else {
				f.fail = "GET /api/tool-connections/conn-1/grants"
			}
			if operation == "create" || operation == "owner conflict" {
				response := resource.CreateResponse{State: tfsdk.State{Schema: s.Schema}}
				r.Create(ctx, resource.CreateRequest{Plan: plan}, &response)
				if !response.Diagnostics.HasError() {
					t.Fatal("connection create hid grant failure")
				}
			}
			if operation != "create" {
				response := resource.UpdateResponse{State: state}
				r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &response)
				if !response.Diagnostics.HasError() {
					t.Fatal("connection update hid grant failure")
				}
			}
		})
	}
}

func TestInstallStateConversionAndNativeWriteErrorsAreReported(t *testing.T) {
	for _, kind := range []string{"unknown agent", "native put", "missing owner state", "prior unknown agents", "owner conflict"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			f, c := newMCPFixture(t)
			r := &PaperclipConnectionInstallResource{client: c}
			var s resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &s)
			list, _ := types.ListValueFrom(ctx, types.StringType, []string{"agent-1"})
			if kind == "unknown agent" || kind == "prior unknown agents" {
				list = types.ListValueMust(types.StringType, []attr.Value{types.StringUnknown()})
			}
			model := PaperclipConnectionInstallResourceModel{ID: types.StringValue("conn-1"), ConnectionID: types.StringValue("conn-1"), OwnerID: types.StringValue("owner-1"), AgentIDs: list, TargetType: types.StringValue("agent")}
			plan := tfsdk.Plan{Schema: s.Schema}
			if d := plan.Set(ctx, &model); d.HasError() {
				t.Fatal(d)
			}
			state := tfsdk.State{Schema: s.Schema, Raw: plan.Raw}
			if kind == "owner conflict" {
				if err := c.ClaimConnectionAggregate("installs", "conn-1", "other-owner"); err != nil {
					t.Fatal(err)
				}
			}
			if kind == "native put" {
				f.fail = "PUT /api/tool-connections/conn-1/installs"
			}
			if kind == "missing owner state" {
				model.OwnerID = types.StringNull()
				if d := plan.Set(ctx, &model); d.HasError() {
					t.Fatal(d)
				}
				state = tfsdk.State{Schema: s.Schema, Raw: tftypes.NewValue(tftypes.String, "invalid-state")}
			}
			if kind == "prior unknown agents" {
				response := resource.ReadResponse{State: state}
				r.Read(ctx, resource.ReadRequest{State: state}, &response)
				if !response.Diagnostics.HasError() {
					t.Fatal("invalid prior install state accepted")
				}
				return
			}
			if kind != "missing owner state" {
				response := resource.CreateResponse{State: state}
				r.Create(ctx, resource.CreateRequest{Plan: plan}, &response)
				if !response.Diagnostics.HasError() {
					t.Fatal("invalid create or failed PUT accepted")
				}
			}
			response := resource.UpdateResponse{State: state}
			r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &response)
			if !response.Diagnostics.HasError() {
				t.Fatal("invalid update or failed PUT accepted")
			}
		})
	}
}

func TestApplicationMetadataRejectsNonObjectsWithoutEchoingInput(t *testing.T) {
	ctx := context.Background()
	v := applicationMetadataValidator{}
	for _, value := range []types.String{types.StringNull(), types.StringUnknown(), types.StringValue(`{}`), types.StringValue(`{"tags":["mcp"]}`), types.StringValue(`null`), types.StringValue(`[]`), types.StringValue(`not-a-secret-value`)} {
		response := validator.StringResponse{}
		v.ValidateString(ctx, validator.StringRequest{ConfigValue: value}, &response)
		invalid := value.ValueString() == "null" || value.ValueString() == "[]" || value.ValueString() == "not-a-secret-value"
		if response.Diagnostics.HasError() != invalid {
			t.Fatalf("unexpected metadata validation: %v", response.Diagnostics)
		}
		for _, diagnostic := range response.Diagnostics {
			if strings.Contains(diagnostic.Detail(), "not-a-secret-value") {
				t.Fatal("metadata error echoed its input")
			}
		}
	}
}

func TestGrantUpdateRetainsPendingStateWhenDelegationRevocationFails(t *testing.T) {
	ctx := context.Background()
	f, c := newMCPFixture(t)
	r := &PaperclipConnectionGrantsResource{client: c}
	s, state := createGrantState(t, r)
	var model connectionGrantsModel
	if d := state.Get(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	model.Delegations = types.MapNull(types.SetType{ElemType: types.StringType})
	f.fail = "DELETE /api/tool-connections/conn-1/grants/personal-1/delegations/delegation-1"
	response := resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{Plan: grantPlan(t, s, model), State: state}, &response)
	if !response.Diagnostics.HasError() || len(f.grants[1]["delegations"].([]interface{})) != 1 {
		t.Fatal("failed delegation revocation was hidden")
	}
	var ready types.Bool
	if d := response.State.GetAttribute(ctx, path.Root("ready"), &ready); d.HasError() {
		t.Fatal(d)
	}
	if ready.ValueBool() {
		t.Fatal("incomplete revocation reported convergence")
	}
}

func TestNativeRequiredConnectionConfigurationAndCredentialPlacement(t *testing.T) {
	for _, tc := range []struct {
		name, transport, config string
		valid                   bool
	}{
		{"remote url", "mcp_remote", `{"url":"https://example.test/mcp"}`, true},
		{"remote endpoint", "mcp_remote", `{"endpoint":"https://example.test/mcp"}`, true},
		{"remote alias", "mcp_remote", `{"remoteUrl":"http://example.test/mcp"}`, true},
		{"missing endpoint", "mcp_remote", `{}`, false},
		{"bad endpoint", "mcp_remote", `{"url":"%broken"}`, false},
		{"native precedence", "mcp_remote", `{"url":"","endpoint":"https://example.test"}`, false},
		{"scheme", "mcp_remote", `{"url":"file:///secret"}`, false},
		{"userinfo", "mcp_remote", `{"url":"https://user:never-print-this@example.test"}`, false},
		{"query credential", "mcp_remote", `{"url":"https://example.test?api_key=never-print-this"}`, false},
		{"stdio template", "local_stdio", `{"templateId":"approved-template"}`, true},
		{"missing template", "local_stdio", `{}`, false},
		{"rest clearing", "rest_api", `{}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := PaperclipConnectionResourceModel{Transport: types.StringValue(tc.transport), Config: types.StringValue(tc.config)}
			err := validateRequiredConnectionConfig(plan)
			if (err == nil) != tc.valid {
				t.Fatalf("unexpected native configuration result: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "never-print-this") {
				t.Fatal("credential appeared in configuration error")
			}
		})
	}
	for _, plan := range []PaperclipConnectionResourceModel{{Config: types.StringUnknown()}, {Transport: types.StringUnknown()}, {GalleryKey: types.StringValue("native-gallery")}} {
		if err := validateRequiredConnectionConfig(plan); err != nil {
			t.Fatal("unknown or gallery-generated configuration must defer", err)
		}
	}
}

func TestArchiveIsAnExplicitRetirementOperation(t *testing.T) {
	ctx := context.Background()
	_, c := newMCPFixture(t)
	v := nativeEnumValidator{values: []string{"active", "disabled", "draft"}, archiveViaDestroy: true}
	response := validator.StringResponse{}
	v.ValidateString(ctx, validator.StringRequest{ConfigValue: types.StringValue("archived")}, &response)
	if !response.Diagnostics.HasError() || !strings.Contains(response.Diagnostics.Errors()[0].Detail(), "resource destruction") {
		t.Fatal("archive lacked actionable native retirement guidance")
	}
	app := &PaperclipAppResource{client: c}
	model := PaperclipAppResourceModel{Status: types.StringValue("archived")}
	if _, err := app.createAppState(ctx, model); err == nil {
		t.Fatal("archived application creation reported convergence")
	}
	if _, err := app.updateAppState(ctx, model, model); err == nil {
		t.Fatal("archived application update reported convergence")
	}
	connection := &PaperclipConnectionResource{client: c}
	if d := connection.validateConnection(ctx, PaperclipConnectionResourceModel{Status: types.StringValue("archived"), CredentialSecretRefs: types.MapNull(secretRefType())}, PaperclipConnectionResourceModel{}); !d.HasError() {
		t.Fatal("archived connection creation reported convergence")
	}
}

func TestSupportedRestConfigurationCanBeCleared(t *testing.T) {
	ctx := context.Background()
	f, c := newMCPFixture(t)
	r := &PaperclipConnectionResource{client: c}
	var s resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &s)
	model := legacyConnectionModel(t)
	model.Transport = types.StringValue("rest_api")
	model.CredentialRefs = types.StringNull()
	model.Config = types.StringNull()
	f.connection.Transport = "rest_api"
	state := tfsdk.State{Schema: s.Schema}
	if d := state.Set(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	plan := tfsdk.Plan{Schema: s.Schema, Raw: state.Raw}
	response := resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &response)
	if response.Diagnostics.HasError() || len(f.connection.Config) != 0 {
		t.Fatalf("supported native config clearing failed: %v", response.Diagnostics)
	}
}

func TestLocalStdioActivationUsesNativeTrustPolicyInsteadOfBlanketRejection(t *testing.T) {
	_, c := newMCPFixture(t)
	r := &PaperclipConnectionResource{client: c}
	model := PaperclipConnectionResourceModel{CompanyID: types.StringValue("company-1"), Transport: types.StringValue("local_stdio"), Config: types.StringValue(`{"templateId":"approved-template"}`), Enabled: types.BoolValue(true), CredentialSecretRefs: types.MapNull(secretRefType())}
	if d := r.validateConnection(context.Background(), model, PaperclipConnectionResourceModel{}); d.HasError() {
		t.Fatal("provider rejected a supported native activation before target trust policy could evaluate it", d)
	}
}
