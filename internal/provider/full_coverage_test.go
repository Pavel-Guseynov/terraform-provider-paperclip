package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.guseynov.org/guseynov/paperclip/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resource_schema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

var _ = client.ErrNotFound

func TestFullCoverageDataSources(t *testing.T) {
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/agents":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{
					"id": "ag-1", "companyId": "comp-1", "name": "Agent Full", "role": "engineer",
					"title": "Senior Engineer", "adapterType": "antigravity", "status": "active",
					"runtimeConfig": map[string]interface{}{"mem": "4G"},
					"adapterConfig": map[string]interface{}{"debug": true},
					"metadata":      map[string]interface{}{"env": "prod"},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/ag-1/files/AGENTS.md":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"content": "Follow all rules."})
		case r.Method == http.MethodGet && r.URL.Path == "/api/projects/proj-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "proj-1", "companyId": "comp-1", "name": "Project Full",
				"description": "Full description", "leadAgentId": "ag-1",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/projects":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{
					"id": "proj-1", "companyId": "comp-1", "name": "Project Full",
					"description": "Full description", "leadAgentId": "ag-1",
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/skills":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{
					"id": "sk-1", "companyId": "comp-1", "name": "Skill Full", "slug": "skill-full",
					"description": "Full skill description", "content": "Skill content markdown",
					"schemaVersion": 2,
					"metadata":      map[string]interface{}{"author": "tester"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	// 1. Agent Data Source: Name + CompanyID with all optional attributes populated
	dsAgent := NewPaperclipAgentDataSource().(*PaperclipAgentDataSource)
	agentSchema := configureTestDataSource(t, srv.URL, dsAgent)
	var agentResp datasource.ReadResponse
	agentResp.State = tfsdk.State{Schema: agentSchema}
	planAgent := tfsdk.Plan{Schema: agentSchema}
	_ = planAgent.Set(ctx, &PaperclipAgentDataSourceModel{
		CompanyID: types.StringValue("comp-1"),
		Name:      types.StringValue("Agent Full"),
	})
	dsAgent.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: agentSchema, Raw: planAgent.Raw}}, &agentResp)
	if agentResp.Diagnostics.HasError() {
		t.Fatalf("unexpected agent ds error: %v", agentResp.Diagnostics)
	}

	// Agent Not Found in company
	var agentMissingResp datasource.ReadResponse
	agentMissingResp.State = tfsdk.State{Schema: agentSchema}
	planAgentMissing := tfsdk.Plan{Schema: agentSchema}
	_ = planAgentMissing.Set(ctx, &PaperclipAgentDataSourceModel{
		CompanyID: types.StringValue("comp-1"),
		Name:      types.StringValue("Nonexistent"),
	})
	dsAgent.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: agentSchema, Raw: planAgentMissing.Raw}}, &agentMissingResp)
	if !agentMissingResp.Diagnostics.HasError() {
		t.Fatal("expected error for nonexistent agent in company")
	}

	// Agent Invalid Arguments (neither id nor name+company_id)
	var agentInvalidArgsResp datasource.ReadResponse
	agentInvalidArgsResp.State = tfsdk.State{Schema: agentSchema}
	planAgentInvalid := tfsdk.Plan{Schema: agentSchema}
	_ = planAgentInvalid.Set(ctx, &PaperclipAgentDataSourceModel{})
	dsAgent.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: agentSchema, Raw: planAgentInvalid.Raw}}, &agentInvalidArgsResp)
	if !agentInvalidArgsResp.Diagnostics.HasError() {
		t.Fatal("expected error for missing agent arguments")
	}

	// 2. Project Data Source: ID with Description & LeadAgentID populated
	dsProj := NewPaperclipProjectDataSource().(*PaperclipProjectDataSource)
	projSchema := configureTestDataSource(t, srv.URL, dsProj)
	var projResp datasource.ReadResponse
	projResp.State = tfsdk.State{Schema: projSchema}
	planProj := tfsdk.Plan{Schema: projSchema}
	_ = planProj.Set(ctx, &PaperclipProjectDataSourceModel{
		ID: types.StringValue("proj-1"),
	})
	dsProj.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: projSchema, Raw: planProj.Raw}}, &projResp)
	if projResp.Diagnostics.HasError() {
		t.Fatalf("unexpected proj ds error: %v", projResp.Diagnostics)
	}

	// Project Data Source: CompanyID + Name with Description & LeadAgentID
	var projNameResp datasource.ReadResponse
	projNameResp.State = tfsdk.State{Schema: projSchema}
	planProjName := tfsdk.Plan{Schema: projSchema}
	_ = planProjName.Set(ctx, &PaperclipProjectDataSourceModel{
		CompanyID: types.StringValue("comp-1"),
		Name:      types.StringValue("Project Full"),
	})
	dsProj.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: projSchema, Raw: planProjName.Raw}}, &projNameResp)
	if projNameResp.Diagnostics.HasError() {
		t.Fatalf("unexpected proj name ds error: %v", projNameResp.Diagnostics)
	}

	// Project Data Source: Invalid arguments (neither ID nor CompanyID+Name)
	var projInvalidResp datasource.ReadResponse
	projInvalidResp.State = tfsdk.State{Schema: projSchema}
	planProjInvalid := tfsdk.Plan{Schema: projSchema}
	_ = planProjInvalid.Set(ctx, &PaperclipProjectDataSourceModel{
		CompanyID: types.StringValue("comp-1"),
	})
	dsProj.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: projSchema, Raw: planProjInvalid.Raw}}, &projInvalidResp)
	if !projInvalidResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid arguments for project data source")
	}

	// 3. Skill Data Source: company_id is required
	dsSkill := NewPaperclipSkillDataSource().(*PaperclipSkillDataSource)
	skillSchema := configureTestDataSource(t, srv.URL, dsSkill)
	var skillEmptyResp datasource.ReadResponse
	skillEmptyResp.State = tfsdk.State{Schema: skillSchema}
	planSkillEmpty := tfsdk.Plan{Schema: skillSchema}
	_ = planSkillEmpty.Set(ctx, &PaperclipSkillDataSourceModel{})
	dsSkill.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: skillSchema, Raw: planSkillEmpty.Raw}}, &skillEmptyResp)
	if !skillEmptyResp.Diagnostics.HasError() {
		t.Fatal("expected error when company_id is empty")
	}

	// Skill Data Source: Slug with Description, Content, SchemaVersion > 0
	var skillResp datasource.ReadResponse
	skillResp.State = tfsdk.State{Schema: skillSchema}
	planSkill := tfsdk.Plan{Schema: skillSchema}
	_ = planSkill.Set(ctx, &PaperclipSkillDataSourceModel{
		CompanyID: types.StringValue("comp-1"),
		Slug:      types.StringValue("skill-full"),
	})
	dsSkill.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: skillSchema, Raw: planSkill.Raw}}, &skillResp)
	if skillResp.Diagnostics.HasError() {
		t.Fatalf("unexpected skill ds error: %v", skillResp.Diagnostics)
	}
}

