package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestPaperclipSkillResourceFullLifecycle(t *testing.T) {
	ctx := context.Background()

	var deleteCalled, fileDeleted bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/comp-1/skills":
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":            "skill-1",
				"companyId":     "comp-1",
				"name":          body["name"],
				"slug":          body["slug"],
				"description":   body["description"],
				"content":       body["content"],
				"schemaVersion": 1,
				"sharingScope":  "company",
				"fileInventory": []map[string]interface{}{
					{"path": "SKILL.md", "kind": "skill_md"},
					{"path": "helper.sh", "kind": "script"},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/comp-1/skills/import":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"imported": []map[string]interface{}{
					{
						"id":        "skill-imp",
						"companyId": "comp-1",
						"name":      "imported-name",
						"slug":      "imported-slug",
						"markdown":  "# Imported",
					},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/comp-1/skills/install-catalog":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"skill": map[string]interface{}{
					"id":        "skill-cat",
					"companyId": "comp-1",
					"name":      "catalog-name",
					"slug":      "catalog-slug",
					"markdown":  "# Catalog",
				},
			})
		case r.Method == http.MethodPost && (r.URL.Path == "/api/companies/comp-1/skills/skill-1/rename" ||
			r.URL.Path == "/api/companies/comp-1/skills/skill-imp/rename" ||
			r.URL.Path == "/api/companies/comp-1/skills/skill-cat/rename"):
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"skill": map[string]interface{}{
					"id":        "skill-1",
					"companyId": "comp-1",
					"name":      body["name"],
					"slug":      body["slug"],
				},
			})
		case r.Method == http.MethodPatch && r.URL.Path == "/api/companies/comp-1/skills/skill-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":          "skill-1",
				"companyId":   "comp-1",
				"name":        "renamed-skill",
				"slug":        "renamed-slug",
				"description": "updated desc",
				"content":     "# Updated Content",
			})
		case r.Method == http.MethodPatch && (r.URL.Path == "/api/companies/comp-1/skills/skill-1/files" ||
			r.URL.Path == "/api/companies/comp-1/skills/skill-imp/files" ||
			r.URL.Path == "/api/companies/comp-1/skills/skill-cat/files"):
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"skillId": "skill-1",
				"path":    body["path"],
				"content": body["content"],
			})
		case r.Method == http.MethodDelete && r.URL.Path == "/api/companies/comp-1/skills/skill-1/files":
			fileDeleted = true
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/skills/skill-1/files":
			p := r.URL.Query().Get("path")
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"skillId": "skill-1",
				"path":    p,
				"content": "fresh-" + p,
			})
		case r.Method == http.MethodPost && (r.URL.Path == "/api/companies/comp-1/skills/skill-1/versions" ||
			r.URL.Path == "/api/companies/comp-1/skills/skill-imp/versions" ||
			r.URL.Path == "/api/companies/comp-1/skills/skill-cat/versions"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":             "ver-1",
				"companyId":      "comp-1",
				"companySkillId": "skill-1",
				"label":          "v1.0.0",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/skills/skill-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":               "skill-1",
				"companyId":        "comp-1",
				"name":             "renamed-skill",
				"slug":             "renamed-slug",
				"description":      "updated desc",
				"content":          "# Updated Content",
				"schemaVersion":    1,
				"sharingScope":     "company",
				"currentVersionId": "ver-1",
				"fileInventory": []map[string]interface{}{
					{"path": "SKILL.md", "kind": "skill_md"},
					{"path": "new.sh", "kind": "script"},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/skills/skill-imp":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":        "skill-imp",
				"companyId": "comp-1",
				"name":      "imp-skill",
				"slug":      "imp-slug",
				"content":   "# Imported",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/skills/skill-cat":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":        "skill-cat",
				"companyId": "comp-1",
				"name":      "cat-skill",
				"slug":      "cat-slug",
				"content":   "# Catalog",
			})
		case r.Method == http.MethodDelete && r.URL.Path == "/api/companies/comp-1/skills/skill-1":
			deleteCalled = true
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	r := NewPaperclipSkillResource().(*PaperclipSkillResource)

	var meta resource.MetadataResponse
	r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "paperclip"}, &meta)
	if meta.TypeName != "paperclip_skill" {
		t.Fatalf("unexpected type name: %s", meta.TypeName)
	}

	var confResp resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: nil}, &confResp)
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: "invalid"}, &confResp)
	if !confResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid configure")
	}

	resSchema := configureTestResource(t, server.URL, r)

	// UpgradeState test
	upgraders := r.UpgradeState(ctx)
	up0, ok := upgraders[0]
	if !ok {
		t.Fatal("expected upgrader for version 0")
	}
	priorState := tfsdk.State{Schema: *up0.PriorSchema}
	type PriorV0Model struct {
		ID            types.String `tfsdk:"id"`
		CompanyID     types.String `tfsdk:"company_id"`
		Name          types.String `tfsdk:"name"`
		Slug          types.String `tfsdk:"slug"`
		Description   types.String `tfsdk:"description"`
		Content       types.String `tfsdk:"content"`
		SchemaVersion types.Int64  `tfsdk:"schema_version"`
		Metadata      types.String `tfsdk:"metadata"`
	}
	priorModel := PriorV0Model{
		ID:            types.StringValue("old-id"),
		CompanyID:     types.StringValue("comp-1"),
		Name:          types.StringValue("old-name"),
		Slug:          types.StringValue("old-slug"),
		Description:   types.StringValue("old-desc"),
		Content:       types.StringValue("old-content"),
		SchemaVersion: types.Int64Value(1),
		Metadata:      types.StringValue("{}"),
	}
	_ = priorState.Set(ctx, &priorModel)
	var upResp resource.UpgradeStateResponse
	upResp.State = tfsdk.State{Schema: resSchema}
	up0.StateUpgrader(ctx, resource.UpgradeStateRequest{State: &priorState}, &upResp)
	if upResp.Diagnostics.HasError() {
		t.Fatalf("upgrade failed: %v", upResp.Diagnostics)
	}
	var upgradedModel PaperclipSkillResourceModel
	_ = upResp.State.Get(ctx, &upgradedModel)
	if upgradedModel.SharingScope.ValueString() != "company" {
		t.Fatalf("expected upgraded sharing scope 'company', got: %s", upgradedModel.SharingScope.ValueString())
	}

	// 1. Create standard skill with files map and version snapshot
	filesMap := map[string]string{
		"helper.sh": "echo hello",
	}
	catsList := []string{"coding", "review"}
	filesMapVal, _ := types.MapValueFrom(ctx, types.StringType, filesMap)
	catsListVal, _ := types.ListValueFrom(ctx, types.StringType, catsList)
	planModel := PaperclipSkillResourceModel{
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("Skill 1"),
		Slug:            types.StringValue("skill-1"),
		Description:     types.StringValue("Initial desc"),
		Content:         types.StringValue("# Initial content"),
		Categories:      catsListVal,
		Files:           filesMapVal,
		FileInventory:   types.ListNull(fileInventoryObjectType),
		VersionSnapshot: types.StringValue("v1.0.0"),
	}
	plan := tfsdk.Plan{Schema: resSchema}
	_ = plan.Set(ctx, &planModel)
	createResp := resource.CreateResponse{State: tfsdk.State{Schema: resSchema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("create failed: %v", createResp.Diagnostics)
	}

	var stateModel PaperclipSkillResourceModel
	_ = createResp.State.Get(ctx, &stateModel)
	if stateModel.ID.ValueString() != "skill-1" {
		t.Fatalf("expected ID skill-1, got %s", stateModel.ID.ValueString())
	}

	// 2. Read
	readResp := resource.ReadResponse{State: createResp.State}
	r.Read(ctx, resource.ReadRequest{State: createResp.State}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("read failed: %v", readResp.Diagnostics)
	}

	// 3. Update with rename, file replacement, and new version
	newFilesMap := map[string]string{
		"new.sh": "echo new",
	}
	newFilesMapVal, _ := types.MapValueFrom(ctx, types.StringType, newFilesMap)
	planModel.ID = types.StringValue("skill-1")
	planModel.Name = types.StringValue("renamed-skill")
	planModel.Slug = types.StringValue("renamed-slug")
	planModel.Files = newFilesMapVal
	planModel.VersionSnapshot = types.StringValue("v1.1.0")
	_ = plan.Set(ctx, &planModel)

	updateResp := resource.UpdateResponse{State: createResp.State}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: createResp.State}, &updateResp)
	if updateResp.Diagnostics.HasError() {
		t.Fatalf("update failed: %v", updateResp.Diagnostics)
	}
	if !fileDeleted {
		t.Fatal("expected old auxiliary file to be deleted during update")
	}

	// 4. Delete
	deleteResp := resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: updateResp.State}, &deleteResp)
	if deleteResp.Diagnostics.HasError() {
		t.Fatalf("delete failed: %v", deleteResp.Diagnostics)
	}
	if !deleteCalled {
		t.Fatal("expected delete to be called")
	}

	// 5. ImportState
	impResp := resource.ImportStateResponse{State: tfsdk.State{Schema: resSchema}}
	_ = impResp.State.Set(ctx, &PaperclipSkillResourceModel{
		Categories:    types.ListNull(types.StringType),
		Files:         types.MapNull(types.StringType),
		FileInventory: types.ListNull(fileInventoryObjectType),
	})
	r.ImportState(ctx, resource.ImportStateRequest{ID: "invalid"}, &impResp)
	if !impResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid import ID")
	}
	impRespGood := resource.ImportStateResponse{State: tfsdk.State{Schema: resSchema}}
	_ = impRespGood.State.Set(ctx, &PaperclipSkillResourceModel{
		Categories:    types.ListNull(types.StringType),
		Files:         types.MapNull(types.StringType),
		FileInventory: types.ListNull(fileInventoryObjectType),
	})
	r.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/skill-1"}, &impRespGood)
	if impRespGood.Diagnostics.HasError() {
		t.Fatalf("import failed: %v", impRespGood.Diagnostics)
	}

	// 6. Create with ImportSource and CatalogSkillID
	impPlanModel := PaperclipSkillResourceModel{
		CompanyID:     types.StringValue("comp-1"),
		Name:          types.StringValue("imp-skill"),
		Slug:          types.StringValue("imp-slug"),
		ImportSource:  types.StringValue("https://example.com/skill"),
		Categories:    types.ListNull(types.StringType),
		Files:         types.MapNull(types.StringType),
		FileInventory: types.ListNull(fileInventoryObjectType),
	}
	_ = plan.Set(ctx, &impPlanModel)
	createRespImp := resource.CreateResponse{State: tfsdk.State{Schema: resSchema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &createRespImp)
	if createRespImp.Diagnostics.HasError() {
		t.Fatalf("create with import_source failed: %v", createRespImp.Diagnostics)
	}

	catPlanModel := PaperclipSkillResourceModel{
		CompanyID:      types.StringValue("comp-1"),
		Name:           types.StringValue("cat-skill"),
		Slug:           types.StringValue("cat-slug"),
		CatalogSkillID: types.StringValue("catalog-1"),
		Categories:     types.ListNull(types.StringType),
		Files:          types.MapNull(types.StringType),
		FileInventory:  types.ListNull(fileInventoryObjectType),
	}
	_ = plan.Set(ctx, &catPlanModel)
	createRespCat := resource.CreateResponse{State: tfsdk.State{Schema: resSchema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &createRespCat)
	if createRespCat.Diagnostics.HasError() {
		t.Fatalf("create with catalog_skill_id failed: %v", createRespCat.Diagnostics)
	}
}

func TestPaperclipSkillResourceRetirementOrdering422(t *testing.T) {
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodDelete && r.URL.Path == "/api/companies/comp-1/skills/skill-attached" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"error": "Cannot delete skill \"Reviewer\" while it is still used by Agent1. Detach it from those agents first.",
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	r := NewPaperclipSkillResource().(*PaperclipSkillResource)
	resSchema := configureTestResource(t, server.URL, r)

	state := tfsdk.State{Schema: resSchema}
	_ = state.Set(ctx, &PaperclipSkillResourceModel{
		ID:            types.StringValue("skill-attached"),
		CompanyID:     types.StringValue("comp-1"),
		Name:          types.StringValue("Reviewer"),
		Slug:          types.StringValue("reviewer"),
		Content:       types.StringValue("content"),
		Categories:    types.ListNull(types.StringType),
		Files:         types.MapNull(types.StringType),
		FileInventory: types.ListNull(fileInventoryObjectType),
	})

	var delResp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: state}, &delResp)
	if !delResp.Diagnostics.HasError() {
		t.Fatal("expected error on 422 retirement ordering failure")
	}
}

