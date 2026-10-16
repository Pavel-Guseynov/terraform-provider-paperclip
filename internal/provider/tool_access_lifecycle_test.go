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

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const toolTestUUID = "11111111-1111-4111-8111-111111111111"

type toolAPIFixture struct {
	t           *testing.T
	server      *httptest.Server
	profiles    map[string]map[string]interface{}
	policies    map[string]map[string]interface{}
	requests    []string
	writes      []map[string]interface{}
	fail        string
	badIdentity bool
}

func newToolFixture(t *testing.T) *toolAPIFixture {
	f := &toolAPIFixture{t: t, profiles: map[string]map[string]interface{}{}, policies: map[string]map[string]interface{}{}}
	f.profiles["other"] = map[string]interface{}{"id": "other", "companyId": "comp-1", "profileKey": "other-owner", "name": "Other", "status": "active", "defaultAction": "deny", "metadata": map[string]interface{}{}, "entries": []interface{}{}, "bindings": []interface{}{}}
	f.profiles["app"] = map[string]interface{}{"id": "app", "companyId": "comp-1", "profileKey": "app:connection", "name": "Finish", "status": "active", "defaultAction": "deny", "metadata": map[string]interface{}{"source": "app_gallery_finish"}, "entries": []interface{}{}, "bindings": []interface{}{}}
	f.policies["other"] = map[string]interface{}{"id": "other", "companyId": "comp-1", "name": "Other policy", "policyType": "block", "priority": 9, "enabled": true, "selectors": map[string]interface{}{}}
	f.policies["app"] = map[string]interface{}{"id": "app", "companyId": "comp-1", "name": "Finish policy", "policyType": "require_approval", "priority": 20, "enabled": true, "selectors": map[string]interface{}{}, "config": map[string]interface{}{"source": "app_gallery_finish"}}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}
