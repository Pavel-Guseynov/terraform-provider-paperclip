package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.guseynov.org/guseynov/paperclip/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestSkillPolicyValidatorAndRulesCoverage(t *testing.T) {
	ctx := context.Background()

	// 1. Validator coverage
	v := defaultEffectValidator{}
	if v.Description(ctx) == "" || v.MarkdownDescription(ctx) == "" {
		t.Fatal("validator descriptions should not be empty")
	}

	for _, tc := range []struct {
		val types.String
		err bool
	}{
		{val: types.StringNull(), err: false},
		{val: types.StringUnknown(), err: false},
		{val: types.StringValue("allow"), err: false},
		{val: types.StringValue("deny"), err: false},
		{val: types.StringValue("invalid"), err: true},
	} {
		var resp validator.StringResponse
		v.ValidateString(ctx, validator.StringRequest{
			Path:        path.Root("default_effect"),
			ConfigValue: tc.val,
		}, &resp)
		if tc.err != resp.Diagnostics.HasError() {
			t.Fatalf("unexpected validation result for %v: %v", tc.val, resp.Diagnostics)
		}
	}

	// 2. parsePolicyRules coverage
	rules, err := parsePolicyRules(types.StringNull())
	if err != nil || len(rules) != 0 {
		t.Fatalf("expected empty rules on null, got: %v, err=%v", rules, err)
	}
	rules, err = parsePolicyRules(types.StringUnknown())
	if err != nil || len(rules) != 0 {
		t.Fatalf("expected empty rules on unknown, got: %v, err=%v", rules, err)
	}
	rules, err = parsePolicyRules(types.StringValue(""))
	if err != nil || len(rules) != 0 {
		t.Fatalf("expected empty rules on empty, got: %v, err=%v", rules, err)
	}
	rules, err = parsePolicyRules(types.StringValue("null"))
	if err != nil || len(rules) != 0 {
		t.Fatalf("expected empty rules on JSON null, got: %v, err=%v", rules, err)
	}
	rules, err = parsePolicyRules(types.StringValue(`[{"effect":"allow"}]`))
	if err != nil || len(rules) != 1 {
		t.Fatalf("expected 1 rule on valid JSON, got: %v, err=%v", rules, err)
	}
	if _, err := parsePolicyRules(types.StringValue(`invalid json`)); err == nil {
		t.Fatal("expected error on invalid JSON")
	}
}

func TestSkillPolicyResourceAllBranches(t *testing.T) {
	ctx := context.Background()

	// 1. Configure with nil and wrong type
	r := NewPaperclipSkillPolicyResource().(*PaperclipSkillPolicyResource)
	var confResp resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: nil}, &confResp)
	if confResp.Diagnostics.HasError() {
		t.Fatal("expected no error on nil ProviderData")
	}
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: "invalid"}, &confResp)
	if !confResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid ProviderData")
	}

	// Mock server for error conditions
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-err/skill-policy":
			http.Error(w, `{"error":"server error"}`, http.StatusInternalServerError)
		case req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-404/skill-policy":
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		case req.Method == http.MethodPut && req.URL.Path == "/api/companies/comp-1/skill-policy":
			http.Error(w, `{"error":"conflict"}`, http.StatusConflict)
		case req.Method == http.MethodDelete && req.URL.Path == "/api/companies/comp-err/skill-policy":
			http.Error(w, `{"error":"reset error"}`, http.StatusInternalServerError)
		case req.Method == http.MethodDelete && req.URL.Path == "/api/companies/comp-404/skill-policy":
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	schema := configureTestResource(t, server.URL, r)

	// Create: GetSkillPolicy fails
	planErr := tfsdk.Plan{Schema: schema}
	_ = planErr.Set(ctx, &PaperclipSkillPolicyResourceModel{
		CompanyID:     types.StringValue("comp-err"),
		DefaultEffect: types.StringValue("allow"),
	})
	var createErrResp resource.CreateResponse
	r.Create(ctx, resource.CreateRequest{Plan: planErr}, &createErrResp)
	if !createErrResp.Diagnostics.HasError() {
		t.Fatal("expected error when GetSkillPolicy fails")
	}

	// Create: invalid rules JSON
	planBadRules := tfsdk.Plan{Schema: schema}
	_ = planBadRules.Set(ctx, &PaperclipSkillPolicyResourceModel{
		CompanyID:     types.StringValue("comp-1"),
		DefaultEffect: types.StringValue("allow"),
		Rules:         types.StringValue("not json"),
	})
	// Mock success for GetSkillPolicy
	goodServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"companyId":     "comp-1",
			"defaultEffect": "allow",
			"revision":      1,
		})
	}))
	defer goodServer.Close()
	rGood := NewPaperclipSkillPolicyResource().(*PaperclipSkillPolicyResource)
	goodSchema := configureTestResource(t, goodServer.URL, rGood)

	var createBadRulesResp resource.CreateResponse
	rGood.Create(ctx, resource.CreateRequest{Plan: planBadRules}, &createBadRulesResp)
	if !createBadRulesResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid rules JSON during create")
	}

	// Create: ReplaceSkillPolicy fails
	rConfServer := NewPaperclipSkillPolicyResource().(*PaperclipSkillPolicyResource)
	confSchema := configureTestResource(t, server.URL, rConfServer)
	planConf := tfsdk.Plan{Schema: confSchema}
	_ = planConf.Set(ctx, &PaperclipSkillPolicyResourceModel{
		CompanyID:     types.StringValue("comp-1"),
		DefaultEffect: types.StringValue("allow"),
	})
	// Make server return 200 on GET comp-1, 409 on PUT
	c1Server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if req.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"companyId":     "comp-1",
				"defaultEffect": "allow",
				"revision":      1,
			})
			return
		}
		http.Error(w, `{"error":"conflict"}`, http.StatusConflict)
	}))
	defer c1Server.Close()
	rC1 := NewPaperclipSkillPolicyResource().(*PaperclipSkillPolicyResource)
	_ = configureTestResource(t, c1Server.URL, rC1)
	var createConfResp resource.CreateResponse
	rC1.Create(ctx, resource.CreateRequest{Plan: planConf}, &createConfResp)
	if !createConfResp.Diagnostics.HasError() {
		t.Fatal("expected error on ReplaceSkillPolicy failure")
	}

	// Read: 404 removes resource
	state404 := tfsdk.State{Schema: schema}
	_ = state404.Set(ctx, &PaperclipSkillPolicyResourceModel{
		CompanyID: types.StringValue("comp-404"),
	})
	var read404Resp resource.ReadResponse
	read404Resp.State = state404
	r.Read(ctx, resource.ReadRequest{State: state404}, &read404Resp)
	if read404Resp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on 404 read: %v", read404Resp.Diagnostics)
	}

	// Read: 500 returns error
	state500 := tfsdk.State{Schema: schema}
	_ = state500.Set(ctx, &PaperclipSkillPolicyResourceModel{
		CompanyID: types.StringValue("comp-err"),
	})
	var read500Resp resource.ReadResponse
	read500Resp.State = state500
	r.Read(ctx, resource.ReadRequest{State: state500}, &read500Resp)
	if !read500Resp.Diagnostics.HasError() {
		t.Fatal("expected error on 500 read")
	}

	// Update: bad rules JSON
	stateGood := tfsdk.State{Schema: goodSchema}
	_ = stateGood.Set(ctx, &PaperclipSkillPolicyResourceModel{
		CompanyID:     types.StringValue("comp-1"),
		DefaultEffect: types.StringValue("allow"),
		Revision:      types.Int64Value(1),
	})
	var updBadRulesResp resource.UpdateResponse
	rGood.Update(ctx, resource.UpdateRequest{Plan: planBadRules, State: stateGood}, &updBadRulesResp)
	if !updBadRulesResp.Diagnostics.HasError() {
		t.Fatal("expected error on bad rules during update")
	}

	// Update: ReplaceSkillPolicy error
	var updConfResp resource.UpdateResponse
	rC1.Update(ctx, resource.UpdateRequest{Plan: planConf, State: stateGood}, &updConfResp)
	if !updConfResp.Diagnostics.HasError() {
		t.Fatal("expected error on update ReplaceSkillPolicy failure")
	}

	// Delete: 500 error
	var del500Resp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: state500}, &del500Resp)
	if !del500Resp.Diagnostics.HasError() {
		t.Fatal("expected error on 500 delete")
	}

	// Delete: 404 ignored
	var del404Resp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: state404}, &del404Resp)
	if del404Resp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on 404 delete: %v", del404Resp.Diagnostics)
	}

	// ImportState: empty ID error
	var impResp resource.ImportStateResponse
	r.ImportState(ctx, resource.ImportStateRequest{ID: ""}, &impResp)
	if !impResp.Diagnostics.HasError() {
		t.Fatal("expected error on empty import ID")
	}
}

