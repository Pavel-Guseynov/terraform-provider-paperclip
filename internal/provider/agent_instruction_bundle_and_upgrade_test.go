package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"git.guseynov.org/guseynov/paperclip/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestPaperclipAgentUpgradeStateV1ToV2(t *testing.T) {
	ctx := context.Background()
	r := NewPaperclipAgentResource().(*PaperclipAgentResource)
	upgraders := r.UpgradeState(ctx)
	u1, ok := upgraders[1]
	if !ok {
		t.Fatal("missing v1 state upgrader")
	}

	priorState := tfsdk.State{Schema: *u1.PriorSchema}
	_ = priorState.Set(ctx, &struct {
		ID                   types.String               `tfsdk:"id"`
		CompanyID            types.String               `tfsdk:"company_id"`
		Name                 types.String               `tfsdk:"name"`
		Role                 types.String               `tfsdk:"role"`
		Title                types.String               `tfsdk:"title"`
		Icon                 types.String               `tfsdk:"icon"`
		ReportsTo            types.String               `tfsdk:"reports_to"`
		Capabilities         types.String               `tfsdk:"capabilities"`
		DefaultEnvironmentID types.String               `tfsdk:"default_environment_id"`
		AdapterType          types.String               `tfsdk:"adapter_type"`
		Status               types.String               `tfsdk:"status"`
		RuntimeConfig        types.String               `tfsdk:"runtime_config"`
		AdapterConfig        types.String               `tfsdk:"adapter_config"`
		Metadata             types.String               `tfsdk:"metadata"`
		Instructions         types.String               `tfsdk:"instructions"`
		CanAssignTasks       types.Bool                 `tfsdk:"can_assign_tasks"`
		CanCreateAgents      types.Bool                 `tfsdk:"can_create_agents"`
		CanCreateSkills      types.Bool                 `tfsdk:"can_create_skills"`
		TrustPreset          types.String               `tfsdk:"trust_preset"`
		AuthorizationPolicy  types.String               `tfsdk:"authorization_policy"`
		Paused               types.Bool                 `tfsdk:"paused"`
		Retirement           types.String               `tfsdk:"retirement"`
		Env                  map[string]EnvBindingModel `tfsdk:"env"`
	}{
		ID:                   types.StringValue("ag-v1"),
		CompanyID:            types.StringValue("comp-1"),
		Name:                 types.StringValue("agent-1"),
		Role:                 types.StringValue("engineer"),
		Title:                types.StringValue("Staff"),
		Icon:                 types.StringValue("robot"),
		ReportsTo:            types.StringValue("lead-1"),
		Capabilities:         types.StringValue("coding"),
		DefaultEnvironmentID: types.StringValue("env-1"),
		AdapterType:          types.StringValue("antigravity_local"),
		Status:               types.StringValue("active"),
		RuntimeConfig:        types.StringValue(`{}`),
		AdapterConfig:        types.StringValue(`{}`),
		Metadata:             types.StringValue(`{}`),
		Instructions:         types.StringValue("legacy instructions to be dropped"),
		CanAssignTasks:       types.BoolValue(true),
		CanCreateAgents:      types.BoolValue(false),
		CanCreateSkills:      types.BoolValue(true),
		TrustPreset:          types.StringValue("standard"),
		AuthorizationPolicy:  types.StringNull(),
		Paused:               types.BoolValue(false),
		Retirement:           types.StringValue("delete"),
		Env: map[string]EnvBindingModel{
			"VAR1": {
				Type:  types.StringValue("plain"),
				Value: types.StringValue("val1"),
			},
		},
	})

	var sResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sResp)

	var upgResp resource.UpgradeStateResponse
	upgResp.State = tfsdk.State{Schema: sResp.Schema}
	u1.StateUpgrader(ctx, resource.UpgradeStateRequest{State: &priorState}, &upgResp)
	if upgResp.Diagnostics.HasError() {
		t.Fatalf("unexpected upgrader error: %v", upgResp.Diagnostics)
	}

	var upgraded PaperclipAgentResourceModel
	_ = upgResp.State.Get(ctx, &upgraded)
	if upgraded.ID.ValueString() != "ag-v1" ||
		upgraded.Name.ValueString() != "agent-1" ||
		upgraded.Title.ValueString() != "Staff" ||
		upgraded.Icon.ValueString() != "robot" ||
		upgraded.ReportsTo.ValueString() != "lead-1" ||
		upgraded.Capabilities.ValueString() != "coding" ||
		upgraded.DefaultEnvironmentID.ValueString() != "env-1" ||
		upgraded.AdapterType.ValueString() != "antigravity_local" ||
		!upgraded.CanAssignTasks.ValueBool() ||
		upgraded.CanCreateAgents.ValueBool() ||
		!upgraded.CanCreateSkills.ValueBool() ||
		upgraded.TrustPreset.ValueString() != "standard" ||
		upgraded.Paused.ValueBool() ||
		upgraded.Retirement.ValueString() != "delete" ||
		upgraded.Env["VAR1"].Value.ValueString() != "val1" {
		t.Fatalf("unexpected upgraded state: %+v", upgraded)
	}

	// Error path
	badState := tfsdk.State{
		Schema: *u1.PriorSchema,
		Raw:    tftypes.NewValue(tftypes.String, "bad"),
	}
	var badResp resource.UpgradeStateResponse
	badResp.State = tfsdk.State{Schema: sResp.Schema}
	u1.StateUpgrader(ctx, resource.UpgradeStateRequest{State: &badState}, &badResp)
	if !badResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid state in v1 UpgradeState")
	}
}

