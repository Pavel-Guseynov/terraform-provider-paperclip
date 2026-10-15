package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	ephemeralschema "github.com/hashicorp/terraform-plugin-framework/ephemeral/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestPaperclipProjectUpgradeStateProtocol(t *testing.T) {
	ctx := context.Background()
	server := providerserver.NewProtocol6(New("test")())()
	schemas, err := server.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil || len(schemas.Diagnostics) != 0 {
		for _, d := range schemas.Diagnostics {
			t.Logf("DIAG: %s - %s", d.Summary, d.Detail)
		}
		t.Fatalf("provider schema failed: %v, %v", err, schemas)
	}
	projectSchema := schemas.ResourceSchemas["paperclip_project"]
	if projectSchema.Version != 2 {
		t.Fatalf("project schema has no versioned migration: %d", projectSchema.Version)
	}
	for _, test := range []struct {
		name    string
		version int64
		lead    string
		nulls   bool
	}{
		{name: "assigned lead", lead: `,"lead_agent_id":"existing-agent"`},
		{name: "null lead", lead: `,"lead_agent_id":null`, nulls: true},
		{name: "already separated version zero"},
		{name: "current version", version: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			description, policy := `"preserved description"`, `"{\"enabled\":true}"`
			if test.nulls {
				description, policy = "null", "null"
			}
			wantJSON := `{"id":"existing-project","company_id":"existing-company","name":"project","description":` + description + `,"execution_workspace_policy":` + policy + `}`
			priorJSON := strings.TrimSuffix(wantJSON, "}") + test.lead + "}"
			if !test.nulls {
				wantJSON = strings.Replace(wantJSON, policy, `{"enabled":true}`, 1)
			}
			// No client is configured: migration must need neither credentials nor API calls.
			upgraded, err := server.UpgradeResourceState(ctx, &tfprotov6.UpgradeResourceStateRequest{
				TypeName: "paperclip_project", Version: test.version,
				RawState: &tfprotov6.RawState{JSON: []byte(priorJSON)},
			})
			if err != nil || len(upgraded.Diagnostics) != 0 {
				t.Fatalf("upgrade failed: %v, %v", err, upgraded)
			}
			got, err := upgraded.UpgradedState.Unmarshal(projectSchema.ValueType())
			if err != nil {
				t.Fatal(err)
			}
			want, err := (&tfprotov6.RawState{JSON: []byte(wantJSON)}).Unmarshal(projectSchema.ValueType())
			if err != nil || !got.Equal(want) {
				t.Fatalf("upgrade changed retained project state: %s; expected %s: %v", got, want, err)
			}
			var values map[string]tftypes.Value
			if err := got.As(&values); err != nil {
				t.Fatal(err)
			}
			if _, exists := values["lead_agent_id"]; exists {
				t.Fatal("retired ownership remains in upgraded state")
			}
		})
	}
	for _, raw := range []string{
		`{"id":`,
		`{"id":{},"company_id":"company","name":"project","description":null,"execution_workspace_policy":null,"lead_agent_id":null}`,
	} {
		upgraded, err := server.UpgradeResourceState(ctx, &tfprotov6.UpgradeResourceStateRequest{
			TypeName: "paperclip_project", Version: 0,
			RawState: &tfprotov6.RawState{JSON: []byte(raw)},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(upgraded.Diagnostics) == 0 || upgraded.UpgradedState != nil {
			t.Fatalf("malformed prior state was accepted: %v", upgraded)
		}
	}
}

func TestPaperclipProjectLeadLifecycle(t *testing.T) {
	ctx := context.Background()
	lead, projectID := "old-agent", "project"
	getStatus, patchStatus, writes := 200, 200, 0
	ignoreWrite := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/api/projects/project" {
			t.Errorf("unexpected project request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if req.Method == http.MethodPatch {
			writes++
			if patchStatus != 200 {
				w.WriteHeader(patchStatus)
				return
			}
			var body map[string]interface{}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			value, present := body["leadAgentId"]
			if !present || len(body) != 1 {
				t.Errorf("assignment touched other project settings: %v", body)
			}
			if !ignoreWrite {
				lead, _ = value.(string)
			}
		} else if req.Method != http.MethodGet {
			t.Errorf("unexpected method %s", req.Method)
		}
		if req.Method == http.MethodGet && getStatus != 200 {
			w.WriteHeader(getStatus)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": projectID, "leadAgentId": lead})
	}))
	defer server.Close()
	r := NewPaperclipProjectLeadResource().(*PaperclipProjectLeadResource)
	s := configureTestResource(t, server.URL, r)
	model := PaperclipProjectLeadModel{ID: types.StringUnknown(), ProjectID: types.StringValue("project"), AgentID: types.StringValue("agent")}
	plan := tfsdk.Plan{Schema: s}
	setPlan := func() {
		t.Helper()
		if diags := plan.Set(ctx, &model); diags.HasError() {
			t.Fatal(diags)
		}
	}
	setPlan()
	created := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
	if created.Diagnostics.HasError() || lead != "agent" || writes != 1 {
		t.Fatalf("assignment create failed: %v", created.Diagnostics)
	}
	if diags := created.State.Get(ctx, &model); diags.HasError() || model.ID.ValueString() != "project" {
		t.Fatalf("invalid created identity: %v", diags)
	}
	imported := resource.ImportStateResponse{State: tfsdk.State{Schema: s}}
	if diags := imported.State.Set(ctx, &PaperclipProjectLeadModel{}); diags.HasError() {
		t.Fatal(diags)
	}
	r.ImportState(ctx, resource.ImportStateRequest{ID: "project"}, &imported)
	if imported.Diagnostics.HasError() {
		t.Fatal(imported.Diagnostics)
	}
	lead = "replacement"
	read := resource.ReadResponse{State: imported.State}
	r.Read(ctx, resource.ReadRequest{State: imported.State}, &read)
	var observed PaperclipProjectLeadModel
	if diags := read.State.Get(ctx, &observed); diags.HasError() || observed.AgentID.ValueString() != lead || observed.ProjectID.ValueString() != "project" {
		t.Fatalf("import did not read native assignment: %v", diags)
	}
	setPlan()
	updated := resource.UpdateResponse{State: read.State}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: read.State}, &updated)
	if updated.Diagnostics.HasError() || lead != "agent" {
		t.Fatalf("drift correction failed: %v", updated.Diagnostics)
	}
	deleted := resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: updated.State}, &deleted)
	if deleted.Diagnostics.HasError() || lead != "" {
		t.Fatalf("assignment removal failed: %v", deleted.Diagnostics)
	}
	read = resource.ReadResponse{State: updated.State}
	r.Read(ctx, resource.ReadRequest{State: updated.State}, &read)
	if read.Diagnostics.HasError() || !read.State.Raw.IsNull() {
		t.Fatal("missing assignment was not removed from state")
	}
	lead = "external-agent"
	before := writes
	r.Delete(ctx, resource.DeleteRequest{State: updated.State}, &deleted)
	if deleted.Diagnostics.HasError() || writes != before || lead != "external-agent" {
		t.Fatal("deletion changed an externally replaced lead")
	}
	for _, status := range []int{403, 404, 500} {
		getStatus = status
		read = resource.ReadResponse{State: updated.State}
		r.Read(ctx, resource.ReadRequest{State: updated.State}, &read)
		if read.Diagnostics.HasError() != (status != 404) || (status == 404 && !read.State.Raw.IsNull()) {
			t.Fatalf("incorrect read failure handling for %d: %v", status, read.Diagnostics)
		}
		deleted = resource.DeleteResponse{}
		r.Delete(ctx, resource.DeleteRequest{State: updated.State}, &deleted)
		if deleted.Diagnostics.HasError() != (status != 404) {
			t.Fatalf("incorrect deletion failure handling for %d", status)
		}
	}
	getStatus = 200
	for _, failure := range []string{"patch", "readback", "identity", "read", "empty"} {
		t.Run(failure, func(t *testing.T) {
			lead = "agent"
			switch failure {
			case "patch":
				patchStatus = 403
			case "readback":
				ignoreWrite = true
				lead = "other"
			case "identity":
				projectID = "wrong-project"
			case "read":
				getStatus = 403
			case "empty":
				model.AgentID = types.StringValue("")
			}
			setPlan()
			badCreate := resource.CreateResponse{State: tfsdk.State{Schema: s}}
			r.Create(ctx, resource.CreateRequest{Plan: plan}, &badCreate)
			if !badCreate.Diagnostics.HasError() {
				t.Fatal("create accepted failure")
			}
			badUpdate := resource.UpdateResponse{State: updated.State}
			r.Update(ctx, resource.UpdateRequest{Plan: plan}, &badUpdate)
			if !badUpdate.Diagnostics.HasError() {
				t.Fatal("update accepted failure")
			}
			if failure == "patch" {
				badDelete := resource.DeleteResponse{}
				r.Delete(ctx, resource.DeleteRequest{State: updated.State}, &badDelete)
				if !badDelete.Diagnostics.HasError() {
					t.Fatal("delete hid patch failure")
				}
			}
			patchStatus, getStatus, projectID, ignoreWrite = 200, 200, "project", false
			model.AgentID = types.StringValue("agent")
		})
	}
	var projectSchema resource.SchemaResponse
	NewPaperclipProjectResource().Schema(ctx, resource.SchemaRequest{}, &projectSchema)
	if _, exists := projectSchema.Schema.Attributes["lead_agent_id"]; exists {
		t.Fatal("two resources own project lead assignment")
	}
}

func TestPaperclipConnectionInstallReadComparesAgentMembership(t *testing.T) {
	ctx := context.Background()
	var installs []map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet || req.URL.Path != "/api/tool-connections/conn/installs" {
			t.Errorf("unexpected install request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"installs": installs})
	}))
	defer server.Close()
	cli, _ := client.NewClient(server.URL, "fixture-token")
	r := &PaperclipConnectionInstallResource{client: cli}
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	agents, _ := types.ListValueFrom(ctx, types.StringType, []string{"agent-1", "agent-2"})
	model := PaperclipConnectionInstallResourceModel{
		ID: types.StringValue("conn"), ConnectionID: types.StringValue("conn"),
		AgentIDs: agents, TargetType: types.StringValue("agent"), CompanyID: types.StringNull(),
	}
	initial := tfsdk.State{Schema: schemaResp.Schema}
	if diags := initial.Set(ctx, &model); diags.HasError() {
		t.Fatal(diags)
	}
	for _, test := range []struct {
		name   string
		actual []string
		want   []string
	}{
		{"reordered", []string{"agent-2", "agent-1"}, []string{"agent-1", "agent-2"}},
		{"removed", []string{}, []string{}},
		{"replaced", []string{"agent-3"}, []string{"agent-3"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			installs = []map[string]interface{}{}
			for _, id := range test.actual {
				installs = append(installs, map[string]interface{}{"targetType": "agent", "targetId": id})
			}
			read := resource.ReadResponse{State: initial}
			r.Read(ctx, resource.ReadRequest{State: initial}, &read)
			if read.Diagnostics.HasError() {
				t.Fatal(read.Diagnostics)
			}
			var observed PaperclipConnectionInstallResourceModel
			if diags := read.State.Get(ctx, &observed); diags.HasError() {
				t.Fatal(diags)
			}
			want, _ := types.ListValueFrom(ctx, types.StringType, test.want)
			if !observed.AgentIDs.Equal(want) {
				t.Fatalf("unexpected install membership: got %s, want %s", observed.AgentIDs, want)
			}
		})
	}
}

// paperclipPolicies models Paperclip's tool-policy routes for the fixture
// company: App finish upserts company-wide ask-first policies and disables its
// stale ones, the generic routes create config-free policies under
// company-unique names and delete them, and a PATCH is refused with 422 as the
// live server refuses finish-owned ones.
type paperclipPolicies struct {
	t    *testing.T
	rows []map[string]interface{}
	// connectionMissing makes the connection read fail as a deleted one does.
	connectionMissing bool
}

func (p *paperclipPolicies) finish(askFirst []string) {
	upserted := map[string]bool{}
	for _, row := range p.rows {
		config, ok := row["config"].(map[string]interface{})
		if !ok {
			continue
		}
		entry, _ := config["catalogEntryId"].(string)
		row["enabled"] = false
		for _, id := range askFirst {
			if id == entry {
				row["enabled"], row["selectors"], upserted[id] = true, map[string]interface{}{"catalogEntryId": id}, true
			}
		}
	}
	for _, id := range askFirst {
		if !upserted[id] {
			p.rows = append(p.rows, map[string]interface{}{
				"id": "finish-" + id, "enabled": true, "policyType": "require_approval",
				"selectors": map[string]interface{}{"catalogEntryId": id},
				"config":    map[string]interface{}{"source": "app_gallery_finish", "connectionId": "conn", "catalogEntryId": id},
			})
		}
	}
}

func (p *paperclipPolicies) enabled() []map[string]interface{} {
	rows := []map[string]interface{}{}
	for _, row := range p.rows {
		if row["enabled"] == true {
			rows = append(rows, row)
		}
	}
	return rows
}

func (p *paperclipPolicies) serve(w http.ResponseWriter, req *http.Request) bool {
	const collection = "/api/companies/company/tools/policies"
	switch {
	case req.URL.Path == "/api/tool-connections/conn" && req.Method == http.MethodGet:
		if p.connectionMissing {
			http.NotFound(w, req)
			return true
		}
		// Apply names the ask-first policy after its connection.
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "conn", "name": "gitea (demo)", "applicationId": "app"})
	case req.URL.Path == collection && req.Method == http.MethodGet:
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"policies": p.rows})
	case req.URL.Path == collection && req.Method == http.MethodPost:
		var body map[string]interface{}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || body["config"] != nil {
			http.Error(w, "Tool policy type 'require_approval' does not support config", http.StatusUnprocessableEntity)
			return true
		}
		for _, row := range p.rows {
			if row["name"] == body["name"] {
				http.Error(w, "duplicate key value violates unique constraint \"tool_policies_company_name_uq\"", http.StatusInternalServerError)
				return true
			}
		}
		entries := []string{}
		for _, entry := range body["selectors"].(map[string]interface{})["catalogEntryIds"].([]interface{}) {
			entries = append(entries, entry.(string))
		}
		body["id"], body["enabled"] = "created-"+strings.Join(entries, "+"), true
		p.rows = append(p.rows, body)
		_ = json.NewEncoder(w).Encode(body)
	case strings.HasPrefix(req.URL.Path, collection+"/") && req.Method == http.MethodDelete:
		id := strings.TrimPrefix(req.URL.Path, collection+"/")
		for i, row := range p.rows {
			if row["id"] == id {
				p.rows = append(p.rows[:i], p.rows[i+1:]...)
				break
			}
		}
		_, _ = w.Write([]byte(`{}`))
	case strings.HasPrefix(req.URL.Path, collection+"/"):
		p.t.Errorf("unexpected %s %s", req.Method, req.URL.Path)
		http.Error(w, "Tool policy type 'require_approval' does not support config", http.StatusUnprocessableEntity)
	default:
		return false
	}
	return true
}

