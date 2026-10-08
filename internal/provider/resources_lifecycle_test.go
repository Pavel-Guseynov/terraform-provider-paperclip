package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.guseynov.org/guseynov/paperclip/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	dsschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func configureTestDataSource(t *testing.T, endpoint string, d datasource.DataSourceWithConfigure) dsschema.Schema {
	t.Helper()
	var schemaResp datasource.SchemaResponse
	d.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResp)
	cli, err := client.NewClient(endpoint, "token")
	if err != nil {
		t.Fatal(err)
	}
	var confResp datasource.ConfigureResponse
	d.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: cli}, &confResp)
	if confResp.Diagnostics.HasError() {
		t.Fatalf("datasource configure failed: %v", confResp.Diagnostics)
	}
	return schemaResp.Schema
}

func TestPaperclipCompanyResourceLifecycle(t *testing.T) {
	ctx := context.Background()
	fail := false
	var archiveCalled bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if fail {
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/api/companies":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "comp-1", "name": "Acme", "description": "Acme Corp", "requireTaskStartConfirmation": false,
			})
		case req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "comp-1", "name": "Acme", "description": "Acme Corp", "requireTaskStartConfirmation": false,
			})
		case req.Method == http.MethodPatch && req.URL.Path == "/api/companies/comp-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "comp-1", "name": "Acme Updated", "description": "New Desc", "requireTaskStartConfirmation": true,
			})
		case req.Method == http.MethodPost && req.URL.Path == "/api/companies/comp-1/archive":
			archiveCalled = true
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	r := NewPaperclipCompanyResource().(*PaperclipCompanyResource)
	// Metadata
	var meta resource.MetadataResponse
	r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "paperclip"}, &meta)
	if meta.TypeName != "paperclip_company" {
		t.Fatalf("unexpected type name: %s", meta.TypeName)
	}

	// Configure edge cases
	var confResp resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: nil}, &confResp)
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: "invalid"}, &confResp)
	if !confResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid configure")
	}

	schema := configureTestResource(t, server.URL, r)

	// Create
	model := PaperclipCompanyResourceModel{
		Name:                         types.StringValue("Acme"),
		Description:                  types.StringValue("Acme Corp"),
		RequireTaskStartConfirmation: types.BoolValue(false),
	}
	plan := tfsdk.Plan{Schema: schema}
	if diags := plan.Set(ctx, &model); diags.HasError() {
		t.Fatal(diags)
	}
	created := resource.CreateResponse{State: tfsdk.State{Schema: schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
	if created.Diagnostics.HasError() {
		t.Fatalf("create failed: %v", created.Diagnostics)
	}

	// Read
	read := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatalf("read failed: %v", read.Diagnostics)
	}

	// Update
	model.Name = types.StringValue("Acme Updated")
	model.Description = types.StringValue("New Desc")
	model.RequireTaskStartConfirmation = types.BoolValue(true)
	model.ID = types.StringValue("comp-1")
	if diags := plan.Set(ctx, &model); diags.HasError() {
		t.Fatal(diags)
	}
	updated := resource.UpdateResponse{State: created.State}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: created.State}, &updated)
	if updated.Diagnostics.HasError() {
		t.Fatalf("update failed: %v", updated.Diagnostics)
	}

	// Delete
	r.Delete(ctx, resource.DeleteRequest{State: updated.State}, &resource.DeleteResponse{})
	if !archiveCalled {
		t.Fatal("expected company delete to call POST /archive, not DELETE")
	}

	// ImportState
	importResp := resource.ImportStateResponse{State: tfsdk.State{Schema: schema}}
	_ = importResp.State.Set(ctx, &PaperclipCompanyResourceModel{})
	r.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1"}, &importResp)
	if importResp.Diagnostics.HasError() {
		t.Fatalf("import failed: %v", importResp.Diagnostics)
	}

	// 404 read removes resource
	read404 := resource.ReadResponse{State: tfsdk.State{Schema: schema}}
	model404 := PaperclipCompanyResourceModel{ID: types.StringValue("comp-missing")}
	_ = read404.State.Set(ctx, &model404)
	r.Read(ctx, resource.ReadRequest{State: read404.State}, &read404)
	if !read404.State.Raw.IsNull() {
		t.Fatal("expected state to be removed on 404")
	}

	// Server failure error paths
	fail = true
	var errCreate resource.CreateResponse
	errCreate.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &errCreate)
	if !errCreate.Diagnostics.HasError() {
		t.Fatal("expected error on failed create")
	}

	var errRead resource.ReadResponse
	errRead.State = created.State
	r.Read(ctx, resource.ReadRequest{State: created.State}, &errRead)
	if !errRead.Diagnostics.HasError() {
		t.Fatal("expected error on failed read")
	}

	var errUpdate resource.UpdateResponse
	errUpdate.State = created.State
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: created.State}, &errUpdate)
	if !errUpdate.Diagnostics.HasError() {
		t.Fatal("expected error on failed update")
	}

	var errDelete resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: created.State}, &errDelete)
	if !errDelete.Diagnostics.HasError() {
		t.Fatal("expected error on failed delete")
	}
}