func TestPaperclipAgentInstructionBundleResourceLifecycle(t *testing.T) {
	ctx := context.Background()

	// In-memory server state
	type bundleState struct {
		mode      string
		rootPath  *string
		entryFile string
		files     map[string]string
		virtuals  map[string]string
	}

	state := &bundleState{
		mode:      "managed",
		entryFile: "AGENTS.md",
		files: map[string]string{
			"AGENTS.md": "# Original instructions",
			"extra.md":  "# Extra file",
		},
		virtuals: map[string]string{
			"promptTemplate.legacy.md": "legacy prompt",
		},
	}

	failPatch := false
	failFilePut := false
	failFileDelete := false
	failGet := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/agents/ag-1/instructions-bundle":
			if failGet {
				http.Error(w, "server failure", http.StatusInternalServerError)
				return
			}
			var summaries []client.AgentInstructionsFileSummary
			for p, c := range state.files {
				summaries = append(summaries, client.AgentInstructionsFileSummary{
					Path:        p,
					Size:        int64(len(c)),
					Language:    "markdown",
					Markdown:    true,
					IsEntryFile: p == state.entryFile,
					Editable:    true,
					Virtual:     false,
				})
			}
			for p, c := range state.virtuals {
				summaries = append(summaries, client.AgentInstructionsFileSummary{
					Path:        p,
					Size:        int64(len(c)),
					Language:    "markdown",
					Markdown:    true,
					IsEntryFile: false,
					Editable:    false,
					Virtual:     true,
				})
			}
			_ = json.NewEncoder(w).Encode(client.AgentInstructionsBundle{
				AgentID:   "ag-1",
				CompanyID: "comp-1",
				Mode:      state.mode,
				RootPath:  "/tmp/managed",
				EntryFile: state.entryFile,
				Editable:  true,
				Files:     summaries,
			})
		case req.Method == http.MethodPatch && req.URL.Path == "/api/agents/ag-1/instructions-bundle":
			if failPatch {
				http.Error(w, "permission denied: requires instance admin", http.StatusForbidden)
				return
			}
			var body map[string]interface{}
			_ = json.NewDecoder(req.Body).Decode(&body)
			if m, ok := body["mode"].(string); ok && m != "" {
				state.mode = m
			}
			if rp, ok := body["rootPath"].(string); ok {
				state.rootPath = &rp
			}
			if ef, ok := body["entryFile"].(string); ok && ef != "" {
				state.entryFile = ef
			}
			_ = json.NewEncoder(w).Encode(client.AgentInstructionsBundle{
				AgentID:   "ag-1",
				CompanyID: "comp-1",
				Mode:      state.mode,
				RootPath:  "/tmp/managed",
				EntryFile: state.entryFile,
				Editable:  true,
			})
		case req.Method == http.MethodPut && req.URL.Path == "/api/agents/ag-1/instructions-bundle/file":
			if failFilePut {
				http.Error(w, "write error", http.StatusInternalServerError)
				return
			}
			var body map[string]interface{}
			_ = json.NewDecoder(req.Body).Decode(&body)
			p := body["path"].(string)
			c := body["content"].(string)
			state.files[p] = c
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
		case req.Method == http.MethodGet && req.URL.Path == "/api/agents/ag-1/instructions-bundle/file":
			p := req.URL.Query().Get("path")
			if c, ok := state.files[p]; ok {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"content": c})
				return
			}
			if c, ok := state.virtuals[p]; ok {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"content": c})
				return
			}
			http.NotFound(w, req)
		case req.Method == http.MethodDelete && req.URL.Path == "/api/agents/ag-1/instructions-bundle/file":
			if failFileDelete {
				http.Error(w, "delete error", http.StatusInternalServerError)
				return
			}
			p := req.URL.Query().Get("path")
			delete(state.files, p)
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, req)
		}
	}))
	defer srv.Close()

	cli, err := client.NewClient(srv.URL, "token")
	if err != nil {
		t.Fatal(err)
	}

	r := NewPaperclipAgentInstructionBundleResource().(*PaperclipAgentInstructionBundleResource)

	// Metadata
	var metaResp resource.MetadataResponse
	r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "paperclip"}, &metaResp)
	if metaResp.TypeName != "paperclip_agent_instruction_bundle" {
		t.Fatalf("unexpected type name: %s", metaResp.TypeName)
	}

	// Schema
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)
	if schemaResp.Schema.Version != 1 {
		t.Fatalf("unexpected schema version: %d", schemaResp.Schema.Version)
	}

	// Configure
	var confResp resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: nil}, &confResp)
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: "invalid"}, &confResp)
	if !confResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid configure type")
	}
	confResp = resource.ConfigureResponse{}
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: cli}, &confResp)
	if confResp.Diagnostics.HasError() {
		t.Fatalf("unexpected configure error: %v", confResp.Diagnostics)
	}

	bundleModel := func(agentID, mode, rootPath, entryFile string, files map[string]string) PaperclipAgentInstructionBundleResourceModel {
		m := PaperclipAgentInstructionBundleResourceModel{
			ID:                        stringOrNull(agentID),
			AgentID:                   stringOrNull(agentID),
			Mode:                      stringOrNull(mode),
			RootPath:                  stringOrNull(rootPath),
			EntryFile:                 stringOrNull(entryFile),
			ClearLegacyPromptTemplate: types.BoolValue(false),
			FileInventory:             types.ListNull(types.ObjectType{AttrTypes: instructionBundleFileInventoryAttrTypes}),
		}
		if files != nil {
			mFiles, _ := types.MapValueFrom(ctx, types.StringType, files)
			m.Files = mFiles
		} else {
			m.Files = types.MapNull(types.StringType)
		}
		return m
	}

	// ValidateConfig
	{
		var valResp resource.ValidateConfigResponse
		// managed valid
		mModel := bundleModel("ag-1", "managed", "", "AGENTS.md", nil)
		var planManaged tfsdk.Plan
		planManaged.Schema = schemaResp.Schema
		_ = planManaged.Set(ctx, &mModel)
		cfgManaged := tfsdk.Config{
			Schema: schemaResp.Schema,
			Raw:    planManaged.Raw,
		}
		r.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: cfgManaged}, &valResp)
		if valResp.Diagnostics.HasError() {
			t.Fatalf("unexpected validation error: %v", valResp.Diagnostics)
		}

		// external without root_path
		valResp = resource.ValidateConfigResponse{}
		extMissingModel := bundleModel("ag-1", "external", "", "AGENTS.md", nil)
		var planExtMissing tfsdk.Plan
		planExtMissing.Schema = schemaResp.Schema
		_ = planExtMissing.Set(ctx, &extMissingModel)
		cfgExtMissing := tfsdk.Config{
			Schema: schemaResp.Schema,
			Raw:    planExtMissing.Raw,
		}
		r.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: cfgExtMissing}, &valResp)
		if !valResp.Diagnostics.HasError() {
			t.Fatal("expected validation error for external mode without root_path")
		}

		// external with relative root_path
		valResp = resource.ValidateConfigResponse{}
		extRelModel := bundleModel("ag-1", "external", "relative/path", "AGENTS.md", nil)
		var planExtRel tfsdk.Plan
		planExtRel.Schema = schemaResp.Schema
		_ = planExtRel.Set(ctx, &extRelModel)
		cfgExtRel := tfsdk.Config{
			Schema: schemaResp.Schema,
			Raw:    planExtRel.Raw,
		}
		r.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: cfgExtRel}, &valResp)
		if !valResp.Diagnostics.HasError() {
			t.Fatal("expected validation error for relative root_path")
		}
	}

	// ModifyPlan
	{
		// Null plan (destroy)
		var mpResp resource.ModifyPlanResponse
		r.ModifyPlan(ctx, resource.ModifyPlanRequest{
			Plan: tfsdk.Plan{Raw: tftypes.NewValue(tftypes.String, nil)},
		}, &mpResp)
		if mpResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error on null plan: %v", mpResp.Diagnostics)
		}

		// Invalid entry_file path traversal
		mpResp = resource.ModifyPlanResponse{}
		planBadEntry := tfsdk.Plan{Schema: schemaResp.Schema}
		mBadEntry := bundleModel("ag-1", "managed", "", "../escape.md", nil)
		_ = planBadEntry.Set(ctx, &mBadEntry)
		r.ModifyPlan(ctx, resource.ModifyPlanRequest{
			Plan: planBadEntry,
		}, &mpResp)
		if !mpResp.Diagnostics.HasError() {
			t.Fatal("expected error on escaping entry_file")
		}

		// Missing entry_file in non-empty files map
		mpResp = resource.ModifyPlanResponse{}
		planNoEntry := tfsdk.Plan{Schema: schemaResp.Schema}
		mNoEntry := bundleModel("ag-1", "managed", "", "AGENTS.md", map[string]string{
			"other.md": "content",
		})
		_ = planNoEntry.Set(ctx, &mNoEntry)
		r.ModifyPlan(ctx, resource.ModifyPlanRequest{
			Plan: planNoEntry,
		}, &mpResp)
		if !mpResp.Diagnostics.HasError() {
			t.Fatal("expected error when entry_file is missing from declared files map")
		}

		// Declared file with traversal or virtual file
		mpResp = resource.ModifyPlanResponse{}
		planBadP := tfsdk.Plan{Schema: schemaResp.Schema}
		mBadP := bundleModel("ag-1", "managed", "", "AGENTS.md", map[string]string{
			"AGENTS.md":                "entry",
			"../bad.md":                "bad",
			"promptTemplate.legacy.md": "virtual",
		})
		_ = planBadP.Set(ctx, &mBadP)
		r.ModifyPlan(ctx, resource.ModifyPlanRequest{
			Plan: planBadP,
		}, &mpResp)
		if !mpResp.Diagnostics.HasError() {
			t.Fatal("expected error on path traversal and virtual file in files map")
		}

		// Declared files map with unknown content at plan time (GUS-849 regression)
		mpResp = resource.ModifyPlanResponse{}
		planUnknownFile := tfsdk.Plan{Schema: schemaResp.Schema}
		unknownFilesMap, _ := types.MapValue(types.StringType, map[string]attr.Value{
			"AGENTS.md": types.StringUnknown(),
		})
		mUnknownFile := bundleModel("ag-1", "managed", "", "AGENTS.md", nil)
		mUnknownFile.Files = unknownFilesMap
		_ = planUnknownFile.Set(ctx, &mUnknownFile)
		r.ModifyPlan(ctx, resource.ModifyPlanRequest{
			Plan: planUnknownFile,
		}, &mpResp)
		if mpResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error when files map contains unknown values at plan time: %v", mpResp.Diagnostics)
		}

		// Declared file with traversal or virtual file even if value is unknown
		mpResp = resource.ModifyPlanResponse{}
		planBadPUnknown := tfsdk.Plan{Schema: schemaResp.Schema}
		badFilesUnknown, _ := types.MapValue(types.StringType, map[string]attr.Value{
			"AGENTS.md":                types.StringUnknown(),
			"../bad.md":                types.StringUnknown(),
			"promptTemplate.legacy.md": types.StringUnknown(),
		})
		mBadPUnknown := bundleModel("ag-1", "managed", "", "AGENTS.md", nil)
		mBadPUnknown.Files = badFilesUnknown
		_ = planBadPUnknown.Set(ctx, &mBadPUnknown)
		r.ModifyPlan(ctx, resource.ModifyPlanRequest{
			Plan: planBadPUnknown,
		}, &mpResp)
		if !mpResp.Diagnostics.HasError() {
			t.Fatal("expected error on path traversal and virtual file even with unknown values")
		}

		// External mode without root_path in ModifyPlan
		mpResp = resource.ModifyPlanResponse{}
		planExtNoRoot := tfsdk.Plan{Schema: schemaResp.Schema}
		mExtNoRoot := bundleModel("ag-1", "external", "", "AGENTS.md", nil)
		_ = planExtNoRoot.Set(ctx, &mExtNoRoot)
		r.ModifyPlan(ctx, resource.ModifyPlanRequest{
			Plan: planExtNoRoot,
		}, &mpResp)
		if !mpResp.Diagnostics.HasError() {
			t.Fatal("expected error on external mode without root_path in ModifyPlan")
		}

		// Initial creation defaults files to entryFile: ""
		planInit := tfsdk.Plan{Schema: schemaResp.Schema}
		mInit := bundleModel("ag-1", "managed", "", "AGENTS.md", nil)
		_ = planInit.Set(ctx, &mInit)
		mpResp = resource.ModifyPlanResponse{Plan: planInit}
		r.ModifyPlan(ctx, resource.ModifyPlanRequest{
			Plan:  planInit,
			State: tfsdk.State{Raw: tftypes.NewValue(tftypes.String, nil)},
		}, &mpResp)
		if mpResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error on plan files initialization: %v", mpResp.Diagnostics)
		}
	}

	// Create Error Branches
	{
		// Unconfigured client
		unconfR := &PaperclipAgentInstructionBundleResource{}
		var createResp resource.CreateResponse
		unconfR.Create(ctx, resource.CreateRequest{}, &createResp)
		if !createResp.Diagnostics.HasError() {
			t.Fatal("expected unconfigured client error")
		}

		// UpdateAgentInstructionsBundle failure (403 forbidden)
		failPatch = true
		planCreate := tfsdk.Plan{Schema: schemaResp.Schema}
		mCreate := bundleModel("ag-1", "managed", "", "AGENTS.md", map[string]string{
			"AGENTS.md": "# Desired Entry",
			"new.md":    "# New file",
		})
		_ = planCreate.Set(ctx, &mCreate)
		createResp = resource.CreateResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
		r.Create(ctx, resource.CreateRequest{Plan: planCreate}, &createResp)
		if !createResp.Diagnostics.HasError() {
			t.Fatal("expected error on failed patch in Create")
		}
		failPatch = false

		// File write error
		failFilePut = true
		createResp = resource.CreateResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
		r.Create(ctx, resource.CreateRequest{Plan: planCreate}, &createResp)
		if !createResp.Diagnostics.HasError() {
			t.Fatal("expected error on failed file write in Create")
		}
		failFilePut = false

		// Delete error during undeclared file cleanup
		failFileDelete = true
		createResp = resource.CreateResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
		r.Create(ctx, resource.CreateRequest{Plan: planCreate}, &createResp)
		if !createResp.Diagnostics.HasError() {
			t.Fatal("expected error on failed file delete in Create")
		}
		failFileDelete = false
	}

	// Create Success Path
	var createdState tfsdk.State
	{
		planCreate := tfsdk.Plan{Schema: schemaResp.Schema}
		mCreate := bundleModel("ag-1", "managed", "", "AGENTS.md", map[string]string{
			"AGENTS.md": "# Desired Entry",
			"new.md":    "# New file",
		})
		_ = planCreate.Set(ctx, &mCreate)
		var createResp resource.CreateResponse
		createResp.State = tfsdk.State{Schema: schemaResp.Schema}
		r.Create(ctx, resource.CreateRequest{Plan: planCreate}, &createResp)
		if createResp.Diagnostics.HasError() {
			t.Fatalf("unexpected Create error: %v", createResp.Diagnostics)
		}
		createdState = createResp.State

		var m PaperclipAgentInstructionBundleResourceModel
		_ = createdState.Get(ctx, &m)
		if m.ID.ValueString() != "ag-1" || m.AgentID.ValueString() != "ag-1" || m.Mode.ValueString() != "managed" {
			t.Fatalf("unexpected state after create: %+v", m)
		}
		if _, ok := state.files["extra.md"]; ok {
			t.Fatal("expected extra.md to be deleted because it was not declared in desired files")
		}
		if _, ok := state.virtuals["promptTemplate.legacy.md"]; !ok {
			t.Fatal("expected virtual promptTemplate.legacy.md to be preserved")
		}
	}

	// Read Success Path and Drift Detection
	{
		// Unconfigured client
		unconfR := &PaperclipAgentInstructionBundleResource{}
		var readResp resource.ReadResponse
		unconfR.Read(ctx, resource.ReadRequest{}, &readResp)
		if !readResp.Diagnostics.HasError() {
			t.Fatal("expected unconfigured client error in Read")
		}

		// Read normal
		readResp = resource.ReadResponse{State: createdState}
		r.Read(ctx, resource.ReadRequest{State: createdState}, &readResp)
		if readResp.Diagnostics.HasError() {
			t.Fatalf("unexpected read error: %v", readResp.Diagnostics)
		}

		// Out-of-band edit to file shows as drift
		state.files["new.md"] = "# Tampered out-of-band content"
		readResp = resource.ReadResponse{State: createdState}
		r.Read(ctx, resource.ReadRequest{State: createdState}, &readResp)
		if readResp.Diagnostics.HasError() {
			t.Fatalf("unexpected read error: %v", readResp.Diagnostics)
		}
		var drifted PaperclipAgentInstructionBundleResourceModel
		_ = readResp.State.Get(ctx, &drifted)
		var driftedMap map[string]string
		_ = drifted.Files.ElementsAs(ctx, &driftedMap, false)
		if driftedMap["new.md"] != "# Tampered out-of-band content" {
			t.Fatalf("expected drift to be reflected in state, got %s", driftedMap["new.md"])
		}

		// Read 404 removes resource
		read404State := tfsdk.State{Schema: schemaResp.Schema}
		m404 := bundleModel("ag-nonexistent", "managed", "", "AGENTS.md", nil)
		_ = read404State.Set(ctx, &m404)
		readResp = resource.ReadResponse{State: read404State}
		r.Read(ctx, resource.ReadRequest{State: read404State}, &readResp)
		if readResp.Diagnostics.HasError() {
			t.Fatalf("unexpected read error: %v", readResp.Diagnostics)
		}
		var dummy PaperclipAgentInstructionBundleResourceModel
		_ = readResp.State.Get(ctx, &dummy)
		if !dummy.ID.IsNull() {
			t.Fatal("expected resource to be removed from state on 404")
		}

		// Read 500 reports error
		failGet = true
		readResp = resource.ReadResponse{State: createdState}
		r.Read(ctx, resource.ReadRequest{State: createdState}, &readResp)
		if !readResp.Diagnostics.HasError() {
			t.Fatal("expected error on 500 in Read")
		}
		failGet = false
	}

	// Update
	{
		// Unconfigured client
		unconfR := &PaperclipAgentInstructionBundleResource{}
		var updResp resource.UpdateResponse
		unconfR.Update(ctx, resource.UpdateRequest{}, &updResp)
		if !updResp.Diagnostics.HasError() {
			t.Fatal("expected unconfigured client error in Update")
		}

		// Update: remove new.md, change AGENTS.md, switch entry_file
		planUpd := tfsdk.Plan{Schema: schemaResp.Schema}
		mUpd := bundleModel("ag-1", "managed", "", "CUSTOM.md", map[string]string{
			"CUSTOM.md": "# Custom Entry",
		})
		_ = planUpd.Set(ctx, &mUpd)
		updResp = resource.UpdateResponse{State: createdState}
		r.Update(ctx, resource.UpdateRequest{Plan: planUpd, State: createdState}, &updResp)
		if updResp.Diagnostics.HasError() {
			t.Fatalf("unexpected update error: %v", updResp.Diagnostics)
		}
		if _, ok := state.files["new.md"]; ok {
			t.Fatal("expected new.md to be deleted on update")
		}
		if state.entryFile != "CUSTOM.md" {
			t.Fatalf("expected entryFile to be CUSTOM.md, got %s", state.entryFile)
		}

		// Update failure paths
		failPatch = true
		updResp = resource.UpdateResponse{State: createdState}
		r.Update(ctx, resource.UpdateRequest{Plan: planUpd, State: createdState}, &updResp)
		if !updResp.Diagnostics.HasError() {
			t.Fatal("expected error on failed patch in Update")
		}
		failPatch = false

		failFilePut = true
		updResp = resource.UpdateResponse{State: createdState}
		r.Update(ctx, resource.UpdateRequest{Plan: planUpd, State: createdState}, &updResp)
		if !updResp.Diagnostics.HasError() {
			t.Fatal("expected error on failed file put in Update")
		}
		failFilePut = false

		failFileDelete = true
		state.files["undeclared.md"] = "undel"
		updResp = resource.UpdateResponse{State: createdState}
		r.Update(ctx, resource.UpdateRequest{Plan: planUpd, State: createdState}, &updResp)
		if !updResp.Diagnostics.HasError() {
			t.Fatal("expected error on failed file delete in Update")
		}
		failFileDelete = false
		delete(state.files, "undeclared.md")
	}

	// Delete
	{
		// Unconfigured client
		unconfR := &PaperclipAgentInstructionBundleResource{}
		var delResp resource.DeleteResponse
		unconfR.Delete(ctx, resource.DeleteRequest{}, &delResp)
		if !delResp.Diagnostics.HasError() {
			t.Fatal("expected unconfigured client error in Delete")
		}

		// Success delete
		delResp = resource.DeleteResponse{}
		state.files["other.md"] = "other"
		r.Delete(ctx, resource.DeleteRequest{State: createdState}, &delResp)
		if delResp.Diagnostics.HasError() {
			t.Fatalf("unexpected delete error: %v", delResp.Diagnostics)
		}
		if _, ok := state.files["other.md"]; ok {
			t.Fatal("expected other.md to be deleted on destroy")
		}
		if state.files[state.entryFile] != "" {
			t.Fatal("expected entryFile content to be cleared on destroy")
		}
	}

	// ImportState
	{
		// Unconfigured client
		unconfR := &PaperclipAgentInstructionBundleResource{}
		var impResp resource.ImportStateResponse
		unconfR.ImportState(ctx, resource.ImportStateRequest{ID: "ag-1"}, &impResp)
		if !impResp.Diagnostics.HasError() {
			t.Fatal("expected unconfigured client error in ImportState")
		}

		// Empty ID error
		impResp = resource.ImportStateResponse{}
		r.ImportState(ctx, resource.ImportStateRequest{ID: ""}, &impResp)
		if !impResp.Diagnostics.HasError() {
			t.Fatal("expected error on empty import ID")
		}

		// 404 error
		impResp = resource.ImportStateResponse{}
		r.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/ag-nonexistent"}, &impResp)
		if !impResp.Diagnostics.HasError() {
			t.Fatal("expected error on importing nonexistent agent")
		}

		// Success import with <company_id>/<agent_id>
		state.files["CUSTOM.md"] = "# Imported Content"
		impResp = resource.ImportStateResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
		r.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/ag-1"}, &impResp)
		if impResp.Diagnostics.HasError() {
			t.Fatalf("unexpected import error: %v", impResp.Diagnostics)
		}
		var imported PaperclipAgentInstructionBundleResourceModel
		_ = impResp.State.Get(ctx, &imported)
		if imported.ID.ValueString() != "ag-1" || imported.AgentID.ValueString() != "ag-1" {
			t.Fatalf("unexpected imported model: %+v", imported)
		}
	}
}

