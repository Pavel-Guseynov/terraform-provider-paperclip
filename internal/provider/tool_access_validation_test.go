package provider

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestToolConditionsNativeValidation(t *testing.T) {
	for _, tc := range []struct{ name, raw, want string }{
		{"root empty", `{}`, "supported condition group"},
		{"unknown group", `{"unknown":{}}`, "unsupported conditions group"},
		{"null group", `{"risk":null}`, "must be an object"},
		{"unknown field", `{"actor":{"anything":true}}`, "unsupported conditions.actor.anything"},
		{"boolean", `{"risk":{"isWrite":"true"}}`, "expected native bool"},
		{"arguments empty", `{"arguments":{}}`, "predicate or bound"},
		{"args empty predicate", `{"args":{"fieldEquals":{}}}`, "predicate or bound"},
		{"time empty", `{"timeWindow":{"daysOfWeekUtc":[]}}`, "predicate or bound"},
		{"record type", `{"arguments":{"fieldEquals":[]}}`, "expected native record"},
		{"predicate key", `{"arguments":{"fieldEquals":{"":true}}}`, "expected native record"},
		{"in type", `{"arguments":{"fieldIn":{"path":false}}}`, "expected native in"},
		{"in empty", `{"arguments":{"fieldIn":{"path":[]}}}`, "expected native in"},
		{"matches type", `{"arguments":{"fieldMatches":{"path":3}}}`, "expected native matches"},
		{"matches empty", `{"arguments":{"fieldMatches":{"path":" "}}}`, "expected native matches"},
		{"unsafe regex", `{"arguments":{"fieldMatches":{"path":"(x+)+"}}}`, "unsafe regular expression"},
		{"regex alternation", `{"arguments":{"fieldMatches":{"path":"x|y"}}}`, "unsafe regular expression"},
		{"regex backref", `{"arguments":{"fieldMatches":{"path":"(x)\\1"}}}`, "unsafe regular expression"},
		{"actor enum", `{"actor":{"actorType":"board"}}`, "expected native actor"},
		{"uuid", `{"context":{"issueId":"invalid"}}`, "expected native uuid"},
		{"array type", `{"context":{"projectIds":false}}`, "expected native uuid[]"},
		{"array item", `{"context":{"projectIds":[3]}}`, "expected native uuid[]"},
		{"risk enum", `{"risk":{"max":"unsafe"}}`, "expected native risk"},
		{"text empty", `{"trustBoundary":{"providerType":""}}`, "expected native text"},
		{"text whitespace", `{"trustBoundary":{"providerType":" mcp "}}`, "expected native text"},
		{"date type", `{"timeWindow":{"startAt":false}}`, "expected native datetime"},
		{"date malformed", `{"timeWindow":{"startAt":"yesterday"}}`, "expected native datetime"},
		{"day range", `{"timeWindow":{"daysOfWeekUtc":[7]}}`, "expected native day[]"},
		{"hour fractional", `{"timeWindow":{"startHourUtc":1.5}}`, "expected native hour"},
		{"valid groups", `{"actor":{"actorType":"agent","actorTypes":["agent","user","system","plugin"],"agentId":"` + toolTestUUID + `","agentIds":["` + toolTestUUID + `"]},"context":{"requireIssue":true,"requireProject":false,"requireRoutine":true},"risk":{"levels":["read","write","destructive","low","medium","high","critical"],"max":"high","isWrite":true,"isDestructive":false},"credentialScope":{"applicationKeys":["app"],"connectionIds":[]},"trustBoundary":{"providerTypes":["mcp"],"paperclipSelfOnly":false},"timeWindow":{"startAt":"2026-01-01T00:00:00Z","endAt":"2026-12-01T00:00:00+06:00","daysOfWeekUtc":[0,6],"startHourUtc":0,"endHourUtc":24},"arguments":{"fieldEquals":{"x":null},"fieldNotEquals":{"x":false},"fieldIn":{"x":[1,null]},"fieldMatches":{"x":"^safe.*$"},"fieldExists":["x"],"fieldAbsent":["y"]},"args":{"fieldEquals":{"x":3}}}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var value map[string]interface{}
			if err := json.Unmarshal([]byte(tc.raw), &value); err != nil {
				t.Fatal(err)
			}
			err := validateToolConditions(value)
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("wanted %q, got %v", tc.want, err)
			}
		})
	}
}
func TestToolEntryShapeValidation(t *testing.T) {
	for _, tc := range []struct{ raw, want string }{
		{`[]`, ""}, {`[{"selectorType":"tool_name","toolName":"read","effect":"include","conditions":{}}]`, ""},
		{`null`, "JSON array"}, {`{}`, "JSON array"}, {`{`, "JSON array"}, {`[{}]`, "supported selectorType"},
		{`[{"selectorType":"application","applicationId":"bad"}]`, "UUID"},
		{`[{"selectorType":"tool_name","toolName":"read","effect":"allow"}]`, "effect"},
		{`[{"selectorType":"tool_name","toolName":" read"}]`, "toolName"},
		{`[{"selectorType":"risk_level","riskLevel":"bad"}]`, "riskLevel"},
		{`[{"selectorType":"tool_name","toolName":"read","conditions":false}]`, "conditions"},
		{`[{"selectorType":"tool_name","toolName":"read","readOnlyHint":true}]`, "unknown entry field"},
		{`[{"selectorType":"tool_name","toolName":"read","conditions":null}]`, ""},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			_, err := parseToolEntries(types.StringValue(tc.raw))
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("wanted %q, got %v", tc.want, err)
			}
		})
	}
	tooMany := make([]map[string]interface{}, 251)
	if _, err := parseToolEntries(types.StringValue(marshalJSON(tooMany))); err == nil {
		t.Fatal("native entry limit ignored")
	}
	var response validator.StringResponse
	v := toolEntriesValidator{}
	v.ValidateString(context.Background(), validator.StringRequest{ConfigValue: types.StringNull()}, &response)
	v.ValidateString(context.Background(), validator.StringRequest{ConfigValue: types.StringValue(`{}`)}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("schema validator accepted non-array")
	}
	if v.Description(context.Background()) != v.MarkdownDescription(context.Background()) {
		t.Fatal("validator descriptions differ")
	}
}
func TestToolPolicyPayloadRejectsUnsupportedShapes(t *testing.T) {
	for _, field := range []string{"selectors", "conditions", "config", "type", "priority", "name", "description", "conditions-shape", "nonrate-config", "rate-empty", "rate-nested", "finish-config"} {
		t.Run(field, func(t *testing.T) {
			m := policyTestModel()
			switch field {
			case "selectors":
				m.Selectors = types.StringValue(`[]`)
			case "conditions":
				m.Conditions = types.StringValue(`null`)
			case "config":
				m.Config = types.StringValue(`{`)
			case "type":
				m.PolicyType = types.StringValue("trust_rule")
			case "priority":
				m.Priority = types.Int64Value(10001)
			case "name":
				m.Name = types.StringValue(" ")
			case "description":
				m.Description = types.StringValue(strings.Repeat("x", 4001))
			case "conditions-shape":
				m.Conditions = types.StringValue(`{}`)
			case "nonrate-config":
				m.PolicyType = types.StringValue("allow")
			case "rate-empty":
				m.Config = types.StringNull()
			case "rate-nested":
				m.Config = types.StringValue(`{"rateLimit":{"limit":1,"windowSeconds":1}}`)
			case "finish-config":
				m.Config = types.StringValue(`{"source":"app_gallery_finish","limit":1,"windowSeconds":1}`)
			}
			_, err := m.policy()
			if (err == nil) != (field == "rate-nested") {
				t.Fatalf("unexpected policy validation: %v", err)
			}
		})
	}
}
func TestToolProfilePayloadRejectsNativeUnsupportedInput(t *testing.T) {
	for _, field := range []string{"key", "name", "metadata", "entries", "reserved", "description"} {
		t.Run(field, func(t *testing.T) {
			m := profileTestModel()
			switch field {
			case "key":
				m.ProfileKey = types.StringValue("a/b")
			case "name":
				m.Name = types.StringValue(" ")
			case "metadata":
				m.Metadata = types.StringValue(`[]`)
			case "entries":
				m.Entries = types.StringValue(`{}`)
			case "reserved":
				m.Metadata = types.StringValue(`{"source":"app_gallery_finish"}`)
			case "description":
				m.Description = types.StringValue(strings.Repeat("x", 4001))
			}
			if _, err := m.payload(); err == nil {
				t.Fatal("invalid input accepted")
			}
		})
	}
}
func TestToolResourceMalformedStateFailsWithoutMutation(t *testing.T) {
	ctx := context.Background()
	for _, r := range []resource.Resource{NewPaperclipToolProfileResource(), NewPaperclipToolProfileBindingResource(), NewPaperclipPolicyResource()} {
		var sr resource.SchemaResponse
		r.Schema(ctx, resource.SchemaRequest{}, &sr)
		s := sr.Schema
		configure := r.(resource.ResourceWithConfigure)
		cr := resource.ConfigureResponse{}
		configure.Configure(ctx, resource.ConfigureRequest{}, &cr)
		configure.Configure(ctx, resource.ConfigureRequest{ProviderData: "invalid"}, &cr)
		if !cr.Diagnostics.HasError() {
			t.Fatal("invalid configure type accepted")
		}
		bad := tftypes.NewValue(tftypes.String, "malformed state")
		state := tfsdk.State{Schema: s, Raw: bad}
		plan := tfsdk.Plan{Schema: s, Raw: bad}
		create := resource.CreateResponse{State: state}
		r.Create(ctx, resource.CreateRequest{Plan: plan}, &create)
		if !create.Diagnostics.HasError() {
			t.Fatal("malformed create accepted")
		}
		read := resource.ReadResponse{State: state}
		r.Read(ctx, resource.ReadRequest{State: state}, &read)
		if !read.Diagnostics.HasError() {
			t.Fatal("malformed read accepted")
		}
		update := resource.UpdateResponse{State: state}
		r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &update)
		if !update.Diagnostics.HasError() {
			t.Fatal("malformed update accepted")
		}
		deleteResponse := resource.DeleteResponse{}
		r.Delete(ctx, resource.DeleteRequest{State: state}, &deleteResponse)
		if !deleteResponse.Diagnostics.HasError() {
			t.Fatal("malformed delete accepted")
		}
		modify := resource.ModifyPlanResponse{Plan: plan}
		r.(resource.ResourceWithModifyPlan).ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: state}, &modify)
		if !modify.Diagnostics.HasError() {
			t.Fatal("malformed plan accepted")
		}
		modify = resource.ModifyPlanResponse{}
		r.(resource.ResourceWithModifyPlan).ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: tfsdk.Plan{Schema: s, Raw: tftypes.NewValue(s.Type().TerraformType(ctx), nil)}}, &modify)
		requireToolDiagnostics(t, modify.Diagnostics)
		imp := resource.ImportStateResponse{State: tfsdk.State{Schema: s}}
		r.(resource.ResourceWithImportState).ImportState(ctx, resource.ImportStateRequest{ID: "invalid"}, &imp)
		if !imp.Diagnostics.HasError() {
			t.Fatal("incomplete import accepted")
		}
	}
}
