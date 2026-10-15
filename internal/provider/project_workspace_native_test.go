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
	"time"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func testProjectPolicy(t *testing.T, remote map[string]interface{}) types.Object {
	t.Helper()
	result, ds := policyFromAPI(projectPolicyFields, remote)
	if ds.HasError() {
		t.Fatal(ds)
	}
	return result
}

func invalidProjectPolicyJSON(t *testing.T) types.Object {
	t.Helper()
	policy := testProjectPolicy(t, map[string]interface{}{"enabled": true})
	attrs := policy.Attributes()
	attrs["cleanup_policy"] = types.StringValue("not an object")
	return types.ObjectValueMust(policyTypes(projectPolicyFields), attrs)
}

func TestWorkspaceSourceAndUnsupportedValueRules(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*PaperclipWorkspaceResourceModel)
		want   string
	}{
		{"local path", func(m *PaperclipWorkspaceResourceModel) { m.SourceType = types.StringValue("local_path") }, ""},
		{"non git path", func(m *PaperclipWorkspaceResourceModel) { m.SourceType = types.StringValue("non_git_path") }, ""},
		{"git URL", func(m *PaperclipWorkspaceResourceModel) {
			m.SourceType = types.StringValue("git_repo")
			m.Cwd = types.StringNull()
			m.RepoURL = types.StringValue("https://example.test/repository.git")
		}, ""},
		{"missing source", func(m *PaperclipWorkspaceResourceModel) { m.Cwd = types.StringNull() }, "workspace requires cwd or repo_url"},
		{"remote reference", func(m *PaperclipWorkspaceResourceModel) {
			m.SourceType = types.StringValue("remote_managed")
			m.Cwd = types.StringNull()
			m.RemoteWorkspaceRef = types.StringValue("workspace/one")
		}, ""},
		{"remote URL", func(m *PaperclipWorkspaceResourceModel) {
			m.SourceType = types.StringValue("remote_managed")
			m.RepoURL = types.StringValue("https://example.test/repository.git")
		}, ""},
		{"missing remote", func(m *PaperclipWorkspaceResourceModel) { m.SourceType = types.StringValue("remote_managed") }, "remote_managed requires remote_workspace_ref or repo_url"},
		{"unknown source", func(m *PaperclipWorkspaceResourceModel) { m.Cwd = types.StringUnknown() }, ""},
		{"empty ref", func(m *PaperclipWorkspaceResourceModel) { m.RepoRef = types.StringValue("") }, "repo_ref cannot be empty or whitespace"},
		{"trimmed ref", func(m *PaperclipWorkspaceResourceModel) { m.RepoRef = types.StringValue(" main ") }, "repo_ref cannot have leading or trailing whitespace"},
		{"sentinel cwd", func(m *PaperclipWorkspaceResourceModel) { m.Cwd = types.StringValue("/__paperclip_repo_only__") }, "native sentinel"},
		{"relative URL", func(m *PaperclipWorkspaceResourceModel) { m.RepoURL = types.StringValue("repository.git") }, "repo_url must be an absolute repository URL"},
		{"malformed URL", func(m *PaperclipWorkspaceResourceModel) { m.RepoURL = types.StringValue("://%") }, "repo_url must be an absolute repository URL"},
		{"independent null", func(m *PaperclipWorkspaceResourceModel) { m.RepoRef = types.StringValue("main") }, "default_ref cannot be independently null while repo_ref is set"},
		{"unknown ref", func(m *PaperclipWorkspaceResourceModel) { m.RepoRef = types.StringUnknown() }, ""},
		{"malformed metadata", func(m *PaperclipWorkspaceResourceModel) { m.Metadata = types.StringValue("[]") }, "metadata: must be a JSON object"},
		{"reserved metadata", func(m *PaperclipWorkspaceResourceModel) { m.Metadata = types.StringValue(`{"runtimeConfig":{}}`) }, "metadata.runtimeConfig is owned by runtime_config"},
		{"malformed runtime", func(m *PaperclipWorkspaceResourceModel) { m.RuntimeConfig = types.StringValue("null") }, "runtime_config: must be a JSON object"},
		{"empty runtime", func(m *PaperclipWorkspaceResourceModel) { m.RuntimeConfig = types.StringValue("{}") }, "runtime_config with no non-null fields"},
		{"null runtime members", func(m *PaperclipWorkspaceResourceModel) { m.RuntimeConfig = types.StringValue(`{"desiredState":null}`) }, "runtime_config with no non-null fields"},
		{"invalid runtime key", func(m *PaperclipWorkspaceResourceModel) { m.RuntimeConfig = types.StringValue(`{"unknown":true}`) }, "unsupported runtime_config key"},
		{"invalid runtime object", func(m *PaperclipWorkspaceResourceModel) {
			m.RuntimeConfig = types.StringValue(`{"workspaceRuntime":[]}`)
		}, "workspaceRuntime must be an object"},
		{"invalid desired state", func(m *PaperclipWorkspaceResourceModel) {
			m.RuntimeConfig = types.StringValue(`{"desiredState":"paused"}`)
		}, "desiredState must be running, stopped or manual"},
		{"invalid services object", func(m *PaperclipWorkspaceResourceModel) { m.RuntimeConfig = types.StringValue(`{"serviceStates":[]}`) }, "serviceStates must be an object"},
		{"empty services", func(m *PaperclipWorkspaceResourceModel) { m.RuntimeConfig = types.StringValue(`{"serviceStates":{}}`) }, "serviceStates cannot be empty"},
		{"invalid service state", func(m *PaperclipWorkspaceResourceModel) {
			m.RuntimeConfig = types.StringValue(`{"serviceStates":{"app":"paused"}}`)
		}, "serviceStates values must be running, stopped or manual"},
		{"valid runtime", func(m *PaperclipWorkspaceResourceModel) {
			m.RuntimeConfig = types.StringValue(`{"workspaceRuntime":{},"desiredState":"running","serviceStates":{"app":"manual"}}`)
		}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := PaperclipWorkspaceResourceModel{Name: types.StringValue("workspace"), Cwd: types.StringValue("/work/project"), SourceType: types.StringValue("local_path"), IsPrimary: types.BoolValue(true)}
			tc.change(&m)
			err := validateWorkspaceConfig(m)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
			r := NewPaperclipWorkspaceResource().(*PaperclipWorkspaceResource)
			var s resource.SchemaResponse
			r.Schema(context.Background(), resource.SchemaRequest{}, &s)
			plan := tfsdk.Plan{Schema: s.Schema}
			if ds := plan.Set(context.Background(), m); ds.HasError() {
				t.Fatal(ds)
			}
			var response resource.ValidateConfigResponse
			r.ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: tfsdk.Config{Schema: s.Schema, Raw: plan.Raw}}, &response)
			if response.Diagnostics.HasError() != (tc.want != "") {
				t.Fatalf("validation: %v", response.Diagnostics)
			}
		})
	}
}