func TestPaperclipAgentInstructionBundleCoverageExhaustive(t *testing.T) {
	ctx := context.Background()
	r := NewPaperclipAgentInstructionBundleResource().(*PaperclipAgentInstructionBundleResource)
	var sResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sResp)

	// 1. ValidateConfig with bad raw config
	{
		badVal := tftypes.NewValue(tftypes.Object{
			AttributeTypes: map[string]tftypes.Type{
				"mode": tftypes.Number,
			},
		}, map[string]tftypes.Value{
			"mode": tftypes.NewValue(tftypes.Number, 123),
		})
		badCfg := tfsdk.Config{
			Schema: sResp.Schema,
			Raw:    badVal,
		}
		var valResp resource.ValidateConfigResponse
		r.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: badCfg}, &valResp)
		if !valResp.Diagnostics.HasError() {
			t.Fatal("expected error in ValidateConfig with bad raw config")
		}
	}

	// 2. ModifyPlan
	{
		// Bad raw plan
		badPlan := tfsdk.Plan{
			Schema: sResp.Schema,
			Raw:    tftypes.NewValue(tftypes.String, "invalid"),
		}
		var modResp resource.ModifyPlanResponse
		r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: badPlan}, &modResp)
		if !modResp.Diagnostics.HasError() {
			t.Fatal("expected error in ModifyPlan with bad raw plan")
		}

		// External mode with relative root_path
		mRel := PaperclipAgentInstructionBundleResourceModel{
			ID:                        types.StringNull(),
			AgentID:                   types.StringValue("ag-1"),
			Mode:                      types.StringValue("external"),
			RootPath:                  types.StringValue("relative/path"),
			EntryFile:                 types.StringValue("AGENTS.md"),
			ClearLegacyPromptTemplate: types.BoolValue(false),
			Files:                     types.MapNull(types.StringType),
			FileInventory:             types.ListNull(types.ObjectType{AttrTypes: instructionBundleFileInventoryAttrTypes}),
		}
		var relPlan tfsdk.Plan
		relPlan.Schema = sResp.Schema
		_ = relPlan.Set(ctx, &mRel)
		modResp = resource.ModifyPlanResponse{}
		r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: relPlan}, &modResp)
		if !modResp.Diagnostics.HasError() {
			t.Fatal("expected error in ModifyPlan with relative root_path")
		}
	}

	// 3. populateState edge cases
	{
		m := PaperclipAgentInstructionBundleResourceModel{
			AgentID:                   types.StringValue("ag-model"),
			ClearLegacyPromptTemplate: types.BoolNull(),
		}
		bundle := &client.AgentInstructionsBundle{
			AgentID:   "",
			Mode:      "",
			EntryFile: "",
			Files:     nil,
		}
		r.populateState(ctx, &m, bundle, nil)
		if m.ID.ValueString() != "ag-model" ||
			m.AgentID.ValueString() != "ag-model" ||
			m.Mode.ValueString() != "managed" ||
			m.EntryFile.ValueString() != "AGENTS.md" ||
			m.ClearLegacyPromptTemplate.ValueBool() != false ||
			!m.Files.IsNull() ||
			!m.FileInventory.IsNull() {
			t.Fatalf("unexpected populated model: %+v", m)
		}
	}

	// 4. Create edge cases
	{
		failSecondGet := false
		failFinalGet := false
		returnEntryFileAsUndeclared := false
		getCount := 0

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case req.Method == http.MethodPatch:
				_ = json.NewEncoder(w).Encode(client.AgentInstructionsBundle{
					AgentID:   "ag-create",
					Mode:      "external",
					RootPath:  "/tmp/ext",
					EntryFile: "AGENTS.md",
				})
			case req.Method == http.MethodPut:
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
			case req.Method == http.MethodGet && req.URL.Path == "/api/agents/ag-create/instructions-bundle":
				getCount++
				if failSecondGet && getCount == 1 {
					http.Error(w, "second get failure", http.StatusInternalServerError)
					return
				}
				if failFinalGet && getCount == 2 {
					http.Error(w, "final get failure", http.StatusInternalServerError)
					return
				}
				var files []client.AgentInstructionsFileSummary
				if returnEntryFileAsUndeclared {
					files = append(files, client.AgentInstructionsFileSummary{
						Path:        "AGENTS.md",
						IsEntryFile: true,
						Editable:    true,
						Virtual:     false,
					})
				}
				_ = json.NewEncoder(w).Encode(client.AgentInstructionsBundle{
					AgentID:   "ag-create",
					Mode:      "external",
					RootPath:  "/tmp/ext",
					EntryFile: "AGENTS.md",
					Files:     files,
				})
			default:
				http.NotFound(w, req)
			}
		}))
		defer srv.Close()

		cli, _ := client.NewClient(srv.URL, "token")
		crR := NewPaperclipAgentInstructionBundleResource().(*PaperclipAgentInstructionBundleResource)
		var confResp resource.ConfigureResponse
		crR.Configure(ctx, resource.ConfigureRequest{ProviderData: cli}, &confResp)

		// Create with bad plan
		badPlan := tfsdk.Plan{Schema: sResp.Schema, Raw: tftypes.NewValue(tftypes.String, "invalid")}
		var cResp resource.CreateResponse
		crR.Create(ctx, resource.CreateRequest{Plan: badPlan}, &cResp)
		if !cResp.Diagnostics.HasError() {
			t.Fatal("expected error in Create with bad plan")
		}

		// Create with external mode and null Files (desiredFiles == nil)
		extModel := PaperclipAgentInstructionBundleResourceModel{
			ID:                        types.StringNull(),
			AgentID:                   types.StringValue("ag-create"),
			Mode:                      types.StringValue("external"),
			RootPath:                  types.StringValue("/tmp/ext"),
			EntryFile:                 types.StringValue("AGENTS.md"),
			ClearLegacyPromptTemplate: types.BoolValue(true),
			Files:                     types.MapNull(types.StringType),
			FileInventory:             types.ListNull(types.ObjectType{AttrTypes: instructionBundleFileInventoryAttrTypes}),
		}
		var extPlan tfsdk.Plan
		extPlan.Schema = sResp.Schema
		_ = extPlan.Set(ctx, &extModel)

		cResp = resource.CreateResponse{State: tfsdk.State{Schema: sResp.Schema}}
		crR.Create(ctx, resource.CreateRequest{Plan: extPlan}, &cResp)
		if cResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error in Create external with null files: %v", cResp.Diagnostics)
		}

		extModelWithFiles := extModel
		extFiles, _ := types.MapValueFrom(ctx, types.StringType, map[string]string{"AGENTS.md": "content"})
		extModelWithFiles.Files = extFiles
		var extPlanWithFiles tfsdk.Plan
		extPlanWithFiles.Schema = sResp.Schema
		_ = extPlanWithFiles.Set(ctx, &extModelWithFiles)

		// Create: second Get error
		failSecondGet = true
		getCount = 0
		cResp = resource.CreateResponse{State: tfsdk.State{Schema: sResp.Schema}}
		crR.Create(ctx, resource.CreateRequest{Plan: extPlanWithFiles}, &cResp)
		if !cResp.Diagnostics.HasError() {
			t.Fatal("expected error on second Get failure in Create")
		}
		failSecondGet = false

		// Create: final Get error
		failFinalGet = true
		getCount = 0
		cResp = resource.CreateResponse{State: tfsdk.State{Schema: sResp.Schema}}
		crR.Create(ctx, resource.CreateRequest{Plan: extPlanWithFiles}, &cResp)
		if !cResp.Diagnostics.HasError() {
			t.Fatal("expected error on final Get failure in Create")
		}
		failFinalGet = false

		// Create: undeclared entry file removal rejection
		noEntryModel := PaperclipAgentInstructionBundleResourceModel{
			ID:                        types.StringNull(),
			AgentID:                   types.StringValue("ag-create"),
			Mode:                      types.StringValue("external"),
			RootPath:                  types.StringValue("/tmp/ext"),
			EntryFile:                 types.StringValue("AGENTS.md"),
			ClearLegacyPromptTemplate: types.BoolValue(false),
			FileInventory:             types.ListNull(types.ObjectType{AttrTypes: instructionBundleFileInventoryAttrTypes}),
		}
		noEntryFiles, _ := types.MapValueFrom(ctx, types.StringType, map[string]string{"other.md": "content"})
		noEntryModel.Files = noEntryFiles
		var noEntryPlan tfsdk.Plan
		noEntryPlan.Schema = sResp.Schema
		_ = noEntryPlan.Set(ctx, &noEntryModel)
		returnEntryFileAsUndeclared = true
		getCount = 0
		cResp = resource.CreateResponse{State: tfsdk.State{Schema: sResp.Schema}}
		crR.Create(ctx, resource.CreateRequest{Plan: noEntryPlan}, &cResp)
		if !cResp.Diagnostics.HasError() {
			t.Fatal("expected error on undeclared entry file removal in Create")
		}
		returnEntryFileAsUndeclared = false

		// Create: final Get error
		failFinalGet = true
		getCount = 0
		cResp = resource.CreateResponse{State: tfsdk.State{Schema: sResp.Schema}}
		crR.Create(ctx, resource.CreateRequest{Plan: extPlan}, &cResp)
		if !cResp.Diagnostics.HasError() {
			t.Fatal("expected error on final Get failure in Create")
		}
		failFinalGet = false
	}

	// 5. Read edge cases
	{
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case req.Method == http.MethodGet && req.URL.Path == "/api/agents/ag-read/instructions-bundle":
				_ = json.NewEncoder(w).Encode(client.AgentInstructionsBundle{
					AgentID:   "ag-read",
					Mode:      "managed",
					EntryFile: "AGENTS.md",
					Files: []client.AgentInstructionsFileSummary{
						{Path: "missing.md", Editable: true, Virtual: false},
						{Path: "fail.md", Editable: true, Virtual: false},
					},
				})
			case req.Method == http.MethodGet && req.URL.Path == "/api/agents/ag-read/instructions-bundle/file":
				p := req.URL.Query().Get("path")
				if p == "missing.md" {
					http.NotFound(w, req)
					return
				}
				if p == "fail.md" {
					http.Error(w, "read error", http.StatusInternalServerError)
					return
				}
				http.NotFound(w, req)
			default:
				http.NotFound(w, req)
			}
		}))
		defer srv.Close()

		cli, _ := client.NewClient(srv.URL, "token")
		readR := NewPaperclipAgentInstructionBundleResource().(*PaperclipAgentInstructionBundleResource)
		var confResp resource.ConfigureResponse
		readR.Configure(ctx, resource.ConfigureRequest{ProviderData: cli}, &confResp)

		// Bad raw state
		badState := tfsdk.State{Schema: sResp.Schema, Raw: tftypes.NewValue(tftypes.String, "invalid")}
		var rResp resource.ReadResponse
		readR.Read(ctx, resource.ReadRequest{State: badState}, &rResp)
		if !rResp.Diagnostics.HasError() {
			t.Fatal("expected error in Read with bad state")
		}

		// agent_id == "" fallback to ID, plus missing.md 404 continue and fail.md 500 error
		mRead := PaperclipAgentInstructionBundleResourceModel{
			ID:                        types.StringValue("ag-read"),
			AgentID:                   types.StringNull(),
			Mode:                      types.StringValue("managed"),
			RootPath:                  types.StringNull(),
			EntryFile:                 types.StringValue("AGENTS.md"),
			ClearLegacyPromptTemplate: types.BoolValue(false),
			Files:                     types.MapNull(types.StringType),
			FileInventory:             types.ListNull(types.ObjectType{AttrTypes: instructionBundleFileInventoryAttrTypes}),
		}
		var stRead tfsdk.State
		stRead.Schema = sResp.Schema
		_ = stRead.Set(ctx, &mRead)
		rResp = resource.ReadResponse{State: stRead}
		readR.Read(ctx, resource.ReadRequest{State: stRead}, &rResp)
		if !rResp.Diagnostics.HasError() {
			t.Fatal("expected error diagnostic in Read due to fail.md")
		}
	}

	// 6. Update edge cases
	{
		failSecondGet := false
		failFinalGet := false
		returnEntryFileAsUndeclared := false
		getCount := 0

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case req.Method == http.MethodPatch:
				_ = json.NewEncoder(w).Encode(client.AgentInstructionsBundle{
					AgentID:   "ag-upd",
					Mode:      "external",
					RootPath:  "/tmp/ext",
					EntryFile: "AGENTS.md",
				})
			case req.Method == http.MethodPut:
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
			case req.Method == http.MethodGet && req.URL.Path == "/api/agents/ag-upd/instructions-bundle":
				getCount++
				if failSecondGet && getCount == 1 {
					http.Error(w, "second get failure", http.StatusInternalServerError)
					return
				}
				if failFinalGet && getCount == 2 {
					http.Error(w, "final get failure", http.StatusInternalServerError)
					return
				}
				var files []client.AgentInstructionsFileSummary
				if returnEntryFileAsUndeclared {
					files = append(files, client.AgentInstructionsFileSummary{
						Path:        "AGENTS.md",
						IsEntryFile: true,
						Editable:    true,
						Virtual:     false,
					})
				}
				_ = json.NewEncoder(w).Encode(client.AgentInstructionsBundle{
					AgentID:   "ag-upd",
					Mode:      "external",
					RootPath:  "/tmp/ext",
					EntryFile: "AGENTS.md",
					Files:     files,
				})
			default:
				http.NotFound(w, req)
			}
		}))
		defer srv.Close()

		cli, _ := client.NewClient(srv.URL, "token")
		updR := NewPaperclipAgentInstructionBundleResource().(*PaperclipAgentInstructionBundleResource)
		var confResp resource.ConfigureResponse
		updR.Configure(ctx, resource.ConfigureRequest{ProviderData: cli}, &confResp)

		priorModel := PaperclipAgentInstructionBundleResourceModel{
			ID:                        types.StringValue("ag-upd"),
			AgentID:                   types.StringValue("ag-upd"),
			Mode:                      types.StringValue("managed"),
			RootPath:                  types.StringNull(),
			EntryFile:                 types.StringValue("AGENTS.md"),
			ClearLegacyPromptTemplate: types.BoolValue(false),
			Files:                     types.MapNull(types.StringType),
			FileInventory:             types.ListNull(types.ObjectType{AttrTypes: instructionBundleFileInventoryAttrTypes}),
		}
		var priorState tfsdk.State
		priorState.Schema = sResp.Schema
		_ = priorState.Set(ctx, &priorModel)

		// Bad plan in update
		badPlan := tfsdk.Plan{Schema: sResp.Schema, Raw: tftypes.NewValue(tftypes.String, "invalid")}
		var uResp resource.UpdateResponse
		updR.Update(ctx, resource.UpdateRequest{Plan: badPlan, State: priorState}, &uResp)
		if !uResp.Diagnostics.HasError() {
			t.Fatal("expected error in Update with bad plan")
		}

		// Update external with null Files (desiredFiles == nil)
		extModel := PaperclipAgentInstructionBundleResourceModel{
			ID:                        types.StringValue("ag-upd"),
			AgentID:                   types.StringValue("ag-upd"),
			Mode:                      types.StringValue("external"),
			RootPath:                  types.StringValue("/tmp/ext"),
			EntryFile:                 types.StringValue("AGENTS.md"),
			ClearLegacyPromptTemplate: types.BoolValue(true),
			Files:                     types.MapNull(types.StringType),
			FileInventory:             types.ListNull(types.ObjectType{AttrTypes: instructionBundleFileInventoryAttrTypes}),
		}
		var extPlan tfsdk.Plan
		extPlan.Schema = sResp.Schema
		_ = extPlan.Set(ctx, &extModel)

		uResp = resource.UpdateResponse{State: priorState}
		updR.Update(ctx, resource.UpdateRequest{Plan: extPlan, State: priorState}, &uResp)
		if uResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error in Update external with null files: %v", uResp.Diagnostics)
		}

		extModelWithFiles := extModel
		extFiles, _ := types.MapValueFrom(ctx, types.StringType, map[string]string{"AGENTS.md": "content"})
		extModelWithFiles.Files = extFiles
		var extPlanWithFiles tfsdk.Plan
		extPlanWithFiles.Schema = sResp.Schema
		_ = extPlanWithFiles.Set(ctx, &extModelWithFiles)

		// Update: second Get error
		failSecondGet = true
		getCount = 0
		uResp = resource.UpdateResponse{State: priorState}
		updR.Update(ctx, resource.UpdateRequest{Plan: extPlanWithFiles, State: priorState}, &uResp)
		if !uResp.Diagnostics.HasError() {
			t.Fatal("expected error on second Get failure in Update")
		}
		failSecondGet = false

		// Update: final Get error
		failFinalGet = true
		getCount = 0
		uResp = resource.UpdateResponse{State: priorState}
		updR.Update(ctx, resource.UpdateRequest{Plan: extPlanWithFiles, State: priorState}, &uResp)
		if !uResp.Diagnostics.HasError() {
			t.Fatal("expected error on final Get failure in Update")
		}
		failFinalGet = false

		// Update: undeclared entry file removal rejection
		noEntryModel := PaperclipAgentInstructionBundleResourceModel{
			ID:                        types.StringValue("ag-upd"),
			AgentID:                   types.StringValue("ag-upd"),
			Mode:                      types.StringValue("external"),
			RootPath:                  types.StringValue("/tmp/ext"),
			EntryFile:                 types.StringValue("AGENTS.md"),
			ClearLegacyPromptTemplate: types.BoolValue(false),
			FileInventory:             types.ListNull(types.ObjectType{AttrTypes: instructionBundleFileInventoryAttrTypes}),
		}
		noEntryFiles, _ := types.MapValueFrom(ctx, types.StringType, map[string]string{"other.md": "content"})
		noEntryModel.Files = noEntryFiles
		var noEntryPlan tfsdk.Plan
		noEntryPlan.Schema = sResp.Schema
		_ = noEntryPlan.Set(ctx, &noEntryModel)
		returnEntryFileAsUndeclared = true
		getCount = 0
		uResp = resource.UpdateResponse{State: priorState}
		updR.Update(ctx, resource.UpdateRequest{Plan: noEntryPlan, State: priorState}, &uResp)
		if !uResp.Diagnostics.HasError() {
			t.Fatal("expected error on undeclared entry file removal in Update")
		}
		returnEntryFileAsUndeclared = false

		// Update: final Get error
		failFinalGet = true
		getCount = 0
		uResp = resource.UpdateResponse{State: priorState}
		updR.Update(ctx, resource.UpdateRequest{Plan: extPlan, State: priorState}, &uResp)
		if !uResp.Diagnostics.HasError() {
			t.Fatal("expected error on final Get failure in Update")
		}
		failFinalGet = false
	}

	// 7. Delete edge cases
	{
		modeIsExternal := false
		return404 := false
		return500 := false
		resetToManagedCalled := false

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			switch {
			case req.Method == http.MethodGet && req.URL.Path == "/api/agents/ag-del/instructions-bundle":
				if return404 {
					http.NotFound(w, req)
					return
				}
				if return500 {
					http.Error(w, "server error", http.StatusInternalServerError)
					return
				}
				curMode := "managed"
				if modeIsExternal {
					curMode = "external"
				}
				_ = json.NewEncoder(w).Encode(client.AgentInstructionsBundle{
					AgentID:   "ag-del",
					Mode:      curMode,
					EntryFile: "AGENTS.md",
				})
			case req.Method == http.MethodPut && req.URL.Path == "/api/agents/ag-del/instructions-bundle/file":
				w.WriteHeader(http.StatusOK)
			case req.Method == http.MethodPatch && req.URL.Path == "/api/agents/ag-del/instructions-bundle":
				resetToManagedCalled = true
				_ = json.NewEncoder(w).Encode(client.AgentInstructionsBundle{
					AgentID:   "ag-del",
					Mode:      "managed",
					EntryFile: "AGENTS.md",
				})
			default:
				http.NotFound(w, req)
			}
		}))
		defer srv.Close()

		cli, _ := client.NewClient(srv.URL, "token")
		delR := NewPaperclipAgentInstructionBundleResource().(*PaperclipAgentInstructionBundleResource)
		var confResp resource.ConfigureResponse
		delR.Configure(ctx, resource.ConfigureRequest{ProviderData: cli}, &confResp)

		// Bad raw state
		badState := tfsdk.State{Schema: sResp.Schema, Raw: tftypes.NewValue(tftypes.String, "invalid")}
		var dResp resource.DeleteResponse
		delR.Delete(ctx, resource.DeleteRequest{State: badState}, &dResp)
		if !dResp.Diagnostics.HasError() {
			t.Fatal("expected error in Delete with bad state")
		}

		stModel := PaperclipAgentInstructionBundleResourceModel{
			ID:                        types.StringValue("ag-del"),
			AgentID:                   types.StringValue("ag-del"),
			Mode:                      types.StringValue("managed"),
			RootPath:                  types.StringNull(),
			EntryFile:                 types.StringValue("AGENTS.md"),
			ClearLegacyPromptTemplate: types.BoolValue(false),
			Files:                     types.MapNull(types.StringType),
			FileInventory:             types.ListNull(types.ObjectType{AttrTypes: instructionBundleFileInventoryAttrTypes}),
		}
		var stValid tfsdk.State
		stValid.Schema = sResp.Schema
		_ = stValid.Set(ctx, &stModel)

		// 404 on Delete
		return404 = true
		dResp = resource.DeleteResponse{}
		delR.Delete(ctx, resource.DeleteRequest{State: stValid}, &dResp)
		if dResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error on 404 in Delete: %v", dResp.Diagnostics)
		}
		return404 = false

		// 500 on Delete
		return500 = true
		dResp = resource.DeleteResponse{}
		delR.Delete(ctx, resource.DeleteRequest{State: stValid}, &dResp)
		if !dResp.Diagnostics.HasError() {
			t.Fatal("expected error on 500 in Delete")
		}
		return500 = false

		// External mode on Delete resets to managed
		modeIsExternal = true
		dResp = resource.DeleteResponse{}
		delR.Delete(ctx, resource.DeleteRequest{State: stValid}, &dResp)
		if dResp.Diagnostics.HasError() {
			t.Fatalf("unexpected error on external delete: %v", dResp.Diagnostics)
		}
		if !resetToManagedCalled {
			t.Fatal("expected UpdateAgentInstructionsBundle to reset external mode to managed on Delete")
		}
	}
}