func TestFullCoverageAgentRemaining(t *testing.T) {
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && (r.URL.Path == "/api/agents" || r.URL.Path == "/api/companies/comp-1/agents"):
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["name"] == "ag-inst-err" {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"id": "ag-inst-err", "name": "ag-inst-err", "role": "engineer", "adapterType": "antigravity",
				})
			} else if body["name"] == "ag-perm-err" {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"id": "ag-perm-err", "name": "ag-perm-err", "role": "engineer", "adapterType": "antigravity",
				})
			} else {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"id": "ag-unk", "name": "ag-unk", "role": "", "title": "",
					"adapterType": "", "status": "idle",
				})
			}
		case r.Method == http.MethodPatch && r.URL.Path == "/api/agents/ag-perm-err/permissions":
			http.Error(w, "permissions error", http.StatusInternalServerError)
		case r.Method == http.MethodPut && (r.URL.Path == "/api/agents/ag-inst-err/files/AGENTS.md" || r.URL.Path == "/api/agents/ag-inst-err/instructions-bundle/file"):
			http.Error(w, "instructions error", http.StatusInternalServerError)
		case (r.Method == http.MethodPatch || r.Method == http.MethodGet) && r.URL.Path == "/api/agents/ag-unk/permissions":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"canAssignTasks": false, "canCreateAgents": false})
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/ag-unk":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "ag-unk", "name": "ag-unk", "role": "", "title": "",
				"adapterType": "", "status": "idle",
				"permissions": map[string]interface{}{
					"canAssignTasks": false, "canCreateAgents": false,
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/ag-empty":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "ag-empty", "name": "ag-empty", "role": "", "title": "", "adapterType": "",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/ag-empty/files/AGENTS.md":
			w.WriteHeader(http.StatusNotFound)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/agents/ag-upd-inst-err":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "ag-upd-inst-err", "name": "ag"})
		case r.Method == http.MethodPut && (r.URL.Path == "/api/agents/ag-upd-inst-err/files/AGENTS.md" || r.URL.Path == "/api/agents/ag-upd-inst-err/instructions-bundle/file"):
			http.Error(w, "instructions update error", http.StatusInternalServerError)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/agents/ag-upd-perm-err":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "ag-upd-perm-err", "name": "ag"})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/agents/ag-upd-perm-err/permissions":
			http.Error(w, "permissions error", http.StatusInternalServerError)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/agents/ag-upd-no-title":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "ag-upd-no-title", "name": "ag", "role": "eng", "title": "", "adapterType": "antigravity",
			})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/agents/ag-upd-no-title/permissions":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"canAssignTasks": false, "canCreateAgents": false})
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/ag-upd-no-title/permissions":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"canAssignTasks": false, "canCreateAgents": false})
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/ag-upd-no-title":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "ag-upd-no-title", "name": "ag", "role": "eng", "title": "", "adapterType": "antigravity",
				"permissions": map[string]interface{}{
					"canAssignTasks": false, "canCreateAgents": false,
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	r := NewPaperclipAgentResource().(*PaperclipAgentResource)
	schema := configureTestResource(t, srv.URL, r)

	// 1. Create with instructions error (line 248)
	planInstErr := tfsdk.Plan{Schema: schema}
	_ = planInstErr.Set(ctx, &PaperclipAgentResourceModel{
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("ag-inst-err"),
		Role:            types.StringValue("engineer"),
		AdapterType:     types.StringValue("antigravity"),
		Instructions:    types.StringValue("some rules"),
		CanAssignTasks:  types.BoolValue(false),
		CanCreateAgents: types.BoolValue(false),
	})
	var createResp1 resource.CreateResponse
	createResp1.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: planInstErr}, &createResp1)
	if !createResp1.Diagnostics.HasError() {
		t.Fatal("expected error on instructions creation")
	}

	// 1b. Create with permissions reconciliation error (line 254)
	planPermErr := tfsdk.Plan{Schema: schema}
	_ = planPermErr.Set(ctx, &PaperclipAgentResourceModel{
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("ag-perm-err"),
		Role:            types.StringValue("engineer"),
		AdapterType:     types.StringValue("antigravity"),
		Instructions:    types.StringNull(),
		CanAssignTasks:  types.BoolValue(false),
		CanCreateAgents: types.BoolValue(false),
	})
	var createRespPermErr resource.CreateResponse
	createRespPermErr.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: planPermErr}, &createRespPermErr)
	if !createRespPermErr.Diagnostics.HasError() {
		t.Fatal("expected error on permissions reconciliation in create")
	}

	// 2. Create with unknown attributes and populated response (lines 264-289)
	planUnk := tfsdk.Plan{Schema: schema}
	_ = planUnk.Set(ctx, &PaperclipAgentResourceModel{
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("ag-unk"),
		Role:            types.StringUnknown(),
		Title:           types.StringUnknown(),
		AdapterType:     types.StringUnknown(),
		RuntimeConfig:   types.StringUnknown(),
		AdapterConfig:   types.StringUnknown(),
		Metadata:        types.StringUnknown(),
		Instructions:    types.StringUnknown(),
		CanAssignTasks:  types.BoolValue(false),
		CanCreateAgents: types.BoolValue(false),
	})
	var createResp2 resource.CreateResponse
	createResp2.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: planUnk}, &createResp2)
	if createResp2.Diagnostics.HasError() {
		t.Fatalf("unexpected error on unknown attributes: %v", createResp2.Diagnostics)
	}

	// 3. Read where agent has empty Role, empty AdapterType, empty Title (lines 318, 323, 328)
	var readResp resource.ReadResponse
	readResp.State = tfsdk.State{Schema: schema}
	stateEmpty := tfsdk.State{Schema: schema}
	_ = stateEmpty.Set(ctx, &PaperclipAgentResourceModel{
		ID:        types.StringValue("ag-empty"),
		CompanyID: types.StringValue("comp-1"),
	})
	r.Read(ctx, resource.ReadRequest{State: stateEmpty}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("unexpected read error: %v", readResp.Diagnostics)
	}

	// 4. Update with instructions update error (line 423)
	planUpdInstErr := tfsdk.Plan{Schema: schema}
	_ = planUpdInstErr.Set(ctx, &PaperclipAgentResourceModel{
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("ag"),
		Role:            types.StringValue("eng"),
		AdapterType:     types.StringValue("antigravity"),
		Instructions:    types.StringValue("new instructions"),
		CanAssignTasks:  types.BoolValue(false),
		CanCreateAgents: types.BoolValue(false),
	})
	stateUpdInstErr := tfsdk.State{Schema: schema}
	_ = stateUpdInstErr.Set(ctx, &PaperclipAgentResourceModel{
		ID: types.StringValue("ag-upd-inst-err"),
	})
	var updResp1 resource.UpdateResponse
	updResp1.State = stateUpdInstErr
	r.Update(ctx, resource.UpdateRequest{Plan: planUpdInstErr, State: stateUpdInstErr}, &updResp1)
	if !updResp1.Diagnostics.HasError() {
		t.Fatal("expected error on instructions update")
	}

	// 5. Update with permissions error (line 429)
	planUpdPermErr := tfsdk.Plan{Schema: schema}
	_ = planUpdPermErr.Set(ctx, &PaperclipAgentResourceModel{
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("ag"),
		Role:            types.StringValue("eng"),
		AdapterType:     types.StringValue("antigravity"),
		Instructions:    types.StringNull(),
		CanAssignTasks:  types.BoolValue(false),
		CanCreateAgents: types.BoolValue(false),
	})
	stateUpdPermErr := tfsdk.State{Schema: schema}
	_ = stateUpdPermErr.Set(ctx, &PaperclipAgentResourceModel{
		ID: types.StringValue("ag-upd-perm-err"),
	})
	var updResp2 resource.UpdateResponse
	updResp2.State = stateUpdPermErr
	r.Update(ctx, resource.UpdateRequest{Plan: planUpdPermErr, State: stateUpdPermErr}, &updResp2)
	if !updResp2.Diagnostics.HasError() {
		t.Fatal("expected error on permissions update")
	}

	// 6. Update where updated agent has Title == "" (line 439)
	planNoTitle := tfsdk.Plan{Schema: schema}
	_ = planNoTitle.Set(ctx, &PaperclipAgentResourceModel{
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("ag"),
		Role:            types.StringValue("eng"),
		Title:           types.StringNull(),
		AdapterType:     types.StringValue("antigravity"),
		CanAssignTasks:  types.BoolValue(false),
		CanCreateAgents: types.BoolValue(false),
	})
	stateNoTitle := tfsdk.State{Schema: schema}
	_ = stateNoTitle.Set(ctx, &PaperclipAgentResourceModel{
		ID: types.StringValue("ag-upd-no-title"),
	})
	var updResp3 resource.UpdateResponse
	updResp3.State = stateNoTitle
	r.Update(ctx, resource.UpdateRequest{Plan: planNoTitle, State: stateNoTitle}, &updResp3)
	if updResp3.Diagnostics.HasError() {
		t.Fatalf("unexpected error on title null update: %v", updResp3.Diagnostics)
	}
}

func TestFullCoverageCompanyRemaining(t *testing.T) {
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies":
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["name"] == "Company NoDesc" {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"id": "comp-nodesc", "name": "Company NoDesc", "description": "",
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "comp-desc", "name": "Company Desc", "description": "Has description",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-nodesc":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "comp-nodesc", "name": "Company NoDesc", "description": "",
			})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/companies/comp-nodesc":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "comp-nodesc", "name": "Company NoDesc", "description": "",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-desc":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "comp-desc", "name": "Company Desc", "description": "Has description",
			})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/companies/comp-desc":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "comp-desc", "name": "Company Desc", "description": "Updated description",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	r := NewPaperclipCompanyResource().(*PaperclipCompanyResource)
	schema := configureTestResource(t, srv.URL, r)

	// Create with description (line 108)
	plan := tfsdk.Plan{Schema: schema}
	_ = plan.Set(ctx, &PaperclipCompanyResourceModel{
		Name:        types.StringValue("Company Desc"),
		Description: types.StringValue("Has description"),
	})
	var createResp resource.CreateResponse
	createResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on company create: %v", createResp.Diagnostics)
	}

	// Read with description (line 137)
	var readResp resource.ReadResponse
	readResp.State = createResp.State
	r.Read(ctx, resource.ReadRequest{State: createResp.State}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on company read: %v", readResp.Diagnostics)
	}

	// Update with description (line 165)
	var updResp resource.UpdateResponse
	updResp.State = createResp.State
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: createResp.State}, &updResp)
	if updResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on company update: %v", updResp.Diagnostics)
	}

	// Create without description (line 108)
	planNoDesc := tfsdk.Plan{Schema: schema}
	_ = planNoDesc.Set(ctx, &PaperclipCompanyResourceModel{
		Name:        types.StringValue("Company NoDesc"),
		Description: types.StringNull(),
	})
	var createNoDescResp resource.CreateResponse
	createNoDescResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: planNoDesc}, &createNoDescResp)
	if createNoDescResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on company create no desc: %v", createNoDescResp.Diagnostics)
	}

	// Read without description (line 137)
	var readNoDescResp resource.ReadResponse
	readNoDescResp.State = createNoDescResp.State
	r.Read(ctx, resource.ReadRequest{State: createNoDescResp.State}, &readNoDescResp)
	if readNoDescResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on company read no desc: %v", readNoDescResp.Diagnostics)
	}

	// Update without description (line 165)
	var updNoDescResp resource.UpdateResponse
	updNoDescResp.State = createNoDescResp.State
	r.Update(ctx, resource.UpdateRequest{Plan: planNoDesc, State: createNoDescResp.State}, &updNoDescResp)
	if updNoDescResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on company update no desc: %v", updNoDescResp.Diagnostics)
	}
}

