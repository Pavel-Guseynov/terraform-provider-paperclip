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

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestAgentConfigurationOwnershipValidation(t *testing.T) {
	for _, tc := range []struct{ attribute, value, diagnostic string }{
		{"adapter_config", `{"instructionsBundleMode":"managed"}`, "instructionsBundleMode"},
		{"adapter_config", `{"instructionsRootPath":"/agents"}`, "instructionsRootPath"},
		{"adapter_config", `{"instructionsEntryFile":"AGENTS.md"}`, "instructionsEntryFile"},
		{"adapter_config", `{"instructionsFilePath":"AGENTS.md"}`, "instructionsFilePath"},
		{"adapter_config", `{"paperclipSkillSync":{}}`, "paperclipSkillSync"},
		{"adapter_config", `null`, "must be a JSON object"},
		{"runtime_config", `{"modelProfiles":[]}`, "modelProfiles is no longer supported"},
		{"runtime_config", `{"heartbeat":{"enabled":"false"}}`, "heartbeat.enabled must be a boolean"},
		{"runtime_config", `{"heartbeat":{"intervalSec":-1}}`, "heartbeat.intervalSec must be a non-negative number"},
		{"runtime_config", `{"heartbeat":{"maxDailyRuns":1.5}}`, "heartbeat.maxDailyRuns must be a non-negative integer"},
		{"runtime_config", `{"heartbeat":null}`, "heartbeat must be an object"},
	} {
		t.Run(tc.value, func(t *testing.T) {
			ctx := context.Background()
			r := NewPaperclipAgentResource().(*PaperclipAgentResource)
			var sr resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &sr)
			model := PaperclipAgentResourceModel{}
			if tc.attribute == "adapter_config" {
				model.AdapterConfig = types.StringValue(tc.value)
			} else {
				model.RuntimeConfig = types.StringValue(tc.value)
			}
			plan := tfsdk.Plan{Schema: sr.Schema}
			if d := plan.Set(ctx, &model); d.HasError() {
				t.Fatal(d)
			}
			var resp resource.ValidateConfigResponse
			r.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: tfsdk.Config{Schema: sr.Schema, Raw: plan.Raw}}, &resp)
			if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), tc.diagnostic) {
				t.Fatalf("expected %q, got %v", tc.diagnostic, resp.Diagnostics)
			}
		})
	}
}

func TestAgentHeartbeatTypes(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want string
	}{
		{`{"enabled":true,"intervalSec":0.5,"wakeOnDemand":false,"maxConcurrentRuns":50,"skipTimerWhenNoActionableWork":true,"maxDailyRuns":null,"maxDailyCostCents":0}`, ""},
		{`{"wakeOnAssignment":true,"wakeOnOnDemand":false,"wakeOnAutomation":true,"requireActionableTimerWork":false,"issueOnlyTimer":true,"dailyRunLimit":2,"dailyRunCap":0,"maxRunsPerDay":1,"dailyCostCentsLimit":null,"dailySpendCentsLimit":1,"dailyBudgetCents":3}`, ""},
		{`{"maxConcurrentRuns":0}`, "between 1 and 50"},
		{`{"maxConcurrentRuns":51}`, "between 1 and 50"},
		{`{"intervalSec":"5"}`, "non-negative number"},
		{`{"maxDailyCostCents":-1}`, "non-negative integer"},
		{`{"maxConcurrentRuns":null}`, "non-negative integer"},
		{`{"typo":true}`, "not a supported native heartbeat setting"},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			var value interface{}
			if err := json.Unmarshal([]byte(tc.raw), &value); err != nil {
				t.Fatal(err)
			}
			err := validateHeartbeat(value)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q got %v", tc.want, err)
			}
		})
	}
}