func TestPaperclipAgentInstructionBundleOmittedFilesPreservation(t *testing.T) {
	ctx := context.Background()

	var putCalls []string
	var deleteCalls []string

	serverFiles := map[string]string{
		"AGENTS.md":     "# Original AGENTS.md content",
		"extra.md":      "# Original extra file content",
		"sub/nested.md": "# Original nested file content",
	}

	mode := "managed"
	clearLegacy := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/agents/ag-pres/instructions-bundle":
			var summaries []client.AgentInstructionsFileSummary
			for p, c := range serverFiles {
				summaries = append(summaries, client.AgentInstructionsFileSummary{
					Path:        p,
					Size:        int64(len(c)),
					Language:    "markdown",
					Markdown:    true,
					IsEntryFile: p == "AGENTS.md",
					Editable:    true,
					Virtual:     false,
				})
			}
			_ = json.NewEncoder(w).Encode(client.AgentInstructionsBundle{
				AgentID:   "ag-pres",
				CompanyID: "comp-1",
				Mode:      mode,
				EntryFile: "AGENTS.md",
				Editable:  true,
				Files:     summaries,
			})
		case req.Method == http.MethodPatch && req.URL.Path == "/api/agents/ag-pres/instructions-bundle":
			var body map[string]interface{}
			_ = json.NewDecoder(req.Body).Decode(&body)
			if m, ok := body["mode"].(string); ok && m != "" {
				mode = m
			}
			if cl, ok := body["clearLegacyPromptTemplate"].(bool); ok {
				clearLegacy = cl
			}
			_ = json.NewEncoder(w).Encode(client.AgentInstructionsBundle{
				AgentID:   "ag-pres",
				CompanyID: "comp-1",
				Mode:      mode,
				EntryFile: "AGENTS.md",
				Editable:  true,
			})
		case req.Method == http.MethodPut && req.URL.Path == "/api/agents/ag-pres/instructions-bundle/file":
			var body map[string]interface{}
			_ = json.NewDecoder(req.Body).Decode(&body)
			p := body["path"].(string)
			putCalls = append(putCalls, p)
			serverFiles[p] = body["content"].(string)
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
		case req.Method == http.MethodGet && req.URL.Path == "/api/agents/ag-pres/instructions-bundle/file":
			p := req.URL.Query().Get("path")
			if c, ok := serverFiles[p]; ok {
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"content": c})
				return
			}
			http.NotFound(w, req)
		case req.Method == http.MethodDelete && req.URL.Path == "/api/agents/ag-pres/instructions-bundle/file":
			p := req.URL.Query().Get("path")
			deleteCalls = append(deleteCalls, p)
			delete(serverFiles, p)
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, req)
		}
	}))
	defer srv.Close()

	cli, _ := client.NewClient(srv.URL, "token")
	r := NewPaperclipAgentInstructionBundleResource().(*PaperclipAgentInstructionBundleResource)
	var confResp resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: cli}, &confResp)
	if confResp.Diagnostics.HasError() {
		t.Fatalf("unexpected configure error: %v", confResp.Diagnostics)
	}

	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	// Step 1: Create resource with `files` omitted from config
	createPlanModel := PaperclipAgentInstructionBundleResourceModel{
		ID:                        types.StringNull(),
		AgentID:                   types.StringValue("ag-pres"),
		Mode:                      types.StringValue("managed"),
		RootPath:                  types.StringNull(),
		EntryFile:                 types.StringValue("AGENTS.md"),
		ClearLegacyPromptTemplate: types.BoolValue(false),
		Files:                     types.MapNull(types.StringType),
		FileInventory:             types.ListNull(types.ObjectType{AttrTypes: instructionBundleFileInventoryAttrTypes}),
	}
	var createPlan tfsdk.Plan
	createPlan.Schema = schemaResp.Schema
	_ = createPlan.Set(ctx, &createPlanModel)

	cfgEmptyFiles := tfsdk.Config{
		Schema: schemaResp.Schema,
		Raw:    createPlan.Raw,
	}

	var createResp resource.CreateResponse
	createResp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{
		Config: cfgEmptyFiles,
		Plan:   createPlan,
	}, &createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("unexpected create error: %v", createResp.Diagnostics)
	}

	// Verify Finding F2: NO PUT or DELETE calls were made during create with omitted files!
	if len(putCalls) != 0 {
		t.Fatalf("expected 0 PUT calls during create with files omitted, got %v", putCalls)
	}
	if len(deleteCalls) != 0 {
		t.Fatalf("expected 0 DELETE calls during create with files omitted, got %v", deleteCalls)
	}
	// Verify all server files remain intact
	if serverFiles["AGENTS.md"] != "# Original AGENTS.md content" ||
		serverFiles["extra.md"] != "# Original extra file content" ||
		serverFiles["sub/nested.md"] != "# Original nested file content" {
		t.Fatalf("server files were unexpectedly modified during create: %+v", serverFiles)
	}

	var createdState PaperclipAgentInstructionBundleResourceModel
	_ = createResp.State.Get(ctx, &createdState)
	var stateFiles map[string]string
	_ = createdState.Files.ElementsAs(ctx, &stateFiles, false)
	if len(stateFiles) != 3 || stateFiles["AGENTS.md"] != "# Original AGENTS.md content" {
		t.Fatalf("expected 3 files in state, got %+v", stateFiles)
	}

	// Step 2: Update another attribute (clear_legacy_prompt_template = true) with `files` omitted in config
	updatePlanModel := PaperclipAgentInstructionBundleResourceModel{
		ID:                        createdState.ID,
		AgentID:                   types.StringValue("ag-pres"),
		Mode:                      types.StringValue("managed"),
		RootPath:                  types.StringNull(),
		EntryFile:                 types.StringValue("AGENTS.md"),
		ClearLegacyPromptTemplate: types.BoolValue(true),
		Files:                     types.MapUnknown(types.StringType), // Framework marks computed omitted attribute as unknown
		FileInventory:             types.ListNull(types.ObjectType{AttrTypes: instructionBundleFileInventoryAttrTypes}),
	}
	var updatePlan tfsdk.Plan
	updatePlan.Schema = schemaResp.Schema
	_ = updatePlan.Set(ctx, &updatePlanModel)

	updateCfgModel := PaperclipAgentInstructionBundleResourceModel{
		ID:                        types.StringNull(),
		AgentID:                   types.StringValue("ag-pres"),
		Mode:                      types.StringValue("managed"),
		RootPath:                  types.StringNull(),
		EntryFile:                 types.StringValue("AGENTS.md"),
		ClearLegacyPromptTemplate: types.BoolValue(true),
		Files:                     types.MapNull(types.StringType), // omitted in HCL config
		FileInventory:             types.ListNull(types.ObjectType{AttrTypes: instructionBundleFileInventoryAttrTypes}),
	}
	var cfgPlan tfsdk.Plan
	cfgPlan.Schema = schemaResp.Schema
	_ = cfgPlan.Set(ctx, &updateCfgModel)
	updateConfig := tfsdk.Config{
		Schema: schemaResp.Schema,
		Raw:    cfgPlan.Raw,
	}

	// Test ModifyPlan: files should be preserved from prior state, not unknown
	var mpResp resource.ModifyPlanResponse
	mpResp.Plan = updatePlan
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{
		Config: updateConfig,
		State:  createResp.State,
		Plan:   updatePlan,
	}, &mpResp)
	if mpResp.Diagnostics.HasError() {
		t.Fatalf("unexpected ModifyPlan error: %v", mpResp.Diagnostics)
	}

	var plannedMod PaperclipAgentInstructionBundleResourceModel
	_ = mpResp.Plan.Get(ctx, &plannedMod)
	if plannedMod.Files.IsUnknown() {
		t.Fatal("expected planned Files to NOT be unknown when files is omitted from config during update")
	}
	var plannedFiles map[string]string
	_ = plannedMod.Files.ElementsAs(ctx, &plannedFiles, false)
	if len(plannedFiles) != 3 {
		t.Fatalf("expected 3 files preserved in plan, got %+v", plannedFiles)
	}

	// Execute Update: verify zero file PUT or DELETE calls and remote content survives
	putCalls = nil
	deleteCalls = nil
	var updateResp resource.UpdateResponse
	updateResp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Update(ctx, resource.UpdateRequest{
		Config: updateConfig,
		State:  createResp.State,
		Plan:   mpResp.Plan,
	}, &updateResp)
	if updateResp.Diagnostics.HasError() {
		t.Fatalf("unexpected Update error: %v", updateResp.Diagnostics)
	}

	if len(putCalls) != 0 {
		t.Fatalf("expected 0 PUT calls during update with files omitted, got %v", putCalls)
	}
	if len(deleteCalls) != 0 {
		t.Fatalf("expected 0 DELETE calls during update with files omitted, got %v", deleteCalls)
	}
	if !clearLegacy {
		t.Fatal("expected clearLegacy to be updated to true on server")
	}
	if serverFiles["AGENTS.md"] != "# Original AGENTS.md content" ||
		serverFiles["extra.md"] != "# Original extra file content" ||
		serverFiles["sub/nested.md"] != "# Original nested file content" {
		t.Fatalf("server files were corrupted during update: %+v", serverFiles)
	}
}