func TestPaperclipPermissionLifecycle(t *testing.T) {
	ctx := context.Background()
	ready, exists := false, false
	ignoreDelete, failRead, failDelete := false, false, false
	writes := 0
	catalog := []map[string]interface{}{
		{"id": "read-id", "toolName": "read", "isReadOnly": true, "status": "active"},
		{"id": "write-id", "toolName": "write", "isWrite": true, "status": "active"},
		{"id": "excluded-id", "toolName": "excluded", "status": "active"},
	}
	var applied client.PaperclipAppFinish
	policies := &paperclipPolicies{t: t}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case "/api/tool-connections/conn/catalog":
			items := []map[string]interface{}{}
			if ready {
				items = catalog
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"catalog": items})
		case "/api/tool-connections/conn/catalog/refresh":
			if req.Method != http.MethodPost {
				t.Error("invalid discovery method")
			}
			ready = true
			_, _ = w.Write([]byte(`{}`))
		case "/api/companies/company/tools/apps/conn/finish":
			var requested client.PaperclipAppFinish
			if err := json.NewDecoder(req.Body).Decode(&requested); err != nil {
				t.Error(err)
			}
			if requested.EnabledCatalogEntryIDs == nil || requested.AskFirstCatalogEntryIDs == nil || requested.ReviewedCatalogEntryIDs == nil || requested.Access == nil {
				t.Error("finish requires every array and an access scope")
			}
			applied = requested
			policies.finish(requested.AskFirstCatalogEntryIDs)
			writes++
			exists = true
			_, _ = w.Write([]byte(`{}`))
		case "/api/companies/company/tools/profiles/app-profile/unbind":
			if failDelete {
				http.Error(w, "fixture unbind failure", http.StatusInternalServerError)
				return
			}
			if !ignoreDelete {
				applied.Access = nil
			}
			_, _ = w.Write([]byte(`{"unbound":1}`))
		case "/api/companies/company/tools/profiles":
			if failRead {
				http.Error(w, "fixture read failure", http.StatusInternalServerError)
				return
			}
			profiles := []map[string]interface{}{}
			if exists {
				entries := []map[string]interface{}{}
				for _, id := range applied.EnabledCatalogEntryIDs {
					entries = append(entries, map[string]interface{}{"id": "entry-" + id, "catalogEntryId": id, "connectionId": "conn", "selectorType": "catalog_entry", "effect": "include"})
				}
				bindings := []map[string]interface{}{}
				if applied.Access == "all_agents" {
					bindings = append(bindings, map[string]interface{}{"targetType": "company", "targetId": "company"})
				} else if access, ok := applied.Access.(map[string]interface{}); ok && access != nil {
					if agentIDs, ok := access["agentIds"].([]interface{}); ok {
						for _, id := range agentIDs {
							bindings = append(bindings, map[string]interface{}{"targetType": "agent", "targetId": id})
						}
					}
				}
				profiles = append(profiles, map[string]interface{}{"id": "app-profile", "profileKey": "app:conn", "status": "active", "defaultAction": "deny", "entries": entries, "bindings": bindings})
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"profiles": profiles})
		default:
			if entry, ok := strings.CutPrefix(req.URL.Path, "/api/tool-profile-entries/entry-"); ok && req.Method == http.MethodDelete {
				if !ignoreDelete {
					remaining := []string{}
					for _, id := range applied.EnabledCatalogEntryIDs {
						if id != entry {
							remaining = append(remaining, id)
						}
					}
					applied.EnabledCatalogEntryIDs = remaining
				}
				_, _ = w.Write([]byte(`{}`))
				return
			}
			if !policies.serve(w, req) {
				http.NotFound(w, req)
			}
		}
	}))
	defer server.Close()
	cli, _ := client.NewClient(server.URL, "fixture-token")
	r := &PaperclipAppPermissionResource{client: cli}
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	agents, _ := types.ListValueFrom(ctx, types.StringType, []string{"agent-1"})
	model := PaperclipAppPermissionResourceModel{
		ID: types.StringUnknown(), CompanyID: types.StringValue("company"), ConnectionID: types.StringValue("conn"),
		ToolSelection:          types.StringValue(`{"approval_mode":"writes","enabled_tools":["read","write"]}`),
		EnabledCatalogEntryIDs: types.ListUnknown(types.StringType), AskFirstCatalogEntryIDs: types.ListUnknown(types.StringType),
		ReviewedCatalogEntryIDs: types.ListUnknown(types.StringType), AgentIDs: agents, AccessType: types.StringValue("agents"),
	}
	initial := tfsdk.State{Schema: schemaResp.Schema}
	if diags := initial.Set(ctx, &model); diags.HasError() {
		t.Fatal(diags)
	}
	plan := tfsdk.Plan{Schema: schemaResp.Schema, Raw: initial.Raw}
	// The policy is named after its connection, so an unreadable connection
	// fails before finish grants any tool.
	policies.connectionMissing = true
	var unreadable resource.CreateResponse
	unreadable.State = initial
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &unreadable)
	if !unreadable.Diagnostics.HasError() || writes != 0 {
		t.Fatal("permission was applied without reading its connection")
	}
	policies.connectionMissing = false
	var created resource.CreateResponse
	created.State = initial
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
	if created.Diagnostics.HasError() {
		t.Fatal(created.Diagnostics)
	}
	if writes != 1 || !reflect.DeepEqual(applied.EnabledCatalogEntryIDs, []string{"read-id", "write-id"}) || !reflect.DeepEqual(applied.AskFirstCatalogEntryIDs, []string{}) {
		t.Fatalf("incorrect grant: %#v", applied)
	}
	enabled := policies.enabled()
	if len(enabled) != 1 || !strings.HasPrefix(enabled[0]["name"].(string), "Ask first: gitea (demo) (") {
		t.Fatalf("write was not gated by one policy named after its connection: %#v", policies.rows)
	}
	gate := map[string]interface{}{}
	for key, value := range enabled[0] {
		if key != "name" {
			gate[key] = value
		}
	}
	scoped := map[string]interface{}{
		"id": "created-write-id", "enabled": true, "description": "Ask first before running any of the 1 ask-first tools of gitea (demo).",
		"policyType": "require_approval", "priority": float64(50),
		"selectors": map[string]interface{}{"catalogEntryIds": []interface{}{"write-id"}, "agentIds": []interface{}{"agent-1"}},
	}
	if !reflect.DeepEqual(gate, scoped) {
		t.Fatalf("write was not gated for exactly its granted agent: %#v", policies.rows)
	}

	// A native permission change must become a real update in the next plan.
	applied.EnabledCatalogEntryIDs = []string{"read-id"}
	applied.AskFirstCatalogEntryIDs = []string{}
	applied.Access = "all_agents"
	var read resource.ReadResponse
	read.State = created.State
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatal(read.Diagnostics)
	}
	var observed PaperclipAppPermissionResourceModel
	if diags := read.State.Get(ctx, &observed); diags.HasError() {
		t.Fatal(diags)
	}
	if observed.AccessType.ValueString() != "all_agents" || len(observed.AgentIDs.Elements()) != 0 {
		t.Fatal("native company-wide access was not recorded in resource state")
	}
	driftPlan := tfsdk.Plan{Schema: schemaResp.Schema, Raw: read.State.Raw}
	// OpenTofu overlays the configured agent scope on refreshed native state.
	if diags := driftPlan.SetAttribute(ctx, path.Root("access_type"), "agents"); diags.HasError() {
		t.Fatal(diags)
	}
	if diags := driftPlan.SetAttribute(ctx, path.Root("agent_ids"), agents); diags.HasError() {
		t.Fatal(diags)
	}
	var modified resource.ModifyPlanResponse
	modified.Plan = driftPlan
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: driftPlan, State: read.State}, &modified)
	if modified.Diagnostics.HasError() {
		t.Fatal(modified.Diagnostics)
	}
	var planned PaperclipAppPermissionResourceModel
	if diags := modified.Plan.Get(ctx, &planned); diags.HasError() {
		t.Fatal(diags)
	}
	var expected []string
	if diags := planned.EnabledCatalogEntryIDs.ElementsAs(ctx, &expected, false); diags.HasError() {
		t.Fatal(diags)
	}
	if !reflect.DeepEqual(expected, []string{"read-id", "write-id"}) {
		t.Fatalf("drift was not planned: %v", expected)
	}
	var updated resource.UpdateResponse
	updated.State = read.State
	r.Update(ctx, resource.UpdateRequest{Plan: modified.Plan, State: read.State}, &updated)
	if updated.Diagnostics.HasError() {
		t.Fatal(updated.Diagnostics)
	}
	if writes != 2 {
		t.Fatal("drift was not repaired")
	}
	var converged PaperclipAppPermissionResourceModel
	if diags := updated.State.Get(ctx, &converged); diags.HasError() {
		t.Fatal(diags)
	}
	if converged.AccessType.ValueString() != "agents" || !reflect.DeepEqual(converged.AgentIDs, agents) {
		t.Fatal("company-wide permission did not converge to the configured agent scope")
	}

	// Applying a previously reviewed ID set after the catalog changes fails closed.
	catalog[1]["id"] = "changed-id"
	var rejected resource.UpdateResponse
	rejected.State = updated.State
	r.Update(ctx, resource.UpdateRequest{Plan: modified.Plan, State: updated.State}, &rejected)
	if !rejected.Diagnostics.HasError() || writes != 2 {
		t.Fatal("changed catalog was applied without re-plan")
	}
	catalog[1]["id"] = "write-id"

	// Removal empties the native profile, since finish cannot express zero
	// access, and deletes the approval policies only once access is gone.
	for _, failure := range []string{"unbind", "unchanged", "read"} {
		failDelete = failure == "unbind"
		ignoreDelete = failure == "unchanged"
		failRead = failure == "read"
		var failedDelete resource.DeleteResponse
		r.Delete(ctx, resource.DeleteRequest{State: updated.State}, &failedDelete)
		if !failedDelete.Diagnostics.HasError() {
			t.Fatalf("permission removal accepted %s failure", failure)
		}
	}
	failDelete, ignoreDelete, failRead = false, false, false
	var deleted resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: updated.State}, &deleted)
	if deleted.Diagnostics.HasError() || writes != 2 || applied.Access != nil || len(applied.EnabledCatalogEntryIDs) != 0 || len(policies.enabled()) != 0 {
		t.Fatal("permission removal failed")
	}
	exists = false
	read.State = updated.State
	r.Read(ctx, resource.ReadRequest{State: updated.State}, &read)
	if read.Diagnostics.HasError() || !read.State.Raw.IsNull() {
		t.Fatal("missing native profile was retained in state")
	}
}

func TestPaperclipPermissionWaitsForOAuthAuthorization(t *testing.T) {
	ctx := context.Background()
	status, finished := "draft", false
	catalogReads, finishes := 0, 0
	policies := &paperclipPolicies{t: t}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case "/api/tool-connections/conn":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "conn", "name": "huggingface", "applicationId": "app", "authKind": "oauth", "status": status})
		case "/api/tool-connections/conn/catalog":
			catalogReads++
			if status == "draft" {
				http.Error(w, `{"error":"This app needs you to sign in.","code":"oauth_challenge"}`, http.StatusBadGateway)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"catalog": []map[string]interface{}{{"id": "search-id", "toolName": "search", "isReadOnly": true, "status": "active"}}})
		case "/api/companies/company/tools/apps/conn/finish":
			finishes++
			finished = true
			_, _ = w.Write([]byte(`{}`))
		case "/api/companies/company/tools/profiles":
			profiles := []map[string]interface{}{}
			if finished {
				profiles = append(profiles, map[string]interface{}{
					"id": "app-profile", "profileKey": "app:conn", "status": "active", "defaultAction": "deny",
					"entries":  []map[string]interface{}{{"id": "entry-search-id", "catalogEntryId": "search-id", "connectionId": "conn", "selectorType": "catalog_entry", "effect": "include"}},
					"bindings": []map[string]interface{}{{"targetType": "agent", "targetId": "agent-1"}},
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"profiles": profiles})
		default:
			if !policies.serve(w, req) {
				http.NotFound(w, req)
			}
		}
	}))
	defer server.Close()
	cli, _ := client.NewClient(server.URL, "fixture-token")
	r := &PaperclipAppPermissionResource{client: cli}
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	agents, _ := types.ListValueFrom(ctx, types.StringType, []string{"agent-1"})
	initial := tfsdk.State{Schema: schemaResp.Schema}
	if diags := initial.Set(ctx, &PaperclipAppPermissionResourceModel{
		ID: types.StringUnknown(), CompanyID: types.StringValue("company"), ConnectionID: types.StringValue("conn"),
		ToolSelection:          types.StringValue(`{"approval_mode":"writes","enabled_tools":["search"]}`),
		EnabledCatalogEntryIDs: types.ListUnknown(types.StringType), AskFirstCatalogEntryIDs: types.ListUnknown(types.StringType),
		ReviewedCatalogEntryIDs: types.ListUnknown(types.StringType), AgentIDs: agents, AccessType: types.StringValue("agents"),
	}); diags.HasError() {
		t.Fatal(diags)
	}
	plan := tfsdk.Plan{Schema: schemaResp.Schema, Raw: initial.Raw}

	// Before authorization the permission plans, applies, reads and deletes
	// without a catalog read or a finish.
	var modified resource.ModifyPlanResponse
	modified.Plan = plan
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan}, &modified)
	var planned PaperclipAppPermissionResourceModel
	modified.Plan.Get(ctx, &planned)
	if modified.Diagnostics.HasError() || len(planned.EnabledCatalogEntryIDs.Elements()) != 0 {
		t.Fatalf("pending permission plan: %v %s", modified.Diagnostics, planned.EnabledCatalogEntryIDs)
	}
	var pending resource.CreateResponse
	pending.State = initial
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &pending)
	if pending.Diagnostics.HasError() || pending.State.Raw.IsNull() {
		t.Fatalf("pending permission was not recorded: %v", pending.Diagnostics)
	}
	var kept resource.ReadResponse
	kept.State = pending.State
	r.Read(ctx, resource.ReadRequest{State: pending.State}, &kept)
	if kept.Diagnostics.HasError() || kept.State.Raw.IsNull() {
		t.Fatalf("pending permission was not kept: %v", kept.Diagnostics)
	}
	var removed resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: pending.State}, &removed)
	if removed.Diagnostics.HasError() || catalogReads != 0 || finishes != 0 {
		t.Fatalf("pending permission touched native state: %v reads=%d finishes=%d", removed.Diagnostics, catalogReads, finishes)
	}

	// Once authorized, the unfinished permission reads back absent and its
	// re-creation finishes the declared selection.
	status = "active"
	var absent resource.ReadResponse
	absent.State = pending.State
	r.Read(ctx, resource.ReadRequest{State: pending.State}, &absent)
	if absent.Diagnostics.HasError() || !absent.State.Raw.IsNull() {
		t.Fatalf("authorized connection kept the pending permission: %v", absent.Diagnostics)
	}
	var created resource.CreateResponse
	created.State = initial
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
	if created.Diagnostics.HasError() || finishes != 1 {
		t.Fatalf("authorized permission was not finished: %v finishes=%d", created.Diagnostics, finishes)
	}
}

func TestPaperclipPermissionRevokesUndeclaredAccess(t *testing.T) {
	ctx := context.Background()
	entries := []map[string]interface{}{{"id": "entry-write", "catalogEntryId": "write-id", "connectionId": "conn", "selectorType": "catalog_entry", "effect": "include"}}
	bindings := []map[string]interface{}{
		{"targetType": "agent", "targetId": "agent-2"}, {"targetType": "agent", "targetId": "agent-1"},
		{"targetType": "gateway", "targetId": "gateway-1"},
	}
	policies := &paperclipPolicies{t: t}
	policies.finish([]string{"write-id"})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case "/api/tool-connections/conn/catalog":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"catalog": []map[string]interface{}{
				{"id": "read-id", "toolName": "read", "isReadOnly": true, "status": "active"},
				{"id": "write-id", "toolName": "write", "isWrite": true, "status": "active"},
			}})
		case "/api/companies/company/tools/profiles":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"profiles": []map[string]interface{}{{
				"id": "stale-profile", "profileKey": "app:conn", "status": "active", "defaultAction": "deny",
				"entries": entries, "bindings": bindings,
			}}})
		case "/api/companies/company/tools/profiles/stale-profile/unbind":
			var target map[string]interface{}
			if err := json.NewDecoder(req.Body).Decode(&target); err != nil {
				t.Error(err)
			}
			remaining := []map[string]interface{}{}
			for _, binding := range bindings {
				if binding["targetType"] != target["targetType"] || binding["targetId"] != target["targetId"] {
					remaining = append(remaining, binding)
				}
			}
			bindings = remaining
			_, _ = w.Write([]byte(`{"unbound":1}`))
		case "/api/tool-profile-entries/entry-write":
			entries = []map[string]interface{}{}
			_, _ = w.Write([]byte(`{}`))
		case "/api/tool-profiles/stale-profile":
			// The runtime gateway's foreign key makes Paperclip refuse the deletion.
			t.Error("a profile a runtime gateway references cannot be deleted")
			http.Error(w, "violates RESTRICT setting of foreign key constraint", http.StatusInternalServerError)
		case "/api/companies/company/tools/apps/conn/finish":
			t.Error("finish cannot express zero access and must not be called")
		default:
			if !policies.serve(w, req) {
				http.NotFound(w, req)
			}
		}
	}))
	defer server.Close()
	cli, _ := client.NewClient(server.URL, "fixture-token")
	r := &PaperclipAppPermissionResource{client: cli}
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	none, _ := types.ListValueFrom(ctx, types.StringType, []string{})
	prior := tfsdk.State{Schema: schemaResp.Schema}
	if diags := prior.Set(ctx, &PaperclipAppPermissionResourceModel{
		ID: types.StringValue("conn"), CompanyID: types.StringValue("company"), ConnectionID: types.StringValue("conn"),
		ToolSelection:          types.StringValue(`{"approval_mode":"writes","enabled_tools":["read","write"]}`),
		EnabledCatalogEntryIDs: none, AskFirstCatalogEntryIDs: none, ReviewedCatalogEntryIDs: none,
		AgentIDs: none, AccessType: types.StringValue("agents"),
	}); diags.HasError() {
		t.Fatal(diags)
	}

	// A stale grant is visible although the permission declares no agent.
	var read resource.ReadResponse
	read.State = prior
	r.Read(ctx, resource.ReadRequest{State: prior}, &read)
	var observed PaperclipAppPermissionResourceModel
	if diags := read.State.Get(ctx, &observed); diags.HasError() || read.Diagnostics.HasError() {
		t.Fatal(diags, read.Diagnostics)
	}
	stale, _ := types.ListValueFrom(ctx, types.StringType, []string{"agent-1", "agent-2"})
	if !observed.AgentIDs.Equal(stale) || !observed.AskFirstCatalogEntryIDs.IsNull() {
		t.Fatalf("stale access was hidden: %s %s", observed.AgentIDs, observed.AskFirstCatalogEntryIDs)
	}

	plan := tfsdk.Plan{Schema: schemaResp.Schema, Raw: read.State.Raw}
	if diags := plan.SetAttribute(ctx, path.Root("agent_ids"), none); diags.HasError() {
		t.Fatal(diags)
	}
	var modified resource.ModifyPlanResponse
	modified.Plan = plan
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: read.State}, &modified)
	var planned PaperclipAppPermissionResourceModel
	if diags := modified.Plan.Get(ctx, &planned); diags.HasError() || modified.Diagnostics.HasError() {
		t.Fatal(diags, modified.Diagnostics)
	}
	if !planned.EnabledCatalogEntryIDs.Equal(none) || !planned.AskFirstCatalogEntryIDs.Equal(none) {
		t.Fatalf("zero access planned tools: %s %s", planned.EnabledCatalogEntryIDs, planned.AskFirstCatalogEntryIDs)
	}

	var updated resource.UpdateResponse
	updated.State = read.State
	r.Update(ctx, resource.UpdateRequest{Plan: modified.Plan, State: read.State}, &updated)
	gatewayOnly := []map[string]interface{}{{"targetType": "gateway", "targetId": "gateway-1"}}
	if updated.Diagnostics.HasError() || !reflect.DeepEqual(bindings, gatewayOnly) || len(entries) != 0 || len(policies.enabled()) != 0 {
		t.Fatalf("stale access was not revoked: %v %v %v %#v", updated.Diagnostics, bindings, entries, policies.rows)
	}

	// An empty profile is the converged state of a permission granting no agent.
	var converged resource.ReadResponse
	converged.State = updated.State
	r.Read(ctx, resource.ReadRequest{State: updated.State}, &converged)
	var final PaperclipAppPermissionResourceModel
	if diags := converged.State.Get(ctx, &final); diags.HasError() || converged.Diagnostics.HasError() {
		t.Fatal(diags, converged.Diagnostics)
	}
	if !final.AgentIDs.Equal(none) || !final.EnabledCatalogEntryIDs.Equal(none) || !final.AskFirstCatalogEntryIDs.Equal(none) {
		t.Fatalf("revoked permission did not converge: %#v", final)
	}
}

