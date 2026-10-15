package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/ephemeral"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestProviderConfigureAndMetadata(t *testing.T) {
	ctx := context.Background()
	p := New("0.1.0")().(*PaperclipProvider)

	var mResp provider.MetadataResponse
	p.Metadata(ctx, provider.MetadataRequest{}, &mResp)
	if mResp.TypeName != "paperclip" || mResp.Version != "0.1.0" {
		t.Fatalf("unexpected metadata: %+v", mResp)
	}

	var sResp provider.SchemaResponse
	p.Schema(ctx, provider.SchemaRequest{}, &sResp)
	if len(sResp.Schema.Attributes) == 0 {
		t.Fatal("expected provider schema attributes")
	}

	if len(p.Resources(ctx)) == 0 || len(p.DataSources(ctx)) == 0 || len(p.EphemeralResources(ctx)) == 0 {
		t.Fatal("expected provider resources, data sources, and ephemeral resources")
	}

	// Bad config raw
	badConfig := tfsdk.Config{Schema: sResp.Schema, Raw: tftypes.NewValue(tftypes.String, "invalid")}
	var badResp provider.ConfigureResponse
	p.Configure(ctx, provider.ConfigureRequest{Config: badConfig}, &badResp)
	if !badResp.Diagnostics.HasError() {
		t.Fatal("expected error on bad provider config")
	}

	// Configure with env vars
	os.Setenv("PAPERCLIP_API_URL", "http://127.0.0.1:3100")
	os.Setenv("PAPERCLIP_API_KEY", "test-key")
	defer func() {
		os.Unsetenv("PAPERCLIP_API_URL")
		os.Unsetenv("PAPERCLIP_API_KEY")
		os.Unsetenv("PAPERCLIP_ENDPOINT")
		os.Unsetenv("PAPERCLIP_URL")
		os.Unsetenv("PAPERCLIP_BOARD_TOKEN")
		os.Unsetenv("PAPERCLIP_TOKEN")
	}()

	var okResp provider.ConfigureResponse
	validPlan := tfsdk.Plan{Schema: sResp.Schema}
	_ = validPlan.Set(ctx, &PaperclipProviderModel{
		Endpoint: types.StringValue("http://127.0.0.1:3100"),
		Token:    types.StringValue("key"),
	})
	validConfig := tfsdk.Config{Schema: sResp.Schema, Raw: validPlan.Raw}
	p.Configure(ctx, provider.ConfigureRequest{Config: validConfig}, &okResp)
	if okResp.Diagnostics.HasError() {
		t.Fatalf("unexpected configure error: %v", okResp.Diagnostics)
	}

	// Fallback env vars
	os.Unsetenv("PAPERCLIP_API_URL")
	os.Setenv("PAPERCLIP_ENDPOINT", "http://127.0.0.1:3100")
	os.Unsetenv("PAPERCLIP_API_KEY")
	os.Setenv("PAPERCLIP_BOARD_TOKEN", "board-token")
	var envResp1 provider.ConfigureResponse
	emptyPlan := tfsdk.Plan{Schema: sResp.Schema}
	_ = emptyPlan.Set(ctx, &PaperclipProviderModel{})
	emptyModelConfig := tfsdk.Config{Schema: sResp.Schema, Raw: emptyPlan.Raw}
	p.Configure(ctx, provider.ConfigureRequest{Config: emptyModelConfig}, &envResp1)

	os.Unsetenv("PAPERCLIP_ENDPOINT")
	os.Setenv("PAPERCLIP_URL", "http://127.0.0.1:3100")
	os.Unsetenv("PAPERCLIP_BOARD_TOKEN")
	os.Setenv("PAPERCLIP_TOKEN", "token")
	var envResp2 provider.ConfigureResponse
	p.Configure(ctx, provider.ConfigureRequest{Config: emptyModelConfig}, &envResp2)

	// PaperclipEndpoint & PaperclipToken attributes
	var legacyResp provider.ConfigureResponse
	legacyPlan := tfsdk.Plan{Schema: sResp.Schema}
	_ = legacyPlan.Set(ctx, &PaperclipProviderModel{
		PaperclipEndpoint: types.StringValue("http://127.0.0.1:3100"),
		PaperclipToken:    types.StringValue("legacy-token"),
	})
	legacyConfig := tfsdk.Config{Schema: sResp.Schema, Raw: legacyPlan.Raw}
	p.Configure(ctx, provider.ConfigureRequest{Config: legacyConfig}, &legacyResp)

	// Default 127.0.0.1:3100 when endpoint is empty
	os.Unsetenv("PAPERCLIP_URL")
	var defaultResp provider.ConfigureResponse
	p.Configure(ctx, provider.ConfigureRequest{Config: emptyModelConfig}, &defaultResp)
}