func TestPaperclipAgentInstructionBundleDeleteErrorHandling(t *testing.T) {
	ctx := context.Background()

	failDeleteFile := false
	failClearEntry := false
	failResetMode := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodGet && req.URL.Path == "/api/agents/ag-err/instructions-bundle":
			_ = json.NewEncoder(w).Encode(client.AgentInstructionsBundle{
				AgentID:   "ag-err",
				CompanyID: "comp-1",
				Mode:      "external",
				EntryFile: "AGENTS.md",
				Files: []client.AgentInstructionsFileSummary{
					{
						Path:        "AGENTS.md",
						IsEntryFile: true,
						Editable:    true,
						Virtual:     false,
					},
					{
						Path:        "extra.md",
						IsEntryFile: false,
						Editable:    true,
						Virtual:     false,
					},
				},
			})
		case req.Method == http.MethodDelete && req.URL.Path == "/api/agents/ag-err/instructions-bundle/file":
			if failDeleteFile {
				http.Error(w, "disk failure on delete", http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
		case req.Method == http.MethodPut && req.URL.Path == "/api/agents/ag-err/instructions-bundle/file":
			if failClearEntry {
				http.Error(w, "disk failure on clear entry file", http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"ok": true})
		case req.Method == http.MethodPatch && req.URL.Path == "/api/agents/ag-err/instructions-bundle":
			if failResetMode {
				http.Error(w, "database failure on reset mode", http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(client.AgentInstructionsBundle{
				AgentID:   "ag-err",
				Mode:      "managed",
				EntryFile: "AGENTS.md",
			})
		default:
			http.NotFound(w, req)
		}
	}))
	defer srv.Close()

	cli, _ := client.NewClient(srv.URL, "token")
	r := NewPaperclipAgentInstructionBundleResource().(*PaperclipAgentInstructionBundleResource)
	var confResp resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: cli}, &confResp)

	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	stModel := PaperclipAgentInstructionBundleResourceModel{
		ID:                        types.StringValue("ag-err"),
		AgentID:                   types.StringValue("ag-err"),
		Mode:                      types.StringValue("external"),
		RootPath:                  types.StringNull(),
		EntryFile:                 types.StringValue("AGENTS.md"),
		ClearLegacyPromptTemplate: types.BoolValue(false),
		Files:                     types.MapNull(types.StringType),
		FileInventory:             types.ListNull(types.ObjectType{AttrTypes: instructionBundleFileInventoryAttrTypes}),
	}
	var state tfsdk.State
	state.Schema = schemaResp.Schema
	_ = state.Set(ctx, &stModel)

	// Finding F3: Case 1: DeleteAgentInstructionsFile returns 500 -> Delete must report error
	failDeleteFile = true
	var dResp1 resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: state}, &dResp1)
	if !dResp1.Diagnostics.HasError() {
		t.Fatal("expected error diagnostic when DeleteAgentInstructionsFile fails")
	}
	failDeleteFile = false

	// Finding F3: Case 2: UpdateAgentInstructionsFile (entry file clear) returns 500 -> Delete must report error
	failClearEntry = true
	var dResp2 resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: state}, &dResp2)
	if !dResp2.Diagnostics.HasError() {
		t.Fatal("expected error diagnostic when clearing entry file fails")
	}
	failClearEntry = false

	// Finding F3: Case 3: UpdateAgentInstructionsBundle (mode reset) returns 500 -> Delete must report error
	failResetMode = true
	var dResp3 resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: state}, &dResp3)
	if !dResp3.Diagnostics.HasError() {
		t.Fatal("expected error diagnostic when resetting bundle mode fails")
	}
	failResetMode = false
}

