package provider

import (
	"context"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestGatewayImportedPolicyDefaultsPlanWithoutChanges(t *testing.T) {
	ctx := context.Background()
	r := &PaperclipMCPGatewayResource{}
	var s resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &s)
	for attr, kind := range map[string]string{"auth_config": "authConfig", "header_policy": "headerPolicy", "metadata_policy": "metadataPolicy", "on_demand_tools_config": "onDemandToolsConfig"} {
		t.Run(attr, func(t *testing.T) {
			state := types.StringValue(gatewayPolicyDefaults[kind])
			plan := types.StringValue(`{}`)
			request := planmodifier.StringRequest{StateValue: state, PlanValue: plan}
			response := planmodifier.StringResponse{PlanValue: plan}
			for _, modifier := range s.Schema.Attributes[attr].(schema.StringAttribute).PlanModifiers {
				modifier.PlanModifyString(ctx, request, &response)
			}
			if !response.PlanValue.Equal(state) {
				t.Fatal("imported native policy defaults produce a write plan")
			}
		})
	}
}

func TestGatewayOAuthOptionalDefaultsDoNotCauseImportDrift(t *testing.T) {
	if !semanticJSONEqualNormalized(`{}`, `{"oauth":{"dynamicClientRegistration":false,"authorizationCodePkce":false,"protectedResourceMetadataPath":null}}`, func(v interface{}) interface{} { return normalizeGatewayPolicy("authConfig", v) }) {
		t.Fatal("disabled optional OAuth fields cause drift")
	}
	if semanticJSONEqualNormalized(`{}`, `{"oauth":{"protectedResourceMetadataPath":"/metadata"}}`, func(v interface{}) interface{} { return normalizeGatewayPolicy("authConfig", v) }) {
		t.Fatal("native metadata path drift was masked")
	}
	if normalizeGatewayPolicy("authConfig", false) != false {
		t.Fatal("malformed policy was normalized into valid defaults")
	}
}