func TestEphemeralsLifecycle(t *testing.T) {
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/cli-auth/challenges":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "chal-1", "token": "tok-1", "boardApiToken": "board-tok",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/cli-auth/challenges/chal-1/approve":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"approved": true, "status": "approved", "keyId": "k-1",
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/cli-auth/me":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"source": "board_key", "keyId": "k-1",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/board-api-keys":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "k-1", "name": "bk-1", "token": "board-tok",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/agents/agent-1/keys":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "ak-1", "token": "agent-tok",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cli, _ := client.NewClient(server.URL, "token")

	// BoardKey
	bk := NewPaperclipBoardKeyEphemeralResource().(*PaperclipBoardKeyEphemeralResource)
	var bkMeta ephemeral.MetadataResponse
	bk.Metadata(ctx, ephemeral.MetadataRequest{ProviderTypeName: "paperclip"}, &bkMeta)
	var bkSchema ephemeral.SchemaResponse
	bk.Schema(ctx, ephemeral.SchemaRequest{}, &bkSchema)

	var bkConf ephemeral.ConfigureResponse
	bk.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: nil}, &bkConf)
	bk.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: "invalid"}, &bkConf)
	bk.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: cli}, &bkConf)

	// bad config
	badConfigBK := tfsdk.Config{Schema: bkSchema.Schema, Raw: tftypes.NewValue(tftypes.String, "invalid")}
	var bkOpenBad ephemeral.OpenResponse
	bk.Open(ctx, ephemeral.OpenRequest{Config: badConfigBK}, &bkOpenBad)
	if !bkOpenBad.Diagnostics.HasError() {
		t.Fatal("expected error on bad config")
	}

	// unconfigured
	unconfBK := &PaperclipBoardKeyEphemeralResource{}
	goodPlan := tfsdk.Plan{Schema: bkSchema.Schema}
	_ = goodPlan.Set(ctx, &PaperclipBoardKeyEphemeralResourceModel{
		KeyID: types.StringValue("bk-boost"),
	})
	goodConfig := tfsdk.Config{Schema: bkSchema.Schema, Raw: goodPlan.Raw}
	var bkOpenUnconf ephemeral.OpenResponse
	unconfBK.Open(ctx, ephemeral.OpenRequest{Config: goodConfig}, &bkOpenUnconf)
	if !bkOpenUnconf.Diagnostics.HasError() {
		t.Fatal("expected error on unconfigured")
	}

	// token unavailable
	unavailPlan := tfsdk.Plan{Schema: bkSchema.Schema}
	_ = unavailPlan.Set(ctx, &PaperclipBoardKeyEphemeralResourceModel{
		KeyID: types.StringValue("bk-unavail"),
	})
	unavailConfig := tfsdk.Config{Schema: bkSchema.Schema, Raw: unavailPlan.Raw}
	var bkOpenUnavailable ephemeral.OpenResponse
	bkOpenUnavailable.Result = tfsdk.EphemeralResultData{Schema: bkSchema.Schema}
	bk.Open(ctx, ephemeral.OpenRequest{Config: unavailConfig}, &bkOpenUnavailable)
	if !bkOpenUnavailable.Diagnostics.HasError() {
		t.Fatal("expected error when token is unavailable")
	}

	// success
	cli.RegisterMintedToken("bk-boost", "tok-boost")
	var bkOpenSuccess ephemeral.OpenResponse
	privBk := initOpenResponsePrivate(&bkOpenSuccess)
	bkOpenSuccess.Result = tfsdk.EphemeralResultData{Schema: bkSchema.Schema}
	bk.Open(ctx, ephemeral.OpenRequest{Config: goodConfig}, &bkOpenSuccess)
	if bkOpenSuccess.Diagnostics.HasError() {
		t.Fatalf("bk open failed: %v", bkOpenSuccess.Diagnostics)
	}

	var bkCloseReq ephemeral.CloseRequest
	initCloseRequestPrivate(&bkCloseReq, privBk)
	var bkCloseResp ephemeral.CloseResponse
	bk.Close(ctx, bkCloseReq, &bkCloseResp)
	if bkCloseResp.Diagnostics.HasError() {
		t.Fatalf("bk close failed: %v", bkCloseResp.Diagnostics)
	}
	if !cli.IsTokenDelivered("bk-boost") {
		t.Fatal("expected bk-boost marked delivered")
	}

	// AgentKey
	ak := NewPaperclipAgentKeyEphemeralResource().(*PaperclipAgentKeyEphemeralResource)
	var akMeta ephemeral.MetadataResponse
	ak.Metadata(ctx, ephemeral.MetadataRequest{ProviderTypeName: "paperclip"}, &akMeta)
	var akSchema ephemeral.SchemaResponse
	ak.Schema(ctx, ephemeral.SchemaRequest{}, &akSchema)

	var akConf ephemeral.ConfigureResponse
	ak.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: nil}, &akConf)
	ak.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: "invalid"}, &akConf)
	ak.Configure(ctx, ephemeral.ConfigureRequest{ProviderData: cli}, &akConf)

	var akOpenBad ephemeral.OpenResponse
	badConfigAK := tfsdk.Config{Schema: akSchema.Schema, Raw: tftypes.NewValue(tftypes.String, "invalid")}
	ak.Open(ctx, ephemeral.OpenRequest{Config: badConfigAK}, &akOpenBad)
	if !akOpenBad.Diagnostics.HasError() {
		t.Fatal("expected error on bad config")
	}

	unconfAK := &PaperclipAgentKeyEphemeralResource{}
	akGoodPlan := tfsdk.Plan{Schema: akSchema.Schema}
	_ = akGoodPlan.Set(ctx, &PaperclipAgentKeyEphemeralResourceModel{
		KeyID: types.StringValue("ak-boost"),
	})
	akGoodConfig := tfsdk.Config{Schema: akSchema.Schema, Raw: akGoodPlan.Raw}
	var akOpenUnconf ephemeral.OpenResponse
	unconfAK.Open(ctx, ephemeral.OpenRequest{Config: akGoodConfig}, &akOpenUnconf)
	if !akOpenUnconf.Diagnostics.HasError() {
		t.Fatal("expected error on unconfigured")
	}

	// token unavailable
	akUnavailPlan := tfsdk.Plan{Schema: akSchema.Schema}
	_ = akUnavailPlan.Set(ctx, &PaperclipAgentKeyEphemeralResourceModel{
		KeyID: types.StringValue("ak-unavail"),
	})
	akUnavailConfig := tfsdk.Config{Schema: akSchema.Schema, Raw: akUnavailPlan.Raw}
	var akOpenUnavailable ephemeral.OpenResponse
	akOpenUnavailable.Result = tfsdk.EphemeralResultData{Schema: akSchema.Schema}
	ak.Open(ctx, ephemeral.OpenRequest{Config: akUnavailConfig}, &akOpenUnavailable)
	if !akOpenUnavailable.Diagnostics.HasError() {
		t.Fatal("expected error when agent token unavailable")
	}

	// success
	cli.RegisterMintedToken("ak-boost", "ak-tok-boost")
	var akOpenSuccess ephemeral.OpenResponse
	privAk := initOpenResponsePrivate(&akOpenSuccess)
	akOpenSuccess.Result = tfsdk.EphemeralResultData{Schema: akSchema.Schema}
	ak.Open(ctx, ephemeral.OpenRequest{Config: akGoodConfig}, &akOpenSuccess)
	if akOpenSuccess.Diagnostics.HasError() {
		t.Fatalf("ak open failed: %v", akOpenSuccess.Diagnostics)
	}

	var akCloseReq ephemeral.CloseRequest
	initCloseRequestPrivate(&akCloseReq, privAk)
	var akCloseResp ephemeral.CloseResponse
	ak.Close(ctx, akCloseReq, &akCloseResp)
	if akCloseResp.Diagnostics.HasError() {
		t.Fatalf("ak close failed: %v", akCloseResp.Diagnostics)
	}
	if !cli.IsTokenDelivered("ak-boost") {
		t.Fatal("expected ak-boost marked delivered")
	}
}