func TestPaperclipAgentInstructionBundleOmittedFilesErrorBranches(t *testing.T) {
	ctx := context.Background()

	failBundleGet := false
	failFileGet500 := false
	failFileGet404 := false

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case req.Method == http.MethodPatch && req.URL.Path == "/api/agents/ag-omitted-err/instructions-bundle":
			_ = json.NewEncoder(w).Encode(client.AgentInstructionsBundle{
				AgentID:   "ag-omitted-err",
				Mode:      "managed",
				EntryFile: "AGENTS.md",
			})
		case req.Method == http.MethodGet && req.URL.Path == "/api/agents/ag-omitted-err/instructions-bundle":
			if failBundleGet {
				http.Error(w, "bundle get error", http.StatusInternalServerError)
				return
			}
			_ = json.NewEncoder(w).Encode(client.AgentInstructionsBundle{
				AgentID:   "ag-omitted-err",
				CompanyID: "comp-1",
				Mode:      "managed",
				EntryFile: "AGENTS.md",
				Files: []client.AgentInstructionsFileSummary{
					{
						Path:        "AGENTS.md",
						IsEntryFile: true,
						Editable:    true,
						Virtual:     false,
					},
				},
			})
		case req.Method == http.MethodGet && req.URL.Path == "/api/agents/ag-omitted-err/instructions-bundle/file":
			if failFileGet500 {
				http.Error(w, "file get error", http.StatusInternalServerError)
				return
			}
			if failFileGet404 {
				http.NotFound(w, req)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"content": "# Test"})
		default:
			http.NotFound(w, req)
		}
	}))
	defer srv.Close()

	cli, _ := client.NewClient(srv.URL, "token")
	r := NewPaperclipAgentInstructionBundleResource().(*PaperclipAgentInstructionBundleResource)
	var confResp resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: cli}, &confResp)

	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	planModel := PaperclipAgentInstructionBundleResourceModel{
		ID:                        types.StringNull(),
		AgentID:                   types.StringValue("ag-omitted-err"),
		Mode:                      types.StringValue("managed"),
		RootPath:                  types.StringNull(),
		EntryFile:                 types.StringValue("AGENTS.md"),
		ClearLegacyPromptTemplate: types.BoolValue(false),
		Files:                     types.MapNull(types.StringType),
		FileInventory:             types.ListNull(types.ObjectType{AttrTypes: instructionBundleFileInventoryAttrTypes}),
	}
	var plan tfsdk.Plan
	plan.Schema = schemaResp.Schema
	_ = plan.Set(ctx, &planModel)

	cfg := tfsdk.Config{
		Schema: schemaResp.Schema,
		Raw:    plan.Raw,
	}

	stateModel := PaperclipAgentInstructionBundleResourceModel{
		ID:                        types.StringValue("ag-omitted-err"),
		AgentID:                   types.StringValue("ag-omitted-err"),
		Mode:                      types.StringValue("managed"),
		RootPath:                  types.StringNull(),
		EntryFile:                 types.StringValue("AGENTS.md"),
		ClearLegacyPromptTemplate: types.BoolValue(false),
		Files:                     types.MapNull(types.StringType),
		FileInventory:             types.ListNull(types.ObjectType{AttrTypes: instructionBundleFileInventoryAttrTypes}),
	}
	var state tfsdk.State
	state.Schema = schemaResp.Schema
	_ = state.Set(ctx, &stateModel)

	// Create: Bundle get error
	failBundleGet = true
	var cResp1 resource.CreateResponse
	cResp1.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Config: cfg, Plan: plan}, &cResp1)
	if !cResp1.Diagnostics.HasError() {
		t.Fatal("expected error in Create when bundle get fails")
	}
	failBundleGet = false

	// Create: File get 500 error
	failFileGet500 = true
	var cResp2 resource.CreateResponse
	cResp2.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Config: cfg, Plan: plan}, &cResp2)
	if !cResp2.Diagnostics.HasError() {
		t.Fatal("expected error in Create when file get fails with 500")
	}
	failFileGet500 = false

	// Create: File get 404 (handled gracefully by skipping)
	failFileGet404 = true
	var cResp3 resource.CreateResponse
	cResp3.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Config: cfg, Plan: plan}, &cResp3)
	if cResp3.Diagnostics.HasError() {
		t.Fatalf("unexpected error in Create when file get returns 404: %v", cResp3.Diagnostics)
	}
	failFileGet404 = false

	// Update: Bundle get error
	failBundleGet = true
	var uResp1 resource.UpdateResponse
	uResp1.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Update(ctx, resource.UpdateRequest{Config: cfg, Plan: plan, State: state}, &uResp1)
	if !uResp1.Diagnostics.HasError() {
		t.Fatal("expected error in Update when bundle get fails")
	}
	failBundleGet = false

	// Update: File get 500 error
	failFileGet500 = true
	var uResp2 resource.UpdateResponse
	uResp2.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Update(ctx, resource.UpdateRequest{Config: cfg, Plan: plan, State: state}, &uResp2)
	if !uResp2.Diagnostics.HasError() {
		t.Fatal("expected error in Update when file get fails with 500")
	}
	failFileGet500 = false

	// Update: File get 404 (handled gracefully by skipping)
	failFileGet404 = true
	var uResp3 resource.UpdateResponse
	uResp3.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Update(ctx, resource.UpdateRequest{Config: cfg, Plan: plan, State: state}, &uResp3)
	if uResp3.Diagnostics.HasError() {
		t.Fatalf("unexpected error in Update when file get returns 404: %v", uResp3.Diagnostics)
	}
	failFileGet404 = false
}

