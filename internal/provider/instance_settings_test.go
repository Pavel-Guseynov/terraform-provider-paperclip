package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

const settingsEnvironmentID = "11111111-1111-4111-8111-111111111111"

type settingsFixture struct {
	root                                       map[string]interface{}
	health                                     map[string]interface{}
	environment                                map[string]interface{}
	writes                                     []string
	bodies                                     []map[string]interface{}
	failPath, failMethod                       string
	readbackFails, ignorePatch                 bool
	ownershipAfterPatch, healthFailsAfterPatch bool
}

func newSettingsFixture(t *testing.T) (*PaperclipInstanceSettingsResource, *settingsFixture, tfsdk.Plan) {
	t.Helper()
	f := &settingsFixture{
		root:        map[string]interface{}{"id": "settings-id", "defaultEnvironmentId": settingsEnvironmentID, "general": map[string]interface{}{"censorUsernameInLogs": false, "keyboardShortcuts": false, "feedbackDataSharingPreference": "prompt", "backupRetention": map[string]interface{}{"dailyDays": float64(7), "weeklyWeeks": float64(4), "monthlyMonths": float64(1)}, "executionMode": "any"}, "experimental": map[string]interface{}{"enableEnvironments": false, "enableCases": true, "enableApps": true, "worktreeRunExecutionActivatedAt": nil}},
		health:      map[string]interface{}{"status": "ok"},
		environment: map[string]interface{}{"id": settingsEnvironmentID, "name": "Remote", "status": "active", "driver": "ssh", "config": map[string]interface{}{}, "envVars": map[string]interface{}{}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if (req.URL.Path == f.failPath && req.Method == f.failMethod) || (f.readbackFails && len(f.writes) > 0 && req.Method == "GET") || (f.healthFailsAfterPatch && len(f.writes) > 0 && req.URL.Path == "/api/health") {
			w.WriteHeader(403)
			_, _ = w.Write([]byte(`{"error":"Instance admin required"}`))
			return
		}
		if req.Method == "GET" {
			switch req.URL.Path {
			case "/api/instance/settings":
				_ = json.NewEncoder(w).Encode(f.root)
			case "/api/health":
				_ = json.NewEncoder(w).Encode(f.health)
			case "/api/environments/" + settingsEnvironmentID:
				_ = json.NewEncoder(w).Encode(f.environment)
			default:
				w.WriteHeader(404)
				_, _ = w.Write([]byte(`{"error":"Environment not found"}`))
			}
			return
		}
		if req.Method != "PATCH" {
			t.Errorf("unexpected mutation %s", req.Method)
		}
		f.writes = append(f.writes, req.URL.Path)
		if f.ownershipAfterPatch {
			f.health["hiddenSettings"] = []string{"instance.general.keyboardShortcuts"}
		}
		var body map[string]interface{}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		f.bodies = append(f.bodies, body)
		target := f.root
		if strings.HasSuffix(req.URL.Path, "/general") {
			target = f.root["general"].(map[string]interface{})
		}
		if strings.HasSuffix(req.URL.Path, "/experimental") {
			target = f.root["experimental"].(map[string]interface{})
		}
		if !f.ignorePatch {
			for key, value := range body {
				target[key] = value
			}
		}
		_ = json.NewEncoder(w).Encode(target)
	}))
	t.Cleanup(server.Close)
	r := NewPaperclipInstanceSettingsResource().(*PaperclipInstanceSettingsResource)
	s := configureTestResource(t, server.URL, r)
	plan := tfsdk.Plan{Schema: s}
	setSettingsPlan(t, &plan, instanceSettingsModel{DefaultEnvironmentID: types.StringValue(settingsEnvironmentID), General: types.StringValue(`{"censorUsernameInLogs":true,"keyboardShortcuts":true,"feedbackDataSharingPreference":"not_allowed","backupRetention":{"dailyDays":14,"weeklyWeeks":2,"monthlyMonths":3},"executionMode":"any"}`), Experimental: types.StringValue(`{"enableEnvironments":true}`)})
	return r, f, plan
}
func setSettingsPlan(t *testing.T, p *tfsdk.Plan, m instanceSettingsModel) {
	t.Helper()
	if d := p.Set(context.Background(), &m); d.HasError() {
		t.Fatal(d)
	}
}
func TestInstanceSettingsRegistered(t *testing.T) {
	for _, factory := range New("test")().(*PaperclipProvider).Resources(context.Background()) {
		var m resource.MetadataResponse
		factory().Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "paperclip"}, &m)
		if m.TypeName == "paperclip_instance_settings" {
			return
		}
	}
	t.Fatal("instance settings resource is absent")
}
func TestInstanceSettingsValidation(t *testing.T) {
	r, _, p := newSettingsFixture(t)
	ctx := context.Background()
	for _, tc := range []struct{ section, raw, want string }{
		{"general", `[]`, "JSON object"}, {"general", `null`, "JSON object"}, {"general", `{`, "JSON object"},
		{"general", `{"unknown":true}`, "unsupported general"}, {"general", `{"censorUsernameInLogs":1}`, "unsupported general"},
		{"general", `{"keyboardShortcuts":null}`, "unsupported general"}, {"general", `{"feedbackDataSharingPreference":"invalid"}`, "unsupported general"},
		{"general", `{"executionMode":"cloud"}`, "unsupported general"}, {"general", `{"backupRetention":{}}`, "unsupported general"},
		{"general", `{"backupRetention":{"dailyDays":3,"weeklyWeeks":1,"monthlyMonths":6}}`, ""},
		{"general", `{"backupRetention":{"dailyDays":0,"weeklyWeeks":4,"monthlyMonths":1}}`, "unsupported general"},
		{"experimental", `{"unknown":true}`, "unsupported experimental"}, {"experimental", `{"enableApps":true}`, "unsupported experimental"},
		{"experimental", `{"worktreeRunExecutionActivatedAt":null}`, "unsupported experimental"}, {"experimental", `{"enableEnvironments":"true"}`, "unsupported experimental"},
		{"experimental", `{"enableCases":false}`, ""},
	} {
		t.Run(tc.section+tc.raw, func(t *testing.T) {
			m := instanceSettingsModel{}
			if tc.section == "general" {
				m.General = types.StringValue(tc.raw)
			} else {
				m.Experimental = types.StringValue(tc.raw)
			}
			setSettingsPlan(t, &p, m)
			resp := resource.ValidateConfigResponse{}
			r.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: tfsdk.Config{Schema: p.Schema, Raw: p.Raw}}, &resp)
			if resp.Diagnostics.HasError() != (tc.want != "") || tc.want != "" && !strings.Contains(fmt.Sprint(resp.Diagnostics), tc.want) {
				t.Fatal(resp.Diagnostics)
			}
		})
	}
	for _, id := range []string{"", settingsEnvironmentID, "bad"} {
		m := instanceSettingsModel{DefaultEnvironmentID: types.StringValue(id)}
		_, err := settingsBodies(m)
		if (err != nil) != (id == "bad") {
			t.Fatal(id, err)
		}
	}
	for _, value := range []types.String{types.StringNull(), types.StringUnknown()} {
		if _, err := settingsBodies(instanceSettingsModel{General: value, Experimental: value, DefaultEnvironmentID: value}); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{`{"censorUsernameInLogs":false}`, `{"keyboardShortcuts":false}`, `{"feedbackDataSharingPreference":"allowed"}`, `{"feedbackDataSharingPreference":"prompt"}`, `{"executionMode":"kubernetes"}`} {
		if _, err := settingsObject(types.StringValue(raw), "general"); err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range strings.Fields(writableExperimental) {
		if _, err := settingsObject(types.StringValue(fmt.Sprintf(`{"%s":true}`, key)), "experimental"); err != nil {
			t.Fatal(err)
		}
	}
}
func TestInstanceSettingsLifecycleOwnsOnlyDeclaredKeys(t *testing.T) {
	r, f, p := newSettingsFixture(t)
	ctx := context.Background()
	created := resource.CreateResponse{State: tfsdk.State{Schema: p.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: p}, &created)
	if created.Diagnostics.HasError() || len(f.writes) != 3 {
		t.Fatal(created.Diagnostics, f.writes)
	}
	if len(f.bodies[0]) != 1 || f.bodies[0]["defaultEnvironmentId"] != settingsEnvironmentID || len(f.bodies[1]) != 5 || len(f.bodies[2]) != 1 || f.bodies[2]["enableEnvironments"] != true {
		t.Fatal("PATCH included undeclared keys or omitted declared values", f.bodies)
	}
	var m instanceSettingsModel
	if d := created.State.Get(ctx, &m); d.HasError() {
		t.Fatal(d)
	}
	if m.ID.ValueString() != "instance" || !strings.Contains(m.General.ValueString(), `"censorUsernameInLogs":true`) || m.Experimental.ValueString() != `{"enableEnvironments":true}` {
		t.Fatal(m)
	}
	if f.root["experimental"].(map[string]interface{})["enableCases"] != true {
		t.Fatal("undeclared flag changed")
	}
	f.root["general"].(map[string]interface{})["keyboardShortcuts"] = false
	read := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatal(read.Diagnostics)
	}
	_ = read.State.Get(ctx, &m)
	if !strings.Contains(m.General.ValueString(), `"keyboardShortcuts":false`) {
		t.Fatal("remote drift hidden", m)
	}
	setSettingsPlan(t, &p, instanceSettingsModel{ID: types.StringValue("instance"), DefaultEnvironmentID: types.StringValue(""), General: types.StringValue(`{}`)})
	updated := resource.UpdateResponse{State: read.State}
	r.Update(ctx, resource.UpdateRequest{Plan: p}, &updated)
	if updated.Diagnostics.HasError() || f.root["defaultEnvironmentId"] != nil {
		t.Fatal(updated.Diagnostics)
	}
	_ = updated.State.Get(ctx, &m)
	if m.General.ValueString() != `{}` || !m.Experimental.IsNull() {
		t.Fatal("removed keys still owned", m)
	}
	r.Delete(ctx, resource.DeleteRequest{}, &resource.DeleteResponse{})
	if len(f.writes) != 4 {
		t.Fatal("destroy wrote settings")
	}
}
func TestInstanceSettingsPlanRejectsDeploymentOwnersAndInvalidReferences(t *testing.T) {
	for _, mode := range []string{"managed", "hidden general", "hidden experimental", "hidden page", "cloud", "archived", "fake", "missing", "bad UUID", "unknown flag", "unknown general", "missing flag", "missing general", "read denied", "health denied", "incomplete", "unknown", "valid"} {
		t.Run(mode, func(t *testing.T) {
			r, f, p := newSettingsFixture(t)
			ctx := context.Background()
			var m instanceSettingsModel
			_ = p.Get(ctx, &m)
			want := ""
			switch mode {
			case "managed":
				f.root["experimental"].(map[string]interface{})["managedKeys"] = map[string]interface{}{"enableEnvironments": map[string]interface{}{"managed": true, "managedBy": "paperclip-cloud"}}
				want = "managedBy paperclip-cloud"
			case "hidden general":
				f.health["hiddenSettings"] = []string{"instance.general.keyboardShortcuts"}
				want = "hosting operator (hidden)"
			case "hidden experimental":
				f.health["hiddenSettings"] = []string{"instance.experimental.enableEnvironments"}
				want = "hosting operator (hidden)"
			case "hidden page":
				f.health["hiddenSettings"] = []string{"instance.experimental"}
				want = "hosting operator (hidden)"
			case "cloud":
				f.health["cloud"] = map[string]interface{}{"managed": true, "managedBy": "paperclip-cloud"}
				want = "platform-managed"
			case "archived":
				f.environment["status"] = "archived"
				want = "archived"
			case "fake":
				f.environment["driver"] = "sandbox"
				f.environment["config"] = map[string]interface{}{"provider": "fake"}
				want = "fake"
			case "missing":
				m.DefaultEnvironmentID = types.StringValue("22222222-2222-4222-8222-222222222222")
				want = "invalid default environment"
			case "bad UUID":
				m.DefaultEnvironmentID = types.StringValue("bad")
				want = "UUID"
			case "unknown flag":
				m.Experimental = types.StringValue(`{"inventedFlag":true}`)
				want = "unsupported experimental"
			case "unknown general":
				m.General = types.StringValue(`{"inventedSetting":true}`)
				want = "unsupported general"
			case "missing flag":
				delete(f.root["experimental"].(map[string]interface{}), "enableEnvironments")
				want = "target API does not expose experimental.enableEnvironments"
			case "missing general":
				delete(f.root["general"].(map[string]interface{}), "keyboardShortcuts")
				want = "target API does not expose general.keyboardShortcuts"
			case "read denied":
				f.failPath, f.failMethod = "/api/instance/settings", "GET"
				want = "Instance admin required"
			case "health denied":
				f.failPath, f.failMethod = "/api/health", "GET"
				want = "Instance admin required"
			case "incomplete":
				delete(f.root, "general")
				want = "incomplete"
			case "unknown":
				m.DefaultEnvironmentID, m.General, m.Experimental = types.StringUnknown(), types.StringUnknown(), types.StringUnknown()
			}
			setSettingsPlan(t, &p, m)
			resp := resource.ModifyPlanResponse{}
			r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: p}, &resp)
			if resp.Diagnostics.HasError() != (want != "") || want != "" && !strings.Contains(fmt.Sprint(resp.Diagnostics), want) || len(f.writes) != 0 {
				t.Fatal(resp.Diagnostics, f.writes)
			}
			if want != "" {
				create := resource.CreateResponse{State: tfsdk.State{Schema: p.Schema}}
				r.Create(ctx, resource.CreateRequest{Plan: p}, &create)
				if !create.Diagnostics.HasError() || len(f.writes) != 0 {
					t.Fatal("apply skipped preflight", create.Diagnostics)
				}
			}
		})
	}
}
func TestInstanceSettingsImportAdoptsOnlyWritableUnownedValues(t *testing.T) {
	r, f, p := newSettingsFixture(t)
	ctx := context.Background()
	f.health["hiddenSettings"] = []string{"instance.general.keyboardShortcuts"}
	f.root["experimental"].(map[string]interface{})["managedKeys"] = map[string]interface{}{"enableCases": map[string]interface{}{"managed": true, "managedBy": "paperclip-cloud"}}
	setSettingsPlan(t, &p, instanceSettingsModel{})
	imported := resource.ImportStateResponse{State: tfsdk.State{Schema: p.Schema, Raw: p.Raw}}
	r.ImportState(ctx, resource.ImportStateRequest{ID: "instance"}, &imported)
	if imported.Diagnostics.HasError() {
		t.Fatal(imported.Diagnostics)
	}
	read := resource.ReadResponse{State: imported.State}
	r.Read(ctx, resource.ReadRequest{State: imported.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatal(read.Diagnostics)
	}
	var m instanceSettingsModel
	_ = read.State.Get(ctx, &m)
	if strings.Contains(m.General.ValueString(), "keyboardShortcuts") || m.Experimental.ValueString() != `{"enableEnvironments":false}` || m.DefaultEnvironmentID.ValueString() != settingsEnvironmentID {
		t.Fatal(m)
	}
	setSettingsPlan(t, &p, m)
	plan := resource.ModifyPlanResponse{}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: p}, &plan)
	reread := resource.ReadResponse{State: read.State}
	r.Read(ctx, resource.ReadRequest{State: read.State}, &reread)
	if plan.Diagnostics.HasError() || reread.Diagnostics.HasError() || !reread.State.Raw.Equal(p.Raw) || len(f.writes) != 0 {
		t.Fatal("import changed settings", plan.Diagnostics, reread.Diagnostics)
	}
	invalid := resource.ImportStateResponse{}
	r.ImportState(ctx, resource.ImportStateRequest{ID: "other"}, &invalid)
	if !invalid.Diagnostics.HasError() {
		t.Fatal("invalid import accepted")
	}
}
func TestInstanceSettingsFailuresRetainStateAndExposeOwnershipDrift(t *testing.T) {
	for _, mode := range []string{"patch", "readback", "read", "health", "bad state"} {
		t.Run(mode, func(t *testing.T) {
			r, f, p := newSettingsFixture(t)
			ctx := context.Background()
			var m instanceSettingsModel
			_ = p.Get(ctx, &m)
			m.ID = types.StringValue("instance")
			setSettingsPlan(t, &p, m)
			state := tfsdk.State{Schema: p.Schema, Raw: p.Raw}
			switch mode {
			case "patch":
				f.failPath, f.failMethod = "/api/instance/settings/general", "PATCH"
			case "readback":
				f.readbackFails = true
			case "read":
				f.failPath, f.failMethod = "/api/instance/settings", "GET"
			case "health":
				f.failPath, f.failMethod = "/api/health", "GET"
			case "bad state":
				_ = state.SetAttribute(ctx, path.Root("general"), types.StringValue("[]"))
			}
			if mode == "patch" || mode == "readback" {
				out := resource.UpdateResponse{State: state}
				r.Update(ctx, resource.UpdateRequest{Plan: p}, &out)
				if !out.Diagnostics.HasError() || out.State.Raw.IsNull() {
					t.Fatal(out.Diagnostics)
				}
			} else {
				out := resource.ReadResponse{State: state}
				r.Read(ctx, resource.ReadRequest{State: state}, &out)
				if !out.Diagnostics.HasError() || !out.State.Raw.Equal(state.Raw) {
					t.Fatal(out.Diagnostics)
				}
			}
		})
	}
}
func TestInstanceSettingsOwnershipMetadataFailsClosed(t *testing.T) {
	_, f, _ := newSettingsFixture(t)
	for _, metadata := range []interface{}{nil, map[string]interface{}{}, map[string]interface{}{"managedBy": ""}} {
		f.root["experimental"].(map[string]interface{})["managedKeys"] = map[string]interface{}{"enableCases": metadata}
		// A listed key remains managed even if its owner metadata is malformed.
		raw, _ := json.Marshal(f.root)
		var remote client.InstanceSettings
		_ = json.Unmarshal(raw, &remote)
		health := &client.HealthResponse{}
		if got := settingOwner(health, &remote, "experimental", "enableCases"); got != "deployment (managedKeys)" {
			t.Fatal(got)
		}
	}
}

