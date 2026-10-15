package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

// 1. Skill rename sends name and slug in PATCH metadata
func TestRegressionSkillRenameSendsNameAndSlug(t *testing.T) {
	ctx := context.Background()
	var receivedPatch map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodPatch && req.URL.Path == "/api/companies/comp-1/skills/skill-1":
			_ = json.NewDecoder(req.Body).Decode(&receivedPatch)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":          "skill-1",
				"companyId":   "comp-1",
				"name":        "renamed-skill",
				"slug":        "renamed-slug",
				"description": "Renamed description",
			})
		case req.Method == http.MethodPost && req.URL.Path == "/api/companies/comp-1/skills/skill-1/rename":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"skill": map[string]interface{}{
					"id": "skill-1", "companyId": "comp-1", "name": "renamed-skill", "slug": "renamed-slug",
				},
			})
		case req.Method == http.MethodPatch && req.URL.Path == "/api/companies/comp-1/skills/skill-1/files":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"skillId": "skill-1",
				"path":    "SKILL.md",
				"content": "# Updated Content",
			})
		case req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-1/skills/skill-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":          "skill-1",
				"companyId":   "comp-1",
				"name":        "renamed-skill",
				"slug":        "renamed-slug",
				"description": "Renamed description",
				"markdown":    "# Updated Content",
			})
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	cli, _ := client.NewClient(server.URL, "token")
	r := NewPaperclipSkillResource().(*PaperclipSkillResource)
	r.client = cli
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	plan := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = plan.Set(ctx, &PaperclipSkillResourceModel{
		ID:            types.StringValue("skill-1"),
		CompanyID:     types.StringValue("comp-1"),
		Name:          types.StringValue("renamed-skill"),
		Slug:          types.StringValue("renamed-slug"),
		Description:   types.StringValue("Renamed description"),
		Content:       types.StringValue("# Updated Content"),
		SchemaVersion: types.Int64Value(1),
		Metadata:      types.StringValue(`{"author":"antigravity"}`),
		Categories:    types.ListNull(types.StringType),
		Files:         types.MapNull(types.StringType),
		FileInventory: types.ListNull(fileInventoryObjectType),
	})
	state := tfsdk.State{Schema: schemaResp.Schema}
	_ = state.Set(ctx, &PaperclipSkillResourceModel{
		ID:            types.StringValue("skill-1"),
		CompanyID:     types.StringValue("comp-1"),
		Name:          types.StringValue("old-skill"),
		Slug:          types.StringValue("old-slug"),
		Description:   types.StringValue("Old description"),
		Content:       types.StringValue("# Old Content"),
		SchemaVersion: types.Int64Value(1),
		Categories:    types.ListNull(types.StringType),
		Files:         types.MapNull(types.StringType),
		FileInventory: types.ListNull(fileInventoryObjectType),
	})

	var updateResp resource.UpdateResponse
	updateResp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &updateResp)
	if updateResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error during skill update: %v", updateResp.Diagnostics)
	}

	if receivedPatch["name"] != "renamed-skill" {
		t.Fatalf("expected PATCH metadata to contain name 'renamed-skill', got: %v", receivedPatch["name"])
	}
	if receivedPatch["slug"] != "renamed-slug" {
		t.Fatalf("expected PATCH metadata to contain slug 'renamed-slug', got: %v", receivedPatch["slug"])
	}
	meta, ok := receivedPatch["metadata"].(map[string]interface{})
	if !ok || meta["author"] != "antigravity" {
		t.Fatalf("expected PATCH metadata to contain parsed metadata, got: %v", receivedPatch["metadata"])
	}
}

