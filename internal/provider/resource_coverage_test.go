package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"git.guseynov.org/guseynov/paperclip/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestProviderInvalidEndpointConfigure(t *testing.T) {
	ctx := context.Background()
	p := New("0.1.0")().(*PaperclipProvider)
	var sResp provider.SchemaResponse
	p.Schema(ctx, provider.SchemaRequest{}, &sResp)

	var invalidEndpointResp provider.ConfigureResponse
	invalidEndpointPlan := tfsdk.Plan{Schema: sResp.Schema}
	_ = invalidEndpointPlan.Set(ctx, &PaperclipProviderModel{
		Endpoint: types.StringValue("://invalid-url"),
	})
	p.Configure(ctx, provider.ConfigureRequest{Config: tfsdk.Config{Schema: sResp.Schema, Raw: invalidEndpointPlan.Raw}}, &invalidEndpointResp)
	if !invalidEndpointResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid endpoint in provider Configure")
	}
}

func TestDataSourcesErrorAndEdgeCases(t *testing.T) {
	ctx := context.Background()

	errServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer errServer.Close()

	cliErr, _ := client.NewClient(errServer.URL, "token")

	// 1. Company Data Source
	dsComp := NewPaperclipCompanyDataSource().(*PaperclipCompanyDataSource)
	dsCompSchema := configureTestDataSource(t, errServer.URL, dsComp)

	// Neither ID nor Name
	var emptyCompResp datasource.ReadResponse
	planCompEmpty := tfsdk.Config{Schema: dsCompSchema}
	_ = planCompEmpty.Raw
	emptyCompConfig := PaperclipCompanyDataSourceModel{}
	emptyCompPlan := tfsdk.Plan{Schema: dsCompSchema}
	_ = emptyCompPlan.Set(ctx, &emptyCompConfig)
	dsComp.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: dsCompSchema, Raw: emptyCompPlan.Raw}}, &emptyCompResp)
	if !emptyCompResp.Diagnostics.HasError() {
		t.Fatal("expected error when neither ID nor Name provided for company data source")
	}

	// ID provided, server error
	var errCompResp datasource.ReadResponse
	idCompConfig := PaperclipCompanyDataSourceModel{ID: types.StringValue("comp-1")}
	idCompPlan := tfsdk.Plan{Schema: dsCompSchema}
	_ = idCompPlan.Set(ctx, &idCompConfig)
	dsComp.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: dsCompSchema, Raw: idCompPlan.Raw}}, &errCompResp)
	if !errCompResp.Diagnostics.HasError() {
		t.Fatal("expected error on company read 500")
	}

	// Name provided, company with null description
	okCompServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode([]map[string]interface{}{{
			"id": "comp-1", "name": "Acme", "description": "", "requireTaskStartConfirmation": true,
		}})
	}))
	defer okCompServer.Close()
	dsCompOk := NewPaperclipCompanyDataSource().(*PaperclipCompanyDataSource)
	configureTestDataSource(t, okCompServer.URL, dsCompOk)
	var okCompResp datasource.ReadResponse
	okCompResp.State = tfsdk.State{Schema: dsCompSchema}
	nameCompConfig := PaperclipCompanyDataSourceModel{Name: types.StringValue("Acme")}
	nameCompPlan := tfsdk.Plan{Schema: dsCompSchema}
	_ = nameCompPlan.Set(ctx, &nameCompConfig)
	dsCompOk.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: dsCompSchema, Raw: nameCompPlan.Raw}}, &okCompResp)
	if okCompResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error: %v", okCompResp.Diagnostics)
	}

	// 2. Project Data Source
	dsProj := NewPaperclipProjectDataSource().(*PaperclipProjectDataSource)
	dsProjSchema := configureTestDataSource(t, errServer.URL, dsProj)

	// Neither ID nor (CompanyID+Name)
	var emptyProjResp datasource.ReadResponse
	dsProj.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: dsProjSchema, Raw: emptyCompPlan.Raw}}, &emptyProjResp)
	if !emptyProjResp.Diagnostics.HasError() {
		t.Fatal("expected error when arguments missing for project data source")
	}

	// ID provided, server error
	var errProjResp datasource.ReadResponse
	idProjPlan := tfsdk.Plan{Schema: dsProjSchema}
	_ = idProjPlan.Set(ctx, &PaperclipProjectDataSourceModel{ID: types.StringValue("p1")})
	dsProj.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: dsProjSchema, Raw: idProjPlan.Raw}}, &errProjResp)
	if !errProjResp.Diagnostics.HasError() {
		t.Fatal("expected error on project read 500")
	}

	// CompanyID+Name provided, server error
	var errProjListResp datasource.ReadResponse
	nameProjPlan := tfsdk.Plan{Schema: dsProjSchema}
	_ = nameProjPlan.Set(ctx, &PaperclipProjectDataSourceModel{
		CompanyID: types.StringValue("c1"),
		Name:      types.StringValue("p1"),
	})
	dsProj.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: dsProjSchema, Raw: nameProjPlan.Raw}}, &errProjListResp)
	if !errProjListResp.Diagnostics.HasError() {
		t.Fatal("expected error on project list 500")
	}

	// Mock server for project edge cases (empty description, null leadAgentID, not found)
	projMockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/projects/p-empty":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "p-empty", "name": "p-empty", "description": "", "leadAgentId": "", "companyId": "",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/c1/projects":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "p-other", "name": "other"},
				{"id": "p-found", "name": "p1", "description": "", "leadAgentId": ""},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer projMockServer.Close()
	dsProjMock := NewPaperclipProjectDataSource().(*PaperclipProjectDataSource)
	configureTestDataSource(t, projMockServer.URL, dsProjMock)

	// Read by ID with empty fields
	var pEmptyResp datasource.ReadResponse
	pEmptyResp.State = tfsdk.State{Schema: dsProjSchema}
	pEmptyPlan := tfsdk.Plan{Schema: dsProjSchema}
	_ = pEmptyPlan.Set(ctx, &PaperclipProjectDataSourceModel{ID: types.StringValue("p-empty")})
	dsProjMock.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: dsProjSchema, Raw: pEmptyPlan.Raw}}, &pEmptyResp)
	if pEmptyResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error: %v", pEmptyResp.Diagnostics)
	}

	// Read by CompanyID+Name found with empty fields
	var pFoundResp datasource.ReadResponse
	pFoundResp.State = tfsdk.State{Schema: dsProjSchema}
	dsProjMock.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: dsProjSchema, Raw: nameProjPlan.Raw}}, &pFoundResp)
	if pFoundResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error: %v", pFoundResp.Diagnostics)
	}

	// Read by CompanyID+Name not found
	var pNotFoundResp datasource.ReadResponse
	pNotFoundPlan := tfsdk.Plan{Schema: dsProjSchema}
	_ = pNotFoundPlan.Set(ctx, &PaperclipProjectDataSourceModel{
		CompanyID: types.StringValue("c1"),
		Name:      types.StringValue("not-found"),
	})
	dsProjMock.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: dsProjSchema, Raw: pNotFoundPlan.Raw}}, &pNotFoundResp)
	if !pNotFoundResp.Diagnostics.HasError() {
		t.Fatal("expected error on project not found")
	}

	// 3. Agent Data Source
	dsAgent := NewPaperclipAgentDataSource().(*PaperclipAgentDataSource)
	dsAgentSchema := configureTestDataSource(t, errServer.URL, dsAgent)

	// Missing arguments
	var emptyAgentResp datasource.ReadResponse
	dsAgent.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: dsAgentSchema, Raw: emptyCompPlan.Raw}}, &emptyAgentResp)
	if !emptyAgentResp.Diagnostics.HasError() {
		t.Fatal("expected error on missing agent args")
	}

	// Read by ID error
	var errAgentResp datasource.ReadResponse
	idAgentPlan := tfsdk.Plan{Schema: dsAgentSchema}
	_ = idAgentPlan.Set(ctx, &PaperclipAgentDataSourceModel{ID: types.StringValue("a1")})
	dsAgent.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: dsAgentSchema, Raw: idAgentPlan.Raw}}, &errAgentResp)
	if !errAgentResp.Diagnostics.HasError() {
		t.Fatal("expected error on agent read 500")
	}

	// Read by CompanyID+Name error
	var errAgentListResp datasource.ReadResponse
	nameAgentPlan := tfsdk.Plan{Schema: dsAgentSchema}
	_ = nameAgentPlan.Set(ctx, &PaperclipAgentDataSourceModel{
		CompanyID: types.StringValue("c1"),
		Name:      types.StringValue("ag1"),
	})
	dsAgent.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: dsAgentSchema, Raw: nameAgentPlan.Raw}}, &errAgentListResp)
	if !errAgentListResp.Diagnostics.HasError() {
		t.Fatal("expected error on agent list 500")
	}

	// Agent mock server (agent with null fields and instructions)
	agentMockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/c1/agents":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "ag-other", "name": "other"},
				{
					"id": "ag-1", "name": "ag1", "role": "engineer", "adapterType": "antigravity",
					"title": "", "status": "", "runtimeConfig": nil, "adapterConfig": nil, "metadata": nil,
					"companyId": "",
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/ag-1/instructions-bundle/file":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"content": "agent instructions"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer agentMockServer.Close()
	dsAgentMock := NewPaperclipAgentDataSource().(*PaperclipAgentDataSource)
	configureTestDataSource(t, agentMockServer.URL, dsAgentMock)

	var agFoundResp datasource.ReadResponse
	agFoundResp.State = tfsdk.State{Schema: dsAgentSchema}
	dsAgentMock.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: dsAgentSchema, Raw: nameAgentPlan.Raw}}, &agFoundResp)
	if agFoundResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error: %v", agFoundResp.Diagnostics)
	}

	// Read by CompanyID+Name not found
	var agNotFoundResp datasource.ReadResponse
	agNotFoundPlan := tfsdk.Plan{Schema: dsAgentSchema}
	_ = agNotFoundPlan.Set(ctx, &PaperclipAgentDataSourceModel{
		CompanyID: types.StringValue("c1"),
		Name:      types.StringValue("missing"),
	})
	dsAgentMock.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: dsAgentSchema, Raw: agNotFoundPlan.Raw}}, &agNotFoundResp)
	if !agNotFoundResp.Diagnostics.HasError() {
		t.Fatal("expected error on agent not found")
	}

	// 4. Skill Data Source
	dsSkill := NewPaperclipSkillDataSource().(*PaperclipSkillDataSource)
	dsSkillSchema := configureTestDataSource(t, errServer.URL, dsSkill)

	// Missing company_id
	var noCompSkillResp datasource.ReadResponse
	dsSkill.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: dsSkillSchema, Raw: emptyCompPlan.Raw}}, &noCompSkillResp)
	if !noCompSkillResp.Diagnostics.HasError() {
		t.Fatal("expected error on missing company_id")
	}

	// Missing id, slug, and name
	var noArgsSkillResp datasource.ReadResponse
	skillOnlyCompPlan := tfsdk.Plan{Schema: dsSkillSchema}
	_ = skillOnlyCompPlan.Set(ctx, &PaperclipSkillDataSourceModel{CompanyID: types.StringValue("c1")})
	dsSkill.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: dsSkillSchema, Raw: skillOnlyCompPlan.Raw}}, &noArgsSkillResp)
	if !noArgsSkillResp.Diagnostics.HasError() {
		t.Fatal("expected error on missing id/slug/name")
	}

	// ID provided, error
	var errSkillResp datasource.ReadResponse
	skillIdPlan := tfsdk.Plan{Schema: dsSkillSchema}
	_ = skillIdPlan.Set(ctx, &PaperclipSkillDataSourceModel{
		CompanyID: types.StringValue("c1"),
		ID:        types.StringValue("sk-1"),
	})
	dsSkill.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: dsSkillSchema, Raw: skillIdPlan.Raw}}, &errSkillResp)
	if !errSkillResp.Diagnostics.HasError() {
		t.Fatal("expected error on skill read 500")
	}

	// Slug provided, error
	var errSkillListResp datasource.ReadResponse
	skillSlugPlan := tfsdk.Plan{Schema: dsSkillSchema}
	_ = skillSlugPlan.Set(ctx, &PaperclipSkillDataSourceModel{
		CompanyID: types.StringValue("c1"),
		Slug:      types.StringValue("slug-1"),
	})
	dsSkill.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: dsSkillSchema, Raw: skillSlugPlan.Raw}}, &errSkillListResp)
	if !errSkillListResp.Diagnostics.HasError() {
		t.Fatal("expected error on skill list 500")
	}

	// Skill mock server
	skillMockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/c1/skills":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "sk-other", "slug": "other", "name": "other"},
				{
					"id": "sk-found", "slug": "slug-found", "name": "skill-name", "content": "content",
					"description": "", "schemaVersion": 0, "metadata": nil,
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer skillMockServer.Close()
	dsSkillMock := NewPaperclipSkillDataSource().(*PaperclipSkillDataSource)
	configureTestDataSource(t, skillMockServer.URL, dsSkillMock)

	// Read by Name
	var skNameResp datasource.ReadResponse
	skNameResp.State = tfsdk.State{Schema: dsSkillSchema}
	skillNamePlan := tfsdk.Plan{Schema: dsSkillSchema}
	_ = skillNamePlan.Set(ctx, &PaperclipSkillDataSourceModel{
		CompanyID: types.StringValue("c1"),
		Name:      types.StringValue("skill-name"),
	})
	dsSkillMock.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: dsSkillSchema, Raw: skillNamePlan.Raw}}, &skNameResp)
	if skNameResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error: %v", skNameResp.Diagnostics)
	}

	// Read not found
	var skNotFoundResp datasource.ReadResponse
	skillNotFoundPlan := tfsdk.Plan{Schema: dsSkillSchema}
	_ = skillNotFoundPlan.Set(ctx, &PaperclipSkillDataSourceModel{
		CompanyID: types.StringValue("c1"),
		Slug:      types.StringValue("missing"),
	})
	dsSkillMock.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: dsSkillSchema, Raw: skillNotFoundPlan.Raw}}, &skNotFoundResp)
	if !skNotFoundResp.Diagnostics.HasError() {
		t.Fatal("expected error on skill not found")
	}

	_ = cliErr
}