func (f *toolAPIFixture) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	p := r.URL.Path
	request := r.Method + " " + p
	f.requests = append(f.requests, request)
	if f.fail == request {
		w.WriteHeader(403)
		fmt.Fprint(w, `{"error":"fixture permission denied"}`)
		return
	}
	body := map[string]interface{}{}
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	if r.Method != "GET" {
		f.writes = append(f.writes, body)
	}
	if r.Method == "GET" && p == "/api/companies/comp-1/tools/profiles" {
		items := []interface{}{}
		for _, v := range f.profiles {
			items = append(items, v)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"profiles": items})
		return
	}
	if r.Method == "GET" && p == "/api/companies/comp-1/tools/policies" {
		items := []interface{}{}
		for _, v := range f.policies {
			items = append(items, v)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"policies": items})
		return
	}
	if r.Method == "POST" && p == "/api/companies/comp-1/tools/profiles" {
		body["id"] = "owned"
		body["companyId"] = "comp-1"
		body["bindings"] = []interface{}{}
		if body["metadata"] == nil {
			body["metadata"] = map[string]interface{}{}
		}
		f.profiles["owned"] = body
		if f.badIdentity {
			body["id"] = ""
		}
		_ = json.NewEncoder(w).Encode(body)
		return
	}
	if r.Method == "POST" && p == "/api/companies/comp-1/tools/policies" {
		body["id"] = "owned"
		body["companyId"] = "comp-1"
		f.policies["owned"] = body
		if f.badIdentity {
			body["id"] = ""
		}
		_ = json.NewEncoder(w).Encode(body)
		return
	}
	if strings.HasPrefix(p, "/api/tool-profiles/") {
		id := strings.TrimPrefix(p, "/api/tool-profiles/")
		profile := f.profiles[id]
		if profile == nil {
			http.NotFound(w, r)
			return
		}
		if r.Method == "PATCH" {
			for key, v := range body {
				if (key == "description" || key == "metadata") && v == nil {
					continue
				}
				profile[key] = v
			}
			if f.badIdentity {
				profile["id"] = "wrong"
			}
			_ = json.NewEncoder(w).Encode(profile)
			return
		}
		if r.Method == "DELETE" {
			delete(f.profiles, id)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"profile": profile})
			return
		}
	}
	if strings.HasPrefix(p, "/api/companies/comp-1/tools/policies/") {
		id := strings.TrimPrefix(p, "/api/companies/comp-1/tools/policies/")
		policy := f.policies[id]
		if policy == nil {
			http.NotFound(w, r)
			return
		}
		if r.Method == "PATCH" {
			for key, v := range body {
				policy[key] = v
			}
			_ = json.NewEncoder(w).Encode(policy)
			return
		}
		if r.Method == "DELETE" {
			delete(f.policies, id)
			fmt.Fprint(w, `{}`)
			return
		}
	}
	if strings.HasPrefix(p, "/api/companies/comp-1/tools/profiles/") {
		parts := strings.Split(strings.TrimPrefix(p, "/api/companies/comp-1/tools/profiles/"), "/")
		profile := f.profiles[parts[0]]
		if profile == nil {
			http.NotFound(w, r)
			return
		}
		bindings := profile["bindings"].([]interface{})
		if parts[1] == "bind" {
			for _, item := range bindings {
				b := item.(map[string]interface{})
				if b["targetType"] == body["targetType"] && b["targetId"] == body["targetId"] {
					w.WriteHeader(409)
					fmt.Fprint(w, `{"error":"duplicate binding"}`)
					return
				}
			}
			body["id"] = "binding"
			body["companyId"] = "comp-1"
			body["profileId"] = parts[0]
			if body["metadata"] == nil {
				body["metadata"] = map[string]interface{}{}
			}
			profile["bindings"] = append(bindings, body)
			if f.badIdentity {
				body["id"] = ""
			}
			_ = json.NewEncoder(w).Encode(body)
			return
		}
		if parts[1] == "unbind" {
			kept := []interface{}{}
			for _, item := range bindings {
				b := item.(map[string]interface{})
				if b["targetType"] != body["targetType"] || b["targetId"] != body["targetId"] {
					kept = append(kept, b)
				}
			}
			profile["bindings"] = kept
			fmt.Fprint(w, `{"unbound":1}`)
			return
		}
	}
	if r.Method == "GET" {
		if p == "/api/companies/comp-1/tools/applications" {
			fmt.Fprintf(w, `{"applications":[{"id":%q,"companyId":"comp-1","name":"App","type":"mcp_http"}]}`, toolTestUUID)
			return
		}
		if p == "/api/companies/comp-1/tools/connections" {
			fmt.Fprintf(w, `{"connections":[{"id":%q,"companyId":"comp-1"}]}`, toolTestUUID)
			return
		}
		if p == "/api/companies/comp-1/tools/gateways" {
			fmt.Fprintf(w, `{"gateways":[{"id":%q,"companyId":"comp-1"}]}`, toolTestUUID)
			return
		}
		if p == "/api/tool-connections/"+toolTestUUID+"/catalog" {
			fmt.Fprintf(w, `{"catalog":[{"id":%q,"companyId":"comp-1"}]}`, toolTestUUID)
			return
		}
		parts := strings.Split(p, "/")
		id := parts[len(parts)-1]
		if id == toolTestUUID || id == "comp-1" {
			fmt.Fprintf(w, `{"id":%q,"companyId":"comp-1"}`, id)
			return
		}
	}
	http.NotFound(w, r)
}
func toolPlan(t *testing.T, s schema.Schema, m interface{}) tfsdk.Plan {
	t.Helper()
	p := tfsdk.Plan{Schema: s}
	requireToolDiagnostics(t, p.Set(context.Background(), m))
	return p
}
func toolState(t *testing.T, s schema.Schema, m interface{}) tfsdk.State {
	t.Helper()
	p := tfsdk.State{Schema: s}
	requireToolDiagnostics(t, p.Set(context.Background(), m))
	return p
}
func requireToolDiagnostics(t *testing.T, d diag.Diagnostics) {
	t.Helper()
	if d.HasError() {
		t.Fatal(d)
	}
}
func profileTestModel() toolProfileModel {
	return toolProfileModel{CompanyID: types.StringValue("comp-1"), ProfileKey: types.StringValue("owned"), Name: types.StringValue("Owned"), Status: types.StringValue("active"), DefaultAction: types.StringValue("deny"), Metadata: types.StringValue(`{}`), Entries: types.StringValue(`[{"selectorType":"tool_name","toolName":"read"}]`), ForceDestroy: types.BoolValue(false)}
}
func createToolProfile(t *testing.T, f *toolAPIFixture) (*PaperclipToolProfileResource, schema.Schema, toolProfileModel, tfsdk.State) {
	t.Helper()
	r := &PaperclipToolProfileResource{}
	s := configureTestResource(t, f.server.URL, r)
	m := profileTestModel()
	resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(context.Background(), resource.CreateRequest{Plan: toolPlan(t, s, &m)}, &resp)
	requireToolDiagnostics(t, resp.Diagnostics)
	requireToolDiagnostics(t, resp.State.Get(context.Background(), &m))
	return r, s, m, resp.State
}
func policyTestModel() PaperclipPolicyResourceModel {
	return PaperclipPolicyResourceModel{CompanyID: types.StringValue("comp-1"), Name: types.StringValue("Owned policy"), PolicyType: types.StringValue("rate_limit"), Priority: types.Int64Value(0), Enabled: types.BoolValue(false), Description: types.StringValue(""), Selectors: types.StringValue(`{"projectId":"` + toolTestUUID + `"}`), Conditions: types.StringValue(`{"risk":{"isWrite":true}}`), Config: types.StringValue(`{"limit":2,"windowSeconds":60}`)}
}
func createToolPolicy(t *testing.T, f *toolAPIFixture) (*PaperclipPolicyResource, schema.Schema, PaperclipPolicyResourceModel, tfsdk.State) {
	t.Helper()
	r := &PaperclipPolicyResource{}
	s := configureTestResource(t, f.server.URL, r)
	m := policyTestModel()
	resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(context.Background(), resource.CreateRequest{Plan: toolPlan(t, s, &m)}, &resp)
	requireToolDiagnostics(t, resp.Diagnostics)
	requireToolDiagnostics(t, resp.State.Get(context.Background(), &m))
	return r, s, m, resp.State
}