func TestProjectLeadLifecycle(t *testing.T) {
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPatch && r.URL.Path == "/api/projects/proj-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "proj-1", "leadAgentId": "agent-1"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/projects/proj-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "proj-1", "leadAgentId": "agent-1"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/projects/proj-nolead":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "proj-nolead", "leadAgentId": ""})
		case r.Method == http.MethodGet && r.URL.Path == "/api/projects/proj-diff":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "proj-diff", "leadAgentId": "agent-diff"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	r := NewPaperclipProjectLeadResource().(*PaperclipProjectLeadResource)
	var meta resource.MetadataResponse
	r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "paperclip"}, &meta)
	var sResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sResp)

	var confResp resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: nil}, &confResp)
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: "invalid"}, &confResp)
	cli, _ := client.NewClient(server.URL, "token")
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: cli}, &confResp)

	// Bad plan / state
	badPlan := tfsdk.Plan{Schema: sResp.Schema, Raw: tftypes.NewValue(tftypes.String, "invalid")}
	badState := tfsdk.State{Schema: sResp.Schema, Raw: tftypes.NewValue(tftypes.String, "invalid")}
	var badCreate resource.CreateResponse
	r.Create(ctx, resource.CreateRequest{Plan: badPlan}, &badCreate)
	var badRead resource.ReadResponse
	r.Read(ctx, resource.ReadRequest{State: badState}, &badRead)
	var badUpdate resource.UpdateResponse
	r.Update(ctx, resource.UpdateRequest{Plan: badPlan, State: badState}, &badUpdate)
	var badDelete resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: badState}, &badDelete)

	// Empty project/agent id assign error
	modelEmpty := PaperclipProjectLeadModel{ProjectID: types.StringValue(""), AgentID: types.StringValue("")}
	planEmpty := tfsdk.Plan{Schema: sResp.Schema}
	_ = planEmpty.Set(ctx, &modelEmpty)
	var createEmpty resource.CreateResponse
	r.Create(ctx, resource.CreateRequest{Plan: planEmpty}, &createEmpty)
	if !createEmpty.Diagnostics.HasError() {
		t.Fatal("expected error on empty assign")
	}

	// Create success
	model := PaperclipProjectLeadModel{
		ProjectID: types.StringValue("proj-1"),
		AgentID:   types.StringValue("agent-1"),
	}
	plan := tfsdk.Plan{Schema: sResp.Schema}
	_ = plan.Set(ctx, &model)
	var created resource.CreateResponse
	created.State = tfsdk.State{Schema: sResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
	if created.Diagnostics.HasError() {
		t.Fatalf("lead create failed: %v", created.Diagnostics)
	}

	// Read success
	var read resource.ReadResponse
	read.State = created.State
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatalf("lead read failed: %v", read.Diagnostics)
	}

	// Read 404
	model404 := PaperclipProjectLeadModel{ProjectID: types.StringValue("proj-missing")}
	state404 := tfsdk.State{Schema: sResp.Schema}
	_ = state404.Set(ctx, &model404)
	var read404 resource.ReadResponse
	read404.State = state404
	r.Read(ctx, resource.ReadRequest{State: state404}, &read404)

	// Read empty leadAgentID
	modelNoLead := PaperclipProjectLeadModel{ProjectID: types.StringValue("proj-nolead")}
	stateNoLead := tfsdk.State{Schema: sResp.Schema}
	_ = stateNoLead.Set(ctx, &modelNoLead)
	var readNoLead resource.ReadResponse
	readNoLead.State = stateNoLead
	r.Read(ctx, resource.ReadRequest{State: stateNoLead}, &readNoLead)

	// Update
	var updated resource.UpdateResponse
	updated.State = created.State
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: created.State}, &updated)

	// Delete 404
	var del404 resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: state404}, &del404)

	// Delete diff lead
	modelDiff := PaperclipProjectLeadModel{ProjectID: types.StringValue("proj-diff"), AgentID: types.StringValue("agent-mine")}
	stateDiff := tfsdk.State{Schema: sResp.Schema}
	_ = stateDiff.Set(ctx, &modelDiff)
	var delDiff resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: stateDiff}, &delDiff)

	// Delete success
	var delSuccess resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: created.State}, &delSuccess)

	// ImportState
	var impResp resource.ImportStateResponse
	impResp.State = tfsdk.State{Schema: sResp.Schema}
	_ = impResp.State.Set(ctx, &PaperclipProjectLeadModel{})
	r.ImportState(ctx, resource.ImportStateRequest{ID: "proj-1"}, &impResp)
	r.ImportState(ctx, resource.ImportStateRequest{ID: ""}, &impResp)
}