func TestSkillAssignmentHelpersAndAllBranches(t *testing.T) {
	ctx := context.Background()

	// 1. toDesiredSkillEntries
	if len(toDesiredSkillEntries(nil)) != 0 {
		t.Fatal("expected empty slice on nil")
	}
	v1 := "v1.0"
	emptyVer := ""
	entries := toDesiredSkillEntries([]PaperclipDesiredSkillModel{
		{Key: types.StringValue("k1"), VersionID: types.StringValue(v1)},
		{Key: types.StringValue("k2"), VersionID: types.StringValue(emptyVer)},
		{Key: types.StringValue("k3"), VersionID: types.StringNull()},
	})
	if len(entries) != 3 || entries[0].VersionID == nil || entries[1].VersionID != nil || entries[2].VersionID != nil {
		t.Fatalf("unexpected entries: %+v", entries)
	}

	// 2. snapshotToModelSkills
	if len(snapshotToModelSkills(nil, nil)) != 0 {
		t.Fatal("expected empty models on nil snapshot")
	}
	// With DesiredSkillEntries
	ver := "1.0.0"
	s1 := snapshotToModelSkills(nil, &client.PaperclipAgentSkillSnapshot{
		DesiredSkillEntries: []client.PaperclipDesiredSkillEntry{
			{Key: "k1", VersionID: &ver},
			{Key: "k2", VersionID: nil},
		},
	})
	if len(s1) != 2 || s1[0].VersionID.ValueString() != "1.0.0" || !s1[1].VersionID.IsNull() {
		t.Fatalf("unexpected models: %+v", s1)
	}
	// With legacy DesiredSkills
	s2 := snapshotToModelSkills(nil, &client.PaperclipAgentSkillSnapshot{
		DesiredSkills: []string{"k1", "k2"},
	})
	if len(s2) != 2 || s2[0].Key.ValueString() != "k1" || !s2[0].VersionID.IsNull() {
		t.Fatalf("unexpected legacy models: %+v", s2)
	}
	// Empty snapshot
	s3 := snapshotToModelSkills(nil, &client.PaperclipAgentSkillSnapshot{})
	if len(s3) != 0 {
		t.Fatal("expected empty models")
	}

	// 3. Configure
	r := NewPaperclipSkillAssignmentResource().(*PaperclipSkillAssignmentResource)
	var confResp resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: nil}, &confResp)
	if confResp.Diagnostics.HasError() {
		t.Fatal("expected no error on nil")
	}
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: 123}, &confResp)
	if !confResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid provider data")
	}

	// Mock server for error conditions
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/agents/ag-404/skills":
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		case req.Method == http.MethodGet && req.URL.Path == "/api/agents/ag-err/skills":
			http.Error(w, `{"error":"server error"}`, http.StatusInternalServerError)
		case req.Method == http.MethodPost && req.URL.Path == "/api/agents/ag-err/skills/sync":
			http.Error(w, `{"error":"server error"}`, http.StatusInternalServerError)
		case req.Method == http.MethodPost && req.URL.Path == "/api/agents/ag-404/skills/sync":
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	schema := configureTestResource(t, server.URL, r)

	// Create error
	planErr := tfsdk.Plan{Schema: schema}
	_ = planErr.Set(ctx, &PaperclipSkillAssignmentResourceModel{
		AgentID: types.StringValue("ag-err"),
	})
	var createErrResp resource.CreateResponse
	r.Create(ctx, resource.CreateRequest{Plan: planErr}, &createErrResp)
	if !createErrResp.Diagnostics.HasError() {
		t.Fatal("expected error on sync failure")
	}

	// Read 404 (removes resource)
	state404 := tfsdk.State{Schema: schema}
	_ = state404.Set(ctx, &PaperclipSkillAssignmentResourceModel{
		AgentID: types.StringValue("ag-404"),
	})
	var read404Resp resource.ReadResponse
	read404Resp.State = state404
	r.Read(ctx, resource.ReadRequest{State: state404}, &read404Resp)
	if read404Resp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on 404 read: %v", read404Resp.Diagnostics)
	}

	// Read 500 error
	state500 := tfsdk.State{Schema: schema}
	_ = state500.Set(ctx, &PaperclipSkillAssignmentResourceModel{
		AgentID: types.StringValue("ag-err"),
	})
	var read500Resp resource.ReadResponse
	read500Resp.State = state500
	r.Read(ctx, resource.ReadRequest{State: state500}, &read500Resp)
	if !read500Resp.Diagnostics.HasError() {
		t.Fatal("expected error on 500 read")
	}

	// Update error
	var updErrResp resource.UpdateResponse
	r.Update(ctx, resource.UpdateRequest{Plan: planErr, State: state500}, &updErrResp)
	if !updErrResp.Diagnostics.HasError() {
		t.Fatal("expected error on update failure")
	}

	// Delete 500 error
	var del500Resp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: state500}, &del500Resp)
	if !del500Resp.Diagnostics.HasError() {
		t.Fatal("expected error on 500 delete")
	}

	// Delete 404 ignored
	var del404Resp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: state404}, &del404Resp)
	if del404Resp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on 404 delete: %v", del404Resp.Diagnostics)
	}

	// ImportState empty ID
	var impResp resource.ImportStateResponse
	r.ImportState(ctx, resource.ImportStateRequest{ID: ""}, &impResp)
	if !impResp.Diagnostics.HasError() {
		t.Fatal("expected error on empty import ID")
	}
}