func TestWorkspaceUnsupportedClearPreservesIdentityWithoutWrites(t *testing.T) {
	ctx := context.Background()
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		writes++
		t.Errorf("unsupported clear contacted API: %s %s", req.Method, req.URL.Path)
		http.Error(w, "unexpected write", 500)
	}))
	defer server.Close()
	r := NewPaperclipWorkspaceResource().(*PaperclipWorkspaceResource)
	s := configureTestResource(t, server.URL, r)
	m := PaperclipWorkspaceResourceModel{ID: types.StringValue("existing"), ProjectID: types.StringValue("project"), Name: types.StringValue("workspace"), RepoURL: types.StringValue("https://example.test/repo"), RepoRef: types.StringValue("main"), DefaultRef: types.StringValue("release"), IsPrimary: types.BoolValue(true)}
	state := tfsdk.State{Schema: s}
	if ds := state.Set(ctx, m); ds.HasError() {
		t.Fatal(ds)
	}
	m.DefaultRef = types.StringNull()
	plan := tfsdk.Plan{Schema: s}
	if ds := plan.Set(ctx, m); ds.HasError() {
		t.Fatal(ds)
	}
	response := resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &response)
	if !response.Diagnostics.HasError() || !response.State.Raw.Equal(state.Raw) || writes != 0 {
		t.Fatalf("unsupported clear mutated identity: %v, writes=%d", response.Diagnostics, writes)
	}
	if len(response.Diagnostics) != 1 || response.Diagnostics[0].Summary() != "Unsupported Paperclip workspace configuration" || response.Diagnostics[0].Detail() != "default_ref cannot be independently null while repo_ref is set: the Paperclip API reads null as repo_ref. Set default_ref to the effective inherited ref or an explicit distinct ref, or clear both refs" {
		t.Fatalf("unsupported clear diagnostic changed: %v", response.Diagnostics)
	}
	var modify resource.ModifyPlanResponse
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan}, &modify)
	if !modify.Diagnostics.HasError() || writes != 0 {
		t.Fatalf("unsupported clear not rejected at plan: %v", modify.Diagnostics)
	}
	create := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &create)
	if !create.Diagnostics.HasError() || writes != 0 {
		t.Fatalf("unsupported create was allowed: %v", create.Diagnostics)
	}
}

func TestWorkspacePrimarySelectionReadOnlyPreflight(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		want       string
	}{
		{"only workspace", `[{"id":"existing","projectId":"project"}]`, 200, "is_primary cannot be false"},
		{"alternate", `[{"id":"existing","projectId":"project"},{"id":"alternate","projectId":"project"}]`, 200, ""},
		{"read error", `{"error":"denied"}`, 403, "403"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method != "GET" || req.URL.Path != "/api/projects/project/workspaces" {
					t.Errorf("unexpected preflight mutation: %s %s", req.Method, req.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			r := NewPaperclipWorkspaceResource().(*PaperclipWorkspaceResource)
			s := configureTestResource(t, server.URL, r)
			m := PaperclipWorkspaceResourceModel{ID: types.StringValue("existing"), ProjectID: types.StringValue("project"), Name: types.StringValue("workspace"), Cwd: types.StringValue("/work"), IsPrimary: types.BoolValue(false)}
			err := r.validateWrite(context.Background(), m, "existing")
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q got %v", tc.want, err)
			}
			plan := tfsdk.Plan{Schema: s}
			if ds := plan.Set(context.Background(), m); ds.HasError() {
				t.Fatal(ds)
			}
			var response resource.ModifyPlanResponse
			r.ModifyPlan(context.Background(), resource.ModifyPlanRequest{Plan: plan}, &response)
			if response.Diagnostics.HasError() != (tc.want != "") {
				t.Fatal(response.Diagnostics)
			}
		})
	}
}