func TestPaperclipProjectResourceLifecycle(t *testing.T) {
	ctx := context.Background()
	fail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if fail {
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/api/companies/comp-1/projects":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "proj-1", "companyId": "comp-1", "name": "Project Alpha", "description": "Alpha Project",
			})
		case req.Method == http.MethodGet && req.URL.Path == "/api/projects/proj-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "proj-1", "companyId": "comp-1", "name": "Project Alpha", "description": "Alpha Project",
			})
		case req.Method == http.MethodPatch && req.URL.Path == "/api/projects/proj-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "proj-1", "companyId": "comp-1", "name": "Project Alpha Beta", "description": "Updated",
			})
		case req.Method == http.MethodDelete && req.URL.Path == "/api/projects/proj-1":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	r := NewPaperclipProjectResource().(*PaperclipProjectResource)
	var meta resource.MetadataResponse
	r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "paperclip"}, &meta)
	if meta.TypeName != "paperclip_project" {
		t.Fatalf("unexpected type name: %s", meta.TypeName)
	}

	var confResp resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: nil}, &confResp)
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: "invalid"}, &confResp)

	schema := configureTestResource(t, server.URL, r)

	model := PaperclipProjectResourceModel{
		CompanyID:   types.StringValue("comp-1"),
		Name:        types.StringValue("Project Alpha"),
		Description: types.StringValue("Alpha Project"),
	}
	plan := tfsdk.Plan{Schema: schema}
	_ = plan.Set(ctx, &model)
	created := resource.CreateResponse{State: tfsdk.State{Schema: schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
	if created.Diagnostics.HasError() {
		t.Fatalf("create failed: %v", created.Diagnostics)
	}

	read := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatalf("read failed: %v", read.Diagnostics)
	}

	model.Name = types.StringValue("Project Alpha Beta")
	model.Description = types.StringValue("Updated")
	model.ID = types.StringValue("proj-1")
	_ = plan.Set(ctx, &model)
	updated := resource.UpdateResponse{State: created.State}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: created.State}, &updated)
	if updated.Diagnostics.HasError() {
		t.Fatalf("update failed: %v", updated.Diagnostics)
	}

	r.Delete(ctx, resource.DeleteRequest{State: updated.State}, &resource.DeleteResponse{})

	// ImportState
	importResp := resource.ImportStateResponse{State: tfsdk.State{Schema: schema}}
	r.ImportState(ctx, resource.ImportStateRequest{ID: "proj-1"}, &importResp)
	r.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/proj-1"}, &importResp)

	// 404 read
	read404 := resource.ReadResponse{State: tfsdk.State{Schema: schema}}
	m404 := PaperclipProjectResourceModel{ID: types.StringValue("proj-missing")}
	_ = read404.State.Set(ctx, &m404)
	r.Read(ctx, resource.ReadRequest{State: read404.State}, &read404)
	if !read404.State.Raw.IsNull() {
		t.Fatal("expected state to be removed on 404")
	}

	// Server failure error paths
	fail = true
	var errResp resource.CreateResponse
	errResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &errResp)
	var errRead resource.ReadResponse
	errRead.State = created.State
	r.Read(ctx, resource.ReadRequest{State: created.State}, &errRead)
	var errUpdate resource.UpdateResponse
	errUpdate.State = created.State
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: created.State}, &errUpdate)
	var errDelete resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: created.State}, &errDelete)
}

func TestPaperclipSkillResourceLifecycle(t *testing.T) {
	ctx := context.Background()
	fail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if fail {
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/api/companies/comp-1/skills":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "skill-1", "companyId": "comp-1", "name": "Reviewer Skill", "content": "# Prompt",
			})
		case req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-1/skills/skill-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "skill-1", "companyId": "comp-1", "name": "Reviewer Skill", "content": "# Prompt",
			})
		case req.Method == http.MethodPatch && req.URL.Path == "/api/companies/comp-1/skills/skill-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "skill-1", "companyId": "comp-1", "name": "Reviewer Skill Updated", "content": "# Prompt 2",
			})
		case req.Method == http.MethodPatch && req.URL.Path == "/api/companies/comp-1/skills/skill-1/files":
			w.WriteHeader(http.StatusOK)
		case req.Method == http.MethodDelete && req.URL.Path == "/api/companies/comp-1/skills/skill-1":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	r := NewPaperclipSkillResource().(*PaperclipSkillResource)
	var meta resource.MetadataResponse
	r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "paperclip"}, &meta)

	var confResp resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: nil}, &confResp)
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: "invalid"}, &confResp)

	schema := configureTestResource(t, server.URL, r)

	model := PaperclipSkillResourceModel{
		CompanyID: types.StringValue("comp-1"),
		Name:      types.StringValue("Reviewer Skill"),
		Content:   types.StringValue("# Prompt"),
	}
	plan := tfsdk.Plan{Schema: schema}
	_ = plan.Set(ctx, &model)
	created := resource.CreateResponse{State: tfsdk.State{Schema: schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
	if created.Diagnostics.HasError() {
		t.Fatalf("create failed: %v", created.Diagnostics)
	}

	read := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatalf("read failed: %v", read.Diagnostics)
	}

	model.Name = types.StringValue("Reviewer Skill Updated")
	model.ID = types.StringValue("skill-1")
	_ = plan.Set(ctx, &model)
	updated := resource.UpdateResponse{State: created.State}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: created.State}, &updated)
	if updated.Diagnostics.HasError() {
		t.Fatalf("update failed: %v", updated.Diagnostics)
	}

	r.Delete(ctx, resource.DeleteRequest{State: updated.State}, &resource.DeleteResponse{})

	// ImportState
	importResp := resource.ImportStateResponse{State: tfsdk.State{Schema: schema}}
	_ = importResp.State.Set(ctx, &PaperclipSkillResourceModel{})
	r.ImportState(ctx, resource.ImportStateRequest{ID: "skill-1"}, &importResp)
	r.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/skill-1"}, &importResp)

	// 404 read
	read404 := resource.ReadResponse{State: tfsdk.State{Schema: schema}}
	m404 := PaperclipSkillResourceModel{ID: types.StringValue("skill-missing"), CompanyID: types.StringValue("comp-1")}
	_ = read404.State.Set(ctx, &m404)
	r.Read(ctx, resource.ReadRequest{State: read404.State}, &read404)
	if !read404.State.Raw.IsNull() {
		t.Fatal("expected state to be removed on 404")
	}

	// Server failure error paths
	fail = true
	var errResp resource.CreateResponse
	errResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &errResp)
	var errRead resource.ReadResponse
	errRead.State = created.State
	r.Read(ctx, resource.ReadRequest{State: created.State}, &errRead)
	var errUpdate resource.UpdateResponse
	errUpdate.State = created.State
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: created.State}, &errUpdate)
	var errDelete resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: created.State}, &errDelete)
}