func TestAgentReconciliationBranches(t *testing.T) {
	ctx := context.Background()

	// 1. GetAgent error in reconcilePermissions
	srvErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer srvErr.Close()
	rErr := NewPaperclipAgentResource().(*PaperclipAgentResource)
	configureTestResource(t, srvErr.URL, rErr)
	if err := rErr.reconcilePermissions(ctx, "a1", false, false, true, "", nil); err == nil {
		t.Fatal("expected error on GetAgent failure")
	}

	// 2. agent.Permissions == nil
	srvNoPerms := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "a1", "permissions": nil})
	}))
	defer srvNoPerms.Close()
	rNoPerms := NewPaperclipAgentResource().(*PaperclipAgentResource)
	configureTestResource(t, srvNoPerms.URL, rNoPerms)
	if err := rNoPerms.reconcilePermissions(ctx, "a1", true, false, true, "", nil); err == nil {
		t.Fatal("expected error on nil permissions")
	}

	// 3. UpdateAgentPermissions error
	step := 0
	srvUpdErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		step++
		if step == 1 {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "a1", "permissions": map[string]interface{}{"canAssignTasks": false, "canCreateAgents": false, "canCreateSkills": true},
			})
		} else {
			http.Error(w, "update error", http.StatusInternalServerError)
		}
	}))
	defer srvUpdErr.Close()
	rUpdErr := NewPaperclipAgentResource().(*PaperclipAgentResource)
	configureTestResource(t, srvUpdErr.URL, rUpdErr)
	if err := rUpdErr.reconcilePermissions(ctx, "a1", true, false, true, "", nil); err == nil {
		t.Fatal("expected error on UpdateAgentPermissions")
	}

	// 4. Second GetAgent error
	step = 0
	srvGet2Err := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		step++
		if step == 1 {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "a1", "permissions": map[string]interface{}{"canAssignTasks": false, "canCreateAgents": false, "canCreateSkills": true},
			})
		} else if step == 2 {
			w.WriteHeader(http.StatusOK)
		} else {
			http.Error(w, "get2 error", http.StatusInternalServerError)
		}
	}))
	defer srvGet2Err.Close()
	rGet2Err := NewPaperclipAgentResource().(*PaperclipAgentResource)
	configureTestResource(t, srvGet2Err.URL, rGet2Err)
	if err := rGet2Err.reconcilePermissions(ctx, "a1", true, false, true, "", nil); err == nil {
		t.Fatal("expected error on second GetAgent")
	}

	// 5. Does not converge
	step = 0
	srvNoConv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id": "a1", "permissions": map[string]interface{}{"canAssignTasks": false, "canCreateAgents": false, "canCreateSkills": true},
		})
	}))
	defer srvNoConv.Close()
	rNoConv := NewPaperclipAgentResource().(*PaperclipAgentResource)
	configureTestResource(t, srvNoConv.URL, rNoConv)
	if err := rNoConv.reconcilePermissions(ctx, "a1", true, false, true, "", nil); err == nil {
		t.Fatal("expected divergence error")
	}

	// permissionState with nil
	canAssign, canCreate, canSkills := permissionState(nil)
	if !canAssign.IsNull() || !canCreate.IsNull() || !canSkills.IsNull() {
		t.Fatal("expected nulls for nil permissions")
	}

	// Agent Create instructions update error
	srvInstrErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/comp-1/agents":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "a1", "companyId": "comp-1", "name": "ag1", "role": "r", "adapterType": "antigravity",
				"permissions": map[string]interface{}{"canAssignTasks": false, "canCreateAgents": false},
			})
		case r.Method == http.MethodPut && r.URL.Path == "/api/agents/a1/instructions-bundle/file":
			http.Error(w, "instructions error", http.StatusInternalServerError)
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/a1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "a1", "companyId": "comp-1", "name": "ag1", "role": "r", "adapterType": "antigravity",
				"permissions": map[string]interface{}{"canAssignTasks": false, "canCreateAgents": false},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srvInstrErr.Close()
	rInstrErr := NewPaperclipAgentResource().(*PaperclipAgentResource)
	schema := configureTestResource(t, srvInstrErr.URL, rInstrErr)
	plan := tfsdk.Plan{Schema: schema}
	_ = plan.Set(ctx, &PaperclipAgentResourceModel{
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("ag1"),
		Role:            types.StringValue("r"),
		AdapterType:     types.StringValue("antigravity"),
		Instructions:    types.StringValue("instructions"),
		CanAssignTasks:  types.BoolValue(false),
		CanCreateAgents: types.BoolValue(false),
	})
	var createResp resource.CreateResponse
	createResp.State = tfsdk.State{Schema: schema}
	rInstrErr.Create(ctx, resource.CreateRequest{Plan: plan}, &createResp)
	if !createResp.Diagnostics.HasError() {
		t.Fatal("expected error on instructions update failure")
	}

	// Agent Read 404
	srv404 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv404.Close()
	r404 := NewPaperclipAgentResource().(*PaperclipAgentResource)
	configureTestResource(t, srv404.URL, r404)
	var read404Resp resource.ReadResponse
	read404Resp.State = tfsdk.State{Schema: schema}
	state404 := tfsdk.State{Schema: schema}
	_ = state404.Set(ctx, &PaperclipAgentResourceModel{ID: types.StringValue("a1")})
	r404.Read(ctx, resource.ReadRequest{State: state404}, &read404Resp)
	if !read404Resp.State.Raw.IsNull() {
		t.Fatal("expected state to be removed on 404")
	}

	// Agent Read 500
	var read500Resp resource.ReadResponse
	read500Resp.State = tfsdk.State{Schema: schema}
	rErr.Read(ctx, resource.ReadRequest{State: state404}, &read500Resp)
	if !read500Resp.Diagnostics.HasError() {
		t.Fatal("expected error on agent read 500")
	}

	// Agent Update 500
	var upd500Resp resource.UpdateResponse
	upd500Resp.State = tfsdk.State{Schema: schema}
	rErr.Update(ctx, resource.UpdateRequest{Plan: plan, State: state404}, &upd500Resp)
	if !upd500Resp.Diagnostics.HasError() {
		t.Fatal("expected error on agent update 500")
	}

	// Agent Delete 500
	var del500Resp resource.DeleteResponse
	rErr.Delete(ctx, resource.DeleteRequest{State: state404}, &del500Resp)
	if !del500Resp.Diagnostics.HasError() {
		t.Fatal("expected error on agent delete 500")
	}
}