func TestFullCoverageConnectionRemaining(t *testing.T) {
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/tool-connections":
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["name"] == "conn-err" {
				http.Error(w, "creation error", http.StatusInternalServerError)
				return
			}
			if body["name"] == "conn-no-app" {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"id": "conn-no-app", "name": "conn-no-app", "appId": "",
				})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "conn-auth", "name": "conn-auth", "appId": "app-1", "authKind": "api_key",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/comp-1/tools/apps/connect":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"connectionId": "conn-no-app",
				"connection": map[string]interface{}{
					"id": "conn-no-app", "name": "conn-no-app", "appId": "",
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/tool-connections/conn-500":
			http.Error(w, "connection 500", http.StatusInternalServerError)
		case r.Method == http.MethodGet && r.URL.Path == "/api/tool-connections/conn-bad-refs":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "conn-bad-refs", "name": "conn-bad-refs", "appId": "app-1",
				"credentialRefs": []map[string]interface{}{{"invalid": true}},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/tool-connections/conn-grant-500":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "conn-grant-500", "name": "conn-grant-500", "appId": "app-1",
				"credentialRefs": []map[string]interface{}{{"name": "key", "secretId": "sec-1"}},
			})
		case r.Method == http.MethodGet && (r.URL.Path == "/api/tool-connections/conn-grant-500/grants" || r.URL.Path == "/api/tool-connections/conn-grant-500/default-grant"):
			http.Error(w, "grants error", http.StatusInternalServerError)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/tool-connections/conn-patch-err":
			http.Error(w, "update error", http.StatusInternalServerError)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/tool-connections/conn-grant-err":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "conn-grant-err", "name": "conn-grant-err", "appId": "app-1", "authKind": "api_key",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/tool-connections/conn-grant-err/grants":
			http.Error(w, "grant reconcile error", http.StatusInternalServerError)
		case r.Method == http.MethodPost && r.URL.Path == "/api/tool-connections/conn-auth/grants":
			http.Error(w, "grant reconcile error", http.StatusInternalServerError)
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies":
			http.Error(w, "companies 500", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	r := NewPaperclipConnectionResource().(*PaperclipConnectionResource)
	schema := configureTestResource(t, srv.URL, r)

	validSecRefs := types.StringValue("[{\"name\":\"key\",\"secretId\":\"sec-1\"}]")

	// 1. CreateConnection error (line 228)
	planErr := tfsdk.Plan{Schema: schema}
	_ = planErr.Set(ctx, &PaperclipConnectionResourceModel{
		CompanyID: types.StringValue("comp-1"),
		Name:      types.StringValue("conn-err"),
		Transport: types.StringValue("stdio"),
	})
	var createErrResp resource.CreateResponse
	createErrResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: planErr}, &createErrResp)
	if !createErrResp.Diagnostics.HasError() {
		t.Fatal("expected error on connection create")
	}

	// 1b. CreateConnection invalid credential refs (line 215)
	planBadRefs := tfsdk.Plan{Schema: schema}
	_ = planBadRefs.Set(ctx, &PaperclipConnectionResourceModel{
		CompanyID:      types.StringValue("comp-1"),
		Name:           types.StringValue("conn-bad-refs"),
		AppID:          types.StringValue("app-1"),
		Transport:      types.StringValue("stdio"),
		CredentialRefs: types.StringValue("[{\"bad\": true}]"),
	})
	var createBadRefsResp resource.CreateResponse
	createBadRefsResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: planBadRefs}, &createBadRefsResp)
	if !createBadRefsResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid credential refs in connection create")
	}

	// 2a. Create with AppID == "" and plan.AppID.IsUnknown() (line 240)
	planNoApp := tfsdk.Plan{Schema: schema}
	_ = planNoApp.Set(ctx, &PaperclipConnectionResourceModel{
		CompanyID:  types.StringValue("comp-1"),
		Name:       types.StringValue("conn-no-app"),
		GalleryKey: types.StringValue("my-gallery"),
		AppID:      types.StringUnknown(),
		Transport:  types.StringValue("stdio"),
	})
	var createNoAppResp resource.CreateResponse
	createNoAppResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: planNoApp}, &createNoAppResp)
	if createNoAppResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on connection create no app: %v", createNoAppResp.Diagnostics)
	}

	// 2b. Create with state.Set diags error (line 264)
	badStateSchema := resource_schema.Schema{
		Attributes: map[string]resource_schema.Attribute{
			"id": resource_schema.Int64Attribute{Computed: true},
		},
	}
	var createStateErrResp resource.CreateResponse
	createStateErrResp.State = tfsdk.State{Schema: badStateSchema}
	r.Create(ctx, resource.CreateRequest{Plan: planNoApp}, &createStateErrResp)
	if !createStateErrResp.Diagnostics.HasError() {
		t.Fatal("expected error when state.Set fails during create")
	}

	// 2c. Create with credential grant error
	planAuth := tfsdk.Plan{Schema: schema}
	_ = planAuth.Set(ctx, &PaperclipConnectionResourceModel{
		CompanyID:      types.StringValue("comp-1"),
		Name:           types.StringValue("conn-auth"),
		Transport:      types.StringValue("stdio"),
		AuthKind:       types.StringValue("api_key"),
		CredentialRefs: validSecRefs,
	})
	var createAuthResp resource.CreateResponse
	createAuthResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: planAuth}, &createAuthResp)
	if !createAuthResp.Diagnostics.HasError() {
		t.Fatal("expected error on credential grant during connection create")
	}

	// 3a. Read with connection 500 (line 291)
	state500 := tfsdk.State{Schema: schema}
	_ = state500.Set(ctx, &PaperclipConnectionResourceModel{
		ID:        types.StringValue("conn-500"),
		CompanyID: types.StringValue("comp-1"),
		AppID:     types.StringValue("app-1"),
	})
	var read500Resp resource.ReadResponse
	read500Resp.State = state500
	r.Read(ctx, resource.ReadRequest{State: state500}, &read500Resp)
	if !read500Resp.Diagnostics.HasError() {
		t.Fatal("expected error on connection 500 during read")
	}

	// 3b. Read with invalid credential refs (line 337)
	stateBadRefs := tfsdk.State{Schema: schema}
	_ = stateBadRefs.Set(ctx, &PaperclipConnectionResourceModel{
		ID:        types.StringValue("conn-bad-refs"),
		CompanyID: types.StringValue("comp-1"),
		AppID:     types.StringValue("app-1"),
	})
	var readBadRefsResp resource.ReadResponse
	readBadRefsResp.State = stateBadRefs
	r.Read(ctx, resource.ReadRequest{State: stateBadRefs}, &readBadRefsResp)
	if !readBadRefsResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid native credential refs during read")
	}

	// 3c. Read with default grant 500 (line 342)
	stateGrant500 := tfsdk.State{Schema: schema}
	_ = stateGrant500.Set(ctx, &PaperclipConnectionResourceModel{
		ID:        types.StringValue("conn-grant-500"),
		CompanyID: types.StringValue("comp-1"),
		AppID:     types.StringValue("app-1"),
	})
	var readGrant500Resp resource.ReadResponse
	readGrant500Resp.State = stateGrant500
	r.Read(ctx, resource.ReadRequest{State: stateGrant500}, &readGrant500Resp)
	if !readGrant500Resp.Diagnostics.HasError() {
		t.Fatal("expected error on default grant 500 during read")
	}

	// 4. UpdateConnection error (line 395)
	statePatchErr := tfsdk.State{Schema: schema}
	_ = statePatchErr.Set(ctx, &PaperclipConnectionResourceModel{
		ID:        types.StringValue("conn-patch-err"),
		CompanyID: types.StringValue("comp-1"),
	})
	var updErrResp resource.UpdateResponse
	updErrResp.State = statePatchErr
	r.Update(ctx, resource.UpdateRequest{Plan: planErr, State: statePatchErr}, &updErrResp)
	if !updErrResp.Diagnostics.HasError() {
		t.Fatal("expected error on connection update")
	}

	// 4b. UpdateConnection invalid credential refs (line 387)
	planUpdBadRefs := tfsdk.Plan{Schema: schema}
	_ = planUpdBadRefs.Set(ctx, &PaperclipConnectionResourceModel{
		CompanyID:      types.StringValue("comp-1"),
		Name:           types.StringValue("conn-patch-err"),
		AppID:          types.StringValue("app-1"),
		Transport:      types.StringValue("stdio"),
		CredentialRefs: types.StringValue("[{\"bad\": true}]"),
	})
	var updBadRefsResp resource.UpdateResponse
	updBadRefsResp.State = statePatchErr
	r.Update(ctx, resource.UpdateRequest{Plan: planUpdBadRefs, State: statePatchErr}, &updBadRefsResp)
	if !updBadRefsResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid credential refs in connection update")
	}

	// 5. Update with AuthKind != "" and credential grant error (lines 407, 412)
	planGrantErr := tfsdk.Plan{Schema: schema}
	_ = planGrantErr.Set(ctx, &PaperclipConnectionResourceModel{
		CompanyID:      types.StringValue("comp-1"),
		Name:           types.StringValue("conn-grant-err"),
		Transport:      types.StringValue("stdio"),
		CredentialRefs: validSecRefs,
	})
	stateGrantErr := tfsdk.State{Schema: schema}
	_ = stateGrantErr.Set(ctx, &PaperclipConnectionResourceModel{
		ID:             types.StringValue("conn-grant-err"),
		CompanyID:      types.StringValue("comp-1"),
		CredentialRefs: validSecRefs,
	})
	var updGrantErrResp resource.UpdateResponse
	updGrantErrResp.State = stateGrantErr
	r.Update(ctx, resource.UpdateRequest{Plan: planGrantErr, State: stateGrantErr}, &updGrantErrResp)
	if !updGrantErrResp.Diagnostics.HasError() {
		t.Fatal("expected error on credential grant during connection update")
	}
}