func TestPaperclipSecretResourceLifecycle(t *testing.T) {
	ctx := context.Background()
	fail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if fail {
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/api/companies/comp-1/secrets":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "sec-1", "companyId": "comp-1", "name": "db-pass", "description": "DB Password",
			})
		case req.Method == http.MethodGet && (req.URL.Path == "/api/companies/comp-1/secrets/sec-1" || req.URL.Path == "/api/companies/comp-1/secrets"):
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{{
				"id": "sec-1", "companyId": "comp-1", "name": "db-pass", "description": "DB Password",
				"versions": []interface{}{map[string]interface{}{"version": 1}},
			}})
		case req.Method == http.MethodPatch && req.URL.Path == "/api/secrets/sec-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "sec-1", "name": "db-pass", "description": "Updated DB Password",
			})
		case req.Method == http.MethodDelete && req.URL.Path == "/api/secrets/sec-1":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	r := NewPaperclipSecretResource().(*PaperclipSecretResource)
	var meta resource.MetadataResponse
	r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "paperclip"}, &meta)

	var confResp resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: nil}, &confResp)
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: "invalid"}, &confResp)

	schema := configureTestResource(t, server.URL, r)

	// ValidateConfig
	model := PaperclipSecretResourceModel{
		CompanyID:   types.StringValue("comp-1"),
		Name:        types.StringValue("db-pass"),
		Description: types.StringValue("DB Password"),
		Value:       types.StringValue("super-secret"),
		Rotation:    types.StringValue("v1"),
	}
	plan := tfsdk.Plan{Schema: schema}
	_ = plan.Set(ctx, &model)
	var valResp resource.ValidateConfigResponse
	r.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: tfsdk.Config{Schema: schema, Raw: plan.Raw}}, &valResp)
	if valResp.Diagnostics.HasError() {
		t.Fatalf("ValidateConfig failed: %v", valResp.Diagnostics)
	}

	cfg := tfsdk.Config{Schema: schema, Raw: plan.Raw}
	created := resource.CreateResponse{State: tfsdk.State{Schema: schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan, Config: cfg}, &created)
	if created.Diagnostics.HasError() {
		t.Fatalf("create failed: %v", created.Diagnostics)
	}

	read := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatalf("read failed: %v", read.Diagnostics)
	}

	model.Description = types.StringValue("Updated DB Password")
	model.ID = types.StringValue("sec-1")
	_ = plan.Set(ctx, &model)
	cfg = tfsdk.Config{Schema: schema, Raw: plan.Raw}
	updated := resource.UpdateResponse{State: created.State}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, Config: cfg, State: created.State}, &updated)
	if updated.Diagnostics.HasError() {
		t.Fatalf("update failed: %v", updated.Diagnostics)
	}

	r.Delete(ctx, resource.DeleteRequest{State: updated.State}, &resource.DeleteResponse{})

	// ImportState
	importResp := resource.ImportStateResponse{State: tfsdk.State{Schema: schema}}
	_ = importResp.State.Set(ctx, &PaperclipSecretResourceModel{})
	r.ImportState(ctx, resource.ImportStateRequest{ID: "sec-1"}, &importResp)
	r.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/sec-1"}, &importResp)

	// 404 read
	read404 := resource.ReadResponse{State: tfsdk.State{Schema: schema}}
	m404 := PaperclipSecretResourceModel{ID: types.StringValue("sec-missing"), CompanyID: types.StringValue("comp-1")}
	_ = read404.State.Set(ctx, &m404)
	r.Read(ctx, resource.ReadRequest{State: read404.State}, &read404)
	if !read404.State.Raw.IsNull() {
		t.Fatal("expected state to be removed on 404")
	}

	// Server failure error paths
	fail = true
	var errResp resource.CreateResponse
	errResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan, Config: cfg}, &errResp)
	var errRead resource.ReadResponse
	errRead.State = created.State
	r.Read(ctx, resource.ReadRequest{State: created.State}, &errRead)
	var errUpdate resource.UpdateResponse
	errUpdate.State = created.State
	r.Update(ctx, resource.UpdateRequest{Plan: plan, Config: cfg, State: created.State}, &errUpdate)
	var errDelete resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: created.State}, &errDelete)
}