func TestPaperclipPermissionUnscopedPolicyDriftConvergence(t *testing.T) {
	ctx := context.Background()
	policies := &paperclipPolicies{t: t}
	policies.finish([]string{"write-id"})
	var finishedAskFirst []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case "/api/tool-connections/conn/catalog":
			catalog := []map[string]interface{}{
				{"id": "read-id", "toolName": "read", "connectionId": "conn", "isReadOnly": true, "status": "active"},
				{"id": "write-id", "toolName": "write", "connectionId": "conn", "isWrite": true, "status": "active"},
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"catalog": catalog})
		case "/api/companies/company/tools/profiles":
			profiles := []map[string]interface{}{
				{
					"profileKey":    "app:conn",
					"status":        "active",
					"defaultAction": "deny",
					"entries": []map[string]interface{}{
						{"catalogEntryId": "read-id", "connectionId": "conn", "selectorType": "catalog_entry", "effect": "include"},
						{"catalogEntryId": "write-id", "connectionId": "conn", "selectorType": "catalog_entry", "effect": "include"},
					},
					"bindings": []map[string]interface{}{
						{"targetType": "agent", "targetId": "agent-1"},
					},
				},
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"profiles": profiles})
		case "/api/companies/company/tools/apps/conn/finish":
			var requested client.PaperclipAppFinish
			if err := json.NewDecoder(req.Body).Decode(&requested); err != nil {
				t.Error(err)
			}
			finishedAskFirst = requested.AskFirstCatalogEntryIDs
			policies.finish(requested.AskFirstCatalogEntryIDs)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{})
		default:
			if !policies.serve(w, req) {
				http.NotFound(w, req)
			}
		}
	}))
	defer server.Close()

	cli, _ := client.NewClient(server.URL, "fixture-token")
	r := &PaperclipAppPermissionResource{client: cli}
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	agents, _ := types.ListValueFrom(ctx, types.StringType, []string{"agent-1"})
	enabledIDs, _ := types.ListValueFrom(ctx, types.StringType, []string{"read-id", "write-id"})
	priorAskIDs, _ := types.ListValueFrom(ctx, types.StringType, []string{"write-id"})
	reviewedIDs, _ := types.ListValueFrom(ctx, types.StringType, []string{})
	priorModel := PaperclipAppPermissionResourceModel{
		ID:                      types.StringValue("conn"),
		CompanyID:               types.StringValue("company"),
		ConnectionID:            types.StringValue("conn"),
		ToolSelection:           types.StringValue(`{"approval_mode":"writes","enabled_tools":["read","write"]}`),
		EnabledCatalogEntryIDs:  enabledIDs,
		AskFirstCatalogEntryIDs: priorAskIDs,
		ReviewedCatalogEntryIDs: reviewedIDs,
		AgentIDs:                agents,
		AccessType:              types.StringValue("agents"),
	}
	priorState := tfsdk.State{Schema: schemaResp.Schema}
	if diags := priorState.Set(ctx, &priorModel); diags.HasError() {
		t.Fatal(diags)
	}

	// 1. Read: finish's company-wide ask-first policy offers write to every
	// agent, so it is drift that no ask-first ID list represents.
	var readResp resource.ReadResponse
	readResp.State = priorState
	r.Read(ctx, resource.ReadRequest{State: priorState}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatal(readResp.Diagnostics)
	}
	var readModel PaperclipAppPermissionResourceModel
	if diags := readResp.State.Get(ctx, &readModel); diags.HasError() {
		t.Fatal(diags)
	}
	if !readModel.AskFirstCatalogEntryIDs.IsNull() {
		t.Fatalf("expected null ask_first_catalog_entry_ids due to unscoped policy, got: %#v", readModel.AskFirstCatalogEntryIDs)
	}

	// 2. ModifyPlan: Desired tool selection plans write-id back into ask_first_catalog_entry_ids.
	planRaw := tfsdk.Plan{Schema: schemaResp.Schema, Raw: readResp.State.Raw}
	var modifyResp resource.ModifyPlanResponse
	modifyResp.Plan = planRaw
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: planRaw, State: readResp.State}, &modifyResp)
	if modifyResp.Diagnostics.HasError() {
		t.Fatal(modifyResp.Diagnostics)
	}
	var plannedModel PaperclipAppPermissionResourceModel
	if diags := modifyResp.Plan.Get(ctx, &plannedModel); diags.HasError() {
		t.Fatal(diags)
	}
	var plannedAskIDs []string
	if diags := plannedModel.AskFirstCatalogEntryIDs.ElementsAs(ctx, &plannedAskIDs, false); diags.HasError() {
		t.Fatal(diags)
	}
	if !reflect.DeepEqual(plannedAskIDs, []string{"write-id"}) {
		t.Fatalf("expected planned ask_first_catalog_entry_ids to be [write-id], got: %v", plannedAskIDs)
	}

	// 3. Update: finish receives no ask-first entry and disables its own policy,
	// and write is gated by one new policy for exactly agent-1.
	var updateResp resource.UpdateResponse
	updateResp.State = readResp.State
	r.Update(ctx, resource.UpdateRequest{Plan: modifyResp.Plan, State: readResp.State}, &updateResp)
	if updateResp.Diagnostics.HasError() {
		t.Fatal(updateResp.Diagnostics)
	}
	enabled := policies.enabled()
	if !reflect.DeepEqual(finishedAskFirst, []string{}) || len(enabled) != 1 || enabled[0]["id"] != "created-write-id" || policies.rows[0]["enabled"] != false {
		t.Fatalf("ask-first policy was not replaced by an agent-scoped one: %#v %#v", finishedAskFirst, policies.rows)
	}
	var updatedModel PaperclipAppPermissionResourceModel
	if diags := updateResp.State.Get(ctx, &updatedModel); diags.HasError() {
		t.Fatal(diags)
	}
	var updatedAskIDs []string
	if diags := updatedModel.AskFirstCatalogEntryIDs.ElementsAs(ctx, &updatedAskIDs, false); diags.HasError() {
		t.Fatal(diags)
	}
	if !reflect.DeepEqual(updatedAskIDs, []string{"write-id"}) {
		t.Fatalf("expected converged ask_first_catalog_entry_ids to be [write-id], got: %v", updatedAskIDs)
	}

	// 4. Subsequent Read: State matches converged native state (no drift).
	var finalRead resource.ReadResponse
	finalRead.State = updateResp.State
	r.Read(ctx, resource.ReadRequest{State: updateResp.State}, &finalRead)
	if finalRead.Diagnostics.HasError() {
		t.Fatal(finalRead.Diagnostics)
	}
	var finalModel PaperclipAppPermissionResourceModel
	if diags := finalRead.State.Get(ctx, &finalModel); diags.HasError() {
		t.Fatal(diags)
	}
	var finalAskIDs []string
	if diags := finalModel.AskFirstCatalogEntryIDs.ElementsAs(ctx, &finalAskIDs, false); diags.HasError() {
		t.Fatal(diags)
	}
	if !reflect.DeepEqual(finalAskIDs, []string{"write-id"}) {
		t.Fatalf("expected converged final state to be [write-id], got: %v", finalAskIDs)
	}
}

func TestProviderStructure(t *testing.T) {
	ctx := context.Background()
	p := New("0.1.0")()

	var metaResp provider.MetadataResponse
	p.Metadata(ctx, provider.MetadataRequest{}, &metaResp)
	if metaResp.TypeName != "paperclip" || metaResp.Version != "0.1.0" {
		t.Fatalf("unexpected metadata: %+v", metaResp)
	}

	var schemaResp provider.SchemaResponse
	p.Schema(ctx, provider.SchemaRequest{}, &schemaResp)
	if _, ok := schemaResp.Schema.Attributes["endpoint"]; !ok {
		t.Fatalf("missing endpoint attribute in schema")
	}
	if _, ok := schemaResp.Schema.Attributes["token"]; !ok {
		t.Fatalf("missing token attribute in schema")
	}
	if _, ok := schemaResp.Schema.Attributes["paperclip_endpoint"]; !ok {
		t.Fatalf("missing paperclip_endpoint attribute in schema")
	}
	if _, ok := schemaResp.Schema.Attributes["paperclip_token"]; !ok {
		t.Fatalf("missing paperclip_token attribute in schema")
	}

	res := p.Resources(ctx)
	if len(res) != 33 {
		t.Fatalf("expected exactly 33 resources, got %d", len(res))
	}
	for _, rFn := range res {
		r := rFn()
		var rSchemaResp resource.SchemaResponse
		r.Schema(ctx, resource.SchemaRequest{}, &rSchemaResp)
		if rSchemaResp.Diagnostics.HasError() {
			t.Fatalf("resource schema error: %v", rSchemaResp.Diagnostics)
		}
	}

	ephemeralProvider, ok := p.(provider.ProviderWithEphemeralResources)
	if !ok {
		t.Fatal("provider does not expose ephemeral resources")
	}
	ephemeralResources := ephemeralProvider.EphemeralResources(ctx)
	if len(ephemeralResources) != 2 {
		t.Fatalf("expected exactly two ephemeral resources, got %d", len(ephemeralResources))
	}
	for _, ephemeralResource := range ephemeralResources {
		var ephemeralSchemaResp ephemeral.SchemaResponse
		ephemeralResource().Schema(ctx, ephemeral.SchemaRequest{}, &ephemeralSchemaResp)
		if ephemeralSchemaResp.Diagnostics.HasError() {
			t.Fatalf("ephemeral schema error: %v", ephemeralSchemaResp.Diagnostics)
		}
		token, ok := ephemeralSchemaResp.Schema.Attributes["token"].(ephemeralschema.StringAttribute)
		if !ok || !token.Sensitive || !token.Computed {
			t.Fatal("ephemeral token must be sensitive and computed")
		}
	}

	ds := p.DataSources(ctx)
	if len(ds) != 6 {
		t.Fatalf("expected exactly 6 data sources, got %d", len(ds))
	}
	for _, dsFn := range ds {
		d := dsFn()
		var dSchemaResp datasource.SchemaResponse
		d.Schema(ctx, datasource.SchemaRequest{}, &dSchemaResp)
		if dSchemaResp.Diagnostics.HasError() {
			t.Fatalf("data source schema error: %v", dSchemaResp.Diagnostics)
		}
	}
}

func TestPaperclipBoardKeyEphemeralResource(t *testing.T) {
	t.Setenv("PAPERCLIP_SCRATCH_DIR", t.TempDir())
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(req.URL.Path, "/api/board-api-keys/") && req.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	cli, _ := client.NewClient(server.URL, "test-token")

	r := NewPaperclipBoardKeyEphemeralResource().(*PaperclipBoardKeyEphemeralResource)
	var schemaResp ephemeral.SchemaResponse
	r.Schema(ctx, ephemeral.SchemaRequest{}, &schemaResp)
	model := PaperclipBoardKeyEphemeralResourceModel{
		KeyID: types.StringValue("key-1"),
	}
	tmpState := tfsdk.State{Schema: schemaResp.Schema}
	if diags := tmpState.Set(ctx, &model); diags.HasError() {
		t.Fatalf("failed to build ephemeral config: %v", diags)
	}
	request := ephemeral.OpenRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: tmpState.Raw}}

	var unconfigured ephemeral.OpenResponse
	unconfigured.Result = tfsdk.EphemeralResultData{Raw: tmpState.Raw, Schema: schemaResp.Schema}
	r.Open(ctx, request, &unconfigured)
	if !unconfigured.Diagnostics.HasError() {
		t.Fatal("unconfigured ephemeral resource must fail")
	}

	var nilConfigure ephemeral.ConfigureResponse
	r.Configure(ctx, ephemeral.ConfigureRequest{}, &nilConfigure)
	if nilConfigure.Diagnostics.HasError() {
		t.Fatalf("nil provider data should be deferred: %v", nilConfigure.Diagnostics)
	}
	var invalidConfigure ephemeral.ConfigureResponse
	r.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: "invalid"}, &invalidConfigure)
	if !invalidConfigure.Diagnostics.HasError() {
		t.Fatal("invalid provider data must fail")
	}
	var configure ephemeral.ConfigureResponse
	r.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: cli}, &configure)
	if configure.Diagnostics.HasError() {
		t.Fatalf("configure failed: %v", configure.Diagnostics)
	}

	// Unavailable token fails with error diagnostic
	var unavailableOpen ephemeral.OpenResponse
	unavailableOpen.Result = tfsdk.EphemeralResultData{Raw: tmpState.Raw, Schema: schemaResp.Schema}
	r.Open(ctx, request, &unavailableOpen)
	if !unavailableOpen.Diagnostics.HasError() {
		t.Fatal("expected unavailable token to fail")
	}

	// Register minted token
	cli.RegisterMintedToken("key-1", "pcp_board_value")

	var opened ephemeral.OpenResponse
	opened.Result = tfsdk.EphemeralResultData{Raw: tmpState.Raw, Schema: schemaResp.Schema}
	priv := initOpenResponsePrivate(&opened)
	r.Open(ctx, request, &opened)
	if opened.Diagnostics.HasError() {
		t.Fatalf("open failed: %v", opened.Diagnostics)
	}
	var result PaperclipBoardKeyEphemeralResourceModel
	if diags := opened.Result.Get(ctx, &result); diags.HasError() {
		t.Fatalf("read result failed: %v", diags)
	}
	if result.KeyID.ValueString() != "key-1" || result.Token.ValueString() != "pcp_board_value" {
		t.Fatalf("unexpected ephemeral result: %#v", result)
	}

	// Close marks delivery and revokes superseded key
	var closeReq ephemeral.CloseRequest
	initCloseRequestPrivate(&closeReq, priv)
	var closeResp ephemeral.CloseResponse
	r.Close(ctx, closeReq, &closeResp)
	if closeResp.Diagnostics.HasError() {
		t.Fatalf("close failed: %v", closeResp.Diagnostics)
	}
	if !cli.IsTokenDelivered("key-1") {
		t.Fatal("expected key-1 marked delivered after close")
	}

	// Re-opening after delivery returns unknown token without error
	var deliveredOpen ephemeral.OpenResponse
	deliveredOpen.Result = tfsdk.EphemeralResultData{Raw: tmpState.Raw, Schema: schemaResp.Schema}
	r.Open(ctx, request, &deliveredOpen)
	if deliveredOpen.Diagnostics.HasError() {
		t.Fatalf("delivered open failed: %v", deliveredOpen.Diagnostics)
	}
	var deliveredResult PaperclipBoardKeyEphemeralResourceModel
	if diags := deliveredOpen.Result.Get(ctx, &deliveredResult); diags.HasError() {
		t.Fatalf("read delivered result failed: %v", diags)
	}
	if !deliveredResult.Token.IsNull() {
		t.Fatalf("expected null token after delivery, got %v", deliveredResult.Token)
	}
}