func TestFullCoverageConnectionInstallRemaining(t *testing.T) {
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if (r.Method == http.MethodPut || r.Method == http.MethodGet) && r.URL.Path == "/api/tool-connections/conn-inst/installs" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"installs": []interface{}{}})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	r := NewPaperclipConnectionInstallResource().(*PaperclipConnectionInstallResource)
	schema := configureTestResource(t, srv.URL, r)

	// Create with TargetType == "" defaulting to agent (lines 99, 105)
	planDefault := tfsdk.Plan{Schema: schema}
	_ = planDefault.Set(ctx, &PaperclipConnectionInstallResourceModel{
		ID:           types.StringNull(),
		ConnectionID: types.StringValue("conn-inst"),
		CompanyID:    types.StringValue("comp-1"),
		TargetType:   types.StringNull(),
		AgentIDs:     types.ListNull(types.StringType),
	})
	var createResp resource.CreateResponse
	createResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: planDefault}, &createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on install default targetType: %v", createResp.Diagnostics)
	}

	// Update with TargetType == "" defaulting to agent (lines 189, 195)
	var updResp resource.UpdateResponse
	updResp.State = createResp.State
	r.Update(ctx, resource.UpdateRequest{Plan: planDefault, State: createResp.State}, &updResp)
	if updResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on install update default targetType: %v", updResp.Diagnostics)
	}

	// Diagnostics errors with raw invalid config
	badPlan := tfsdk.Plan{Schema: schema, Raw: tftypes.NewValue(tftypes.String, "invalid")}
	var badCreateResp resource.CreateResponse
	r.Create(ctx, resource.CreateRequest{Plan: badPlan}, &badCreateResp)
	if !badCreateResp.Diagnostics.HasError() {
		t.Fatal("expected error on bad plan create")
	}

	badState := tfsdk.State{Schema: schema, Raw: tftypes.NewValue(tftypes.String, "invalid")}
	var badReadResp resource.ReadResponse
	r.Read(ctx, resource.ReadRequest{State: badState}, &badReadResp)
	if !badReadResp.Diagnostics.HasError() {
		t.Fatal("expected error on bad state read")
	}

	var badUpdResp resource.UpdateResponse
	r.Update(ctx, resource.UpdateRequest{Plan: badPlan, State: badState}, &badUpdResp)
	if !badUpdResp.Diagnostics.HasError() {
		t.Fatal("expected error on bad plan update")
	}

	// Element conversion diagnostics errors (lines 99, 163, 189)
	badInstallElemSchema := resource_schema.Schema{
		Attributes: map[string]resource_schema.Attribute{
			"id":            resource_schema.StringAttribute{Computed: true},
			"connection_id": resource_schema.StringAttribute{Required: true},
			"company_id":    resource_schema.StringAttribute{Optional: true},
			"target_type":   resource_schema.StringAttribute{Optional: true},
			"agent_ids":     resource_schema.ListAttribute{ElementType: types.Int64Type, Optional: true},
		},
	}
	intList, _ := types.ListValue(types.Int64Type, []attr.Value{types.Int64Value(123)})
	badElemPlan := tfsdk.Plan{Schema: badInstallElemSchema}
	_ = badElemPlan.Set(ctx, &struct {
		ID           types.String `tfsdk:"id"`
		ConnectionID types.String `tfsdk:"connection_id"`
		CompanyID    types.String `tfsdk:"company_id"`
		TargetType   types.String `tfsdk:"target_type"`
		AgentIDs     types.List   `tfsdk:"agent_ids"`
	}{
		ConnectionID: types.StringValue("conn-inst"),
		AgentIDs:     intList,
	})
	var elemCreateResp resource.CreateResponse
	r.Create(ctx, resource.CreateRequest{Plan: badElemPlan}, &elemCreateResp)
	if !elemCreateResp.Diagnostics.HasError() {
		t.Fatal("expected error on agent_ids element conversion in create")
	}

	badElemState := tfsdk.State{Schema: badInstallElemSchema}
	_ = badElemState.Set(ctx, &struct {
		ID           types.String `tfsdk:"id"`
		ConnectionID types.String `tfsdk:"connection_id"`
		CompanyID    types.String `tfsdk:"company_id"`
		TargetType   types.String `tfsdk:"target_type"`
		AgentIDs     types.List   `tfsdk:"agent_ids"`
	}{
		ID:           types.StringValue("conn-inst"),
		ConnectionID: types.StringValue("conn-inst"),
		AgentIDs:     intList,
	})
	var elemReadResp resource.ReadResponse
	r.Read(ctx, resource.ReadRequest{State: badElemState}, &elemReadResp)
	if !elemReadResp.Diagnostics.HasError() {
		t.Fatal("expected error on agent_ids element conversion in read")
	}

	var elemUpdResp resource.UpdateResponse
	r.Update(ctx, resource.UpdateRequest{Plan: badElemPlan, State: badElemState}, &elemUpdResp)
	if !elemUpdResp.Diagnostics.HasError() {
		t.Fatal("expected error on agent_ids element conversion in update")
	}
}

func TestFullCoverageLocalEnvironmentRemaining(t *testing.T) {
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/environments":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{{"id": "env-1", "companyId": "comp-1", "driver": "local"}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/environments/env-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "env-1", "companyId": "comp-1", "driver": "local", "envVars": map[string]interface{}{}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/comp-1/environments/env-1/inherit-path":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "env-1", "inherited": true})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	r := NewPaperclipLocalEnvironmentResource().(*PaperclipLocalEnvironmentResource)
	schema := configureTestResource(t, srv.URL, r)

	plan := tfsdk.Plan{Schema: schema}
	_ = plan.Set(ctx, &PaperclipLocalEnvironmentModel{
		CompanyID:          types.StringValue("comp-1"),
		InheritServicePath: types.BoolValue(true),
	})

	// State with incompatible schema causes state.Set to fail on line 83
	badEnvSchema := resource_schema.Schema{
		Attributes: map[string]resource_schema.Attribute{
			"id": resource_schema.Int64Attribute{Computed: true},
		},
	}
	badState := tfsdk.State{Schema: badEnvSchema}
	if err := r.apply(ctx, plan, &badState); err == nil {
		t.Fatal("expected error when state.Set fails")
	}
}