func TestPaperclipPolicyResourceLifecycle(t *testing.T) {
	ctx := context.Background()
	fail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if fail {
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/api/companies/comp-1/tools/policies":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "pol-1", "companyId": "comp-1", "name": "Policy 1", "policyType": "allow",
			})
		case req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-1/tools/policies/pol-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "pol-1", "companyId": "comp-1", "name": "Policy 1", "policyType": "allow",
			})
		case req.Method == http.MethodPatch && req.URL.Path == "/api/companies/comp-1/tools/policies/pol-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "pol-1", "companyId": "comp-1", "name": "Policy Updated", "policyType": "allow",
			})
		case req.Method == http.MethodDelete && req.URL.Path == "/api/companies/comp-1/tools/policies/pol-1":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	r := NewPaperclipPolicyResource().(*PaperclipPolicyResource)
	var meta resource.MetadataResponse
	r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "paperclip"}, &meta)

	var confResp resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: nil}, &confResp)
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: "invalid"}, &confResp)

	schema := configureTestResource(t, server.URL, r)

	model := PaperclipPolicyResourceModel{
		ID:         types.StringNull(),
		CompanyID:  types.StringValue("comp-1"),
		Name:       types.StringValue("Policy 1"),
		PolicyType: types.StringValue("allow"),
		Rules:      types.StringNull(),
		Metadata:   types.StringNull(),
	}
	plan := tfsdk.Plan{Schema: schema}
	_ = plan.Set(ctx, &model)
	created := resource.CreateResponse{State: tfsdk.State{Schema: schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
	if created.Diagnostics.HasError() {
		t.Fatalf("create failed: %v", created.Diagnostics)
	}

	read := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatalf("read failed: %v", read.Diagnostics)
	}

	model.Name = types.StringValue("Policy Updated")
	model.ID = types.StringValue("pol-1")
	_ = plan.Set(ctx, &model)
	updated := resource.UpdateResponse{State: created.State}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: created.State}, &updated)
	if updated.Diagnostics.HasError() {
		t.Fatalf("update failed: %v", updated.Diagnostics)
	}

	r.Delete(ctx, resource.DeleteRequest{State: updated.State}, &resource.DeleteResponse{})

	// ImportState
	importResp := resource.ImportStateResponse{State: tfsdk.State{Schema: schema}}
	_ = importResp.State.Set(ctx, &PaperclipPolicyResourceModel{})
	r.ImportState(ctx, resource.ImportStateRequest{ID: "pol-1"}, &importResp)
	r.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/pol-1"}, &importResp)

	// 404 read
	read404 := resource.ReadResponse{State: tfsdk.State{Schema: schema}}
	m404 := PaperclipPolicyResourceModel{ID: types.StringValue("pol-missing"), CompanyID: types.StringValue("comp-1")}
	_ = read404.State.Set(ctx, &m404)
	r.Read(ctx, resource.ReadRequest{State: read404.State}, &read404)
	if !read404.State.Raw.IsNull() {
		t.Fatal("expected state to be removed on 404")
	}

	// Server failure error paths
	fail = true
	var errResp resource.CreateResponse
	errResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &errResp)
	var errRead resource.ReadResponse
	errRead.State = created.State
	r.Read(ctx, resource.ReadRequest{State: created.State}, &errRead)
	var errUpdate resource.UpdateResponse
	errUpdate.State = created.State
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: created.State}, &errUpdate)
	var errDelete resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: created.State}, &errDelete)
}

func TestPaperclipConnectionInstallResourceLifecycle(t *testing.T) {
	ctx := context.Background()
	fail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if fail {
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/tool-connections/conn-1/installs":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"installs": []map[string]interface{}{
					{"targetId": "agent-1", "targetType": "agent"},
				},
			})
		case req.Method == http.MethodPut && req.URL.Path == "/api/tool-connections/conn-1/installs":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"installs": []map[string]interface{}{
					{"targetId": "agent-1", "targetType": "agent"},
					{"targetId": "agent-2", "targetType": "agent"},
				},
			})
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	r := NewPaperclipConnectionInstallResource().(*PaperclipConnectionInstallResource)
	var meta resource.MetadataResponse
	r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "paperclip"}, &meta)

	var confResp resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: nil}, &confResp)
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: "invalid"}, &confResp)

	schema := configureTestResource(t, server.URL, r)

	agentList, _ := types.ListValueFrom(ctx, types.StringType, []string{"agent-1"})
	model := PaperclipConnectionInstallResourceModel{
		ID:           types.StringNull(),
		ConnectionID: types.StringValue("conn-1"),
		AgentIDs:     agentList,
		TargetType:   types.StringValue("agent"),
		CompanyID:    types.StringNull(),
	}
	plan := tfsdk.Plan{Schema: schema}
	_ = plan.Set(ctx, &model)
	created := resource.CreateResponse{State: tfsdk.State{Schema: schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
	if created.Diagnostics.HasError() {
		t.Fatalf("create failed: %v", created.Diagnostics)
	}

	read := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatalf("read failed: %v", read.Diagnostics)
	}

	agentList2, _ := types.ListValueFrom(ctx, types.StringType, []string{"agent-1", "agent-2"})
	model.AgentIDs = agentList2
	model.ID = types.StringValue("conn-1")
	_ = plan.Set(ctx, &model)
	updated := resource.UpdateResponse{State: created.State}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: created.State}, &updated)
	if updated.Diagnostics.HasError() {
		t.Fatalf("update failed: %v", updated.Diagnostics)
	}

	r.Delete(ctx, resource.DeleteRequest{State: updated.State}, &resource.DeleteResponse{})

	// ImportState
	importResp := resource.ImportStateResponse{State: tfsdk.State{Schema: schema}}
	_ = importResp.State.Set(ctx, &PaperclipConnectionInstallResourceModel{})
	r.ImportState(ctx, resource.ImportStateRequest{ID: "conn-1"}, &importResp)

	// Server failure error paths
	fail = true
	var errResp resource.CreateResponse
	errResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &errResp)
	var errRead resource.ReadResponse
	errRead.State = created.State
	r.Read(ctx, resource.ReadRequest{State: created.State}, &errRead)
	var errUpdate resource.UpdateResponse
	errUpdate.State = created.State
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: created.State}, &errUpdate)
}