// 2. Skill content can be explicitly cleared to empty string
func TestRegressionSkillContentCanBeCleared(t *testing.T) {
	ctx := context.Background()
	var filesPatchReceived bool
	var receivedContent interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodPatch && req.URL.Path == "/api/companies/comp-1/skills/skill-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":          "skill-1",
				"companyId":   "comp-1",
				"name":        "skill-1",
				"description": "Cleared content",
			})
		case req.Method == http.MethodPatch && req.URL.Path == "/api/companies/comp-1/skills/skill-1/files":
			filesPatchReceived = true
			var body map[string]interface{}
			_ = json.NewDecoder(req.Body).Decode(&body)
			receivedContent = body["content"]
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"skillId": "skill-1",
				"path":    "SKILL.md",
				"content": "",
			})
		case req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-1/skills/skill-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":          "skill-1",
				"companyId":   "comp-1",
				"name":        "skill-1",
				"description": "Cleared content",
				"markdown":    "",
			})
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	cli, _ := client.NewClient(server.URL, "token")
	r := NewPaperclipSkillResource().(*PaperclipSkillResource)
	r.client = cli
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	plan := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = plan.Set(ctx, &PaperclipSkillResourceModel{
		ID:            types.StringValue("skill-1"),
		CompanyID:     types.StringValue("comp-1"),
		Name:          types.StringValue("skill-1"),
		Description:   types.StringValue("Cleared content"),
		Content:       types.StringValue(""),
		Categories:    types.ListNull(types.StringType),
		Files:         types.MapNull(types.StringType),
		FileInventory: types.ListNull(fileInventoryObjectType),
	})
	state := tfsdk.State{Schema: schemaResp.Schema}
	_ = state.Set(ctx, &PaperclipSkillResourceModel{
		ID:            types.StringValue("skill-1"),
		CompanyID:     types.StringValue("comp-1"),
		Name:          types.StringValue("skill-1"),
		Description:   types.StringValue("Previous content"),
		Content:       types.StringValue("# Old content to clear"),
		Categories:    types.ListNull(types.StringType),
		Files:         types.MapNull(types.StringType),
		FileInventory: types.ListNull(fileInventoryObjectType),
	})

	var updateResp resource.UpdateResponse
	updateResp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &updateResp)
	if updateResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error during skill update: %v", updateResp.Diagnostics)
	}

	if !filesPatchReceived {
		t.Fatal("expected PATCH /files to be called when clearing skill content to empty string")
	}
	if receivedContent != "" {
		t.Fatalf("expected empty string content in files PATCH, got: %v", receivedContent)
	}
}