func TestPaperclipAgentKeyEphemeralResource(t *testing.T) {
	t.Setenv("PAPERCLIP_SCRATCH_DIR", t.TempDir())
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasPrefix(req.URL.Path, "/api/agents/") && req.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	cli, _ := client.NewClient(server.URL, "board-token")

	r := NewPaperclipAgentKeyEphemeralResource().(*PaperclipAgentKeyEphemeralResource)
	var schemaResp ephemeral.SchemaResponse
	r.Schema(ctx, ephemeral.SchemaRequest{}, &schemaResp)
	model := PaperclipAgentKeyEphemeralResourceModel{
		KeyID: types.StringValue("key-1"),
	}
	tmpState := tfsdk.State{Schema: schemaResp.Schema}
	if diags := tmpState.Set(ctx, &model); diags.HasError() {
		t.Fatalf("failed to build ephemeral config: %v", diags)
	}
	request := ephemeral.OpenRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: tmpState.Raw}}
	newResponse := func() ephemeral.OpenResponse {
		return ephemeral.OpenResponse{
			Result: tfsdk.EphemeralResultData{Raw: tmpState.Raw, Schema: schemaResp.Schema},
		}
	}

	unconfigured := newResponse()
	r.Open(ctx, request, &unconfigured)
	if !unconfigured.Diagnostics.HasError() {
		t.Fatal("unconfigured ephemeral resource must fail")
	}

	var nilConfigure ephemeral.ConfigureResponse
	r.Configure(ctx, ephemeral.ConfigureRequest{}, &nilConfigure)
	if nilConfigure.Diagnostics.HasError() {
		t.Fatalf("nil provider data should be deferred: %v", nilConfigure.Diagnostics)
	}
	var invalidConfigure ephemeral.ConfigureResponse
	r.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: "invalid"}, &invalidConfigure)
	if !invalidConfigure.Diagnostics.HasError() {
		t.Fatal("invalid provider data must fail")
	}
	var configure ephemeral.ConfigureResponse
	r.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: cli}, &configure)
	if configure.Diagnostics.HasError() {
		t.Fatalf("configure failed: %v", configure.Diagnostics)
	}

	// Unavailable token fails with error diagnostic
	unavailableOpen := newResponse()
	r.Open(ctx, request, &unavailableOpen)
	if !unavailableOpen.Diagnostics.HasError() {
		t.Fatal("expected unavailable token to fail")
	}

	// Register minted token
	cli.RegisterMintedToken("key-1", "pcp_agent_value")

	opened := newResponse()
	priv := initOpenResponsePrivate(&opened)
	r.Open(ctx, request, &opened)
	if opened.Diagnostics.HasError() {
		t.Fatalf("open failed: %v", opened.Diagnostics)
	}
	var result PaperclipAgentKeyEphemeralResourceModel
	if diags := opened.Result.Get(ctx, &result); diags.HasError() {
		t.Fatalf("read result failed: %v", diags)
	}
	if result.KeyID.ValueString() != "key-1" || result.Token.ValueString() != "pcp_agent_value" {
		t.Fatalf("unexpected ephemeral result: %#v", result)
	}

	// Close marks delivery and revokes superseded key
	var closeReq ephemeral.CloseRequest
	initCloseRequestPrivate(&closeReq, priv)
	var closeResp ephemeral.CloseResponse
	r.Close(ctx, closeReq, &closeResp)
	if closeResp.Diagnostics.HasError() {
		t.Fatalf("close failed: %v", closeResp.Diagnostics)
	}
	if !cli.IsTokenDelivered("key-1") {
		t.Fatal("expected key-1 marked delivered after close")
	}

	// Re-opening after delivery returns unknown token without error
	deliveredOpen := newResponse()
	r.Open(ctx, request, &deliveredOpen)
	if deliveredOpen.Diagnostics.HasError() {
		t.Fatalf("delivered open failed: %v", deliveredOpen.Diagnostics)
	}
	var deliveredResult PaperclipAgentKeyEphemeralResourceModel
	if diags := deliveredOpen.Result.Get(ctx, &deliveredResult); diags.HasError() {
		t.Fatalf("read delivered result failed: %v", diags)
	}
	if !deliveredResult.Token.IsNull() {
		t.Fatalf("expected null token after delivery, got %v", deliveredResult.Token)
	}
}

func TestAgentStatusIsBoardOwned(t *testing.T) {
	ctx := context.Background()
	r := NewPaperclipAgentResource()
	var resp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &resp)
	attr, ok := resp.Schema.Attributes["status"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("status attribute missing or not a string attribute")
	}
	if attr.Optional || attr.Required || !attr.Computed {
		t.Fatalf("status must be computed-only board-owned state: %+v", attr)
	}
	if attr.Default != nil {
		t.Fatalf("status must not declare a provider-side default")
	}
}

func TestPaperclipCustomConnectionRequiresExistingApplication(t *testing.T) {
	ctx := context.Background()
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	r, connSchema := newConfiguredPaperclipConnectionResource(t, server.URL)
	var createReq resource.CreateRequest
	createReq.Plan = tfsdk.Plan{Schema: connSchema}
	createReq.Plan.Set(ctx, &PaperclipConnectionResourceModel{
		CompanyID: types.StringValue("comp-1"), AppID: types.StringNull(), Name: types.StringValue("custom"),
		Transport: types.StringValue("mcp_remote"), AuthKind: types.StringValue("none"),
		Config: types.StringValue(`{"url":"https://example.test/mcp"}`), CredentialRefs: types.StringUnknown(),
	})
	var createResp resource.CreateResponse
	createResp.State = tfsdk.State{Schema: connSchema}
	r.Create(ctx, createReq, &createResp)
	if !createResp.Diagnostics.HasError() || requests != 0 {
		t.Fatalf("a custom connection without an application was requested: %v requests=%d", createResp.Diagnostics, requests)
	}
}

func TestPaperclipGalleryConnectionIsConnectedAndAdoptedBySource(t *testing.T) {
	ctx := context.Background()
	record := map[string]interface{}{
		"id": "conn-gallery", "companyId": "comp-1", "applicationId": "app-gallery", "name": "Hugging Face",
		"transport": "mcp_remote", "authKind": "oauth", "status": "draft", "credentialPolicy": "shared",
		"config": map[string]interface{}{"sourceTemplateKey": "hugging-face", "connectionMethodKey": "mcp-oauth", "url": "https://huggingface.co/mcp?login&gradio=none"},
	}
	var connectBody map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/companies/comp-1/tools/apps/connect" && r.Method == http.MethodPost:
			_ = json.NewDecoder(r.Body).Decode(&connectBody)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"connectionId": "conn-gallery", "connection": record,
				"application": map[string]interface{}{"id": "app-gallery", "applicationKey": "app-gallery:hugging-face:random"},
				"auth":        map[string]interface{}{"kind": "oauth", "startUrl": "https://huggingface.co/oauth/authorize"},
			})
		case r.URL.Path == "/api/tool-connections/conn-gallery" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(record)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	r, connSchema := newConfiguredPaperclipConnectionResource(t, server.URL)
	var createReq resource.CreateRequest
	createReq.Plan = tfsdk.Plan{Schema: connSchema}
	createReq.Plan.Set(ctx, &PaperclipConnectionResourceModel{
		CompanyID: types.StringValue("comp-1"), AppID: types.StringUnknown(), Name: types.StringUnknown(),
		Transport: types.StringValue("mcp_remote"), AuthKind: types.StringValue("oauth"),
		Config: types.StringUnknown(), CredentialRefs: types.StringUnknown(),
		CredentialPolicy: types.StringValue("shared"), GalleryKey: types.StringValue("hugging-face"),
	})
	var createResp resource.CreateResponse
	createResp.State = tfsdk.State{Schema: connSchema}
	r.Create(ctx, createReq, &createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("gallery connect failed: %v", createResp.Diagnostics)
	}
	// Paperclip's gallery definition owns the application, name and configuration.
	if !reflect.DeepEqual(connectBody, map[string]interface{}{"galleryKey": "hugging-face"}) {
		t.Fatalf("gallery connect sent more than its gallery key: %v", connectBody)
	}
	var created PaperclipConnectionResourceModel
	createResp.State.Get(ctx, &created)
	if created.ID.ValueString() != "conn-gallery" || created.AppID.ValueString() != "app-gallery" || created.Name.ValueString() != "Hugging Face" || !strings.Contains(created.Config.ValueString(), `"sourceTemplateKey":"hugging-face"`) {
		t.Fatalf("gallery connection was not recorded from Paperclip: %+v", created)
	}
	// An adopted gallery connection reads its declared gallery key back.
	imported := tfsdk.State{Schema: connSchema}
	imported.Set(ctx, &PaperclipConnectionResourceModel{
		ID: types.StringValue("conn-gallery"), CompanyID: types.StringValue("comp-1"), AppID: types.StringNull(),
		Name: types.StringNull(), Transport: types.StringNull(), AuthKind: types.StringNull(), Config: types.StringNull(),
		CredentialRefs: types.StringNull(), CredentialPolicy: types.StringNull(), GalleryKey: types.StringNull(),
	})
	var readResp resource.ReadResponse
	readResp.State = imported
	r.Read(ctx, resource.ReadRequest{State: imported}, &readResp)
	var read PaperclipConnectionResourceModel
	readResp.State.Get(ctx, &read)
	if readResp.Diagnostics.HasError() || read.GalleryKey.ValueString() != "hugging-face" || read.AppID.ValueString() != "app-gallery" {
		t.Fatalf("adopted gallery connection did not read its source: %v %+v", readResp.Diagnostics, read)
	}
}

func TestAgentSchemaIdentityAttributes(t *testing.T) {
	ctx := context.Background()
	r := NewPaperclipAgentResource()
	var resp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &resp)

	companyIDAttr, ok := resp.Schema.Attributes["company_id"].(schema.StringAttribute)
	if !ok || !companyIDAttr.Required {
		t.Fatalf("company_id must be required: %+v", companyIDAttr)
	}

	nameAttr, ok := resp.Schema.Attributes["name"].(schema.StringAttribute)
	if !ok || !nameAttr.Required {
		t.Fatalf("name must be required: %+v", nameAttr)
	}

	for _, field := range []string{"role", "adapter_type", "adapter_config"} {
		attr, ok := resp.Schema.Attributes[field].(schema.StringAttribute)
		if !ok {
			t.Fatalf("%s attribute missing or not a string attribute", field)
		}
		if attr.Required || !attr.Optional || !attr.Computed {
			t.Fatalf("%s must be optional and computed to allow identity-only declarations: %+v", field, attr)
		}
	}
	for _, field := range []string{"title", "runtime_config", "metadata"} {
		attr, ok := resp.Schema.Attributes[field].(schema.StringAttribute)
		if !ok {
			t.Fatalf("%s attribute missing or not a string attribute", field)
		}
		if attr.Required || !attr.Optional || attr.Computed {
			t.Fatalf("%s must be optional and non-computed to allow explicit clearing: %+v", field, attr)
		}
	}

	assignAttr, ok := resp.Schema.Attributes["can_assign_tasks"].(schema.BoolAttribute)
	if !ok {
		t.Fatalf("can_assign_tasks attribute missing or not a bool attribute")
	}
	if !assignAttr.Required || assignAttr.Optional || assignAttr.Computed {
		t.Fatalf("can_assign_tasks must be declared for every managed agent: %+v", assignAttr)
	}
	for _, term := range []string{
		"tasks:assign",
		"protected project assignment policies",
		"not the creation of unassigned issues",
	} {
		if !strings.Contains(assignAttr.Description, term) {
			t.Errorf("can_assign_tasks description must contain %q, got: %s", term, assignAttr.Description)
		}
	}
	if strings.Contains(assignAttr.Description, "create and assign") {
		t.Errorf("can_assign_tasks description must not claim agent may create tasks: %s", assignAttr.Description)
	}

	createAttr, ok := resp.Schema.Attributes["can_create_agents"].(schema.BoolAttribute)
	if !ok {
		t.Fatalf("can_create_agents attribute missing or not a bool attribute")
	}
	if !createAttr.Required || createAttr.Optional || createAttr.Computed {
		t.Fatalf("can_create_agents must be declared for every managed agent: %+v", createAttr)
	}
}

// permissionBoard serves one agent whose permissions the test controls and
// records every permissions write the provider sends.
func permissionBoard(t *testing.T, permissions map[string]interface{}, applyWrites bool) (*PaperclipAgentResource, *[]map[string]bool) {
	t.Helper()
	var writes []map[string]bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case req.URL.Path == "/api/agents/agent-1" && req.Method == http.MethodGet:
			agent := map[string]interface{}{"id": "agent-1", "companyId": "comp-1", "name": "inventory"}
			if permissions != nil {
				agent["permissions"] = permissions
			}
			_ = json.NewEncoder(w).Encode(agent)
		case req.URL.Path == "/api/agents/agent-1/permissions" && req.Method == http.MethodPatch:
			var body map[string]bool
			_ = json.NewDecoder(req.Body).Decode(&body)
			writes = append(writes, body)
			if applyWrites {
				for flag, value := range body {
					permissions[flag] = value
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "agent-1"})
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(server.Close)
	cli, _ := client.NewClient(server.URL, "board-token")
	return &PaperclipAgentResource{client: cli}, &writes
}

func TestAgentPermissionReconciliation(t *testing.T) {
	ctx := context.Background()
	// The board state inventory held on 2026-09-30: created with the
	// create-agents permission that no managed agent declares.
	r, writes := permissionBoard(t, map[string]interface{}{"canCreateAgents": true, "canCreateSkills": true, "canAssignTasks": true}, true)

	// Declared values that differ are written once, carrying the undeclared flag
	// back unchanged because the route replaces all of them, and proven by a read.
	expected := []map[string]bool{{"canCreateAgents": false, "canCreateSkills": true, "canAssignTasks": true}}
	if err := r.reconcilePermissions(ctx, "agent-1", true, false, true, "", nil); err != nil || !reflect.DeepEqual(*writes, expected) {
		t.Fatalf("drifted create-agents flag must be written once with the other flags preserved: err %v writes %v", err, *writes)
	}
	// Declared values the board already reports write nothing.
	if err := r.reconcilePermissions(ctx, "agent-1", true, false, true, "", nil); err != nil || len(*writes) != 1 {
		t.Fatalf("converged values must not be rewritten: err %v writes %v", err, *writes)
	}
	// A changed task-assignment declaration keeps the declared create-agents value.
	expected = append(expected, map[string]bool{"canCreateAgents": false, "canCreateSkills": true, "canAssignTasks": false})
	if err := r.reconcilePermissions(ctx, "agent-1", false, false, true, "", nil); err != nil || !reflect.DeepEqual(*writes, expected) {
		t.Fatalf("task assignment must be written with the declared create-agents value: err %v writes %v", err, *writes)
	}
}

func TestAgentPermissionReconciliationRefusesUnprovenState(t *testing.T) {
	ctx := context.Background()
	t.Run("board reports no permissions", func(t *testing.T) {
		r, writes := permissionBoard(t, nil, true)
		err := r.reconcilePermissions(ctx, "agent-1", true, false, true, "", nil)
		if err == nil || !strings.Contains(err.Error(), "board reported no permissions for agent agent-1") || len(*writes) != 0 {
			t.Fatalf("absent permissions must fail without a write: err %v writes %v", err, *writes)
		}
	})
	t.Run("board ignores the write", func(t *testing.T) {
		r, writes := permissionBoard(t, map[string]interface{}{"canCreateAgents": true, "canCreateSkills": true, "canAssignTasks": true}, false)
		err := r.reconcilePermissions(ctx, "agent-1", true, false, true, "", nil)
		if err == nil || !strings.Contains(err.Error(), "agent permissions did not converge: declared can_assign_tasks true and can_create_agents false") || len(*writes) != 1 {
			t.Fatalf("an unconverged write must fail after one attempt: err %v writes %v", err, *writes)
		}
	})
}

func TestResultValueNormalization(t *testing.T) {
	if !stringOrNull("").IsNull() {
		t.Fatalf("empty API string must normalize to null")
	}
	if got := stringOrNull("idle"); got.IsNull() || got.IsUnknown() || got.ValueString() != "idle" {
		t.Fatalf("non-empty API string must normalize to a known value, got %v", got)
	}
	if !jsonStringOrNull(nil).IsNull() {
		t.Fatalf("absent API object must normalize to null")
	}
	if got := jsonStringOrNull(map[string]interface{}{"a": float64(1)}); got.IsNull() || got.IsUnknown() || got.ValueString() != `{"a":1}` {
		t.Fatalf("API object must normalize to its JSON encoding, got %v", got)
	}
	if !jsonStringOrNull(map[string]interface{}{"f": func() {}}).IsNull() {
		t.Fatalf("unencodable API object must normalize to null")
	}
}

func TestSkillExactContentAndDescriptionRefresh(t *testing.T) {
	if got := stringOrNull(""); !got.IsNull() {
		t.Fatalf("empty string must normalize to null, got: %#v", got)
	}
	if got := stringOrNull("desc"); got.ValueString() != "desc" {
		t.Fatalf("non-empty string must normalize to value, got: %#v", got)
	}
	skill := client.PaperclipSkill{
		Name:        "skill-1",
		Content:     "# Exact content",
		Description: "remote desc",
	}
	if skill.Content != "# Exact content" {
		t.Fatalf("unexpected content: %s", skill.Content)
	}
}