func TestAllResourcesBadPlanStateAndConfigure(t *testing.T) {
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()
	cli, _ := client.NewClient(server.URL, "token")

	// Helper for testing any resource
	testResource := func(r resource.Resource) {
		var meta resource.MetadataResponse
		r.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "paperclip"}, &meta)
		var sResp resource.SchemaResponse
		r.Schema(ctx, resource.SchemaRequest{}, &sResp)

		badPlan := tfsdk.Plan{Schema: sResp.Schema, Raw: tftypes.NewValue(tftypes.String, "invalid")}
		badState := tfsdk.State{Schema: sResp.Schema, Raw: tftypes.NewValue(tftypes.String, "invalid")}
		badConfig := tfsdk.Config{Schema: sResp.Schema, Raw: tftypes.NewValue(tftypes.String, "invalid")}

		if rc, ok := r.(resource.ResourceWithConfigure); ok {
			var confResp resource.ConfigureResponse
			rc.Configure(ctx, resource.ConfigureRequest{ProviderData: nil}, &confResp)
			rc.Configure(ctx, resource.ConfigureRequest{ProviderData: "invalid"}, &confResp)
			rc.Configure(ctx, resource.ConfigureRequest{ProviderData: cli}, &confResp)
		}

		if rv, ok := r.(resource.ResourceWithValidateConfig); ok {
			var valResp resource.ValidateConfigResponse
			rv.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: badConfig}, &valResp)
		}

		if rm, ok := r.(resource.ResourceWithModifyPlan); ok {
			var modResp resource.ModifyPlanResponse
			modResp.Plan = badPlan
			rm.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: badPlan}, &modResp)
		}

		var cResp resource.CreateResponse
		r.Create(ctx, resource.CreateRequest{Plan: badPlan, Config: badConfig}, &cResp)
		var rResp resource.ReadResponse
		r.Read(ctx, resource.ReadRequest{State: badState}, &rResp)
		var uResp resource.UpdateResponse
		r.Update(ctx, resource.UpdateRequest{Plan: badPlan, State: badState, Config: badConfig}, &uResp)
		var dResp resource.DeleteResponse
		r.Delete(ctx, resource.DeleteRequest{State: badState}, &dResp)
	}

	resources := []resource.Resource{
		NewPaperclipCompanyResource(),
		NewPaperclipProjectResource(),
		NewPaperclipProjectLeadResource(),
		NewPaperclipWorkspaceResource(),
		NewPaperclipLocalEnvironmentResource(),
		NewPaperclipSkillResource(),
		NewPaperclipSecretResource(),
		NewPaperclipAgentResource(),
		NewPaperclipAppResource(),
		NewPaperclipConnectionResource(),
		NewPaperclipPolicyResource(),
		NewPaperclipStdioTemplateResource(),
		NewPaperclipConnectionInstallResource(),
		NewPaperclipAppPermissionResource(),
		NewPaperclipSkillAssignmentResource(),
		NewPaperclipSkillPolicyResource(),
		NewPaperclipInvitationResource(),
		NewPaperclipInviteResource(),
		NewPaperclipMembershipResource(),
		NewPaperclipPermissionGrantsResource(),
		NewPaperclipMemberPermissionsResource(),
		NewPaperclipInstanceAdminResource(),
		NewPaperclipUserCompanyAccessResource(),
		NewPaperclipUserInstanceAccessResource(),
		NewPaperclipPluginResource(),
		NewPaperclipPluginConfigResource(),
	}

	for _, r := range resources {
		testResource(r)
	}

	// Helper for data sources
	testDataSource := func(d datasource.DataSource) {
		var meta datasource.MetadataResponse
		d.Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: "paperclip"}, &meta)
		var sResp datasource.SchemaResponse
		d.Schema(ctx, datasource.SchemaRequest{}, &sResp)

		badConfig := tfsdk.Config{Schema: sResp.Schema, Raw: tftypes.NewValue(tftypes.String, "invalid")}

		if dc, ok := d.(datasource.DataSourceWithConfigure); ok {
			var confResp datasource.ConfigureResponse
			dc.Configure(ctx, datasource.ConfigureRequest{ProviderData: nil}, &confResp)
			dc.Configure(ctx, datasource.ConfigureRequest{ProviderData: "invalid"}, &confResp)
			dc.Configure(ctx, datasource.ConfigureRequest{ProviderData: cli}, &confResp)
		}

		var rResp datasource.ReadResponse
		d.Read(ctx, datasource.ReadRequest{Config: badConfig}, &rResp)
	}

	dataSources := []datasource.DataSource{
		NewPaperclipCompanyDataSource(),
		NewPaperclipProjectDataSource(),
		NewPaperclipAgentDataSource(),
		NewPaperclipSkillDataSource(),
		NewPaperclipUserDataSource(),
		NewPaperclipUsersDataSource(),
	}

	for _, d := range dataSources {
		testDataSource(d)
	}
}