func TestAgentSessionCompactionPlan(t *testing.T) {
	type testCase struct{ name, heartbeat, diagnostic string }
	cases := []testCase{
		{"native thresholds", `{"maxConcurrentRuns":1,"sessionCompaction":{"enabled":true,"maxSessionRuns":30,"maxRawInputTokens":15000000,"maxSessionAgeHours":24}}`, ""},
		{"adapter defaults", `{"sessionCompaction":{}}`, ""},
		{"disabled thresholds", `{"sessionCompaction":{"enabled":false,"maxSessionRuns":0,"maxRawInputTokens":0,"maxSessionAgeHours":0}}`, ""},
		{"partial override", `{"sessionCompaction":{"maxSessionRuns":30}}`, ""},
		{"unknown heartbeat key", `{"sessionCompaction":{},"typo":true}`, "heartbeat.typo is not a supported native heartbeat setting"},
		{"unknown compaction key", `{"sessionCompaction":{"typo":1}}`, "heartbeat.sessionCompaction.typo is not a supported native session compaction setting"},
	}
	for _, value := range []string{`null`, `false`, `1`, `"policy"`, `[]`} {
		cases = append(cases, testCase{
			"non-object policy " + value, `{"sessionCompaction":` + value + `}`, "heartbeat.sessionCompaction must be an object",
		})
	}
	for _, field := range []string{"enabled", "maxSessionRuns", "maxRawInputTokens", "maxSessionAgeHours"} {
		invalid := []string{`null`, `"1"`, `{}`, `[]`}
		wantType := "a boolean"
		if field == "enabled" {
			invalid = append(invalid, `1`)
		} else {
			invalid = append(invalid, `true`, `-1`, `0.5`)
			wantType = "a non-negative integer"
		}
		for _, value := range invalid {
			cases = append(cases, testCase{
				field + " rejects " + value,
				fmt.Sprintf(`{"sessionCompaction":{%q:%s}}`, field, value),
				fmt.Sprintf("heartbeat.sessionCompaction.%s must be %s", field, wantType),
			})
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			r := NewPaperclipAgentResource().(*PaperclipAgentResource)
			var sr resource.SchemaResponse
			r.Schema(ctx, resource.SchemaRequest{}, &sr)
			runtime := types.StringValue(`{"heartbeat":` + tc.heartbeat + `}`)
			plan := tfsdk.Plan{Schema: sr.Schema}
			if d := plan.Set(ctx, &PaperclipAgentResourceModel{AdapterType: types.StringValue("codex_local"), RuntimeConfig: runtime}); d.HasError() {
				t.Fatal(d)
			}
			resp := resource.ModifyPlanResponse{Plan: plan}
			r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: tfsdk.State{Schema: sr.Schema, Raw: tftypes.NewValue(sr.Schema.Type().TerraformType(ctx), nil)}}, &resp)
			if tc.diagnostic != "" {
				want := diag.Diagnostics{diag.NewAttributeErrorDiagnostic(path.Root("runtime_config"), "Invalid heartbeat configuration", tc.diagnostic)}
				if !reflect.DeepEqual(resp.Diagnostics, want) {
					t.Fatalf("want %v, got %v", want, resp.Diagnostics)
				}
				return
			}
			if resp.Diagnostics.HasError() {
				t.Fatal(resp.Diagnostics)
			}
			var planned types.String
			if d := resp.Plan.GetAttribute(ctx, path.Root("runtime_config"), &planned); d.HasError() {
				t.Fatal(d)
			}
			if !planned.Equal(runtime) {
				t.Fatalf("runtime config changed: want %s, got %s", runtime, planned)
			}
		})
	}
}

func TestAgentPublishedSchemaSemantics(t *testing.T) {
	for _, tc := range []struct{ schema, config, want string }{
		{`{"fields":[]}`, `{"model":"not-in-a-catalog","futureSetting":true}`, ""},
		{`{"fields":[{"key":"optional","type":"text"}]}`, `{}`, ""},
		{`{"fields":[],"additionalProperties":false}`, `{"typo":true}`, "additionalProperties=false"},
		{`{"fields":[],"additionalProperties":false}`, `{"env":{},"cwd":"/work","promptTemplate":"x","bootstrapPromptTemplate":"x","graceSec":10,"timeoutSec":0}`, ""},
		{`{"fields":[],"additionalProperties":true}`, `{"futureSetting":true}`, ""},
		{`{"fields":[{"key":"required","type":"text","required":true}]}`, `{}`, "required by the published schema"},
		{`{"fields":[{"key":"required","type":"text","required":true}]}`, `{"required":""}`, ""},
		{`{"fields":[{"key":"text","type":"textarea"},{"key":"suggestion","type":"combobox","options":[{"value":"old-model"}]}]}`, `{"text":"x","suggestion":"future-model"}`, ""},
		{`{"fields":[{"key":"text","type":"text"}]}`, `{"text":1}`, "published field type text"},
		{`{"fields":[{"key":"n","type":"number"}]}`, `{"n":1.5}`, ""},
		{`{"fields":[{"key":"n","type":"number"}]}`, `{"n":"1"}`, "published field type number"},
		{`{"fields":[{"key":"b","type":"toggle"}]}`, `{"b":false}`, ""},
		{`{"fields":[{"key":"b","type":"toggle"}]}`, `{"b":null}`, "published field type toggle"},
		{`{"fields":[{"key":"choice","type":"select"}]}`, `{"choice":"open"}`, ""},
		{`{"fields":[{"key":"choice","type":"select","options":[{"value":"allowed"}]}]}`, `{"choice":"denied"}`, "published select option"},
		{`{"fields":[{"key":"future","type":"unrecognized"}]}`, `{"future":{}}`, "unsupported type"},
	} {
		t.Run(tc.schema+tc.config, func(t *testing.T) {
			var s client.AdapterConfigSchema
			var c map[string]interface{}
			if err := json.Unmarshal([]byte(tc.schema), &s); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(tc.config), &c); err != nil {
				t.Fatal(err)
			}
			err := validatePublishedAdapterConfig(c, &s)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q got %v", tc.want, err)
			}
		})
	}
}