func TestLocalEnvironmentMoreEdgeCases(t *testing.T) {
	ctx := context.Background()
	r := NewPaperclipLocalEnvironmentResource().(*PaperclipLocalEnvironmentResource)

	// 1. InheritServicePath false
	var sResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sResp)
	planFalse := tfsdk.Plan{Schema: sResp.Schema}
	_ = planFalse.Set(ctx, &PaperclipLocalEnvironmentModel{
		CompanyID:          types.StringValue("c1"),
		InheritServicePath: types.BoolValue(false),
	})
	var state tfsdk.State
	if err := r.apply(ctx, planFalse, &state); err == nil {
		t.Fatal("expected error when InheritServicePath is false")
	}

	// 2. Server 500 on FindLocalEnvironment
	srvErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/instance/settings/experimental" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"enableEnvironments": true})
			return
		}
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer srvErr.Close()
	schema := configureTestResource(t, srvErr.URL, r)
	planTrue := tfsdk.Plan{Schema: schema}
	_ = planTrue.Set(ctx, &PaperclipLocalEnvironmentModel{
		CompanyID:          types.StringValue("c1"),
		InheritServicePath: types.BoolValue(true),
	})
	state = tfsdk.State{Schema: schema}
	if err := r.apply(ctx, planTrue, &state); err == nil {
		t.Fatal("expected error when FindLocalEnvironment fails")
	}

	// 3. Read 500 error
	var readResp resource.ReadResponse
	readResp.State = state
	r.Read(ctx, resource.ReadRequest{State: state}, &readResp)
	if !readResp.Diagnostics.HasError() {
		t.Fatal("expected error on local env read 500")
	}
}