func TestProjectExecutionWorkspacePolicyParsing(t *testing.T) {
	policy, err := nativeJSONObject(types.StringValue(`{"enabled":true,"defaultMode":"shared_workspace"}`))
	if err != nil || policy["defaultMode"] != "shared_workspace" {
		t.Fatalf("valid project workspace policy did not parse: policy=%v err=%v", policy, err)
	}
	policy, err = nativeJSONObject(types.StringNull())
	if err != nil || policy != nil {
		t.Fatalf("null project workspace policy must remain absent: policy=%v err=%v", policy, err)
	}
	if _, err := nativeJSONObject(types.StringValue(`{"enabled":`)); err == nil {
		t.Fatalf("invalid project workspace policy JSON must fail")
	}
	if _, err := nativeJSONObject(types.StringValue("null")); err == nil {
		t.Fatalf("JSON null is not a project workspace policy object")
	}
}

func TestPaperclipResourceStateNormalizesOmittedOptionalFields(t *testing.T) {
	appState := PaperclipAppResourceModel{
		Type:           types.StringValue("mcp_http"),
		ApplicationKey: types.StringUnknown(),
		Description:    types.StringUnknown(), Metadata: types.StringUnknown(),
	}
	normalizePaperclipAppState(&appState, &client.PaperclipToolApp{
		ID: "app-1", CompanyID: "company-1", Name: "committer",
	})
	if !appState.ApplicationKey.IsNull() || !appState.Description.IsNull() || !appState.Metadata.IsNull() {
		t.Fatalf("omitted application fields must normalize to known nulls: %+v", appState)
	}
	if appState.Type.ValueString() != "mcp_http" {
		t.Fatalf("normalization must preserve the configured application type: %+v", appState)
	}

	secretState := PaperclipSecretResourceModel{Description: types.StringUnknown()}
	normalizePaperclipSecretState(&secretState, &client.PaperclipSecret{
		ID: "secret-1", CompanyID: "company-1", Name: "committer-token",
	})
	if !secretState.Description.IsNull() {
		t.Fatalf("omitted secret description must normalize to a known null: %+v", secretState)
	}
	if !secretState.Value.IsNull() {
		t.Fatalf("secret value must be absent from state: %+v", secretState)
	}
}

func TestPaperclipSecretRotationSchemaIsConfidentialAndObservable(t *testing.T) {
	ctx := context.Background()
	r := NewPaperclipSecretResource()
	var resp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &resp)

	value := resp.Schema.Attributes["value"].(schema.StringAttribute)
	if !value.Optional || !value.Sensitive || !value.WriteOnly || value.Computed || value.Required {
		t.Fatalf("value must be optional, sensitive, and write-only: %+v", value)
	}
	rotation := resp.Schema.Attributes["rotation"].(schema.StringAttribute)
	if !rotation.Optional || rotation.Sensitive || rotation.WriteOnly || !rotation.Computed || rotation.Required || len(rotation.PlanModifiers) != 1 {
		t.Fatalf("rotation must be optional computed durable non-secret state: %+v", rotation)
	}
}

func TestPaperclipSecretRotationValueIsSentExactlyOnce(t *testing.T) {
	state := PaperclipSecretResourceModel{Rotation: types.StringValue("revision-1")}
	config := PaperclipSecretResourceModel{Rotation: types.StringValue("revision-2"), Value: types.StringValue("protected")}
	unchanged := PaperclipSecretResourceModel{Rotation: types.StringValue("revision-1")}
	changed := PaperclipSecretResourceModel{Rotation: types.StringValue("revision-2")}
	if got := secretRotationValue(config, unchanged, state); got != "" {
		t.Fatal("unchanged rotation must not resend the value")
	}
	if got := secretRotationValue(config, changed, state); got != "protected" {
		t.Fatal("changed rotation must send the write-only value exactly once")
	}
}

func TestPaperclipSecretCreateAndRotationReadValueFromConfig(t *testing.T) {
	ctx := context.Background()
	values := []string{}
	metadataUpdates := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var payload map[string]interface{}
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/api/companies/company-1/secrets":
			value, ok := payload["value"].(string)
			if !ok {
				t.Fatal("secret create omitted write-only config value")
			}
			values = append(values, value)
		case req.Method == http.MethodPost && req.URL.Path == "/api/secrets/secret-1/rotate":
			value, ok := payload["value"].(string)
			if !ok || len(payload) != 1 {
				t.Fatalf("secret rotation must carry only the write-only value: %#v", payload)
			}
			values = append(values, value)
			payload["name"] = "committer-token"
			payload["description"] = "managed token"
		case req.Method == http.MethodPatch && req.URL.Path == "/api/secrets/secret-1":
			if _, present := payload["value"]; present {
				t.Fatal("secret metadata update must not carry a value")
			}
			metadataUpdates++
		default:
			t.Fatalf("unexpected secret request: %s %s", req.Method, req.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id": "secret-1", "companyId": "company-1", "name": payload["name"], "description": payload["description"],
		})
	}))
	defer server.Close()

	cli, err := client.NewClient(server.URL, "test-token")
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	secretResource := NewPaperclipSecretResource().(*PaperclipSecretResource)
	secretResource.client = cli
	var schemaResp resource.SchemaResponse
	secretResource.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	configModel := PaperclipSecretResourceModel{
		CompanyID: types.StringValue("company-1"), Name: types.StringValue("committer-token"),
		Description: types.StringValue("managed token"), Value: types.StringValue("initial-protected"), Rotation: types.StringValue("revision-1"),
	}
	planModel := configModel
	planModel.Value = types.StringNull()
	configState := tfsdk.State{Schema: schemaResp.Schema}
	plan := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = configState.Set(ctx, &configModel)
	config := tfsdk.Config{Schema: schemaResp.Schema, Raw: configState.Raw}
	_ = plan.Set(ctx, &planModel)
	createResp := resource.CreateResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	secretResource.Create(ctx, resource.CreateRequest{Config: config, Plan: plan}, &createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("create failed: %v", createResp.Diagnostics)
	}

	var createdState PaperclipSecretResourceModel
	_ = createResp.State.Get(ctx, &createdState)
	if !createdState.Value.IsNull() {
		t.Fatal("write-only value persisted after create")
	}
	configModel.Value = types.StringValue("rotated-protected")
	configModel.Rotation = types.StringValue("revision-2")
	planModel = configModel
	planModel.ID = types.StringValue("secret-1")
	planModel.Value = types.StringNull()
	configState = tfsdk.State{Schema: schemaResp.Schema}
	plan = tfsdk.Plan{Schema: schemaResp.Schema}
	_ = configState.Set(ctx, &configModel)
	config = tfsdk.Config{Schema: schemaResp.Schema, Raw: configState.Raw}
	_ = plan.Set(ctx, &planModel)
	updateResp := resource.UpdateResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	secretResource.Update(ctx, resource.UpdateRequest{Config: config, Plan: plan, State: createResp.State}, &updateResp)
	if updateResp.Diagnostics.HasError() {
		t.Fatalf("update failed: %v", updateResp.Diagnostics)
	}
	if !reflect.DeepEqual(values, []string{"initial-protected", "rotated-protected"}) {
		t.Fatalf("Paperclip received %d unexpected secret values", len(values))
	}
	var updatedState PaperclipSecretResourceModel
	_ = updateResp.State.Get(ctx, &updatedState)
	if !updatedState.Value.IsNull() {
		t.Fatal("write-only value persisted after update")
	}

	configModel.Description = types.StringValue("updated metadata")
	planModel = configModel
	planModel.ID = types.StringValue("secret-1")
	planModel.Value = types.StringNull()
	configState = tfsdk.State{Schema: schemaResp.Schema}
	plan = tfsdk.Plan{Schema: schemaResp.Schema}
	_ = configState.Set(ctx, &configModel)
	config = tfsdk.Config{Schema: schemaResp.Schema, Raw: configState.Raw}
	_ = plan.Set(ctx, &planModel)
	metadataResp := resource.UpdateResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	secretResource.Update(ctx, resource.UpdateRequest{Config: config, Plan: plan, State: updateResp.State}, &metadataResp)
	if metadataResp.Diagnostics.HasError() {
		t.Fatalf("metadata update failed: %v", metadataResp.Diagnostics)
	}
	if metadataUpdates != 1 {
		t.Fatalf("expected exactly one metadata PATCH, got %d", metadataUpdates)
	}
	if !reflect.DeepEqual(values, []string{"initial-protected", "rotated-protected"}) {
		t.Fatalf("metadata update resent a secret value: %#v", values)
	}
}

func TestPaperclipSecretRotationValidationFailsClosed(t *testing.T) {
	tests := map[string]struct {
		model      PaperclipSecretResourceModel
		errorCount int
	}{
		"operator managed":               {model: PaperclipSecretResourceModel{}, errorCount: 0},
		"complete rotation":              {model: PaperclipSecretResourceModel{Value: types.StringValue("protected"), Rotation: types.StringValue("revision-1")}, errorCount: 0},
		"unknown value":                  {model: PaperclipSecretResourceModel{Value: types.StringUnknown(), Rotation: types.StringValue("revision-1")}, errorCount: 0},
		"unknown rotation":               {model: PaperclipSecretResourceModel{Value: types.StringValue("protected"), Rotation: types.StringUnknown()}, errorCount: 0},
		"unknown value without rotation": {model: PaperclipSecretResourceModel{Value: types.StringUnknown()}, errorCount: 1},
		"unknown rotation without value": {model: PaperclipSecretResourceModel{Rotation: types.StringUnknown()}, errorCount: 1},
		"both unknown":                   {model: PaperclipSecretResourceModel{Value: types.StringUnknown(), Rotation: types.StringUnknown()}, errorCount: 0},
		"missing rotation":               {model: PaperclipSecretResourceModel{Value: types.StringValue("protected")}, errorCount: 1},
		"missing value":                  {model: PaperclipSecretResourceModel{Rotation: types.StringValue("revision-1")}, errorCount: 1},
		"empty declaration":              {model: PaperclipSecretResourceModel{Value: types.StringValue(""), Rotation: types.StringValue("")}, errorCount: 2},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if got := len(validateSecretRotation(test.model)); got != test.errorCount {
				t.Fatalf("expected %d validation errors, got %d", test.errorCount, got)
			}
		})
	}
}

func TestPaperclipResourceStateRefreshesExactRemoteState(t *testing.T) {
	appState := PaperclipAppResourceModel{
		ApplicationKey: types.StringValue("committer"),
		Description:    types.StringValue("generated application"),
		Metadata:       types.StringValue(`{"managed":true}`),
	}
	normalizePaperclipAppState(&appState, &client.PaperclipToolApp{ID: "app-1", Name: "committer"})
	if !appState.ApplicationKey.IsNull() || !appState.Description.IsNull() || !appState.Metadata.IsNull() {
		t.Fatalf("omissive application response must refresh cleared fields to null: %+v", appState)
	}

	appStateWithRemote := PaperclipAppResourceModel{}
	normalizePaperclipAppState(&appStateWithRemote, &client.PaperclipToolApp{
		ID:             "app-1",
		Name:           "committer",
		ApplicationKey: "app-key",
		Description:    "app-desc",
		Metadata:       map[string]interface{}{"managed": true},
	})
	if appStateWithRemote.ApplicationKey.ValueString() != "app-key" || appStateWithRemote.Description.ValueString() != "app-desc" || appStateWithRemote.Metadata.IsNull() {
		t.Fatalf("remote values must refresh into state: %+v", appStateWithRemote)
	}

	secretState := PaperclipSecretResourceModel{Description: types.StringValue("operator token")}
	normalizePaperclipSecretState(&secretState, &client.PaperclipSecret{ID: "secret-1", Name: "committer-token"})
	if !secretState.Description.IsNull() {
		t.Fatalf("omissive secret response must refresh cleared description to null: %+v", secretState)
	}

	secretStateWithDesc := PaperclipSecretResourceModel{}
	normalizePaperclipSecretState(&secretStateWithDesc, &client.PaperclipSecret{ID: "secret-1", Name: "committer-token", Description: "operator token"})
	if secretStateWithDesc.Description.ValueString() != "operator token" {
		t.Fatalf("remote description must refresh into state: %+v", secretStateWithDesc)
	}
}

func TestApplicationCRUDRefreshesRemoteKey(t *testing.T) {
	returnAppKey := true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if req.Method == http.MethodGet && req.URL.Path != "/api/companies/comp-123/tools/applications" {
			t.Errorf("unexpected application read: %s", req.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if req.Method == http.MethodPost || req.Method == http.MethodPatch {
			var body client.PaperclipToolApp
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Fatalf("decode application request: %v", err)
			}
			if body.ApplicationKey != "harness.mcp.gitea-committer" {
				t.Fatalf("configured application key was not sent to Paperclip: %#v", body)
			}
		}
		resp := map[string]interface{}{
			"id":        "app-123",
			"companyId": "comp-123",
			"name":      "gitea-committer",
		}
		if returnAppKey {
			resp["applicationKey"] = "harness.mcp.gitea-committer"
		}
		if req.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"applications": []map[string]interface{}{resp}})
		} else {
			_ = json.NewEncoder(w).Encode(resp)
		}
	}))
	defer server.Close()
	cli, err := client.NewClient(server.URL, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	r := &PaperclipAppResource{client: cli}
	configured := types.StringValue("harness.mcp.gitea-committer")
	model := PaperclipAppResourceModel{
		ID: types.StringValue("app-123"), CompanyID: types.StringValue("comp-123"),
		Name: types.StringValue("gitea-committer"), Type: types.StringValue("mcp_http"),
		ApplicationKey: configured,
		Description:    types.StringNull(), Metadata: types.StringNull(),
	}
	operations := map[string]func() (PaperclipAppResourceModel, error){
		"create": func() (PaperclipAppResourceModel, error) { return r.createAppState(context.Background(), model) },
		"read":   func() (PaperclipAppResourceModel, error) { return r.readAppState(context.Background(), model) },
		"update": func() (PaperclipAppResourceModel, error) { return r.updateAppState(context.Background(), model, model) },
	}
	for name, operation := range operations {
		t.Run(name, func(t *testing.T) {
			got, err := operation()
			if err != nil {
				t.Fatal(err)
			}
			if got.ApplicationKey.IsNull() || got.ApplicationKey.IsUnknown() || got.ApplicationKey.ValueString() != configured.ValueString() {
				t.Fatalf("configured application key was not retained as known state: %#v", got.ApplicationKey)
			}
			if !got.Metadata.IsNull() || got.Metadata.IsUnknown() {
				t.Fatalf("unset metadata must remain known null: %#v", got.Metadata)
			}
		})
	}

	returnAppKey = false
	got, err := r.readAppState(context.Background(), model)
	if err != nil {
		t.Fatal(err)
	}
	if !got.ApplicationKey.IsNull() {
		t.Fatalf("expected omitted application key to refresh to null, got: %#v", got.ApplicationKey)
	}
}

func TestApplicationKeyState(t *testing.T) {
	if got := stringOrNull("paperclip.canonical-key"); got.ValueString() != "paperclip.canonical-key" {
		t.Fatalf("returned application key must normalize to value, got %#v", got)
	}
	if got := stringOrNull(""); !got.IsNull() {
		t.Fatalf("omitted optional application key must normalize to known null: %#v", got)
	}
}

func TestRefreshManagedJSONState(t *testing.T) {
	remote := map[string]interface{}{
		"heartbeat": map[string]interface{}{
			"maxConcurrentRuns": float64(2),
			"boardDefault":      true,
		},
		"boardOwned": "preserved",
	}
	managed := types.StringValue(`{"heartbeat":{"maxConcurrentRuns":2}}`)
	if got := refreshManagedJSONState(managed, remote); got != managed {
		t.Fatalf("expected board-enriched remote JSON to preserve managed state, got %#v", got)
	}

	changed := types.StringValue(`{"heartbeat":{"maxConcurrentRuns":1}}`)
	if got := refreshManagedJSONState(changed, remote); got.ValueString() == changed.ValueString() {
		t.Fatal("expected a changed managed value to refresh from the remote object")
	}
	if got := refreshManagedJSONState(types.StringValue("not-json"), remote); got.IsNull() || got.IsUnknown() {
		t.Fatal("expected invalid prior state to refresh from the remote object")
	}
	if got := refreshManagedJSONState(types.StringNull(), remote); got.IsNull() || got.IsUnknown() {
		t.Fatal("expected null prior state to adopt the remote object")
	}
	if got := refreshManagedJSONState(types.StringUnknown(), remote); got.IsNull() || got.IsUnknown() {
		t.Fatal("expected unknown prior state to adopt the remote object")
	}
	if got := refreshManagedJSONState(managed, nil); !got.IsNull() {
		t.Fatalf("expected an absent remote object to refresh to null, got %#v", got)
	}
	if jsonContains([]interface{}{float64(1)}, map[string]interface{}{}) {
		t.Fatal("expected mismatched JSON kinds not to compare as a managed subset")
	}
	if !jsonContains([]interface{}{float64(1)}, []interface{}{float64(1)}) {
		t.Fatal("expected equal JSON arrays to compare exactly")
	}
	if jsonContains(map[string]interface{}{}, map[string]interface{}{"missing": true}) {
		t.Fatal("expected a missing managed key to fail subset comparison")
	}
}