func TestWorkspaceNativeReadPreservesInheritanceAndAllFields(t *testing.T) {
	ctx := context.Background()
	ws := client.PaperclipWorkspace{ID: "existing", ProjectID: "project", Name: "workspace", RepoRef: "main", Cwd: "/work", Visibility: "advanced", SetupCommand: "setup", CleanupCommand: "cleanup", RemoteProvider: "cloud", RemoteWorkspaceRef: "remote", SharedWorkspaceKey: "shared", Metadata: map[string]interface{}{"team": "core", "runtimeConfig": map[string]interface{}{"desiredState": "manual"}}, RuntimeConfig: map[string]interface{}{"workspaceRuntime": nil, "desiredState": "manual", "serviceStates": nil}, IsPrimary: true, SourceType: "git_repo"}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "GET" || req.URL.Path != "/api/projects/project/workspaces" {
			t.Errorf("unexpected refresh %s %s", req.Method, req.URL.Path)
		}
		native := ws
		if native.DefaultRef == "" {
			native.DefaultRef = native.RepoRef
		}
		_ = json.NewEncoder(w).Encode([]client.PaperclipWorkspace{native})
	}))
	defer server.Close()
	r := NewPaperclipWorkspaceResource().(*PaperclipWorkspaceResource)
	s := configureTestResource(t, server.URL, r)
	m := PaperclipWorkspaceResourceModel{ID: types.StringValue("existing"), ProjectID: types.StringValue("project"), Metadata: types.StringNull(), RuntimeConfig: types.StringValue(`{ "desiredState": "manual" }`)}
	state := tfsdk.State{Schema: s}
	if ds := state.Set(ctx, m); ds.HasError() {
		t.Fatal(ds)
	}
	response := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &response)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	var got PaperclipWorkspaceResourceModel
	if ds := response.State.Get(ctx, &got); ds.HasError() {
		t.Fatal(ds)
	}
	if got.DefaultRef.ValueString() != "main" || got.RepoRef.ValueString() != "main" || got.RemoteProvider.ValueString() != "cloud" || got.SharedWorkspaceKey.ValueString() != "shared" || got.SetupCommand.ValueString() != "setup" || got.CleanupCommand.ValueString() != "cleanup" || got.Cwd.ValueString() != "/work" || got.Visibility.ValueString() != "advanced" || got.RemoteWorkspaceRef.ValueString() != "remote" || got.Metadata.ValueString() != `{"team":"core"}` || got.RuntimeConfig.ValueString() != m.RuntimeConfig.ValueString() {
		t.Fatalf("native configuration not reflected: %+v", got)
	}
	ws.DefaultRef = "release"
	drift := resource.ReadResponse{State: response.State}
	r.Read(ctx, resource.ReadRequest{State: response.State}, &drift)
	if ds := drift.State.Get(ctx, &got); ds.HasError() {
		t.Fatal(ds)
	}
	if got.DefaultRef.ValueString() != "release" {
		t.Fatal("independent default ref drift was hidden")
	}
	if normalizeWorkspaceRuntime("unexpected") != "unexpected" {
		t.Fatal("non object normalization changed value")
	}
}