func TestInstanceSettingsReadbackNeverClaimsOverriddenValuesConverged(t *testing.T) {
	for _, mode := range []string{"ignored", "ownership changed", "health failure"} {
		t.Run(mode, func(t *testing.T) {
			r, f, p := newSettingsFixture(t)
			switch mode {
			case "ignored":
				f.ignorePatch = true
			case "ownership changed":
				f.ownershipAfterPatch = true
			case "health failure":
				f.healthFailsAfterPatch = true
			}
			out := resource.CreateResponse{State: tfsdk.State{Schema: p.Schema}}
			r.Create(context.Background(), resource.CreateRequest{Plan: p}, &out)
			want := "did not converge"
			if mode == "ownership changed" {
				want = "managedBy"
			}
			if mode == "health failure" {
				want = "ownership readback failed"
			}
			if !out.Diagnostics.HasError() || !strings.Contains(fmt.Sprint(out.Diagnostics), want) || out.State.Raw.IsNull() {
				t.Fatal(out.Diagnostics)
			}
		})
	}
}

func TestInstanceSettingsProtocolErrorsDoNotMutate(t *testing.T) {
	r, f, p := newSettingsFixture(t)
	ctx := context.Background()
	r.Configure(ctx, resource.ConfigureRequest{}, &resource.ConfigureResponse{})
	configure := resource.ConfigureResponse{}
	(&PaperclipInstanceSettingsResource{}).Configure(ctx, resource.ConfigureRequest{ProviderData: "wrong"}, &configure)
	if !configure.Diagnostics.HasError() {
		t.Fatal("wrong client accepted")
	}
	for _, rr := range []*PaperclipInstanceSettingsResource{r, {}} {
		response := resource.ModifyPlanResponse{}
		rr.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: tfsdk.Plan{Schema: p.Schema}}, &response)
		if response.Diagnostics.HasError() {
			t.Fatal(response.Diagnostics)
		}
	}
	invalid := p
	invalid.Raw = tftypes.NewValue(tftypes.String, "invalid resource object")
	validation := resource.ValidateConfigResponse{}
	r.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: tfsdk.Config{Schema: p.Schema, Raw: invalid.Raw}}, &validation)
	modify := resource.ModifyPlanResponse{}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: invalid}, &modify)
	read := resource.ReadResponse{}
	r.Read(ctx, resource.ReadRequest{State: tfsdk.State{Schema: p.Schema, Raw: invalid.Raw}}, &read)
	create := resource.CreateResponse{State: tfsdk.State{Schema: p.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: invalid}, &create)
	if !validation.Diagnostics.HasError() || !modify.Diagnostics.HasError() || !read.Diagnostics.HasError() || !create.Diagnostics.HasError() {
		t.Fatal("malformed protocol values accepted")
	}
	var m instanceSettingsModel
	_ = p.Get(ctx, &m)
	m.General = types.StringUnknown()
	setSettingsPlan(t, &p, m)
	create = resource.CreateResponse{State: tfsdk.State{Schema: p.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: p}, &create)
	if !create.Diagnostics.HasError() || !strings.Contains(fmt.Sprint(create.Diagnostics), "Unknown instance settings at apply") {
		t.Fatal(create.Diagnostics)
	}
	m.General = types.StringValue(`{}`)
	setSettingsPlan(t, &p, m)
	create = resource.CreateResponse{State: tfsdk.State{Schema: schema.Schema{}}}
	r.Create(ctx, resource.CreateRequest{Plan: p}, &create)
	if !create.Diagnostics.HasError() || len(f.writes) > 0 {
		t.Fatal("state save failed without preventing mutation", create.Diagnostics)
	}
}