func TestPaperclipSkillAssignmentResourceLifecycle(t *testing.T) {
	ctx := context.Background()

	var deleteCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/agents/agent-1/skills":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"adapterType": "antigravity_local",
				"supported":   true,
				"mode":        "replace",
				"desiredSkillEntries": []map[string]interface{}{
					{"key": "skill-1", "versionId": "ver-1"},
					{"key": "skill-2"},
				},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/agents/agent-1/skills/sync":
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			desired, _ := body["desiredSkills"].([]interface{})
			if len(desired) == 0 {
				deleteCalled = true
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"adapterType":         "antigravity_local",
				"supported":           true,
				"mode":                body["mode"],
				"desiredSkillEntries": desired,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	r := NewPaperclipSkillAssignmentResource().(*PaperclipSkillAssignmentResource)

	var meta resource.MetadataResponse
	r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "paperclip"}, &meta)
	if meta.TypeName != "paperclip_skill_assignment" {
		t.Fatalf("unexpected type name: %s", meta.TypeName)
	}

	var confResp resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: nil}, &confResp)
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: "invalid"}, &confResp)
	if !confResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid configure")
	}

	resSchema := configureTestResource(t, server.URL, r)

	// Create
	planModel := PaperclipSkillAssignmentResourceModel{
		AgentID: types.StringValue("agent-1"),
		Skills: []PaperclipDesiredSkillModel{
			{Key: types.StringValue("skill-1"), VersionID: types.StringValue("ver-1")},
			{Key: types.StringValue("skill-2")},
		},
	}
	plan := tfsdk.Plan{Schema: resSchema}
	_ = plan.Set(ctx, &planModel)

	var createResp resource.CreateResponse
	createResp.State = tfsdk.State{Schema: resSchema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("create failed: %v", createResp.Diagnostics)
	}

	var state PaperclipSkillAssignmentResourceModel
	_ = createResp.State.Get(ctx, &state)
	if state.ID.ValueString() != "agent-1" || len(state.Skills) != 2 {
		t.Fatalf("unexpected state after create: %+v", state)
	}

	// Read
	var readResp resource.ReadResponse
	readResp.State = createResp.State
	r.Read(ctx, resource.ReadRequest{State: createResp.State}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("read failed: %v", readResp.Diagnostics)
	}

	// Update
	planModel.Skills = []PaperclipDesiredSkillModel{
		{Key: types.StringValue("skill-1")},
	}
	_ = plan.Set(ctx, &planModel)
	var updateResp resource.UpdateResponse
	updateResp.State = createResp.State
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: createResp.State}, &updateResp)
	if updateResp.Diagnostics.HasError() {
		t.Fatalf("update failed: %v", updateResp.Diagnostics)
	}

	// Delete
	var delResp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: updateResp.State}, &delResp)
	if delResp.Diagnostics.HasError() {
		t.Fatalf("delete failed: %v", delResp.Diagnostics)
	}
	if !deleteCalled {
		t.Fatal("expected sync with empty desired skills on delete")
	}

	// ImportState
	var impResp resource.ImportStateResponse
	impResp.State = tfsdk.State{Schema: resSchema}
	_ = impResp.State.Set(ctx, &PaperclipSkillAssignmentResourceModel{})
	r.ImportState(ctx, resource.ImportStateRequest{ID: ""}, &impResp)
	if !impResp.Diagnostics.HasError() {
		t.Fatal("expected error on empty import ID")
	}

	var impRespGood resource.ImportStateResponse
	impRespGood.State = tfsdk.State{Schema: resSchema}
	_ = impRespGood.State.Set(ctx, &PaperclipSkillAssignmentResourceModel{})
	r.ImportState(ctx, resource.ImportStateRequest{ID: "agent-1"}, &impRespGood)
	if impRespGood.Diagnostics.HasError() {
		t.Fatalf("import failed: %v", impRespGood.Diagnostics)
	}
}