func TestPaperclipWorkspaceAndStdioTemplateAndDataSources(t *testing.T) {
	ctx := context.Background()
	fail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if fail {
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		// Workspace endpoints
		case req.Method == http.MethodDelete && req.URL.Path == "/api/projects/proj-1/workspaces/ws-1":
			w.WriteHeader(http.StatusOK)
		// Stdio template endpoints
		case req.Method == http.MethodPost && req.URL.Path == "/api/companies/comp-1/tools/stdio-templates":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "tmpl-1", "companyId": "comp-1", "templateId": "tmpl-1", "name": "cli-cmd", "command": "echo",
			})
		case req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-1/tools/stdio-templates":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"templates": []map[string]interface{}{
					{"id": "tmpl-1", "companyId": "comp-1", "templateId": "tmpl-1", "name": "cli-cmd", "command": "echo"},
				},
			})
		case req.Method == http.MethodPost && req.URL.Path == "/api/companies/comp-1/tools/stdio-templates/tmpl-1/disable":
			w.WriteHeader(http.StatusOK)
		// Data sources endpoints
		case req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "comp-1", "name": "Acme", "description": "Acme Corp", "requireTaskStartConfirmation": false,
			})
		case req.Method == http.MethodGet && req.URL.Path == "/api/projects/proj-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "proj-1", "companyId": "comp-1", "name": "Project 1",
			})
		case req.Method == http.MethodGet && req.URL.Path == "/api/agents/agent-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "agent-1", "companyId": "comp-1", "name": "Eng", "role": "engineer",
			})
		case req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-1/skills/skill-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "skill-1", "companyId": "comp-1", "name": "Reviewer", "content": "# Prompt",
			})
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	// 1. Workspace Delete & ImportState
	wsRes := NewPaperclipWorkspaceResource().(*PaperclipWorkspaceResource)
	wsSchema := configureTestResource(t, server.URL, wsRes)
	wsModel := PaperclipWorkspaceResourceModel{
		ID:         types.StringValue("ws-1"),
		ProjectID:  types.StringValue("proj-1"),
		Name:       types.StringNull(),
		RepoURL:    types.StringNull(),
		Branch:     types.StringNull(),
		IsPrimary:  types.BoolNull(),
		SourceType: types.StringNull(),
	}
	wsState := tfsdk.State{Schema: wsSchema}
	_ = wsState.Set(ctx, &wsModel)
	wsRes.Delete(ctx, resource.DeleteRequest{State: wsState}, &resource.DeleteResponse{})

	var wsImport resource.ImportStateResponse
	wsImport.State = tfsdk.State{Schema: wsSchema}
	_ = wsImport.State.Set(ctx, &PaperclipWorkspaceResourceModel{})
	wsRes.ImportState(ctx, resource.ImportStateRequest{ID: "ws-1"}, &wsImport)
	wsRes.ImportState(ctx, resource.ImportStateRequest{ID: "proj-1/ws-1"}, &wsImport)

	// 2. Stdio Template Lifecycle
	tmplRes := NewPaperclipStdioTemplateResource().(*PaperclipStdioTemplateResource)
	tmplSchema := configureTestResource(t, server.URL, tmplRes)
	emptyList, _ := types.ListValueFrom(ctx, types.StringType, []string{})
	tmplModel := PaperclipStdioTemplateResourceModel{
		ID:          types.StringNull(),
		CompanyID:   types.StringValue("comp-1"),
		TemplateID:  types.StringValue("tmpl-1"),
		Name:        types.StringValue("cli-cmd"),
		Description: types.StringNull(),
		Command:     types.StringValue("echo"),
		Args:        emptyList,
		EnvKeys:     emptyList,
	}
	tmplPlan := tfsdk.Plan{Schema: tmplSchema}
	_ = tmplPlan.Set(ctx, &tmplModel)
	tmplCreated := resource.CreateResponse{State: tfsdk.State{Schema: tmplSchema}}
	tmplRes.Create(ctx, resource.CreateRequest{Plan: tmplPlan}, &tmplCreated)
	if tmplCreated.Diagnostics.HasError() {
		t.Fatalf("stdio template create failed: %v", tmplCreated.Diagnostics)
	}

	tmplRead := resource.ReadResponse{State: tmplCreated.State}
	tmplRes.Read(ctx, resource.ReadRequest{State: tmplCreated.State}, &tmplRead)
	if tmplRead.Diagnostics.HasError() {
		t.Fatalf("stdio template read failed: %v", tmplRead.Diagnostics)
	}

	tmplRes.Delete(ctx, resource.DeleteRequest{State: tmplRead.State}, &resource.DeleteResponse{})

	var tmplImport resource.ImportStateResponse
	tmplImport.State = tfsdk.State{Schema: tmplSchema}
	_ = tmplImport.State.Set(ctx, &PaperclipStdioTemplateResourceModel{})
	tmplRes.ImportState(ctx, resource.ImportStateRequest{ID: "tmpl-1"}, &tmplImport)
	tmplRes.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/tmpl-1"}, &tmplImport)

	// 3. Data Sources: Company, Project, Agent, Skill
	// Company DS
	compDS := NewPaperclipCompanyDataSource().(*PaperclipCompanyDataSource)
	compDSSchema := configureTestDataSource(t, server.URL, compDS)
	compDSModel := PaperclipCompanyDataSourceModel{ID: types.StringValue("comp-1")}
	compState := tfsdk.State{Schema: compDSSchema}
	_ = compState.Set(ctx, &compDSModel)
	compDSConfig := tfsdk.Config{Schema: compDSSchema, Raw: compState.Raw}
	var compDSRead datasource.ReadResponse
	compDSRead.State = tfsdk.State{Schema: compDSSchema}
	compDS.Read(ctx, datasource.ReadRequest{Config: compDSConfig}, &compDSRead)
	if compDSRead.Diagnostics.HasError() {
		t.Fatalf("company datasource read failed: %v", compDSRead.Diagnostics)
	}

	// Project DS
	projDS := NewPaperclipProjectDataSource().(*PaperclipProjectDataSource)
	projDSSchema := configureTestDataSource(t, server.URL, projDS)
	projDSModel := PaperclipProjectDataSourceModel{ID: types.StringValue("proj-1")}
	projState := tfsdk.State{Schema: projDSSchema}
	_ = projState.Set(ctx, &projDSModel)
	projDSConfig := tfsdk.Config{Schema: projDSSchema, Raw: projState.Raw}
	var projDSRead datasource.ReadResponse
	projDSRead.State = tfsdk.State{Schema: projDSSchema}
	projDS.Read(ctx, datasource.ReadRequest{Config: projDSConfig}, &projDSRead)
	if projDSRead.Diagnostics.HasError() {
		t.Fatalf("project datasource read failed: %v", projDSRead.Diagnostics)
	}

	// Agent DS
	agentDS := NewPaperclipAgentDataSource().(*PaperclipAgentDataSource)
	agentDSSchema := configureTestDataSource(t, server.URL, agentDS)
	agentDSModel := PaperclipAgentDataSourceModel{ID: types.StringValue("agent-1")}
	agentState := tfsdk.State{Schema: agentDSSchema}
	_ = agentState.Set(ctx, &agentDSModel)
	agentDSConfig := tfsdk.Config{Schema: agentDSSchema, Raw: agentState.Raw}
	var agentDSRead datasource.ReadResponse
	agentDSRead.State = tfsdk.State{Schema: agentDSSchema}
	agentDS.Read(ctx, datasource.ReadRequest{Config: agentDSConfig}, &agentDSRead)
	if agentDSRead.Diagnostics.HasError() {
		t.Fatalf("agent datasource read failed: %v", agentDSRead.Diagnostics)
	}

	// Skill DS
	skillDS := NewPaperclipSkillDataSource().(*PaperclipSkillDataSource)
	skillDSSchema := configureTestDataSource(t, server.URL, skillDS)
	skillDSModel := PaperclipSkillDataSourceModel{CompanyID: types.StringValue("comp-1"), ID: types.StringValue("skill-1")}
	skillState := tfsdk.State{Schema: skillDSSchema}
	_ = skillState.Set(ctx, &skillDSModel)
	skillDSConfig := tfsdk.Config{Schema: skillDSSchema, Raw: skillState.Raw}
	var skillDSRead datasource.ReadResponse
	skillDSRead.State = tfsdk.State{Schema: skillDSSchema}
	skillDS.Read(ctx, datasource.ReadRequest{Config: skillDSConfig}, &skillDSRead)
	if skillDSRead.Diagnostics.HasError() {
		t.Fatalf("skill datasource read failed: %v", skillDSRead.Diagnostics)
	}

	// Failures for data sources
	fail = true
	var failDSRead datasource.ReadResponse
	failDSRead.State = tfsdk.State{Schema: compDSSchema}
	compDS.Read(ctx, datasource.ReadRequest{Config: compDSConfig}, &failDSRead)
	if !failDSRead.Diagnostics.HasError() {
		t.Fatal("expected failure on company datasource read")
	}
}