func TestPaperclipAppSchema(t *testing.T) {
	ctx := context.Background()
	r := NewPaperclipAppResource()
	var resp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &resp)
	attr, ok := resp.Schema.Attributes["type"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("type attribute missing or not a string attribute")
	}
	if !attr.Required {
		t.Fatalf("type attribute must be required")
	}
	if len(attr.Validators) == 0 {
		t.Fatalf("type attribute must carry the allowed-value validator")
	}
	if len(attr.PlanModifiers) == 0 {
		t.Fatalf("type attribute must require replacement on change")
	}
}

func TestToolAppTypeValidator(t *testing.T) {
	ctx := context.Background()
	v := toolAppTypeValidator{}
	if v.Description(ctx) == "" || v.MarkdownDescription(ctx) == "" {
		t.Fatal("validator must describe the allowed set")
	}

	check := func(value types.String) validator.StringResponse {
		var resp validator.StringResponse
		v.ValidateString(ctx, validator.StringRequest{
			Path:        path.Root("type"),
			ConfigValue: value,
		}, &resp)
		return resp
	}

	if resp := check(types.StringValue("mcp_http")); resp.Diagnostics.HasError() {
		t.Fatalf("mcp_http must validate, got: %v", resp.Diagnostics)
	}
	if resp := check(types.StringValue("mcp_stdio")); resp.Diagnostics.HasError() {
		t.Fatalf("mcp_stdio must validate, got: %v", resp.Diagnostics)
	}
	if resp := check(types.StringValue("invalid_type")); !resp.Diagnostics.HasError() {
		t.Fatal("invalid type must fail validation")
	}
	if resp := check(types.StringValue("")); !resp.Diagnostics.HasError() {
		t.Fatal("empty type must fail validation")
	}
	if resp := check(types.StringNull()); resp.Diagnostics.HasError() {
		t.Fatal("null value is left to Required handling, not the validator")
	}
	if resp := check(types.StringUnknown()); resp.Diagnostics.HasError() {
		t.Fatal("unknown value is left to later plan phases, not the validator")
	}
}

func TestPaperclipAppLifecycle(t *testing.T) {
	ctx := context.Background()
	var createdPayload map[string]interface{}
	var updatedPayload map[string]interface{}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/companies/comp-1/tools/applications" && r.Method == http.MethodPost:
			_ = json.NewDecoder(r.Body).Decode(&createdPayload)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":             "app-456",
				"companyId":      "comp-1",
				"name":           createdPayload["name"],
				"type":           createdPayload["type"],
				"applicationKey": createdPayload["applicationKey"],
			})
		case r.URL.Path == "/api/companies/comp-1/tools/applications" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"applications": []map[string]interface{}{{
				"id":             "app-456",
				"companyId":      "comp-1",
				"name":           "harness.mcp.gitea-committer",
				"type":           "mcp_http",
				"applicationKey": "harness.mcp.gitea-committer",
			}}})
		case r.URL.Path == "/api/tool-applications/app-456" && r.Method == http.MethodPatch:
			_ = json.NewDecoder(r.Body).Decode(&updatedPayload)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":             "app-456",
				"companyId":      "comp-1",
				"name":           updatedPayload["name"],
				"type":           updatedPayload["type"],
				"applicationKey": updatedPayload["applicationKey"],
			})
		case r.URL.Path == "/api/tool-applications/app-456" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	cli, err := client.NewClient(server.URL, "test-token")
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}

	r := NewPaperclipAppResource().(*PaperclipAppResource)
	var cfgResp resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{
		ProviderData: cli,
	}, &cfgResp)
	if cfgResp.Diagnostics.HasError() {
		t.Fatalf("Configure failed: %v", cfgResp.Diagnostics)
	}

	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	// 1. Create with mcp_http
	planModel := PaperclipAppResourceModel{
		CompanyID:      types.StringValue("comp-1"),
		Name:           types.StringValue("harness.mcp.gitea-committer"),
		Type:           types.StringValue("mcp_http"),
		ApplicationKey: types.StringValue("harness.mcp.gitea-committer"),
	}
	var createReq resource.CreateRequest
	createReq.Plan = tfsdk.Plan{Schema: schemaResp.Schema}
	createReq.Plan.Set(ctx, &planModel)

	var createResp resource.CreateResponse
	createResp.State = tfsdk.State{Schema: schemaResp.Schema}

	r.Create(ctx, createReq, &createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("Create failed: %v", createResp.Diagnostics)
	}
	if createdPayload["type"] != "mcp_http" {
		t.Fatalf("expected create payload to contain type mcp_http, got: %v", createdPayload["type"])
	}
	var stateModel PaperclipAppResourceModel
	createResp.State.Get(ctx, &stateModel)
	if stateModel.Type.ValueString() != "mcp_http" || stateModel.ID.ValueString() != "app-456" {
		t.Fatalf("expected state type mcp_http, got: %v", stateModel.Type)
	}
	validState := createResp.State

	// 2. Read
	var readReq resource.ReadRequest
	readReq.State = validState
	var readResp resource.ReadResponse
	readResp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Read(ctx, readReq, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("Read failed: %v", readResp.Diagnostics)
	}
	var readStateModel PaperclipAppResourceModel
	readResp.State.Get(ctx, &readStateModel)
	if readStateModel.Type.ValueString() != "mcp_http" {
		t.Fatalf("expected read state type mcp_http, got: %v", readStateModel.Type)
	}

	// 3. Update
	updatedPlan := PaperclipAppResourceModel{
		ID:        types.StringValue("app-456"),
		CompanyID: types.StringValue("comp-1"),
		Name:      types.StringValue("harness.mcp.gitea-committer-renamed"),
		Type:      types.StringValue("mcp_http"),
	}
	var updateReq resource.UpdateRequest
	updateReq.Plan = tfsdk.Plan{Schema: schemaResp.Schema}
	updateReq.Plan.Set(ctx, &updatedPlan)
	updateReq.State = validState
	var updateResp resource.UpdateResponse
	updateResp.State = tfsdk.State{Schema: schemaResp.Schema}

	r.Update(ctx, updateReq, &updateResp)
	if updateResp.Diagnostics.HasError() {
		t.Fatalf("Update failed: %v", updateResp.Diagnostics)
	}
	if updatedPayload["type"] != "mcp_http" {
		t.Fatalf("expected update payload to contain type mcp_http, got: %v", updatedPayload["type"])
	}
	validState = updateResp.State

	// 4. ImportState
	var importReq resource.ImportStateRequest
	importReq.ID = "comp-1/app-456"
	var importResp resource.ImportStateResponse
	importResp.State = tfsdk.State{Schema: schemaResp.Schema}
	importResp.State.Set(ctx, &PaperclipAppResourceModel{})
	r.ImportState(ctx, importReq, &importResp)
	if importResp.Diagnostics.HasError() {
		t.Fatalf("ImportState failed: %v", importResp.Diagnostics)
	}
	readReq.State = importResp.State
	readResp = resource.ReadResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	r.Read(ctx, readReq, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("Read after import failed: %v", readResp.Diagnostics)
	}
	var importedState PaperclipAppResourceModel
	readResp.State.Get(ctx, &importedState)
	if importedState.Type.ValueString() != "mcp_http" {
		t.Fatalf("expected imported state type mcp_http, got: %v", importedState.Type)
	}

	// 5. Validation: Missing type in Create
	badPlan := PaperclipAppResourceModel{
		CompanyID: types.StringValue("comp-1"),
		Name:      types.StringValue("bad-app"),
	}
	createReq.Plan.Set(ctx, &badPlan)
	var badCreateResp resource.CreateResponse
	badCreateResp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, createReq, &badCreateResp)
	if !badCreateResp.Diagnostics.HasError() {
		t.Fatal("expected error creating app with missing type")
	}

	// 6. Validation: Invalid type in Create
	badTypePlan := PaperclipAppResourceModel{
		CompanyID: types.StringValue("comp-1"),
		Name:      types.StringValue("bad-app"),
		Type:      types.StringValue("invalid_type"),
	}
	createReq.Plan.Set(ctx, &badTypePlan)
	badCreateResp = resource.CreateResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	r.Create(ctx, createReq, &badCreateResp)
	if !badCreateResp.Diagnostics.HasError() {
		t.Fatal("expected error creating app with invalid type")
	}

	// 7. Validation: Missing type in Update
	updateReq.Plan.Set(ctx, &badPlan)
	var badUpdateResp resource.UpdateResponse
	badUpdateResp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Update(ctx, updateReq, &badUpdateResp)
	if !badUpdateResp.Diagnostics.HasError() {
		t.Fatal("expected error updating app with missing type")
	}

	// 8. Validation: Invalid type in Update
	updateReq.Plan.Set(ctx, &badTypePlan)
	badUpdateResp = resource.UpdateResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	r.Update(ctx, updateReq, &badUpdateResp)
	if !badUpdateResp.Diagnostics.HasError() {
		t.Fatal("expected error updating app with invalid type")
	}

	// 9. Delete
	var delReq resource.DeleteRequest
	delReq.State = validState
	var delResp resource.DeleteResponse
	r.Delete(ctx, delReq, &delResp)
	if delResp.Diagnostics.HasError() {
		t.Fatalf("Delete failed: %v", delResp.Diagnostics)
	}
}

func newConfiguredPaperclipAppResource(t *testing.T, serverURL string) (*PaperclipAppResource, schema.Schema) {
	t.Helper()
	cli, err := client.NewClient(serverURL, "test-token")
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	r := NewPaperclipAppResource().(*PaperclipAppResource)
	var cfgResp resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: cli}, &cfgResp)
	if cfgResp.Diagnostics.HasError() {
		t.Fatalf("Configure failed: %v", cfgResp.Diagnostics)
	}
	var schemaResp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)
	return r, schemaResp.Schema
}

func TestPaperclipAppTypeNotOverwrittenByAPIEcho(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// The API echoes a normalized type that differs from the config value.
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":        "app-echo",
			"companyId": "comp-1",
			"name":      "harness.mcp.gitea-committer",
			"type":      "MCP_HTTP",
		})
	}))
	defer server.Close()

	r, appSchema := newConfiguredPaperclipAppResource(t, server.URL)

	planModel := PaperclipAppResourceModel{
		CompanyID: types.StringValue("comp-1"),
		Name:      types.StringValue("harness.mcp.gitea-committer"),
		Type:      types.StringValue("mcp_http"),
	}
	var createReq resource.CreateRequest
	createReq.Plan = tfsdk.Plan{Schema: appSchema}
	createReq.Plan.Set(ctx, &planModel)
	createResp := resource.CreateResponse{State: tfsdk.State{Schema: appSchema}}
	r.Create(ctx, createReq, &createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("Create failed: %v", createResp.Diagnostics)
	}
	var created PaperclipAppResourceModel
	createResp.State.Get(ctx, &created)
	if created.Type.ValueString() != "mcp_http" {
		t.Fatalf("create state must keep the planned type over the API echo, got %q", created.Type.ValueString())
	}

	updatePlan := planModel
	updatePlan.ID = types.StringValue("app-echo")
	var updateReq resource.UpdateRequest
	updateReq.Plan = tfsdk.Plan{Schema: appSchema}
	updateReq.Plan.Set(ctx, &updatePlan)
	updateReq.State = createResp.State
	updateResp := resource.UpdateResponse{State: tfsdk.State{Schema: appSchema}}
	r.Update(ctx, updateReq, &updateResp)
	if updateResp.Diagnostics.HasError() {
		t.Fatalf("Update failed: %v", updateResp.Diagnostics)
	}
	var updated PaperclipAppResourceModel
	updateResp.State.Get(ctx, &updated)
	if updated.Type.ValueString() != "mcp_http" {
		t.Fatalf("update state must keep the planned type over the API echo, got %q", updated.Type.ValueString())
	}
}

func TestPaperclipAppImportWithoutTypeFails(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/companies/comp-1/tools/applications":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"applications": []map[string]interface{}{{
				"id":        "app-null",
				"companyId": "comp-1",
				"name":      "harness.mcp.untyped",
			}}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	r, appSchema := newConfiguredPaperclipAppResource(t, server.URL)

	var importReq resource.ImportStateRequest
	importReq.ID = "comp-1/app-null"
	importResp := resource.ImportStateResponse{State: tfsdk.State{Schema: appSchema}}
	importResp.State.Set(ctx, &PaperclipAppResourceModel{})
	r.ImportState(ctx, importReq, &importResp)
	if importResp.Diagnostics.HasError() {
		t.Fatalf("ImportState failed: %v", importResp.Diagnostics)
	}

	var readReq resource.ReadRequest
	readReq.State = importResp.State
	readResp := resource.ReadResponse{State: tfsdk.State{Schema: appSchema}}
	r.Read(ctx, readReq, &readResp)
	if !readResp.Diagnostics.HasError() {
		t.Fatal("expected Read after import to fail when Paperclip never reports a type")
	}
}

func newConfiguredPaperclipConnectionResource(t *testing.T, serverURL string) (*PaperclipConnectionResource, schema.Schema) {
	t.Helper()
	cli, err := client.NewClient(serverURL, "test-token")
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	r := NewPaperclipConnectionResource().(*PaperclipConnectionResource)
	var cfgResp resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: cli}, &cfgResp)
	if cfgResp.Diagnostics.HasError() {
		t.Fatalf("Configure failed: %v", cfgResp.Diagnostics)
	}
	var schemaResp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)
	return r, schemaResp.Schema
}