func TestToolProfileEntriesReplaceOnlyOwnedSet(t *testing.T) {
	f := newToolFixture(t)
	others := marshalJSON([]interface{}{f.profiles["other"], f.profiles["app"]})
	r, s, m, state := createToolProfile(t, f)
	if m.ID.ValueString() != "owned" || m.OwnerID.ValueString() == "" {
		t.Fatal("creation lost its native identity/owner")
	}
	m.Entries = types.StringValue(`[{"selectorType":"risk_level","riskLevel":"low","effect":"exclude"}]`)
	resp := resource.UpdateResponse{State: state}
	r.Update(context.Background(), resource.UpdateRequest{Plan: toolPlan(t, s, &m), State: state}, &resp)
	requireToolDiagnostics(t, resp.Diagnostics)
	entries := f.profiles["owned"]["entries"].([]interface{})
	if len(entries) != 1 || entries[0].(map[string]interface{})["selectorType"] != "risk_level" {
		t.Fatalf("entries were merged: %v", entries)
	}
	if marshalJSON([]interface{}{f.profiles["other"], f.profiles["app"]}) != others {
		t.Fatal("other owners changed")
	}
	m.Entries = types.StringValue(`[]`)
	resp2 := resource.UpdateResponse{State: resp.State}
	r.Update(context.Background(), resource.UpdateRequest{Plan: toolPlan(t, s, &m), State: resp.State}, &resp2)
	requireToolDiagnostics(t, resp2.Diagnostics)
	if len(f.profiles["owned"]["entries"].([]interface{})) != 0 {
		t.Fatal("undeclared entries survived")
	}
}
func TestToolProfileRefreshPreservesEntrySetAndDetectsDrift(t *testing.T) {
	f := newToolFixture(t)
	r, _, m, state := createToolProfile(t, f)
	prior := m.Entries
	f.profiles["owned"]["entries"] = []interface{}{map[string]interface{}{"id": "native-entry", "selectorType": "tool_name", "toolName": "read", "effect": "include", "applicationId": nil}}
	read := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &read)
	requireToolDiagnostics(t, read.Diagnostics)
	requireToolDiagnostics(t, read.State.Get(context.Background(), &m))
	if !m.Entries.Equal(prior) {
		t.Fatal("generated entry IDs/defaults caused drift")
	}
	f.profiles["owned"]["entries"] = []interface{}{}
	r.Read(context.Background(), resource.ReadRequest{State: read.State}, &read)
	requireToolDiagnostics(t, read.Diagnostics)
	requireToolDiagnostics(t, read.State.Get(context.Background(), &m))
	if m.Entries.ValueString() != "[]" {
		t.Fatal("remote entry deletion was masked")
	}
}
func TestToolProfileNullClearingFailsBeforeMutation(t *testing.T) {
	f := newToolFixture(t)
	r, s, m, state := createToolProfile(t, f)
	for _, field := range []string{"description", "metadata"} {
		t.Run(field, func(t *testing.T) {
			prior := m
			if field == "description" {
				f.profiles["owned"]["description"] = "retained"
				prior.Description = types.StringValue("retained")
				m.Description = types.StringNull()
			} else {
				m.Metadata = types.StringNull()
			}
			writes := len(f.writes)
			resp := resource.UpdateResponse{State: state}
			r.Update(context.Background(), resource.UpdateRequest{Plan: toolPlan(t, s, &m), State: state}, &resp)
			if !resp.Diagnostics.HasError() || len(f.writes) != writes {
				t.Fatal("unsupported null clearing wrote or succeeded")
			}
			plan := resource.ModifyPlanResponse{Plan: toolPlan(t, s, &m)}
			r.ModifyPlan(context.Background(), resource.ModifyPlanRequest{Plan: plan.Plan, State: toolState(t, s, &prior)}, &plan)
			if !plan.Diagnostics.HasError() {
				t.Fatal("known unsupported transition did not fail at plan")
			}
			m = prior
			f.profiles["owned"]["description"] = nil
			m.Description = types.StringNull()
		})
	}
}
func TestToolProfileDefaultDeleteRequiresExplicitDeclaration(t *testing.T) {
	for _, mode := range []string{"blocked", "force", "reassign", "same", "inactive", "missing", "app"} {
		t.Run(mode, func(t *testing.T) {
			f := newToolFixture(t)
			r, s, m, _ := createToolProfile(t, f)
			f.profiles["owned"]["summary"] = map[string]interface{}{"isCompanyDefault": true}
			switch mode {
			case "force":
				m.ForceDestroy = types.BoolValue(true)
			case "reassign":
				m.ReassignToProfileID = types.StringValue("other")
			case "same":
				m.ReassignToProfileID = m.ID
			case "inactive":
				f.profiles["other"]["status"] = "disabled"
				m.ReassignToProfileID = types.StringValue("other")
			case "missing", "app":
				m.ReassignToProfileID = types.StringValue(mode)
			}
			writes := len(f.writes)
			resp := resource.DeleteResponse{}
			r.Delete(context.Background(), resource.DeleteRequest{State: toolState(t, s, &m)}, &resp)
			success := mode == "force" || mode == "reassign"
			if resp.Diagnostics.HasError() == success {
				t.Fatal(resp.Diagnostics)
			}
			if !success && len(f.writes) != writes {
				t.Fatal("blocked delete mutated")
			}
			if success && f.profiles["owned"] != nil {
				t.Fatal("delete retained owned profile")
			}
		})
	}
}
func TestToolPolicyUpdatesPriorityWithoutGlobalReorder(t *testing.T) {
	f := newToolFixture(t)
	others := marshalJSON([]interface{}{f.policies["other"], f.policies["app"]})
	r, s, m, state := createToolPolicy(t, f)
	if m.Priority.ValueInt64() != 0 || m.Enabled.ValueBool() || m.Description.IsNull() {
		t.Fatal("zero priority, disabled flag or empty description lost")
	}
	m.Priority = types.Int64Value(10000)
	m.Enabled = types.BoolValue(true)
	m.Description = types.StringNull()
	m.Conditions = types.StringNull()
	m.Selectors = types.StringNull()
	m.Config = types.StringNull()
	m.PolicyType = types.StringValue("block")
	resp := resource.UpdateResponse{State: state}
	r.Update(context.Background(), resource.UpdateRequest{Plan: toolPlan(t, s, &m), State: state}, &resp)
	requireToolDiagnostics(t, resp.Diagnostics)
	p := f.policies["owned"]
	if p["priority"] != float64(10000) || p["enabled"] != true || p["description"] != nil || p["conditions"] != nil || p["config"] != nil || len(p["selectors"].(map[string]interface{})) != 0 {
		t.Fatalf("native fields failed update/clear: %v", p)
	}
	if marshalJSON([]interface{}{f.policies["other"], f.policies["app"]}) != others {
		t.Fatal("other owners were renumbered/mutated")
	}
	for _, request := range f.requests {
		if strings.Contains(request, "reorder") {
			t.Fatal("global reorder called")
		}
	}
	read := resource.ReadResponse{State: resp.State}
	r.Read(context.Background(), resource.ReadRequest{State: resp.State}, &read)
	requireToolDiagnostics(t, read.Diagnostics)
	if !reflect.DeepEqual(resp.State.Raw, read.State.Raw) {
		t.Fatal("policy refresh changed exact state")
	}
	del := resource.DeleteResponse{}
	r.Delete(context.Background(), resource.DeleteRequest{State: read.State}, &del)
	requireToolDiagnostics(t, del.Diagnostics)
	if f.policies["owned"] != nil || f.policies["app"] == nil {
		t.Fatal("delete ownership boundary failed")
	}
}

