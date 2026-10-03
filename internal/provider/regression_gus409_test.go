package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"git.guseynov.org/guseynov/paperclip/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
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
		ID:          types.StringValue("skill-1"),
		CompanyID:   types.StringValue("comp-1"),
		Name:        types.StringValue("skill-1"),
		Description: types.StringValue("Cleared content"),
		Content:     types.StringValue(""),
	})
	state := tfsdk.State{Schema: schemaResp.Schema}
	_ = state.Set(ctx, &PaperclipSkillResourceModel{
		ID:          types.StringValue("skill-1"),
		CompanyID:   types.StringValue("comp-1"),
		Name:        types.StringValue("skill-1"),
		Description: types.StringValue("Previous content"),
		Content:     types.StringValue("# Old content to clear"),
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

	// Verify that each resource schema accepts existing state models without schema migration or replacement
	resources := []struct {
		name string
		r    resource.Resource
		val  interface{}
	}{
		{
			name: "paperclip_company",
			r:    NewPaperclipCompanyResource(),
			val: &PaperclipCompanyResourceModel{
				ID:                           types.StringValue("comp-1"),
				Name:                         types.StringValue("Acme"),
				Description:                  types.StringValue("Corp"),
				RequireTaskStartConfirmation: types.BoolValue(false),
			},
		},
		{
			name: "paperclip_project",
			r:    NewPaperclipProjectResource(),
			val: &PaperclipProjectResourceModel{
				ID:          types.StringValue("proj-1"),
				CompanyID:   types.StringValue("comp-1"),
				Name:        types.StringValue("Test Project"),
				Description: types.StringValue("Desc"),
			},
		},
		{
			name: "paperclip_project_lead",
			r:    NewPaperclipProjectLeadResource(),
			val: &PaperclipProjectLeadModel{
				ID:        types.StringValue("comp-1/proj-1"),
				ProjectID: types.StringValue("proj-1"),
				AgentID:   types.StringValue("agent-1"),
			},
		},
		{
			name: "paperclip_workspace",
			r:    NewPaperclipWorkspaceResource(),
			val: &PaperclipWorkspaceResourceModel{
				ID:         types.StringValue("ws-1"),
				ProjectID:  types.StringValue("proj-1"),
				Name:       types.StringValue("Default"),
				Branch:     types.StringValue("main"),
				IsPrimary:  types.BoolValue(true),
				SourceType: types.StringValue("git"),
			},
		},
		{
			name: "paperclip_local_environment",
			r:    NewPaperclipLocalEnvironmentResource(),
			val: &PaperclipLocalEnvironmentModel{
				ID:                 types.StringValue("env-1"),
				CompanyID:          types.StringValue("comp-1"),
				InheritServicePath: types.BoolValue(true),
			},
		},
		{
			name: "paperclip_skill",
			r:    NewPaperclipSkillResource(),
			val: &PaperclipSkillResourceModel{
				ID:            types.StringValue("skill-1"),
				CompanyID:     types.StringValue("comp-1"),
				Name:          types.StringValue("test-skill"),
				Slug:          types.StringValue("test-skill"),
				Description:   types.StringValue("Skill description"),
				Content:       types.StringValue("# Skill"),
				SchemaVersion: types.Int64Value(1),
			},
		},
		{
			name: "paperclip_secret",
			r:    NewPaperclipSecretResource(),
			val: &PaperclipSecretResourceModel{
				ID:          types.StringValue("sec-1"),
				CompanyID:   types.StringValue("comp-1"),
				Name:        types.StringValue("db-pass"),
				Description: types.StringValue("Pass"),
				Value:       types.StringValue("val"),
				Rotation:    types.StringValue("rev-1"),
			},
		},
		{
			name: "paperclip_agent",
			r:    NewPaperclipAgentResource(),
			val: &PaperclipAgentResourceModel{
				ID:              types.StringValue("agent-1"),
				CompanyID:       types.StringValue("comp-1"),
				Name:            types.StringValue("agent-1"),
				Role:            types.StringValue("engineer"),
				AdapterType:     types.StringValue("hermes_local"),
				CanAssignTasks:  types.BoolValue(true),
				CanCreateAgents: types.BoolValue(false),
			},
		},
		{
			name: "paperclip_app",
			r:    NewPaperclipAppResource(),
			val: &PaperclipAppResourceModel{
				ID:          types.StringValue("app-1"),
				CompanyID:   types.StringValue("comp-1"),
				Name:        types.StringValue("github"),
				Type:        types.StringValue("custom"),
				Description: types.StringValue("GitHub App"),
			},
		},
		{
			name: "paperclip_connection",
			r:    NewPaperclipConnectionResource(),
			val: &PaperclipConnectionResourceModel{
				ID:        types.StringValue("conn-1"),
				CompanyID: types.StringValue("comp-1"),
				AppID:     types.StringValue("app-1"),
				Name:      types.StringValue("prod-github"),
			},
		},
		{
			name: "paperclip_policy",
			r:    NewPaperclipPolicyResource(),
			val: &PaperclipPolicyResourceModel{
				ID:         types.StringValue("pol-1"),
				CompanyID:  types.StringValue("comp-1"),
				Name:       types.StringValue("require-approval"),
				PolicyType: types.StringValue("tool_execution"),
				Rules:      types.StringValue(`{"allow":true}`),
			},
		},
		{
			name: "paperclip_stdio_template",
			r:    NewPaperclipStdioTemplateResource(),
			val: &PaperclipStdioTemplateResourceModel{
				ID:          types.StringValue("tmpl-1"),
				CompanyID:   types.StringValue("comp-1"),
				Name:        types.StringValue("bash-template"),
				Description: types.StringValue("Desc"),
				Command:     types.StringValue("bash"),
				Args:        types.ListNull(types.StringType),
				EnvKeys:     types.ListNull(types.StringType),
			},
		},
		{
			name: "paperclip_connection_install",
			r:    NewPaperclipConnectionInstallResource(),
			val: &PaperclipConnectionInstallResourceModel{
				ID:           types.StringValue("conn-1"),
				ConnectionID: types.StringValue("conn-1"),
				TargetType:   types.StringValue("agent"),
				AgentIDs:     types.ListNull(types.StringType),
			},
		},
		{
			name: "paperclip_app_permission",
			r:    NewPaperclipAppPermissionResource(),
			val: &PaperclipAppPermissionResourceModel{
				ID:                      types.StringValue("comp-1/conn-1/agent/agent-1"),
				CompanyID:               types.StringValue("comp-1"),
				ConnectionID:            types.StringValue("conn-1"),
				AccessType:              types.StringValue("agent"),
				ToolSelection:           types.StringValue(`{"mode":"all"}`),
				EnabledCatalogEntryIDs:  types.ListNull(types.StringType),
				AskFirstCatalogEntryIDs: types.ListNull(types.StringType),
				ReviewedCatalogEntryIDs: types.ListNull(types.StringType),
				AgentIDs:                types.ListNull(types.StringType),
			},
		},
	}

	for _, tc := range resources {
		t.Run(tc.name, func(t *testing.T) {
			var sResp resource.SchemaResponse
			tc.r.Schema(ctx, resource.SchemaRequest{}, &sResp)
			if sResp.Diagnostics.HasError() {
				t.Fatalf("schema error: %v", sResp.Diagnostics)
			}

			// Validate state serialization and deserialization
			state := tfsdk.State{Schema: sResp.Schema}
			diags := state.Set(ctx, tc.val)
			if diags.HasError() {
				t.Fatalf("failed to set prior state: %v", diags)
			}

			// Validate plan against state (zero-change plan)
			plan := tfsdk.Plan{Schema: sResp.Schema}
			diags = plan.Set(ctx, tc.val)
			if diags.HasError() {
				t.Fatalf("failed to set plan from state: %v", diags)
			}

			// If resource supports ModifyPlan, call it
			if rm, ok := tc.r.(resource.ResourceWithModifyPlan); ok {
				var modResp resource.ModifyPlanResponse
				modResp.Plan = plan
				rm.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: state}, &modResp)
				if modResp.Diagnostics.HasError() {
					t.Fatalf("ModifyPlan reported error: %v", modResp.Diagnostics)
				}
				if len(modResp.RequiresReplace) > 0 {
					t.Fatalf("unexpected replacement required for existing state: %v", modResp.RequiresReplace)
				}
			}
		})
	}
}