func TestPaperclipConnectionLifecycle(t *testing.T) {
	ctx := context.Background()
	var createdPayload map[string]interface{}
	var updatedPayload map[string]interface{}
	failGrantRead := true
	connectionCreates := 0
	credentialRefs := []map[string]interface{}{{"key": "user", "name": "user", "secretId": "secret-1"}}
	secretRefs, _ := client.ConnectionCredentialSecretRefs(credentialRefs)
	grant := client.PaperclipConnectionGrant{ID: "grant-1", Kind: "organization", Status: "active", IsDefault: true, CredentialSecretRefs: secretRefs}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/tool-connections/conn-456/grants":
			if failGrantRead {
				http.Error(w, "fixture grant read failure", 500)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"grants": []client.PaperclipConnectionGrant{grant}})
		case r.URL.Path == "/api/companies/comp-1/tools/connections" && r.Method == http.MethodPost:
			connectionCreates++
			_ = json.NewDecoder(r.Body).Decode(&createdPayload)
			if createdPayload["applicationId"] != "app-123" {
				t.Errorf("expected applicationId in create payload to be app-123, got: %v", createdPayload["applicationId"])
			}
			if _, exists := createdPayload["appId"]; exists {
				t.Errorf("create payload sent obsolete appId: %v", createdPayload["appId"])
			}
			if createdPayload["transport"] != "local_stdio" || createdPayload["authKind"] != "basic" {
				t.Errorf("create payload omitted Paperclip transport/authKind: %v", createdPayload)
			}
			if _, exists := createdPayload["credentials"]; exists {
				t.Errorf("create payload sent obsolete credentials: %v", createdPayload)
			}
			if _, exists := createdPayload["credentialRefs"]; !exists {
				t.Errorf("create payload omitted credentialRefs: %v", createdPayload)
			}
			rawSecretRefs, exists := createdPayload["credentialSecretRefs"].([]interface{})
			if !exists || len(rawSecretRefs) != 0 {
				t.Fatalf("expected empty credentialSecretRefs, got: %v", createdPayload["credentialSecretRefs"])
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":            "conn-456",
				"companyId":     "comp-1",
				"applicationId": createdPayload["applicationId"],
				"name":          createdPayload["name"],
				"transport":     "local_stdio",
			})
		case r.URL.Path == "/api/tool-connections/conn-456" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":             "conn-456",
				"companyId":      "comp-1",
				"applicationId":  "app-123",
				"name":           "openobserve-connection",
				"transport":      "local_stdio",
				"authKind":       "basic",
				"config":         map[string]interface{}{"command": "oo"},
				"credentialRefs": credentialRefs,
			})
		case r.URL.Path == "/api/tool-connections/conn-456" && r.Method == http.MethodPatch:
			_ = json.NewDecoder(r.Body).Decode(&updatedPayload)
			if value, exists := updatedPayload["applicationId"]; exists {
				t.Errorf("connection update sent immutable applicationId: %v", value)
			}
			if value, exists := updatedPayload["appId"]; exists {
				t.Errorf("connection update sent obsolete appId: %v", value)
			}
			if _, exists := updatedPayload["credentials"]; exists {
				t.Errorf("connection update sent obsolete credentials: %v", updatedPayload)
			}
			if _, exists := updatedPayload["credentialRefs"]; !exists {
				t.Errorf("connection update omitted credentialRefs: %v", updatedPayload)
			}
			rawSecretRefs, exists := updatedPayload["credentialSecretRefs"].([]interface{})
			if !exists || len(rawSecretRefs) != 0 {
				t.Fatalf("expected empty credentialSecretRefs, got: %v", updatedPayload["credentialSecretRefs"])
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":        "conn-456",
				"companyId": "comp-1",
				"name":      updatedPayload["name"],
				"transport": "local_stdio",
			})
		case r.URL.Path == "/api/tool-connections/conn-456" && r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	r, connSchema := newConfiguredPaperclipConnectionResource(t, server.URL)

	// 1. Create
	planModel := PaperclipConnectionResourceModel{
		CompanyID:      types.StringValue("comp-1"),
		AppID:          types.StringValue("app-123"),
		Name:           types.StringValue("openobserve-connection"),
		Transport:      types.StringValue("local_stdio"),
		AuthKind:       types.StringValue("basic"),
		Config:         types.StringValue(`{ "command": "oo" }`),
		CredentialRefs: types.StringValue(`[{ "secretId": "secret-1", "name": "user", "key": "user" }]`),
	}
	var createReq resource.CreateRequest
	createReq.Plan = tfsdk.Plan{Schema: connSchema}
	createReq.Plan.Set(ctx, &planModel)

	var createResp resource.CreateResponse
	createResp.State = tfsdk.State{Schema: connSchema}

	r.Create(ctx, createReq, &createResp)
	if !createResp.Diagnostics.HasError() {
		t.Fatal("Create hid the post-create credential read failure")
	}
	var stateModel PaperclipConnectionResourceModel
	createResp.State.Get(ctx, &stateModel)
	if stateModel.AppID.ValueString() != "app-123" || stateModel.ID.ValueString() != "conn-456" {
		t.Fatalf("expected state app_id app-123, got: %v", stateModel.AppID)
	}
	failGrantRead = false
	var retried resource.UpdateResponse
	retried.State = createResp.State
	r.Update(ctx, resource.UpdateRequest{State: createResp.State, Plan: tfsdk.Plan{Schema: connSchema, Raw: createResp.State.Raw}}, &retried)
	if retried.Diagnostics.HasError() || connectionCreates != 1 {
		t.Fatal("failed Create did not retain its remote identity for reconciliation")
	}
	validState := retried.State

	// 2. Read
	var readReq resource.ReadRequest
	readReq.State = validState
	var readResp resource.ReadResponse
	readResp.State = tfsdk.State{Schema: connSchema}
	r.Read(ctx, readReq, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("Read failed: %v", readResp.Diagnostics)
	}
	var readStateModel PaperclipConnectionResourceModel
	readResp.State.Get(ctx, &readStateModel)
	if readStateModel.AppID.ValueString() != "app-123" {
		t.Fatalf("expected read state app_id app-123, got: %v", readStateModel.AppID)
	}
	if readStateModel.Config != planModel.Config || readStateModel.CredentialRefs != planModel.CredentialRefs || !readStateModel.CredentialGrantReady.ValueBool() {
		t.Fatal("equivalent native JSON or credential grant caused false drift")
	}
	grant.CredentialSecretRefs = []map[string]interface{}{}
	var driftRead resource.ReadResponse
	driftRead.State = readResp.State
	r.Read(ctx, resource.ReadRequest{State: readResp.State}, &driftRead)
	var driftModel PaperclipConnectionResourceModel
	driftRead.State.Get(ctx, &driftModel)
	if driftRead.Diagnostics.HasError() || driftModel.CredentialGrantReady.ValueBool() {
		t.Fatal("missing execution credentials were not reported as drift")
	}
	var corrected resource.ModifyPlanResponse
	corrected.Plan = tfsdk.Plan{Schema: connSchema, Raw: driftRead.State.Raw}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: corrected.Plan}, &corrected)
	corrected.Plan.Get(ctx, &driftModel)
	if corrected.Diagnostics.HasError() || !driftModel.CredentialGrantReady.ValueBool() {
		t.Fatal("credential drift did not schedule repair")
	}
	grant.CredentialSecretRefs = secretRefs

	// 3. Update
	updatedPlan := PaperclipConnectionResourceModel{
		ID:             types.StringValue("conn-456"),
		CompanyID:      types.StringValue("comp-1"),
		AppID:          types.StringValue("app-123"),
		Name:           types.StringValue("openobserve-connection-renamed"),
		Transport:      types.StringValue("local_stdio"),
		AuthKind:       types.StringValue("basic"),
		Config:         types.StringValue(`{"command":"oo"}`),
		CredentialRefs: types.StringValue(`[{"key":"user","name":"user","secretId":"secret-1"}]`),
	}
	var updateReq resource.UpdateRequest
	updateReq.Plan = tfsdk.Plan{Schema: connSchema}
	updateReq.Plan.Set(ctx, &updatedPlan)
	updateReq.State = validState
	var updateResp resource.UpdateResponse
	updateResp.State = tfsdk.State{Schema: connSchema}

	r.Update(ctx, updateReq, &updateResp)
	if updateResp.Diagnostics.HasError() {
		t.Fatalf("Update failed: %v", updateResp.Diagnostics)
	}
	var updatedStateModel PaperclipConnectionResourceModel
	updateResp.State.Get(ctx, &updatedStateModel)
	if updatedStateModel.AppID.ValueString() != "app-123" {
		t.Fatalf("expected updated state app_id app-123, got: %v", updatedStateModel.AppID)
	}
	validState = updateResp.State

	// 4. ImportState with company/id
	var importReq resource.ImportStateRequest
	importReq.ID = "comp-1/conn-456"
	var importResp resource.ImportStateResponse
	importResp.State = tfsdk.State{Schema: connSchema}
	importResp.State.Set(ctx, &PaperclipConnectionResourceModel{})
	r.ImportState(ctx, importReq, &importResp)
	if importResp.Diagnostics.HasError() {
		t.Fatalf("ImportState failed: %v", importResp.Diagnostics)
	}
	readReq.State = importResp.State
	readResp = resource.ReadResponse{State: tfsdk.State{Schema: connSchema}}
	r.Read(ctx, readReq, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("Read after import failed: %v", readResp.Diagnostics)
	}
	var importedState PaperclipConnectionResourceModel
	readResp.State.Get(ctx, &importedState)
	if importedState.AppID.ValueString() != "app-123" {
		t.Fatalf("expected imported state app_id app-123, got: %v", importedState.AppID)
	}

	// 5. ImportState with passthrough id
	importReq.ID = "conn-456"
	importResp = resource.ImportStateResponse{State: tfsdk.State{Schema: connSchema}}
	importResp.State.Set(ctx, &PaperclipConnectionResourceModel{})
	r.ImportState(ctx, importReq, &importResp)
	if importResp.Diagnostics.HasError() {
		t.Fatalf("ImportState passthrough failed: %v", importResp.Diagnostics)
	}

	// 6. Delete
	var delReq resource.DeleteRequest
	delReq.State = validState
	var delResp resource.DeleteResponse
	r.Delete(ctx, delReq, &delResp)
	if delResp.Diagnostics.HasError() {
		t.Fatalf("Delete failed: %v", delResp.Diagnostics)
	}
}

func TestPaperclipConnectionCreateWithoutCredentialsIsKnownAfterApply(t *testing.T) {
	ctx := context.Background()
	for _, echoEmptyRefs := range []bool{false, true} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			if r.URL.Path != "/api/companies/comp-1/tools/connections" || r.Method != http.MethodPost {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			payload := map[string]interface{}{"id": "conn-789", "companyId": "comp-1", "applicationId": "app-123", "name": "phonemata", "transport": "local_stdio"}
			if echoEmptyRefs {
				payload["credentialRefs"] = []map[string]interface{}{}
			}
			_ = json.NewEncoder(w).Encode(payload)
		}))
		r, connSchema := newConfiguredPaperclipConnectionResource(t, server.URL)
		planModel := PaperclipConnectionResourceModel{
			CompanyID:      types.StringValue("comp-1"),
			AppID:          types.StringValue("app-123"),
			Name:           types.StringValue("phonemata"),
			Transport:      types.StringValue("local_stdio"),
			AuthKind:       types.StringValue("none"),
			Config:         types.StringValue(`{"command":"phonemata-mcp"}`),
			CredentialRefs: types.StringUnknown(),
		}
		var createReq resource.CreateRequest
		createReq.Plan = tfsdk.Plan{Schema: connSchema}
		createReq.Plan.Set(ctx, &planModel)
		var createResp resource.CreateResponse
		createResp.State = tfsdk.State{Schema: connSchema}
		r.Create(ctx, createReq, &createResp)
		server.Close()
		if createResp.Diagnostics.HasError() {
			t.Fatalf("echo=%v: create failed: %v", echoEmptyRefs, createResp.Diagnostics)
		}
		var stateModel PaperclipConnectionResourceModel
		createResp.State.Get(ctx, &stateModel)
		if stateModel.CredentialRefs.IsUnknown() || stateModel.CredentialRefs.IsNull() || stateModel.CredentialRefs.ValueString() != "[]" {
			t.Fatalf("echo=%v: credential_refs is not a known empty list after create: %#v", echoEmptyRefs, stateModel.CredentialRefs)
		}
		if !stateModel.CredentialGrantReady.ValueBool() {
			t.Fatalf("echo=%v: credential grant readiness was not recorded", echoEmptyRefs)
		}
	}
}

func TestPaperclipOAuthConnectionRequestsItsDeclaredCredentialPolicy(t *testing.T) {
	ctx := context.Background()
	for _, declared := range []types.String{types.StringValue("shared"), types.StringUnknown()} {
		var requested interface{}
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			record := map[string]interface{}{"id": "conn-hf", "companyId": "comp-1", "applicationId": "app-hf", "name": "huggingface", "transport": "mcp_remote", "authKind": "oauth", "status": "draft"}
			switch {
			case r.URL.Path == "/api/companies/comp-1/tools/connections" && r.Method == http.MethodPost:
				var body map[string]interface{}
				_ = json.NewDecoder(r.Body).Decode(&body)
				requested = body["credentialPolicy"]
				if requested != nil {
					record["credentialPolicy"] = requested
				}
			case r.URL.Path == "/api/tool-connections/conn-hf" && r.Method == http.MethodGet:
				// Paperclip gives an OAuth connection created without a policy a
				// per-user credential.
				record["credentialPolicy"] = "per_user"
			default:
				w.WriteHeader(http.StatusNotFound)
				return
			}
			_ = json.NewEncoder(w).Encode(record)
		}))
		r, connSchema := newConfiguredPaperclipConnectionResource(t, server.URL)
		var createReq resource.CreateRequest
		createReq.Plan = tfsdk.Plan{Schema: connSchema}
		createReq.Plan.Set(ctx, &PaperclipConnectionResourceModel{
			CompanyID:        types.StringValue("comp-1"),
			AppID:            types.StringValue("app-hf"),
			Name:             types.StringValue("huggingface"),
			Transport:        types.StringValue("mcp_remote"),
			AuthKind:         types.StringValue("oauth"),
			Config:           types.StringValue(`{"url":"https://huggingface.co/mcp?login"}`),
			CredentialRefs:   types.StringUnknown(),
			CredentialPolicy: declared,
		})
		var createResp resource.CreateResponse
		createResp.State = tfsdk.State{Schema: connSchema}
		r.Create(ctx, createReq, &createResp)
		if createResp.Diagnostics.HasError() {
			t.Fatalf("%s: create failed: %v", declared, createResp.Diagnostics)
		}
		var created PaperclipConnectionResourceModel
		createResp.State.Get(ctx, &created)
		if declared.IsUnknown() {
			if requested != nil || !created.CredentialPolicy.IsNull() {
				t.Fatalf("an undeclared policy was sent or left unknown: %v, %s", requested, created.CredentialPolicy)
			}
		} else if requested != "shared" || created.CredentialPolicy.ValueString() != "shared" {
			t.Fatalf("the shared policy was not requested and recorded: %v, %s", requested, created.CredentialPolicy)
		}
		// Read records the live policy, so a per-user connection plans its
		// replacement against a declared shared one.
		var readResp resource.ReadResponse
		readResp.State = createResp.State
		r.Read(ctx, resource.ReadRequest{State: createResp.State}, &readResp)
		server.Close()
		var read PaperclipConnectionResourceModel
		readResp.State.Get(ctx, &read)
		if readResp.Diagnostics.HasError() || read.CredentialPolicy.ValueString() != "per_user" {
			t.Fatalf("%s: live credential policy was not read: %v %s", declared, readResp.Diagnostics, read.CredentialPolicy)
		}
	}
}

func configureTestResource(t *testing.T, serverURL string, r resource.ResourceWithConfigure) schema.Schema {
	t.Helper()
	cli, err := client.NewClient(serverURL, "test-token")
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	var cfgResp resource.ConfigureResponse
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: cli}, &cfgResp)
	if cfgResp.Diagnostics.HasError() {
		t.Fatalf("Configure failed: %v", cfgResp.Diagnostics)
	}
	var schemaResp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)
	return schemaResp.Schema
}

func TestPaperclipWorkspaceIndependentRefsAndExactRefresh(t *testing.T) {
	ctx := context.Background()
	var createdPayload, updatedPayload map[string]interface{}
	liveRefs := map[string]interface{}{"repoRef": nil, "defaultRef": nil}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/projects/proj-1/workspaces" && r.Method == http.MethodPost:
			_ = json.NewDecoder(r.Body).Decode(&createdPayload)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "ws-1", "projectId": "proj-1", "name": createdPayload["name"], "repoUrl": createdPayload["repoUrl"],
				"repoRef": createdPayload["repoRef"], "defaultRef": createdPayload["defaultRef"], "isPrimary": true, "sourceType": "git_repo",
			})
		case r.URL.Path == "/api/projects/proj-1/workspaces" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{{
				"id": "ws-1", "projectId": "proj-1", "name": "neumata", "repoUrl": "https://git.example.test/neumata.git",
				"repoRef": liveRefs["repoRef"], "defaultRef": liveRefs["defaultRef"], "isPrimary": true, "sourceType": "git_repo",
			}})
		case r.URL.Path == "/api/projects/proj-1/workspaces/ws-1" && r.Method == http.MethodPatch:
			_ = json.NewDecoder(r.Body).Decode(&updatedPayload)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "ws-1", "projectId": "proj-1", "name": updatedPayload["name"], "repoUrl": updatedPayload["repoUrl"],
				"repoRef": updatedPayload["repoRef"], "defaultRef": updatedPayload["defaultRef"], "isPrimary": true, "sourceType": "git_repo",
			})
		default:
			t.Errorf("unexpected workspace request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	r := NewPaperclipWorkspaceResource().(*PaperclipWorkspaceResource)
	wsSchema := configureTestResource(t, server.URL, r)
	if _, present := wsSchema.Attributes["source"]; present {
		t.Fatal("workspace schema still carries the classification field Paperclip never stores")
	}
	planModel := PaperclipWorkspaceResourceModel{
		ProjectID: types.StringValue("proj-1"),
		Name:      types.StringValue("neumata"),
		RepoURL:   types.StringValue("https://git.example.test/neumata.git"),
		RepoRef:   types.StringValue("develop"), DefaultRef: types.StringValue("main"),
		IsPrimary:  types.BoolValue(true),
		SourceType: types.StringValue("git_repo"),
	}
	var createReq resource.CreateRequest
	createReq.Plan = tfsdk.Plan{Schema: wsSchema}
	createReq.Plan.Set(ctx, &planModel)
	createResp := resource.CreateResponse{State: tfsdk.State{Schema: wsSchema}}
	r.Create(ctx, createReq, &createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("create failed: %v", createResp.Diagnostics)
	}
	for _, key := range []string{"branch", "source"} {
		if _, present := createdPayload[key]; present {
			t.Fatalf("create sent provider-only field %q", key)
		}
	}
	if createdPayload["repoRef"] != "develop" || createdPayload["defaultRef"] != "main" {
		t.Fatalf("create did not apply the declared branch to both refs: %v", createdPayload)
	}
	var created PaperclipWorkspaceResourceModel
	createResp.State.Get(ctx, &created)
	if created.RepoRef.ValueString() != "develop" || created.DefaultRef.ValueString() != "main" {
		t.Fatalf("created refs read back incorrectly: %+v", created)
	}

	readReq := resource.ReadRequest{State: createResp.State}
	readBranch := func() string {
		t.Helper()
		readResp := resource.ReadResponse{State: tfsdk.State{Schema: wsSchema}}
		r.Read(ctx, readReq, &readResp)
		if readResp.Diagnostics.HasError() {
			t.Fatalf("read failed: %v", readResp.Diagnostics)
		}
		var refreshed PaperclipWorkspaceResourceModel
		readResp.State.Get(ctx, &refreshed)
		return refreshed.DefaultRef.ValueString()
	}
	if branch := readBranch(); branch != "" {
		t.Fatalf("missing live refs read back as %q instead of planning an update", branch)
	}
	liveRefs["repoRef"], liveRefs["defaultRef"] = "develop", "main"
	if branch := readBranch(); branch != "main" {
		t.Fatalf("independent live default ref read back as %q", branch)
	}
	liveRefs["defaultRef"] = "develop"
	if branch := readBranch(); branch != "develop" {
		t.Fatalf("converged live refs read back as %q", branch)
	}

	driftedState := created
	driftedState.DefaultRef = types.StringNull()
	var updateReq resource.UpdateRequest
	updateReq.Plan = createReq.Plan
	updateReq.State = tfsdk.State{Schema: wsSchema}
	updateReq.State.Set(ctx, &driftedState)
	updateResp := resource.UpdateResponse{State: tfsdk.State{Schema: wsSchema}}
	r.Update(ctx, updateReq, &updateResp)
	if updateResp.Diagnostics.HasError() {
		t.Fatalf("update failed: %v", updateResp.Diagnostics)
	}
	if updatedPayload["repoRef"] != "develop" || updatedPayload["defaultRef"] != "main" {
		t.Fatalf("update did not restore both refs: %v", updatedPayload)
	}
	var updated PaperclipWorkspaceResourceModel
	updateResp.State.Get(ctx, &updated)
	if updated.DefaultRef.ValueString() != "main" {
		t.Fatalf("updated default ref read back as %q", updated.DefaultRef.ValueString())
	}
}