func TestFullCoverageProjectRemaining(t *testing.T) {
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	r := NewPaperclipProjectResource().(*PaperclipProjectResource)
	schema := configureTestResource(t, srv.URL, r)

	// Create with invalid workspace policy JSON (line 165)
	planInvalid := tfsdk.Plan{Schema: schema}
	_ = planInvalid.Set(ctx, &PaperclipProjectResourceModel{
		CompanyID:                types.StringValue("comp-1"),
		Name:                     types.StringValue("proj"),
		ExecutionWorkspacePolicy: types.StringValue("not valid json"),
	})
	var createResp resource.CreateResponse
	createResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: planInvalid}, &createResp)
	if !createResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid execution_workspace_policy in create")
	}

	// Update with invalid workspace policy JSON (line 229)
	state := tfsdk.State{Schema: schema}
	_ = state.Set(ctx, &PaperclipProjectResourceModel{
		ID:        types.StringValue("proj-1"),
		CompanyID: types.StringValue("comp-1"),
	})
	var updResp resource.UpdateResponse
	updResp.State = state
	r.Update(ctx, resource.UpdateRequest{Plan: planInvalid, State: state}, &updResp)
	if !updResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid execution_workspace_policy in update")
	}
}

func TestFullCoverageSkillRemaining(t *testing.T) {
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/comp-1/skills":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "sk-v2", "companyId": "comp-1", "slug": "sk-v2", "name": "Skill V2",
				"description": "Custom desc", "schemaVersion": 2, "content": "Skill markdown",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/skills/sk-v2":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "sk-v2", "companyId": "comp-1", "slug": "sk-v2", "name": "Skill V2",
				"description": "Custom desc", "schemaVersion": 2, "content": "Skill markdown",
			})
		case r.Method == http.MethodPatch && (r.URL.Path == "/api/companies/comp-1/skills/sk-v2" || r.URL.Path == "/api/companies/comp-1/skills/sk-v2/files"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "sk-v2", "companyId": "comp-1", "slug": "sk-v2", "name": "Skill V2",
				"description": "Updated desc", "schemaVersion": 2, "content": "Updated markdown",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	r := NewPaperclipSkillResource().(*PaperclipSkillResource)
	schema := configureTestResource(t, srv.URL, r)

	// Create with SchemaVersion > 0, Description != "" (lines 131, 154, 160)
	plan := tfsdk.Plan{Schema: schema}
	_ = plan.Set(ctx, &PaperclipSkillResourceModel{
		CompanyID:     types.StringValue("comp-1"),
		Name:          types.StringValue("Skill V2"),
		Description:   types.StringValue("Custom desc"),
		SchemaVersion: types.Int64Value(2),
		Content:       types.StringValue("Skill markdown"),
	})
	var createResp resource.CreateResponse
	createResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on skill create: %v", createResp.Diagnostics)
	}

	// Read with Description != "" and SchemaVersion > 0 (lines 193, 199)
	var readResp resource.ReadResponse
	readResp.State = createResp.State
	r.Read(ctx, resource.ReadRequest{State: createResp.State}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on skill read: %v", readResp.Diagnostics)
	}

	// Update with SchemaVersion > 0 and Description != "" (lines 220, 242, 248)
	var updResp resource.UpdateResponse
	updResp.State = createResp.State
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: createResp.State}, &updResp)
	if updResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on skill update: %v", updResp.Diagnostics)
	}
}

func TestFullCoverageStdioTemplateRemaining(t *testing.T) {
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/comp-1/tools/stdio-templates":
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["templateId"] == "tpl-err" {
				http.Error(w, "template create error", http.StatusInternalServerError)
				return
			}
			if body["templateId"] == "tpl-noid" {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"templateId": "tpl-noid", "name": "tpl-noid"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "tpl-1", "templateId": "tpl-1", "name": "tpl-1"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/tools/stdio-templates":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "", "templateId": "tpl-noid", "name": "noid"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-500/tools/stdio-templates":
			http.Error(w, "read error", http.StatusInternalServerError)
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/comp-1/tools/stdio-templates/tpl-by-id/disable":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	r := NewPaperclipStdioTemplateResource().(*PaperclipStdioTemplateResource)
	schema := configureTestResource(t, srv.URL, r)

	emptyList, _ := types.ListValue(types.StringType, []attr.Value{})

	// 1. Diagnostics error on bad plan Create (line 149)
	badPlan := tfsdk.Plan{Schema: schema, Raw: tftypes.NewValue(tftypes.String, "invalid")}
	var badCreateResp resource.CreateResponse
	r.Create(ctx, resource.CreateRequest{Plan: badPlan}, &badCreateResp)
	if !badCreateResp.Diagnostics.HasError() {
		t.Fatal("expected error on bad plan")
	}

	// 1b. Diagnostics error on invalid args element type Create (line 157)
	badArgsSchema := resource_schema.Schema{
		Attributes: map[string]resource_schema.Attribute{
			"id":          resource_schema.StringAttribute{Computed: true},
			"company_id":  resource_schema.StringAttribute{Required: true},
			"template_id": resource_schema.StringAttribute{Required: true},
			"name":        resource_schema.StringAttribute{Required: true},
			"description": resource_schema.StringAttribute{Optional: true},
			"command":     resource_schema.StringAttribute{Required: true},
			"args":        resource_schema.ListAttribute{ElementType: types.Int64Type, Optional: true},
			"env_keys":    resource_schema.ListAttribute{ElementType: types.StringType, Optional: true},
		},
	}
	intList, _ := types.ListValue(types.Int64Type, []attr.Value{types.Int64Value(123)})
	badArgsPlan := tfsdk.Plan{Schema: badArgsSchema}
	_ = badArgsPlan.Set(ctx, &struct {
		ID          types.String `tfsdk:"id"`
		CompanyID   types.String `tfsdk:"company_id"`
		TemplateID  types.String `tfsdk:"template_id"`
		Name        types.String `tfsdk:"name"`
		Description types.String `tfsdk:"description"`
		Command     types.String `tfsdk:"command"`
		Args        types.List   `tfsdk:"args"`
		EnvKeys     types.List   `tfsdk:"env_keys"`
	}{
		CompanyID:  types.StringValue("comp-1"),
		TemplateID: types.StringValue("tpl-int"),
		Name:       types.StringValue("int"),
		Command:    types.StringValue("echo"),
		Args:       intList,
		EnvKeys:    types.ListNull(types.StringType),
	})
	var badArgsResp resource.CreateResponse
	r.Create(ctx, resource.CreateRequest{Plan: badArgsPlan}, &badArgsResp)
	if !badArgsResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid args element type in create")
	}

	// 2. Create error on CreateStdioTemplate (line 172)
	planErr := tfsdk.Plan{Schema: schema}
	_ = planErr.Set(ctx, &PaperclipStdioTemplateResourceModel{
		ID:          types.StringNull(),
		CompanyID:   types.StringValue("comp-1"),
		TemplateID:  types.StringValue("tpl-err"),
		Name:        types.StringValue("err"),
		Description: types.StringNull(),
		Command:     types.StringValue("echo"),
		Args:        emptyList,
		EnvKeys:     emptyList,
	})
	var createErrResp resource.CreateResponse
	createErrResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: planErr}, &createErrResp)
	if !createErrResp.Diagnostics.HasError() {
		t.Fatal("expected error on template create failure")
	}

	// 3. Create where created.ID == "" falls back to TemplateID (line 179)
	planNoID := tfsdk.Plan{Schema: schema}
	_ = planNoID.Set(ctx, &PaperclipStdioTemplateResourceModel{
		ID:          types.StringNull(),
		CompanyID:   types.StringValue("comp-1"),
		TemplateID:  types.StringValue("tpl-noid"),
		Name:        types.StringValue("noid"),
		Description: types.StringNull(),
		Command:     types.StringValue("echo"),
		Args:        emptyList,
		EnvKeys:     emptyList,
	})
	var createNoIDResp resource.CreateResponse
	createNoIDResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: planNoID}, &createNoIDResp)
	if createNoIDResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on template no ID: %v", createNoIDResp.Diagnostics)
	}

	// 4. Read where state.TemplateID == "" and 404 removes resource (lines 196, 201)
	state404 := tfsdk.State{Schema: schema}
	_ = state404.Set(ctx, &PaperclipStdioTemplateResourceModel{
		ID:          types.StringValue("tpl-404"),
		TemplateID:  types.StringNull(),
		CompanyID:   types.StringValue("comp-1"),
		Name:        types.StringValue("404"),
		Description: types.StringNull(),
		Command:     types.StringValue("echo"),
		Args:        emptyList,
		EnvKeys:     emptyList,
	})
	var read404Resp resource.ReadResponse
	read404Resp.State = state404
	r.Read(ctx, resource.ReadRequest{State: state404}, &read404Resp)
	if !read404Resp.State.Raw.IsNull() {
		t.Fatal("expected state removed on 404")
	}

	// 5. Read where GetStdioTemplate returns 500 (line 206)
	state500 := tfsdk.State{Schema: schema}
	_ = state500.Set(ctx, &PaperclipStdioTemplateResourceModel{
		ID:          types.StringValue("tpl-500"),
		TemplateID:  types.StringValue("tpl-500"),
		CompanyID:   types.StringValue("comp-500"),
		Name:        types.StringValue("500"),
		Description: types.StringNull(),
		Command:     types.StringValue("echo"),
		Args:        emptyList,
		EnvKeys:     emptyList,
	})
	var read500Resp resource.ReadResponse
	read500Resp.State = state500
	r.Read(ctx, resource.ReadRequest{State: state500}, &read500Resp)
	if !read500Resp.Diagnostics.HasError() {
		t.Fatal("expected error on template read 500")
	}

	// 6. Read where tpl.ID == "" (line 216)
	stateTplNoID := tfsdk.State{Schema: schema}
	_ = stateTplNoID.Set(ctx, &PaperclipStdioTemplateResourceModel{
		ID:          types.StringValue("tpl-noid"),
		TemplateID:  types.StringValue("tpl-noid"),
		CompanyID:   types.StringValue("comp-1"),
		Name:        types.StringValue("noid"),
		Description: types.StringNull(),
		Command:     types.StringValue("echo"),
		Args:        emptyList,
		EnvKeys:     emptyList,
	})
	var readTplNoIDResp resource.ReadResponse
	readTplNoIDResp.State = stateTplNoID
	r.Read(ctx, resource.ReadRequest{State: stateTplNoID}, &readTplNoIDResp)
	if readTplNoIDResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on tpl no ID read: %v", readTplNoIDResp.Diagnostics)
	}

	// 7. Read diagnostics error on bad state (line 229)
	badState := tfsdk.State{Schema: schema, Raw: tftypes.NewValue(tftypes.String, "invalid")}
	var badReadResp resource.ReadResponse
	r.Read(ctx, resource.ReadRequest{State: badState}, &badReadResp)
	if !badReadResp.Diagnostics.HasError() {
		t.Fatal("expected error on bad state read")
	}

	// 8. Delete where state.TemplateID == "" falls back to state.ID (line 253)
	stateDel := tfsdk.State{Schema: schema}
	_ = stateDel.Set(ctx, &PaperclipStdioTemplateResourceModel{
		ID:          types.StringValue("tpl-by-id"),
		TemplateID:  types.StringNull(),
		CompanyID:   types.StringValue("comp-1"),
		Name:        types.StringValue("del"),
		Description: types.StringNull(),
		Command:     types.StringValue("echo"),
		Args:        emptyList,
		EnvKeys:     emptyList,
	})
	var delResp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: stateDel}, &delResp)
	if delResp.Diagnostics.HasError() {
		t.Fatalf("unexpected delete error: %v", delResp.Diagnostics)
	}
}