func TestPaperclipAgentResourceLifecycle(t *testing.T) {
	ctx := context.Background()
	fail := false
	currentPerms := map[string]interface{}{"canAssignTasks": true, "canCreateAgents": false, "canCreateSkills": false}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if fail {
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/companies/comp-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "comp-1"})
		case req.Method == http.MethodPost && req.URL.Path == "/api/companies/comp-1/agents":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "agent-1", "companyId": "comp-1", "name": "Agent 1", "role": "engineer",
				"title": "Senior Engineer", "adapterType": "antigravity_local", "status": "active",
				"runtimeConfig": map[string]interface{}{"mode": "fast"},
				"adapterConfig": map[string]interface{}{"timeout": 30},
				"metadata":      map[string]interface{}{"tag": "v1"},
				"permissions":   currentPerms,
			})
		case req.Method == http.MethodGet && req.URL.Path == "/api/agents/agent-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "agent-1", "companyId": "comp-1", "name": "Agent 1", "role": "engineer",
				"title": "Senior Engineer", "adapterType": "antigravity_local", "status": "active",
				"runtimeConfig": map[string]interface{}{"mode": "fast"},
				"adapterConfig": map[string]interface{}{"timeout": 30},
				"metadata":      map[string]interface{}{"tag": "v1"},
				"permissions":   currentPerms,
			})
		case req.Method == http.MethodPatch && req.URL.Path == "/api/agents/agent-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "agent-1", "companyId": "comp-1", "name": "Agent Updated", "role": "lead",
				"title": "Staff Engineer", "adapterType": "antigravity_local", "status": "active",
				"runtimeConfig": map[string]interface{}{"mode": "accurate"},
				"adapterConfig": map[string]interface{}{"timeout": 60},
				"metadata":      map[string]interface{}{"tag": "v2"},
				"permissions":   currentPerms,
			})
		case req.Method == http.MethodDelete && req.URL.Path == "/api/agents/agent-1":
			w.WriteHeader(http.StatusOK)
		case req.Method == http.MethodGet && req.URL.Path == "/api/agents/agent-1/permissions":
			_ = json.NewEncoder(w).Encode(currentPerms)
		case req.Method == http.MethodPatch && req.URL.Path == "/api/agents/agent-1/permissions":
			var body map[string]interface{}
			_ = json.NewDecoder(req.Body).Decode(&body)
			for k, v := range body {
				currentPerms[k] = v
			}
			_ = json.NewEncoder(w).Encode(currentPerms)
		case req.Method == http.MethodGet && strings.HasPrefix(req.URL.Path, "/api/agents/agent-1/instructions-bundle/file"):
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"content": "# Instructions"})
		case req.Method == http.MethodPut && req.URL.Path == "/api/agents/agent-1/instructions-bundle/file":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	r := NewPaperclipAgentResource().(*PaperclipAgentResource)
	var meta resource.MetadataResponse
	r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "paperclip"}, &meta)

	var confResp resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: nil}, &confResp)
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: "invalid"}, &confResp)

	schema := configureTestResource(t, server.URL, r)

	model := PaperclipAgentResourceModel{
		ID:              types.StringNull(),
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("Agent 1"),
		Role:            types.StringValue("engineer"),
		Title:           types.StringValue("Senior Engineer"),
		AdapterType:     types.StringValue("antigravity_local"),
		Status:          types.StringNull(),
		RuntimeConfig:   types.StringValue(`{"mode":"fast"}`),
		AdapterConfig:   types.StringValue(`{"timeout":30}`),
		Metadata:        types.StringValue(`{"tag":"v1"}`),
		Instructions:    types.StringValue("# Instructions"),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(false),
	}
	plan := tfsdk.Plan{Schema: schema}
	_ = plan.Set(ctx, &model)
	created := resource.CreateResponse{State: tfsdk.State{Schema: schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
	if created.Diagnostics.HasError() {
		t.Fatalf("agent create failed: %v", created.Diagnostics)
	}

	read := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatalf("agent read failed: %v", read.Diagnostics)
	}

	model.Name = types.StringValue("Agent Updated")
	model.Role = types.StringValue("lead")
	model.Title = types.StringValue("Staff Engineer")
	model.CanCreateAgents = types.BoolValue(true)
	model.ID = types.StringValue("agent-1")
	_ = plan.Set(ctx, &model)
	updated := resource.UpdateResponse{State: created.State}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: created.State}, &updated)
	if updated.Diagnostics.HasError() {
		t.Fatalf("agent update failed: %v", updated.Diagnostics)
	}

	r.Delete(ctx, resource.DeleteRequest{State: updated.State}, &resource.DeleteResponse{})

	// ImportState
	importResp := resource.ImportStateResponse{State: tfsdk.State{Schema: schema}}
	_ = importResp.State.Set(ctx, &PaperclipAgentResourceModel{})
	r.ImportState(ctx, resource.ImportStateRequest{ID: "agent-1"}, &importResp)
	r.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/agent-1"}, &importResp)

	// 404 read removes resource
	read404 := resource.ReadResponse{State: tfsdk.State{Schema: schema}}
	m404 := PaperclipAgentResourceModel{ID: types.StringValue("agent-missing"), CompanyID: types.StringValue("comp-1")}
	_ = read404.State.Set(ctx, &m404)
	r.Read(ctx, resource.ReadRequest{State: read404.State}, &read404)
	if !read404.State.Raw.IsNull() {
		t.Fatal("expected state to be removed on 404")
	}

	// Server failure error paths
	fail = true
	var errResp resource.CreateResponse
	errResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &errResp)
	var errRead resource.ReadResponse
	errRead.State = created.State
	r.Read(ctx, resource.ReadRequest{State: created.State}, &errRead)
	var errUpdate resource.UpdateResponse
	errUpdate.State = created.State
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: created.State}, &errUpdate)
	var errDelete resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: created.State}, &errDelete)
}