func TestProjectExecutionWorkspacePolicyAndUpgradeState(t *testing.T) {
	ctx := context.Background()

	p1, err := nativeJSONObject(types.StringNull())
	if err != nil || p1 != nil {
		t.Fatal(err)
	}
	p2, err := nativeJSONObject(types.StringUnknown())
	if err != nil || p2 != nil {
		t.Fatal(err)
	}
	p3, err := nativeJSONObject(types.StringValue(`{"mode":"test"}`))
	if err != nil || p3 == nil || p3["mode"] != "test" {
		t.Fatal(err)
	}
	if _, err := nativeJSONObject(types.StringValue("invalid json")); err == nil {
		t.Fatal("expected error on invalid json")
	}
	if _, err := nativeJSONObject(types.StringValue("null")); err == nil {
		t.Fatal("expected error on null json")
	}

	// UpgradeState
	r := NewPaperclipProjectResource().(*PaperclipProjectResource)
	upgraders := r.UpgradeState(ctx)
	upgrader, ok := upgraders[0]
	if !ok {
		t.Fatal("expected upgrader 0")
	}

	var upRespBad resource.UpgradeStateResponse
	upgrader.StateUpgrader(ctx, resource.UpgradeStateRequest{RawState: &tfprotov6.RawState{JSON: []byte(`invalid`)}}, &upRespBad)
	if !upRespBad.Diagnostics.HasError() {
		t.Fatal("expected error on bad prior state")
	}

	var sResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sResp)
	var upRespGood resource.UpgradeStateResponse
	upRespGood.State = tfsdk.State{Schema: sResp.Schema}
	upgrader.StateUpgrader(ctx, resource.UpgradeStateRequest{RawState: &tfprotov6.RawState{JSON: []byte(`{"id":"p-1","company_id":"c-1","name":"Name","description":"Desc","lead_agent_id":"lead-1","execution_workspace_policy":"{\"enabled\":true}"}`)}}, &upRespGood)
	if upRespGood.Diagnostics.HasError() {
		t.Fatalf("unexpected upgrade error: %v", upRespGood.Diagnostics)
	}

	skillRes := NewPaperclipSkillResource().(*PaperclipSkillResource)
	skillUpgraders := skillRes.UpgradeState(ctx)
	skillUp0, ok := skillUpgraders[0]
	if !ok {
		t.Fatal("expected skill upgrader 0")
	}
	badSkillPrior := tfsdk.State{Schema: *skillUp0.PriorSchema, Raw: tftypes.NewValue(tftypes.String, "invalid")}
	var upRespSkillBad resource.UpgradeStateResponse
	skillUp0.StateUpgrader(ctx, resource.UpgradeStateRequest{State: &badSkillPrior}, &upRespSkillBad)
	if !upRespSkillBad.Diagnostics.HasError() {
		t.Fatal("expected error on bad skill prior state")
	}
}