func TestFullCoverageWorkspaceRemaining(t *testing.T) {
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/projects/proj-err/workspaces":
			http.Error(w, "workspace create error", http.StatusInternalServerError)
		case r.Method == http.MethodGet && r.URL.Path == "/api/projects/proj-404/workspaces":
			_ = json.NewEncoder(w).Encode([]interface{}{})
		case r.Method == http.MethodGet && r.URL.Path == "/api/projects/proj-500/workspaces":
			http.Error(w, "workspace read error", http.StatusInternalServerError)
		case r.Method == http.MethodPatch && r.URL.Path == "/api/projects/proj-1/workspaces/ws-upd-err":
			http.Error(w, "workspace update error", http.StatusInternalServerError)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/projects/proj-1/workspaces/ws-del-err":
			http.Error(w, "workspace delete error", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	r := NewPaperclipWorkspaceResource().(*PaperclipWorkspaceResource)
	schema := configureTestResource(t, srv.URL, r)

	// 1. CreateWorkspace error (line 157)
	planErr := tfsdk.Plan{Schema: schema}
	_ = planErr.Set(ctx, &PaperclipWorkspaceResourceModel{
		ProjectID: types.StringValue("proj-err"),
		Name:      types.StringValue("ws-err"),
	})
	var createErrResp resource.CreateResponse
	createErrResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: planErr}, &createErrResp)
	if !createErrResp.Diagnostics.HasError() {
		t.Fatal("expected error on workspace create")
	}

	// 2. Read 404 removes resource (line 176)
	state404 := tfsdk.State{Schema: schema}
	_ = state404.Set(ctx, &PaperclipWorkspaceResourceModel{
		ID:        types.StringValue("ws-missing"),
		ProjectID: types.StringValue("proj-404"),
	})
	var read404Resp resource.ReadResponse
	read404Resp.State = state404
	r.Read(ctx, resource.ReadRequest{State: state404}, &read404Resp)
	if !read404Resp.State.Raw.IsNull() {
		t.Fatal("expected state removed on 404")
	}

	// 3. Read 500 adds error (line 181)
	state500 := tfsdk.State{Schema: schema}
	_ = state500.Set(ctx, &PaperclipWorkspaceResourceModel{
		ID:        types.StringValue("ws-500"),
		ProjectID: types.StringValue("proj-500"),
	})
	var read500Resp resource.ReadResponse
	read500Resp.State = state500
	r.Read(ctx, resource.ReadRequest{State: state500}, &read500Resp)
	if !read500Resp.Diagnostics.HasError() {
		t.Fatal("expected error on workspace read 500")
	}

	// 4. UpdateWorkspace error (line 206)
	planUpdErr := tfsdk.Plan{Schema: schema}
	_ = planUpdErr.Set(ctx, &PaperclipWorkspaceResourceModel{
		ProjectID: types.StringValue("proj-1"),
		Name:      types.StringValue("ws-upd"),
	})
	stateUpdErr := tfsdk.State{Schema: schema}
	_ = stateUpdErr.Set(ctx, &PaperclipWorkspaceResourceModel{
		ID:        types.StringValue("ws-upd-err"),
		ProjectID: types.StringValue("proj-1"),
	})
	var updErrResp resource.UpdateResponse
	updErrResp.State = stateUpdErr
	r.Update(ctx, resource.UpdateRequest{Plan: planUpdErr, State: stateUpdErr}, &updErrResp)
	if !updErrResp.Diagnostics.HasError() {
		t.Fatal("expected error on workspace update")
	}

	// 5. DeleteWorkspace error (line 226)
	stateDelErr := tfsdk.State{Schema: schema}
	_ = stateDelErr.Set(ctx, &PaperclipWorkspaceResourceModel{
		ID:        types.StringValue("ws-del-err"),
		ProjectID: types.StringValue("proj-1"),
	})
	var delErrResp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: stateDelErr}, &delErrResp)
	if !delErrResp.Diagnostics.HasError() {
		t.Fatal("expected error on workspace delete 500")
	}
}