// 3. Agent Update preserves planned role and adapter_type rather than overwriting with server response
func TestRegressionAgentUpdatePreservesPlannedRoleAndAdapterType(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/agents/agent-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":          "agent-1",
				"companyId":   "comp-1",
				"name":        "agent-1",
				"role":        "server_default_role",
				"adapterType": "server_default_adapter",
				"permissions": map[string]interface{}{"canAssignTasks": true, "canCreateAgents": false},
			})
		case req.Method == http.MethodPatch && req.URL.Path == "/api/agents/agent-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":          "agent-1",
				"companyId":   "comp-1",
				"name":        "agent-1",
				"role":        "server_default_role",
				"adapterType": "server_default_adapter",
			})
		case req.Method == http.MethodPatch && req.URL.Path == "/api/agents/agent-1/permissions":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "agent-1"})
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	cli, _ := client.NewClient(server.URL, "token")
	r := NewPaperclipAgentResource().(*PaperclipAgentResource)
	r.client = cli
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	// Part A: Planned Role and AdapterType are known -> Must be preserved in state
	planA := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = planA.Set(ctx, &PaperclipAgentResourceModel{
		ID:              types.StringValue("agent-1"),
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("agent-1"),
		Role:            types.StringValue("planned_custom_role"),
		AdapterType:     types.StringValue("planned_custom_adapter"),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(false),
	})
	stateA := tfsdk.State{Schema: schemaResp.Schema}
	_ = stateA.Set(ctx, &PaperclipAgentResourceModel{
		ID:              types.StringValue("agent-1"),
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("agent-1"),
		Role:            types.StringValue("prior_role"),
		AdapterType:     types.StringValue("prior_adapter"),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(false),
	})

	var updateRespA resource.UpdateResponse
	updateRespA.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Update(ctx, resource.UpdateRequest{Plan: planA, State: stateA}, &updateRespA)
	if updateRespA.Diagnostics.HasError() {
		t.Fatalf("unexpected error during agent update: %v", updateRespA.Diagnostics)
	}

	var resultingA PaperclipAgentResourceModel
	_ = updateRespA.State.Get(ctx, &resultingA)
	if resultingA.Role.ValueString() != "planned_custom_role" {
		t.Fatalf("expected planned role 'planned_custom_role' to be preserved, got: %q", resultingA.Role.ValueString())
	}
	if resultingA.AdapterType.ValueString() != "planned_custom_adapter" {
		t.Fatalf("expected planned adapter 'planned_custom_adapter' to be preserved, got: %q", resultingA.AdapterType.ValueString())
	}

	// Part B: Planned Role and AdapterType are unknown -> Takes updated value from server
	planB := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = planB.Set(ctx, &PaperclipAgentResourceModel{
		ID:              types.StringValue("agent-1"),
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("agent-1"),
		Role:            types.StringUnknown(),
		AdapterType:     types.StringUnknown(),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(false),
	})
	stateB := tfsdk.State{Schema: schemaResp.Schema}
	_ = stateB.Set(ctx, &PaperclipAgentResourceModel{
		ID:              types.StringValue("agent-1"),
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("agent-1"),
		Role:            types.StringValue("server_default_role"),
		AdapterType:     types.StringValue("server_default_adapter"),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(false),
	})

	var updateRespB resource.UpdateResponse
	updateRespB.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Update(ctx, resource.UpdateRequest{Plan: planB, State: stateB}, &updateRespB)
	if updateRespB.Diagnostics.HasError() {
		t.Fatalf("unexpected error during agent update with unknown role/adapter: %v", updateRespB.Diagnostics)
	}

	var resultingB PaperclipAgentResourceModel
	_ = updateRespB.State.Get(ctx, &resultingB)
	if resultingB.Role.ValueString() != "server_default_role" {
		t.Fatalf("expected updated role from server, got: %q", resultingB.Role.ValueString())
	}
	if resultingB.AdapterType.ValueString() != "server_default_adapter" {
		t.Fatalf("expected updated adapter from server, got: %q", resultingB.AdapterType.ValueString())
	}
}

// 4. Workspace Import rejects bare workspace ID and accepts <project_id>/<workspace_id>
func TestRegressionWorkspaceImportRejectsBareID(t *testing.T) {
	ctx := context.Background()
	r := NewPaperclipWorkspaceResource().(*PaperclipWorkspaceResource)
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	// Bare ID must fail
	var respBare resource.ImportStateResponse
	respBare.State = tfsdk.State{Schema: schemaResp.Schema}
	_ = respBare.State.Set(ctx, &PaperclipWorkspaceResourceModel{})
	r.ImportState(ctx, resource.ImportStateRequest{ID: "ws-123"}, &respBare)
	if !respBare.Diagnostics.HasError() {
		t.Fatal("expected error when importing workspace with bare ID without project prefix")
	}

	// Composite ID must succeed
	var respValid resource.ImportStateResponse
	respValid.State = tfsdk.State{Schema: schemaResp.Schema}
	_ = respValid.State.Set(ctx, &PaperclipWorkspaceResourceModel{})
	r.ImportState(ctx, resource.ImportStateRequest{ID: "proj-1/ws-123"}, &respValid)
	if respValid.Diagnostics.HasError() {
		t.Fatalf("unexpected error importing workspace with valid composite ID: %v", respValid.Diagnostics)
	}

	var wsModel PaperclipWorkspaceResourceModel
	_ = respValid.State.Get(ctx, &wsModel)
	if wsModel.ProjectID.ValueString() != "proj-1" || wsModel.ID.ValueString() != "ws-123" {
		t.Fatalf("unexpected imported attributes: project=%q, ws=%q", wsModel.ProjectID.ValueString(), wsModel.ID.ValueString())
	}
}