func TestLocalEnvironmentEdgeCases(t *testing.T) {
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/instance/settings/experimental":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"enableEnvironments": true})
		case r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/environments" && r.URL.Query().Get("driver") == "local":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "env-1", "driver": "local", "envVars": map[string]interface{}{}},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/api/environments/env-1":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id": "env-1", "driver": "local", "envVars": map[string]interface{}{}},
			)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cli, _ := client.NewClient(server.URL, "token")
	r := NewPaperclipLocalEnvironmentResource().(*PaperclipLocalEnvironmentResource)
	var sResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sResp)
	var confResp resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: cli}, &confResp)

	// ValidateConfig
	var valResp resource.ValidateConfigResponse
	valPlan := tfsdk.Plan{Schema: sResp.Schema}
	_ = valPlan.Set(ctx, &PaperclipLocalEnvironmentModel{
		CompanyID:          types.StringValue("comp-1"),
		InheritServicePath: types.BoolValue(false),
	})
	valCfgFalse := tfsdk.Config{Schema: sResp.Schema, Raw: valPlan.Raw}
	r.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: valCfgFalse}, &valResp)
	if !valResp.Diagnostics.HasError() {
		t.Fatal("expected error when InheritServicePath is false")
	}

	// Create with InheritServicePath false
	planFalse := tfsdk.Plan{Schema: sResp.Schema}
	_ = planFalse.Set(ctx, &PaperclipLocalEnvironmentModel{
		CompanyID:          types.StringValue("comp-1"),
		InheritServicePath: types.BoolValue(false),
	})
	var createFalse resource.CreateResponse
	r.Create(ctx, resource.CreateRequest{Plan: planFalse}, &createFalse)
	if !createFalse.Diagnostics.HasError() {
		t.Fatal("expected error on create when InheritServicePath is false")
	}

	// Create success (null ID finds local environment)
	planGood := tfsdk.Plan{Schema: sResp.Schema}
	_ = planGood.Set(ctx, &PaperclipLocalEnvironmentModel{
		CompanyID:          types.StringValue("comp-1"),
		InheritServicePath: types.BoolValue(true),
		ID:                 types.StringNull(),
	})
	var created resource.CreateResponse
	created.State = tfsdk.State{Schema: sResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: planGood}, &created)
	if created.Diagnostics.HasError() {
		t.Fatalf("local env create failed: %v", created.Diagnostics)
	}

	// Update with ID already known
	planWithID := tfsdk.Plan{Schema: sResp.Schema}
	_ = planWithID.Set(ctx, &PaperclipLocalEnvironmentModel{
		CompanyID:          types.StringValue("comp-1"),
		InheritServicePath: types.BoolValue(true),
		ID:                 types.StringValue("env-1"),
	})
	var updated resource.UpdateResponse
	updated.State = created.State
	r.Update(ctx, resource.UpdateRequest{Plan: planWithID, State: created.State}, &updated)
	if updated.Diagnostics.HasError() {
		t.Fatalf("local env update failed: %v", updated.Diagnostics)
	}

	// Read
	var read resource.ReadResponse
	read.State = updated.State
	r.Read(ctx, resource.ReadRequest{State: updated.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatalf("local env read failed: %v", read.Diagnostics)
	}

	// Delete
	var del resource.DeleteResponse
	r.Delete(ctx, resource.DeleteRequest{State: updated.State}, &del)
}