func TestFullCoverageAppPermissionRemaining(t *testing.T) {
	ctx := context.Background()

	var lastConn string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/tool-connections/conn-cat-err":
			lastConn = "conn-cat-err"
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "conn-cat-err", "status": "active", "name": "conn-cat-err"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/tool-connections/conn-cat-err/catalog":
			http.Error(w, "catalog error", http.StatusInternalServerError)
		case r.Method == http.MethodGet && r.URL.Path == "/api/tool-connections/conn-cat-empty":
			lastConn = "conn-cat-empty"
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "conn-cat-empty", "status": "active", "name": "conn-cat-empty"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/tool-connections/conn-cat-empty/catalog":
			_ = json.NewEncoder(w).Encode([]interface{}{})
		case r.Method == http.MethodGet && r.URL.Path == "/api/tool-connections/conn-cat-ok":
			lastConn = "conn-cat-ok"
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "conn-cat-ok", "status": "active", "name": "conn-cat-ok"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/tool-connections/conn-cat-ok/catalog":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{{"id": "entry-1", "toolName": "read"}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/tool-connections/conn-err":
			lastConn = "conn-err"
			http.Error(w, "connection 500", http.StatusInternalServerError)
		case r.Method == http.MethodGet && r.URL.Path == "/api/tool-connections/conn-policy-err":
			lastConn = "conn-policy-err"
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "conn-policy-err", "status": "active", "name": "conn-policy-err"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/tool-connections/conn-policy-err/catalog":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{{"id": "entry-1", "toolName": "read", "isReadOnly": false}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/tool-connections/conn-state-err":
			lastConn = "conn-state-err"
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "conn-state-err", "status": "active", "name": "conn-state-err"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/tool-connections/conn-state-err/catalog":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{{"id": "entry-1", "toolName": "read", "isReadOnly": true}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/tool-connections/conn-verify-err":
			lastConn = "conn-verify-err"
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "conn-verify-err", "status": "active", "name": "conn-verify-err"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/tool-connections/conn-verify-err/catalog":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{{"id": "entry-1", "toolName": "read", "isReadOnly": true}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/tool-connections/conn-revoke-ok":
			lastConn = "conn-revoke-ok"
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "conn-revoke-ok", "status": "active", "name": "conn-revoke-ok"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/tool-connections/conn-revoke-ok/catalog":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{{"id": "entry-1", "toolName": "read"}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/tool-connections/conn-read-state-500":
			lastConn = "conn-read-state-500"
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "conn-read-state-500", "status": "active", "name": "conn-read-state-500"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/tool-connections/conn-404":
			lastConn = "conn-404"
			http.NotFound(w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies":
			if lastConn == "conn-err" {
				http.Error(w, "companies 500", http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode([]interface{}{})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-dup-err/tools/profiles":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "prof-1", "profileKey": "app:conn-revoke-ok", "defaultAction": "deny"},
				{"id": "prof-2", "profileKey": "app:conn-revoke-ok", "defaultAction": "deny"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-state-err/tools/profiles":
			http.Error(w, "profiles error", http.StatusInternalServerError)
		case r.Method == http.MethodGet && (r.URL.Path == "/api/companies/comp-1/tools/profiles" || r.URL.Path == "/api/companies/comp-policy-err/tools/profiles"):
			if lastConn == "conn-verify-err" {
				_ = json.NewEncoder(w).Encode([]map[string]interface{}{
					{"id": "prof-1", "profileKey": "app:conn-verify-err", "defaultAction": "deny", "status": "active", "entries": []interface{}{}},
				})
				return
			}
			_ = json.NewEncoder(w).Encode([]interface{}{})
		case r.Method == http.MethodPost && (r.URL.Path == "/api/companies/comp-1/tools/profiles" || r.URL.Path == "/api/companies/comp-policy-err/tools/profiles" || r.URL.Path == "/api/companies/comp-dup-err/tools/profiles"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "prof-1"})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/tools/apps/") && strings.HasSuffix(r.URL.Path, "/finish"):
			if strings.Contains(r.URL.Path, "/conn-cat-ok/") {
				http.Error(w, "finish error", http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "prof-1"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-policy-err/tools/policies":
			http.Error(w, "policies error", http.StatusInternalServerError)
		case r.Method == http.MethodGet && (r.URL.Path == "/api/companies/comp-1/tools/policies" || r.URL.Path == "/api/companies/comp-state-err/tools/policies" || r.URL.Path == "/api/companies/comp-dup-err/tools/policies"):
			_ = json.NewEncoder(w).Encode([]interface{}{})
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/comp-1/tools/policies":
			http.Error(w, "policy creation error", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	r := NewPaperclipAppPermissionResource().(*PaperclipAppPermissionResource)
	schema := configureTestResource(t, srv.URL, r)

	strListNull := types.ListNull(types.StringType)
	agentList, _ := types.ListValue(types.StringType, []attr.Value{types.StringValue("agent-1")})

	// 1. ModifyPlan catalog error (line 109)
	var stCatErr tfsdk.State = tfsdk.State{Schema: schema}
	_ = stCatErr.Set(ctx, &PaperclipAppPermissionResourceModel{
		ID:                      types.StringNull(),
		CompanyID:               types.StringValue("comp-1"),
		ConnectionID:            types.StringValue("conn-cat-err"),
		ToolSelection:           types.StringValue("{\"enabled_tools\":[\"read\"],\"approval_mode\":\"writes\"}"),
		AgentIDs:                agentList,
		AccessType:              types.StringValue("agents"),
		EnabledCatalogEntryIDs:  strListNull,
		AskFirstCatalogEntryIDs: strListNull,
		ReviewedCatalogEntryIDs: strListNull,
	})
	var modCatErr resource.ModifyPlanResponse
	modCatErr.Plan = tfsdk.Plan{Schema: schema, Raw: stCatErr.Raw}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: modCatErr.Plan}, &modCatErr)
	if !modCatErr.Diagnostics.HasError() {
		t.Fatal("expected error on catalog error in ModifyPlan")
	}

	// 2. ModifyPlan empty catalog (line 113)
	var stCatEmpty tfsdk.State = tfsdk.State{Schema: schema}
	_ = stCatEmpty.Set(ctx, &PaperclipAppPermissionResourceModel{
		ID:                      types.StringNull(),
		CompanyID:               types.StringValue("comp-1"),
		ConnectionID:            types.StringValue("conn-cat-empty"),
		ToolSelection:           types.StringValue("{\"enabled_tools\":[\"read\"],\"approval_mode\":\"writes\"}"),
		AgentIDs:                agentList,
		AccessType:              types.StringValue("agents"),
		EnabledCatalogEntryIDs:  strListNull,
		AskFirstCatalogEntryIDs: strListNull,
		ReviewedCatalogEntryIDs: strListNull,
	})
	var modCatEmpty resource.ModifyPlanResponse
	modCatEmpty.Plan = tfsdk.Plan{Schema: schema, Raw: stCatEmpty.Raw}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: modCatEmpty.Plan}, &modCatEmpty)
	if modCatEmpty.Diagnostics.HasError() {
		t.Fatalf("unexpected error on empty catalog ModifyPlan: %v", modCatEmpty.Diagnostics)
	}

	// 3. ModifyPlan invalid tool selection JSON (line 117)
	var stInvalidSel tfsdk.State = tfsdk.State{Schema: schema}
	_ = stInvalidSel.Set(ctx, &PaperclipAppPermissionResourceModel{
		ID:                      types.StringNull(),
		CompanyID:               types.StringValue("comp-1"),
		ConnectionID:            types.StringValue("conn-cat-ok"),
		ToolSelection:           types.StringValue("not-json"),
		AgentIDs:                agentList,
		AccessType:              types.StringValue("agents"),
		EnabledCatalogEntryIDs:  strListNull,
		AskFirstCatalogEntryIDs: strListNull,
		ReviewedCatalogEntryIDs: strListNull,
	})
	var modInvalidSel resource.ModifyPlanResponse
	modInvalidSel.Plan = tfsdk.Plan{Schema: schema, Raw: stInvalidSel.Raw}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: modInvalidSel.Plan}, &modInvalidSel)
	if !modInvalidSel.Diagnostics.HasError() {
		t.Fatal("expected error on invalid tool selection JSON")
	}

	// 4. revoke with GetToolConnection error (line 142)
	if err := r.revoke(ctx, "comp-1", "conn-err"); err == nil {
		t.Fatal("expected error on revoke connection 500")
	}

	// 5. revoke with GetConnectionCatalog error (line 152)
	if err := r.revoke(ctx, "comp-1", "conn-cat-err"); err == nil {
		t.Fatal("expected error on revoke catalog 500")
	}

	// 5b. revoke with ReconcileAskFirstPolicies error (line 155)
	if err := r.revoke(ctx, "comp-policy-err", "conn-revoke-ok"); err == nil {
		t.Fatal("expected error on revoke reconcile policies 500")
	}

	// 5c. revoke with GetAppPermissionState error (line 159)
	if err := r.revoke(ctx, "comp-dup-err", "conn-revoke-ok"); err == nil {
		t.Fatal("expected error on revoke get state duplicate profile error")
	}

	// 6a. apply with invalid agent_ids element type (line 175)
	var diagsAgentElem diag.Diagnostics
	planBadAgentElem := &PaperclipAppPermissionResourceModel{
		AccessType:    types.StringValue("agents"),
		AgentIDs:      types.ListValueMust(types.Int64Type, []attr.Value{types.Int64Value(123)}),
		CompanyID:     types.StringValue("comp-1"),
		ConnectionID:  types.StringValue("conn-cat-ok"),
		ToolSelection: types.StringValue("{\"enabled_tools\":[\"read\"],\"approval_mode\":\"writes\"}"),
	}
	r.apply(ctx, planBadAgentElem, &diagsAgentElem)
	if !diagsAgentElem.HasError() {
		t.Fatal("expected error on agent_ids element conversion in apply")
	}

	// 6b. apply with empty agents and revoke error (line 179)
	emptyAgents, _ := types.ListValue(types.StringType, []attr.Value{})
	var stRevokeErr tfsdk.State = tfsdk.State{Schema: schema}
	_ = stRevokeErr.Set(ctx, &PaperclipAppPermissionResourceModel{
		ID:                      types.StringNull(),
		CompanyID:               types.StringValue("comp-1"),
		ConnectionID:            types.StringValue("conn-cat-err"),
		ToolSelection:           types.StringValue("{\"enabled_tools\":[\"read\"],\"approval_mode\":\"writes\"}"),
		AgentIDs:                emptyAgents,
		AccessType:              types.StringValue("agents"),
		EnabledCatalogEntryIDs:  strListNull,
		AskFirstCatalogEntryIDs: strListNull,
		ReviewedCatalogEntryIDs: strListNull,
	})
	var createRevokeErr resource.CreateResponse
	createRevokeErr.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Schema: schema, Raw: stRevokeErr.Raw}}, &createRevokeErr)
	if !createRevokeErr.Diagnostics.HasError() {
		t.Fatal("expected error on create with empty agents when revoke fails")
	}

	// 7. apply error on catalog discovery (line 211)
	var createCatErr resource.CreateResponse
	createCatErr.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Schema: schema, Raw: stCatErr.Raw}}, &createCatErr)
	if !createCatErr.Diagnostics.HasError() {
		t.Fatal("expected error on catalog discovery in create")
	}

	// 7b. apply error on pair.planned.ElementsAs (line 230)
	var diagsPlannedElem diag.Diagnostics
	planBadPlannedElem := &PaperclipAppPermissionResourceModel{
		AccessType:             types.StringValue("agents"),
		AgentIDs:               agentList,
		CompanyID:              types.StringValue("comp-1"),
		ConnectionID:           types.StringValue("conn-cat-ok"),
		ToolSelection:          types.StringValue("{\"enabled_tools\":[\"read\"],\"approval_mode\":\"writes\"}"),
		EnabledCatalogEntryIDs: types.ListValueMust(types.Int64Type, []attr.Value{types.Int64Value(123)}),
	}
	r.apply(ctx, planBadPlannedElem, &diagsPlannedElem)
	if !diagsPlannedElem.HasError() {
		t.Fatal("expected error on planned enabled_catalog_entry_ids element conversion in apply")
	}

	// 8. apply error on FinishAppPermissions (line 243)
	var stFinishErr tfsdk.State = tfsdk.State{Schema: schema}
	_ = stFinishErr.Set(ctx, &PaperclipAppPermissionResourceModel{
		ID:                      types.StringNull(),
		CompanyID:               types.StringValue("comp-1"),
		ConnectionID:            types.StringValue("conn-cat-ok"),
		ToolSelection:           types.StringValue("{\"enabled_tools\":[\"read\"],\"approval_mode\":\"writes\"}"),
		AgentIDs:                agentList,
		AccessType:              types.StringValue("agents"),
		EnabledCatalogEntryIDs:  strListNull,
		AskFirstCatalogEntryIDs: strListNull,
		ReviewedCatalogEntryIDs: strListNull,
	})
	var createFinishErr resource.CreateResponse
	createFinishErr.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Schema: schema, Raw: stFinishErr.Raw}}, &createFinishErr)
	if !createFinishErr.Diagnostics.HasError() {
		t.Fatal("expected error on finish in create")
	}

	// 8b. apply error on ReconcileAskFirstPolicies (line 247)
	var stPolicyErr tfsdk.State = tfsdk.State{Schema: schema}
	_ = stPolicyErr.Set(ctx, &PaperclipAppPermissionResourceModel{
		ID:                      types.StringNull(),
		CompanyID:               types.StringValue("comp-policy-err"),
		ConnectionID:            types.StringValue("conn-policy-err"),
		ToolSelection:           types.StringValue("{\"enabled_tools\":[\"read\"],\"approval_mode\":\"writes\"}"),
		AgentIDs:                agentList,
		AccessType:              types.StringValue("agents"),
		EnabledCatalogEntryIDs:  strListNull,
		AskFirstCatalogEntryIDs: strListNull,
		ReviewedCatalogEntryIDs: strListNull,
	})
	var createPolicyErr resource.CreateResponse
	createPolicyErr.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Schema: schema, Raw: stPolicyErr.Raw}}, &createPolicyErr)
	if !createPolicyErr.Diagnostics.HasError() {
		t.Fatal("expected error on reconcile policies in create")
	}

	// 8c. apply error on GetAppPermissionState verification (line 252)
	var stStateErr tfsdk.State = tfsdk.State{Schema: schema}
	_ = stStateErr.Set(ctx, &PaperclipAppPermissionResourceModel{
		ID:                      types.StringNull(),
		CompanyID:               types.StringValue("comp-state-err"),
		ConnectionID:            types.StringValue("conn-state-err"),
		ToolSelection:           types.StringValue("{\"enabled_tools\":[\"read\"],\"approval_mode\":\"writes\"}"),
		AgentIDs:                agentList,
		AccessType:              types.StringValue("agents"),
		EnabledCatalogEntryIDs:  strListNull,
		AskFirstCatalogEntryIDs: strListNull,
		ReviewedCatalogEntryIDs: strListNull,
	})
	var createStateErr resource.CreateResponse
	createStateErr.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Schema: schema, Raw: stStateErr.Raw}}, &createStateErr)
	if !createStateErr.Diagnostics.HasError() {
		t.Fatal("expected error on get app permission state verification in create")
	}

	// 8d. apply error on permission verification mismatch (line 256)
	var stVerifyErr tfsdk.State = tfsdk.State{Schema: schema}
	_ = stVerifyErr.Set(ctx, &PaperclipAppPermissionResourceModel{
		ID:                      types.StringNull(),
		CompanyID:               types.StringValue("comp-1"),
		ConnectionID:            types.StringValue("conn-verify-err"),
		ToolSelection:           types.StringValue("{\"enabled_tools\":[\"read\"],\"approval_mode\":\"writes\"}"),
		AgentIDs:                agentList,
		AccessType:              types.StringValue("agents"),
		EnabledCatalogEntryIDs:  strListNull,
		AskFirstCatalogEntryIDs: strListNull,
		ReviewedCatalogEntryIDs: strListNull,
	})
	var createVerifyErr resource.CreateResponse
	createVerifyErr.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Schema: schema, Raw: stVerifyErr.Raw}}, &createVerifyErr)
	if !createVerifyErr.Diagnostics.HasError() {
		t.Fatal("expected error on permission verification mismatch in create")
	}

	// 9. Read connection 404 (line 297)
	var stRead404 tfsdk.State = tfsdk.State{Schema: schema}
	_ = stRead404.Set(ctx, &PaperclipAppPermissionResourceModel{
		ID:                      types.StringValue("conn-404"),
		ConnectionID:            types.StringValue("conn-404"),
		CompanyID:               types.StringValue("comp-1"),
		ToolSelection:           types.StringValue("{\"enabled_tools\":[\"read\"],\"approval_mode\":\"writes\"}"),
		AgentIDs:                agentList,
		AccessType:              types.StringValue("agents"),
		EnabledCatalogEntryIDs:  strListNull,
		AskFirstCatalogEntryIDs: strListNull,
		ReviewedCatalogEntryIDs: strListNull,
	})
	var read404Resp resource.ReadResponse
	read404Resp.State = stRead404
	r.Read(ctx, resource.ReadRequest{State: stRead404}, &read404Resp)
	if !read404Resp.State.Raw.IsNull() {
		t.Fatal("expected state removed on permission read 404")
	}

	// 10. Read connection 500 (line 301)
	var stRead500 tfsdk.State = tfsdk.State{Schema: schema}
	_ = stRead500.Set(ctx, &PaperclipAppPermissionResourceModel{
		ID:                      types.StringValue("conn-err"),
		ConnectionID:            types.StringValue("conn-err"),
		CompanyID:               types.StringValue("comp-1"),
		ToolSelection:           types.StringValue("{\"enabled_tools\":[\"read\"],\"approval_mode\":\"writes\"}"),
		AgentIDs:                agentList,
		AccessType:              types.StringValue("agents"),
		EnabledCatalogEntryIDs:  strListNull,
		AskFirstCatalogEntryIDs: strListNull,
		ReviewedCatalogEntryIDs: strListNull,
	})
	var read500Resp resource.ReadResponse
	read500Resp.State = stRead500
	r.Read(ctx, resource.ReadRequest{State: stRead500}, &read500Resp)
	if !read500Resp.Diagnostics.HasError() {
		t.Fatal("expected error on connection 500 in Read")
	}

	// 11. Read app state 500 (line 321)
	var stReadState500 tfsdk.State = tfsdk.State{Schema: schema}
	_ = stReadState500.Set(ctx, &PaperclipAppPermissionResourceModel{
		ID:                      types.StringValue("conn-read-state-500"),
		ConnectionID:            types.StringValue("conn-read-state-500"),
		CompanyID:               types.StringValue("comp-state-err"),
		ToolSelection:           types.StringValue("{\"enabled_tools\":[\"read\"],\"approval_mode\":\"writes\"}"),
		AgentIDs:                agentList,
		AccessType:              types.StringValue("agents"),
		EnabledCatalogEntryIDs:  strListNull,
		AskFirstCatalogEntryIDs: strListNull,
		ReviewedCatalogEntryIDs: strListNull,
	})
	var readState500Resp resource.ReadResponse
	readState500Resp.State = stReadState500
	r.Read(ctx, resource.ReadRequest{State: stReadState500}, &readState500Resp)
	if !readState500Resp.Diagnostics.HasError() {
		t.Fatal("expected error on app permission state 500 in Read")
	}
}