func TestPaperclipSkillPolicyResourceLifecycle(t *testing.T) {
	ctx := context.Background()

	var resetCalled bool
	currentRevision := int64(1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/skill-policy":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"schemaVersion": 1,
				"revision":      currentRevision,
				"defaultEffect": "deny",
				"rules": []map[string]interface{}{
					{"name": "rule-1", "action": "skills.use", "effect": "allow"},
				},
				"materialized": true,
			})
		case r.Method == http.MethodPut && r.URL.Path == "/api/companies/comp-1/skill-policy":
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			expectedRev := int64(body["expectedRevision"].(float64))
			if expectedRev != currentRevision {
				w.WriteHeader(http.StatusConflict)
				_ = json.NewEncoder(w).Encode(map[string]interface{}{
					"code":             "skill_policy_revision_conflict",
					"error":            "Skill policy revision is stale",
					"expectedRevision": expectedRev,
					"currentRevision":  currentRevision,
				})
				return
			}
			currentRevision++
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"schemaVersion": 1,
				"revision":      currentRevision,
				"defaultEffect": body["defaultEffect"],
				"rules":         body["rules"],
				"materialized":  true,
			})
		case r.Method == http.MethodDelete && r.URL.Path == "/api/companies/comp-1/skill-policy":
			resetCalled = true
			currentRevision = 0
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"schemaVersion": 1,
				"revision":      0,
				"defaultEffect": "allow",
				"rules":         []map[string]interface{}{},
				"materialized":  false,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	r := NewPaperclipSkillPolicyResource().(*PaperclipSkillPolicyResource)

	var meta resource.MetadataResponse
	r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "paperclip"}, &meta)
	if meta.TypeName != "paperclip_skill_policy" {
		t.Fatalf("unexpected type name: %s", meta.TypeName)
	}

	var confResp resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: nil}, &confResp)
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: "invalid"}, &confResp)
	if !confResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid configure")
	}

	resSchema := configureTestResource(t, server.URL, r)

	// Create
	planModel := PaperclipSkillPolicyResourceModel{
		CompanyID:     types.StringValue("comp-1"),
		DefaultEffect: types.StringValue("deny"),
		Rules:         types.StringValue(`[{"name":"rule-1","action":"skills.use","effect":"allow"}]`),
	}
	plan := tfsdk.Plan{Schema: resSchema}
	_ = plan.Set(ctx, &planModel)

	var createResp resource.CreateResponse
	createResp.State = tfsdk.State{Schema: resSchema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("create failed: %v", createResp.Diagnostics)
	}

	var state PaperclipSkillPolicyResourceModel
	_ = createResp.State.Get(ctx, &state)
	if state.Revision.ValueInt64() != 2 {
		t.Fatalf("expected revision 2 after create, got %d", state.Revision.ValueInt64())
	}

	// Read
	var readResp resource.ReadResponse
	readResp.State = createResp.State
	r.Read(ctx, resource.ReadRequest{State: createResp.State}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("read failed: %v", readResp.Diagnostics)
	}

	// Update (successful)
	planModel.Revision = state.Revision
	planModel.DefaultEffect = types.StringValue("allow")
	planModel.Rules = types.StringValue(`[]`)
	_ = plan.Set(ctx, &planModel)

	var updateResp resource.UpdateResponse
	updateResp.State = createResp.State
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: createResp.State}, &updateResp)
	if updateResp.Diagnostics.HasError() {
		t.Fatalf("update failed: %v", updateResp.Diagnostics)
	}

	// Update (conflict)
	state.Revision = types.Int64Value(999) // Stale revision
	updateRespConflict := resource.UpdateResponse{State: updateResp.State}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: updateResp.State}, &updateRespConflict)
	// Even with state having valid revision, if server has different revision conflict is triggered:
	staleState := tfsdk.State{Schema: resSchema}
	_ = staleState.Set(ctx, &PaperclipSkillPolicyResourceModel{
		ID:            types.StringValue("comp-1"),
		CompanyID:     types.StringValue("comp-1"),
		DefaultEffect: types.StringValue("deny"),
		Revision:      types.Int64Value(999),
	})
	var updateStaleResp resource.UpdateResponse
	updateStaleResp.State = staleState
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: staleState}, &updateStaleResp)
	if !updateStaleResp.Diagnostics.HasError() {
		t.Fatal("expected conflict error when updating with stale revision")
	}

	// Delete
	var delResp resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: updateResp.State}, &delResp)
	if delResp.Diagnostics.HasError() {
		t.Fatalf("delete failed: %v", delResp.Diagnostics)
	}
	if !resetCalled {
		t.Fatal("expected reset to be called on delete")
	}

	// ImportState
	var impResp resource.ImportStateResponse
	impResp.State = tfsdk.State{Schema: resSchema}
	_ = impResp.State.Set(ctx, &PaperclipSkillPolicyResourceModel{})
	r.ImportState(ctx, resource.ImportStateRequest{ID: ""}, &impResp)
	if !impResp.Diagnostics.HasError() {
		t.Fatal("expected error on empty import ID")
	}

	var impRespGood resource.ImportStateResponse
	impRespGood.State = tfsdk.State{Schema: resSchema}
	_ = impRespGood.State.Set(ctx, &PaperclipSkillPolicyResourceModel{})
	r.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1"}, &impRespGood)
	if impRespGood.Diagnostics.HasError() {
		t.Fatalf("import failed: %v", impRespGood.Diagnostics)
	}
}

