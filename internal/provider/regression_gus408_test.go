package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestRegressionUnknownRoute404DoesNotRemoveState(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"API route not found"}`))
	}))
	defer server.Close()

	r := NewPaperclipAgentResource().(*PaperclipAgentResource)
	schema := configureTestResource(t, server.URL, r)

	state := tfsdk.State{Schema: schema}
	_ = state.Set(ctx, &PaperclipAgentResourceModel{
		ID:        types.StringValue("agent-1"),
		CompanyID: types.StringValue("comp-1"),
	})

	var readResp resource.ReadResponse
	readResp.State = state
	r.Read(ctx, resource.ReadRequest{State: state}, &readResp)

	if !readResp.Diagnostics.HasError() {
		t.Fatal("expected error on unknown route 404, got none")
	}
	if readResp.State.Raw.IsNull() {
		t.Fatal("expected resource state NOT to be removed on unknown route 404")
	}
}

func TestRegressionAmbiguousNameLookupRejectsAndNamesCandidates(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/agents":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "ag-first", "name": "DuplicateName", "companyId": "comp-1"},
				{"id": "ag-second", "name": "DuplicateName", "companyId": "comp-1"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "comp-1", "name": "DuplicateCompany"},
				{"id": "comp-2", "name": "DuplicateCompany"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/projects":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "proj-1", "name": "DuplicateProject", "companyId": "comp-1"},
				{"id": "proj-2", "name": "DuplicateProject", "companyId": "comp-1"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/skills":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "skill-1", "name": "DuplicateSkill", "slug": "dup-skill", "companyId": "comp-1"},
				{"id": "skill-2", "name": "DuplicateSkill", "slug": "dup-skill-2", "companyId": "comp-1"},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	// 1. Agent Data Source with 2 matches
	dsAgent := NewPaperclipAgentDataSource().(*PaperclipAgentDataSource)
	agentSchema := configureTestDataSource(t, server.URL, dsAgent)
	var agentResp datasource.ReadResponse
	agentResp.State = tfsdk.State{Schema: agentSchema}
	planAgent := tfsdk.Plan{Schema: agentSchema}
	_ = planAgent.Set(ctx, &PaperclipAgentDataSourceModel{
		CompanyID: types.StringValue("comp-1"),
		Name:      types.StringValue("DuplicateName"),
	})
	dsAgent.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: agentSchema, Raw: planAgent.Raw}}, &agentResp)

	if !agentResp.Diagnostics.HasError() {
		t.Fatal("expected error on ambiguous agent name lookup, but it succeeded")
	}
	diagErr := agentResp.Diagnostics.Errors()[0].Detail()
	if !strings.Contains(diagErr, "ag-first") || !strings.Contains(diagErr, "ag-second") {
		t.Fatalf("expected candidate IDs 'ag-first' and 'ag-second' in error, got: %s", diagErr)
	}

	// 2. Company Data Source with 2 matches
	dsComp := NewPaperclipCompanyDataSource().(*PaperclipCompanyDataSource)
	compSchema := configureTestDataSource(t, server.URL, dsComp)
	var compResp datasource.ReadResponse
	compResp.State = tfsdk.State{Schema: compSchema}
	planComp := tfsdk.Plan{Schema: compSchema}
	_ = planComp.Set(ctx, &PaperclipCompanyDataSourceModel{
		Name: types.StringValue("DuplicateCompany"),
	})
	dsComp.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: compSchema, Raw: planComp.Raw}}, &compResp)

	if !compResp.Diagnostics.HasError() {
		t.Fatal("expected error on ambiguous company name lookup, but it succeeded")
	}
	compDiag := compResp.Diagnostics.Errors()[0].Detail()
	if !strings.Contains(compDiag, "comp-1") || !strings.Contains(compDiag, "comp-2") {
		t.Fatalf("expected candidate IDs 'comp-1' and 'comp-2' in error, got: %s", compDiag)
	}

	// 3. Project Data Source with 2 matches
	dsProj := NewPaperclipProjectDataSource().(*PaperclipProjectDataSource)
	projSchema := configureTestDataSource(t, server.URL, dsProj)
	var projResp datasource.ReadResponse
	projResp.State = tfsdk.State{Schema: projSchema}
	planProj := tfsdk.Plan{Schema: projSchema}
	_ = planProj.Set(ctx, &PaperclipProjectDataSourceModel{
		CompanyID: types.StringValue("comp-1"),
		Name:      types.StringValue("DuplicateProject"),
	})
	dsProj.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: projSchema, Raw: planProj.Raw}}, &projResp)

	if !projResp.Diagnostics.HasError() {
		t.Fatal("expected error on ambiguous project name lookup, but it succeeded")
	}
	projDiag := projResp.Diagnostics.Errors()[0].Detail()
	if !strings.Contains(projDiag, "proj-1") || !strings.Contains(projDiag, "proj-2") {
		t.Fatalf("expected candidate IDs 'proj-1' and 'proj-2' in error, got: %s", projDiag)
	}

	// 4. Skill Data Source with 2 matches
	dsSkill := NewPaperclipSkillDataSource().(*PaperclipSkillDataSource)
	skillSchema := configureTestDataSource(t, server.URL, dsSkill)
	var skillResp datasource.ReadResponse
	skillResp.State = tfsdk.State{Schema: skillSchema}
	planSkill := tfsdk.Plan{Schema: skillSchema}
	_ = planSkill.Set(ctx, &PaperclipSkillDataSourceModel{
		CompanyID: types.StringValue("comp-1"),
		Name:      types.StringValue("DuplicateSkill"),
	})
	dsSkill.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: skillSchema, Raw: planSkill.Raw}}, &skillResp)

	if !skillResp.Diagnostics.HasError() {
		t.Fatal("expected error on ambiguous skill name lookup, but it succeeded")
	}
	skillDiag := skillResp.Diagnostics.Errors()[0].Detail()
	if !strings.Contains(skillDiag, "skill-1") || !strings.Contains(skillDiag, "skill-2") {
		t.Fatalf("expected candidate IDs 'skill-1' and 'skill-2' in error, got: %s", skillDiag)
	}
}

func TestRegressionDataSourceIDLookupsMismatchValidations(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/ag-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "ag-1", "name": "AgentReal", "companyId": "comp-1",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "comp-1", "name": "CompanyReal",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/projects/proj-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "proj-1", "name": "ProjectReal", "companyId": "comp-1",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/skills/skill-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "skill-1", "slug": "slug-real", "name": "SkillReal", "companyId": "comp-1",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	// 1. Agent DS: ID with name mismatch, ID with company mismatch
	dsAgent := NewPaperclipAgentDataSource().(*PaperclipAgentDataSource)
	agentSchema := configureTestDataSource(t, server.URL, dsAgent)

	var resp1 datasource.ReadResponse
	resp1.State = tfsdk.State{Schema: agentSchema}
	plan1 := tfsdk.Plan{Schema: agentSchema}
	_ = plan1.Set(ctx, &PaperclipAgentDataSourceModel{
		ID:   types.StringValue("ag-1"),
		Name: types.StringValue("NameMismatch"),
	})
	dsAgent.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: agentSchema, Raw: plan1.Raw}}, &resp1)
	if !resp1.Diagnostics.HasError() {
		t.Fatal("expected error on agent name mismatch")
	}

	var resp2 datasource.ReadResponse
	resp2.State = tfsdk.State{Schema: agentSchema}
	plan2 := tfsdk.Plan{Schema: agentSchema}
	_ = plan2.Set(ctx, &PaperclipAgentDataSourceModel{
		ID:        types.StringValue("ag-1"),
		CompanyID: types.StringValue("comp-wrong"),
	})
	dsAgent.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: agentSchema, Raw: plan2.Raw}}, &resp2)
	if !resp2.Diagnostics.HasError() {
		t.Fatal("expected error on agent company mismatch")
	}

	// 2. Company DS: ID with name mismatch
	dsComp := NewPaperclipCompanyDataSource().(*PaperclipCompanyDataSource)
	compSchema := configureTestDataSource(t, server.URL, dsComp)

	var resp3 datasource.ReadResponse
	resp3.State = tfsdk.State{Schema: compSchema}
	plan3 := tfsdk.Plan{Schema: compSchema}
	_ = plan3.Set(ctx, &PaperclipCompanyDataSourceModel{
		ID:   types.StringValue("comp-1"),
		Name: types.StringValue("NameMismatch"),
	})
	dsComp.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: compSchema, Raw: plan3.Raw}}, &resp3)
	if !resp3.Diagnostics.HasError() {
		t.Fatal("expected error on company name mismatch")
	}

	// 3. Project DS: ID with name mismatch, ID with company mismatch
	dsProj := NewPaperclipProjectDataSource().(*PaperclipProjectDataSource)
	projSchema := configureTestDataSource(t, server.URL, dsProj)

	var resp4 datasource.ReadResponse
	resp4.State = tfsdk.State{Schema: projSchema}
	plan4 := tfsdk.Plan{Schema: projSchema}
	_ = plan4.Set(ctx, &PaperclipProjectDataSourceModel{
		ID:   types.StringValue("proj-1"),
		Name: types.StringValue("NameMismatch"),
	})
	dsProj.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: projSchema, Raw: plan4.Raw}}, &resp4)
	if !resp4.Diagnostics.HasError() {
		t.Fatal("expected error on project name mismatch")
	}

	var resp5 datasource.ReadResponse
	resp5.State = tfsdk.State{Schema: projSchema}
	plan5 := tfsdk.Plan{Schema: projSchema}
	_ = plan5.Set(ctx, &PaperclipProjectDataSourceModel{
		ID:        types.StringValue("proj-1"),
		CompanyID: types.StringValue("comp-wrong"),
	})
	dsProj.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: projSchema, Raw: plan5.Raw}}, &resp5)
	if !resp5.Diagnostics.HasError() {
		t.Fatal("expected error on project company mismatch")
	}

	// 4. Skill DS: ID with slug mismatch, ID with name mismatch
	dsSkill := NewPaperclipSkillDataSource().(*PaperclipSkillDataSource)
	skillSchema := configureTestDataSource(t, server.URL, dsSkill)

	var resp6 datasource.ReadResponse
	resp6.State = tfsdk.State{Schema: skillSchema}
	plan6 := tfsdk.Plan{Schema: skillSchema}
	_ = plan6.Set(ctx, &PaperclipSkillDataSourceModel{
		CompanyID: types.StringValue("comp-1"),
		ID:        types.StringValue("skill-1"),
		Slug:      types.StringValue("slug-wrong"),
	})
	dsSkill.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: skillSchema, Raw: plan6.Raw}}, &resp6)
	if !resp6.Diagnostics.HasError() {
		t.Fatal("expected error on skill slug mismatch")
	}

	var resp7 datasource.ReadResponse
	resp7.State = tfsdk.State{Schema: skillSchema}
	plan7 := tfsdk.Plan{Schema: skillSchema}
	_ = plan7.Set(ctx, &PaperclipSkillDataSourceModel{
		CompanyID: types.StringValue("comp-1"),
		ID:        types.StringValue("skill-1"),
		Name:      types.StringValue("NameMismatch"),
	})
	dsSkill.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: skillSchema, Raw: plan7.Raw}}, &resp7)
	if !resp7.Diagnostics.HasError() {
		t.Fatal("expected error on skill name mismatch")
	}
}

func TestRegressionLocalEnvironmentModifyPlanCapabilityMissing(t *testing.T) {
	ctx := context.Background()
	var writeCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch || r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodDelete {
			writeCalls++
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message": "API route not found"}`))
	}))
	defer server.Close()

	r := NewPaperclipLocalEnvironmentResource().(*PaperclipLocalEnvironmentResource)
	schema := configureTestResource(t, server.URL, r)

	plan := tfsdk.Plan{Schema: schema}
	_ = plan.Set(ctx, &PaperclipLocalEnvironmentModel{
		CompanyID:          types.StringValue("comp-1"),
		InheritServicePath: types.BoolValue(true),
	})

	var modifyPlanResp resource.ModifyPlanResponse
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan}, &modifyPlanResp)

	if !modifyPlanResp.Diagnostics.HasError() {
		t.Fatal("expected ModifyPlan to fail when target installation lacks environments capability")
	}

	foundCapabilityError := false
	for _, diag := range modifyPlanResp.Diagnostics.Errors() {
		if strings.Contains(diag.Detail(), "environments") && strings.Contains(diag.Detail(), "route not found") {
			foundCapabilityError = true
			break
		}
	}
	if !foundCapabilityError {
		t.Fatalf("expected capability diagnostic naming environments and route not found, got: %v", modifyPlanResp.Diagnostics.Errors())
	}

	if writeCalls != 0 {
		t.Fatalf("expected zero writes when capability check fails at plan time, got %d writes", writeCalls)
	}
}

