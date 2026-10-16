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

	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type gatewayFixture struct {
	t             *testing.T
	server        *httptest.Server
	gateways      map[string]map[string]interface{}
	tokens        map[string]map[string]interface{}
	events        []string
	fail          string
	inventory     string
	badWrite      bool
	finishProfile bool
}

func newGatewayFixture(t *testing.T) *gatewayFixture {
	f := &gatewayFixture{t: t, gateways: map[string]map[string]interface{}{}, tokens: map[string]map[string]interface{}{}}
	f.server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.server.Close)
	return f
}
func (f *gatewayFixture) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	p := r.URL.Path
	event := r.Method + " " + p
	if event == f.fail {
		w.WriteHeader(403)
		fmt.Fprint(w, `{"error":"fixture original cause","token":"pcgw_private"}`)
		return
	}
	if r.Method == "GET" {
		switch p {
		case "/api/companies/comp-1/tools/profiles":
			key := "owned"
			if f.finishProfile {
				key = "app:finish"
			}
			fmt.Fprintf(w, `{"profiles":[{"id":%q,"companyId":"comp-1","profileKey":%q,"entries":[],"bindings":[]}]}`, toolTestUUID, key)
			return
		case "/api/companies/comp-1/tools/gateways":
			if f.inventory != "" {
				fmt.Fprint(w, f.inventory)
				return
			}
			items := []interface{}{}
			for _, g := range f.gateways {
				if g["status"] == "archived" {
					continue
				}
				tokens := []interface{}{}
				for _, token := range f.tokens {
					if token["gatewayId"] == g["id"] {
						tokens = append(tokens, token)
					}
				}
				g["tokens"] = tokens
				items = append(items, g)
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"gateways": items})
			return
		}
		if strings.HasSuffix(p, "/"+toolTestUUID) || p == "/api/companies/comp-1" {
			fmt.Fprintf(w, `{"id":%q,"companyId":"comp-1"}`, toolTestUUID)
			return
		}
		http.NotFound(w, r)
		return
	}
	body := map[string]interface{}{}
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.events = append(f.events, event)
	if p == "/api/companies/comp-1/tools/gateways" && r.Method == "POST" {
		body["id"], body["companyId"], body["status"], body["tokens"] = "gateway-1", "comp-1", "active", []interface{}{}
		f.gateways["gateway-1"] = body
		if f.badWrite {
			body["id"] = ""
		}
		_ = json.NewEncoder(w).Encode(body)
		return
	}
	if r.Method == "PATCH" && p == "/api/tool-gateway/gateways/gateway-1" {
		if body["companyId"] != "comp-1" {
			f.t.Error("PATCH omitted companyId")
		}
		for k, v := range body {
			f.gateways["gateway-1"][k] = v
		}
		_ = json.NewEncoder(w).Encode(f.gateways["gateway-1"])
		return
	}
	if r.Method == "POST" && p == "/api/tool-gateway/gateways/gateway-1/tokens" {
		if body["subjectType"] != "gateway_client" || body["companyId"] != "comp-1" || body["expiresAt"] == nil {
			f.t.Error("mint body violated native contract")
		}
		id := fmt.Sprintf("token-%d", len(f.tokens)+1)
		body["id"], body["gatewayId"], body["revokedAt"] = id, "gateway-1", nil
		f.tokens[id] = body
		created := map[string]interface{}{}
		for k, v := range body {
			created[k] = v
		}
		created["token"] = "pcgw_private_" + id
		_ = json.NewEncoder(w).Encode(created)
		return
	}
	if r.Method == "POST" && strings.HasSuffix(p, "/revoke") {
		id := strings.Split(p, "/")[4]
		if body["companyId"] != "comp-1" {
			f.t.Error("revoke omitted companyId")
		}
		f.tokens[id]["revokedAt"] = "2100-01-01T00:00:00Z"
		_ = json.NewEncoder(w).Encode(f.tokens[id])
		return
	}
	f.t.Errorf("unsupported mutation %s", event)
	http.NotFound(w, r)
}
func gatewayTestModel() gatewayModel {
	return gatewayModel{CompanyID: types.StringValue("comp-1"), Name: types.StringValue("Gateway"), Slug: types.StringValue("gateway"), ProfileID: types.StringValue(toolTestUUID), Status: types.StringValue("active"), DefaultProfileMode: types.StringValue("gateway_only"), ContextScopeType: types.StringValue("none"), AuthConfig: types.StringValue(`{}`), HeaderPolicy: types.StringValue(`{}`), MetadataPolicy: types.StringValue(`{}`), OnDemandToolsConfig: types.StringValue(`{}`), Metadata: types.StringValue(`{}`)}
}
func createGateway(t *testing.T, f *gatewayFixture) (*PaperclipMCPGatewayResource, schema.Schema, gatewayModel, tfsdk.State) {
	t.Helper()
	r := &PaperclipMCPGatewayResource{}
	s := configureTestResource(t, f.server.URL, r)
	m := gatewayTestModel()
	resp := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(context.Background(), resource.CreateRequest{Plan: toolPlan(t, s, &m)}, &resp)
	requireToolDiagnostics(t, resp.Diagnostics)
	requireToolDiagnostics(t, resp.State.Get(context.Background(), &m))
	return r, s, m, resp.State
}
func TestMCPGatewayRegistration(t *testing.T) {
	ctx := context.Background()
	p := New("test")().(*PaperclipProvider)
	want := map[string]bool{"paperclip_mcp_gateway": false, "paperclip_mcp_gateway_token": false}
	for _, factory := range p.Resources(ctx) {
		var m resource.MetadataResponse
		factory().Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "paperclip"}, &m)
		if _, ok := want[m.TypeName]; ok {
			want[m.TypeName] = true
		}
	}
	for k, v := range want {
		if !v {
			t.Errorf("missing native lifecycle resource %s", k)
		}
	}
	found := false
	for _, factory := range p.EphemeralResources(ctx) {
		var m ephemeral.MetadataResponse
		factory().Metadata(ctx, ephemeral.MetadataRequest{ProviderTypeName: "paperclip"}, &m)
		found = found || m.TypeName == "paperclip_mcp_gateway_token"
	}
	if !found {
		t.Error("missing apply-only token producer")
	}
}
func TestGatewayCreateRefreshImportUpdateAndArchive(t *testing.T) {
	f := newGatewayFixture(t)
	r, s, m, state := createGateway(t, f)
	ctx := context.Background()
	if len(f.events) != 1 || m.ID.ValueString() != "gateway-1" || m.OwnerID.ValueString() == "" {
		t.Fatal("gateway creation lost identity")
	}
	read := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &read)
	requireToolDiagnostics(t, read.Diagnostics)
	if !reflect.DeepEqual(state.Raw, read.State.Raw) {
		t.Fatal("default policy expansion caused drift")
	}
	imp := resource.ImportStateResponse{State: toolState(t, s, &gatewayModel{})}
	r.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/gateway-1"}, &imp)
	requireToolDiagnostics(t, imp.Diagnostics)
	// Import has no policy spelling to preserve; use its refreshed native values.
	importer := &PaperclipMCPGatewayResource{}
	configureTestResource(t, f.server.URL, importer)
	importRead := resource.ReadResponse{State: imp.State}
	importer.Read(ctx, resource.ReadRequest{State: imp.State}, &importRead)
	requireToolDiagnostics(t, importRead.Diagnostics)
	var imported gatewayModel
	requireToolDiagnostics(t, importRead.State.Get(ctx, &imported))
	if imported.ID != m.ID || imported.ProfileID != m.ProfileID {
		t.Fatal("import changed native identity")
	}
	if imported.Metadata.ValueString() != "{}" { t.Fatal("imported native empty metadata became null and caused plan drift") }
	m.Status, m.Description, m.DefaultProfileMode = types.StringValue("disabled"), types.StringValue(""), types.StringValue("gateway_then_context")
	m.ContextScopeType, m.ContextScopeID = types.StringValue("project"), types.StringValue(toolTestUUID)
	m.AgentID, m.ProjectID, m.IssueID, m.ApprovalIssueID = types.StringValue(toolTestUUID), types.StringValue(toolTestUUID), types.StringValue(toolTestUUID), types.StringValue(toolTestUUID)
	m.AuthConfig = types.StringValue(`{"bearer":{"enabled":false,"defaultTtlSeconds":null},"oauth":{"protectedResourceMetadataPath":null,"dynamicClientRegistration":false,"authorizationCodePkce":false}}`)
	m.HeaderPolicy = types.StringValue(`{"callerPassthrough":{"enabled":true,"allowedHeaders":["X-Request-Id"]},"staticHeaders":[{"name":"X-Public","value":"example","valueRef":null}]}`)
	m.MetadataPolicy, m.OnDemandToolsConfig, m.Metadata = types.StringValue(`{"forwardCompanyId":true}`), types.StringValue(`{"enabled":true}`), types.StringValue(`{"owner":"test"}`)
	update := resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{Plan: toolPlan(t, s, &m), State: state}, &update)
	requireToolDiagnostics(t, update.Diagnostics)
	if f.gateways["gateway-1"]["status"] != "disabled" || f.gateways["gateway-1"]["contextScopeId"] != toolTestUUID {
		t.Fatal("update did not write native scope/status")
	}
	m.Description, m.ContextScopeID, m.ContextScopeType, m.Metadata = types.StringNull(), types.StringNull(), types.StringValue("none"), types.StringValue(`{}`)
	for _, ptr := range []*types.String{&m.AgentID, &m.ProjectID, &m.IssueID, &m.ApprovalIssueID} {
		*ptr = types.StringNull()
	}
	r.Update(ctx, resource.UpdateRequest{Plan: toolPlan(t, s, &m), State: update.State}, &update)
	requireToolDiagnostics(t, update.Diagnostics)
	if f.gateways["gateway-1"]["description"] != nil || f.gateways["gateway-1"]["projectId"] != nil {
		t.Fatal("native null clearing was lost")
	}
	del := resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: update.State}, &del)
	requireToolDiagnostics(t, del.Diagnostics)
	if f.gateways["gateway-1"]["status"] != "archived" || f.gateways["gateway-1"]["archivedAt"] != nil {
		t.Fatal("retirement fabricated native behavior")
	}
	r.Delete(ctx, resource.DeleteRequest{State: update.State}, &del)
	requireToolDiagnostics(t, del.Diagnostics)
	read = resource.ReadResponse{State: update.State}
	r.Read(ctx, resource.ReadRequest{State: update.State}, &read)
	requireToolDiagnostics(t, read.Diagnostics)
	if !read.State.Raw.IsNull() {
		t.Fatal("archived gateway remained managed")
	}
}