func TestAgentRuntimeOwnedDrift(t *testing.T) {
	declared := types.StringValue(`{"heartbeat":{"enabled":true,"intervalSec":30},"custom":{}}`)
	remote := map[string]interface{}{"heartbeat": map[string]interface{}{"enabled": true, "intervalSec": float64(30), "maxConcurrentRuns": float64(20)}, "custom": map[string]interface{}{}, "aiConnection": map[string]interface{}{"provider": "openai", "method": "subscription", "mode": "responsible_user"}}
	if got := refreshAgentRuntimeConfig(declared, remote); !got.Equal(declared) {
		t.Fatalf("unowned defaults changed state: %s", got)
	}
	remote["heartbeat"].(map[string]interface{})["intervalSec"] = float64(60)
	got := refreshAgentRuntimeConfig(declared, remote)
	if got.Equal(declared) || strings.Contains(got.ValueString(), "aiConnection") || strings.Contains(got.ValueString(), "maxConcurrentRuns") {
		t.Fatalf("drift not scoped: %s", got)
	}
	delete(remote, "custom")
	if got := refreshAgentRuntimeConfig(declared, remote); strings.Contains(got.ValueString(), "custom") {
		t.Fatal(got)
	}
	remote["heartbeat"] = "invalid"
	if got := refreshAgentRuntimeConfig(declared, remote); !strings.Contains(got.ValueString(), "invalid") {
		t.Fatal(got)
	}
	if got := refreshAgentRuntimeConfig(types.StringNull(), remote); !got.IsNull() {
		t.Fatal(got)
	}
	if got := refreshAgentRuntimeConfig(types.StringUnknown(), remote); got.IsUnknown() {
		t.Fatal(got)
	}
	if got := refreshAgentRuntimeConfig(types.StringValue(`invalid`), remote); got.IsNull() {
		t.Fatal(got)
	}
	if got := refreshAgentRuntimeConfig(types.StringValue(`{"heartbeat":null}`), remote); got.IsNull() {
		t.Fatal(got)
	}
	var object map[string]interface{}
	if err := json.Unmarshal([]byte(got.ValueString()), &object); err != nil || reflect.DeepEqual(object, remote) {
		t.Fatalf("owned projection: %v %v", object, err)
	}
}