func TestSkillResourceErrorBranchesAndRenameScenarios(t *testing.T) {
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/api/companies/comp-1/skills/import":
			// Return imported skill with name "orig-name", slug "orig-slug"
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"imported": []map[string]interface{}{
					{
						"id":        "skill-imp-ren",
						"companyId": "comp-1",
						"name":      "orig-name",
						"slug":      "orig-slug",
						"markdown":  "# Content",
					},
				},
			})
		case req.Method == http.MethodPost && req.URL.Path == "/api/companies/comp-1/skills/install-catalog":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"skill": map[string]interface{}{
					"id":        "skill-cat-ren",
					"companyId": "comp-1",
					"name":      "orig-cat-name",
					"slug":      "orig-cat-slug",
				},
			})
		case req.Method == http.MethodPost && (req.URL.Path == "/api/companies/comp-1/skills/skill-imp-ren/rename" ||
			req.URL.Path == "/api/companies/comp-1/skills/skill-cat-ren/rename"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"skill": map[string]interface{}{
					"id":        "renamed-id",
					"companyId": "comp-1",
					"name":      "new-name",
					"slug":      "new-slug",
				},
			})
		case req.Method == http.MethodPost && req.URL.Path == "/api/companies/comp-err/skills/import":
			http.Error(w, `{"error":"import failed"}`, http.StatusInternalServerError)
		case req.Method == http.MethodPost && req.URL.Path == "/api/companies/comp-err/skills/install-catalog":
			http.Error(w, `{"error":"install failed"}`, http.StatusInternalServerError)
		case req.Method == http.MethodPost && req.URL.Path == "/api/companies/comp-err/skills":
			http.Error(w, `{"error":"create failed"}`, http.StatusInternalServerError)
		case req.Method == http.MethodPost && req.URL.Path == "/api/companies/comp-1/skills":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":        "sk-created",
				"companyId": "comp-1",
				"name":      "Created",
				"slug":      "created",
			})
		case req.Method == http.MethodPut && req.URL.Path == "/api/companies/comp-1/skills/sk-created/files":
			http.Error(w, `{"error":"file upload failed"}`, http.StatusInternalServerError)
		case req.Method == http.MethodPost && req.URL.Path == "/api/companies/comp-1/skills/sk-created/versions":
			http.Error(w, `{"error":"version create failed"}`, http.StatusInternalServerError)
		case req.Method == http.MethodGet && (req.URL.Path == "/api/companies/comp-1/skills/sk-created" || req.URL.Path == "/api/companies/comp-1/skills/renamed-id"):
			id := "sk-created"
			if req.URL.Path == "/api/companies/comp-1/skills/renamed-id" {
				id = "renamed-id"
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":        id,
				"companyId": "comp-1",
				"name":      "Created",
				"slug":      "created",
			})
		case req.Method == http.MethodPatch && req.URL.Path == "/api/companies/comp-1/skills/sk-created":
			http.Error(w, `{"error":"update failed"}`, http.StatusInternalServerError)
		case req.Method == http.MethodPost && req.URL.Path == "/api/companies/comp-1/skills/sk-created/rename":
			http.Error(w, `{"error":"rename failed"}`, http.StatusInternalServerError)
		case req.Method == http.MethodDelete && req.URL.Path == "/api/companies/comp-1/skills/sk-created/files":
			http.Error(w, `{"error":"file delete failed"}`, http.StatusInternalServerError)
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	r := NewPaperclipSkillResource().(*PaperclipSkillResource)
	schema := configureTestResource(t, server.URL, r)

	// 1. Import with rename
	planImp := tfsdk.Plan{Schema: schema}
	_ = planImp.Set(ctx, &PaperclipSkillResourceModel{
		CompanyID:     types.StringValue("comp-1"),
		Name:          types.StringValue("new-name"),
		Slug:          types.StringValue("new-slug"),
		ImportSource:  types.StringValue("https://example.com/source"),
		Categories:    types.ListNull(types.StringType),
		Files:         types.MapNull(types.StringType),
		FileInventory: types.ListNull(fileInventoryObjectType),
	})
	var createImpResp resource.CreateResponse
	createImpResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: planImp}, &createImpResp)
	if createImpResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on import with rename: %v", createImpResp.Diagnostics)
	}

	// 2. Catalog install with rename
	planCat := tfsdk.Plan{Schema: schema}
	_ = planCat.Set(ctx, &PaperclipSkillResourceModel{
		CompanyID:      types.StringValue("comp-1"),
		Name:           types.StringValue("new-name"),
		CatalogSkillID: types.StringValue("catalog-1"),
		Categories:     types.ListNull(types.StringType),
		Files:          types.MapNull(types.StringType),
		FileInventory:  types.ListNull(fileInventoryObjectType),
	})
	var createCatResp resource.CreateResponse
	createCatResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: planCat}, &createCatResp)
	if createCatResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on catalog with rename: %v", createCatResp.Diagnostics)
	}

	// 3. Import failure
	planImpErr := tfsdk.Plan{Schema: schema}
	_ = planImpErr.Set(ctx, &PaperclipSkillResourceModel{
		CompanyID:     types.StringValue("comp-err"),
		ImportSource:  types.StringValue("https://example.com/source"),
		Categories:    types.ListNull(types.StringType),
		Files:         types.MapNull(types.StringType),
		FileInventory: types.ListNull(fileInventoryObjectType),
	})
	var createImpErrResp resource.CreateResponse
	r.Create(ctx, resource.CreateRequest{Plan: planImpErr}, &createImpErrResp)
	if !createImpErrResp.Diagnostics.HasError() {
		t.Fatal("expected error on import failure")
	}

	// 4. Catalog failure
	planCatErr := tfsdk.Plan{Schema: schema}
	_ = planCatErr.Set(ctx, &PaperclipSkillResourceModel{
		CompanyID:      types.StringValue("comp-err"),
		CatalogSkillID: types.StringValue("cat-1"),
		Categories:     types.ListNull(types.StringType),
		Files:          types.MapNull(types.StringType),
		FileInventory:  types.ListNull(fileInventoryObjectType),
	})
	var createCatErrResp resource.CreateResponse
	r.Create(ctx, resource.CreateRequest{Plan: planCatErr}, &createCatErrResp)
	if !createCatErrResp.Diagnostics.HasError() {
		t.Fatal("expected error on catalog install failure")
	}

	// 5. Create standard failure
	planCreateErr := tfsdk.Plan{Schema: schema}
	_ = planCreateErr.Set(ctx, &PaperclipSkillResourceModel{
		CompanyID:     types.StringValue("comp-err"),
		Name:          types.StringValue("Name"),
		Categories:    types.ListNull(types.StringType),
		Files:         types.MapNull(types.StringType),
		FileInventory: types.ListNull(fileInventoryObjectType),
	})
	var createErrResp resource.CreateResponse
	r.Create(ctx, resource.CreateRequest{Plan: planCreateErr}, &createErrResp)
	if !createErrResp.Diagnostics.HasError() {
		t.Fatal("expected error on create failure")
	}

	// 6. Auxiliary file failure during create
	filesMap := map[string]string{"aux.sh": "echo"}
	filesMapVal, _ := types.MapValueFrom(ctx, types.StringType, filesMap)
	planFileErr := tfsdk.Plan{Schema: schema}
	_ = planFileErr.Set(ctx, &PaperclipSkillResourceModel{
		CompanyID:     types.StringValue("comp-1"),
		Name:          types.StringValue("Created"),
		Files:         filesMapVal,
		Categories:    types.ListNull(types.StringType),
		FileInventory: types.ListNull(fileInventoryObjectType),
	})
	var createFileErrResp resource.CreateResponse
	r.Create(ctx, resource.CreateRequest{Plan: planFileErr}, &createFileErrResp)
	if !createFileErrResp.Diagnostics.HasError() {
		t.Fatal("expected error on auxiliary file upload failure")
	}

	// 7. Version snapshot failure during create
	planVerErr := tfsdk.Plan{Schema: schema}
	_ = planVerErr.Set(ctx, &PaperclipSkillResourceModel{
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("Created"),
		VersionSnapshot: types.StringValue("v1"),
		Categories:      types.ListNull(types.StringType),
		Files:           types.MapNull(types.StringType),
		FileInventory:   types.ListNull(fileInventoryObjectType),
	})
	var createVerErrResp resource.CreateResponse
	r.Create(ctx, resource.CreateRequest{Plan: planVerErr}, &createVerErrResp)
	if !createVerErrResp.Diagnostics.HasError() {
		t.Fatal("expected error on version snapshot failure")
	}

	// 8. Update: rename failure
	planRenameErr := tfsdk.Plan{Schema: schema}
	_ = planRenameErr.Set(ctx, &PaperclipSkillResourceModel{
		ID:            types.StringValue("sk-created"),
		CompanyID:     types.StringValue("comp-1"),
		Name:          types.StringValue("New Name"),
		Categories:    types.ListNull(types.StringType),
		Files:         types.MapNull(types.StringType),
		FileInventory: types.ListNull(fileInventoryObjectType),
	})
	stateOrig := tfsdk.State{Schema: schema}
	_ = stateOrig.Set(ctx, &PaperclipSkillResourceModel{
		ID:            types.StringValue("sk-created"),
		CompanyID:     types.StringValue("comp-1"),
		Name:          types.StringValue("Created"),
		Slug:          types.StringValue("created"),
		Categories:    types.ListNull(types.StringType),
		Files:         types.MapNull(types.StringType),
		FileInventory: types.ListNull(fileInventoryObjectType),
	})
	var updRenameErrResp resource.UpdateResponse
	r.Update(ctx, resource.UpdateRequest{Plan: planRenameErr, State: stateOrig}, &updRenameErrResp)
	if !updRenameErrResp.Diagnostics.HasError() {
		t.Fatal("expected error on rename failure")
	}

	// 9. Update: invalid metadata JSON
	planBadMeta := tfsdk.Plan{Schema: schema}
	_ = planBadMeta.Set(ctx, &PaperclipSkillResourceModel{
		ID:            types.StringValue("sk-created"),
		CompanyID:     types.StringValue("comp-1"),
		Name:          types.StringValue("Created"),
		Metadata:      types.StringValue("invalid json{"),
		Categories:    types.ListNull(types.StringType),
		Files:         types.MapNull(types.StringType),
		FileInventory: types.ListNull(fileInventoryObjectType),
	})
	var updBadMetaResp resource.UpdateResponse
	r.Update(ctx, resource.UpdateRequest{Plan: planBadMeta, State: stateOrig}, &updBadMetaResp)
	if !updBadMetaResp.Diagnostics.HasError() {
		t.Fatal("expected error on bad metadata JSON during update")
	}

	// 10. Update: UpdateSkill failure
	planUpdFail := tfsdk.Plan{Schema: schema}
	_ = planUpdFail.Set(ctx, &PaperclipSkillResourceModel{
		ID:            types.StringValue("sk-created"),
		CompanyID:     types.StringValue("comp-1"),
		Name:          types.StringValue("Created"),
		Slug:          types.StringValue("created"),
		Categories:    types.ListNull(types.StringType),
		Files:         types.MapNull(types.StringType),
		FileInventory: types.ListNull(fileInventoryObjectType),
	})
	var updErrResp resource.UpdateResponse
	r.Update(ctx, resource.UpdateRequest{Plan: planUpdFail, State: stateOrig}, &updErrResp)
	if !updErrResp.Diagnostics.HasError() {
		t.Fatal("expected error on update failure")
	}

	// 11. Create: empty Content with SKILL.md in filesMap (lines 353 and 418)
	filesWithSkill := map[string]string{"SKILL.md": "# From Files", "aux.sh": "echo ok"}
	filesWithSkillVal, _ := types.MapValueFrom(ctx, types.StringType, filesWithSkill)
	planSkillFile := tfsdk.Plan{Schema: schema}
	_ = planSkillFile.Set(ctx, &PaperclipSkillResourceModel{
		CompanyID:     types.StringValue("comp-1"),
		Name:          types.StringValue("Created"),
		Slug:          types.StringValue("created"),
		Files:         filesWithSkillVal,
		Categories:    types.ListNull(types.StringType),
		FileInventory: types.ListNull(fileInventoryObjectType),
	})
	var createSkillFileResp resource.CreateResponse
	createSkillFileResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: planSkillFile}, &createSkillFileResp)
	// Notice: aux.sh returns 500 on PUT, but SKILL.md was extracted and continued

	// 12. PopulateSkillModel with folder, forked, and categories
	var mod PaperclipSkillResourceModel
	populateSkillModel(ctx, &mod, &client.PaperclipSkill{
		ID:           "sk-1",
		FolderID:     "fld-1",
		ForkedFromID: "fork-1",
		Categories:   []string{"cat1", "cat2"},
	}, types.MapNull(types.StringType))
	if mod.FolderID.ValueString() != "fld-1" || mod.ForkedFromID.ValueString() != "fork-1" || mod.Categories.IsNull() || len(mod.Categories.Elements()) != 2 {
		t.Fatalf("unexpected populated model: %+v", mod)
	}
}

