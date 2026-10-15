package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func bindingTestModel() toolBindingModel {
	return toolBindingModel{CompanyID: types.StringValue("comp-1"), ProfileID: types.StringValue("owned"), TargetType: types.StringValue("company"), TargetID: types.StringValue("comp-1"), Priority: types.Int64Value(100)}
}
func TestToolNativeReferenceAuthorities(t *testing.T) {
	f := newToolFixture(t)
	r := &PaperclipToolProfileResource{}
	configureTestResource(t, f.server.URL, r)
	ctx := context.Background()
	for _, kind := range []string{"agent", "project", "routine", "issue", "gateway", "application", "connection", "catalog_entry"} {
		t.Run(kind, func(t *testing.T) {
			if err := r.client.AssertToolTarget(ctx, "comp-1", kind, toolTestUUID); err != nil {
				t.Fatal(err)
			}
			if err := r.client.AssertToolTarget(ctx, "comp-1", kind, "missing"); err == nil {
				t.Fatal("missing reference accepted")
			}
		})
	}
	if err := r.client.AssertToolTarget(ctx, "comp-1", "company", "other"); err == nil {
		t.Fatal("cross-company default accepted")
	}
	if err := r.client.AssertToolTarget(ctx, "comp-1", "unknown", toolTestUUID); err == nil {
		t.Fatal("unknown reference type accepted")
	}
	for _, path := range []string{"/api/companies/comp-1/tools/connections", "/api/tool-connections/" + toolTestUUID + "/catalog", "/api/companies/comp-1/tools/gateways", "/api/companies/comp-1"} {
		t.Run(path, func(t *testing.T) {
			f.fail = "GET " + path
			kind := "catalog_entry"
			if strings.Contains(path, "gateways") {
				kind = "gateway"
			}
			id := toolTestUUID
			if strings.HasSuffix(path, "comp-1") {
				kind = "company"
				id = "comp-1"
			}
			if err := r.client.AssertToolTarget(ctx, "comp-1", kind, id); err == nil {
				t.Fatal("unreadable reference accepted")
			}
			f.fail = ""
		})
	}
	// Each shared reference is checked once even when several selectors use it.
	f.requests = nil
	if err := validateToolReferenceObjects(ctx, r.client, "comp-1", map[string]interface{}{"projectId": toolTestUUID, "projectIds": []interface{}{toolTestUUID}}, map[string]interface{}{"context": map[string]interface{}{"projectId": toolTestUUID}}); err != nil {
		t.Fatal(err)
	}
	if len(f.requests) != 1 {
		t.Fatalf("shared reference was recomputed: %v", f.requests)
	}
	for _, selection := range []map[string]interface{}{{"projectIds": false}, {"projectId": true}} {
		if err := validateToolReferenceObjects(ctx, r.client, "comp-1", selection, nil); err == nil {
			t.Fatal("malformed selector identity accepted")
		}
	}
}
func TestToolProfileInventoryFailuresPreserveState(t *testing.T) {
	for _, body := range []string{`{}`, `{"profiles":[{"id":"p","companyId":"wrong","entries":[],"bindings":[]}]}`, `{"profiles":[{"id":"p","companyId":"comp-1","entries":[]}]}`, `{"profiles":[{"id":"p","companyId":"comp-1","entries":[],"bindings":[]},{"id":"p","companyId":"comp-1","entries":[],"bindings":[]}]}`, `not-json`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/api/companies/comp-1/tools/profiles" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				fmt.Fprint(w, body)
			}))
			defer server.Close()
			r := &PaperclipToolProfileResource{}
			s := configureTestResource(t, server.URL, r)
			m := profileTestModel()
			m.ID = types.StringValue("p")
			state := toolState(t, s, &m)
			read := resource.ReadResponse{State: state}
			r.Read(context.Background(), resource.ReadRequest{State: state}, &read)
			if !read.Diagnostics.HasError() || !reflect.DeepEqual(state.Raw, read.State.Raw) {
				t.Fatal("incomplete inventory erased state or succeeded")
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, `{"id":"wrong","companyId":"other"}`) }))
	defer server.Close()
	c, _ := client.NewClient(server.URL, "token")
	if err := c.AssertToolTarget(context.Background(), "comp-1", "project", toolTestUUID); err == nil {
		t.Fatal("contradictory reference identity accepted")
	}
}
func TestToolResourceTransportFailuresRetainOriginalCause(t *testing.T) {
	ctx := context.Background()
	for _, operation := range []string{"read", "update-read", "update-write", "create", "delete-read", "delete-write"} {
		t.Run("profile/"+operation, func(t *testing.T) {
			f := newToolFixture(t)
			r, s, m, state := createToolProfile(t, f)
			switch operation {
			case "read", "update-read", "delete-read":
				f.fail = "GET /api/companies/comp-1/tools/profiles"
			case "create":
				f.fail = "POST /api/companies/comp-1/tools/profiles"
				m.ProfileKey = types.StringValue("new")
				m.OwnerID = types.StringNull()
			case "update-write":
				f.fail = "PATCH /api/tool-profiles/owned"
			case "delete-write":
				f.fail = "DELETE /api/tool-profiles/owned"
			}
			var text string
			switch operation {
			case "read":
				resp := resource.ReadResponse{State: state}
				r.Read(ctx, resource.ReadRequest{State: state}, &resp)
				text = fmt.Sprint(resp.Diagnostics)
				if !reflect.DeepEqual(resp.State.Raw, state.Raw) {
					t.Fatal("read failure erased state")
				}
			case "create":
				resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
				r.Create(ctx, resource.CreateRequest{Plan: toolPlan(t, s, &m)}, &resp)
				text = fmt.Sprint(resp.Diagnostics)
			case "update-read", "update-write":
				resp := resource.UpdateResponse{State: state}
				r.Update(ctx, resource.UpdateRequest{Plan: toolPlan(t, s, &m), State: state}, &resp)
				text = fmt.Sprint(resp.Diagnostics)
			default:
				resp := resource.DeleteResponse{}
				r.Delete(ctx, resource.DeleteRequest{State: state}, &resp)
				text = fmt.Sprint(resp.Diagnostics)
			}
			if !strings.Contains(text, "fixture permission denied") {
				t.Fatalf("original HTTP failure lost: %s", text)
			}
		})
	}
	for _, operation := range []string{"read", "create", "delete"} {
		t.Run("policy/"+operation, func(t *testing.T) {
			f := newToolFixture(t)
			r, s, m, state := createToolPolicy(t, f)
			var text string
			switch operation {
			case "read":
				f.fail = "GET /api/companies/comp-1/tools/policies"
				resp := resource.ReadResponse{State: state}
				r.Read(ctx, resource.ReadRequest{State: state}, &resp)
				text = fmt.Sprint(resp.Diagnostics)
			case "create":
				f.fail = "POST /api/companies/comp-1/tools/policies"
				m.Name = types.StringValue("Other new")
				m.OwnerID = types.StringNull()
				resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
				r.Create(ctx, resource.CreateRequest{Plan: toolPlan(t, s, &m)}, &resp)
				text = fmt.Sprint(resp.Diagnostics)
			case "delete":
				f.fail = "DELETE /api/companies/comp-1/tools/policies/owned"
				resp := resource.DeleteResponse{}
				r.Delete(ctx, resource.DeleteRequest{State: state}, &resp)
				text = fmt.Sprint(resp.Diagnostics)
			}
			if !strings.Contains(text, "fixture permission denied") {
				t.Fatal(text)
			}
		})
	}
	for _, operation := range []string{"read", "create", "delete-read", "delete-write"} {
		t.Run("binding/"+operation, func(t *testing.T) {
			f := newToolFixture(t)
			createToolProfile(t, f)
			r := &PaperclipToolProfileBindingResource{}
			s := configureTestResource(t, f.server.URL, r)
			m := bindingTestModel()
			created := resource.CreateResponse{State: tfsdk.State{Schema: s}}
			r.Create(ctx, resource.CreateRequest{Plan: toolPlan(t, s, &m)}, &created)
			requireToolDiagnostics(t, created.Diagnostics)
			requireToolDiagnostics(t, created.State.Get(ctx, &m))
			var text string
			switch operation {
			case "read", "delete-read":
				f.fail = "GET /api/companies/comp-1/tools/profiles"
				if operation == "read" {
					resp := resource.ReadResponse{State: created.State}
					r.Read(ctx, resource.ReadRequest{State: created.State}, &resp)
					text = fmt.Sprint(resp.Diagnostics)
				} else {
					resp := resource.DeleteResponse{}
					r.Delete(ctx, resource.DeleteRequest{State: created.State}, &resp)
					text = fmt.Sprint(resp.Diagnostics)
				}
			case "create":
				f.profiles["owned"]["bindings"] = []interface{}{}
				f.fail = "POST /api/companies/comp-1/tools/profiles/owned/bind"
				resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
				r.Create(ctx, resource.CreateRequest{Plan: toolPlan(t, s, &m)}, &resp)
				text = fmt.Sprint(resp.Diagnostics)
			case "delete-write":
				f.fail = "POST /api/companies/comp-1/tools/profiles/owned/unbind"
				resp := resource.DeleteResponse{}
				r.Delete(ctx, resource.DeleteRequest{State: created.State}, &resp)
				text = fmt.Sprint(resp.Diagnostics)
			}
			if !strings.Contains(text, "fixture permission denied") {
				t.Fatal(text)
			}
		})
	}
}
func TestToolResourceInvalidWriteIdentitiesAreErrors(t *testing.T) {
	for _, kind := range []string{"profile", "policy", "binding"} {
		t.Run(kind, func(t *testing.T) {
			f := newToolFixture(t)
			if kind == "binding" {
				createToolProfile(t, f)
			}
			f.badIdentity = true
			switch kind {
			case "profile":
				r := &PaperclipToolProfileResource{}
				s := configureTestResource(t, f.server.URL, r)
				m := profileTestModel()
				resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
				r.Create(context.Background(), resource.CreateRequest{Plan: toolPlan(t, s, &m)}, &resp)
				if !resp.Diagnostics.HasError() {
					t.Fatal("invalid profile write accepted")
				}
			case "policy":
				r := &PaperclipPolicyResource{}
				s := configureTestResource(t, f.server.URL, r)
				m := policyTestModel()
				resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
				r.Create(context.Background(), resource.CreateRequest{Plan: toolPlan(t, s, &m)}, &resp)
				if !resp.Diagnostics.HasError() {
					t.Fatal("invalid policy write accepted")
				}
			case "binding":
				r := &PaperclipToolProfileBindingResource{}
				s := configureTestResource(t, f.server.URL, r)
				m := bindingTestModel()
				resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
				r.Create(context.Background(), resource.CreateRequest{Plan: toolPlan(t, s, &m)}, &resp)
				if !resp.Diagnostics.HasError() {
					t.Fatal("invalid binding write accepted")
				}
			}
		})
	}
}
func TestToolPolicyStateUpgradeKeepsNativeIdentity(t *testing.T) {
	ctx := context.Background()
	r := &PaperclipPolicyResource{}
	upgrade := r.UpgradeState(ctx)[0]
	old := tfsdk.State{Schema: *upgrade.PriorSchema, Raw: tftypes.NewValue(upgrade.PriorSchema.Type().TerraformType(ctx), map[string]tftypes.Value{"id": tftypes.NewValue(tftypes.String, "owned"), "company_id": tftypes.NewValue(tftypes.String, "comp-1"), "name": tftypes.NewValue(tftypes.String, "Owned"), "policy_type": tftypes.NewValue(tftypes.String, "allow"), "rules": tftypes.NewValue(tftypes.String, `{"ignored":true}`), "metadata": tftypes.NewValue(tftypes.String, nil)})}
	var schemaResponse resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResponse)
	resp := resource.UpgradeStateResponse{State: tfsdk.State{Schema: schemaResponse.Schema}}
	upgrade.StateUpgrader(ctx, resource.UpgradeStateRequest{State: &old}, &resp)
	requireToolDiagnostics(t, resp.Diagnostics)
	var m PaperclipPolicyResourceModel
	requireToolDiagnostics(t, resp.State.Get(ctx, &m))
	if m.ID.ValueString() != "owned" || m.OwnerID.ValueString() == "" || !m.Conditions.IsNull() {
		t.Fatal("upgrade replaced identity or fabricated conditions")
	}
	old.Raw = tftypes.NewValue(tftypes.String, "bad")
	bad := resource.UpgradeStateResponse{}
	upgrade.StateUpgrader(ctx, resource.UpgradeStateRequest{State: &old}, &bad)
	if !bad.Diagnostics.HasError() {
		t.Fatal("malformed old state accepted")
	}
}