func TestAgentUnsupportedTransitionsDoNotWrite(t *testing.T) {
	for _, tc := range []struct{ name, priorRuntime, nextRuntime, priorAdapter, nextAdapter, priorType, nextType, want string }{
		{"AI binding removal", `{"aiConnection":{"provider":"openai","method":"subscription","mode":"responsible_user"}}`, `{}`, `{}`, `{}`, "codex_local", "codex_local", "runtime_config.aiConnection cannot be cleared"},
		{"AI binding null", `{"aiConnection":{"provider":"openai","method":"subscription","mode":"responsible_user"}}`, `{"aiConnection":null}`, `{}`, `{}`, "codex_local", "codex_local", "runtime_config.aiConnection cannot be cleared"},
		{"swap prunes cwd", `{}`, `{}`, `{"cwd":"/work"}`, `{}`, "codex_local", "other", "adapter_type cannot change while removing adapter-agnostic key cwd"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			writes := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					writes++
				}
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"fields": []interface{}{}})
			}))
			defer server.Close()
			r := NewPaperclipAgentResource().(*PaperclipAgentResource)
			s := configureTestResource(t, server.URL, r)
			prior := PaperclipAgentResourceModel{ID: types.StringValue("same-id"), AdapterType: types.StringValue(tc.priorType), AdapterConfig: types.StringValue(tc.priorAdapter), RuntimeConfig: types.StringValue(tc.priorRuntime)}
			next := prior
			next.AdapterType = types.StringValue(tc.nextType)
			next.AdapterConfig = types.StringValue(tc.nextAdapter)
			next.RuntimeConfig = types.StringValue(tc.nextRuntime)
			state := tfsdk.State{Schema: s}
			plan := tfsdk.Plan{Schema: s}
			if d := state.Set(ctx, &prior); d.HasError() {
				t.Fatal(d)
			}
			if d := plan.Set(ctx, &next); d.HasError() {
				t.Fatal(d)
			}
			mod := resource.ModifyPlanResponse{Plan: plan}
			r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: state}, &mod)
			if !mod.Diagnostics.HasError() || !strings.Contains(mod.Diagnostics.Errors()[0].Detail(), tc.want) {
				t.Fatalf("expected %q, got %v", tc.want, mod.Diagnostics)
			}
			upd := resource.UpdateResponse{State: state}
			r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &upd)
			if !upd.Diagnostics.HasError() || !strings.Contains(upd.Diagnostics.Errors()[0].Detail(), tc.want) {
				t.Fatalf("expected %q, got %v", tc.want, upd.Diagnostics)
			}
			var preserved PaperclipAgentResourceModel
			upd.State.Get(ctx, &preserved)
			if preserved.ID.ValueString() != "same-id" || writes != 0 {
				t.Fatalf("identity=%s writes=%d", preserved.ID, writes)
			}
		})
	}
}

