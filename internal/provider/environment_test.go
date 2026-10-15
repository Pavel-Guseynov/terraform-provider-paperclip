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

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestEnvironmentIsRegistered(t *testing.T) {
	for _, factory := range New("test")().(*PaperclipProvider).Resources(context.Background()) {
		var response resource.MetadataResponse
		factory().Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "paperclip"}, &response)
		if response.TypeName == "paperclip_environment" {
			return
		}
	}
	t.Fatal("paperclip_environment is missing: native environment lifecycle cannot be managed")
}

func TestEnvironmentNativeDriverConfig(t *testing.T) {
	ssh := `"host":"host","username":"user","remoteWorkspacePath":"/work"`
	for _, tc := range []struct{ name, driver, raw, errorText string }{
		{"local", "local", `{}`, ""},
		{"ssh defaults", "ssh", `{` + ssh + `}`, ""},
		{"ssh reference", "ssh", `{` + ssh + `,"knownHosts":" host key ","privateKeySecretRef":{"type":"secret_ref","secretId":"11111111-1111-4111-8111-111111111111"}}`, ""},
		{"ssh version", "ssh", `{` + ssh + `,"privateKeySecretRef":{"type":"secret_ref","secretId":"11111111-1111-4111-8111-111111111111","version":2}}`, ""},
		{"bad json", "local", `{`, "JSON object"},
		{"null", "local", `null`, "JSON object"},
		{"array", "local", `[]`, "JSON object"},
		{"driver", "invalid", `{}`, "driver must be"},
		{"ssh unknown", "ssh", `{"unexpected":1}`, "unknown SSH"},
		{"ssh missing", "ssh", `{}`, "SSH requires"},
		{"ssh relative path", "ssh", `{"host":"h","username":"u","remoteWorkspacePath":"relative"}`, "absolute"},
		{"ssh plaintext", "ssh", `{` + ssh + `,"privateKey":"must-not-send"}`, "plaintext privateKey"},
		{"ssh port zero", "ssh", `{` + ssh + `,"port":0}`, "SSH port"},
		{"ssh port range", "ssh", `{` + ssh + `,"port":65536}`, "SSH port"},
		{"ssh port type", "ssh", `{` + ssh + `,"port":true}`, "SSH port"},
		{"ssh fraction", "ssh", `{` + ssh + `,"port":22.5}`, "SSH port"},
		{"ssh boolean", "ssh", `{` + ssh + `,"strictHostKeyChecking":1}`, "boolean"},
		{"ssh known hosts type", "ssh", `{` + ssh + `,"knownHosts":1}`, "knownHosts"},
		{"ssh empty hosts", "ssh", `{` + ssh + `,"knownHosts":" "}`, ""},
		{"ssh reference type", "ssh", `{` + ssh + `,"privateKeySecretRef":"secret"}`, "secret_ref object"},
		{"ssh reference kind", "ssh", `{` + ssh + `,"privateKeySecretRef":{"type":"plain"}}`, "secret_ref object"},
		{"ssh reference extra", "ssh", `{` + ssh + `,"privateKeySecretRef":{"type":"secret_ref","value":"secret"}}`, "unknown privateKeySecretRef"},
		{"ssh reference identity", "ssh", `{` + ssh + `,"privateKeySecretRef":{"type":"secret_ref","secretId":"bad"}}`, "secret UUID"},
		{"ssh reference bad version", "ssh", `{` + ssh + `,"privateKeySecretRef":{"type":"secret_ref","secretId":"11111111-1111-4111-8111-111111111111","version":"2"}}`, "positive integer"},
		{"sandbox", "sandbox", `{"provider":"installed-provider","timeoutMs":1,"runnerIdleTimeoutMs":1000,"runnerLifecycleMode":"warm","streamRunLogs":true,"archiveOnRelease":false,"custom":"value"}`, ""},
		{"sandbox max", "sandbox", `{"provider":"installed-provider","timeoutMs":86400000,"runnerIdleTimeoutMs":86400000,"runnerLifecycleMode":"per_turn"}`, ""},
		{"sandbox inherit", "sandbox", `{"provider":"installed-provider","runnerLifecycleMode":"inherit"}`, ""},
		{"sandbox missing", "sandbox", `{}`, "installed provider"},
		{"sandbox fake", "sandbox", `{"provider":"fake"}`, "cannot be saved"},
		{"sandbox provider invalid", "sandbox", `{"provider":"INVALID"}`, "provider key"},
		{"sandbox bool invalid", "sandbox", `{"provider":"installed","reuseLease":"false"}`, "boolean"},
		{"sandbox numeric invalid", "sandbox", `{"provider":"installed","timeoutMs":0}`, "integer"},
		{"sandbox mode invalid", "sandbox", `{"provider":"installed","runnerLifecycleMode":"other"}`, "runnerLifecycleMode"},
		{"sandbox obsolete", "sandbox", `{"provider":"installed","streamAgentSessionOutput":true}`, "obsolete"},
		{"plugin defaults", "plugin", `{"pluginKey":"@example/driver","driverKey":"remote"}`, ""},
		{"plugin config", "plugin", `{"pluginKey":"@example/driver","driverKey":"remote","driverConfig":{"region":"test"}}`, ""},
		{"plugin extra", "plugin", `{"other":1}`, "unknown plugin"},
		{"plugin missing", "plugin", `{}`, "plugin requires"},
		{"plugin driver invalid", "plugin", `{"pluginKey":"p","driverKey":"INVALID"}`, "valid driverKey"},
		{"plugin config type", "plugin", `{"pluginKey":"p","driverKey":"d","driverConfig":[]}`, "must be an object"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := environmentConfig(tc.driver, tc.raw)
			if tc.errorText != "" {
				if err == nil || !strings.Contains(err.Error(), tc.errorText) {
					t.Fatalf("wanted %q, got %v", tc.errorText, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "ssh defaults" {
				if got["port"] != float64(22) || got["strictHostKeyChecking"] != true || got["privateKey"] != nil || got["privateKeySecretRef"] != nil || got["knownHosts"] != nil {
					t.Fatalf("incorrect native defaults: %#v", got)
				}
			}
			if tc.name == "ssh reference" && got["privateKeySecretRef"].(map[string]interface{})["version"] != "latest" {
				t.Fatal("reference default must be latest")
			}
			if tc.driver == "sandbox" && got["reuseLease"] != false {
				t.Fatal("native reuseLease default is false")
			}
			if tc.name == "plugin defaults" && !reflect.DeepEqual(got["driverConfig"], map[string]interface{}{}) {
				t.Fatal("native driverConfig default must be an object")
			}
		})
	}
}

func environmentModel() PaperclipEnvironmentModel {
	return PaperclipEnvironmentModel{CompanyID: types.StringValue("company"), Name: types.StringValue("Remote"), Driver: types.StringValue("sandbox"), Status: types.StringValue("active"), Config: types.StringValue(`{"provider":"installed","reuseLease":false}`)}
}

type environmentFixture struct {
	remote                 client.PaperclipEnvironment
	requests               []string
	writes                 []map[string]interface{}
	failMethod, failReason string
}

func newEnvironmentFixture(t *testing.T) (*PaperclipEnvironmentResource, *environmentFixture, tfsdk.Plan) {
	t.Helper()
	f := &environmentFixture{remote: client.PaperclipEnvironment{ID: "env-id", Name: "Remote", Driver: "sandbox", Status: "active", Config: map[string]interface{}{"provider": "installed", "reuseLease": false}, EnvVars: map[string]interface{}{}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		f.requests = append(f.requests, req.Method+" "+req.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		if req.Method == f.failMethod {
			status := 403
			if f.failReason == "Environment not found" {
				status = 404
			}
			if strings.Contains("|managed_local|instance_default|pending_sandbox_cleanup|reusable_sandbox_lease|", "|"+f.failReason+"|") {
				status = 409
			}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": "Environment operation rejected", "details": map[string]interface{}{"deleteBlockedReasons": []string{f.failReason}}})
			return
		}
		switch {
		case req.Method == "GET" && req.URL.Path == "/api/companies/company/secrets":
			_, _ = w.Write([]byte(`[{"id":"11111111-1111-4111-8111-111111111111","companyId":"company","name":"Fixture secret"}]`))
		case req.Method == "GET" && strings.HasPrefix(req.URL.Path, "/api/companies/") && strings.HasSuffix(req.URL.Path, "/environments"):
			_ = json.NewEncoder(w).Encode([]client.PaperclipEnvironment{f.remote})
		case req.Method == "GET" && req.URL.Path == "/api/environments/env-id":
			_ = json.NewEncoder(w).Encode(f.remote)
		case req.Method == "GET" && req.URL.Path == "/api/environments/other-id":
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"error":"Environment not found"}`))
		case req.Method == "POST" || req.Method == "PATCH":
			if req.Method == "POST" && req.URL.Path != "/api/companies/company/environments" {
				t.Error("wrong create endpoint")
			}
			if req.Method == "PATCH" && (req.URL.Path != "/api/environments/env-id" || req.URL.Query().Get("companyId") != "company") {
				t.Error("missing native secret context")
			}
			var body map[string]interface{}
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if _, exists := body["companyId"]; exists {
				t.Error("invented company ownership")
			}
			f.writes = append(f.writes, body)
			if config, ok := body["config"].(map[string]interface{}); ok {
				if req.Method == "POST" || f.remote.Driver != body["driver"] {
					f.remote.Config = map[string]interface{}{}
				}
				for key, value := range config {
					f.remote.Config[key] = value
				}
			}
			f.remote.Name, f.remote.Driver, f.remote.Status = body["name"].(string), body["driver"].(string), body["status"].(string)
			f.remote.Description = nil
			if value, ok := body["description"].(string); ok {
				f.remote.Description = &value
			}
			f.remote.Metadata, _ = body["metadata"].(map[string]interface{})
			f.remote.EnvVars = body["envVars"].(map[string]interface{})
			for _, value := range f.remote.EnvVars {
				binding := value.(map[string]interface{})
				if binding["type"] == "secret_ref" || binding["type"] == "user_secret_ref" {
					if _, ok := binding["version"]; !ok {
						binding["version"] = "latest"
					}
				}
				if binding["type"] == "secret_ref" {
					if _, ok := binding["projectionClass"]; !ok {
						binding["projectionClass"] = "unclassified"
					}
					if _, ok := binding["projectionAllowlistKey"]; !ok {
						binding["projectionAllowlistKey"] = nil
					}
				}
				if binding["type"] == "user_secret_ref" {
					if _, ok := binding["required"]; !ok {
						binding["required"] = true
					}
					if _, ok := binding["allowMissingOverride"]; !ok {
						binding["allowMissingOverride"] = false
					}
				}
			}
			_ = json.NewEncoder(w).Encode(f.remote)
		case req.Method == "DELETE" && req.URL.Path == "/api/environments/env-id":
			_, _ = w.Write([]byte(`{"success":true}`))
		default:
			t.Errorf("unexpected request %s %s", req.Method, req.URL.Path)
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(server.Close)
	r := NewPaperclipEnvironmentResource().(*PaperclipEnvironmentResource)
	s := configureTestResource(t, server.URL, r)
	plan := tfsdk.Plan{Schema: s}
	m := environmentModel()
	if d := plan.Set(context.Background(), &m); d.HasError() {
		t.Fatal(d)
	}
	return r, f, plan
}

func TestEnvironmentLifecyclePreservesIdentityAndRemoteDrift(t *testing.T) {
	ctx := context.Background()
	r, f, plan := newEnvironmentFixture(t)
	model := environmentModel()
	model.Description = types.StringValue("")
	model.Metadata = types.StringValue(`{"owner":"test"}`)
	model.Env = map[string]EnvBindingModel{"EDITOR": {Type: types.StringValue("plain"), Value: types.StringValue("vi")}}
	if d := plan.Set(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	created := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
	if created.Diagnostics.HasError() {
		t.Fatal(created.Diagnostics)
	}
	if len(f.writes) != 1 || f.writes[0]["description"] != "" {
		t.Fatal("create lost empty description")
	}
	if d := created.State.Get(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	if model.ID.ValueString() != "env-id" || model.Config.ValueString() != environmentModel().Config.ValueString() {
		t.Fatal("create failed identity/config readback")
	}
	f.remote.Name = "Drifted"
	f.remote.Config["timeoutMs"] = float64(1000)
	f.remote.EnvVars["REMOTE"] = "remote-value"
	read := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatal(read.Diagnostics)
	}
	var drift PaperclipEnvironmentModel
	if d := read.State.Get(ctx, &drift); d.HasError() {
		t.Fatal(d)
	}
	if drift.Name.ValueString() != "Drifted" || !strings.Contains(drift.Config.ValueString(), "timeoutMs") || drift.Env["REMOTE"].Value.ValueString() != "remote-value" {
		t.Fatal("remote drift hidden")
	}
	model.Config = drift.Config
	model.Status = types.StringValue("archived")
	model.Description, model.Metadata, model.Env = types.StringNull(), types.StringNull(), nil
	if d := plan.Set(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	update := resource.UpdateResponse{State: created.State}
	r.Update(ctx, resource.UpdateRequest{Plan: plan}, &update)
	if update.Diagnostics.HasError() {
		t.Fatal(update.Diagnostics)
	}
	if f.remote.Status != "archived" || f.remote.ID != "env-id" || f.remote.Description != nil || f.remote.Metadata != nil || len(f.remote.EnvVars) != 0 {
		t.Fatal("update did not clear supported fields or preserve identity")
	}
	if _, exists := f.writes[1]["config"]; exists {
		t.Fatal("unchanged config was unnecessarily patched")
	}
	deleted := resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: update.State}, &deleted)
	if deleted.Diagnostics.HasError() || f.requests[len(f.requests)-1] != "DELETE /api/environments/env-id" {
		t.Fatal("native deletion not executed", deleted.Diagnostics)
	}
}

func TestEnvironmentRejectsOwnedKeyRemovalAtPlanAndApply(t *testing.T) {
	for _, driver := range []string{"local", "sandbox"} {
		t.Run(driver, func(t *testing.T) {
			r, f, plan := newEnvironmentFixture(t)
			f.remote.Driver = driver
			f.remote.Config["obsolete"] = true
			m := environmentModel()
			m.ID = types.StringValue("env-id")
			m.Driver = types.StringValue(driver)
			if driver == "local" {
				m.Config = types.StringValue(`{}`)
			}
			if d := plan.Set(context.Background(), &m); d.HasError() {
				t.Fatal(d)
			}
			modify := resource.ModifyPlanResponse{Plan: plan}
			r.ModifyPlan(context.Background(), resource.ModifyPlanRequest{Plan: plan}, &modify)
			if !modify.Diagnostics.HasError() || !strings.Contains(fmt.Sprint(modify.Diagnostics), "cannot remove keys") {
				t.Fatal("missing explicit unsupported plan error", modify.Diagnostics)
			}
			update := resource.UpdateResponse{State: tfsdk.State{Schema: plan.Schema}}
			r.Update(context.Background(), resource.UpdateRequest{Plan: plan}, &update)
			if !update.Diagnostics.HasError() || len(f.writes) != 0 || f.remote.ID != "env-id" || f.remote.Config["obsolete"] != true {
				t.Fatal("unsupported removal mutated identity/config", update.Diagnostics)
			}
		})
	}
}

func TestEnvironmentDeleteSurfacesEveryNativeBlock(t *testing.T) {
	for _, reason := range []string{"managed_local", "instance_default", "pending_sandbox_cleanup", "reusable_sandbox_lease"} {
		t.Run(reason, func(t *testing.T) {
			r, f, plan := newEnvironmentFixture(t)
			f.failMethod, f.failReason = "DELETE", reason
			m := environmentModel()
			m.ID = types.StringValue("env-id")
			state := tfsdk.State{Schema: plan.Schema}
			if d := state.Set(context.Background(), &m); d.HasError() {
				t.Fatal(d)
			}
			resp := resource.DeleteResponse{}
			r.Delete(context.Background(), resource.DeleteRequest{State: state}, &resp)
			if !resp.Diagnostics.HasError() || !strings.Contains(fmt.Sprint(resp.Diagnostics), reason) {
				t.Fatal("native blocker lost", resp.Diagnostics)
			}
		})
	}
}

func TestEnvironmentImportAndLocalAdoptionAreReadOnly(t *testing.T) {
	r, f, plan := newEnvironmentFixture(t)
	f.remote.Driver, f.remote.Config = "local", map[string]interface{}{}
	ctx := context.Background()
	for _, id := range []string{"", "env-id", "/env-id", "company/", "company/env/id", " /env-id", "company/env-id"} {
		resp := resource.ImportStateResponse{State: tfsdk.State{Schema: plan.Schema}}
		if d := resp.State.Set(ctx, &PaperclipEnvironmentModel{}); d.HasError() {
			t.Fatal(d)
		}
		r.ImportState(ctx, resource.ImportStateRequest{ID: id}, &resp)
		if id != "company/env-id" {
			if !resp.Diagnostics.HasError() {
				t.Fatal("invalid import accepted", id)
			}
			continue
		}
		read := resource.ReadResponse{State: resp.State}
		r.Read(ctx, resource.ReadRequest{State: resp.State}, &read)
		if read.Diagnostics.HasError() {
			t.Fatal(read.Diagnostics)
		}
		var m PaperclipEnvironmentModel
		if d := read.State.Get(ctx, &m); d.HasError() {
			t.Fatal(d)
		}
		if m.ID.ValueString() != "env-id" || m.Driver.ValueString() != "local" || m.CompanyID.ValueString() != "company" || len(f.writes) != 0 {
			t.Fatal("import changed native identity/ownership")
		}
	}
	model := environmentModel()
	model.Driver, model.Config = types.StringValue("local"), types.StringValue(`{}`)
	if d := plan.Set(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	resp := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
	if !resp.Diagnostics.HasError() || !strings.Contains(fmt.Sprint(resp.Diagnostics), "Import singleton Local") || len(f.writes) != 0 {
		t.Fatal("Local create must require import", resp.Diagnostics)
	}
}

func TestEnvironmentFrameworkRejectsMalformedData(t *testing.T) {
	r, _, plan := newEnvironmentFixture(t)
	ctx := context.Background()
	bad := tftypes.NewValue(tftypes.String, "invalid")
	plan.Raw = bad
	state := tfsdk.State{Schema: plan.Schema, Raw: bad}
	c := resource.CreateResponse{}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &c)
	u := resource.UpdateResponse{}
	r.Update(ctx, resource.UpdateRequest{Plan: plan}, &u)
	rd := resource.ReadResponse{}
	r.Read(ctx, resource.ReadRequest{State: state}, &rd)
	d := resource.DeleteResponse{}
	r.Delete(ctx, resource.DeleteRequest{State: state}, &d)
	v := resource.ValidateConfigResponse{}
	r.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: tfsdk.Config{Schema: plan.Schema, Raw: bad}}, &v)
	mp := resource.ModifyPlanResponse{}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan}, &mp)
	if !c.Diagnostics.HasError() || !u.Diagnostics.HasError() || !rd.Diagnostics.HasError() || !d.Diagnostics.HasError() || !v.Diagnostics.HasError() || !mp.Diagnostics.HasError() {
		t.Fatal("malformed framework input must produce diagnostics")
	}
}

func TestEnvironmentConfigValidationReportsActionableErrors(t *testing.T) {
	r, _, plan := newEnvironmentFixture(t)
	ctx := context.Background()
	for _, tc := range []struct{ name, attribute, value, message string }{
		{"name", "name", " ", "Invalid environment name"},
		{"context", "company_id", "", "Missing secret context"},
		{"status", "status", "paused", "Invalid environment status"},
		{"config", "config", `[]`, "Invalid environment config"},
		{"metadata", "metadata", `[]`, "Invalid environment metadata"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			model := environmentModel()
			if d := plan.Set(ctx, &model); d.HasError() {
				t.Fatal(d)
			}
			if d := plan.SetAttribute(ctx, path.Root(tc.attribute), types.StringValue(tc.value)); d.HasError() {
				t.Fatal(d)
			}
			response := resource.ValidateConfigResponse{}
			r.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: tfsdk.Config{Schema: plan.Schema, Raw: plan.Raw}}, &response)
			if !response.Diagnostics.HasError() || !strings.Contains(fmt.Sprint(response.Diagnostics), tc.message) {
				t.Fatal(response.Diagnostics)
			}
			create := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
			r.Create(ctx, resource.CreateRequest{Plan: plan}, &create)
			if !create.Diagnostics.HasError() {
				t.Fatal("apply skipped validation")
			}
			modify := resource.ModifyPlanResponse{}
			r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan}, &modify)
			if !modify.Diagnostics.HasError() {
				t.Fatal("plan skipped validation")
			}
		})
	}
	for _, version := range []string{"latest", "2", "0", "bad"} {
		model := environmentModel()
		model.Env = map[string]EnvBindingModel{"TOKEN": {Type: types.StringValue("user_secret_ref"), Key: types.StringValue("token"), Version: types.StringValue(version)}}
		if d := validateEnvironment(model); d.HasError() != (version == "0" || version == "bad") {
			t.Fatal(version, d)
		}
	}
	for _, key := range []string{"bad-key", "TOKEN"} {
		model := environmentModel()
		model.Env = map[string]EnvBindingModel{key: {Type: types.StringValue("secret_ref"), SecretID: types.StringValue("bad")}}
		if d := validateEnvironment(model); !d.HasError() {
			t.Fatal("native name/UUID validation missing", key)
		}
	}
}

func TestEnvironmentPlansValidateKnownReferencesAndDeferUnknowns(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []string{"known", "missing", "unknown", "unknown context", "unknown config", "driver change", "Local conversion", "capability error", "read error"} {
		t.Run(mode, func(t *testing.T) {
			r, f, plan := newEnvironmentFixture(t)
			model := environmentModel()
			model.ID = types.StringValue("env-id")
			model.Env = map[string]EnvBindingModel{"TOKEN": {Type: types.StringValue("secret_ref"), SecretID: types.StringValue("11111111-1111-4111-8111-111111111111")}}
			switch mode {
			case "missing":
				model.Env["TOKEN"] = EnvBindingModel{Type: types.StringValue("secret_ref"), SecretID: types.StringValue("22222222-2222-4222-8222-222222222222")}
			case "unknown":
				model.Env["TOKEN"] = EnvBindingModel{Type: types.StringValue("secret_ref"), SecretID: types.StringUnknown()}
			case "unknown context":
				model.CompanyID = types.StringUnknown()
			case "unknown config":
				model.Config = types.StringUnknown()
			case "driver change":
				model.Driver, model.Config = types.StringValue("plugin"), types.StringValue(`{"pluginKey":"p","driverKey":"remote"}`)
			case "Local conversion":
				f.remote.Driver = "local"
			case "capability error":
				f.failMethod, f.failReason = "GET", "Instance admin required"
			case "read error":
				model.ID = types.StringValue("other-id")
			}
			if d := plan.Set(ctx, &model); d.HasError() {
				t.Fatal(d)
			}
			resp := resource.ModifyPlanResponse{Plan: plan}
			r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan}, &resp)
			wantError := mode == "missing" || mode == "Local conversion" || mode == "capability error" || mode == "read error"
			if resp.Diagnostics.HasError() != wantError || len(f.writes) != 0 {
				t.Fatal(mode, resp.Diagnostics)
			}
			if mode == "missing" && !strings.Contains(fmt.Sprint(resp.Diagnostics), "Invalid Secret Reference") {
				t.Fatal("missing known reference not diagnosed", resp.Diagnostics)
			}
		})
	}
	r, _, plan := newEnvironmentFixture(t)
	for _, r := range []*PaperclipEnvironmentResource{r, {}} {
		resp := resource.ModifyPlanResponse{}
		r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: tfsdk.Plan{Schema: plan.Schema}}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatal(resp.Diagnostics)
		}
	}
}

func TestEnvironmentApplyRevalidatesReferencesBeforeMutation(t *testing.T) {
	ctx := context.Background()
	r, f, plan := newEnvironmentFixture(t)
	model := environmentModel()
	model.Env = map[string]EnvBindingModel{"TOKEN": {Type: types.StringValue("secret_ref"), SecretID: types.StringValue("22222222-2222-4222-8222-222222222222")}}
	if d := plan.Set(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	resp := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
	if !resp.Diagnostics.HasError() || !strings.Contains(fmt.Sprint(resp.Diagnostics), "Invalid Secret Reference") || len(f.writes) != 0 {
		t.Fatal("apply-time resolved reference was not verified before mutation", resp.Diagnostics)
	}
}

func TestEnvironmentReadAndWriteErrorsPreserveBinding(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []string{"read error", "missing", "prewrite read", "write error"} {
		t.Run(mode, func(t *testing.T) {
			r, f, plan := newEnvironmentFixture(t)
			model := environmentModel()
			model.ID = types.StringValue("env-id")
			if d := plan.Set(ctx, &model); d.HasError() {
				t.Fatal(d)
			}
			state := tfsdk.State{Schema: plan.Schema, Raw: plan.Raw}
			f.failMethod, f.failReason = "GET", "Instance admin required"
			if mode == "missing" {
				f.failReason = "Environment not found"
			}
			if mode == "write error" {
				f.failMethod = "PATCH"
			}
			if mode == "prewrite read" || mode == "write error" {
				response := resource.UpdateResponse{State: state}
				r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &response)
				if !response.Diagnostics.HasError() || !response.State.Raw.Equal(state.Raw) {
					t.Fatal("failed update lost binding", response.Diagnostics)
				}
			} else {
				response := resource.ReadResponse{State: state}
				r.Read(ctx, resource.ReadRequest{State: state}, &response)
				if mode == "missing" {
					if !response.State.Raw.IsNull() || response.Diagnostics.HasError() {
						t.Fatal("404 did not remove absent binding", response.Diagnostics)
					}
				} else if !response.Diagnostics.HasError() || !response.State.Raw.Equal(state.Raw) {
					t.Fatal("read failure changed binding", response.Diagnostics)
				}
			}
		})
	}
}

func TestEnvironmentBindingsRoundTripNativeVersionsAndBooleans(t *testing.T) {
	r, f, plan := newEnvironmentFixture(t)
	model := environmentModel()
	model.Env = map[string]EnvBindingModel{"TOKEN": {Type: types.StringValue("user_secret_ref"), Key: types.StringValue("token"), Version: types.StringValue("2"), Required: types.BoolValue(true), AllowMissingOverride: types.BoolValue(false)}}
	ctx := context.Background()
	if d := plan.Set(ctx, &model); d.HasError() {
		t.Fatal(d)
	}
	resp := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	if f.remote.EnvVars["TOKEN"].(map[string]interface{})["version"] != float64(2) {
		t.Fatal("version was not a native number")
	}
	var got PaperclipEnvironmentModel
	if d := resp.State.Get(ctx, &got); d.HasError() {
		t.Fatal(d)
	}
	if !reflect.DeepEqual(got.Env, model.Env) {
		t.Fatal("shared binding changed after native readback", got.Env)
	}
	f.remote.EnvVars["TOKEN"] = nil
	f.remote.Metadata = map[string]interface{}{}
	refreshEnvironment(&got, &f.remote)
	if len(got.Env) != 0 {
		t.Fatal("removed binding retained")
	}
	if got.Metadata.ValueString() != "{}" {
		t.Fatal("empty metadata was conflated with null")
	}
}

func TestEnvironmentConfigureRejectsWrongProviderData(t *testing.T) {
	r := &PaperclipEnvironmentResource{}
	r.Configure(context.Background(), resource.ConfigureRequest{}, &resource.ConfigureResponse{})
	resp := resource.ConfigureResponse{}
	r.Configure(context.Background(), resource.ConfigureRequest{ProviderData: "invalid"}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("wrong provider data accepted")
	}
}

func TestEnvironmentArchiveDoesNotPatchEquivalentDefaultedSSHConfig(t *testing.T) {
	r, f, plan := newEnvironmentFixture(t)
	ctx := context.Background()
	m := environmentModel()
	m.Driver = types.StringValue("ssh")
	m.Config = types.StringValue(`{"host":"host","username":"user","remoteWorkspacePath":"/work"}`)
	if d := plan.Set(ctx, &m); d.HasError() {
		t.Fatal(d)
	}
	created := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
	if created.Diagnostics.HasError() {
		t.Fatal(created.Diagnostics)
	}
	if d := created.State.Get(ctx, &m); d.HasError() {
		t.Fatal(d)
	}
	m.Status = types.StringValue("archived")
	if d := plan.Set(ctx, &m); d.HasError() {
		t.Fatal(d)
	}
	updated := resource.UpdateResponse{State: created.State}
	r.Update(ctx, resource.UpdateRequest{Plan: plan}, &updated)
	if updated.Diagnostics.HasError() {
		t.Fatal(updated.Diagnostics)
	}
	if _, exists := f.writes[1]["config"]; exists {
		t.Fatal("archiving patched unchanged SSH config and would enter native config/cleanup gates")
	}
	if f.remote.Status != "archived" {
		t.Fatal("archive did not reach API")
	}
}

func TestEnvironmentDefaultedSecretBindingsDoNotIntroduceFalseDrift(t *testing.T) {
	r, f, plan := newEnvironmentFixture(t)
	ctx := context.Background()
	m := environmentModel()
	m.Env = map[string]EnvBindingModel{"TOKEN": {Type: types.StringValue("secret_ref"), SecretID: types.StringValue("11111111-1111-4111-8111-111111111111")}}
	if d := plan.Set(ctx, &m); d.HasError() {
		t.Fatal(d)
	}
	created := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &created)
	if created.Diagnostics.HasError() {
		t.Fatal(created.Diagnostics)
	}
	var got PaperclipEnvironmentModel
	if d := created.State.Get(ctx, &got); d.HasError() {
		t.Fatal(d)
	}
	if !reflect.DeepEqual(got.Env, m.Env) {
		t.Fatal("native latest/unclassified defaults changed configured null attributes", got.Env)
	}
	f.remote.EnvVars["TOKEN"].(map[string]interface{})["projectionClass"] = "class_3_static_lease"
	read := resource.ReadResponse{State: created.State}
	r.Read(ctx, resource.ReadRequest{State: created.State}, &read)
	if d := read.State.Get(ctx, &got); d.HasError() {
		t.Fatal(d)
	}
	if got.Env["TOKEN"].ProjectionClass.ValueString() != "class_3_static_lease" {
		t.Fatal("changed projection was hidden as a default")
	}
}