func TestToolBindingsScopeAndNativeIdentity(t *testing.T) {
	for _, kind := range []string{"company", "agent", "project", "routine", "issue", "gateway"} {
		t.Run(kind, func(t *testing.T) {
			f := newToolFixture(t)
			_, _, _, _ = createToolProfile(t, f)
			r := &PaperclipToolProfileBindingResource{}
			s := configureTestResource(t, f.server.URL, r)
			target := toolTestUUID
			if kind == "company" {
				target = "comp-1"
			}
			m := toolBindingModel{CompanyID: types.StringValue("comp-1"), ProfileID: types.StringValue("owned"), TargetType: types.StringValue(kind), TargetID: types.StringValue(target), Priority: types.Int64Value(0), Metadata: types.StringValue(`{}`)}
			created := resource.CreateResponse{State: tfsdk.State{Schema: s}}
			r.Create(context.Background(), resource.CreateRequest{Plan: toolPlan(t, s, &m)}, &created)
			requireToolDiagnostics(t, created.Diagnostics)
			requireToolDiagnostics(t, created.State.Get(context.Background(), &m))
			if m.ID.ValueString() != "binding" || m.Priority.ValueInt64() != 0 {
				t.Fatal("binding identity/default lost")
			}
			read := resource.ReadResponse{State: created.State}
			r.Read(context.Background(), resource.ReadRequest{State: created.State}, &read)
			requireToolDiagnostics(t, read.Diagnostics)
			if !reflect.DeepEqual(created.State.Raw, read.State.Raw) {
				t.Fatal("binding refresh changed state")
			}
			before := len(f.writes)
			duplicate := resource.CreateResponse{State: tfsdk.State{Schema: s}}
			r.Create(context.Background(), resource.CreateRequest{Plan: toolPlan(t, s, &m)}, &duplicate)
			if !duplicate.Diagnostics.HasError() || len(f.writes) != before {
				t.Fatal("duplicate binding was silently adopted or mutated")
			}
			del := resource.DeleteResponse{}
			r.Delete(context.Background(), resource.DeleteRequest{State: created.State}, &del)
			requireToolDiagnostics(t, del.Diagnostics)
			if len(f.profiles["owned"]["bindings"].([]interface{})) != 0 {
				t.Fatal("unbind retained target")
			}
		})
	}
}
func TestToolBindingUpdateCannotReplaceIdentity(t *testing.T) {
	m := toolBindingModel{CompanyID: types.StringValue("comp-1"), ProfileID: types.StringValue("p"), TargetType: types.StringValue("company"), TargetID: types.StringValue("comp-1"), Priority: types.Int64Value(100), Metadata: types.StringValue(`{ "x": 1 }`)}
	for _, field := range []string{"company", "profile", "type", "target", "priority", "metadata", "equivalent", "unknown"} {
		t.Run(field, func(t *testing.T) {
			next := m
			switch field {
			case "company":
				next.CompanyID = types.StringValue("other")
			case "profile":
				next.ProfileID = types.StringValue("other")
			case "type":
				next.TargetType = types.StringValue("agent")
			case "target":
				next.TargetID = types.StringValue("other")
			case "priority":
				next.Priority = types.Int64Value(1)
			case "metadata":
				next.Metadata = types.StringValue(`{}`)
			case "equivalent":
				next.Metadata = types.StringValue(`{"x":1}`)
			case "unknown":
				next.Priority = types.Int64Unknown()
				next.TargetID = types.StringUnknown()
			}
			err := bindingTransition(next, m)
			if (err == nil) != (field == "equivalent" || field == "unknown") {
				t.Fatalf("insert-only transition: %v", err)
			}
		})
	}
}
func TestNativeFinishAndTrustObjectsAreNeverClaimed(t *testing.T) {
	f := newToolFixture(t)
	ctx := context.Background()
	profile := &PaperclipToolProfileResource{}
	ps := configureTestResource(t, f.server.URL, profile)
	m := profileTestModel()
	m.ID = types.StringValue("app")
	state := toolState(t, ps, &m)
	read := resource.ReadResponse{State: state}
	profile.Read(ctx, resource.ReadRequest{State: state}, &read)
	if !read.Diagnostics.HasError() || !reflect.DeepEqual(read.State.Raw, state.Raw) {
		t.Fatal("finish profile claimed or lost")
	}
	del := resource.DeleteResponse{}
	profile.Delete(ctx, resource.DeleteRequest{State: state}, &del)
	if !del.Diagnostics.HasError() {
		t.Fatal("finish profile deleted")
	}
	update := resource.UpdateResponse{State: state}
	profile.Update(ctx, resource.UpdateRequest{Plan: toolPlan(t, ps, &m), State: state}, &update)
	if !update.Diagnostics.HasError() {
		t.Fatal("finish profile updated")
	}
	for _, kind := range []string{"app", "trust"} {
		if kind == "trust" {
			f.policies[kind] = map[string]interface{}{"id": kind, "companyId": "comp-1", "name": "Trust", "policyType": "trust_rule"}
		}
		r := &PaperclipPolicyResource{}
		s := configureTestResource(t, f.server.URL, r)
		model := policyTestModel()
		model.ID = types.StringValue(kind)
		st := toolState(t, s, &model)
		rr := resource.ReadResponse{State: st}
		r.Read(ctx, resource.ReadRequest{State: st}, &rr)
		if !rr.Diagnostics.HasError() {
			t.Fatal("native-owned policy imported")
		}
		dd := resource.DeleteResponse{}
		r.Delete(ctx, resource.DeleteRequest{State: st}, &dd)
		if !dd.Diagnostics.HasError() {
			t.Fatal("native-owned policy deleted")
		}
		uu := resource.UpdateResponse{State: st}
		r.Update(ctx, resource.UpdateRequest{Plan: toolPlan(t, s, &model), State: st}, &uu)
		if !uu.Diagnostics.HasError() {
			t.Fatal("native-owned policy updated")
		}
	}
	if len(f.writes) != 0 {
		t.Fatal("native-owned objects received a mutation")
	}
}