func TestInstanceSettingsCanRelinquishKeysAfterDeploymentTakesOwnership(t *testing.T) {
	r, f, p := newSettingsFixture(t)
	ctx := context.Background()
	var m instanceSettingsModel
	_ = p.Get(ctx, &m)
	m.ID = types.StringValue("instance")
	setSettingsPlan(t, &p, m)
	f.health["hiddenSettings"] = []string{"instance.experimental"}
	state := tfsdk.State{Schema: p.Schema, Raw: p.Raw}
	read := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &read)
	if read.Diagnostics.HasError() {
		t.Fatal("refresh prevents relinquishing newly managed keys", read.Diagnostics)
	}
	m.Experimental = types.StringNull()
	setSettingsPlan(t, &p, m)
	plan := resource.ModifyPlanResponse{}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: p}, &plan)
	if plan.Diagnostics.HasError() || len(f.writes) > 0 {
		t.Fatal(plan.Diagnostics)
	}
}

func TestInstanceSettingsAbsentExecutionModeMeansAny(t *testing.T) {
	r, f, p := newSettingsFixture(t)
	ctx := context.Background()
	delete(f.root["general"].(map[string]interface{}), "executionMode")
	state := tfsdk.State{Schema: p.Schema, Raw: p.Raw}
	read := resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, &read)
	var m instanceSettingsModel
	_ = read.State.Get(ctx, &m)
	if read.Diagnostics.HasError() || !strings.Contains(m.General.ValueString(), `"executionMode":"any"`) {
		t.Fatal(m, read.Diagnostics)
	}
}