func TestWorkspaceStateUpgradeRetainsIdentityAndSplitsBranch(t *testing.T) {
	ctx := context.Background()
	server := providerserver.NewProtocol6(New("test")())()
	schemas, err := server.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"id":"existing","project_id":"project","name":"workspace","repo_url":"https://example.test/repo","branch":"release","source_type":"git_repo","is_primary":true}`, `invalid`, `{"id":{},"branch":"main"}`} {
		response, err := server.UpgradeResourceState(ctx, &tfprotov6.UpgradeResourceStateRequest{TypeName: "paperclip_workspace", Version: 0, RawState: &tfprotov6.RawState{JSON: []byte(raw)}})
		if err != nil {
			t.Fatal(err)
		}
		if raw == "invalid" || strings.Contains(raw, `"id":{}`) {
			if len(response.Diagnostics) == 0 {
				t.Fatal("malformed state accepted")
			}
			continue
		}
		if len(response.Diagnostics) != 0 {
			t.Fatal(response.Diagnostics)
		}
		value, err := response.UpgradedState.Unmarshal(schemas.ResourceSchemas["paperclip_workspace"].ValueType())
		if err != nil {
			t.Fatal(err)
		}
		var values map[string]tftypes.Value
		if err := value.As(&values); err != nil {
			t.Fatal(err)
		}
		for key, want := range map[string]string{"id": "existing", "project_id": "project", "repo_ref": "release", "default_ref": "release"} {
			var got string
			if err := values[key].As(&got); err != nil || got != want {
				t.Fatalf("%s = %q (%v)", key, got, err)
			}
		}
		if _, ok := values["branch"]; ok {
			t.Fatal("coupled branch remains in state")
		}
	}
}

func TestNativeObjectValidatorRejectsInvalidJSONObjects(t *testing.T) {
	v := nativeObjectValidator{}
	if v.Description(context.Background()) == "" || v.MarkdownDescription(context.Background()) == "" {
		t.Fatal("missing object contract")
	}
	for _, value := range []string{"", "null", "[]", `{"valid":true}`} {
		var response validator.StringResponse
		v.ValidateString(context.Background(), validator.StringRequest{Path: path.Root("metadata"), ConfigValue: types.StringValue(value)}, &response)
		if response.Diagnostics.HasError() != (value != `{"valid":true}`) {
			t.Fatalf("value %q validation: %v", value, response.Diagnostics)
		}
	}
}

func TestProjectNativeFieldsRoundTripAndClear(t *testing.T) {
	ctx := context.Background()
	var remote map[string]interface{}
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case "/api/goals/goal":
			_ = json.NewEncoder(w).Encode(client.PaperclipGoal{ID: "goal", CompanyID: "company"})
			return
		case "/api/companies/company/projects", "/api/projects/project":
			if req.Method == "POST" || req.Method == "PATCH" {
				writes++
				var payload map[string]interface{}
				if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
					t.Error(err)
				}
				if _, ok := payload["leadAgentId"]; ok {
					t.Error("project resource changed lead ownership")
				}
				if stamp, ok := payload["archivedAt"].(string); ok {
					parsed, err := time.Parse(time.RFC3339Nano, stamp)
					if err != nil {
						t.Error(err)
					}
					payload["archivedAt"] = parsed.Format("2006-01-02T15:04:05.000Z")
				}
				remote = payload
				remote["id"] = "project"
				remote["companyId"] = "company"
			}
			_ = json.NewEncoder(w).Encode(remote)
			return
		default:
			t.Errorf("unexpected native request %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	defer server.Close()
	r := NewPaperclipProjectResource().(*PaperclipProjectResource)
	s := configureTestResource(t, server.URL, r)
	goals, _ := types.SetValueFrom(ctx, types.StringType, []string{"goal"})
	m := PaperclipProjectResourceModel{CompanyID: types.StringValue("company"), Name: types.StringValue("project"), Description: types.StringValue("description"), Status: types.StringValue("in_progress"), GoalIDs: goals, TargetDate: types.StringValue("2026-12-31"), Color: types.StringValue("#123456"), Icon: types.StringValue("rocket"), ArchivedAt: types.StringNull(), ArchiveOnDestroy: types.BoolValue(false), Env: map[string]EnvBindingModel{"REGION": {Type: types.StringValue("plain"), Value: types.StringValue("test")}}, ExecutionWorkspacePolicy: testProjectPolicy(t, map[string]interface{}{"enabled": true, "allowIssueOverride": false, "defaultMode": "isolated_workspace", "sharedWorkspaceConcurrency": "serialize", "workspaceStrategy": map[string]interface{}{"type": "git_worktree", "baseRef": "develop", "branchTemplate": "tasks/{{issue.identifier}}", "worktreeParentDir": "/work", "provisionCommand": "provision", "runtimeProvisionCommand": "runtime", "teardownCommand": "teardown"}, "workspaceRuntime": map[string]interface{}{"services": map[string]interface{}{}}, "branchPolicy": map[string]interface{}{}, "pullRequestPolicy": map[string]interface{}{}, "runtimePolicy": map[string]interface{}{}, "cleanupPolicy": map[string]interface{}{}, "authorizationPolicy": map[string]interface{}{"assignmentPolicy": map[string]interface{}{"mode": "protected"}, "protectedAgent": map[string]interface{}{"blockAssignment": false, "blockReason": "governed"}, "agentVisibility": map[string]interface{}{"mode": "discoverable"}, "managedBy": map[string]interface{}{"owner": "configuration"}}})}
	plan := tfsdk.Plan{Schema: s}
	if ds := plan.Set(ctx, m); ds.HasError() {
		t.Fatal(ds)
	}
	var config resource.ValidateConfigResponse
	r.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: tfsdk.Config{Schema: s, Raw: plan.Raw}}, &config)
	if config.Diagnostics.HasError() {
		t.Fatal(config.Diagnostics)
	}
	var modify resource.ModifyPlanResponse
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan}, &modify)
	if modify.Diagnostics.HasError() {
		t.Fatal(modify.Diagnostics)
	}
	created := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
	if created.Diagnostics.HasError() {
		t.Fatal(created.Diagnostics)
	}
	var got PaperclipProjectResourceModel
	if ds := created.State.Get(ctx, &got); ds.HasError() {
		t.Fatal(ds)
	}
	m.ID = types.StringValue("project")
	if !reflect.DeepEqual(projectRequest(ctx, m), projectRequest(ctx, got)) {
		t.Fatalf("round trip changed native fields: got %+v want %+v", projectRequest(ctx, got), projectRequest(ctx, m))
	}
	if remote["color"] != "#123456" || remote["status"] != "in_progress" || remote["archivedAt"] != nil {
		t.Fatalf("native payload omitted fields: %v", remote)
	}
	m.ArchivedAt = types.StringValue("2026-10-15T00:00:00Z")
	if ds := plan.Set(ctx, m); ds.HasError() {
		t.Fatal(ds)
	}
	archived := resource.UpdateResponse{State: created.State}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: created.State}, &archived)
	if archived.Diagnostics.HasError() {
		t.Fatal(archived.Diagnostics)
	}
	if ds := archived.State.Get(ctx, &got); ds.HasError() || got.ArchivedAt.ValueString() != m.ArchivedAt.ValueString() || remote["archivedAt"] != "2026-10-15T00:00:00.000Z" {
		t.Fatalf("native timestamp normalization failed: %v / %v", got.ArchivedAt, ds)
	}
	remote["color"] = "changed"
	read := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatal(read.Diagnostics)
	}
	if ds := read.State.Get(ctx, &got); ds.HasError() {
		t.Fatal(ds)
	}
	if got.Color.ValueString() != "changed" {
		t.Fatal("project drift was hidden")
	}
	m.Env = map[string]EnvBindingModel{}
	m.Description = types.StringValue("")
	m.Color = types.StringNull()
	m.TargetDate = types.StringNull()
	m.Icon = types.StringNull()
	m.ArchivedAt = types.StringNull()
	m.GoalIDs = types.SetNull(types.StringType)
	m.ExecutionWorkspacePolicy = types.ObjectNull(policyTypes(projectPolicyFields))
	if ds := plan.Set(ctx, m); ds.HasError() {
		t.Fatal(ds)
	}
	updated := resource.UpdateResponse{State: read.State}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: read.State}, &updated)
	if updated.Diagnostics.HasError() {
		t.Fatal(updated.Diagnostics)
	}
	if remote["env"] == nil || len(remote["env"].(map[string]interface{})) != 0 || remote["description"] != "" || remote["color"] != nil || remote["targetDate"] != nil || remote["icon"] != nil || remote["archivedAt"] != nil || remote["executionWorkspacePolicy"] != nil || len(remote["goalIds"].([]interface{})) != 0 || writes != 3 {
		t.Fatalf("native fields did not clear: %v writes=%d", remote, writes)
	}
}

func TestProjectArchiveRetirementWritesOnlyArchiveTimestamp(t *testing.T) {
	for _, status := range []int{200, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method != "PATCH" || req.URL.Path != "/api/projects/project" {
					t.Errorf("wrong retirement route: %s %s", req.Method, req.URL.Path)
				}
				var body map[string]interface{}
				_ = json.NewDecoder(req.Body).Decode(&body)
				stamp, ok := body["archivedAt"].(string)
				if _, err := time.Parse(time.RFC3339Nano, stamp); !ok || err != nil || len(body) != 1 {
					t.Errorf("invalid archive payload: %v", body)
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{}`))
			}))
			defer server.Close()
			r := NewPaperclipProjectResource().(*PaperclipProjectResource)
			s := configureTestResource(t, server.URL, r)
			m := PaperclipProjectResourceModel{ID: types.StringValue("project"), GoalIDs: types.SetNull(types.StringType), ExecutionWorkspacePolicy: types.ObjectNull(policyTypes(projectPolicyFields)), ArchiveOnDestroy: types.BoolValue(true)}
			state := tfsdk.State{Schema: s}
			if ds := state.Set(context.Background(), m); ds.HasError() {
				t.Fatal(ds)
			}
			var response resource.DeleteResponse
			r.Delete(context.Background(), resource.DeleteRequest{State: state}, &response)
			if response.Diagnostics.HasError() != (status == 500) {
				t.Fatal(response.Diagnostics)
			}
		})
	}
}