func TestStdioTemplateAndConnectionInstallEdgeCases(t *testing.T) {
	ctx := context.Background()

	// templateStrings null/unknown
	vals, diags := templateStrings(ctx, types.ListNull(types.StringType))
	if vals != nil || diags.HasError() {
		t.Fatal("expected nil, nil from null templateStrings")
	}

	// templateListState remote empty and prior null
	listVal := templateListState(ctx, types.ListNull(types.StringType), nil)
	if !listVal.IsNull() {
		t.Fatal("expected null listVal")
	}

	// Stdio template Delete 500
	srvErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer srvErr.Close()

	rTmpl := NewPaperclipStdioTemplateResource().(*PaperclipStdioTemplateResource)
	tmplSchema := configureTestResource(t, srvErr.URL, rTmpl)
	stateTmpl := tfsdk.State{Schema: tmplSchema}
	_ = stateTmpl.Set(ctx, &PaperclipStdioTemplateResourceModel{
		CompanyID:  types.StringValue("c1"),
		TemplateID: types.StringValue("t1"),
		ID:         types.StringValue("t1"),
	})
	var delTmplResp resource.DeleteResponse
	rTmpl.Delete(ctx, resource.DeleteRequest{State: stateTmpl}, &delTmplResp)
	if !delTmplResp.Diagnostics.HasError() {
		t.Fatal("expected error on template delete 500")
	}

	// Stdio template Read 404
	srv404 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv404.Close()
	rTmpl404 := NewPaperclipStdioTemplateResource().(*PaperclipStdioTemplateResource)
	configureTestResource(t, srv404.URL, rTmpl404)
	var readTmplResp resource.ReadResponse
	readTmplResp.State = stateTmpl
	rTmpl404.Read(ctx, resource.ReadRequest{State: stateTmpl}, &readTmplResp)
	if !readTmplResp.State.Raw.IsNull() {
		t.Fatal("expected state removed on 404")
	}

	// Connection install ImportState with "/"
	rConnInst := NewPaperclipConnectionInstallResource().(*PaperclipConnectionInstallResource)
	var importResp resource.ImportStateResponse
	var sResp resource.SchemaResponse
	rConnInst.Schema(ctx, resource.SchemaRequest{}, &sResp)
	importResp.State = tfsdk.State{Schema: sResp.Schema}
	_ = importResp.State.Set(ctx, &PaperclipConnectionInstallResourceModel{
		AgentIDs: types.ListNull(types.StringType),
	})
	rConnInst.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/conn-1"}, &importResp)
	if importResp.Diagnostics.HasError() {
		t.Fatalf("unexpected import error: %v", importResp.Diagnostics)
	}
	var importedModel PaperclipConnectionInstallResourceModel
	_ = importResp.State.Get(ctx, &importedModel)
	if importedModel.ConnectionID.ValueString() != "conn-1" {
		t.Fatalf("expected conn-1, got %s", importedModel.ConnectionID.ValueString())
	}

	// Connection install TargetType == "company" in Create and Read
	srvInstComp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPut && r.URL.Path == "/api/tool-connections/conn-1/installs":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"installs": []map[string]interface{}{{"targetType": "company", "targetId": "comp-1"}},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/tool-connections/conn-1/installs":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"installs": []map[string]interface{}{
					{"targetType": "company", "targetId": "comp-1"},
					{"targetType": "agent", "targetId": "ag-1"},
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srvInstComp.Close()
	rInstComp := NewPaperclipConnectionInstallResource().(*PaperclipConnectionInstallResource)
	instSchema := configureTestResource(t, srvInstComp.URL, rInstComp)
	planInstComp := tfsdk.Plan{Schema: instSchema}
	_ = planInstComp.Set(ctx, &PaperclipConnectionInstallResourceModel{
		ID:           types.StringNull(),
		ConnectionID: types.StringValue("conn-1"),
		CompanyID:    types.StringValue("comp-1"),
		TargetType:   types.StringValue("company"),
		AgentIDs:     types.ListNull(types.StringType),
	})
	var createInstResp resource.CreateResponse
	createInstResp.State = tfsdk.State{Schema: instSchema}
	rInstComp.Create(ctx, resource.CreateRequest{Plan: planInstComp}, &createInstResp)
	if createInstResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on install create: %v", createInstResp.Diagnostics)
	}

	var readInstResp resource.ReadResponse
	readInstResp.State = createInstResp.State
	rInstComp.Read(ctx, resource.ReadRequest{State: createInstResp.State}, &readInstResp)
	if readInstResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on install read: %v", readInstResp.Diagnostics)
	}

	var updInstResp resource.UpdateResponse
	updInstResp.State = createInstResp.State
	rInstComp.Update(ctx, resource.UpdateRequest{Plan: planInstComp, State: createInstResp.State}, &updInstResp)
	if updInstResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on install update: %v", updInstResp.Diagnostics)
	}
}