func TestInstanceSettingsPartialGeneralPatchPreservesUndeclaredSettings(t *testing.T) {
	r, f, p := newSettingsFixture(t)
	ctx := context.Background()
	setSettingsPlan(t, &p, instanceSettingsModel{General: types.StringValue(`{"keyboardShortcuts":true}`)})
	result := resource.CreateResponse{State: tfsdk.State{Schema: p.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: p}, &result)
	if result.Diagnostics.HasError() || len(f.bodies) != 1 || f.writes[0] != "/api/instance/settings/general" || len(f.bodies[0]) != 1 || f.bodies[0]["keyboardShortcuts"] != true || f.root["general"].(map[string]interface{})["censorUsernameInLogs"] != false {
		t.Fatal(result.Diagnostics, f.bodies)
	}
}

func TestInstanceSettingsEveryWritableFlagAppliesAndReadsBack(t *testing.T) {
	for _, key := range strings.Fields(writableExperimental) {
		t.Run(key, func(t *testing.T) {
			r, f, plan := newSettingsFixture(t)
			ctx := context.Background()
			f.root["experimental"].(map[string]interface{})[key] = false
			declaration := fmt.Sprintf(`{"%s":true}`, key)
			setSettingsPlan(t, &plan, instanceSettingsModel{Experimental: types.StringValue(declaration)})
			result := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
			r.Create(ctx, resource.CreateRequest{Plan: plan}, &result)
			var observed instanceSettingsModel
			diagnostics := result.State.Get(ctx, &observed)
			if result.Diagnostics.HasError() || diagnostics.HasError() || observed.Experimental.ValueString() != declaration || f.root["experimental"].(map[string]interface{})[key] != true {
				t.Fatal("flag did not apply and read back", result.Diagnostics, diagnostics, observed)
			}
			if len(f.bodies) != 1 || f.writes[0] != "/api/instance/settings/experimental" || len(f.bodies[0]) != 1 || f.bodies[0][key] != true {
				t.Fatal("flag PATCH did not contain exactly the declared key", f.writes, f.bodies)
			}
		})
	}
}