func TestPaperclipAppResourceLifecycle(t *testing.T) {
	ctx := context.Background()
	fail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if fail {
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodPost && req.URL.Path == "/api/companies/comp-1/tools/applications":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "app-1", "companyId": "comp-1", "name": "App 1", "type": "mcp_http",
				"applicationKey": "app-key-1", "description": "App Desc", "status": "active",
				"metadata": map[string]interface{}{"env": "prod"},
			})
		case req.Method == http.MethodGet && req.URL.Path == "/api/tool-applications/app-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "app-1", "companyId": "comp-1", "name": "App 1", "type": "mcp_http",
				"applicationKey": "app-key-1", "description": "App Desc", "status": "active",
				"metadata": map[string]interface{}{"env": "prod"},
			})
		case req.Method == http.MethodPatch && req.URL.Path == "/api/tool-applications/app-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "app-1", "companyId": "comp-1", "name": "App Updated", "type": "mcp_http",
				"applicationKey": "app-key-1", "description": "New Desc", "status": "active",
				"metadata": map[string]interface{}{"env": "staging"},
			})
		case req.Method == http.MethodDelete && req.URL.Path == "/api/tool-applications/app-1":
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	r := NewPaperclipAppResource().(*PaperclipAppResource)
	var meta resource.MetadataResponse
	r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "paperclip"}, &meta)

	var confResp resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: nil}, &confResp)
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: "invalid"}, &confResp)

	schema := configureTestResource(t, server.URL, r)

	model := PaperclipAppResourceModel{
		ID:             types.StringNull(),
		CompanyID:      types.StringValue("comp-1"),
		Name:           types.StringValue("App 1"),
		Type:           types.StringValue("mcp_http"),
		ApplicationKey: types.StringValue("app-key-1"),
		Description:    types.StringValue("App Desc"),
		Metadata:       types.StringValue(`{"env":"prod"}`),
	}
	plan := tfsdk.Plan{Schema: schema}
	_ = plan.Set(ctx, &model)
	created := resource.CreateResponse{State: tfsdk.State{Schema: schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
	if created.Diagnostics.HasError() {
		t.Fatalf("app create failed: %v", created.Diagnostics)
	}

	read := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatalf("app read failed: %v", read.Diagnostics)
	}

	model.Name = types.StringValue("App Updated")
	model.Description = types.StringValue("New Desc")
	model.Metadata = types.StringValue(`{"env":"staging"}`)
	model.ID = types.StringValue("app-1")
	_ = plan.Set(ctx, &model)
	updated := resource.UpdateResponse{State: created.State}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: created.State}, &updated)
	if updated.Diagnostics.HasError() {
		t.Fatalf("app update failed: %v", updated.Diagnostics)
	}

	r.Delete(ctx, resource.DeleteRequest{State: updated.State}, &resource.DeleteResponse{})

	// ImportState
	importResp := resource.ImportStateResponse{State: tfsdk.State{Schema: schema}}
	_ = importResp.State.Set(ctx, &PaperclipAppResourceModel{})
	r.ImportState(ctx, resource.ImportStateRequest{ID: "app-1"}, &importResp)
	r.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/app-1"}, &importResp)

	// 404 read removes resource
	read404 := resource.ReadResponse{State: tfsdk.State{Schema: schema}}
	m404 := PaperclipAppResourceModel{ID: types.StringValue("app-missing"), CompanyID: types.StringValue("comp-1")}
	_ = read404.State.Set(ctx, &m404)
	r.Read(ctx, resource.ReadRequest{State: read404.State}, &read404)
	if !read404.State.Raw.IsNull() {
		t.Fatal("expected state to be removed on 404")
	}

	// Server failure error paths
	fail = true
	var errResp resource.CreateResponse
	errResp.State = tfsdk.State{Schema: schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &errResp)
	var errRead resource.ReadResponse
	errRead.State = created.State
	r.Read(ctx, resource.ReadRequest{State: created.State}, &errRead)
	var errUpdate resource.UpdateResponse
	errUpdate.State = created.State
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: created.State}, &errUpdate)
	var errDelete resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: created.State}, &errDelete)
}