// 5. Local environment Import supports <company_id>/<id> and bare <id>, rejecting empty string
func TestRegressionLocalEnvironmentImportSupported(t *testing.T) {
	ctx := context.Background()
	r := NewPaperclipLocalEnvironmentResource().(*PaperclipLocalEnvironmentResource)
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	// Empty ID must fail
	var respEmpty resource.ImportStateResponse
	respEmpty.State = tfsdk.State{Schema: schemaResp.Schema}
	_ = respEmpty.State.Set(ctx, &PaperclipLocalEnvironmentModel{})
	r.ImportState(ctx, resource.ImportStateRequest{ID: ""}, &respEmpty)
	if !respEmpty.Diagnostics.HasError() {
		t.Fatal("expected error on empty import ID")
	}

	// Composite ID
	var respComposite resource.ImportStateResponse
	respComposite.State = tfsdk.State{Schema: schemaResp.Schema}
	_ = respComposite.State.Set(ctx, &PaperclipLocalEnvironmentModel{})
	r.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/env-1"}, &respComposite)
	if respComposite.Diagnostics.HasError() {
		t.Fatalf("unexpected error on composite import: %v", respComposite.Diagnostics)
	}
	var mComp PaperclipLocalEnvironmentModel
	_ = respComposite.State.Get(ctx, &mComp)
	if mComp.CompanyID.ValueString() != "comp-1" || mComp.ID.ValueString() != "env-1" {
		t.Fatalf("unexpected imported model: %+v", mComp)
	}

	// Bare ID
	var respBare resource.ImportStateResponse
	respBare.State = tfsdk.State{Schema: schemaResp.Schema}
	_ = respBare.State.Set(ctx, &PaperclipLocalEnvironmentModel{})
	r.ImportState(ctx, resource.ImportStateRequest{ID: "env-1"}, &respBare)
	if respBare.Diagnostics.HasError() {
		t.Fatalf("unexpected error on bare import: %v", respBare.Diagnostics)
	}
	var mBare PaperclipLocalEnvironmentModel
	_ = respBare.State.Get(ctx, &mBare)
	if mBare.ID.ValueString() != "env-1" {
		t.Fatalf("expected ID 'env-1', got %q", mBare.ID.ValueString())
	}
}

// 6. Connection install Delete surfaces HTTP errors
func TestRegressionConnectionInstallDeleteSurfacesError(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, "internal server failure", http.StatusInternalServerError)
	}))
	defer server.Close()

	cli, _ := client.NewClient(server.URL, "token")
	r := NewPaperclipConnectionInstallResource().(*PaperclipConnectionInstallResource)
	r.client = cli
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	state := tfsdk.State{Schema: schemaResp.Schema}
	_ = state.Set(ctx, &PaperclipConnectionInstallResourceModel{
		ID:           types.StringValue("conn-1"),
		ConnectionID: types.StringValue("conn-1"),
	})

	var delResp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: state}, &delResp)
	if !delResp.Diagnostics.HasError() {
		t.Fatal("expected error diagnostic when server returns 500 on install delete")
	}
}

// 7. Company Delete calls POST /archive rather than DELETE
func TestRegressionCompanyDeleteCallsArchiveNotDelete(t *testing.T) {
	ctx := context.Background()
	var archiveCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if req.Method == http.MethodDelete && req.URL.Path == "/api/companies/comp-1" {
			t.Fatal("unexpected DELETE request received: companies must be archived")
		}
		if req.Method == http.MethodPost && req.URL.Path == "/api/companies/comp-1/archive" {
			archiveCalled = true
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, req)
	}))
	defer server.Close()

	cli, _ := client.NewClient(server.URL, "token")
	r := NewPaperclipCompanyResource().(*PaperclipCompanyResource)
	r.client = cli
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	state := tfsdk.State{Schema: schemaResp.Schema}
	_ = state.Set(ctx, &PaperclipCompanyResourceModel{
		ID:   types.StringValue("comp-1"),
		Name: types.StringValue("Acme"),
	})

	var delResp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: state}, &delResp)
	if delResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error during company delete: %v", delResp.Diagnostics)
	}
	if !archiveCalled {
		t.Fatal("expected POST /api/companies/comp-1/archive to be invoked on delete")
	}
}