func TestProjectReferencesRejectInvalidGoalsBeforeMutation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  int
		company string
	}{{"absent", 404, ""}, {"cross company", 200, "other"}, {"unreadable", 403, ""}} {
		t.Run(tc.name, func(t *testing.T) {
			writes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method != "GET" {
					writes++
					t.Errorf("invalid goal caused write: %s", req.Method)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(client.PaperclipGoal{ID: "goal", CompanyID: tc.company})
			}))
			defer server.Close()
			r := NewPaperclipProjectResource().(*PaperclipProjectResource)
			s := configureTestResource(t, server.URL, r)
			goals, _ := types.SetValueFrom(context.Background(), types.StringType, []string{"goal"})
			m := PaperclipProjectResourceModel{ID: types.StringValue("project"), CompanyID: types.StringValue("company"), Name: types.StringValue("project"), GoalIDs: goals, ExecutionWorkspacePolicy: types.ObjectNull(policyTypes(projectPolicyFields))}
			plan := tfsdk.Plan{Schema: s}
			if ds := plan.Set(context.Background(), m); ds.HasError() {
				t.Fatal(ds)
			}
			state := tfsdk.State{Schema: s}
			if ds := state.Set(context.Background(), m); ds.HasError() {
				t.Fatal(ds)
			}
			var modify resource.ModifyPlanResponse
			r.ModifyPlan(context.Background(), resource.ModifyPlanRequest{Plan: plan}, &modify)
			if !modify.Diagnostics.HasError() {
				t.Fatal("invalid goal accepted at plan")
			}
			created := resource.CreateResponse{State: state}
			r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &created)
			updated := resource.UpdateResponse{State: state}
			r.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &updated)
			if !created.Diagnostics.HasError() || !updated.Diagnostics.HasError() || writes != 0 || !updated.State.Raw.Equal(state.Raw) {
				t.Fatalf("goal rejection failed: %v / %v writes=%d", created.Diagnostics, updated.Diagnostics, writes)
			}
		})
	}
	r := &PaperclipProjectResource{}
	unknown, _ := types.SetValue(types.StringType, []attr.Value{types.StringUnknown()})
	if ds := r.validateReferences(context.Background(), PaperclipProjectResourceModel{GoalIDs: unknown}); ds.HasError() {
		t.Fatal("unknown goal rejected before apply", ds)
	}
}