func TestPaperclipSkillDataSourceFull(t *testing.T) {
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/skills/skill-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":               "skill-1",
				"companyId":        "comp-1",
				"name":             "Full Skill",
				"slug":             "full-skill",
				"description":      "full skill desc",
				"content":          "# Full Content",
				"schemaVersion":    1,
				"iconUrl":          "https://example.com/icon.png",
				"color":            "#ff0000",
				"tagline":          "Best skill",
				"authorName":       "Author",
				"homepageUrl":      "https://example.com",
				"categories":       []string{"cat1"},
				"sharingScope":     "company",
				"currentVersionId": "ver-1",
				"sourceType":       "catalog",
				"sourceLocator":    "loc-1",
				"folderId":         "folder-1",
				"forkedFromSkillId": "fork-1",
				"metadata":         map[string]interface{}{"k": "v"},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/skills":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{
					"id":        "skill-1",
					"companyId": "comp-1",
					"name":      "Full Skill",
					"slug":      "full-skill",
				},
				{
					"id":        "skill-2",
					"companyId": "comp-1",
					"name":      "Dupe Skill",
					"slug":      "dupe-skill",
				},
				{
					"id":        "skill-3",
					"companyId": "comp-1",
					"name":      "Dupe Skill",
					"slug":      "dupe-skill",
				},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	d := NewPaperclipSkillDataSource().(*PaperclipSkillDataSource)
	var meta datasource.MetadataResponse
	d.Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: "paperclip"}, &meta)
	if meta.TypeName != "paperclip_skill" {
		t.Fatalf("unexpected type name: %s", meta.TypeName)
	}

	dsSchema := configureTestDataSource(t, server.URL, d)

	// Read by ID
	cfg := PaperclipSkillDataSourceModel{
		CompanyID: types.StringValue("comp-1"),
		ID:        types.StringValue("skill-1"),
	}
	planVal := tfsdk.Plan{Schema: dsSchema}
	_ = planVal.Set(ctx, &cfg)
	var readResp datasource.ReadResponse
	readResp.State = tfsdk.State{Schema: dsSchema}
	d.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: dsSchema, Raw: planVal.Raw}}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("read by ID failed: %v", readResp.Diagnostics)
	}

	var state PaperclipSkillDataSourceModel
	_ = readResp.State.Get(ctx, &state)
	if state.Name.ValueString() != "Full Skill" || state.IconURL.ValueString() != "https://example.com/icon.png" ||
		state.Categories == nil || len(*state.Categories) != 1 || (*state.Categories)[0] != "cat1" {
		t.Fatalf("unexpected read state: %+v", state)
	}

	// Read by slug
	cfgSlug := PaperclipSkillDataSourceModel{
		CompanyID: types.StringValue("comp-1"),
		Slug:      types.StringValue("full-skill"),
	}
	_ = planVal.Set(ctx, &cfgSlug)
	var readRespSlug datasource.ReadResponse
	readRespSlug.State = tfsdk.State{Schema: dsSchema}
	d.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: dsSchema, Raw: planVal.Raw}}, &readRespSlug)
	if readRespSlug.Diagnostics.HasError() {
		t.Fatalf("read by slug failed: %v", readRespSlug.Diagnostics)
	}

	// Ambiguous match error
	cfgDupe := PaperclipSkillDataSourceModel{
		CompanyID: types.StringValue("comp-1"),
		Name:      types.StringValue("Dupe Skill"),
	}
	_ = planVal.Set(ctx, &cfgDupe)
	var readRespDupe datasource.ReadResponse
	readRespDupe.State = tfsdk.State{Schema: dsSchema}
	d.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: dsSchema, Raw: planVal.Raw}}, &readRespDupe)
	if !readRespDupe.Diagnostics.HasError() {
		t.Fatal("expected error on ambiguous match")
	}

	// Not found error
	cfgMissing := PaperclipSkillDataSourceModel{
		CompanyID: types.StringValue("comp-1"),
		Slug:      types.StringValue("nonexistent"),
	}
	_ = planVal.Set(ctx, &cfgMissing)
	var readRespMissing datasource.ReadResponse
	readRespMissing.State = tfsdk.State{Schema: dsSchema}
	d.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: dsSchema, Raw: planVal.Raw}}, &readRespMissing)
	if !readRespMissing.Diagnostics.HasError() {
		t.Fatal("expected error on nonexistent skill")
	}

	// Invalid arguments error (no ID, slug, or name)
	cfgNoArgs := PaperclipSkillDataSourceModel{
		CompanyID: types.StringValue("comp-1"),
	}
	_ = planVal.Set(ctx, &cfgNoArgs)
	var readRespNoArgs datasource.ReadResponse
	readRespNoArgs.State = tfsdk.State{Schema: dsSchema}
	d.Read(ctx, datasource.ReadRequest{Config: tfsdk.Config{Schema: dsSchema, Raw: planVal.Raw}}, &readRespNoArgs)
	if !readRespNoArgs.Diagnostics.HasError() {
		t.Fatal("expected error with no ID, slug, or name")
	}
}