func TestPolicyAndSecretAndConnectionEdgeCases(t *testing.T) {
	ctx := context.Background()

	srvErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer srvErr.Close()

	// 1. Policy with rules and metadata JSON in Create, Read, Update
	srvPolicy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/comp-1/tools/policies":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "pol-1", "companyId": "comp-1", "name": "p1", "policyType": "approval",
				"rules": map[string]interface{}{"mode": "strict"}, "metadata": map[string]interface{}{"env": "prod"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/tools/policies":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{{
				"id": "pol-1", "companyId": "comp-1", "name": "p1", "policyType": "approval",
				"rules": map[string]interface{}{"mode": "strict"}, "metadata": map[string]interface{}{"env": "prod"},
			}})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/companies/comp-1/tools/policies/pol-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "pol-1", "companyId": "comp-1", "name": "p1-upd", "policyType": "approval",
				"rules": map[string]interface{}{"mode": "strict"}, "metadata": map[string]interface{}{"env": "prod"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srvPolicy.Close()

	rPol := NewPaperclipPolicyResource().(*PaperclipPolicyResource)
	polSchema := configureTestResource(t, srvPolicy.URL, rPol)
	planPol := tfsdk.Plan{Schema: polSchema}
	_ = planPol.Set(ctx, &PaperclipPolicyResourceModel{
		CompanyID:  types.StringValue("comp-1"),
		Name:       types.StringValue("p1"),
		PolicyType: types.StringValue("approval"),
		Rules:      types.StringValue(`{"mode":"strict"}`),
		Metadata:   types.StringValue(`{"env":"prod"}`),
	})
	var createPolResp resource.CreateResponse
	createPolResp.State = tfsdk.State{Schema: polSchema}
	rPol.Create(ctx, resource.CreateRequest{Plan: planPol}, &createPolResp)
	if createPolResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on policy create: %v", createPolResp.Diagnostics)
	}

	var readPolResp resource.ReadResponse
	readPolResp.State = createPolResp.State
	rPol.Read(ctx, resource.ReadRequest{State: createPolResp.State}, &readPolResp)
	if readPolResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on policy read: %v", readPolResp.Diagnostics)
	}

	var updPolResp resource.UpdateResponse
	updPolResp.State = createPolResp.State
	rPol.Update(ctx, resource.UpdateRequest{Plan: planPol, State: createPolResp.State}, &updPolResp)
	if updPolResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on policy update: %v", updPolResp.Diagnostics)
	}

	// 2. Secret ValidateConfig error (rotation without value)
	rSec := NewPaperclipSecretResource().(*PaperclipSecretResource)
	var secSchemaResp resource.SchemaResponse
	rSec.Schema(ctx, resource.SchemaRequest{}, &secSchemaResp)
	secPlanBad := tfsdk.Plan{Schema: secSchemaResp.Schema}
	_ = secPlanBad.Set(ctx, &PaperclipSecretResourceModel{
		CompanyID: types.StringValue("comp-1"),
		Name:      types.StringValue("s1"),
		Rotation:  types.StringValue("rev-1"),
	})
	var valResp resource.ValidateConfigResponse
	rSec.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: tfsdk.Config{Schema: secSchemaResp.Schema, Raw: secPlanBad.Raw}}, &valResp)
	if !valResp.Diagnostics.HasError() {
		t.Fatal("expected validation error on rotation without value")
	}

	// Secret Rotate 500 error in Update
	rSecErr := NewPaperclipSecretResource().(*PaperclipSecretResource)
	configureTestResource(t, srvErr.URL, rSecErr)
	secPlanOk := tfsdk.Plan{Schema: secSchemaResp.Schema}
	_ = secPlanOk.Set(ctx, &PaperclipSecretResourceModel{
		CompanyID: types.StringValue("comp-1"),
		Name:      types.StringValue("s1"),
		Rotation:  types.StringValue("rev-2"),
		Value:     types.StringValue("val"),
	})
	secStatePrior := tfsdk.State{Schema: secSchemaResp.Schema}
	_ = secStatePrior.Set(ctx, &PaperclipSecretResourceModel{
		ID:        types.StringValue("sec-1"),
		CompanyID: types.StringValue("comp-1"),
		Name:      types.StringValue("s1"),
		Rotation:  types.StringValue("rev-1"),
	})
	var updSecResp resource.UpdateResponse
	updSecResp.State = secStatePrior
	rSecErr.Update(ctx, resource.UpdateRequest{Plan: secPlanOk, State: secStatePrior, Config: tfsdk.Config{Schema: secSchemaResp.Schema, Raw: secPlanOk.Raw}}, &updSecResp)
	if !updSecResp.Diagnostics.HasError() {
		t.Fatal("expected error on rotate secret 500")
	}

	// 3. Connection edge cases: invalid config/credential JSON, missing app_id
	rConn := NewPaperclipConnectionResource().(*PaperclipConnectionResource)
	var connSchemaResp resource.SchemaResponse
	rConn.Schema(ctx, resource.SchemaRequest{}, &connSchemaResp)

	// Invalid config JSON
	planBadConfig := tfsdk.Plan{Schema: connSchemaResp.Schema}
	_ = planBadConfig.Set(ctx, &PaperclipConnectionResourceModel{
		CompanyID: types.StringValue("comp-1"),
		Name:      types.StringValue("c1"),
		Config:    types.StringValue("invalid json"),
	})
	var createBadConfigResp resource.CreateResponse
	rConn.Create(ctx, resource.CreateRequest{Plan: planBadConfig}, &createBadConfigResp)
	if !createBadConfigResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid config JSON")
	}

	// Invalid credential_refs JSON
	planBadRefs := tfsdk.Plan{Schema: connSchemaResp.Schema}
	_ = planBadRefs.Set(ctx, &PaperclipConnectionResourceModel{
		CompanyID:      types.StringValue("comp-1"),
		Name:           types.StringValue("c1"),
		CredentialRefs: types.StringValue("invalid json"),
	})
	var createBadRefsResp resource.CreateResponse
	rConn.Create(ctx, resource.CreateRequest{Plan: planBadRefs}, &createBadRefsResp)
	if !createBadRefsResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid credential_refs JSON")
	}

	// Missing app_id when gallery_key is empty
	planMissingApp := tfsdk.Plan{Schema: connSchemaResp.Schema}
	_ = planMissingApp.Set(ctx, &PaperclipConnectionResourceModel{
		CompanyID: types.StringValue("comp-1"),
		Name:      types.StringValue("c1"),
	})
	var createMissingAppResp resource.CreateResponse
	rConn.Create(ctx, resource.CreateRequest{Plan: planMissingApp}, &createMissingAppResp)
	if !createMissingAppResp.Diagnostics.HasError() {
		t.Fatal("expected error on missing app_id")
	}
}