func TestProjectPolicyAndTimestampUnsupportedValues(t *testing.T) {
	missing := testProjectPolicy(t, map[string]interface{}{})
	if !validatePolicyObject(missing, projectPolicyFields).HasError() {
		t.Fatal("present policy block without enabled accepted")
	}
	v := projectPolicyObjectValidator{fields: projectPolicyFields}
	if v.Description(context.Background()) == "" || v.MarkdownDescription(context.Background()) == "" {
		t.Fatal("missing conditional policy contract")
	}
	var objectResponse validator.ObjectResponse
	v.ValidateObject(context.Background(), validator.ObjectRequest{ConfigValue: missing}, &objectResponse)
	if !objectResponse.Diagnostics.HasError() {
		t.Fatal("missing enabled not rejected by schema validator")
	}
	for _, name := range []string{"", " project "} {
		if !validateProjectConfig(PaperclipProjectResourceModel{Name: types.StringValue(name), ExecutionWorkspacePolicy: types.ObjectNull(policyTypes(projectPolicyFields))}).HasError() {
			t.Fatalf("unrepresentable name %q accepted", name)
		}
	}
	for _, stamp := range []string{"2026-10-15", "2026-10-15T00:00:00+01:00"} {
		m := PaperclipProjectResourceModel{ArchivedAt: types.StringValue(stamp), ExecutionWorkspacePolicy: types.ObjectNull(policyTypes(projectPolicyFields))}
		if !validateProjectConfig(m).HasError() {
			t.Fatalf("invalid archive date %q accepted", stamp)
		}
	}
	for _, remote := range []map[string]interface{}{
		{"enabled": "yes"}, {"workspaceStrategy": false}, {"defaultMode": true}, {"enabled": true, "authorizationPolicy": map[string]interface{}{"trustPreset": "standard"}}, {"unsupported": true}, {"cleanupPolicy": "invalid"},
	} {
		if _, ds := policyFromAPI(projectPolicyFields, remote); !ds.HasError() {
			t.Fatalf("invalid native policy accepted: %v", remote)
		}
	}
	managed := testProjectPolicy(t, map[string]interface{}{"enabled": true, "authorizationPolicy": map[string]interface{}{"managedBy": map[string]interface{}{"owner": "config"}}})
	if !validatePolicyObject(managed, projectPolicyFields).HasError() {
		t.Fatal("unevaluable managedBy-only authorization accepted")
	}
	unknown := types.ObjectUnknown(policyTypes(projectPolicyFields))
	if ds := validatePolicyObject(unknown, projectPolicyFields); ds.HasError() {
		t.Fatal(ds)
	}
	if policyToAPI(unknown, projectPolicyFields) != nil {
		t.Fatal("unknown policy materialized")
	}
}

func TestProjectLegacyInvalidPolicyUpgradeFailsWithoutWrites(t *testing.T) {
	ctx := context.Background()
	server := providerserver.NewProtocol6(New("test")())()
	for _, policy := range []string{`5`, `"broken"`, `"null"`, `"{\"authorizationPolicy\":{\"trustPreset\":\"standard\"}}"`} {
		raw := `{"id":"project","company_id":"company","name":"project","execution_workspace_policy":` + policy + `}`
		result, err := server.UpgradeResourceState(ctx, &tfprotov6.UpgradeResourceStateRequest{TypeName: "paperclip_project", Version: 1, RawState: &tfprotov6.RawState{JSON: []byte(raw)}})
		if err != nil || len(result.Diagnostics) == 0 || result.UpgradedState != nil {
			t.Fatalf("invalid legacy policy upgraded: %s / %v / %v", policy, err, result)
		}
	}
}

func TestProjectEnvironmentReadIncludesNativePlainAndUserSecretDefaults(t *testing.T) {
	got := refreshProjectEnv(nil, map[string]interface{}{
		"REGION":     "ci",
		"USER_TOKEN": map[string]interface{}{"type": "user_secret_ref", "key": "user.token", "required": false, "allowMissingOverride": true},
	})
	if got["REGION"].Type.ValueString() != "plain" || got["REGION"].Value.ValueString() != "ci" || got["USER_TOKEN"].Required.ValueBool() || !got["USER_TOKEN"].AllowMissingOverride.ValueBool() {
		t.Fatalf("native environment read lost bindings or flags: %+v", got)
	}
}

func TestWorkspaceNativeWriteFailuresKeepExistingIdentity(t *testing.T) {
	ctx := context.Background()
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { writes++; http.Error(w, "write rejected", 500) }))
	defer server.Close()
	r := NewPaperclipWorkspaceResource().(*PaperclipWorkspaceResource)
	s := configureTestResource(t, server.URL, r)
	m := PaperclipWorkspaceResourceModel{ID: types.StringValue("existing"), ProjectID: types.StringValue("project"), Name: types.StringValue("workspace"), Cwd: types.StringValue("/work"), IsPrimary: types.BoolValue(true)}
	plan := tfsdk.Plan{Schema: s}
	if ds := plan.Set(ctx, m); ds.HasError() {
		t.Fatal(ds)
	}
	state := tfsdk.State{Schema: s}
	if ds := state.Set(ctx, m); ds.HasError() {
		t.Fatal(ds)
	}
	created := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
	updated := resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &updated)
	if !created.Diagnostics.HasError() || !updated.Diagnostics.HasError() || !updated.State.Raw.Equal(state.Raw) || writes != 2 {
		t.Fatalf("native write error not preserved: %v / %v writes=%d", created.Diagnostics, updated.Diagnostics, writes)
	}
}

func TestProjectUnsupportedNativePolicyReadPreservesIdentity(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "project", "companyId": "company", "name": "project", "status": "backlog", "executionWorkspacePolicy": map[string]interface{}{"enabled": true, "unknown": true}})
	}))
	defer server.Close()
	r := NewPaperclipProjectResource().(*PaperclipProjectResource)
	s := configureTestResource(t, server.URL, r)
	m := PaperclipProjectResourceModel{ID: types.StringValue("project"), CompanyID: types.StringValue("company"), Name: types.StringValue("project"), Status: types.StringValue("backlog"), GoalIDs: types.SetNull(types.StringType), ExecutionWorkspacePolicy: types.ObjectNull(policyTypes(projectPolicyFields))}
	state := tfsdk.State{Schema: s}
	if ds := state.Set(ctx, m); ds.HasError() {
		t.Fatal(ds)
	}
	plan := tfsdk.Plan{Schema: s}
	if ds := plan.Set(ctx, m); ds.HasError() {
		t.Fatal(ds)
	}
	read := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &read)
	updated := resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &updated)
	if !read.Diagnostics.HasError() || !read.State.Raw.Equal(state.Raw) || !updated.Diagnostics.HasError() || !updated.State.Raw.Equal(state.Raw) {
		t.Fatalf("unrepresentable native policy removed identity: %v / %v", read.Diagnostics, updated.Diagnostics)
	}
	created := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
	if !created.Diagnostics.HasError() {
		t.Fatal("unsupported response accepted")
	}
	var id types.String
	if ds := created.State.GetAttribute(ctx, path.Root("id"), &id); ds.HasError() || id.ValueString() != "project" {
		t.Fatalf("successful create lost its remote identity after policy diagnostic: %v / %v", id, ds)
	}
}