func TestPaperclipAgentV1ToV2UpgradeIssuesZeroWrites(t *testing.T) {
	ctx := context.Background()

	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls = append(calls, req.Method+" "+req.URL.String())
		http.Error(w, "no HTTP calls expected during state upgrade", http.StatusInternalServerError)
	}))
	defer srv.Close()

	r := NewPaperclipAgentResource().(*PaperclipAgentResource)
	upgraders := r.UpgradeState(ctx)
	u1, ok := upgraders[1]
	if !ok {
		t.Fatal("missing v1 state upgrader")
	}

	priorState := tfsdk.State{Schema: *u1.PriorSchema}
	_ = priorState.Set(ctx, &struct {
		ID                   types.String               `tfsdk:"id"`
		CompanyID            types.String               `tfsdk:"company_id"`
		Name                 types.String               `tfsdk:"name"`
		Role                 types.String               `tfsdk:"role"`
		Title                types.String               `tfsdk:"title"`
		Icon                 types.String               `tfsdk:"icon"`
		ReportsTo            types.String               `tfsdk:"reports_to"`
		Capabilities         types.String               `tfsdk:"capabilities"`
		DefaultEnvironmentID types.String               `tfsdk:"default_environment_id"`
		AdapterType          types.String               `tfsdk:"adapter_type"`
		Status               types.String               `tfsdk:"status"`
		RuntimeConfig        types.String               `tfsdk:"runtime_config"`
		AdapterConfig        types.String               `tfsdk:"adapter_config"`
		Metadata             types.String               `tfsdk:"metadata"`
		Instructions         types.String               `tfsdk:"instructions"`
		CanAssignTasks       types.Bool                 `tfsdk:"can_assign_tasks"`
		CanCreateAgents      types.Bool                 `tfsdk:"can_create_agents"`
		CanCreateSkills      types.Bool                 `tfsdk:"can_create_skills"`
		TrustPreset          types.String               `tfsdk:"trust_preset"`
		AuthorizationPolicy  types.String               `tfsdk:"authorization_policy"`
		Paused               types.Bool                 `tfsdk:"paused"`
		Retirement           types.String               `tfsdk:"retirement"`
		Env                  map[string]EnvBindingModel `tfsdk:"env"`
	}{
		ID:                   types.StringValue("ag-v1-proof"),
		CompanyID:            types.StringValue("comp-1"),
		Name:                 types.StringValue("agent-proof"),
		Role:                 types.StringValue("engineer"),
		Title:                types.StringValue("Staff"),
		Icon:                 types.StringValue("robot"),
		ReportsTo:            types.StringValue("lead-1"),
		Capabilities:         types.StringValue("coding"),
		DefaultEnvironmentID: types.StringValue("env-1"),
		AdapterType:          types.StringValue("antigravity_local"),
		Status:               types.StringValue("active"),
		RuntimeConfig:        types.StringValue(`{}`),
		AdapterConfig:        types.StringValue(`{}`),
		Metadata:             types.StringValue(`{}`),
		Instructions:         types.StringValue("# Critical Untouched Instructions in AGENTS.md"),
		CanAssignTasks:       types.BoolValue(true),
		CanCreateAgents:      types.BoolValue(false),
		CanCreateSkills:      types.BoolValue(true),
		TrustPreset:          types.StringValue("standard"),
		AuthorizationPolicy:  types.StringNull(),
		Paused:               types.BoolValue(false),
		Retirement:           types.StringValue("delete"),
		Env:                  nil,
	})

	var sResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sResp)

	var upgResp resource.UpgradeStateResponse
	upgResp.State = tfsdk.State{Schema: sResp.Schema}
	u1.StateUpgrader(ctx, resource.UpgradeStateRequest{State: &priorState}, &upgResp)
	if upgResp.Diagnostics.HasError() {
		t.Fatalf("unexpected upgrader error: %v", upgResp.Diagnostics)
	}

	var upgraded PaperclipAgentResourceModel
	_ = upgResp.State.Get(ctx, &upgraded)
	if upgraded.ID.ValueString() != "ag-v1-proof" {
		t.Fatalf("expected ID ag-v1-proof, got %s", upgraded.ID.ValueString())
	}

	// Verify Finding F4: zero HTTP requests issued during state migration
	if len(calls) != 0 {
		t.Fatalf("expected 0 HTTP writes during state upgrade, but %d calls were made: %v", len(calls), calls)
	}
}