func TestAgentConfigurationLifecyclePreservesOtherOwners(t *testing.T) {
	ctx := context.Background()
	live := client.PaperclipAgent{ID: "same-agent", CompanyID: "company", Name: "agent", AdapterType: "codex_local", Status: "idle", Permissions: &client.PaperclipAgentPermissions{CanCreateSkills: true}, RuntimeConfig: map[string]interface{}{"heartbeat": map[string]interface{}{"enabled": true, "intervalSec": float64(30), "maxConcurrentRuns": float64(20)}, "aiConnection": map[string]interface{}{"provider": "openai", "method": "subscription", "mode": "responsible_user"}}, AdapterConfig: map[string]interface{}{"engine": "cli", "instructionsRootPath": "/instructions", "paperclipSkillSync": map[string]interface{}{"mode": "exclusive"}, "env": map[string]interface{}{"OWNED": map[string]interface{}{"type": "secret_ref", "secretId": "secret"}, "INJECTED": map[string]interface{}{"type": "secret_ref", "secretId": "injected"}}}}
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "GET" && r.URL.Path == "/api/adapters/codex_local/config-schema":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"fields": []interface{}{}})
		case r.Method == "GET" && r.URL.Path == "/api/companies/company/secrets":
			_ = json.NewEncoder(w).Encode([]map[string]interface{}{{"id": "secret", "companyId": "company"}, {"id": "injected", "companyId": "company"}})
		case r.Method == "GET" && r.URL.Path == "/api/agents/same-agent":
			_ = json.NewEncoder(w).Encode(live)
		case r.Method == "PATCH" && r.URL.Path == "/api/agents/same-agent":
			writes++
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["replaceAdapterConfig"] != true {
				t.Error("expected native replacement flag")
			}
			live.AdapterConfig = body["adapterConfig"].(map[string]interface{})
			live.RuntimeConfig = body["runtimeConfig"].(map[string]interface{})
			_ = json.NewEncoder(w).Encode(live)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	r := NewPaperclipAgentResource().(*PaperclipAgentResource)
	s := configureTestResource(t, server.URL, r)
	model := PaperclipAgentResourceModel{ID: types.StringValue(live.ID), CompanyID: types.StringValue(live.CompanyID), Name: types.StringValue(live.Name), AdapterType: types.StringValue("codex_local"), AdapterConfig: types.StringValue(`{"engine":"cli"}`), RuntimeConfig: types.StringValue(`{"heartbeat":{"enabled":true,"intervalSec":30}}`), CanAssignTasks: types.BoolValue(false), CanCreateAgents: types.BoolValue(false), CanCreateSkills: types.BoolValue(true), Paused: types.BoolValue(false), Env: map[string]EnvBindingModel{"OWNED": {Type: types.StringValue("secret_ref"), SecretID: types.StringValue("secret")}, "PLAIN": {Type: types.StringValue("plain"), Value: types.StringValue("text")}}}
	state := tfsdk.State{Schema: s}
	state.Set(ctx, &model)
	model.RuntimeConfig = types.StringValue(`{"heartbeat":{"enabled":false,"intervalSec":60}}`)
	plan := tfsdk.Plan{Schema: s}
	plan.Set(ctx, &model)
	resp := resource.UpdateResponse{State: state}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	var updated PaperclipAgentResourceModel
	resp.State.Get(ctx, &updated)
	if !updated.RuntimeConfig.Equal(model.RuntimeConfig) || strings.Contains(updated.AdapterConfig.ValueString(), "env") || !updated.Env["OWNED"].Value.IsNull() {
		t.Fatalf("unexpected managed state: %+v", updated)
	}
	if live.RuntimeConfig["aiConnection"] == nil || live.RuntimeConfig["heartbeat"].(map[string]interface{})["maxConcurrentRuns"] != float64(20) || live.AdapterConfig["instructionsRootPath"] != "/instructions" || live.AdapterConfig["paperclipSkillSync"] == nil {
		t.Fatalf("other owner changed: %+v", live)
	}
	model.Env = map[string]EnvBindingModel{}
	plan.Set(ctx, &model)
	second := resource.UpdateResponse{State: resp.State}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: resp.State}, &second)
	if second.Diagnostics.HasError() {
		t.Fatal(second.Diagnostics)
	}
	env := live.AdapterConfig["env"].(map[string]interface{})
	if env["OWNED"] != nil || env["PLAIN"] != nil || env["INJECTED"] == nil || writes != 2 {
		t.Fatalf("env pruning failed: %v writes=%d", env, writes)
	}
	// The same shared binding can be managed through adapter_config.env, with
	// removal affecting only the previously declared reference.
	model.Env = nil
	model.AdapterConfig = types.StringValue(`{"engine":"cli","env":{"OWNED":{"type":"secret_ref","secretId":"secret"}}}`)
	plan.Set(ctx, &model)
	third := resource.UpdateResponse{State: second.State}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: second.State}, &third)
	if third.Diagnostics.HasError() {
		t.Fatal(third.Diagnostics)
	}
	model.AdapterConfig = types.StringValue(`{"engine":"cli"}`)
	plan.Set(ctx, &model)
	fourth := resource.UpdateResponse{State: third.State}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: third.State}, &fourth)
	if fourth.Diagnostics.HasError() {
		t.Fatal(fourth.Diagnostics)
	}
	env = live.AdapterConfig["env"].(map[string]interface{})
	if env["OWNED"] != nil || env["INJECTED"] == nil {
		t.Fatalf("nested env pruning: %v", env)
	}
	importState := tfsdk.State{Schema: s}
	importState.Set(ctx, &PaperclipAgentResourceModel{ID: types.StringValue("same-agent")})
	imported := resource.ReadResponse{State: importState}
	r.Read(ctx, resource.ReadRequest{State: importState}, &imported)
	if imported.Diagnostics.HasError() {
		t.Fatal(imported.Diagnostics)
	}
	var adopted PaperclipAgentResourceModel
	imported.State.Get(ctx, &adopted)
	if !strings.Contains(adopted.AdapterConfig.ValueString(), "injected") || strings.Contains(adopted.AdapterConfig.ValueString(), "instructionsRootPath") {
		t.Fatalf("import ownership: %s", adopted.AdapterConfig)
	}
	live.RuntimeConfig["heartbeat"].(map[string]interface{})["intervalSec"] = float64(90)
	read := resource.ReadResponse{State: fourth.State}
	r.Read(ctx, resource.ReadRequest{State: fourth.State}, &read)
	if read.Diagnostics.HasError() {
		t.Fatal(read.Diagnostics)
	}
	var drift PaperclipAgentResourceModel
	read.State.Get(ctx, &drift)
	if !strings.Contains(drift.RuntimeConfig.ValueString(), "90") || strings.Contains(drift.RuntimeConfig.ValueString(), "maxConcurrentRuns") {
		t.Fatalf("owned drift not exposed: %s", drift.RuntimeConfig)
	}
}