func TestProjectArchiveCreationAndPrecisionRejectBeforeWrites(t *testing.T) {
	ctx := context.Background()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { calls++; t.Error("unsupported archival contacted API") }))
	defer server.Close()
	r := NewPaperclipProjectResource().(*PaperclipProjectResource)
	s := configureTestResource(t, server.URL, r)
	m := PaperclipProjectResourceModel{Name: types.StringValue("project"), CompanyID: types.StringValue("company"), GoalIDs: types.SetNull(types.StringType), ExecutionWorkspacePolicy: types.ObjectNull(policyTypes(projectPolicyFields)), ArchivedAt: types.StringValue("2026-10-15T00:00:00Z")}
	plan := tfsdk.Plan{Schema: s}
	if ds := plan.Set(ctx, m); ds.HasError() {
		t.Fatal(ds)
	}
	var modify resource.ModifyPlanResponse
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan}, &modify)
	response := resource.CreateResponse{State: tfsdk.State{Schema: s}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &response)
	if !modify.Diagnostics.HasError() || !response.Diagnostics.HasError() || calls != 0 {
		t.Fatalf("archive create was accepted: %v / %v", modify.Diagnostics, response.Diagnostics)
	}
	m.ArchivedAt = types.StringValue("2026-10-15T00:00:00.123456Z")
	if !validateProjectConfig(m).HasError() {
		t.Fatal("sub-millisecond timestamp normalization hidden")
	}
	m.ArchivedAt = types.StringNull()
	m.TargetDate = types.StringValue("2026-10-15T12:00:00Z")
	if !validateProjectConfig(m).HasError() {
		t.Fatal("normalized date-time accepted as calendar date")
	}
	if err := validateProjectCreateArchive(types.StringUnknown()); err != nil {
		t.Fatal("unknown creation timestamp rejected before resolution", err)
	}
}

func TestProjectTimestampSemanticPlan(t *testing.T) {
	m := projectArchiveTimePlanModifier{}
	ctx := context.Background()
	if m.Description(ctx) == "" || m.MarkdownDescription(ctx) == "" {
		t.Fatal("missing timestamp contract")
	}
	for _, tc := range []struct {
		name        string
		plan, state types.String
		equal       bool
	}{
		{"same instant", types.StringValue("2026-10-15T00:00:00Z"), types.StringValue("2026-10-15T00:00:00.000Z"), true},
		{"different", types.StringValue("2026-10-16T00:00:00Z"), types.StringValue("2026-10-15T00:00:00.000Z"), false},
		{"invalid", types.StringValue("invalid"), types.StringValue("invalid"), false},
		{"null", types.StringNull(), types.StringNull(), false},
		{"unknown", types.StringUnknown(), types.StringValue("2026-10-15T00:00:00Z"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := planmodifier.StringResponse{PlanValue: tc.plan}
			m.PlanModifyString(ctx, planmodifier.StringRequest{PlanValue: tc.plan, StateValue: tc.state}, &response)
			want := tc.plan
			if tc.equal {
				want = tc.state
			}
			if !response.PlanValue.Equal(want) {
				t.Fatal(response.PlanValue)
			}
		})
	}
}