func TestGatewayPoliciesRejectInvalidNativeConfiguration(t *testing.T) {
	cases := []struct{ kind, value, diagnostic string }{
		{"authConfig", `[]`, "JSON"},
		{"authConfig", `{"unknown":true}`, "unsupported"},
		{"authConfig", `{"bearer":false}`, "object"},
		{"authConfig", `{"bearer":{"enabled":1}}`, "boolean"},
		{"authConfig", `{"oauth":{"enabled":true}}`, "OAuth"},
		{"authConfig", `{"oauth":{"reservedFor":"v2"}}`, "fixed"},
		{"authConfig", `{"oauth":{"protectedResourceMetadataPath":false}}`, "unsupported"},
		{"authConfig", `{"oauth":{"protectedResourceMetadataPath":" /metadata"}}`, "unsupported"},
		{"authConfig", `{"bearer":{"tokenPrefix":"other"}}`, "fixed"},
		{"authConfig", `{"bearer":{"defaultTtlSeconds":0}}`, "integer"},
		{"authConfig", `{"bearer":{"defaultTtlSeconds":1.5}}`, "integer"},
		{"authConfig", `{"version":2}`, "must be 1"},
		{"headerPolicy", `{"staticHeaders":{}}`, "array"},
		{"headerPolicy", `{"staticHeaders":[false]}`, "objects"},
		{"headerPolicy", `{"staticHeaders":[{"name":"bad name"}]}`, "valid name"},
		{"headerPolicy", `{"staticHeaders":[{"name":"X-Ok","other":true}]}`, "unsupported"},
		{"headerPolicy", `{"staticHeaders":[{"name":"X-Ok","valueRef":true}]}`, "valueRef"},
		{"headerPolicy", `{"staticHeaders":[{"name":"X-Ok","valueRef":" padded"}]}`, "valueRef"},
		{"headerPolicy", `{"staticHeaders":[{"name":"X-Ok","value":true}]}`, "value"},
		{"headerPolicy", `{"callerPassthrough":{"allowedHeaders":[1]}}`, "invalid header"},
		{"headerPolicy", `{"generatedMetadata":{"allowedHeaders":` + strings.TrimSuffix(strings.Repeat(`"X-Ok",`, 21), ",") + `]}}`, "JSON"},
		{"metadataPolicy", `{"forwardCompanyId":"yes"}`, "boolean"},
		{"onDemandToolsConfig", `{"runToolName":"other"}`, "fixed"},
	}
	// Cardinality is checked on well-formed arrays, independently of parse errors.
	cases = append(cases, struct{ kind, value, diagnostic string }{"headerPolicy", `{"generatedMetadata":{"allowedHeaders":[` + strings.TrimSuffix(strings.Repeat(`"X-Ok",`, 21), ",") + `]}}`, "limit 20"})
	for _, tc := range cases {
		t.Run(tc.kind+tc.value, func(t *testing.T) {
			_, err := gatewayPolicy(tc.kind, types.StringValue(tc.value))
			if err == nil || !strings.Contains(err.Error(), tc.diagnostic) {
				t.Fatalf("expected %s, got %v", tc.diagnostic, err)
			}
		})
	}
	valid := map[string]string{"authConfig": `{"bearer":{"defaultTtlSeconds":31536000},"oauth":{"protectedResourceMetadataPath":"/metadata","dynamicClientRegistration":false,"authorizationCodePkce":false}}`, "headerPolicy": `{"generatedMetadata":{"allowedHeaders":["X-Trace"]},"staticHeaders":[{"name":"X-Ok","valueRef":"public-reference","value":null}]}`}
	for kind, value := range valid {
		if _, err := gatewayPolicy(kind, types.StringValue(value)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := gatewayPolicy("authConfig", types.StringUnknown()); err != nil {
		t.Fatal(err)
	}
}
func TestGatewayValidationRejectsKnownReferencesBeforeMutation(t *testing.T) {
	f := newGatewayFixture(t)
	r := &PaperclipMCPGatewayResource{}
	s := configureTestResource(t, f.server.URL, r)
	ctx := context.Background()
	for _, change := range []struct {
		name   string
		change func(*gatewayModel)
	}{
		{"name", func(m *gatewayModel) { m.Name = types.StringValue(" ") }},
		{"slug", func(m *gatewayModel) { m.Slug = types.StringValue("bad/slug") }},
		{"archive", func(m *gatewayModel) { m.Status = types.StringValue("archived") }},
		{"draft-create", func(m *gatewayModel) { m.Status = types.StringValue("draft") }},
		{"scope-none", func(m *gatewayModel) { m.ContextScopeID = types.StringValue("x") }},
		{"missing-scope", func(m *gatewayModel) { m.ContextScopeType = types.StringValue("project") }},
		{"invalid-profile", func(m *gatewayModel) { m.ProfileID = types.StringValue("missing") }},
		{"invalid-project", func(m *gatewayModel) { m.ProjectID = types.StringValue("missing") }},
		{"invalid-issue", func(m *gatewayModel) { m.IssueID = types.StringValue("missing") }},
		{"policy", func(m *gatewayModel) { m.AuthConfig = types.StringValue(`{"oauth":{"enabled":true}}`) }},
		{"metadata", func(m *gatewayModel) { m.Metadata = types.StringValue(`false`) }},
	} {
		t.Run(change.name, func(t *testing.T) {
			m := gatewayTestModel()
			change.change(&m)
			plan := toolPlan(t, s, &m)
			modify := resource.ModifyPlanResponse{Plan: plan}
			r.ModifyPlan(ctx, resource.ModifyPlanRequest{Plan: plan, State: tfsdk.State{Schema: s}}, &modify)
			if !modify.Diagnostics.HasError() {
				t.Fatal("known invalid plan accepted")
			}
			create := resource.CreateResponse{State: tfsdk.State{Schema: s}}
			r.Create(ctx, resource.CreateRequest{Plan: plan}, &create)
			if !create.Diagnostics.HasError() || len(f.events) != 0 {
				t.Fatal("invalid reference wrote before validation")
			}
		})
	}
	m := gatewayTestModel()
	m.CompanyID = types.StringUnknown()
	m.ProfileID = types.StringUnknown()
	if err := r.validate(ctx, m, true); err != nil {
		t.Fatal(err)
	}
	m.CompanyID = types.StringValue("comp-1")
	m.ContextScopeType = types.StringUnknown()
	if err := r.validate(ctx, m, true); err != nil {
		t.Fatal(err)
	}
}
func TestGatewayTokenRejectsInvalidGenerationExpiryActionsAndIdentity(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*gatewayTokenModel)
	}{
		{"name", func(m *gatewayTokenModel) { m.Name = types.StringValue("") }},
		{"label", func(m *gatewayTokenModel) { m.ClientLabel = types.StringValue(strings.Repeat("a", 161)) }},
		{"generation", func(m *gatewayTokenModel) { m.Generation = types.Int64Value(0) }},
		{"expiry", func(m *gatewayTokenModel) { m.ExpiresAt = types.StringValue("never") }},
		{"actions-json", func(m *gatewayTokenModel) { m.AllowedActions = types.StringValue(`{}`) }},
		{"actions-empty", func(m *gatewayTokenModel) { m.AllowedActions = types.StringValue(`[]`) }},
		{"actions-unknown", func(m *gatewayTokenModel) { m.AllowedActions = types.StringValue(`["other"]`) }},
		{"actions-duplicate", func(m *gatewayTokenModel) { m.AllowedActions = types.StringValue(`["tools/list","tools/list"]`) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := gatewayTokenTestModel()
			tc.change(&m)
			if _, err := m.payload(); err == nil {
				t.Fatal("invalid token configuration accepted")
			}
		})
	}
	prior := gatewayTokenTestModel()
	for _, tc := range []struct {
		name   string
		change func(*gatewayTokenModel)
	}{
		{"company", func(m *gatewayTokenModel) { m.CompanyID = types.StringValue("other") }},
		{"gateway", func(m *gatewayTokenModel) { m.GatewayID = types.StringValue("other") }},
		{"name", func(m *gatewayTokenModel) { m.Name = types.StringValue("other") }},
		{"decrease", func(m *gatewayTokenModel) { m.Generation = types.Int64Value(0) }},
		{"label", func(m *gatewayTokenModel) { m.ClientLabel = types.StringValue("other") }},
		{"expiry", func(m *gatewayTokenModel) { m.ExpiresAt = types.StringValue("2101-01-01T00:00:00Z") }},
		{"actions", func(m *gatewayTokenModel) { m.AllowedActions = types.StringValue(`["tools/list"]`) }},
	} {
		t.Run("transition/"+tc.name, func(t *testing.T) {
			m := prior
			tc.change(&m)
			if err := gatewayTokenTransition(m, prior); err == nil {
				t.Fatal("unsupported transition accepted")
			}
		})
	}
	unknown := gatewayTokenModel{CompanyID: types.StringUnknown(), GatewayID: types.StringUnknown(), Name: types.StringUnknown(), Generation: types.Int64Unknown(), ClientLabel: types.StringUnknown(), ExpiresAt: types.StringUnknown(), AllowedActions: types.StringUnknown()}
	if err := validateGatewayToken(context.Background(), nil, unknown); err != nil {
		t.Fatal(err)
	}
}