func TestToolReferenceValidationPrecedesMutation(t *testing.T) {
	f := newToolFixture(t)
	r := &PaperclipToolProfileResource{}
	s := configureTestResource(t, f.server.URL, r)
	m := profileTestModel()
	m.Entries = types.StringValue(`[{"selectorType":"connection","connectionId":"22222222-2222-4222-8222-222222222222"}]`)
	plan := toolPlan(t, s, &m)
	modify := resource.ModifyPlanResponse{Plan: plan}
	r.ModifyPlan(context.Background(), resource.ModifyPlanRequest{Plan: plan, State: tfsdk.State{Schema: s}}, &modify)
	if !modify.Diagnostics.HasError() {
		t.Fatal("known invalid reference survived plan")
	}
	created := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &created)
	if !created.Diagnostics.HasError() || len(f.writes) != 0 {
		t.Fatal("invalid reference wrote before validation")
	}
	m.Entries = types.StringUnknown()
	if err := r.validate(context.Background(), m); err != nil {
		t.Fatal("unknown references must defer:", err)
	}
	m.CompanyID = types.StringUnknown()
	if err := r.validate(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	policy := &PaperclipPolicyResource{}
	configureTestResource(t, f.server.URL, policy)
	pm := policyTestModel()
	pm.Selectors = types.StringValue(`{"projectId":"missing"}`)
	if err := policy.validate(context.Background(), pm); err == nil {
		t.Fatal("invalid policy scope accepted")
	}
}