func TestPaperclipAppRetirementArchivesAndReadsArchivedAsAbsent(t *testing.T) {
	ctx := context.Background()
	var patched map[string]interface{}
	status := "active"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/tool-applications/app-9" && r.Method == http.MethodPatch:
			_ = json.NewDecoder(r.Body).Decode(&patched)
			status = "archived"
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "app-9", "status": status})
		case r.URL.Path == "/api/tool-applications/app-9" && r.Method == http.MethodDelete:
			t.Errorf("hard application delete reached Paperclip")
			w.WriteHeader(http.StatusConflict)
		case r.URL.Path == "/api/companies/comp-1/tools/applications" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"applications": []map[string]interface{}{{
				"id": "app-9", "companyId": "comp-1", "name": "phonemata", "type": "mcp_stdio",
				"applicationKey": "harness.mcp.phonemata", "status": status,
			}}})
		default:
			t.Errorf("unexpected application request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	r := NewPaperclipAppResource().(*PaperclipAppResource)
	appSchema := configureTestResource(t, server.URL, r)
	stateModel := PaperclipAppResourceModel{
		ID:             types.StringValue("app-9"),
		CompanyID:      types.StringValue("comp-1"),
		Name:           types.StringValue("phonemata"),
		Type:           types.StringValue("mcp_stdio"),
		ApplicationKey: types.StringValue("harness.mcp.phonemata"),
	}
	state := tfsdk.State{Schema: appSchema}
	state.Set(ctx, &stateModel)

	readResp := resource.ReadResponse{State: tfsdk.State{Schema: appSchema}}
	r.Read(ctx, resource.ReadRequest{State: state}, &readResp)
	if readResp.Diagnostics.HasError() || readResp.State.Raw.IsNull() {
		t.Fatalf("active application did not read back: %v", readResp.Diagnostics)
	}

	deleteResp := resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: state}, &deleteResp)
	if deleteResp.Diagnostics.HasError() {
		t.Fatalf("retirement failed: %v", deleteResp.Diagnostics)
	}
	if !reflect.DeepEqual(patched, map[string]interface{}{"status": "archived"}) {
		t.Fatalf("retirement sent %v instead of the archived status", patched)
	}

	readResp = resource.ReadResponse{State: tfsdk.State{Schema: appSchema}}
	r.Read(ctx, resource.ReadRequest{State: state}, &readResp)
	if readResp.Diagnostics.HasError() || !readResp.State.Raw.IsNull() {
		t.Fatalf("archived application still reads as present: %v", readResp.Diagnostics)
	}
}

func TestPaperclipStdioTemplateIsImmutable(t *testing.T) {
	ctx := context.Background()
	var createdPayload map[string]interface{}
	creates, disables := 0, 0
	disableStatus := http.StatusOK
	live := map[string]interface{}{
		"id": "tpl-1", "companyId": "comp-1", "templateId": "harness.mcp.phonemata.abc", "name": "phonemata",
		"description": "Harness-managed", "command": "sh", "args": []string{"-c", "exec phonemata-mcp"},
		"envKeys": []string{"TOKEN"}, "status": "active",
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/companies/comp-1/tools/stdio-templates" && r.Method == http.MethodPost:
			creates++
			_ = json.NewDecoder(r.Body).Decode(&createdPayload)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "tpl-1", "companyId": "comp-1", "templateId": createdPayload["templateId"], "name": createdPayload["name"],
				"command": createdPayload["command"], "args": createdPayload["args"], "envKeys": createdPayload["envKeys"], "status": "active",
			})
		case r.URL.Path == "/api/companies/comp-1/tools/stdio-templates" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{live})
		case r.URL.Path == "/api/companies/comp-1/tools/stdio-templates/harness.mcp.phonemata.abc/disable" && r.Method == http.MethodPost:
			disables++
			w.WriteHeader(disableStatus)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{})
		default:
			t.Errorf("unexpected template request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	r := NewPaperclipStdioTemplateResource().(*PaperclipStdioTemplateResource)
	tplSchema := configureTestResource(t, server.URL, r)
	if _, present := tplSchema.Attributes["env"]; present {
		t.Fatal("template schema still carries environment values Paperclip never stores")
	}
	for _, name := range []string{"name", "description", "command", "args", "env_keys"} {
		var modifiers []string
		switch attribute := tplSchema.Attributes[name].(type) {
		case schema.StringAttribute:
			for _, modifier := range attribute.PlanModifiers {
				modifiers = append(modifiers, modifier.Description(ctx))
			}
		case schema.ListAttribute:
			for _, modifier := range attribute.PlanModifiers {
				modifiers = append(modifiers, modifier.Description(ctx))
			}
		default:
			t.Fatalf("unexpected attribute type for %s: %T", name, attribute)
		}
		if !strings.Contains(strings.Join(modifiers, " "), "destroy and recreate") {
			t.Fatalf("attribute %s does not force replacement: %v", name, modifiers)
		}
	}

	args, _ := types.ListValueFrom(ctx, types.StringType, []string{"-c", "exec phonemata-mcp"})
	envKeys, _ := types.ListValueFrom(ctx, types.StringType, []string{"TOKEN"})
	planModel := PaperclipStdioTemplateResourceModel{
		CompanyID:   types.StringValue("comp-1"),
		TemplateID:  types.StringValue("harness.mcp.phonemata.abc"),
		Name:        types.StringValue("phonemata"),
		Description: types.StringValue("Harness-managed"),
		Command:     types.StringValue("sh"),
		Args:        args,
		EnvKeys:     envKeys,
	}
	var createReq resource.CreateRequest
	createReq.Plan = tfsdk.Plan{Schema: tplSchema}
	createReq.Plan.Set(ctx, &planModel)
	createResp := resource.CreateResponse{State: tfsdk.State{Schema: tplSchema}}
	r.Create(ctx, createReq, &createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("create failed: %v", createResp.Diagnostics)
	}
	if _, present := createdPayload["env"]; present {
		t.Fatal("create sent environment values")
	}
	if !reflect.DeepEqual(createdPayload["envKeys"], []interface{}{"TOKEN"}) {
		t.Fatalf("create did not send the environment keys: %v", createdPayload)
	}

	var updateReq resource.UpdateRequest
	updateReq.Plan = createReq.Plan
	updateReq.State = createResp.State
	updateResp := resource.UpdateResponse{State: tfsdk.State{Schema: tplSchema}}
	r.Update(ctx, updateReq, &updateResp)
	if !updateResp.Diagnostics.HasError() || creates != 1 {
		t.Fatalf("in-place template update reached Paperclip: creates=%d diagnostics=%v", creates, updateResp.Diagnostics)
	}

	live["args"] = []string{"-c", "exec other-mcp"}
	live["envKeys"] = []string{}
	readResp := resource.ReadResponse{State: tfsdk.State{Schema: tplSchema}}
	r.Read(ctx, resource.ReadRequest{State: createResp.State}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("read failed: %v", readResp.Diagnostics)
	}
	var refreshed PaperclipStdioTemplateResourceModel
	readResp.State.Get(ctx, &refreshed)
	var refreshedArgs, refreshedKeys []string
	refreshed.Args.ElementsAs(ctx, &refreshedArgs, false)
	refreshed.EnvKeys.ElementsAs(ctx, &refreshedKeys, false)
	if !reflect.DeepEqual(refreshedArgs, []string{"-c", "exec other-mcp"}) || len(refreshedKeys) != 0 {
		t.Fatalf("refresh did not read the live command surface back: args=%v envKeys=%v", refreshedArgs, refreshedKeys)
	}

	live["status"] = "disabled"
	readResp = resource.ReadResponse{State: tfsdk.State{Schema: tplSchema}}
	r.Read(ctx, resource.ReadRequest{State: createResp.State}, &readResp)
	if readResp.Diagnostics.HasError() || !readResp.State.Raw.IsNull() {
		t.Fatalf("disabled template still reads as present: %v", readResp.Diagnostics)
	}

	deleteResp := resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: createResp.State}, &deleteResp)
	if deleteResp.Diagnostics.HasError() || disables != 1 {
		t.Fatalf("removal did not disable the template once: disables=%d diagnostics=%v", disables, deleteResp.Diagnostics)
	}
	disableStatus = http.StatusInternalServerError
	deleteResp = resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: createResp.State}, &deleteResp)
	if !deleteResp.Diagnostics.HasError() {
		t.Fatal("refused template disable was ignored")
	}
}

func TestPaperclipConnectionRejectsMalformedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("malformed connection JSON reached Paperclip: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	r, connSchema := newConfiguredPaperclipConnectionResource(t, server.URL)
	for _, tc := range []struct {
		name           string
		config         types.String
		credentialRefs types.String
	}{
		{name: "config", config: types.StringValue(`{"broken"`)},
		{name: "credential_refs", credentialRefs: types.StringValue(`[{"broken"}`)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := PaperclipConnectionResourceModel{
				CompanyID:      types.StringValue("comp-1"),
				AppID:          types.StringValue("app-1"),
				Name:           types.StringValue("connection"),
				Transport:      types.StringValue("mcp_remote"),
				AuthKind:       types.StringValue("api_key"),
				Config:         tc.config,
				CredentialRefs: tc.credentialRefs,
			}
			var req resource.CreateRequest
			req.Plan = tfsdk.Plan{Schema: connSchema}
			req.Plan.Set(context.Background(), &model)
			resp := resource.CreateResponse{State: tfsdk.State{Schema: connSchema}}
			r.Create(context.Background(), req, &resp)
			if !resp.Diagnostics.HasError() {
				t.Fatal("expected malformed JSON diagnostic")
			}

			stateModel := model
			stateModel.ID = types.StringValue("connection-1")
			stateModel.Config = types.StringValue(`{"url":"https://example.test/mcp"}`)
			stateModel.CredentialRefs = types.StringValue(`[{"key":"Authorization"}]`)
			var updateReq resource.UpdateRequest
			updateReq.Plan = req.Plan
			updateReq.State = tfsdk.State{Schema: connSchema}
			updateReq.State.Set(context.Background(), &stateModel)
			updateResp := resource.UpdateResponse{State: tfsdk.State{Schema: connSchema}}
			r.Update(context.Background(), updateReq, &updateResp)
			if !updateResp.Diagnostics.HasError() {
				t.Fatal("expected malformed update JSON diagnostic")
			}
		})
	}
}

func TestPaperclipConnectionPreservesAppIDWhenReadOmitsApplicationID(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/tool-connections/conn-no-app":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":        "conn-no-app",
				"companyId": "comp-1",
				"name":      "openobserve-connection",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	r, connSchema := newConfiguredPaperclipConnectionResource(t, server.URL)

	priorState := PaperclipConnectionResourceModel{
		ID:        types.StringValue("conn-no-app"),
		CompanyID: types.StringValue("comp-1"),
		AppID:     types.StringValue("app-123"),
		Name:      types.StringValue("openobserve-connection"),
	}

	var readReq resource.ReadRequest
	readReq.State = tfsdk.State{Schema: connSchema}
	readReq.State.Set(ctx, &priorState)

	var readResp resource.ReadResponse
	readResp.State = tfsdk.State{Schema: connSchema}
	r.Read(ctx, readReq, &readResp)

	if readResp.Diagnostics.HasError() {
		t.Fatalf("Read must not fail when read response omits applicationId: %v", readResp.Diagnostics)
	}

	var readState PaperclipConnectionResourceModel
	readResp.State.Get(ctx, &readState)
	if readState.AppID.ValueString() != "app-123" {
		t.Fatalf("Read must preserve existing app_id when API response omits applicationId, got %v", readState.AppID)
	}
}

func TestPaperclipConnectionImportWithoutAppIDFails(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/tool-connections/conn-null-app":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":        "conn-null-app",
				"companyId": "comp-1",
				"name":      "unbound-conn",
			})
		case "/api/companies":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	r, connSchema := newConfiguredPaperclipConnectionResource(t, server.URL)

	var importReq resource.ImportStateRequest
	importReq.ID = "comp-1/conn-null-app"
	importResp := resource.ImportStateResponse{State: tfsdk.State{Schema: connSchema}}
	importResp.State.Set(ctx, &PaperclipConnectionResourceModel{})
	r.ImportState(ctx, importReq, &importResp)
	if importResp.Diagnostics.HasError() {
		t.Fatalf("ImportState failed: %v", importResp.Diagnostics)
	}

	var readReq resource.ReadRequest
	readReq.State = importResp.State
	readResp := resource.ReadResponse{State: tfsdk.State{Schema: connSchema}}
	r.Read(ctx, readReq, &readResp)
	if !readResp.Diagnostics.HasError() {
		t.Fatal("expected Read after import to fail when Paperclip never reports an applicationId and no prior state exists")
	}
}

func TestPaperclipConnectionErrors(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	r, connSchema := newConfiguredPaperclipConnectionResource(t, server.URL)

	// Configure error
	var badCfgResp resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: "invalid"}, &badCfgResp)
	if !badCfgResp.Diagnostics.HasError() {
		t.Fatal("expected configure error with bad provider data")
	}

	// Create error
	planModel := PaperclipConnectionResourceModel{
		CompanyID: types.StringValue("comp-1"),
		AppID:     types.StringValue("app-123"),
		Name:      types.StringValue("conn"),
	}
	var createReq resource.CreateRequest
	createReq.Plan = tfsdk.Plan{Schema: connSchema}
	createReq.Plan.Set(ctx, &planModel)
	var createResp resource.CreateResponse
	createResp.State = tfsdk.State{Schema: connSchema}
	r.Create(ctx, createReq, &createResp)
	if !createResp.Diagnostics.HasError() {
		t.Fatal("expected create error on server error")
	}

	// Update error
	stateModel := PaperclipConnectionResourceModel{
		ID:        types.StringValue("conn-1"),
		CompanyID: types.StringValue("comp-1"),
		AppID:     types.StringValue("app-123"),
		Name:      types.StringValue("conn"),
	}
	var updateReq resource.UpdateRequest
	updateReq.Plan = tfsdk.Plan{Schema: connSchema}
	updateReq.Plan.Set(ctx, &planModel)
	updateReq.State = tfsdk.State{Schema: connSchema}
	updateReq.State.Set(ctx, &stateModel)
	var updateResp resource.UpdateResponse
	updateResp.State = tfsdk.State{Schema: connSchema}
	r.Update(ctx, updateReq, &updateResp)
	if !updateResp.Diagnostics.HasError() {
		t.Fatal("expected update error on server error")
	}

	// Delete error
	var delReq resource.DeleteRequest
	delReq.State = tfsdk.State{Schema: connSchema}
	delReq.State.Set(ctx, &stateModel)
	var delResp resource.DeleteResponse
	r.Delete(ctx, delReq, &delResp)
	if !delResp.Diagnostics.HasError() {
		t.Fatal("expected delete error on server error")
	}
}

func TestPaperclipConnectionReadNotFoundRemovesResource(t *testing.T) {
	ctx := context.Background()
	// Deleting a connection archives it, so an archived connection is as absent
	// as a missing one and the declared connection is created again.
	for name, record := range map[string]map[string]interface{}{
		"missing":  nil,
		"archived": {"id": "conn-retired", "companyId": "comp-1", "applicationId": "app-123", "name": "conn", "status": "archived"},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/companies" {
					_ = json.NewEncoder(w).Encode([]map[string]interface{}{})
					return
				}
				if record == nil {
					w.WriteHeader(http.StatusNotFound)
					return
				}
				_ = json.NewEncoder(w).Encode(record)
			}))
			defer server.Close()

			r, connSchema := newConfiguredPaperclipConnectionResource(t, server.URL)

			stateModel := PaperclipConnectionResourceModel{
				ID:        types.StringValue("conn-retired"),
				CompanyID: types.StringValue("comp-1"),
				AppID:     types.StringValue("app-123"),
				Name:      types.StringValue("conn"),
			}
			var readReq resource.ReadRequest
			readReq.State = tfsdk.State{Schema: connSchema}
			readReq.State.Set(ctx, &stateModel)
			var readResp resource.ReadResponse
			readResp.State = tfsdk.State{Schema: connSchema}
			r.Read(ctx, readReq, &readResp)
			if readResp.Diagnostics.HasError() {
				t.Fatalf("read of a %s connection should not error: %v", name, readResp.Diagnostics)
			}
			if !readResp.State.Raw.IsNull() {
				t.Fatalf("read of a %s connection should remove it from state", name)
			}
		})
	}
}