func TestProjectPolicyReferencesAreReadOnlyAndNative(t *testing.T) {
	for _, tc := range []struct {
		name, field, id, body string
		status                int
		projectID             string
		invalid               bool
	}{
		{name: "local", field: "environmentId", id: "environment", body: `{"id":"environment","driver":"local","status":"ready"}`, status: 200},
		{name: "ssh", field: "environmentId", id: "environment", body: `{"id":"environment","driver":"ssh","status":"ready"}`, status: 200},
		{name: "sandbox", field: "environmentId", id: "environment", body: `{"id":"environment","driver":"sandbox","config":{"provider":"cloud"}}`, status: 200},
		{name: "archived environment", field: "environmentId", id: "environment", body: `{"id":"environment","driver":"local","status":"archived"}`, status: 200, invalid: true},
		{name: "fake sandbox", field: "environmentId", id: "environment", body: `{"id":"environment","driver":"sandbox","config":{"provider":"fake"}}`, status: 200, invalid: true},
		{name: "invalid driver", field: "environmentId", id: "environment", body: `{"id":"environment","driver":"unknown"}`, status: 200, invalid: true},
		{name: "missing environment", field: "environmentId", id: "environment", body: `{"error":"Environment not found"}`, status: 404, invalid: true},
		{name: "wrong environment", field: "environmentId", id: "environment", body: `{"id":"other","driver":"local"}`, status: 200, invalid: true},
		{name: "existing workspace", field: "defaultProjectWorkspaceId", id: "workspace", projectID: "project", body: `[{"id":"workspace","projectId":"project"}]`, status: 200},
		{name: "missing workspace", field: "defaultProjectWorkspaceId", id: "workspace", projectID: "project", body: `[]`, status: 200, invalid: true},
		{name: "workspace on create", field: "defaultProjectWorkspaceId", id: "workspace", body: `[]`, status: 200, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method != "GET" {
					t.Errorf("reference validation mutated API: %s", req.Method)
				}
				if tc.field == "environmentId" && req.URL.Path != "/api/environments/environment" {
					t.Error(req.URL.Path)
				}
				if tc.field == "defaultProjectWorkspaceId" && req.URL.Path != "/api/projects/project/workspaces" {
					t.Error(req.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			r := NewPaperclipProjectResource().(*PaperclipProjectResource)
			_ = configureTestResource(t, server.URL, r)
			model := PaperclipProjectResourceModel{ID: stringOrNull(tc.projectID), ExecutionWorkspacePolicy: testProjectPolicy(t, map[string]interface{}{"enabled": true, tc.field: tc.id})}
			ds := r.validateReferences(context.Background(), model)
			if ds.HasError() != tc.invalid {
				t.Fatalf("reference result: %v", ds)
			}
		})
	}
}

func TestProjectSharedEnvVersionsUseNativeIntegerWireValues(t *testing.T) {
	for _, tc := range []struct {
		version string
		invalid bool
	}{{"latest", false}, {"2", false}, {"0", true}, {"01", true}, {"-1", true}, {"wrong", true}, {"9007199254740992", true}} {
		t.Run(tc.version, func(t *testing.T) {
			binding := EnvBindingModel{Type: types.StringValue("secret_ref"), SecretID: types.StringValue("secret"), Version: types.StringValue(tc.version)}
			ds := ValidateEnvBindingPlan(path.Root("env").AtMapKey("TOKEN"), binding, "project", "env.TOKEN")
			if ds.HasError() != tc.invalid {
				t.Fatal(ds)
			}
			if !tc.invalid {
				wire := EnvBindingsMapToAPI(map[string]EnvBindingModel{"TOKEN": binding})["TOKEN"].(map[string]interface{})
				if tc.version == "2" && wire["version"] != int64(2) {
					t.Fatalf("version sent as non-native string: %v", wire)
				}
				decoded := envBindingFromJSON(wire)
				if decoded.Version.ValueString() != tc.version {
					t.Fatal("version lost while refreshing binding", decoded)
				}
				raw, _ := json.Marshal(wire)
				var fromHTTP map[string]interface{}
				if err := json.Unmarshal(raw, &fromHTTP); err != nil {
					t.Fatal(err)
				}
				if envBindingFromJSON(fromHTTP).Version.ValueString() != tc.version {
					t.Fatal("JSON numeric version lost")
				}
			}
		})
	}
	binding := EnvBindingModel{Type: types.StringValue("plain"), Value: types.StringValue("ci"), Version: types.StringValue("latest")}
	if !ValidateEnvBindingPlan(path.Root("env"), binding, "project", "").HasError() {
		t.Fatal("plain binding silently ignored configured version")
	}
}

func TestSharedEnvRefreshConvergesDefaultsAndShowsPinnedVersionDrift(t *testing.T) {
	declared := map[string]EnvBindingModel{
		"REGION": {Type: types.StringValue("plain"), Value: types.StringValue("ci"), Version: types.StringUnknown(), ProjectionClass: types.StringUnknown()},
		"TOKEN":  {Type: types.StringValue("secret_ref"), SecretID: types.StringValue("secret"), Version: types.StringValue("1")},
	}
	got := refreshAgentEnvBindings(declared, map[string]interface{}{"env": map[string]interface{}{"REGION": map[string]interface{}{"type": "plain", "value": "ci"}, "TOKEN": map[string]interface{}{"type": "secret_ref", "secretId": "secret", "version": float64(2), "projectionClass": "unclassified"}}})
	if got["REGION"].Version.IsUnknown() || got["REGION"].ProjectionClass.IsUnknown() || got["TOKEN"].Version.ValueString() != "2" {
		t.Fatalf("shared env defaults or version drift hidden: %+v", got)
	}
}

func TestSharedEnvRejectsUnrepresentableFlagsKeysAndTypeBeforeWrite(t *testing.T) {
	p := path.Root("env")
	for _, binding := range []EnvBindingModel{
		{Type: types.StringValue("plain"), Value: types.StringValue("ci"), Required: types.BoolValue(false)},
		{Type: types.StringValue("user_secret_ref"), Key: types.StringValue(" key ")},
		{Type: types.StringValue(" plain "), Value: types.StringValue("ci")},
	} {
		if !ValidateEnvBindingPlan(p, binding, "project", "").HasError() {
			t.Fatalf("unrepresentable env accepted: %+v", binding)
		}
	}
	if !ValidateEnvBindingsMapPlan(p, map[string]EnvBindingModel{"INVALID-KEY": {Type: types.StringValue("plain"), Value: types.StringValue("ci")}}, "project").HasError() {
		t.Fatal("invalid native env key accepted")
	}
}

func TestWorkspaceNativeConfigurationSchema(t *testing.T) {
	var response resource.SchemaResponse
	NewPaperclipWorkspaceResource().Schema(context.Background(), resource.SchemaRequest{}, &response)
	for _, name := range []string{"cwd", "repo_ref", "default_ref", "visibility", "setup_command", "cleanup_command", "remote_provider", "remote_workspace_ref", "shared_workspace_key", "metadata", "runtime_config"} {
		if _, ok := response.Schema.Attributes[name]; !ok {
			t.Errorf("native workspace field %s cannot be configured", name)
		}
	}
	if _, ok := response.Schema.Attributes["branch"]; ok {
		t.Error("branch still couples independent native refs")
	}
}