func TestSkillUpdateFileReconciliationAndAllRemainingBranches(t *testing.T) {
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-1/skills/sk-good":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":        "sk-good",
				"companyId": "comp-1",
				"name":      "Good Skill",
				"slug":      "good-skill",
			})
		case req.Method == http.MethodPatch && req.URL.Path == "/api/companies/comp-1/skills/sk-good":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":        "sk-good",
				"companyId": "comp-1",
				"name":      "Good Skill",
				"slug":      "good-skill",
			})
		case req.Method == http.MethodDelete && req.URL.Path == "/api/companies/comp-1/skills/sk-good/files":
			var body map[string]interface{}
			_ = json.NewDecoder(req.Body).Decode(&body)
			if body["path"] == "fail-del.txt" {
				http.Error(w, `{"error":"del failed"}`, http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
		case (req.Method == http.MethodPut || req.Method == http.MethodPatch) && req.URL.Path == "/api/companies/comp-1/skills/sk-good/files":
			var body map[string]interface{}
			_ = json.NewDecoder(req.Body).Decode(&body)
			if body["path"] == "fail-upd.txt" {
				http.Error(w, `{"error":"upd failed"}`, http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"path":    body["path"],
				"content": body["content"],
			})
		case req.Method == http.MethodPost && req.URL.Path == "/api/companies/comp-1/skills/sk-good/versions":
			var body map[string]interface{}
			_ = json.NewDecoder(req.Body).Decode(&body)
			if body["version"] == "fail-ver" || body["label"] == "fail-ver" {
				http.Error(w, `{"error":"ver failed"}`, http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":      "ver-1",
				"version": body["label"],
			})
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	r := NewPaperclipSkillResource().(*PaperclipSkillResource)
	schema := configureTestResource(t, server.URL, r)

	// 1. Successful Update with SKILL.md in state and plan, file delete, file update, file add, version snapshot
	stateFiles := map[string]string{
		"SKILL.md":   "# State Skill",
		"del.txt":    "to delete",
		"update.txt": "old text",
	}
	stateFilesVal, _ := types.MapValueFrom(ctx, types.StringType, stateFiles)
	goodState := tfsdk.State{Schema: schema}
	_ = goodState.Set(ctx, &PaperclipSkillResourceModel{
		ID:              types.StringValue("sk-good"),
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("Good Skill"),
		Slug:            types.StringValue("good-skill"),
		VersionSnapshot: types.StringValue("v1"),
		Files:           stateFilesVal,
		Categories:      types.ListNull(types.StringType),
		FileInventory:   types.ListNull(fileInventoryObjectType),
	})

	planFiles := map[string]string{
		"SKILL.md":   "# Plan Skill",
		"update.txt": "new text",
		"add.txt":    "brand new",
	}
	planFilesVal, _ := types.MapValueFrom(ctx, types.StringType, planFiles)
	goodPlan := tfsdk.Plan{Schema: schema}
	_ = goodPlan.Set(ctx, &PaperclipSkillResourceModel{
		ID:              types.StringValue("sk-good"),
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("Good Skill"),
		Slug:            types.StringValue("good-skill"),
		VersionSnapshot: types.StringValue("v2"),
		Files:           planFilesVal,
		Categories:      types.ListNull(types.StringType),
		FileInventory:   types.ListNull(fileInventoryObjectType),
	})

	var updGoodResp resource.UpdateResponse
	updGoodResp.State = tfsdk.State{Schema: schema}
	r.Update(ctx, resource.UpdateRequest{Plan: goodPlan, State: goodState}, &updGoodResp)
	if updGoodResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on multi-file update: %v", updGoodResp.Diagnostics)
	}

	// 2. Delete file error
	stateDelFail := map[string]string{"fail-del.txt": "content"}
	stateDelFailVal, _ := types.MapValueFrom(ctx, types.StringType, stateDelFail)
	stateDelFailState := tfsdk.State{Schema: schema}
	_ = stateDelFailState.Set(ctx, &PaperclipSkillResourceModel{
		ID:            types.StringValue("sk-good"),
		CompanyID:     types.StringValue("comp-1"),
		Name:          types.StringValue("Good Skill"),
		Slug:          types.StringValue("good-skill"),
		Files:         stateDelFailVal,
		Categories:    types.ListNull(types.StringType),
		FileInventory: types.ListNull(fileInventoryObjectType),
	})
	planEmptyFiles := map[string]string{}
	planEmptyFilesVal, _ := types.MapValueFrom(ctx, types.StringType, planEmptyFiles)
	planDelFailPlan := tfsdk.Plan{Schema: schema}
	_ = planDelFailPlan.Set(ctx, &PaperclipSkillResourceModel{
		ID:            types.StringValue("sk-good"),
		CompanyID:     types.StringValue("comp-1"),
		Name:          types.StringValue("Good Skill"),
		Slug:          types.StringValue("good-skill"),
		Files:         planEmptyFilesVal,
		Categories:    types.ListNull(types.StringType),
		FileInventory: types.ListNull(fileInventoryObjectType),
	})
	var updDelFailResp resource.UpdateResponse
	updDelFailResp.State = tfsdk.State{Schema: schema}
	r.Update(ctx, resource.UpdateRequest{Plan: planDelFailPlan, State: stateDelFailState}, &updDelFailResp)
	if !updDelFailResp.Diagnostics.HasError() {
		t.Fatal("expected error on DeleteSkillFile failure")
	}

	// 3. Update file error
	planUpdFailFiles := map[string]string{"fail-upd.txt": "content"}
	planUpdFailFilesVal, _ := types.MapValueFrom(ctx, types.StringType, planUpdFailFiles)
	planUpdFailPlan := tfsdk.Plan{Schema: schema}
	_ = planUpdFailPlan.Set(ctx, &PaperclipSkillResourceModel{
		ID:            types.StringValue("sk-good"),
		CompanyID:     types.StringValue("comp-1"),
		Name:          types.StringValue("Good Skill"),
		Slug:          types.StringValue("good-skill"),
		Files:         planUpdFailFilesVal,
		Categories:    types.ListNull(types.StringType),
		FileInventory: types.ListNull(fileInventoryObjectType),
	})
	var updUpdFailResp resource.UpdateResponse
	updUpdFailResp.State = tfsdk.State{Schema: schema}
	r.Update(ctx, resource.UpdateRequest{Plan: planUpdFailPlan, State: goodState}, &updUpdFailResp)
	if !updUpdFailResp.Diagnostics.HasError() {
		t.Fatal("expected error on UpdateSkillFile failure")
	}

	// 4. Version error
	planVerFailPlan := tfsdk.Plan{Schema: schema}
	_ = planVerFailPlan.Set(ctx, &PaperclipSkillResourceModel{
		ID:              types.StringValue("sk-good"),
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("Good Skill"),
		Slug:            types.StringValue("good-skill"),
		VersionSnapshot: types.StringValue("fail-ver"),
		Categories:      types.ListNull(types.StringType),
		Files:           types.MapNull(types.StringType),
		FileInventory:   types.ListNull(fileInventoryObjectType),
	})
	var updVerFailResp resource.UpdateResponse
	updVerFailResp.State = tfsdk.State{Schema: schema}
	r.Update(ctx, resource.UpdateRequest{Plan: planVerFailPlan, State: goodState}, &updVerFailResp)
	if !updVerFailResp.Diagnostics.HasError() {
		t.Fatal("expected error on CreateSkillVersion failure")
	}
}