func TestAppPermissionModifyPlanAndRevoke(t *testing.T) {
	ctx := context.Background()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/tool-connections/conn-oauth":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "conn-oauth", "authKind": "oauth", "status": "draft"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/tool-connections/conn-active":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "conn-active", "authKind": "token", "status": "active"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/tool-connections/conn-active/catalog":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{
				{"id": "entry-1", "toolName": "read"},
			})
		case r.Method == http.MethodPost && r.URL.Path == "/api/companies/comp-1/tools/profiles/conn-active/revoke":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && r.URL.Path == "/api/tool-connections/conn-active/permissions":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"accessType": "agents", "agentIds": []string{}, "enabledIds": []string{},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	cli, _ := client.NewClient(server.URL, "token")
	r := NewPaperclipAppPermissionResource().(*PaperclipAppPermissionResource)
	var sResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sResp)
	var confResp resource.ConfigureResponse
	r.Configure(ctx, resource.ConfigureRequest{ProviderData: cli}, &confResp)

	// ModifyPlan null plan
	nullPlan := tfsdk.Plan{Raw: tftypes.NewValue(tftypes.String, nil)}
	var modNull resource.ModifyPlanResponse
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: nullPlan}, &modNull)

	// ModifyPlan empty agent IDs
	planEmptyAgents := tfsdk.Plan{Schema: sResp.Schema}
	emptyAgentList, _ := types.ListValue(types.StringType, []attr.Value{})
	_ = planEmptyAgents.Set(ctx, &PaperclipAppPermissionResourceModel{
		CompanyID:     types.StringValue("comp-1"),
		ConnectionID:  types.StringValue("conn-active"),
		ToolSelection: types.StringValue("all"),
		AgentIDs:      emptyAgentList,
	})
	var modEmpty resource.ModifyPlanResponse
	modEmpty.Plan = planEmptyAgents
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: planEmptyAgents}, &modEmpty)

	// ModifyPlan OAuth draft (awaiting authorization)
	planOAuth := tfsdk.Plan{Schema: sResp.Schema}
	agentList, _ := types.ListValue(types.StringType, []attr.Value{types.StringValue("agent-1")})
	_ = planOAuth.Set(ctx, &PaperclipAppPermissionResourceModel{
		CompanyID:     types.StringValue("comp-1"),
		ConnectionID:  types.StringValue("conn-oauth"),
		ToolSelection: types.StringValue("all"),
		AgentIDs:      agentList,
	})
	var modOAuth resource.ModifyPlanResponse
	modOAuth.Plan = planOAuth
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: planOAuth}, &modOAuth)

	// ModifyPlan success
	planActive := tfsdk.Plan{Schema: sResp.Schema}
	_ = planActive.Set(ctx, &PaperclipAppPermissionResourceModel{
		CompanyID:     types.StringValue("comp-1"),
		ConnectionID:  types.StringValue("conn-active"),
		ToolSelection: types.StringValue("all"),
		AgentIDs:      agentList,
	})
	var modActive resource.ModifyPlanResponse
	modActive.Plan = planActive
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: planActive}, &modActive)

	// apply with invalid accessType
	var d diag.Diagnostics
	r.apply(ctx, &PaperclipAppPermissionResourceModel{
		AccessType: types.StringValue("company"),
	}, &d)
	if !d.HasError() {
		t.Fatal("expected error on non-agents accessType")
	}

	// revoke
	_ = r.revoke(ctx, "comp-1", "conn-active")
}