func TestAppPermissionFullEdgeCases(t *testing.T) {
	ctx := context.Background()

	srvErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))
	defer srvErr.Close()

	r := NewPaperclipAppPermissionResource().(*PaperclipAppPermissionResource)
	var sResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sResp)
	schema := configureTestResource(t, srvErr.URL, r)

	agentList, _ := types.ListValue(types.StringType, []attr.Value{types.StringValue("agent-1")})

	// ModifyPlan error on GetToolConnection
	strListNull := types.ListNull(types.StringType)
	var stErrConn tfsdk.State = tfsdk.State{Schema: schema}
	_ = stErrConn.Set(ctx, &PaperclipAppPermissionResourceModel{
		ID:                      types.StringNull(),
		CompanyID:               types.StringValue("comp-1"),
		ConnectionID:            types.StringValue("conn-1"),
		ToolSelection:           types.StringValue("all"),
		AgentIDs:                agentList,
		AccessType:              types.StringNull(),
		EnabledCatalogEntryIDs:  strListNull,
		AskFirstCatalogEntryIDs: strListNull,
		ReviewedCatalogEntryIDs: strListNull,
	})
	planErrConn := tfsdk.Plan{Schema: schema, Raw: stErrConn.Raw}
	var modErrConn resource.ModifyPlanResponse
	modErrConn.Plan = planErrConn
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: planErrConn}, &modErrConn)
	if !modErrConn.Diagnostics.HasError() {
		t.Fatal("expected error on connection error in ModifyPlan")
	}

	// Revoke edge cases:
	// Connection awaiting authorization
	srvOAuth := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/comp-1/tools/profiles/conn-oauth/revoke":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/tools/profiles":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{})
		case r.Method == http.MethodGet && r.URL.Path == "/api/tool-connections/conn-oauth":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "conn-oauth", "authKind": "oauth", "status": "draft"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srvOAuth.Close()
	rOAuth := NewPaperclipAppPermissionResource().(*PaperclipAppPermissionResource)
	configureTestResource(t, srvOAuth.URL, rOAuth)
	if err := rOAuth.revoke(ctx, "comp-1", "conn-oauth"); err != nil {
		t.Fatalf("unexpected error on oauth revoke: %v", err)
	}

	// Apply catalog changed error
	srvCatalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/tool-connections/conn-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "conn-1", "status": "active"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/tool-connections/conn-1/catalog":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{{"id": "entry-1", "toolName": "read"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srvCatalog.Close()
	rCat := NewPaperclipAppPermissionResource().(*PaperclipAppPermissionResource)
	configureTestResource(t, srvCatalog.URL, rCat)

	plannedCatalog, _ := types.ListValue(types.StringType, []attr.Value{types.StringValue("entry-different")})
	modelCatChanged := &PaperclipAppPermissionResourceModel{
		CompanyID:              types.StringValue("comp-1"),
		ConnectionID:           types.StringValue("conn-1"),
		AccessType:             types.StringValue("agents"),
		ToolSelection:          types.StringValue("all"),
		AgentIDs:               agentList,
		EnabledCatalogEntryIDs: plannedCatalog,
	}
	var diags diag.Diagnostics
	rCat.apply(ctx, modelCatChanged, &diags)
	if !diags.HasError() {
		t.Fatal("expected error on catalog changed in apply")
	}
}