func TestSkillResourceFullCoverageRemainingBranches(t *testing.T) {
	ctx := context.Background()

	// 1. Int64 plan modifier coverage
	pm := int64UseStateForUnknown()
	if pm.Description(ctx) == "" || pm.MarkdownDescription(ctx) == "" {
		t.Fatal("empty description")
	}
	var respA planmodifier.Int64Response
	pm.PlanModifyInt64(ctx, planmodifier.Int64Request{State: tfsdk.State{}}, &respA)
	if !respA.PlanValue.IsNull() && !respA.PlanValue.IsUnknown() {
		t.Fatal("expected no change on null state")
	}
	r0 := NewPaperclipSkillResource().(*PaperclipSkillResource)
	schema0 := configureTestResource(t, "http://127.0.0.1", r0)
	dummyState := tfsdk.State{Schema: schema0}
	if diags := dummyState.Set(ctx, &PaperclipSkillResourceModel{
		ID:            types.StringValue("sk-1"),
		CompanyID:     types.StringValue("comp-1"),
		Name:          types.StringValue("name"),
		Slug:          types.StringValue("slug"),
		Categories:    types.ListNull(types.StringType),
		Files:         types.MapNull(types.StringType),
		FileInventory: types.ListNull(fileInventoryObjectType),
	}); diags.HasError() {
		t.Fatalf("failed to set dummy state: %v", diags)
	}

	var respB planmodifier.Int64Response
	respB.PlanValue = types.Int64Value(10)
	pm.PlanModifyInt64(ctx, planmodifier.Int64Request{
		State:     dummyState,
		PlanValue: types.Int64Value(10),
	}, &respB)
	if respB.PlanValue.ValueInt64() != 10 {
		t.Fatal("expected plan value preserved")
	}
	var respC planmodifier.Int64Response
	pm.PlanModifyInt64(ctx, planmodifier.Int64Request{
		State:       dummyState,
		PlanValue:   types.Int64Unknown(),
		ConfigValue: types.Int64Unknown(),
	}, &respC)
	var respD planmodifier.Int64Response
	pm.PlanModifyInt64(ctx, planmodifier.Int64Request{
		State:       dummyState,
		StateValue:  types.Int64Value(42),
		PlanValue:   types.Int64Unknown(),
		ConfigValue: types.Int64Null(),
	}, &respD)
	if respD.PlanValue.ValueInt64() != 42 {
		t.Fatalf("expected 42, got %v", respD.PlanValue)
	}

	// 2. populateSkillModel branch coverage
	var mod PaperclipSkillResourceModel
	mod.CompanyID = types.StringValue("comp-fallback")
	mod.VersionSnapshot = types.StringUnknown()
	populateSkillModel(ctx, &mod, &client.PaperclipSkill{
		ID:             "sk-1",
		Slug:           "fallback-slug",
		CurrentVersion: &client.PaperclipSkillVersion{Label: "v1.2.3"},
	}, types.MapNull(types.StringType))
	if mod.Key.ValueString() != "company/comp-fallback/fallback-slug" {
		t.Fatalf("expected fallback key, got %v", mod.Key)
	}
	if mod.VersionSnapshot.ValueString() != "v1.2.3" {
		t.Fatalf("expected v1.2.3 version snapshot, got %v", mod.VersionSnapshot)
	}

	var mod2 PaperclipSkillResourceModel
	mod2.VersionSnapshot = types.StringUnknown()
	populateSkillModel(ctx, &mod2, &client.PaperclipSkill{
		ID:   "sk-2",
		Slug: "slug-2",
	}, types.MapNull(types.StringType))
	if !mod2.VersionSnapshot.IsNull() {
		t.Fatalf("expected null version snapshot, got %v", mod2.VersionSnapshot)
	}

	var updGetCount int
	// 3. Mock server for Create/Read/Update edge branches
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		// Import rename error
		case req.Method == http.MethodPost && req.URL.Path == "/api/companies/comp-imp-ren-err/skills/import":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"imported": []map[string]interface{}{{"id": "imp-1", "name": "orig", "slug": "orig"}},
			})
		case req.Method == http.MethodPost && req.URL.Path == "/api/companies/comp-imp-ren-err/skills/imp-1/rename":
			http.Error(w, `{"error":"rename failed"}`, http.StatusInternalServerError)

		// Catalog rename error
		case req.Method == http.MethodPost && req.URL.Path == "/api/companies/comp-cat-ren-err/skills/install-catalog":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"skill": map[string]interface{}{"id": "cat-1", "name": "orig-cat", "slug": "orig-cat"},
			})
		case req.Method == http.MethodPost && req.URL.Path == "/api/companies/comp-cat-ren-err/skills/cat-1/rename":
			http.Error(w, `{"error":"catalog rename failed"}`, http.StatusInternalServerError)

		// Create GetSkill error
		case req.Method == http.MethodPost && req.URL.Path == "/api/companies/comp-create-get-err/skills":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":   "cr-1",
				"name": "Created",
				"slug": "created",
			})
		case req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-create-get-err/skills/cr-1":
			http.Error(w, `{"error":"get after create failed"}`, http.StatusInternalServerError)

		// Create with auxiliary files including SKILL.md (covers continue on SKILL.md in Create)
		case req.Method == http.MethodPost && req.URL.Path == "/api/companies/comp-files-create/skills":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":   "sk-files",
				"name": "WithFiles",
				"slug": "with-files",
			})
		case req.Method == http.MethodPatch && req.URL.Path == "/api/companies/comp-files-create/skills/sk-files/files":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"path": "tool.sh", "content": "echo 1"})
		case req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-files-create/skills/sk-files":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":        "sk-files",
				"companyId": "comp-files-create",
				"name":      "WithFiles",
				"slug":      "with-files",
				"files": map[string]interface{}{
					"SKILL.md": "skill content",
					"tool.sh":  "echo 1",
				},
			})

		// Read 500 error
		case req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-1/skills/sk-500":
			http.Error(w, `{"error":"get failed"}`, http.StatusInternalServerError)

		// Read file 500 error
		case req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-1/skills/sk-file-err":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "sk-file-err", "companyId": "comp-1", "name": "S", "slug": "s"})
		case req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-1/skills/sk-file-err/files" && req.URL.Query().Get("path") == "fail.txt":
			http.Error(w, `{"error":"file read error"}`, http.StatusInternalServerError)

		// Read file 404 (file removed remotely)
		case req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-1/skills/sk-file-404":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "sk-file-404", "companyId": "comp-1", "name": "S", "slug": "s"})
		case req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-1/skills/sk-file-404/files" && req.URL.Query().Get("path") == "gone.txt":
			http.Error(w, `{"error":"not found"}`, http.StatusNotFound)

		// Read skill with FileInventory auxiliary files (leaves state.Files null when not in state)
		case req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-1/skills/sk-inv":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":        "sk-inv",
				"companyId": "comp-1",
				"name":      "S",
				"slug":      "s",
				"fileInventory": []map[string]interface{}{
					{"path": "SKILL.md", "kind": "instructions"},
					{"path": "tool.py", "kind": "auxiliary"},
				},
			})

		// Update with unmanaged files (plan.Files is null)
		case req.Method == http.MethodPatch && req.URL.Path == "/api/companies/comp-unmanaged-upd/skills/sk-unmanaged":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "sk-unmanaged", "companyId": "comp-unmanaged-upd", "name": "Unm", "slug": "unm"})
		case req.Method == http.MethodPatch && req.URL.Path == "/api/companies/comp-unmanaged-upd/skills/sk-unmanaged/files":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"path": "SKILL.md", "content": ""})
		case req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-unmanaged-upd/skills/sk-unmanaged":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":        "sk-unmanaged",
				"companyId": "comp-unmanaged-upd",
				"name":      "Unm",
				"slug":      "unm",
				"fileInventory": []map[string]interface{}{
					{"path": "SKILL.md", "kind": "instructions"},
					{"path": "helper.sh", "kind": "auxiliary"},
				},
			})
		case req.Method == http.MethodDelete && strings.Contains(req.URL.Path, "sk-unmanaged/files"):
			t.Fatal("unexpected delete call for unmanaged skill files")

		// Update GetSkill error
		case req.Method == http.MethodPatch && req.URL.Path == "/api/companies/comp-upd-get-err/skills/sk-upd-get-err":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "sk-upd-get-err", "companyId": "comp-upd-get-err", "name": "Upd", "slug": "upd"})
		case req.Method == http.MethodPatch && req.URL.Path == "/api/companies/comp-upd-get-err/skills/sk-upd-get-err/files":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"path": "SKILL.md", "content": "c"})
		case req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-upd-get-err/skills/sk-upd-get-err":
			updGetCount++
			if updGetCount == 1 {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"id":        "sk-upd-get-err",
					"companyId": "comp-upd-get-err",
					"name":      "Upd",
					"slug":      "upd",
				})
				return
			}
			http.Error(w, `{"error":"get after update failed"}`, http.StatusInternalServerError)

		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	r := NewPaperclipSkillResource().(*PaperclipSkillResource)
	schema := configureTestResource(t, server.URL, r)

	// Create: Import rename error
	planImpRenErr := tfsdk.Plan{Schema: schema}
	_ = planImpRenErr.Set(ctx, &PaperclipSkillResourceModel{
		CompanyID:     types.StringValue("comp-imp-ren-err"),
		Name:          types.StringValue("renamed-name"),
		Slug:          types.StringValue("renamed-slug"),
		ImportSource:  types.StringValue("https://example.com"),
		Categories:    types.ListNull(types.StringType),
		Files:         types.MapNull(types.StringType),
		FileInventory: types.ListNull(fileInventoryObjectType),
	})
	var createImpRenErrResp resource.CreateResponse
	r.Create(ctx, resource.CreateRequest{Plan: planImpRenErr}, &createImpRenErrResp)
	if !createImpRenErrResp.Diagnostics.HasError() {
		t.Fatal("expected error on import rename failure")
	}
	if createImpRenErrResp.Diagnostics.Errors()[0].Summary() != "Error renaming imported Paperclip skill" {
		t.Fatalf("unexpected summary: %s", createImpRenErrResp.Diagnostics.Errors()[0].Summary())
	}

	// Create: Catalog rename error
	planCatRenErr := tfsdk.Plan{Schema: schema}
	_ = planCatRenErr.Set(ctx, &PaperclipSkillResourceModel{
		CompanyID:      types.StringValue("comp-cat-ren-err"),
		Name:           types.StringValue("renamed-cat-name"),
		CatalogSkillID: types.StringValue("cat-1"),
		Categories:     types.ListNull(types.StringType),
		Files:          types.MapNull(types.StringType),
		FileInventory:  types.ListNull(fileInventoryObjectType),
	})
	var createCatRenErrResp resource.CreateResponse
	r.Create(ctx, resource.CreateRequest{Plan: planCatRenErr}, &createCatRenErrResp)
	if !createCatRenErrResp.Diagnostics.HasError() {
		t.Fatal("expected error on catalog rename failure")
	}
	if createCatRenErrResp.Diagnostics.Errors()[0].Summary() != "Error renaming installed catalog skill" {
		t.Fatalf("unexpected summary: %s", createCatRenErrResp.Diagnostics.Errors()[0].Summary())
	}

	// Create: GetSkill error
	planCrGetErr := tfsdk.Plan{Schema: schema}
	_ = planCrGetErr.Set(ctx, &PaperclipSkillResourceModel{
		CompanyID:     types.StringValue("comp-create-get-err"),
		Name:          types.StringValue("Created"),
		Slug:          types.StringValue("created"),
		Categories:    types.ListNull(types.StringType),
		Files:         types.MapNull(types.StringType),
		FileInventory: types.ListNull(fileInventoryObjectType),
	})
	var createCrGetErrResp resource.CreateResponse
	r.Create(ctx, resource.CreateRequest{Plan: planCrGetErr}, &createCrGetErrResp)
	if !createCrGetErrResp.Diagnostics.HasError() {
		t.Fatal("expected error on GetSkill failure after create")
	}
	if createCrGetErrResp.Diagnostics.Errors()[0].Summary() != "Error fetching created Paperclip skill" {
		t.Fatalf("unexpected summary: %s", createCrGetErrResp.Diagnostics.Errors()[0].Summary())
	}

	// Create: with auxiliary files including SKILL.md (covers continue on SKILL.md in Create)
	filesWithSkillMd, _ := types.MapValueFrom(ctx, types.StringType, map[string]string{
		"SKILL.md": "# Title",
		"tool.sh":  "echo 1",
	})
	planFilesSkillMd := tfsdk.Plan{Schema: schema}
	_ = planFilesSkillMd.Set(ctx, &PaperclipSkillResourceModel{
		CompanyID:     types.StringValue("comp-files-create"),
		Name:          types.StringValue("WithFiles"),
		Slug:          types.StringValue("with-files"),
		Categories:    types.ListNull(types.StringType),
		Files:         filesWithSkillMd,
		FileInventory: types.ListNull(fileInventoryObjectType),
	})
	createFilesSkillMdResp := resource.CreateResponse{State: tfsdk.State{Schema: schema}}
	r.Create(ctx, resource.CreateRequest{Plan: planFilesSkillMd}, &createFilesSkillMdResp)
	if createFilesSkillMdResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on create with files: %v", createFilesSkillMdResp.Diagnostics)
	}

	// Read: 500 error
	state500 := tfsdk.State{Schema: schema}
	_ = state500.Set(ctx, &PaperclipSkillResourceModel{
		CompanyID:     types.StringValue("comp-1"),
		ID:            types.StringValue("sk-500"),
		Name:          types.StringValue("S"),
		Slug:          types.StringValue("s"),
		Categories:    types.ListNull(types.StringType),
		FileInventory: types.ListNull(fileInventoryObjectType),
		Files:         types.MapNull(types.StringType),
	})
	var read500Resp resource.ReadResponse
	read500Resp.State = state500
	r.Read(ctx, resource.ReadRequest{State: state500}, &read500Resp)
	if !read500Resp.Diagnostics.HasError() {
		t.Fatal("expected error on 500 GetSkill")
	}

	// Read: file 500 error
	filesErrMap, _ := types.MapValueFrom(ctx, types.StringType, map[string]string{"fail.txt": "content"})
	stateFileErr := tfsdk.State{Schema: schema}
	_ = stateFileErr.Set(ctx, &PaperclipSkillResourceModel{
		CompanyID:     types.StringValue("comp-1"),
		ID:            types.StringValue("sk-file-err"),
		Name:          types.StringValue("S"),
		Slug:          types.StringValue("s"),
		Categories:    types.ListNull(types.StringType),
		FileInventory: types.ListNull(fileInventoryObjectType),
		Files:         filesErrMap,
	})
	var readFileErrResp resource.ReadResponse
	readFileErrResp.State = stateFileErr
	r.Read(ctx, resource.ReadRequest{State: stateFileErr}, &readFileErrResp)
	if !readFileErrResp.Diagnostics.HasError() {
		t.Fatal("expected error on file 500 read")
	}

	// Read: file 404 (drift to null)
	files404Map, _ := types.MapValueFrom(ctx, types.StringType, map[string]string{"gone.txt": "content"})
	stateFile404 := tfsdk.State{Schema: schema}
	_ = stateFile404.Set(ctx, &PaperclipSkillResourceModel{
		CompanyID:     types.StringValue("comp-1"),
		ID:            types.StringValue("sk-file-404"),
		Name:          types.StringValue("S"),
		Slug:          types.StringValue("s"),
		Categories:    types.ListNull(types.StringType),
		FileInventory: types.ListNull(fileInventoryObjectType),
		Files:         files404Map,
	})
	var readFile404Resp resource.ReadResponse
	readFile404Resp.State = stateFile404
	r.Read(ctx, resource.ReadRequest{State: stateFile404}, &readFile404Resp)
	if readFile404Resp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on file 404 read: %v", readFile404Resp.Diagnostics)
	}
	var resMod404 PaperclipSkillResourceModel
	_ = readFile404Resp.State.Get(ctx, &resMod404)
	if !resMod404.Files.IsNull() {
		t.Fatalf("expected Files to be null after all files 404, got %v", resMod404.Files)
	}

	// Read: skill with FileInventory auxiliary files leaves state.Files null when not tracked in state
	stateInv := tfsdk.State{Schema: schema}
	_ = stateInv.Set(ctx, &PaperclipSkillResourceModel{
		CompanyID:     types.StringValue("comp-1"),
		ID:            types.StringValue("sk-inv"),
		Name:          types.StringValue("S"),
		Slug:          types.StringValue("s"),
		Categories:    types.ListNull(types.StringType),
		FileInventory: types.ListNull(fileInventoryObjectType),
		Files:         types.MapNull(types.StringType),
	})
	var readInvResp resource.ReadResponse
	readInvResp.State = stateInv
	r.Read(ctx, resource.ReadRequest{State: stateInv}, &readInvResp)
	if readInvResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error on inventory read: %v", readInvResp.Diagnostics)
	}
	var resModInv PaperclipSkillResourceModel
	_ = readInvResp.State.Get(ctx, &resModInv)
	if !resModInv.Files.IsNull() {
		t.Fatalf("expected Files to remain null when not tracked in state, got %v", resModInv.Files)
	}
	if resModInv.FileInventory.IsNull() {
		t.Fatal("expected FileInventory to be populated")
	}

	// Update: GetSkill error
	planUpdGetErr := tfsdk.Plan{Schema: schema}
	_ = planUpdGetErr.Set(ctx, &PaperclipSkillResourceModel{
		ID:            types.StringValue("sk-upd-get-err"),
		CompanyID:     types.StringValue("comp-upd-get-err"),
		Name:          types.StringValue("Upd"),
		Slug:          types.StringValue("upd"),
		Categories:    types.ListNull(types.StringType),
		FileInventory: types.ListNull(fileInventoryObjectType),
		Files:         types.MapNull(types.StringType),
	})
	stateUpdGetErr := tfsdk.State{Schema: schema}
	_ = stateUpdGetErr.Set(ctx, &PaperclipSkillResourceModel{
		ID:            types.StringValue("sk-upd-get-err"),
		CompanyID:     types.StringValue("comp-upd-get-err"),
		Name:          types.StringValue("Upd"),
		Slug:          types.StringValue("upd"),
		Categories:    types.ListNull(types.StringType),
		FileInventory: types.ListNull(fileInventoryObjectType),
		Files:         types.MapNull(types.StringType),
	})
	var updGetErrResp resource.UpdateResponse
	updGetErrResp.State = stateUpdGetErr
	r.Update(ctx, resource.UpdateRequest{Plan: planUpdGetErr, State: stateUpdGetErr}, &updGetErrResp)
	if !updGetErrResp.Diagnostics.HasError() {
		t.Fatal("expected error on GetSkill after update")
	}
	if updGetErrResp.Diagnostics.Errors()[0].Summary() != "Error fetching updated Paperclip skill" {
		t.Fatalf("unexpected summary: %s", updGetErrResp.Diagnostics.Errors()[0].Summary())
	}

	// Update: unmanaged files in state are not deleted when plan.Files is null
	unmanagedStateFiles, _ := types.MapValueFrom(ctx, types.StringType, map[string]string{"helper.sh": "echo 1"})
	planUnm := tfsdk.Plan{Schema: schema}
	_ = planUnm.Set(ctx, &PaperclipSkillResourceModel{
		ID:            types.StringValue("sk-unmanaged"),
		CompanyID:     types.StringValue("comp-unmanaged-upd"),
		Name:          types.StringValue("Unm"),
		Slug:          types.StringValue("unm"),
		Categories:    types.ListNull(types.StringType),
		FileInventory: types.ListNull(fileInventoryObjectType),
		Files:         types.MapNull(types.StringType),
	})
	stateUnm := tfsdk.State{Schema: schema}
	_ = stateUnm.Set(ctx, &PaperclipSkillResourceModel{
		ID:            types.StringValue("sk-unmanaged"),
		CompanyID:     types.StringValue("comp-unmanaged-upd"),
		Name:          types.StringValue("Unm"),
		Slug:          types.StringValue("unm"),
		Categories:    types.ListNull(types.StringType),
		FileInventory: types.ListNull(fileInventoryObjectType),
		Files:         unmanagedStateFiles,
	})
	var updUnmResp resource.UpdateResponse
	updUnmResp.State = stateUnm
	r.Update(ctx, resource.UpdateRequest{Plan: planUnm, State: stateUnm}, &updUnmResp)
	if updUnmResp.Diagnostics.HasError() {
		t.Fatalf("unexpected error updating unmanaged skill: %v", updUnmResp.Diagnostics)
	}
	var resModUnm PaperclipSkillResourceModel
	_ = updUnmResp.State.Get(ctx, &resModUnm)
	if !resModUnm.Files.IsNull() {
		t.Fatalf("expected Files to remain null in state after unmanaged update, got %v", resModUnm.Files)
	}
}