// 8. Resource Read removes resource from state when remote endpoint returns 404
func TestRegressionResourceReadRemovesStateOn404NotFound(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		http.NotFound(w, req)
	}))
	defer server.Close()

	cli, _ := client.NewClient(server.URL, "token")

	// Secret Read with unknown rotation
	rSec := NewPaperclipSecretResource().(*PaperclipSecretResource)
	rSec.client = cli
	var secSchema resource.SchemaResponse
	rSec.Schema(ctx, resource.SchemaRequest{}, &secSchema)
	stateSec := tfsdk.State{Schema: secSchema.Schema}
	_ = stateSec.Set(ctx, &PaperclipSecretResourceModel{
		ID:        types.StringValue("sec-1"),
		CompanyID: types.StringValue("comp-1"),
		Rotation:  types.StringUnknown(),
	})
	var readSecResp resource.ReadResponse
	readSecResp.State = stateSec
	rSec.Read(ctx, resource.ReadRequest{State: stateSec}, &readSecResp)
	if !readSecResp.State.Raw.IsNull() {
		t.Fatal("expected secret state to be removed on 404")
	}
	if readSecResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error diagnostic on 404: %v", readSecResp.Diagnostics)
	}
}

// 9. Backward compatibility: verify that state written by prior provider versions plans without replacement across all resources
func TestBackwardCompatibilityExistingStatePlansWithoutReplacementAcrossResources(t *testing.T) {
	ctx := context.Background()
	server := providerserver.NewProtocol6(New("test")())()
	schemas, err := server.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil || len(schemas.Diagnostics) != 0 {
		t.Fatalf("failed to get provider schema: %v, %v", err, schemas)
	}

	testCases := []struct {
		typeName string
		rawJSON  string
	}{
		{
			typeName: "paperclip_company",
			rawJSON:  `{"id":"comp-1","name":"Acme","description":"Corp","status":"active","default_responsible_user_id":null,"require_board_approval_for_new_agents":false,"interaction_resolver_governance":null,"feedback_data_sharing_enabled":false,"logo_asset_id":null,"archive_on_destroy":true}`,
		},
		{
			typeName: "paperclip_project",
			rawJSON:  `{"id":"proj-1","company_id":"comp-1","name":"Test Project","description":"Desc","execution_workspace_policy":null,"status":"backlog","archive_on_destroy":false}`,
		},
		{
			typeName: "paperclip_project_lead",
			rawJSON:  `{"id":"comp-1/proj-1","project_id":"proj-1","agent_id":"agent-1"}`,
		},
		{
			typeName: "paperclip_workspace",
			rawJSON:  `{"id":"ws-1","project_id":"proj-1","name":"Default","repo_ref":"develop","default_ref":"develop","is_primary":true,"repo_url":"https://git.example.com/acme/core-platform.git","source_type":"git_repo","visibility":"default"}`,
		},
		{
			typeName: "paperclip_local_environment",
			rawJSON:  `{"id":"env-1","company_id":"comp-1","inherit_service_path":true}`,
		},
		{
			typeName: "paperclip_skill",
			rawJSON:  `{"id":"skill-1","company_id":"comp-1","name":"test-skill","slug":"test-skill","description":"Skill description","content":"# Skill","schema_version":1,"metadata":null}`,
		},
		{
			typeName: "paperclip_secret",
			rawJSON:  `{"id":"sec-1","company_id":"comp-1","name":"db-pass","description":"Pass","rotation":"rev-1"}`,
		},
		{
			typeName: "paperclip_agent",
			rawJSON:  `{"id":"agent-1","company_id":"comp-1","name":"agent-1","role":"engineer","title":"Senior Eng","icon":null,"reports_to":null,"capabilities":null,"default_environment_id":null,"adapter_type":"hermes_local","status":null,"runtime_config":null,"adapter_config":null,"metadata":null,"can_assign_tasks":true,"can_create_agents":false,"can_create_skills":true,"trust_preset":null,"authorization_policy":null,"paused":false,"retirement":"delete"}`,
		},
		{
			typeName: "paperclip_app",
			rawJSON:  `{"id":"app-1","company_id":"comp-1","name":"github","type":"mcp_http","application_key":null,"description":"GitHub App","metadata":null}`,
		},
		{
			typeName: "paperclip_connection",
			rawJSON:  `{"id":"conn-1","company_id":"comp-1","app_id":"app-1","name":"prod-github","gallery_key":null,"transport":"local_stdio","auth_kind":"api_key","config":"{\"templateId\":\"approved-template\"}","credential_refs":null,"credential_policy":"shared","credential_grant_ready":true}`,
		},
		{
			typeName: "paperclip_policy",
			rawJSON:  `{"id":"pol-1","company_id":"comp-1","name":"require-approval","policy_type":"allow","rules":"{\"allow\":true}","metadata":null}`,
		},
		{
			typeName: "paperclip_stdio_template",
			rawJSON:  `{"id":"tmpl-1","company_id":"comp-1","template_id":"tmpl-1","name":"bash-template","description":"Desc","command":"bash","args":null,"env_keys":null}`,
		},
		{
			typeName: "paperclip_connection_install",
			rawJSON:  `{"id":"conn-1","connection_id":"conn-1","agent_ids":["agent-1"],"target_type":"agent","company_id":null}`,
		},
		{
			typeName: "paperclip_app_permission",
			rawJSON:  `{"id":"comp-1/conn-1","company_id":"comp-1","connection_id":"conn-1","tool_selection":"{\"approval_mode\":\"writes\",\"enabled_tools\":[]}","enabled_catalog_entry_ids":[],"ask_first_catalog_entry_ids":[],"reviewed_catalog_entry_ids":[],"agent_ids":["agent-1"],"access_type":"agents"}`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.typeName, func(t *testing.T) {
			resSchema, ok := schemas.ResourceSchemas[tc.typeName]
			if !ok {
				t.Fatalf("resource schema not found for %s", tc.typeName)
			}

			val, err := (&tfprotov6.RawState{JSON: []byte(tc.rawJSON)}).Unmarshal(resSchema.ValueType())
			if err != nil {
				t.Fatalf("failed to unmarshal raw JSON for %s: %v", tc.typeName, err)
			}

			dynVal, err := tfprotov6.NewDynamicValue(resSchema.ValueType(), val)
			if err != nil {
				t.Fatalf("failed to create DynamicValue for %s: %v", tc.typeName, err)
			}

			req := &tfprotov6.PlanResourceChangeRequest{
				TypeName:         tc.typeName,
				PriorState:       &dynVal,
				ProposedNewState: &dynVal,
				Config:           &dynVal,
			}

			resp, err := server.PlanResourceChange(ctx, req)
			if err != nil {
				t.Fatalf("PlanResourceChange error for %s: %v", tc.typeName, err)
			}
			for _, d := range resp.Diagnostics {
				if d.Severity == tfprotov6.DiagnosticSeverityError {
					t.Fatalf("PlanResourceChange error diag for %s: %s (%s)", tc.typeName, d.Summary, d.Detail)
				}
			}
			if len(resp.RequiresReplace) > 0 {
				t.Fatalf("unexpected RequiresReplace for %s: %v", tc.typeName, resp.RequiresReplace)
			}

			plannedVal, err := resp.PlannedState.Unmarshal(resSchema.ValueType())
			if err != nil {
				t.Fatalf("failed to unmarshal PlannedState for %s: %v", tc.typeName, err)
			}
			if !plannedVal.Equal(val) {
				t.Fatalf("planned state introduces diff for %s: got %v, want %v", tc.typeName, plannedVal, val)
			}
		})
	}
}