func TestAgentKnownNestedSecretReferenceFailsAtPlan(t *testing.T) {
	ctx := context.Background()
	writes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			writes++
		}
		if strings.Contains(r.URL.Path, "config-schema") {
			_, _ = w.Write([]byte(`{"fields":[]}`))
		} else {
			_, _ = w.Write([]byte(`[]`))
		}
	}))
	defer server.Close()
	r := NewPaperclipAgentResource().(*PaperclipAgentResource)
	s := configureTestResource(t, server.URL, r)
	plan := tfsdk.Plan{Schema: s}
	plan.Set(ctx, &PaperclipAgentResourceModel{CompanyID: types.StringValue("company"), AdapterType: types.StringValue("codex_local"), AdapterConfig: types.StringValue(`{"env":{"TOKEN":{"type":"secret_ref","secretId":"missing"}}}`)})
	resp := resource.ModifyPlanResponse{Plan: plan}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan}, &resp)
	if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Invalid Secret Reference" || writes != 0 {
		t.Fatalf("%v writes=%d", resp.Diagnostics, writes)
	}
}

func TestAgentPlanRejectsImplicitAdapterClearing(t *testing.T) {
	ctx := context.Background()
	r := NewPaperclipAgentResource().(*PaperclipAgentResource)
	var sr resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sr)
	model := PaperclipAgentResourceModel{AdapterConfig: types.StringValue(`{"model":"future"}`)}
	state := tfsdk.State{Schema: sr.Schema}
	state.Set(ctx, &model)
	plan := tfsdk.Plan{Schema: sr.Schema}
	plan.Set(ctx, &model)
	configPlan := tfsdk.Plan{Schema: sr.Schema}
	configPlan.Set(ctx, &PaperclipAgentResourceModel{})
	resp := resource.ModifyPlanResponse{Plan: plan}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: state, Config: tfsdk.Config{Schema: sr.Schema, Raw: configPlan.Raw}}, &resp)
	if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "set adapter_config = jsonencode({})") {
		t.Fatal(resp.Diagnostics)
	}
}

func TestAgentPlanRejectsInvalidRuntimeBeforeSchemaRead(t *testing.T) {
	ctx := context.Background()
	r := NewPaperclipAgentResource().(*PaperclipAgentResource)
	var sr resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sr)
	plan := tfsdk.Plan{Schema: sr.Schema}
	plan.Set(ctx, &PaperclipAgentResourceModel{RuntimeConfig: types.StringValue(`{"heartbeat":false}`)})
	resp := resource.ModifyPlanResponse{Plan: plan}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan}, &resp)
	if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Detail() != "heartbeat must be an object" {
		t.Fatal(resp.Diagnostics)
	}
}

func TestAgentDuplicateEnvironmentOwnership(t *testing.T) {
	d := validateAgentConfigValues(PaperclipAgentResourceModel{AdapterConfig: types.StringValue(`{"env":{"DUP":"value"}}`), Env: map[string]EnvBindingModel{"DUP": {Type: types.StringValue("plain"), Value: types.StringValue("value")}}})
	if !d.HasError() || d.Errors()[0].Summary() != "Duplicate environment ownership" {
		t.Fatal(d)
	}
}

func TestAgentUnknownRuntimeTransitionDefers(t *testing.T) {
	if err := validateAgentRuntimeTransition(types.StringValue(`{"aiConnection":{}}`), types.StringUnknown()); err != nil {
		t.Fatal(err)
	}
}

func TestAgentNoOpPlanPreservesRefreshedStatus(t *testing.T) {
	ctx := context.Background()
	r := NewPaperclipAgentResource().(*PaperclipAgentResource)
	var sr resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &sr)
	model := PaperclipAgentResourceModel{ID: types.StringValue("same-agent"), Name: types.StringValue("agent"), AdapterConfig: types.StringValue(`{"engine":"cli"}`), RuntimeConfig: types.StringValue(`{"heartbeat":{"intervalSec":45}}`), Status: types.StringValue("idle")}
	state := tfsdk.State{Schema: sr.Schema}
	state.Set(ctx, &model)
	model.Status = types.StringUnknown()
	plan := tfsdk.Plan{Schema: sr.Schema}
	plan.Set(ctx, &model)
	resp := resource.ModifyPlanResponse{Plan: plan}
	r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatal(resp.Diagnostics)
	}
	var status types.String
	resp.Plan.GetAttribute(ctx, path.Root("status"), &status)
	if status.ValueString() != "idle" {
		t.Fatalf("no-op status must remain the refreshed value, got %s", status)
	}
}

