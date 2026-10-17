package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestApplicationUnsupportedClearingDoesNotWrite(t *testing.T) {
	writes := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			writes++
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"app-1","companyId":"company-1","name":"app","type":"mcp_http","description":"retained"}`)
	}))
	defer srv.Close()
	r, _ := newConfiguredPaperclipAppResource(t, srv.URL)
	state := PaperclipAppResourceModel{ID: types.StringValue("app-1"), CompanyID: types.StringValue("company-1"), Name: types.StringValue("app"), Type: types.StringValue("mcp_http"), Description: types.StringValue("retained")}
	plan := state
	plan.Description = types.StringNull()
	_, err := r.updateAppState(context.Background(), plan, state)
	if err == nil || !strings.Contains(err.Error(), "cannot clear description") || writes != 0 {
		t.Fatalf("expected actionable clearing error without mutation; error=%v writes=%d", err, writes)
	}
}

type mcpFixture struct {
	t               *testing.T
	connection      client.PaperclipToolConnection
	grants          []map[string]interface{}
	mutations       []string
	fail            string
	failAt          int
	failSeen        int
	failStatus      int
	personalOwner   string
	agentCompany    string
	secretCompany   string
	memberStatus    string
	malformedGrants bool
	server          *httptest.Server
	installs        []map[string]interface{}
}

func newMCPFixture(t *testing.T) (*mcpFixture, *client.Client) {
	t.Helper()
	enabled := false
	f := &mcpFixture{t: t, personalOwner: "user-1", connection: client.PaperclipToolConnection{ID: "conn-1", CompanyID: "company-1", AppID: "app-1", Name: "connection", Transport: "mcp_remote", AuthKind: "api_key", Config: map[string]interface{}{}, TransportConfig: map[string]interface{}{}, CredentialSecretRefs: []map[string]interface{}{}, ConnectionPurpose: "tool", ConnectionKind: "managed", Ownership: "customer", CredentialSource: "paperclip_vault", CredentialPolicy: "shared", Status: "draft", Enabled: &enabled}, grants: []map[string]interface{}{
		{"id": "grant-0", "kind": "organization", "status": "active", "isDefault": true, "credentialSecretRefs": []map[string]interface{}{}, "members": []interface{}{}, "delegations": []interface{}{}},
		{"id": "personal-1", "kind": "user", "subjectUserId": "user-1", "status": "active", "isDefault": false, "credentialSecretRefs": []map[string]interface{}{}, "members": []interface{}{}, "delegations": []interface{}{}},
	}}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	c, err := client.NewClient(f.server.URL, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	return f, c
}

func (f *mcpFixture) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	key := r.Method + " " + r.URL.Path
	if key == f.fail {
		f.failSeen++
		if f.failAt == 0 || f.failAt == f.failSeen {
			status := f.failStatus
			if status == 0 {
				status = 500
			}
			w.WriteHeader(status)
			if status == 404 {
				fmt.Fprint(w, `{"error":"Not found"}`)
			} else {
				fmt.Fprint(w, `{"error":"injected boundary failure"}`)
			}
			return
		}
	}
	if r.Method != "GET" {
		f.mutations = append(f.mutations, key)
	}
	var body map[string]interface{}
	if r.Body != nil {
		json.NewDecoder(r.Body).Decode(&body)
	}
	respond := func(value interface{}) {
		if err := json.NewEncoder(w).Encode(value); err != nil {
			f.t.Error(err)
		}
	}
	switch key {
	case "GET /api/companies/company-1/tools/applications":
		respond(map[string]interface{}{"applications": []map[string]interface{}{{"id": "app-1", "companyId": "company-1", "type": "mcp_http"}}})
	case "GET /api/companies/company-1/tools/stdio-templates":
		respond(map[string]interface{}{"templates": []map[string]interface{}{{"id": "template-1", "templateId": "approved-template", "companyId": "company-1", "status": "active"}}})
	case "GET /api/companies/company-1/secrets":
		company := f.secretCompany
		if company == "" {
			company = "company-1"
		}
		respond([]map[string]interface{}{{"id": "secret-1", "companyId": company}})
	case "GET /api/agents/agent-1":
		company := f.agentCompany
		if company == "" {
			company = "company-1"
		}
		respond(client.PaperclipAgent{ID: "agent-1", CompanyID: company})
	case "GET /api/plugins/plugin-1":
		respond(map[string]interface{}{"id": "plugin-1"})
	case "GET /api/tool-connections/conn-1/installs":
		respond(map[string]interface{}{"installs": f.installs})
	case "PUT /api/tool-connections/conn-1/installs":
		data, _ := json.Marshal(body["installs"])
		json.Unmarshal(data, &f.installs)
		respond(map[string]interface{}{"installs": f.installs})
	case "GET /api/companies/company-1/members":
		status := f.memberStatus
		if status == "" {
			status = "active"
		}
		respond(map[string]interface{}{"members": []map[string]interface{}{{"id": "membership-1", "principalId": "user-1", "principalType": "user", "status": status, "companyId": "company-1"}}})
	case "GET /api/tool-connections/conn-1":
		respond(f.connection)
	case "POST /api/companies/company-1/tools/connections":
		data, _ := json.Marshal(body)
		json.Unmarshal(data, &f.connection)
		respond(f.connection)
	case "PATCH /api/tool-connections/conn-1":
		for _, forbidden := range []string{"transport", "authKind", "connectionPurpose", "connectionKind", "ownership", "credentialSource"} {
			if _, present := body[forbidden]; present {
				f.t.Errorf("ineffective native update field: %s", forbidden)
			}
		}
		f.connection.Config = nil
		f.connection.TransportConfig = nil
		data, _ := json.Marshal(body)
		json.Unmarshal(data, &f.connection)
		respond(f.connection)
	case "DELETE /api/tool-connections/conn-1":
		f.connection.Status = "archived"
		f.connection.Enabled = new(false)
		respond(f.connection)
	case "GET /api/tool-connections/conn-1/grants":
		if f.malformedGrants {
			respond(map[string]interface{}{})
			return
		}
		respond(map[string]interface{}{"grants": f.grants, "currentUserId": f.personalOwner})
	case "POST /api/tool-connections/conn-1/grants/installations":
		for _, grant := range f.grants {
			if grant["kind"] == "organization" {
				grant["isDefault"] = false
			}
		}
		grant := map[string]interface{}{"id": fmt.Sprintf("grant-%d", len(f.grants)), "kind": "organization", "status": "active", "isDefault": body["isDefault"], "credentialSecretRefs": body["credentialSecretRefs"], "members": []interface{}{}, "delegations": []interface{}{}}
		f.grants = append(f.grants, grant)
		respond(grant)
	default:
		parts := strings.Split(r.URL.Path, "/")
		if len(parts) >= 6 && parts[4] == "grants" {
			for _, grant := range f.grants {
				if grant["id"] != parts[5] {
					continue
				}
				if len(parts) == 6 && r.Method == "DELETE" {
					grant["status"] = "revoked"
					respond(grant)
					return
				}
				if len(parts) == 7 && parts[6] == "members" && r.Method == "PUT" {
					members := []interface{}{}
					for _, id := range body["memberUserIds"].([]interface{}) {
						members = append(members, map[string]interface{}{"subjectId": id})
					}
					grant["members"] = members
					respond(grant)
					return
				}
				if len(parts) == 7 && parts[6] == "delegations" && r.Method == "POST" {
					d := map[string]interface{}{"id": "delegation-1", "agentId": body["agentId"]}
					grant["delegations"] = append(grant["delegations"].([]interface{}), d)
					respond(d)
					return
				}
				if len(parts) == 8 && parts[6] == "delegations" && r.Method == "DELETE" {
					grant["delegations"] = []interface{}{}
					respond(map[string]interface{}{})
					return
				}
			}
		}
		w.WriteHeader(404)
		fmt.Fprint(w, `{"error":"Not found"}`)
	}
}

func protectedRefs(t *testing.T, version string) types.Map {
	t.Helper()
	refs, diags := types.MapValueFrom(context.Background(), secretRefType(), map[string]toolSecretRef{"credentials.api_key": {SecretID: types.StringValue("secret-1"), Version: types.StringValue(version)}})
	if diags.HasError() {
		t.Fatal(diags)
	}
	return refs
}

func TestConnectionProtectedReferenceRoundTripAndClearing(t *testing.T) {
	ctx := context.Background()
	f, c := newMCPFixture(t)
	r := &PaperclipConnectionResource{client: c}
	var s resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &s)
	model := PaperclipConnectionResourceModel{CompanyID: types.StringValue("company-1"), AppID: types.StringValue("app-1"), Name: types.StringValue("connection"), Transport: types.StringValue("mcp_remote"), AuthKind: types.StringValue("api_key"), Config: types.StringValue(`{"url":"https://example.test/mcp"}`), CredentialPolicy: types.StringValue("shared"), CredentialSecretRefs: protectedRefs(t, "3"), TransportConfig: types.StringValue(`{"headers":{"X-Tenant":"example"}}`), Status: types.StringValue("active"), Enabled: types.BoolValue(true)}
	plan := tfsdk.Plan{Schema: s.Schema}
	if d := plan.Set(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	created := resource.CreateResponse{State: tfsdk.State{Schema: s.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
	if created.Diagnostics.HasError() {
		t.Fatal(created.Diagnostics)
	}
	var state PaperclipConnectionResourceModel
	if d := created.State.Get(ctx, &state); d.HasError() {
		t.Fatal(d)
	}
	if state.ID.ValueString() != "conn-1" || !state.CredentialSecretRefs.Equal(model.CredentialSecretRefs) || !state.Enabled.ValueBool() || state.Status.ValueString() != "active" {
		t.Fatalf("native connection did not round-trip protected configuration: %+v", state)
	}
	state.TransportConfig = types.StringNull()
	state.CredentialSecretRefs = types.MapNull(secretRefType())
	state.Enabled = types.BoolValue(false)
	if d := plan.Set(ctx, &state); d.HasError() {
		t.Fatal(d)
	}
	updated := resource.UpdateResponse{State: created.State}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: created.State}, &updated)
	if updated.Diagnostics.HasError() {
		t.Fatal(updated.Diagnostics)
	}
	if len(f.connection.CredentialSecretRefs) != 0 || len(f.connection.TransportConfig) != 0 || f.connection.Config["url"] != "https://example.test/mcp" || *f.connection.Enabled {
		t.Fatal("supported clearing did not reach native connection")
	}
	f.connection.Ownership = "dcr"
	read := resource.ReadResponse{State: updated.State}
	r.Read(ctx, resource.ReadRequest{State: updated.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatal(read.Diagnostics)
	}
	if d := read.State.Get(ctx, &state); d.HasError() {
		t.Fatal(d)
	}
	if state.Ownership.ValueString() != "dcr" {
		t.Fatal("ownership drift was masked")
	}
	destroy := resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: read.State}, &destroy)
	if destroy.Diagnostics.HasError() || f.connection.Status != "archived" {
		t.Fatal("destroy did not archive native connection")
	}
}

func TestConnectionUnsupportedTransitionsDoNotWrite(t *testing.T) {
	for _, field := range []string{"transport", "auth_kind", "connection_purpose", "connection_kind", "ownership", "credential_source"} {
		t.Run(field, func(t *testing.T) {
			f, c := newMCPFixture(t)
			r := &PaperclipConnectionResource{client: c}
			var s resource.SchemaResponse
			r.Schema(context.Background(), resource.SchemaRequest{}, &s)
			state := tfsdk.State{Schema: s.Schema}
			model := PaperclipConnectionResourceModel{ID: types.StringValue("conn-1"), CompanyID: types.StringValue("company-1"), CredentialSecretRefs: types.MapNull(secretRefType()), Transport: types.StringValue("mcp_remote"), AuthKind: types.StringValue("api_key"), ConnectionPurpose: types.StringValue("tool"), ConnectionKind: types.StringValue("managed"), Ownership: types.StringValue("customer"), CredentialSource: types.StringValue("paperclip_vault")}
			model.Config = types.StringValue(`{"url":"https://example.test/mcp"}`)
			if d := state.Set(context.Background(), &model); d.HasError() {
				t.Fatal(d)
			}
			plan := tfsdk.Plan{Schema: s.Schema, Raw: state.Raw}
			if d := plan.SetAttribute(context.Background(), path.Root(field), "changed"); d.HasError() {
				t.Fatal(d)
			}
			response := resource.ModifyPlanResponse{Plan: plan}
			r.ModifyPlan(context.Background(), resource.ModifyPlanRequest{Plan: plan, State: state}, &response)
			if !response.Diagnostics.HasError() || !strings.Contains(response.Diagnostics.Errors()[0].Detail(), "cannot change "+field) || len(f.mutations) != 0 {
				t.Fatalf("unsupported transition did not fail before mutation: %v", response.Diagnostics)
			}
		})
	}
}

func TestTransportConfigurationRejectsSensitiveKeysWithoutEchoingValues(t *testing.T) {
	for _, raw := range []string{`{"headers":{"Authorization":"DO-NOT-RETAIN"}}`, `{"args":[{"client_secret":"DO-NOT-RETAIN"}]}`, `[]`, `null`, `bad-json`} {
		t.Run(raw, func(t *testing.T) {
			err := validateTransportConfig(types.StringValue(raw))
			if err == nil || strings.Contains(err.Error(), "DO-NOT-RETAIN") {
				t.Fatalf("expected sanitized config diagnostic, got %v", err)
			}
		})
	}
	for _, raw := range []types.String{types.StringNull(), types.StringUnknown(), types.StringValue(`{"args":["--json"],"url":"https://example.test"}`)} {
		if err := validateTransportConfig(raw); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNativeEnumValidation(t *testing.T) {
	ctx := context.Background()
	v := nativeEnumValidator{values: []string{"active", "disabled"}}
	if v.Description(ctx) == "" || v.MarkdownDescription(ctx) == "" {
		t.Fatal("native values must be documented")
	}
	for _, value := range []types.String{types.StringNull(), types.StringUnknown(), types.StringValue("active"), types.StringValue("unsupported")} {
		response := validator.StringResponse{}
		v.ValidateString(ctx, validator.StringRequest{ConfigValue: value}, &response)
		if response.Diagnostics.HasError() != (value.ValueString() == "unsupported") {
			t.Fatalf("unexpected validation result for %v", value)
		}
	}
}

func grantPlan(t *testing.T, s resource.SchemaResponse, model connectionGrantsModel) tfsdk.Plan {
	t.Helper()
	p := tfsdk.Plan{Schema: s.Schema}
	if d := p.Set(context.Background(), &model); d.HasError() {
		t.Fatal(d)
	}
	return p
}
func initialGrantModel(t *testing.T) connectionGrantsModel {
	ctx := context.Background()
	members, _ := types.SetValueFrom(ctx, types.StringType, []string{"user-1"})
	delegations, _ := types.MapValueFrom(ctx, types.SetType{ElemType: types.StringType}, map[string][]string{"personal-1": {"agent-1"}})
	return connectionGrantsModel{ConnectionID: types.StringValue("conn-1"), CompanyID: types.StringValue("company-1"), SecretRefs: protectedRefs(t, "1"), MemberUserIDs: members, Delegations: delegations}
}

func TestGrantRotationRevokesSupersededGrantAndReconcilesAudienceAndDelegations(t *testing.T) {
	ctx := context.Background()
	f, c := newMCPFixture(t)
	r := &PaperclipConnectionGrantsResource{client: c}
	var s resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &s)
	model := initialGrantModel(t)
	created := resource.CreateResponse{State: tfsdk.State{Schema: s.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: grantPlan(t, s, model)}, &created)
	if created.Diagnostics.HasError() {
		t.Fatal(created.Diagnostics)
	}
	var state connectionGrantsModel
	if d := created.State.Get(ctx, &state); d.HasError() {
		t.Fatal(d)
	}
	if state.GrantID.ValueString() != "grant-2" || f.grants[0]["status"] != "revoked" || len(f.grants[1]["delegations"].([]interface{})) != 1 {
		t.Fatal("installation/delegation lifecycle did not converge")
	}
	previous := created.State
	state.SecretRefs = protectedRefs(t, "latest")
	state.Delegations = types.MapNull(types.SetType{ElemType: types.StringType})
	state.MemberUserIDs = types.SetNull(types.StringType)
	updated := resource.UpdateResponse{State: previous}
	r.Update(ctx, resource.UpdateRequest{Plan: grantPlan(t, s, state), State: previous}, &updated)
	if updated.Diagnostics.HasError() {
		t.Fatal(updated.Diagnostics)
	}
	if f.grants[2]["status"] != "revoked" || len(f.grants[1]["delegations"].([]interface{})) != 0 {
		t.Fatal("rotation retained superseded credentials or removed delegation")
	}
	read := resource.ReadResponse{State: updated.State}
	r.Read(ctx, resource.ReadRequest{State: updated.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatal(read.Diagnostics)
	}
	var refreshed connectionGrantsModel
	if d := read.State.Get(ctx, &refreshed); d.HasError() {
		t.Fatal(d)
	}
	if refreshed.GrantID.ValueString() != "grant-3" || !refreshed.SecretRefs.Equal(state.SecretRefs) || !refreshed.MemberUserIDs.IsNull() {
		t.Fatal("grant read masked native state")
	}
	destroyed := resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: read.State}, &destroyed)
	if destroyed.Diagnostics.HasError() || f.grants[3]["status"] != "revoked" {
		t.Fatalf("grant destroy did not revoke: %v", destroyed.Diagnostics)
	}
}

func TestGrantPreflightRejectsInvalidReferencesAndOwnersWithoutMutation(t *testing.T) {
	for _, name := range []string{"company", "secret", "personal grant", "personal owner", "agent", "member"} {
		t.Run(name, func(t *testing.T) {
			f, c := newMCPFixture(t)
			r := &PaperclipConnectionGrantsResource{client: c}
			model := initialGrantModel(t)
			switch name {
			case "company":
				model.CompanyID = types.StringValue("other")
			case "secret":
				model.SecretRefs = protectedRefs(t, "0")
			case "personal grant":
				model.Delegations, _ = types.MapValueFrom(context.Background(), types.SetType{ElemType: types.StringType}, map[string][]string{"missing": {"agent-1"}})
			case "personal owner":
				f.personalOwner = "other"
			case "agent":
				f.fail = "GET /api/agents/agent-1"
			case "member":
				f.fail = "GET /api/companies/company-1/members"
			}
			if err := r.reconcile(context.Background(), &model, connectionGrantsModel{}); err == nil || len(f.mutations) != 0 {
				t.Fatalf("preflight did not reject before mutation: %v %v", err, f.mutations)
			}
		})
	}
}

func TestConnectionAggregatesRejectDoubleOwnership(t *testing.T) {
	_, c := newMCPFixture(t)
	for _, kind := range []string{"grants", "installs"} {
		t.Run(kind, func(t *testing.T) {
			if err := c.ClaimConnectionAggregate(kind, "conn-1", "first"); err != nil {
				t.Fatal(err)
			}
			if err := c.ClaimConnectionAggregate(kind, "conn-1", "first"); err != nil {
				t.Fatal(err)
			}
			if err := c.ClaimConnectionAggregate(kind, "conn-1", "second"); err == nil || !strings.Contains(err.Error(), "conflicting owners") {
				t.Fatalf("duplicate aggregate ownership was accepted: %v", err)
			}
		})
	}
}

func TestMCPStateUpgradePreservesRemoteIdentity(t *testing.T) {
	ctx := context.Background()
	for _, r := range []resource.Resource{NewPaperclipAppResource(), NewPaperclipConnectionResource(), NewPaperclipConnectionInstallResource()} {
		var metadata resource.MetadataResponse
		r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "paperclip"}, &metadata)
		t.Run(metadata.TypeName, func(t *testing.T) {
			var current resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &current)
			upgrader := r.(resource.ResourceWithUpgradeState).UpgradeState(ctx)[0]
			values := map[string]tftypes.Value{}
			for key, attribute := range upgrader.PriorSchema.Attributes {
				values[key] = tftypes.NewValue(attribute.GetType().TerraformType(ctx), nil)
			}
			values["id"] = tftypes.NewValue(tftypes.String, "original-id")
			prior := tfsdk.State{Schema: *upgrader.PriorSchema, Raw: tftypes.NewValue(upgrader.PriorSchema.Type().TerraformType(ctx), values)}
			response := resource.UpgradeStateResponse{State: tfsdk.State{Schema: current.Schema}}
			upgrader.StateUpgrader(ctx, resource.UpgradeStateRequest{State: &prior}, &response)
			if response.Diagnostics.HasError() {
				t.Fatal(response.Diagnostics)
			}
			var id types.String
			if d := response.State.GetAttribute(ctx, path.Root("id"), &id); d.HasError() {
				t.Fatal(d)
			}
			if id.ValueString() != "original-id" || current.Schema.Version != 1 {
				t.Fatal("upgrade replaced remote identity")
			}
			invalid := tfsdk.State{Schema: *upgrader.PriorSchema, Raw: tftypes.NewValue(tftypes.String, "invalid")}
			bad := resource.UpgradeStateResponse{State: tfsdk.State{Schema: current.Schema}}
			upgrader.StateUpgrader(ctx, resource.UpgradeStateRequest{State: &invalid}, &bad)
			if !bad.Diagnostics.HasError() {
				t.Fatal("malformed prior state must fail explicitly")
			}
		})
	}
}

func TestApplicationNativeOwnershipStatusAndSupportedEmptyDescription(t *testing.T) {
	ctx := context.Background()
	f, c := newMCPFixture(t)
	app := map[string]interface{}{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if req.Method == "GET" && !strings.Contains(req.URL.Path, "applications") {
			f.serve(w, req)
			return
		}
		if req.Method != "GET" {
			var body map[string]interface{}
			json.NewDecoder(req.Body).Decode(&body)
			for key, value := range body {
				if value != nil {
					app[key] = value
				}
			}
			app["id"] = "app-1"
			app["companyId"] = "company-1"
		}
		if req.Method == "GET" {
			json.NewEncoder(w).Encode(map[string]interface{}{"applications": []interface{}{app}})
		} else {
			json.NewEncoder(w).Encode(app)
		}
	}))
	defer server.Close()
	c.Endpoint = server.URL
	r := &PaperclipAppResource{client: c}
	model := PaperclipAppResourceModel{CompanyID: types.StringValue("company-1"), Name: types.StringValue("app"), Type: types.StringValue("paperclip_plugin"), Description: types.StringValue("description"), PluginID: types.StringValue("plugin-1"), OwnerAgentID: types.StringValue("agent-1"), OwnerUserID: types.StringValue("user-1"), Status: types.StringValue("disabled")}
	created, err := r.createAppState(ctx, model)
	if err != nil {
		t.Fatal(err)
	}
	if created.PluginID.ValueString() != "plugin-1" || created.OwnerAgentID.ValueString() != "agent-1" || created.OwnerUserID.ValueString() != "user-1" || created.Status.ValueString() != "disabled" {
		t.Fatal("native ownership fields were not recorded")
	}
	plan := created
	plan.Description = types.StringValue("")
	plan.Status = types.StringValue("active")
	updated, err := r.updateAppState(ctx, plan, created)
	if err != nil || updated.Description.IsNull() || updated.Description.ValueString() != "" {
		t.Fatalf("supported empty description did not round-trip: %v %+v", err, updated)
	}
	app["ownerUserId"] = "other-user"
	app["status"] = "disabled"
	read, err := r.readAppState(ctx, updated)
	if err != nil || read.OwnerUserID.ValueString() != "other-user" || read.Status.ValueString() != "disabled" {
		t.Fatal("application ownership/status drift was masked")
	}
}

func TestApplicationUnsupportedOwnershipClearingAndIdentityChangesFailAtPlan(t *testing.T) {
	ctx := context.Background()
	_, c := newMCPFixture(t)
	r := &PaperclipAppResource{client: c}
	var s resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &s)
	model := PaperclipAppResourceModel{ID: types.StringValue("app-1"), CompanyID: types.StringValue("company-1"), Name: types.StringValue("app"), Type: types.StringValue("mcp_http"), ApplicationKey: types.StringValue("app-key"), Description: types.StringValue("text"), PluginID: types.StringValue("plugin-1"), OwnerAgentID: types.StringValue("agent-1"), OwnerUserID: types.StringValue("user-1")}
	state := tfsdk.State{Schema: s.Schema}
	if d := state.Set(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	for _, key := range []string{"description", "plugin_id", "owner_agent_id", "owner_user_id", "type", "application_key"} {
		t.Run(key, func(t *testing.T) {
			plan := tfsdk.Plan{Schema: s.Schema, Raw: state.Raw}
			value := types.StringNull()
			if key == "type" || key == "application_key" {
				value = types.StringValue("changed")
			}
			if d := plan.SetAttribute(ctx, path.Root(key), value); d.HasError() {
				t.Fatal(d)
			}
			response := resource.ModifyPlanResponse{Plan: plan}
			r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: state}, &response)
			if !response.Diagnostics.HasError() || response.Diagnostics.Errors()[0].Summary() != "Unsupported application transition" {
				t.Fatalf("unsupported native transition lacked exact diagnostic: %v", response.Diagnostics)
			}
		})
	}
}

func TestProtectedReferenceValidationAndProjectionRoundTrip(t *testing.T) {
	ctx := context.Background()
	f, c := newMCPFixture(t)
	for _, version := range []string{"latest", "2", "0", "not-a-version"} {
		t.Run(version, func(t *testing.T) {
			d := validateToolRefs(ctx, c, "company-1", protectedRefs(t, version))
			if d.HasError() != (version == "0" || version == "not-a-version") {
				t.Fatalf("unexpected version validation: %v", d)
			}
		})
	}
	for _, refs := range []types.Map{types.MapNull(secretRefType()), types.MapUnknown(secretRefType())} {
		if d := validateToolRefs(ctx, c, "company-1", refs); d.HasError() {
			t.Fatal(d)
		}
	}
	malformed, _ := types.MapValue(types.StringType, map[string]attr.Value{"bad": types.StringValue("not-an-object")})
	if d := validateToolRefs(ctx, c, "company-1", malformed); !d.HasError() {
		t.Fatal("malformed reference map accepted")
	}
	empty, _ := types.MapValueFrom(ctx, secretRefType(), map[string]toolSecretRef{"": {SecretID: types.StringValue("secret-1")}})
	if d := validateToolRefs(ctx, c, "company-1", empty); !d.HasError() {
		t.Fatal("empty native configPath accepted")
	}
	ref := toolSecretRef{SecretID: types.StringValue("secret-1"), Version: types.StringNull(), ProjectionClass: types.StringValue("class_3_static_lease"), ProjectionAllowlistKey: types.StringValue("slack.bot_token"), Required: types.BoolValue(true), Label: types.StringValue("Slack bot"), KeyScope: types.StringValue("chat"), ExpiresAt: types.StringValue("2030-01-01T00:00:00Z")}
	value, _ := types.MapValueFrom(ctx, secretRefType(), map[string]toolSecretRef{"credentials.bot_token": ref})
	if d := validateToolRefs(ctx, c, "company-1", value); d.HasError() {
		t.Fatal(d)
	}
	api := toolRefsToAPI(ctx, value)
	if api[0]["projectionClass"] != "class_3_static_lease" || api[0]["required"] != true || api[0]["configPath"] != "credentials.bot_token" {
		t.Fatalf("native reference metadata lost: %v", api)
	}
	state := PaperclipConnectionResourceModel{CredentialSecretRefs: types.MapNull(secretRefType())}
	refreshConnectionFields(ctx, &state, &client.PaperclipToolConnection{CredentialSecretRefs: api})
	if !state.CredentialSecretRefs.Equal(value) {
		t.Fatal("reference projection metadata failed round-trip")
	}
	f.fail = "GET /api/companies/company-1/secrets"
	if d := validateToolRefs(ctx, c, "company-1", protectedRefs(t, "latest")); !d.HasError() {
		t.Fatal("secret read error was ignored")
	}
}

func TestUnsupportedNativeConnectionCreationDoesNotWrite(t *testing.T) {
	for _, name := range []string{"channel", "source", "missing template"} {
		t.Run(name, func(t *testing.T) {
			f, c := newMCPFixture(t)
			r := &PaperclipConnectionResource{client: c}
			plan := PaperclipConnectionResourceModel{CredentialSecretRefs: types.MapNull(secretRefType())}
			switch name {
			case "channel":
				plan.ConnectionPurpose = types.StringValue("channel")
			case "source":
				plan.CredentialSource = types.StringValue("vercel_connect")
			case "missing template":
				plan.Transport = types.StringValue("local_stdio")
			}
			if d := r.validateConnection(context.Background(), plan, PaperclipConnectionResourceModel{}, false); !d.HasError() || len(f.mutations) != 0 {
				t.Fatalf("unsupported native creation was accepted: %v", d)
			}
		})
	}
}

func TestWholeInstallSetRejectsConflictingResourcesBeforeWrite(t *testing.T) {
	ctx := context.Background()
	f, c := newMCPFixture(t)
	r := &PaperclipConnectionInstallResource{client: c}
	var s resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &s)
	agents, _ := types.ListValueFrom(ctx, types.StringType, []string{"agent-1"})
	plan := tfsdk.Plan{Schema: s.Schema}
	model := PaperclipConnectionInstallResourceModel{ConnectionID: types.StringValue("conn-1"), AgentIDs: agents, TargetType: types.StringValue("agent")}
	if d := plan.Set(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	created := resource.CreateResponse{State: tfsdk.State{Schema: s.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
	if created.Diagnostics.HasError() {
		t.Fatal(created.Diagnostics)
	}
	mutations := len(f.mutations)
	duplicate := resource.CreateResponse{State: tfsdk.State{Schema: s.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &duplicate)
	if !duplicate.Diagnostics.HasError() || len(f.mutations) != mutations || len(f.installs) != 1 {
		t.Fatal("second install owner overwrote the whole set")
	}
	conflict := resource.ModifyPlanResponse{Plan: plan}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan}, &conflict)
	if !conflict.Diagnostics.HasError() {
		t.Fatal("plan must detect duplicate install owner")
	}
	ownedPlan := tfsdk.Plan{Schema: s.Schema, Raw: created.State.Raw}
	owned := resource.ModifyPlanResponse{Plan: ownedPlan}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: ownedPlan, State: created.State}, &owned)
	if owned.Diagnostics.HasError() {
		t.Fatal("the same install owner must remain valid", owned.Diagnostics)
	}
}