func TestPaperclipAppPermissionAndLocalEnvLifecycle(t *testing.T) {
	ctx := context.Background()
	fail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if fail {
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/tool-connections/conn-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "conn-1", "name": "App Conn", "companyId": "comp-1"})
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	cli, _ := client.NewClient(server.URL, "token")

	// App permission import
	appPermRes := &PaperclipAppPermissionResource{client: cli}
	var appPermSchema resource.SchemaResponse
	appPermRes.Schema(ctx, resource.SchemaRequest{}, &appPermSchema)
	importResp := resource.ImportStateResponse{State: tfsdk.State{Schema: appPermSchema.Schema}}
	_ = importResp.State.Set(ctx, &PaperclipAppPermissionResourceModel{})
	appPermRes.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/conn-1"}, &importResp)
	appPermRes.ImportState(ctx, resource.ImportStateRequest{ID: "conn-1"}, &importResp)

	// Local environment delete & errors
	envRes := NewPaperclipLocalEnvironmentResource().(*PaperclipLocalEnvironmentResource)
	var envSchema resource.SchemaResponse
	envRes.Schema(ctx, resource.SchemaRequest{}, &envSchema)
	envModel := PaperclipLocalEnvironmentModel{
		ID:                 types.StringValue("local-env-1"),
		CompanyID:          types.StringValue("comp-1"),
		InheritServicePath: types.BoolValue(true),
	}
	envState := tfsdk.State{Schema: envSchema.Schema}
	_ = envState.Set(ctx, &envModel)
	envRes.Delete(ctx, resource.DeleteRequest{State: envState}, &resource.DeleteResponse{})

	var envConf resource.ConfigureResponse
	envRes.Configure(ctx, resource.ConfigureRequest{ProviderData: nil}, &envConf)
	envRes.Configure(ctx, resource.ConfigureRequest{ProviderData: "invalid"}, &envConf)
}