func TestAgentWriteFailuresRetainIdentity(t *testing.T) {
	for _, operation := range []string{"create", "update", "permissions"} {
		t.Run(operation, func(t *testing.T) {
			ctx := context.Background()
			patched := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == "GET" && strings.Contains(r.URL.Path, "config-schema") {
					_, _ = w.Write([]byte(`{"fields":[]}`))
					return
				}
				if r.Method == "GET" && r.URL.Path == "/api/companies/company" {
					_, _ = w.Write([]byte(`{"id":"company"}`))
					return
				}
				if r.Method == "GET" && r.URL.Path == "/api/agents/original" {
					if patched {
						w.WriteHeader(500)
						_, _ = w.Write([]byte(`{"error":"test-owned write failure"}`))
						return
					}
					_, _ = w.Write([]byte(`{"id":"original","permissions":{"canCreateSkills":true}}`))
					return
				}
				if operation == "permissions" && r.Method == "PATCH" {
					patched = true
					_, _ = w.Write([]byte(`{"id":"original","permissions":{"canCreateSkills":true}}`))
					return
				}
				w.WriteHeader(500)
				_, _ = w.Write([]byte(`{"error":"test-owned write failure"}`))
			}))
			defer server.Close()
			r := NewPaperclipAgentResource().(*PaperclipAgentResource)
			s := configureTestResource(t, server.URL, r)
			model := PaperclipAgentResourceModel{ID: types.StringValue("original"), CompanyID: types.StringValue("company"), Name: types.StringValue("agent"), AdapterType: types.StringValue("codex_local"), AdapterConfig: types.StringValue(`{}`)}
			plan := tfsdk.Plan{Schema: s}
			plan.Set(ctx, &model)
			state := tfsdk.State{Schema: s}
			state.Set(ctx, &model)
			if operation == "create" {
				resp := resource.CreateResponse{State: state}
				r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
				if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "test-owned write failure") {
					t.Fatal(resp.Diagnostics)
				}
			} else {
				resp := resource.UpdateResponse{State: state}
				r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &resp)
				if !resp.Diagnostics.HasError() || !strings.Contains(resp.Diagnostics.Errors()[0].Detail(), "test-owned write failure") {
					t.Fatal(resp.Diagnostics)
				}
				var got PaperclipAgentResourceModel
				resp.State.Get(ctx, &got)
				if got.ID.ValueString() != "original" {
					t.Fatal(got.ID)
				}
			}
		})
	}
}

func TestAgentPublishedConfigurationConstraints(t *testing.T) {
	for _, tc := range []struct {
		config  string
		invalid bool
	}{
		{`{"engine":"invalid"}`, true},
		{`{"engine":"cli","model":"future-model","cwd":"/work","timeoutSec":0}`, false},
	} {
		t.Run(tc.config, func(t *testing.T) {
			ctx := context.Background()
			reads := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if req.Method != "GET" || req.URL.Path != "/api/adapters/codex_local/config-schema" {
					t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
					http.NotFound(w, req)
					return
				}
				reads++
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"fields": []interface{}{map[string]interface{}{"key": "engine", "type": "select", "options": []interface{}{map[string]interface{}{"value": "cli"}, map[string]interface{}{"value": "acp"}}}}})
			}))
			defer server.Close()
			r := NewPaperclipAgentResource().(*PaperclipAgentResource)
			s := configureTestResource(t, server.URL, r)
			plan := tfsdk.Plan{Schema: s}
			if d := plan.Set(ctx, &PaperclipAgentResourceModel{AdapterType: types.StringValue("codex_local"), AdapterConfig: types.StringValue(tc.config)}); d.HasError() {
				t.Fatal(d)
			}
			resp := resource.ModifyPlanResponse{Plan: plan}
			r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: tfsdk.State{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}}, &resp)
			if resp.Diagnostics.HasError() != tc.invalid {
				t.Fatalf("invalid=%t, got %v", tc.invalid, resp.Diagnostics)
			}
			if reads != 1 {
				t.Fatalf("schema reads=%d, want 1", reads)
			}
		})
	}
}
