package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"git.guseynov.org/guseynov/paperclip/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

func TestBoolUseStateForUnknownModifier(t *testing.T) {
	ctx := context.Background()
	m := boolUseStateForUnknownModifier{}
	if m.Description(ctx) == "" || m.MarkdownDescription(ctx) == "" {
		t.Fatal("empty description")
	}

	// 1. Plan value not unknown -> no change
	req1 := planmodifier.BoolRequest{
		PlanValue:  types.BoolValue(true),
		StateValue: types.BoolValue(false),
	}
	resp1 := &planmodifier.BoolResponse{
		PlanValue: req1.PlanValue,
	}
	m.PlanModifyBool(ctx, req1, resp1)
	if !resp1.PlanValue.ValueBool() {
		t.Fatal("expected plan value true")
	}

	// 2. Plan value unknown, state value null -> no change
	req2 := planmodifier.BoolRequest{
		PlanValue:  types.BoolUnknown(),
		StateValue: types.BoolNull(),
	}
	resp2 := &planmodifier.BoolResponse{
		PlanValue: req2.PlanValue,
	}
	m.PlanModifyBool(ctx, req2, resp2)
	if !resp2.PlanValue.IsUnknown() {
		t.Fatal("expected plan value to stay unknown")
	}

	// 3. Plan value unknown, state value known -> adopt state
	req3 := planmodifier.BoolRequest{
		PlanValue:  types.BoolUnknown(),
		StateValue: types.BoolValue(true),
	}
	resp3 := &planmodifier.BoolResponse{
		PlanValue: req3.PlanValue,
	}
	m.PlanModifyBool(ctx, req3, resp3)
	if !resp3.PlanValue.ValueBool() {
		t.Fatal("expected plan value to be true")
	}
}

func TestEnvBindingUnit(t *testing.T) {
	ctx := context.Background()

	// 1. EnvBindingAttributeType and EnvBindingMapAttribute
	attrs := EnvBindingAttributeType()
	for _, expected := range []string{"type", "value", "secret_id", "key", "version", "projection_class", "projection_allowlist_key", "required", "allow_missing_override"} {
		if _, ok := attrs[expected]; !ok {
			t.Fatalf("missing attribute %q in EnvBindingAttributeType", expected)
		}
	}
	mapAttr := EnvBindingMapAttribute("test description")
	if len(mapAttr.NestedObject.Attributes) != len(attrs) {
		t.Fatal("EnvBindingMapAttribute nested attributes mismatch")
	}

	// 2. IsRedactedSentinel
	for _, sentinel := range []string{"[REDACTED]", "[redacted]", "<redacted>", "<REDACTED>", "redacted", "REDACTED", "  [redacted]  "} {
		if !IsRedactedSentinel(sentinel) {
			t.Fatalf("expected %q to be recognized as redacted sentinel", sentinel)
		}
	}
	for _, notSentinel := range []string{"my-api-key", "secret123", "", "   "} {
		if IsRedactedSentinel(notSentinel) {
			t.Fatalf("did not expect %q to be recognized as redacted sentinel", notSentinel)
		}
	}

	// 3. IsAllowlistedClass3Key
	if !IsAllowlistedClass3Key("slack.bot_token", "agent", "env.SLACK_BOT_TOKEN") {
		t.Fatal("expected slack.bot_token for agent to be allowlisted")
	}
	if !IsAllowlistedClass3Key("slack.bot_token", "", "") {
		t.Fatal("expected slack.bot_token without constraints to be allowlisted")
	}
	if !IsAllowlistedClass3Key("discord.bot_token", "routine", "env.DISCORD_BOT_TOKEN") {
		t.Fatal("expected discord.bot_token for routine to be allowlisted")
	}
	if !IsAllowlistedClass3Key("discord.bot_token", "tool_connection", "credentials.bot_token") {
		t.Fatal("expected discord.bot_token for tool_connection to be allowlisted")
	}
	if IsAllowlistedClass3Key("unknown.token", "agent", "env.SLACK_BOT_TOKEN") {
		t.Fatal("unexpected allowlisted key")
	}
	if IsAllowlistedClass3Key("slack.bot_token", "unsupported_type", "env.SLACK_BOT_TOKEN") {
		t.Fatal("unexpected allowlisted targetType")
	}
	if IsAllowlistedClass3Key("slack.bot_token", "agent", "env.OTHER_TOKEN") {
		t.Fatal("unexpected allowlisted configPath")
	}

	// 4. ValidateEnvBindingPlan
	basePath := path.Root("env")

	// Unknown type
	diags := ValidateEnvBindingPlan(basePath, EnvBindingModel{Type: types.StringUnknown()}, "agent", "env.KEY")
	if diags.HasError() {
		t.Fatal("unknown type should produce no diags")
	}

	// Null / Empty type
	diags = ValidateEnvBindingPlan(basePath, EnvBindingModel{Type: types.StringNull()}, "agent", "env.KEY")
	if !diags.HasError() {
		t.Fatal("null type should produce error")
	}
	diags = ValidateEnvBindingPlan(basePath, EnvBindingModel{Type: types.StringValue("   ")}, "agent", "env.KEY")
	if !diags.HasError() {
		t.Fatal("empty type should produce error")
	}

	// Plain binding
	diags = ValidateEnvBindingPlan(basePath, EnvBindingModel{
		Type:  types.StringValue("plain"),
		Value: types.StringNull(),
	}, "agent", "env.KEY")
	if !diags.HasError() {
		t.Fatal("plain with null value should produce error")
	}

	diags = ValidateEnvBindingPlan(basePath, EnvBindingModel{
		Type:  types.StringValue("plain"),
		Value: types.StringValue(""),
	}, "agent", "env.KEY")
	if !diags.HasError() {
		t.Fatal("plain with empty value should produce error")
	}

	diags = ValidateEnvBindingPlan(basePath, EnvBindingModel{
		Type:  types.StringValue("plain"),
		Value: types.StringValue("[REDACTED]"),
	}, "agent", "env.KEY")
	if !diags.HasError() {
		t.Fatal("plain with redacted sentinel value should produce error")
	}

	diags = ValidateEnvBindingPlan(basePath, EnvBindingModel{
		Type:                   types.StringValue("plain"),
		Value:                  types.StringValue("valid_value"),
		SecretID:               types.StringValue("sec-1"),
		Key:                    types.StringValue("key1"),
		ProjectionClass:        types.StringValue("unclassified"),
		ProjectionAllowlistKey: types.StringValue("slack.bot_token"),
	}, "agent", "env.KEY")
	if len(diags.Errors()) != 4 {
		t.Fatalf("expected 4 errors for invalid attributes on plain, got %d", len(diags.Errors()))
	}

	diags = ValidateEnvBindingPlan(basePath, EnvBindingModel{
		Type:  types.StringValue("plain"),
		Value: types.StringValue("valid_value"),
	}, "agent", "env.KEY")
	if diags.HasError() {
		t.Fatalf("valid plain binding produced unexpected errors: %v", diags)
	}

	// Secret Ref binding
	diags = ValidateEnvBindingPlan(basePath, EnvBindingModel{
		Type:     types.StringValue("secret_ref"),
		SecretID: types.StringNull(),
	}, "agent", "env.KEY")
	if !diags.HasError() {
		t.Fatal("secret_ref with null secret_id should produce error")
	}

	diags = ValidateEnvBindingPlan(basePath, EnvBindingModel{
		Type:     types.StringValue("secret_ref"),
		SecretID: types.StringValue("   "),
	}, "agent", "env.KEY")
	if !diags.HasError() {
		t.Fatal("secret_ref with empty secret_id should produce error")
	}

	diags = ValidateEnvBindingPlan(basePath, EnvBindingModel{
		Type:     types.StringValue("secret_ref"),
		SecretID: types.StringValue("sec-1"),
		Value:    types.StringValue("plaintext"),
		Key:      types.StringValue("key1"),
	}, "agent", "env.KEY")
	if len(diags.Errors()) != 2 {
		t.Fatalf("expected 2 errors for value/key on secret_ref, got %d", len(diags.Errors()))
	}

	diags = ValidateEnvBindingPlan(basePath, EnvBindingModel{
		Type:            types.StringValue("secret_ref"),
		SecretID:        types.StringValue("sec-1"),
		ProjectionClass: types.StringValue("invalid_proj"),
	}, "agent", "env.KEY")
	if !diags.HasError() {
		t.Fatal("secret_ref with invalid projection_class should produce error")
	}

	diags = ValidateEnvBindingPlan(basePath, EnvBindingModel{
		Type:            types.StringValue("secret_ref"),
		SecretID:        types.StringValue("sec-1"),
		ProjectionClass: types.StringValue("class_3_static_lease"),
	}, "agent", "env.KEY")
	if !diags.HasError() {
		t.Fatal("class_3_static_lease without projection_allowlist_key should produce error")
	}

	diags = ValidateEnvBindingPlan(basePath, EnvBindingModel{
		Type:                   types.StringValue("secret_ref"),
		SecretID:               types.StringValue("sec-1"),
		ProjectionClass:        types.StringValue("class_3_static_lease"),
		ProjectionAllowlistKey: types.StringValue("unauthorized.key"),
	}, "agent", "env.KEY")
	if !diags.HasError() {
		t.Fatal("class_3_static_lease with unauthorized projection_allowlist_key should produce error")
	}

	diags = ValidateEnvBindingPlan(basePath, EnvBindingModel{
		Type:                   types.StringValue("secret_ref"),
		SecretID:               types.StringValue("sec-1"),
		ProjectionClass:        types.StringValue("class_3_static_lease"),
		ProjectionAllowlistKey: types.StringValue("slack.bot_token"),
	}, "agent", "env.SLACK_BOT_TOKEN")
	if diags.HasError() {
		t.Fatalf("valid class_3_static_lease produced error: %v", diags)
	}

	diags = ValidateEnvBindingPlan(basePath, EnvBindingModel{
		Type:                   types.StringValue("secret_ref"),
		SecretID:               types.StringValue("sec-1"),
		ProjectionClass:        types.StringValue("unclassified"),
		ProjectionAllowlistKey: types.StringValue("slack.bot_token"),
	}, "agent", "env.KEY")
	if !diags.HasError() {
		t.Fatal("unclassified with projection_allowlist_key should produce error")
	}

	diags = ValidateEnvBindingPlan(basePath, EnvBindingModel{
		Type:            types.StringValue("secret_ref"),
		SecretID:        types.StringValue("sec-1"),
		ProjectionClass: types.StringValue("unclassified"),
	}, "agent", "env.KEY")
	if diags.HasError() {
		t.Fatalf("valid unclassified secret_ref produced error: %v", diags)
	}

	// User Secret Ref binding
	diags = ValidateEnvBindingPlan(basePath, EnvBindingModel{
		Type: types.StringValue("user_secret_ref"),
		Key:  types.StringNull(),
	}, "agent", "env.KEY")
	if !diags.HasError() {
		t.Fatal("user_secret_ref with null key should produce error")
	}

	diags = ValidateEnvBindingPlan(basePath, EnvBindingModel{
		Type: types.StringValue("user_secret_ref"),
		Key:  types.StringValue("invalid key with spaces!"),
	}, "agent", "env.KEY")
	if !diags.HasError() {
		t.Fatal("user_secret_ref with invalid regex key should produce error")
	}

	diags = ValidateEnvBindingPlan(basePath, EnvBindingModel{
		Type: types.StringValue("user_secret_ref"),
		Key:  types.StringValue(strings.Repeat("a", 121)),
	}, "agent", "env.KEY")
	if !diags.HasError() {
		t.Fatal("user_secret_ref with overly long key should produce error")
	}

	diags = ValidateEnvBindingPlan(basePath, EnvBindingModel{
		Type:                   types.StringValue("user_secret_ref"),
		Key:                    types.StringValue("valid_key"),
		Value:                  types.StringValue("plaintext"),
		SecretID:               types.StringValue("sec-1"),
		ProjectionClass:        types.StringValue("unclassified"),
		ProjectionAllowlistKey: types.StringValue("slack.bot_token"),
	}, "agent", "env.KEY")
	if len(diags.Errors()) != 4 {
		t.Fatalf("expected 4 errors for invalid attributes on user_secret_ref, got %d", len(diags.Errors()))
	}

	diags = ValidateEnvBindingPlan(basePath, EnvBindingModel{
		Type: types.StringValue("user_secret_ref"),
		Key:  types.StringValue("valid_key"),
	}, "agent", "env.KEY")
	if diags.HasError() {
		t.Fatalf("valid user_secret_ref produced error: %v", diags)
	}

	// Unsupported type
	diags = ValidateEnvBindingPlan(basePath, EnvBindingModel{
		Type: types.StringValue("unknown_binding_type"),
	}, "agent", "env.KEY")
	if !diags.HasError() {
		t.Fatal("unknown binding type should produce error")
	}

	// 5. ValidateEnvBindingsMapPlan
	bindingsMap := map[string]EnvBindingModel{
		"VALID": {Type: types.StringValue("plain"), Value: types.StringValue("ok")},
		"BAD":   {Type: types.StringValue("plain"), Value: types.StringValue("[REDACTED]")},
	}
	diags = ValidateEnvBindingsMapPlan(basePath, bindingsMap, "agent")
	if len(diags.Errors()) != 1 {
		t.Fatalf("expected 1 error from ValidateEnvBindingsMapPlan, got %d", len(diags.Errors()))
	}

	// 6. ValidateEnvBindingsJSONPlan
	if d := ValidateEnvBindingsJSONPlan(basePath, "", "agent"); d.HasError() {
		t.Fatal("empty json produced error")
	}
	if d := ValidateEnvBindingsJSONPlan(basePath, "null", "agent"); d.HasError() {
		t.Fatal("null json produced error")
	}
	if d := ValidateEnvBindingsJSONPlan(basePath, "{invalid json", "agent"); !d.HasError() {
		t.Fatal("invalid json should produce error")
	}

	jsonTests := `{
		"PLAIN_STR": "[REDACTED]",
		"PLAIN_OK": "hello",
		"OBJ_PLAIN_BAD": {"type": "plain", "value": "[REDACTED]"},
		"OBJ_PLAIN_EMPTY": {"type": "plain", "value": ""},
		"OBJ_PLAIN_OK": {"type": "plain", "value": "secret-val"},
		"OBJ_SEC_NO_ID": {"type": "secret_ref"},
		"OBJ_SEC_OK": {"type": "secret_ref", "secretId": "sec-1"},
		"OBJ_SEC_CLASS3_BAD": {"type": "secret_ref", "secretId": "sec-1", "projectionClass": "class_3_static_lease", "projectionAllowlistKey": "bad.key"},
		"OBJ_SEC_CLASS3_OK": {"type": "secret_ref", "secretId": "sec-1", "projectionClass": "class_3_static_lease", "projectionAllowlistKey": "slack.bot_token"},
		"OBJ_USD_BAD": {"type": "user_secret_ref", "key": "bad key!"},
		"OBJ_USD_OK": {"type": "user_secret_ref", "key": "good_key"},
		"OBJ_NO_TYPE": {},
		"OBJ_UNKNOWN_TYPE": {"type": "foo"}
	}`
	diags = ValidateEnvBindingsJSONPlan(basePath, jsonTests, "agent")
	if len(diags.Errors()) < 7 {
		t.Fatalf("expected multiple validation errors in ValidateEnvBindingsJSONPlan, got %d: %v", len(diags.Errors()), diags)
	}

	// 7. ValidateEnvBindingsApply & ValidateEnvBindingsJSONApply
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/companies/comp-1/secrets" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"secrets": []map[string]interface{}{
					{"id": "sec-valid", "companyId": "comp-1", "name": "my-secret"},
				},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	cli, _ := client.NewClient(server.URL, "token")

	// Apply map
	if d := ValidateEnvBindingsApply(ctx, nil, "comp-1", nil); d.HasError() {
		t.Fatal("nil client should not error")
	}
	if d := ValidateEnvBindingsApply(ctx, cli, "", nil); d.HasError() {
		t.Fatal("empty company should not error")
	}

	applyMap := map[string]EnvBindingModel{
		"PLAIN":   {Type: types.StringValue("plain"), Value: types.StringValue("val")},
		"SEC_EMP": {Type: types.StringValue("secret_ref"), SecretID: types.StringValue("")},
		"SEC_OK":  {Type: types.StringValue("secret_ref"), SecretID: types.StringValue("sec-valid")},
		"SEC_BAD": {Type: types.StringValue("secret_ref"), SecretID: types.StringValue("sec-cross-company")},
	}
	diags = ValidateEnvBindingsApply(ctx, cli, "comp-1", applyMap)
	if len(diags.Errors()) != 1 {
		t.Fatalf("expected 1 error for non-existent secret in ValidateEnvBindingsApply, got %d: %v", len(diags.Errors()), diags)
	}

	// Apply JSON
	if d := ValidateEnvBindingsJSONApply(ctx, nil, "comp-1", "{}"); d.HasError() {
		t.Fatal("nil client should not error")
	}
	if d := ValidateEnvBindingsJSONApply(ctx, cli, "", "{}"); d.HasError() {
		t.Fatal("empty company should not error")
	}
	if d := ValidateEnvBindingsJSONApply(ctx, cli, "comp-1", "{bad json"); d.HasError() {
		t.Fatal("bad json should be ignored during apply validation")
	}
	if d := ValidateEnvBindingsJSONApply(ctx, cli, "comp-1", ""); d.HasError() {
		t.Fatal("empty json should not error")
	}

	applyJSON := `{
		"STR": "val",
		"OTHER": {"type": "plain", "value": "val"},
		"EMP_SEC": {"type": "secret_ref", "secretId": ""},
		"VALID_SEC": {"type": "secret_ref", "secretId": "sec-valid"},
		"BAD_SEC": {"type": "secret_ref", "secretId": "sec-cross-company"}
	}`
	diags = ValidateEnvBindingsJSONApply(ctx, cli, "comp-1", applyJSON)
	if len(diags.Errors()) != 1 {
		t.Fatalf("expected 1 error for non-existent secret in ValidateEnvBindingsJSONApply, got %d: %v", len(diags.Errors()), diags)
	}

	// 8. EnvBindingsMapToAPI
	apiMap := EnvBindingsMapToAPI(map[string]EnvBindingModel{
		"PLAIN": {Type: types.StringValue("plain"), Value: types.StringValue("val")},
		"SEC": {
			Type:                   types.StringValue("secret_ref"),
			SecretID:               types.StringValue("sec-1"),
			Version:                types.StringValue("2"),
			ProjectionClass:        types.StringValue("class_3_static_lease"),
			ProjectionAllowlistKey: types.StringValue("slack.bot_token"),
		},
		"USD": {
			Type:                 types.StringValue("user_secret_ref"),
			Key:                  types.StringValue("openai_key"),
			Version:              types.StringValue("latest"),
			Required:             types.BoolValue(true),
			AllowMissingOverride: types.BoolValue(false),
		},
	})
	if len(apiMap) != 3 {
		t.Fatalf("expected 3 entries in apiMap, got %d", len(apiMap))
	}
	if pEntry, ok := apiMap["PLAIN"].(map[string]interface{}); !ok || pEntry["type"] != "plain" || pEntry["value"] != "val" {
		t.Fatalf("invalid plain api projection: %#v", apiMap["PLAIN"])
	}
	if sEntry, ok := apiMap["SEC"].(map[string]interface{}); !ok || sEntry["secretId"] != "sec-1" || sEntry["version"] != "2" || sEntry["projectionClass"] != "class_3_static_lease" || sEntry["projectionAllowlistKey"] != "slack.bot_token" {
		t.Fatalf("invalid secret_ref api projection: %#v", apiMap["SEC"])
	}
	if uEntry, ok := apiMap["USD"].(map[string]interface{}); !ok || uEntry["key"] != "openai_key" || uEntry["version"] != "latest" || uEntry["required"] != true || uEntry["allowMissingOverride"] != false {
		t.Fatalf("invalid user_secret_ref api projection: %#v", apiMap["USD"])
	}
}

func TestPaperclipSecretCoverage(t *testing.T) {
	ctx := context.Background()
	secretResource := NewPaperclipSecretResource().(*PaperclipSecretResource)
	var schemaResp resource.SchemaResponse
	secretResource.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	// 1. UpgradeState v0 to v1
	upgrader := secretResource.UpgradeState(ctx)[0]
	v0State := tfsdk.State{Schema: *upgrader.PriorSchema}
	v0Model := struct {
		ID          types.String `tfsdk:"id"`
		CompanyID   types.String `tfsdk:"company_id"`
		Name        types.String `tfsdk:"name"`
		Description types.String `tfsdk:"description"`
		Value       types.String `tfsdk:"value"`
		Rotation    types.String `tfsdk:"rotation"`
	}{
		ID:          types.StringValue("sec-v0"),
		CompanyID:   types.StringValue("comp-1"),
		Name:        types.StringValue("old-sec"),
		Description: types.StringValue("old-desc"),
		Value:       types.StringValue("old-val"),
		Rotation:    types.StringValue("rev-1"),
	}
	_ = v0State.Set(ctx, &v0Model)
	upgradeResp := resource.UpgradeStateResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	upgrader.StateUpgrader(ctx, resource.UpgradeStateRequest{State: &v0State}, &upgradeResp)
	if upgradeResp.Diagnostics.HasError() {
		t.Fatalf("UpgradeState failed: %v", upgradeResp.Diagnostics)
	}
	var upgradedModel PaperclipSecretResourceModel
	_ = upgradeResp.State.Get(ctx, &upgradedModel)
	if upgradedModel.ID.ValueString() != "sec-v0" || upgradedModel.ProviderType.ValueString() != "local_encrypted" || upgradedModel.Status.ValueString() != "active" || upgradedModel.ManagedMode.ValueString() != "paperclip_managed" {
		t.Fatalf("UpgradeState mapped incorrectly: %#v", upgradedModel)
	}

	badV0State := tfsdk.State{Schema: *upgrader.PriorSchema, Raw: tftypes.NewValue(tftypes.String, "bad")}
	badUpgradeResp := resource.UpgradeStateResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	upgrader.StateUpgrader(ctx, resource.UpgradeStateRequest{State: &badV0State}, &badUpgradeResp)
	if !badUpgradeResp.Diagnostics.HasError() {
		t.Fatal("expected error from bad state in UpgradeState")
	}

	// 2. normalizePaperclipSecretState variations
	normModel := PaperclipSecretResourceModel{}
	normalizePaperclipSecretState(&normModel, &client.PaperclipSecret{
		ID:                 "sec-1",
		CompanyID:          "comp-1",
		Name:               "sec-name",
		Description:        "desc",
		Key:                "k1",
		Provider:           "aws_secrets_manager",
		Status:             "disabled",
		ManagedMode:        "external_reference",
		ExternalRef:        "arn:aws:secretsmanager:us-east-1:123:secret:my-sec",
		ProviderConfigID:   "spc-1",
		ProviderVersionRef: "v1",
		ProviderMetadata:   map[string]interface{}{"region": "us-east-1"},
		LatestVersion:      5,
		LastRotatedAt:      "2026-10-07T12:00:00Z",
	})
	if normModel.LatestVersion.ValueInt64() != 5 || normModel.ProviderType.ValueString() != "aws_secrets_manager" || normModel.ManagedMode.ValueString() != "external_reference" {
		t.Fatalf("normalizePaperclipSecretState failed: %#v", normModel)
	}

	normEmpty := PaperclipSecretResourceModel{
		LatestVersion: types.Int64Unknown(),
	}
	normalizePaperclipSecretState(&normEmpty, &client.PaperclipSecret{
		ID:   "sec-2",
		Name: "sec-empty",
	})
	if !normEmpty.LatestVersion.IsNull() || normEmpty.ProviderType.ValueString() != "local_encrypted" || normEmpty.Status.ValueString() != "active" || normEmpty.ManagedMode.ValueString() != "paperclip_managed" {
		t.Fatalf("normalizePaperclipSecretState empty defaults failed: %#v", normEmpty)
	}

	// 3. ValidateConfig
	valConfig := func(m PaperclipSecretResourceModel) resource.ValidateConfigResponse {
		var resp resource.ValidateConfigResponse
		plan := tfsdk.Plan{Schema: schemaResp.Schema}
		_ = plan.Set(ctx, &m)
		cfg := tfsdk.Config{Schema: schemaResp.Schema, Raw: plan.Raw}
		secretResource.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: cfg}, &resp)
		return resp
	}

	if r := valConfig(PaperclipSecretResourceModel{
		Status:       types.StringValue("invalid_status"),
		ProviderType: types.StringValue("invalid_provider"),
		ManagedMode:  types.StringValue("invalid_mode"),
		Key:          types.StringValue("invalid key with space"),
	}); len(r.Diagnostics.Errors()) != 4 {
		t.Fatalf("expected 4 errors in ValidateConfig, got %d: %v", len(r.Diagnostics.Errors()), r.Diagnostics)
	}

	if r := valConfig(PaperclipSecretResourceModel{
		ManagedMode: types.StringValue("external_reference"),
		Value:       types.StringValue("forbidden-plaintext"),
	}); !r.Diagnostics.HasError() {
		t.Fatal("external_reference with value should produce error")
	}

	if r := valConfig(PaperclipSecretResourceModel{
		ManagedMode: types.StringValue("external_reference"),
		ExternalRef: types.StringNull(),
	}); !r.Diagnostics.HasError() {
		t.Fatal("external_reference with null external_ref should produce error")
	}

	if r := valConfig(PaperclipSecretResourceModel{
		ManagedMode: types.StringValue("paperclip_managed"),
		ExternalRef: types.StringValue("forbidden-ext-ref"),
	}); !r.Diagnostics.HasError() {
		t.Fatal("paperclip_managed with external_ref should produce error")
	}

	// 4. Configure
	var confResp resource.ConfigureResponse
	secretResource.Configure(ctx, resource.ConfigureRequest{ProviderData: nil}, &confResp)
	if confResp.Diagnostics.HasError() {
		t.Fatal("nil providerData should not error")
	}
	secretResource.Configure(ctx, resource.ConfigureRequest{ProviderData: "invalid"}, &confResp)
	if !confResp.Diagnostics.HasError() {
		t.Fatal("invalid providerData should error")
	}

	// 5. Create, Read, Update, Delete with Mock Server
	var createdSecret client.PaperclipSecret
	var lastUpdateOpts client.UpdateSecretOptions
	var lastRotateOpts client.RotateSecretOptions
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		p := r.URL.Path

		switch {
		case r.Method == http.MethodPost && p == "/api/companies/comp-1/secrets":
			var opts client.CreateSecretOptions
			_ = json.NewDecoder(r.Body).Decode(&opts)
			createdSecret = client.PaperclipSecret{
				ID:                 "sec-created",
				CompanyID:          "comp-1",
				Name:               opts.Name,
				Key:                opts.Key,
				Provider:           opts.Provider,
				Status:             "active",
				ManagedMode:        opts.ManagedMode,
				ExternalRef:        opts.ExternalRef,
				ProviderConfigID:   opts.ProviderConfigID,
				ProviderVersionRef: opts.ProviderVersionRef,
				ProviderMetadata:   opts.ProviderMetadata,
			}
			_ = json.NewEncoder(w).Encode(createdSecret)
		case r.Method == http.MethodPatch && p == "/api/secrets/sec-created":
			_ = json.NewDecoder(r.Body).Decode(&lastUpdateOpts)
			if lastUpdateOpts.Name != "" {
				createdSecret.Name = lastUpdateOpts.Name
			}
			if lastUpdateOpts.Description != nil {
				createdSecret.Description = *lastUpdateOpts.Description
			}
			if lastUpdateOpts.Status != "" {
				createdSecret.Status = lastUpdateOpts.Status
			}
			if lastUpdateOpts.ProviderConfigID != nil {
				createdSecret.ProviderConfigID = *lastUpdateOpts.ProviderConfigID
			}
			if lastUpdateOpts.ExternalRef != nil {
				createdSecret.ExternalRef = *lastUpdateOpts.ExternalRef
			}
			if lastUpdateOpts.ProviderMetadata != nil {
				createdSecret.ProviderMetadata = lastUpdateOpts.ProviderMetadata
			}
			_ = json.NewEncoder(w).Encode(createdSecret)
		case r.Method == http.MethodPost && p == "/api/secrets/sec-created/rotate":
			_ = json.NewDecoder(r.Body).Decode(&lastRotateOpts)
			createdSecret.LatestVersion++
			createdSecret.LastRotatedAt = "2026-10-07T14:00:00Z"
			_ = json.NewEncoder(w).Encode(createdSecret)
		case r.Method == http.MethodGet && p == "/api/companies/comp-1/secrets":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"secrets": []client.PaperclipSecret{createdSecret},
			})
		case r.Method == http.MethodDelete && p == "/api/secrets/sec-created":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodDelete && p == "/api/secrets/not-found":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		default:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"internal error"}`))
		}
	}))
	defer server.Close()

	cli, _ := client.NewClient(server.URL, "token")
	secretResource.client = cli

	// Create external_reference with disabled status
	createPlanModel := PaperclipSecretResourceModel{
		CompanyID:        types.StringValue("comp-1"),
		Name:             types.StringValue("ext-secret"),
		Key:              types.StringValue("ext_key"),
		ProviderType:     types.StringValue("vault"),
		Status:           types.StringValue("disabled"),
		ManagedMode:      types.StringValue("external_reference"),
		ExternalRef:      types.StringValue("vault/secret/path"),
		ProviderMetadata: types.StringValue(`{"engine":"kv-v2"}`),
	}
	createPlan := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = createPlan.Set(ctx, &createPlanModel)
	createCfg := tfsdk.Config{Schema: schemaResp.Schema, Raw: createPlan.Raw}
	var cResp resource.CreateResponse
	cResp.State = tfsdk.State{Schema: schemaResp.Schema}
	secretResource.Create(ctx, resource.CreateRequest{Plan: createPlan, Config: createCfg}, &cResp)
	if cResp.Diagnostics.HasError() {
		t.Fatalf("Create failed: %v", cResp.Diagnostics)
	}

	var stateModel PaperclipSecretResourceModel
	_ = cResp.State.Get(ctx, &stateModel)
	if stateModel.ID.ValueString() != "sec-created" || stateModel.Status.ValueString() != "disabled" {
		t.Fatalf("Create state incorrect: %#v", stateModel)
	}

	// Create initial status error
	failServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/api/companies/comp-1/secrets" {
			_ = json.NewEncoder(w).Encode(client.PaperclipSecret{ID: "sec-err", CompanyID: "comp-1", Name: "err-sec"})
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"failed to patch initial status"}`))
	}))
	defer failServer.Close()
	failCli, _ := client.NewClient(failServer.URL, "token")
	failSecResource := NewPaperclipSecretResource().(*PaperclipSecretResource)
	failSecResource.client = failCli
	failResp := resource.CreateResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	failSecResource.Create(ctx, resource.CreateRequest{Plan: createPlan, Config: createCfg}, &failResp)
	if !failResp.Diagnostics.HasError() {
		t.Fatal("expected error when initial status patch fails")
	}

	// Read
	readState := tfsdk.State{Schema: schemaResp.Schema}
	_ = readState.Set(ctx, &stateModel)
	var rResp resource.ReadResponse
	rResp.State = tfsdk.State{Schema: schemaResp.Schema}
	secretResource.Read(ctx, resource.ReadRequest{State: readState}, &rResp)
	if rResp.Diagnostics.HasError() {
		t.Fatalf("Read failed: %v", rResp.Diagnostics)
	}

	// Read not found
	notFoundStateModel := stateModel
	notFoundStateModel.ID = types.StringValue("not-found")
	_ = readState.Set(ctx, &notFoundStateModel)
	rResp = resource.ReadResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	secretResource.Read(ctx, resource.ReadRequest{State: readState}, &rResp)
	if !rResp.State.Raw.IsNull() {
		t.Fatal("expected resource removal on 404")
	}

	// Update metadata and rotate
	updatePlanModel := stateModel
	updatePlanModel.Name = types.StringValue("ext-secret-renamed")
	updatePlanModel.Description = types.StringNull()
	updatePlanModel.Status = types.StringValue("active")
	updatePlanModel.ProviderConfigID = types.StringValue("spc-vault")
	updatePlanModel.ExternalRef = types.StringValue("vault/secret/path2")
	updatePlanModel.ProviderMetadata = types.StringNull()
	updatePlanModel.Rotation = types.StringValue("rev-2")
	updateCfgModel := updatePlanModel
	updateCfgModel.Value = types.StringValue("rotated-val")
	updatePlanModel.Value = types.StringNull()
	updatePlanModel.LatestVersion = types.Int64Unknown()
	updatePlanModel.LastRotatedAt = types.StringUnknown()

	updatePlan := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = updatePlan.Set(ctx, &updatePlanModel)
	updateCfgPlan := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = updateCfgPlan.Set(ctx, &updateCfgModel)
	updateCfg := tfsdk.Config{Schema: schemaResp.Schema, Raw: updateCfgPlan.Raw}

	var uResp resource.UpdateResponse
	uResp.State = tfsdk.State{Schema: schemaResp.Schema}
	secretResource.Update(ctx, resource.UpdateRequest{
		Plan:   updatePlan,
		Config: updateCfg,
		State:  cResp.State,
	}, &uResp)
	if uResp.Diagnostics.HasError() {
		t.Fatalf("Update failed: %v", uResp.Diagnostics)
	}
	var updatedState PaperclipSecretResourceModel
	_ = uResp.State.Get(ctx, &updatedState)
	if updatedState.Name.ValueString() != "ext-secret-renamed" || updatedState.LatestVersion.ValueInt64() != 1 {
		t.Fatalf("Updated state incorrect: %#v", updatedState)
	}

	// Update with null Name and non-empty metadata
	updateNullModel := updatedState
	updateNullModel.Name = types.StringNull()
	updateNullModel.Description = types.StringValue("new-desc")
	updateNullModel.ProviderMetadata = types.StringValue(`{"engine":"kv-v2","path":"updated"}`)
	updateNullPlan := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = updateNullPlan.Set(ctx, &updateNullModel)
	updateNullCfgPlan := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = updateNullCfgPlan.Set(ctx, &updateNullModel)
	updateNullCfg := tfsdk.Config{Schema: schemaResp.Schema, Raw: updateNullCfgPlan.Raw}
	var uRespNull resource.UpdateResponse
	uRespNull.State = tfsdk.State{Schema: schemaResp.Schema}
	secretResource.Update(ctx, resource.UpdateRequest{
		Plan:   updateNullPlan,
		Config: updateNullCfg,
		State:  uResp.State,
	}, &uRespNull)
	if uRespNull.Diagnostics.HasError() {
		t.Fatalf("Update with null name failed: %v", uRespNull.Diagnostics)
	}

	// Update with null provider_metadata (clearing metadata)
	updateClearMetaModel := updateNullModel
	updateClearMetaModel.ProviderMetadata = types.StringNull()
	updateClearMetaPlan := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = updateClearMetaPlan.Set(ctx, &updateClearMetaModel)
	updateClearMetaCfgPlan := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = updateClearMetaCfgPlan.Set(ctx, &updateClearMetaModel)
	updateClearMetaCfg := tfsdk.Config{Schema: schemaResp.Schema, Raw: updateClearMetaCfgPlan.Raw}
	var uRespClearMeta resource.UpdateResponse
	uRespClearMeta.State = tfsdk.State{Schema: schemaResp.Schema}
	secretResource.Update(ctx, resource.UpdateRequest{
		Plan:   updateClearMetaPlan,
		Config: updateClearMetaCfg,
		State:  uRespNull.State,
	}, &uRespClearMeta)
	if uRespClearMeta.Diagnostics.HasError() {
		t.Fatalf("Update with null provider_metadata failed: %v", uRespClearMeta.Diagnostics)
	}

	// Update metadata error
	updateErrMetaModel := updateNullModel
	updateErrMetaModel.Description = types.StringValue("fail-desc")
	updateErrMetaPlan := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = updateErrMetaPlan.Set(ctx, &updateErrMetaModel)
	updateErrMetaCfgPlan := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = updateErrMetaCfgPlan.Set(ctx, &updateErrMetaModel)
	updateErrMetaCfg := tfsdk.Config{Schema: schemaResp.Schema, Raw: updateErrMetaCfgPlan.Raw}
	errMetaState := updateNullModel
	errMetaState.ID = types.StringValue("sec-error")
	errMetaStateVal := tfsdk.State{Schema: schemaResp.Schema}
	_ = errMetaStateVal.Set(ctx, &errMetaState)
	var uRespErrMeta resource.UpdateResponse
	uRespErrMeta.State = tfsdk.State{Schema: schemaResp.Schema}
	secretResource.Update(ctx, resource.UpdateRequest{
		Plan:   updateErrMetaPlan,
		Config: updateErrMetaCfg,
		State:  errMetaStateVal,
	}, &uRespErrMeta)
	if !uRespErrMeta.Diagnostics.HasError() {
		t.Fatal("expected Update metadata error on 500")
	}

	// Rotate error
	updateErrRotateModel := updateNullModel
	updateErrRotateModel.Value = types.StringNull()
	updateErrRotateModel.Rotation = types.StringValue("rev-rotate-err")
	updateErrRotatePlan := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = updateErrRotatePlan.Set(ctx, &updateErrRotateModel)
	updateErrRotateCfgModel := updateErrRotateModel
	updateErrRotateCfgModel.Value = types.StringValue("val-rotate-err")
	updateErrRotateCfgPlan := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = updateErrRotateCfgPlan.Set(ctx, &updateErrRotateCfgModel)
	updateErrRotateCfg := tfsdk.Config{Schema: schemaResp.Schema, Raw: updateErrRotateCfgPlan.Raw}
	errRotateState := updateNullModel
	errRotateState.ID = types.StringValue("sec-error")
	errRotateStateVal := tfsdk.State{Schema: schemaResp.Schema}
	_ = errRotateStateVal.Set(ctx, &errRotateState)
	var uRespErrRotate resource.UpdateResponse
	uRespErrRotate.State = tfsdk.State{Schema: schemaResp.Schema}
	secretResource.Update(ctx, resource.UpdateRequest{
		Plan:   updateErrRotatePlan,
		Config: updateErrRotateCfg,
		State:  errRotateStateVal,
	}, &uRespErrRotate)
	if !uRespErrRotate.Diagnostics.HasError() {
		t.Fatal("expected Rotate error on 500")
	}

	// Delete
	delState := tfsdk.State{Schema: schemaResp.Schema}
	_ = delState.Set(ctx, &updatedState)
	var dResp resource.DeleteResponse
	secretResource.Delete(ctx, resource.DeleteRequest{State: delState}, &dResp)
	if dResp.Diagnostics.HasError() {
		t.Fatalf("Delete failed: %v", dResp.Diagnostics)
	}

	// Delete not found ignored
	_ = delState.Set(ctx, &notFoundStateModel)
	dResp = resource.DeleteResponse{}
	secretResource.Delete(ctx, resource.DeleteRequest{State: delState}, &dResp)
	if dResp.Diagnostics.HasError() {
		t.Fatalf("Delete 404 should be ignored: %v", dResp.Diagnostics)
	}

	// ImportState
	var impResp resource.ImportStateResponse
	impResp.State = tfsdk.State{Schema: schemaResp.Schema}
	_ = impResp.State.Set(ctx, &PaperclipSecretResourceModel{})
	secretResource.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/sec-created"}, &impResp)
	if impResp.Diagnostics.HasError() {
		t.Fatalf("ImportState failed: %v", impResp.Diagnostics)
	}
	var imported PaperclipSecretResourceModel
	_ = impResp.State.Get(ctx, &imported)
	if imported.CompanyID.ValueString() != "comp-1" || imported.ID.ValueString() != "sec-created" {
		t.Fatalf("ImportState mapped incorrectly: %#v", imported)
	}

	impResp = resource.ImportStateResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	secretResource.ImportState(ctx, resource.ImportStateRequest{ID: "invalid-import-id"}, &impResp)
	if !impResp.Diagnostics.HasError() {
		t.Fatal("invalid import ID should produce error")
	}
}

func TestPaperclipSecretProviderConfigResourceLifecycle(t *testing.T) {
	ctx := context.Background()
	spcResource := NewPaperclipSecretProviderConfigResource().(*PaperclipSecretProviderConfigResource)
	var schemaResp resource.SchemaResponse
	spcResource.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	// Configure
	var confResp resource.ConfigureResponse
	spcResource.Configure(ctx, resource.ConfigureRequest{ProviderData: nil}, &confResp)
	spcResource.Configure(ctx, resource.ConfigureRequest{ProviderData: 123}, &confResp)
	if !confResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid provider data")
	}

	// ValidateConfig
	valConfig := func(m PaperclipSecretProviderConfigResourceModel) resource.ValidateConfigResponse {
		var resp resource.ValidateConfigResponse
		plan := tfsdk.Plan{Schema: schemaResp.Schema}
		_ = plan.Set(ctx, &m)
		cfg := tfsdk.Config{Schema: schemaResp.Schema, Raw: plan.Raw}
		spcResource.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: cfg}, &resp)
		return resp
	}

	if r := valConfig(PaperclipSecretProviderConfigResourceModel{
		ProviderType: types.StringValue("invalid_vault"),
		Status:       types.StringValue("invalid_status"),
		DisplayName:  types.StringValue(""),
	}); len(r.Diagnostics.Errors()) != 3 {
		t.Fatalf("expected 3 errors in ValidateConfig, got %d: %v", len(r.Diagnostics.Errors()), r.Diagnostics)
	}

	// ValidateConfig bad config decode
	badValReq := resource.ValidateConfigRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: tftypes.NewValue(tftypes.String, "bad")}}
	var badValResp resource.ValidateConfigResponse
	spcResource.ValidateConfig(ctx, badValReq, &badValResp)
	if !badValResp.Diagnostics.HasError() {
		t.Fatal("expected error in ValidateConfig with bad Config")
	}

	// normalize default status
	mDef := PaperclipSecretProviderConfigResourceModel{Status: types.StringNull()}
	normalizePaperclipSecretProviderConfigState(&mDef, &client.PaperclipSecretProviderConfig{ID: "spc-2", Provider: "vault"})
	if mDef.Status.ValueString() != "ready" {
		t.Fatal("expected default ready status")
	}

	// Mock Server
	var storedCfg client.PaperclipSecretProviderConfig
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		p := r.URL.Path

		switch {
		case r.Method == http.MethodPost && p == "/api/companies/comp-err/secret-provider-configs":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"create error"}`))
		case r.Method == http.MethodPost && p == "/api/companies/comp-1/secret-provider-configs":
			_ = json.NewDecoder(r.Body).Decode(&storedCfg)
			storedCfg.ID = "spc-test-1"
			storedCfg.CompanyID = "comp-1"
			storedCfg.HealthStatus = "healthy"
			_ = json.NewEncoder(w).Encode(storedCfg)
		case r.Method == http.MethodGet && p == "/api/secret-provider-configs/spc-test-1":
			_ = json.NewEncoder(w).Encode(storedCfg)
		case r.Method == http.MethodGet && p == "/api/secret-provider-configs/not-found":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		case r.Method == http.MethodGet && p == "/api/secret-provider-configs/err-500":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"read error"}`))
		case r.Method == http.MethodPatch && p == "/api/secret-provider-configs/spc-test-1":
			var update client.PaperclipSecretProviderConfig
			_ = json.NewDecoder(r.Body).Decode(&update)
			storedCfg.DisplayName = update.DisplayName
			storedCfg.Status = update.Status
			storedCfg.IsDefault = update.IsDefault
			storedCfg.Config = update.Config
			_ = json.NewEncoder(w).Encode(storedCfg)
		case r.Method == http.MethodPatch && p == "/api/secret-provider-configs/err-500":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"update error"}`))
		case r.Method == http.MethodDelete && p == "/api/secret-provider-configs/spc-test-1":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodDelete && p == "/api/secret-provider-configs/not-found":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		case r.Method == http.MethodDelete && p == "/api/secret-provider-configs/err-500":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"delete error"}`))
		default:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"internal error"}`))
		}
	}))
	defer server.Close()

	cli, _ := client.NewClient(server.URL, "token")
	confResp = resource.ConfigureResponse{}
	spcResource.Configure(ctx, resource.ConfigureRequest{ProviderData: cli}, &confResp)
	if confResp.Diagnostics.HasError() {
		t.Fatalf("unexpected configure error: %v", confResp.Diagnostics)
	}

	// Create
	planModel := PaperclipSecretProviderConfigResourceModel{
		CompanyID:    types.StringValue("comp-1"),
		ProviderType: types.StringValue("vault"),
		DisplayName:  types.StringValue("Primary Vault"),
		Status:       types.StringValue("ready"),
		IsDefault:    types.BoolValue(true),
		Config:       types.StringValue(`{"address":"https://vault.example.com"}`),
	}
	plan := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = plan.Set(ctx, &planModel)
	var cResp resource.CreateResponse
	cResp.State = tfsdk.State{Schema: schemaResp.Schema}
	spcResource.Create(ctx, resource.CreateRequest{Plan: plan}, &cResp)
	if cResp.Diagnostics.HasError() {
		t.Fatalf("Create failed: %v", cResp.Diagnostics)
	}

	// Create bad plan error
	badCResp := resource.CreateResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	spcResource.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Schema: schemaResp.Schema, Raw: tftypes.NewValue(tftypes.String, "bad")}}, &badCResp)
	if !badCResp.Diagnostics.HasError() {
		t.Fatal("expected error with bad plan on Create")
	}

	// Create server error
	errPlanModel := planModel
	errPlanModel.CompanyID = types.StringValue("comp-err")
	errPlan := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = errPlan.Set(ctx, &errPlanModel)
	var errCResp resource.CreateResponse
	errCResp.State = tfsdk.State{Schema: schemaResp.Schema}
	spcResource.Create(ctx, resource.CreateRequest{Plan: errPlan}, &errCResp)
	if !errCResp.Diagnostics.HasError() {
		t.Fatal("expected server error on Create")
	}

	var stateModel PaperclipSecretProviderConfigResourceModel
	_ = cResp.State.Get(ctx, &stateModel)
	if stateModel.ID.ValueString() != "spc-test-1" || stateModel.HealthStatus.ValueString() != "healthy" {
		t.Fatalf("unexpected state after create: %#v", stateModel)
	}

	// Read
	rState := tfsdk.State{Schema: schemaResp.Schema}
	_ = rState.Set(ctx, &stateModel)
	var rResp resource.ReadResponse
	rResp.State = tfsdk.State{Schema: schemaResp.Schema}
	spcResource.Read(ctx, resource.ReadRequest{State: rState}, &rResp)
	if rResp.Diagnostics.HasError() {
		t.Fatalf("Read failed: %v", rResp.Diagnostics)
	}

	// Read bad state error
	badRResp := resource.ReadResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	spcResource.Read(ctx, resource.ReadRequest{State: tfsdk.State{Schema: schemaResp.Schema, Raw: tftypes.NewValue(tftypes.String, "bad")}}, &badRResp)
	if !badRResp.Diagnostics.HasError() {
		t.Fatal("expected error with bad state on Read")
	}

	// Read not found
	notFoundModel := stateModel
	notFoundModel.ID = types.StringValue("not-found")
	_ = rState.Set(ctx, &notFoundModel)
	rResp = resource.ReadResponse{State: rState}
	spcResource.Read(ctx, resource.ReadRequest{State: rState}, &rResp)
	if !rResp.State.Raw.IsNull() {
		t.Fatal("expected resource removal on 404")
	}

	// Read 500 error
	errStateModel := stateModel
	errStateModel.ID = types.StringValue("err-500")
	_ = rState.Set(ctx, &errStateModel)
	rResp = resource.ReadResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	spcResource.Read(ctx, resource.ReadRequest{State: rState}, &rResp)
	if !rResp.Diagnostics.HasError() {
		t.Fatal("expected error on Read 500")
	}

	// Update (with config null clearing)
	uPlanModel := stateModel
	uPlanModel.DisplayName = types.StringValue("Renamed Vault")
	uPlanModel.Status = types.StringValue("warning")
	uPlanModel.IsDefault = types.BoolValue(false)
	uPlanModel.Config = types.StringNull()
	uPlan := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = uPlan.Set(ctx, &uPlanModel)
	var uResp resource.UpdateResponse
	uResp.State = tfsdk.State{Schema: schemaResp.Schema}
	spcResource.Update(ctx, resource.UpdateRequest{
		Plan:  uPlan,
		State: cResp.State,
	}, &uResp)
	if uResp.Diagnostics.HasError() {
		t.Fatalf("Update failed: %v", uResp.Diagnostics)
	}

	// Update with non-null JSON config
	uPlanModelWithCfg := stateModel
	uPlanModelWithCfg.Config = types.StringValue(`{"address":"https://vault-updated.example.com"}`)
	uPlanWithCfg := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = uPlanWithCfg.Set(ctx, &uPlanModelWithCfg)
	var uRespCfg resource.UpdateResponse
	uRespCfg.State = tfsdk.State{Schema: schemaResp.Schema}
	spcResource.Update(ctx, resource.UpdateRequest{
		Plan:  uPlanWithCfg,
		State: cResp.State,
	}, &uRespCfg)
	if uRespCfg.Diagnostics.HasError() {
		t.Fatalf("Update with config failed: %v", uRespCfg.Diagnostics)
	}

	// Update bad plan error
	badUResp := resource.UpdateResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	spcResource.Update(ctx, resource.UpdateRequest{
		Plan:  tfsdk.Plan{Schema: schemaResp.Schema, Raw: tftypes.NewValue(tftypes.String, "bad")},
		State: cResp.State,
	}, &badUResp)
	if !badUResp.Diagnostics.HasError() {
		t.Fatal("expected error with bad plan on Update")
	}

	// Update server error
	errStateModel = stateModel
	errStateModel.ID = types.StringValue("err-500")
	errState := tfsdk.State{Schema: schemaResp.Schema}
	_ = errState.Set(ctx, &errStateModel)
	var errUResp resource.UpdateResponse
	errUResp.State = tfsdk.State{Schema: schemaResp.Schema}
	spcResource.Update(ctx, resource.UpdateRequest{
		Plan:  uPlan,
		State: errState,
	}, &errUResp)
	if !errUResp.Diagnostics.HasError() {
		t.Fatal("expected server error on Update")
	}

	// Delete
	delState := tfsdk.State{Schema: schemaResp.Schema}
	_ = delState.Set(ctx, &uPlanModel)
	var dResp resource.DeleteResponse
	spcResource.Delete(ctx, resource.DeleteRequest{State: delState}, &dResp)
	if dResp.Diagnostics.HasError() {
		t.Fatalf("Delete failed: %v", dResp.Diagnostics)
	}

	// Delete bad state error
	badDResp := resource.DeleteResponse{}
	spcResource.Delete(ctx, resource.DeleteRequest{State: tfsdk.State{Schema: schemaResp.Schema, Raw: tftypes.NewValue(tftypes.String, "bad")}}, &badDResp)
	if !badDResp.Diagnostics.HasError() {
		t.Fatal("expected error with bad state on Delete")
	}

	// Delete 404 ignored
	_ = delState.Set(ctx, &notFoundModel)
	dResp = resource.DeleteResponse{}
	spcResource.Delete(ctx, resource.DeleteRequest{State: delState}, &dResp)
	if dResp.Diagnostics.HasError() {
		t.Fatalf("Delete 404 should be ignored: %v", dResp.Diagnostics)
	}

	// Delete 500 error
	_ = delState.Set(ctx, &errStateModel)
	dResp = resource.DeleteResponse{}
	spcResource.Delete(ctx, resource.DeleteRequest{State: delState}, &dResp)
	if !dResp.Diagnostics.HasError() {
		t.Fatal("expected error on Delete 500")
	}

	// ImportState
	var impResp resource.ImportStateResponse
	impResp.State = tfsdk.State{Schema: schemaResp.Schema}
	_ = impResp.State.Set(ctx, &PaperclipSecretProviderConfigResourceModel{})
	spcResource.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/spc-test-1"}, &impResp)
	if impResp.Diagnostics.HasError() {
		t.Fatalf("ImportState comp/id failed: %v", impResp.Diagnostics)
	}
	impResp = resource.ImportStateResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	_ = impResp.State.Set(ctx, &PaperclipSecretProviderConfigResourceModel{})
	spcResource.ImportState(ctx, resource.ImportStateRequest{ID: "spc-test-1"}, &impResp)
	if impResp.Diagnostics.HasError() {
		t.Fatalf("ImportState id-only failed: %v", impResp.Diagnostics)
	}
	impResp = resource.ImportStateResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	spcResource.ImportState(ctx, resource.ImportStateRequest{ID: ""}, &impResp)
	if !impResp.Diagnostics.HasError() {
		t.Fatal("empty import ID should error")
	}
}

func TestPaperclipUserSecretDefinitionResourceLifecycle(t *testing.T) {
	ctx := context.Background()
	usdResource := NewPaperclipUserSecretDefinitionResource().(*PaperclipUserSecretDefinitionResource)
	var schemaResp resource.SchemaResponse
	usdResource.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	// Configure
	var confResp resource.ConfigureResponse
	usdResource.Configure(ctx, resource.ConfigureRequest{ProviderData: nil}, &confResp)
	usdResource.Configure(ctx, resource.ConfigureRequest{ProviderData: 123}, &confResp)
	if !confResp.Diagnostics.HasError() {
		t.Fatal("expected error on invalid provider data")
	}

	// normalize defaults
	mDef := PaperclipUserSecretDefinitionResourceModel{
		Status:       types.StringNull(),
		ProviderType: types.StringNull(),
		ManagedMode:  types.StringNull(),
	}
	normalizePaperclipUserSecretDefinitionState(&mDef, &client.PaperclipUserSecretDefinition{ID: "usd-empty"})
	if mDef.Status.ValueString() != "active" || mDef.ProviderType.ValueString() != "local_encrypted" || mDef.ManagedMode.ValueString() != "paperclip_managed" {
		t.Fatalf("unexpected defaults: %#v", mDef)
	}

	// ValidateConfig
	valConfig := func(m PaperclipUserSecretDefinitionResourceModel) resource.ValidateConfigResponse {
		var resp resource.ValidateConfigResponse
		plan := tfsdk.Plan{Schema: schemaResp.Schema}
		_ = plan.Set(ctx, &m)
		cfg := tfsdk.Config{Schema: schemaResp.Schema, Raw: plan.Raw}
		usdResource.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: cfg}, &resp)
		return resp
	}

	if r := valConfig(PaperclipUserSecretDefinitionResourceModel{
		Key:          types.StringValue("invalid key!"),
		Status:       types.StringValue("invalid_status"),
		ProviderType: types.StringValue("invalid_provider"),
		ManagedMode:  types.StringValue("invalid_mode"),
	}); len(r.Diagnostics.Errors()) != 4 {
		t.Fatalf("expected 4 errors in ValidateConfig, got %d: %v", len(r.Diagnostics.Errors()), r.Diagnostics)
	}

	// ValidateConfig bad config decode
	badValReq := resource.ValidateConfigRequest{Config: tfsdk.Config{Schema: schemaResp.Schema, Raw: tftypes.NewValue(tftypes.String, "bad")}}
	var badValResp resource.ValidateConfigResponse
	usdResource.ValidateConfig(ctx, badValReq, &badValResp)
	if !badValResp.Diagnostics.HasError() {
		t.Fatal("expected error in ValidateConfig with bad Config")
	}

	// Mock Server
	var storedDef client.PaperclipUserSecretDefinition
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		p := r.URL.Path

		switch {
		case r.Method == http.MethodPost && p == "/api/companies/comp-err/user-secret-definitions":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"create error"}`))
		case r.Method == http.MethodPost && p == "/api/companies/comp-1/user-secret-definitions":
			_ = json.NewDecoder(r.Body).Decode(&storedDef)
			storedDef.ID = "usd-test-1"
			storedDef.CompanyID = "comp-1"
			_ = json.NewEncoder(w).Encode(storedDef)
		case r.Method == http.MethodGet && p == "/api/companies/comp-1/user-secret-definitions":
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"userSecretDefinitions": []client.PaperclipUserSecretDefinition{storedDef},
			})
		case r.Method == http.MethodGet && p == "/api/companies/comp-err/user-secret-definitions":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"list error"}`))
		case r.Method == http.MethodPatch && p == "/api/companies/comp-1/user-secret-definitions/usd-test-1":
			var update client.PaperclipUserSecretDefinition
			_ = json.NewDecoder(r.Body).Decode(&update)
			storedDef.Name = update.Name
			storedDef.Description = update.Description
			storedDef.Status = update.Status
			storedDef.ProviderConfigID = update.ProviderConfigID
			storedDef.ProviderMetadata = update.ProviderMetadata
			storedDef.UsageGuidance = update.UsageGuidance
			_ = json.NewEncoder(w).Encode(storedDef)
		case r.Method == http.MethodPatch && p == "/api/companies/comp-1/user-secret-definitions/err-500":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"update error"}`))
		case r.Method == http.MethodDelete && p == "/api/companies/comp-1/user-secret-definitions/usd-test-1":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		case r.Method == http.MethodDelete && p == "/api/companies/comp-1/user-secret-definitions/not-found":
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not found"}`))
		case r.Method == http.MethodDelete && p == "/api/companies/comp-1/user-secret-definitions/err-500":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"delete error"}`))
		default:
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"internal error"}`))
		}
	}))
	defer server.Close()

	cli, _ := client.NewClient(server.URL, "token")
	confResp = resource.ConfigureResponse{}
	usdResource.Configure(ctx, resource.ConfigureRequest{ProviderData: cli}, &confResp)
	if confResp.Diagnostics.HasError() {
		t.Fatalf("unexpected configure error: %v", confResp.Diagnostics)
	}

	// Create
	planModel := PaperclipUserSecretDefinitionResourceModel{
		CompanyID:        types.StringValue("comp-1"),
		Key:              types.StringValue("openai_api_key"),
		Name:             types.StringValue("OpenAI API Key"),
		Description:      types.StringValue("Key for OpenAI models"),
		Status:           types.StringValue("active"),
		ProviderType:     types.StringValue("local_encrypted"),
		ProviderConfigID: types.StringNull(),
		ManagedMode:      types.StringValue("paperclip_managed"),
		ProviderMetadata: types.StringValue(`{"scope":"global"}`),
		UsageGuidance:    types.StringValue("Obtain from platform.openai.com"),
	}
	plan := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = plan.Set(ctx, &planModel)
	var cResp resource.CreateResponse
	cResp.State = tfsdk.State{Schema: schemaResp.Schema}
	usdResource.Create(ctx, resource.CreateRequest{Plan: plan}, &cResp)
	if cResp.Diagnostics.HasError() {
		t.Fatalf("Create failed: %v", cResp.Diagnostics)
	}

	// Create bad plan error
	badCResp := resource.CreateResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	usdResource.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan{Schema: schemaResp.Schema, Raw: tftypes.NewValue(tftypes.String, "bad")}}, &badCResp)
	if !badCResp.Diagnostics.HasError() {
		t.Fatal("expected error with bad plan on Create")
	}

	// Create server error
	errPlanModel := planModel
	errPlanModel.CompanyID = types.StringValue("comp-err")
	errPlan := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = errPlan.Set(ctx, &errPlanModel)
	var errCResp resource.CreateResponse
	errCResp.State = tfsdk.State{Schema: schemaResp.Schema}
	usdResource.Create(ctx, resource.CreateRequest{Plan: errPlan}, &errCResp)
	if !errCResp.Diagnostics.HasError() {
		t.Fatal("expected server error on Create")
	}

	var stateModel PaperclipUserSecretDefinitionResourceModel
	_ = cResp.State.Get(ctx, &stateModel)
	if stateModel.ID.ValueString() != "usd-test-1" || stateModel.Key.ValueString() != "openai_api_key" {
		t.Fatalf("unexpected state after create: %#v", stateModel)
	}

	// Read
	rState := tfsdk.State{Schema: schemaResp.Schema}
	_ = rState.Set(ctx, &stateModel)
	var rResp resource.ReadResponse
	rResp.State = tfsdk.State{Schema: schemaResp.Schema}
	usdResource.Read(ctx, resource.ReadRequest{State: rState}, &rResp)
	if rResp.Diagnostics.HasError() {
		t.Fatalf("Read failed: %v", rResp.Diagnostics)
	}

	// Read bad state error
	badRResp := resource.ReadResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	usdResource.Read(ctx, resource.ReadRequest{State: tfsdk.State{Schema: schemaResp.Schema, Raw: tftypes.NewValue(tftypes.String, "bad")}}, &badRResp)
	if !badRResp.Diagnostics.HasError() {
		t.Fatal("expected error with bad state on Read")
	}

	// Read not found
	notFoundModel := stateModel
	notFoundModel.ID = types.StringValue("not-found")
	_ = rState.Set(ctx, &notFoundModel)
	rResp = resource.ReadResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	usdResource.Read(ctx, resource.ReadRequest{State: rState}, &rResp)
	if !rResp.State.Raw.IsNull() {
		t.Fatal("expected resource removal on 404")
	}

	// Read 500 error
	errStateModel := stateModel
	errStateModel.CompanyID = types.StringValue("comp-err")
	_ = rState.Set(ctx, &errStateModel)
	rResp = resource.ReadResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	usdResource.Read(ctx, resource.ReadRequest{State: rState}, &rResp)
	if !rResp.Diagnostics.HasError() {
		t.Fatal("expected error on Read 500")
	}

	// Update (with provider_metadata null clearing)
	uPlanModel := stateModel
	uPlanModel.Name = types.StringValue("Renamed OpenAI Key")
	uPlanModel.Description = types.StringValue("New description")
	uPlanModel.Status = types.StringValue("disabled")
	uPlanModel.ProviderConfigID = types.StringValue("spc-1")
	uPlanModel.ProviderMetadata = types.StringNull()
	uPlanModel.UsageGuidance = types.StringValue("New guidance")
	uPlan := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = uPlan.Set(ctx, &uPlanModel)
	var uResp resource.UpdateResponse
	uResp.State = tfsdk.State{Schema: schemaResp.Schema}
	usdResource.Update(ctx, resource.UpdateRequest{
		Plan:  uPlan,
		State: cResp.State,
	}, &uResp)
	if uResp.Diagnostics.HasError() {
		t.Fatalf("Update failed: %v", uResp.Diagnostics)
	}

	// Update with non-null JSON provider_metadata
	uPlanModelWithMeta := stateModel
	uPlanModelWithMeta.ProviderMetadata = types.StringValue(`{"scope":"regional"}`)
	uPlanWithMeta := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = uPlanWithMeta.Set(ctx, &uPlanModelWithMeta)
	var uRespMeta resource.UpdateResponse
	uRespMeta.State = tfsdk.State{Schema: schemaResp.Schema}
	usdResource.Update(ctx, resource.UpdateRequest{
		Plan:  uPlanWithMeta,
		State: cResp.State,
	}, &uRespMeta)
	if uRespMeta.Diagnostics.HasError() {
		t.Fatalf("Update with metadata failed: %v", uRespMeta.Diagnostics)
	}

	// Update bad plan error
	badUResp := resource.UpdateResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	usdResource.Update(ctx, resource.UpdateRequest{
		Plan:  tfsdk.Plan{Schema: schemaResp.Schema, Raw: tftypes.NewValue(tftypes.String, "bad")},
		State: cResp.State,
	}, &badUResp)
	if !badUResp.Diagnostics.HasError() {
		t.Fatal("expected error with bad plan on Update")
	}

	// Update server error
	errUpdateStateModel := stateModel
	errUpdateStateModel.ID = types.StringValue("err-500")
	errState := tfsdk.State{Schema: schemaResp.Schema}
	_ = errState.Set(ctx, &errUpdateStateModel)
	var errUResp resource.UpdateResponse
	errUResp.State = tfsdk.State{Schema: schemaResp.Schema}
	usdResource.Update(ctx, resource.UpdateRequest{
		Plan:  uPlan,
		State: errState,
	}, &errUResp)
	if !errUResp.Diagnostics.HasError() {
		t.Fatal("expected server error on Update")
	}

	// Delete
	delState := tfsdk.State{Schema: schemaResp.Schema}
	_ = delState.Set(ctx, &uPlanModel)
	var dResp resource.DeleteResponse
	usdResource.Delete(ctx, resource.DeleteRequest{State: delState}, &dResp)
	if dResp.Diagnostics.HasError() {
		t.Fatalf("Delete failed: %v", dResp.Diagnostics)
	}

	// Delete bad state error
	badDResp := resource.DeleteResponse{}
	usdResource.Delete(ctx, resource.DeleteRequest{State: tfsdk.State{Schema: schemaResp.Schema, Raw: tftypes.NewValue(tftypes.String, "bad")}}, &badDResp)
	if !badDResp.Diagnostics.HasError() {
		t.Fatal("expected error with bad state on Delete")
	}

	// Delete 404 ignored
	_ = delState.Set(ctx, &notFoundModel)
	dResp = resource.DeleteResponse{}
	usdResource.Delete(ctx, resource.DeleteRequest{State: delState}, &dResp)
	if dResp.Diagnostics.HasError() {
		t.Fatalf("Delete 404 should be ignored: %v", dResp.Diagnostics)
	}

	// Delete 500 error
	_ = delState.Set(ctx, &errUpdateStateModel)
	dResp = resource.DeleteResponse{}
	usdResource.Delete(ctx, resource.DeleteRequest{State: delState}, &dResp)
	if !dResp.Diagnostics.HasError() {
		t.Fatal("expected error on Delete 500")
	}

	// ImportState
	var impResp resource.ImportStateResponse
	impResp.State = tfsdk.State{Schema: schemaResp.Schema}
	_ = impResp.State.Set(ctx, &PaperclipUserSecretDefinitionResourceModel{})
	usdResource.ImportState(ctx, resource.ImportStateRequest{ID: "comp-1/usd-test-1"}, &impResp)
	if impResp.Diagnostics.HasError() {
		t.Fatalf("ImportState comp/id failed: %v", impResp.Diagnostics)
	}
	impResp = resource.ImportStateResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	usdResource.ImportState(ctx, resource.ImportStateRequest{ID: "invalid-import"}, &impResp)
	if !impResp.Diagnostics.HasError() {
		t.Fatal("invalid import ID should error")
	}
}

func TestPaperclipAgentInstructionsReadError(t *testing.T) {
	ctx := context.Background()
	agentResource := NewPaperclipAgentResource().(*PaperclipAgentResource)
	var schemaResp resource.SchemaResponse
	agentResource.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/agents/agent-1" {
			_ = json.NewEncoder(w).Encode(client.PaperclipAgent{
				ID:        "agent-1",
				CompanyID: "comp-1",
				Name:      "test-agent",
			})
			return
		}
		if r.URL.Path == "/api/agents/agent-1/instructions-bundle/file" {
			// Return 500 internal server error
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"internal file failure"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	cli, _ := client.NewClient(server.URL, "token")
	agentResource.client = cli

	stateModel := PaperclipAgentResourceModel{
		ID:        types.StringValue("agent-1"),
		CompanyID: types.StringValue("comp-1"),
		Name:      types.StringValue("test-agent"),
	}
	rState := tfsdk.State{Schema: schemaResp.Schema}
	_ = rState.Set(ctx, &stateModel)
	var rResp resource.ReadResponse
	rResp.State = tfsdk.State{Schema: schemaResp.Schema}
	agentResource.Read(ctx, resource.ReadRequest{State: rState}, &rResp)
	if !rResp.Diagnostics.HasError() {
		t.Fatal("expected error reading agent instructions on 500 response")
	}
}

func TestPaperclipAgentInstructionsEmpty(t *testing.T) {
	ctx := context.Background()
	agentResource := NewPaperclipAgentResource().(*PaperclipAgentResource)
	var schemaResp resource.SchemaResponse
	agentResource.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/agents/agent-1" {
			_ = json.NewEncoder(w).Encode(client.PaperclipAgent{
				ID:        "agent-1",
				CompanyID: "comp-1",
				Name:      "test-agent",
			})
			return
		}
		if r.URL.Path == "/api/agents/agent-1/instructions-bundle/file" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(""))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	cli, _ := client.NewClient(server.URL, "token")
	agentResource.client = cli

	stateModel := PaperclipAgentResourceModel{
		ID:           types.StringValue("agent-1"),
		CompanyID:    types.StringValue("comp-1"),
		Name:         types.StringValue("test-agent"),
		Instructions: types.StringValue("previous"),
	}
	rState := tfsdk.State{Schema: schemaResp.Schema}
	_ = rState.Set(ctx, &stateModel)
	var rResp resource.ReadResponse
	rResp.State = tfsdk.State{Schema: schemaResp.Schema}
	agentResource.Read(ctx, resource.ReadRequest{State: rState}, &rResp)
	if rResp.Diagnostics.HasError() {
		t.Fatalf("unexpected read error: %v", rResp.Diagnostics)
	}
	var resState PaperclipAgentResourceModel
	_ = rResp.State.Get(ctx, &resState)
	if !resState.Instructions.IsNull() {
		t.Fatalf("expected null instructions, got %v", resState.Instructions)
	}
}

func TestEnvBindingsApplyNon404Error(t *testing.T) {
	ctx := context.Background()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"server error"}`))
	}))
	defer server.Close()

	cli, _ := client.NewClient(server.URL, "token")

	bindings := map[string]EnvBindingModel{
		"SECRET_KEY": {
			Type:     types.StringValue("secret_ref"),
			SecretID: types.StringValue("sec-err"),
		},
	}
	diags := ValidateEnvBindingsApply(ctx, cli, "comp-1", bindings)
	if !diags.HasError() {
		t.Fatal("expected error on 500 server error")
	}
	found := false
	for _, d := range diags.Errors() {
		if strings.Contains(d.Summary(), "Secret Reference Verification Failed") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected 'Secret Reference Verification Failed', got %v", diags)
	}

	jsonDiags := ValidateEnvBindingsJSONApply(ctx, cli, "comp-1", `{"SECRET_KEY": {"type": "secret_ref", "secretId": "sec-err"}}`)
	if !jsonDiags.HasError() {
		t.Fatal("expected error on 500 server error for JSON apply")
	}
	foundJSON := false
	for _, d := range jsonDiags.Errors() {
		if strings.Contains(d.Summary(), "Secret Reference Verification Failed") {
			foundJSON = true
			break
		}
	}
	if !foundJSON {
		t.Fatalf("expected 'Secret Reference Verification Failed', got %v", jsonDiags)
	}
}

func TestRefreshAgentEnvBindings(t *testing.T) {
	if refreshAgentEnvBindings(nil, nil) != nil {
		t.Fatal("expected nil when currentEnv is nil")
	}

	currentEnv := map[string]EnvBindingModel{
		"PLAIN_STR": {Type: types.StringValue("plain")},
		"PLAIN_OBJ": {Type: types.StringValue("plain")},
		"SEC_REF":   {Type: types.StringValue("secret_ref")},
		"USER_SEC":  {Type: types.StringValue("user_secret_ref")},
		"OTHER":     {Type: types.StringValue("plain")},
		"DELETED":   {Type: types.StringValue("plain")},
	}

	resNil := refreshAgentEnvBindings(currentEnv, nil)
	if len(resNil) != 0 {
		t.Fatal("expected empty map when remoteAdapterConfig is nil")
	}

	resNoEnv := refreshAgentEnvBindings(currentEnv, map[string]interface{}{"other": 1})
	if len(resNoEnv) != 0 {
		t.Fatal("expected empty map when remote has no env")
	}

	remote := map[string]interface{}{
		"env": map[string]interface{}{
			"PLAIN_STR": "val1",
			"PLAIN_OBJ": map[string]interface{}{
				"type":  "plain",
				"value": "val2",
			},
			"SEC_REF": map[string]interface{}{
				"type":                   "secret_ref",
				"secretId":               "sec-123",
				"version":                "1",
				"projectionClass":        "class_3_static_lease",
				"projectionAllowlistKey": "slack.bot_token",
			},
			"USER_SEC": map[string]interface{}{
				"type":                 "user_secret_ref",
				"key":                  "user_key",
				"version":              "2",
				"required":             true,
				"allowMissingOverride": true,
			},
			"OTHER": 12345,
			"EXTRA": "ignored",
		},
	}

	res := refreshAgentEnvBindings(currentEnv, remote)
	if len(res) != 5 {
		t.Fatalf("expected 5 refreshed bindings, got %d", len(res))
	}
	if res["PLAIN_STR"].Value.ValueString() != "val1" {
		t.Fatalf("unexpected PLAIN_STR value: %v", res["PLAIN_STR"])
	}
	if res["PLAIN_OBJ"].Value.ValueString() != "val2" {
		t.Fatalf("unexpected PLAIN_OBJ value: %v", res["PLAIN_OBJ"])
	}
	if res["SEC_REF"].SecretID.ValueString() != "sec-123" || res["SEC_REF"].Version.ValueString() != "1" ||
		res["SEC_REF"].ProjectionClass.ValueString() != "class_3_static_lease" ||
		res["SEC_REF"].ProjectionAllowlistKey.ValueString() != "slack.bot_token" {
		t.Fatalf("unexpected SEC_REF: %v", res["SEC_REF"])
	}
	if res["USER_SEC"].Key.ValueString() != "user_key" || res["USER_SEC"].Version.ValueString() != "2" ||
		!res["USER_SEC"].Required.ValueBool() || !res["USER_SEC"].AllowMissingOverride.ValueBool() {
		t.Fatalf("unexpected USER_SEC: %v", res["USER_SEC"])
	}
	if _, ok := res["DELETED"]; ok {
		t.Fatal("expected DELETED to be omitted")
	}

	currentSingle := map[string]EnvBindingModel{
		"SEC_NO_VER":  {Type: types.StringValue("secret_ref")},
		"USER_NO_VER": {Type: types.StringValue("user_secret_ref")},
		"CUSTOM_TYP":  {Type: types.StringValue("plain")},
	}
	remoteSingle := map[string]interface{}{
		"env": map[string]interface{}{
			"SEC_NO_VER": map[string]interface{}{
				"type":     "secret_ref",
				"secretId": "sec-456",
			},
			"USER_NO_VER": map[string]interface{}{
				"type": "user_secret_ref",
				"key":  "usr-key-456",
			},
			"CUSTOM_TYP": map[string]interface{}{
				"type": "custom",
			},
		},
	}
	resSingle := refreshAgentEnvBindings(currentSingle, remoteSingle)
	if !resSingle["SEC_NO_VER"].Version.IsNull() {
		t.Fatalf("expected null version for SEC_NO_VER, got %v", resSingle["SEC_NO_VER"].Version)
	}
	if !resSingle["USER_NO_VER"].Version.IsNull() {
		t.Fatalf("expected null version for USER_NO_VER, got %v", resSingle["USER_NO_VER"].Version)
	}
}

func TestAgentResourceEnvValidateConfig(t *testing.T) {
	ctx := context.Background()
	agentResource := NewPaperclipAgentResource().(*PaperclipAgentResource)
	var schemaResp resource.SchemaResponse
	agentResource.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	validModel := PaperclipAgentResourceModel{
		Name:            types.StringValue("agent-valid"),
		CompanyID:       types.StringValue("comp-1"),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(true),
		Env: map[string]EnvBindingModel{
			"VAR1": {
				Type:  types.StringValue("plain"),
				Value: types.StringValue("hello"),
			},
			"VAR2": {
				Type:     types.StringValue("secret_ref"),
				SecretID: types.StringValue("sec-1"),
			},
			"VAR3": {
				Type: types.StringValue("user_secret_ref"),
				Key:  types.StringValue("user_k"),
			},
		},
		AdapterConfig: types.StringValue(`{"env": {"VAR4": "world"}}`),
	}
	var plan1 tfsdk.Plan
	plan1.Schema = schemaResp.Schema
	_ = plan1.Set(ctx, &validModel)
	config1 := tfsdk.Config{Schema: schemaResp.Schema, Raw: plan1.Raw}
	var resp1 resource.ValidateConfigResponse
	agentResource.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: config1}, &resp1)
	if resp1.Diagnostics.HasError() {
		t.Fatalf("unexpected validation errors: %v", resp1.Diagnostics)
	}

	invalidModel := PaperclipAgentResourceModel{
		Name:            types.StringValue("agent-invalid"),
		CompanyID:       types.StringValue("comp-1"),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(true),
		Env: map[string]EnvBindingModel{
			"BAD_VAR": {
				Type:  types.StringValue("plain"),
				Value: types.StringValue("[REDACTED]"),
			},
		},
		AdapterConfig: types.StringValue(`{"env": {"BAD_ADAPTER": "[REDACTED]"}}`),
	}
	var plan2 tfsdk.Plan
	plan2.Schema = schemaResp.Schema
	_ = plan2.Set(ctx, &invalidModel)
	config2 := tfsdk.Config{Schema: schemaResp.Schema, Raw: plan2.Raw}
	var resp2 resource.ValidateConfigResponse
	agentResource.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: config2}, &resp2)
	if !resp2.Diagnostics.HasError() {
		t.Fatal("expected validation errors for [REDACTED]")
	}

	badJSONModel := PaperclipAgentResourceModel{
		Name:            types.StringValue("agent-bad-json"),
		CompanyID:       types.StringValue("comp-1"),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(true),
		AdapterConfig:   types.StringValue(`not-json`),
	}
	var plan3 tfsdk.Plan
	plan3.Schema = schemaResp.Schema
	_ = plan3.Set(ctx, &badJSONModel)
	config3 := tfsdk.Config{Schema: schemaResp.Schema, Raw: plan3.Raw}
	var resp3 resource.ValidateConfigResponse
	agentResource.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: config3}, &resp3)
}

func TestAgentResourceEnvCreateUpdateReadLifecycle(t *testing.T) {
	ctx := context.Background()
	agentResource := NewPaperclipAgentResource().(*PaperclipAgentResource)
	var schemaResp resource.SchemaResponse
	agentResource.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	var lastCreatedAgent client.PaperclipAgent
	var lastUpdatedAgent client.PaperclipAgent
	companySecrets := []client.PaperclipSecret{
		{
			ID:        "sec-valid",
			CompanyID: "comp-1",
			Name:      "valid-secret",
		},
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/companies/comp-1" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"id":   "comp-1",
				"name": "comp-1",
			})
			return
		}
		if r.URL.Path == "/api/companies/comp-1/secrets" {
			_ = json.NewEncoder(w).Encode(companySecrets)
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/companies/comp-1/agents" {
			_ = json.NewDecoder(r.Body).Decode(&lastCreatedAgent)
			lastCreatedAgent.ID = "agent-created-1"
			lastCreatedAgent.CompanyID = "comp-1"
			lastCreatedAgent.Permissions = &client.PaperclipAgentPermissions{
				CanAssignTasks:  true,
				CanCreateAgents: true,
				CanCreateSkills: true,
			}
			_ = json.NewEncoder(w).Encode(lastCreatedAgent)
			return
		}
		if r.Method == http.MethodPatch && r.URL.Path == "/api/agents/agent-created-1" {
			_ = json.NewDecoder(r.Body).Decode(&lastUpdatedAgent)
			lastUpdatedAgent.ID = "agent-created-1"
			lastUpdatedAgent.CompanyID = "comp-1"
			lastUpdatedAgent.Permissions = &client.PaperclipAgentPermissions{
				CanAssignTasks:  true,
				CanCreateAgents: true,
				CanCreateSkills: true,
			}
			_ = json.NewEncoder(w).Encode(lastUpdatedAgent)
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/api/agents/agent-created-1" {
			agent := lastCreatedAgent
			if lastUpdatedAgent.ID != "" {
				agent = lastUpdatedAgent
			}
			if agent.ID == "" {
				agent.ID = "agent-created-1"
				agent.CompanyID = "comp-1"
				agent.Name = "agent-1"
			}
			if agent.Permissions == nil {
				agent.Permissions = &client.PaperclipAgentPermissions{
					CanAssignTasks:  true,
					CanCreateAgents: true,
					CanCreateSkills: true,
				}
			}
			_ = json.NewEncoder(w).Encode(agent)
			return
		}
		if r.URL.Path == "/api/agents/agent-created-1/permissions" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		if r.URL.Path == "/api/agents/agent-created-1/instructions-bundle/file" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(""))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	cli, _ := client.NewClient(server.URL, "token")
	agentResource.client = cli

	createPlanModel := PaperclipAgentResourceModel{
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("agent-1"),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(true),
		AdapterConfig:   types.StringValue(`{"env":{"PRE_EXISTING":{"type":"plain","value":"pre"}}}`),
		Env: map[string]EnvBindingModel{
			"PLAIN_VAR": {
				Type:  types.StringValue("plain"),
				Value: types.StringValue("val1"),
			},
			"SEC_VAR": {
				Type:     types.StringValue("secret_ref"),
				SecretID: types.StringValue("sec-valid"),
			},
		},
	}
	var plan1 tfsdk.Plan
	plan1.Schema = schemaResp.Schema
	_ = plan1.Set(ctx, &createPlanModel)
	var createResp resource.CreateResponse
	createResp.State = tfsdk.State{Schema: schemaResp.Schema}
	agentResource.Create(ctx, resource.CreateRequest{Plan: plan1}, &createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("unexpected Create error: %v", createResp.Diagnostics)
	}

	if lastCreatedAgent.AdapterConfig == nil || lastCreatedAgent.AdapterConfig["env"] == nil {
		t.Fatalf("expected adapterConfig.env to be populated on agent: %+v", lastCreatedAgent)
	}
	envMap := lastCreatedAgent.AdapterConfig["env"].(map[string]interface{})
	if _, ok := envMap["PRE_EXISTING"]; !ok {
		t.Fatalf("expected PRE_EXISTING in adapterConfig.env: %v", envMap)
	}
	if _, ok := envMap["PLAIN_VAR"]; !ok {
		t.Fatalf("expected PLAIN_VAR in adapterConfig.env: %v", envMap)
	}
	if _, ok := envMap["SEC_VAR"]; !ok {
		t.Fatalf("expected SEC_VAR in adapterConfig.env: %v", envMap)
	}

	var stateAfterCreate PaperclipAgentResourceModel
	_ = createResp.State.Get(ctx, &stateAfterCreate)
	if stateAfterCreate.Env == nil || len(stateAfterCreate.Env) != 2 {
		t.Fatalf("expected 2 bindings in state after Create, got %v", stateAfterCreate.Env)
	}

	companySecrets = nil
	badPlanModel := PaperclipAgentResourceModel{
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("agent-bad"),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(true),
		Env: map[string]EnvBindingModel{
			"SEC_VAR": {
				Type:     types.StringValue("secret_ref"),
				SecretID: types.StringValue("sec-missing"),
			},
		},
	}
	var badPlan tfsdk.Plan
	badPlan.Schema = schemaResp.Schema
	_ = badPlan.Set(ctx, &badPlanModel)
	var badCreateResp resource.CreateResponse
	badCreateResp.State = tfsdk.State{Schema: schemaResp.Schema}
	agentResource.Create(ctx, resource.CreateRequest{Plan: badPlan}, &badCreateResp)
	if !badCreateResp.Diagnostics.HasError() {
		t.Fatal("expected Create to fail with missing secret")
	}

	badJSONPlanModel := PaperclipAgentResourceModel{
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("agent-bad-json"),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(true),
		AdapterConfig:   types.StringValue(`{"env":{"SEC_VAR":{"type":"secret_ref","secretId":"sec-missing"}}}`),
	}
	var badJSONPlan tfsdk.Plan
	badJSONPlan.Schema = schemaResp.Schema
	_ = badJSONPlan.Set(ctx, &badJSONPlanModel)
	var badJSONCreateResp resource.CreateResponse
	badJSONCreateResp.State = tfsdk.State{Schema: schemaResp.Schema}
	agentResource.Create(ctx, resource.CreateRequest{Plan: badJSONPlan}, &badJSONCreateResp)
	if !badJSONCreateResp.Diagnostics.HasError() {
		t.Fatal("expected Create to fail with missing secret in adapter_config")
	}

	var readResp resource.ReadResponse
	readResp.State = tfsdk.State{Schema: schemaResp.Schema}
	agentResource.Read(ctx, resource.ReadRequest{State: createResp.State}, &readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("unexpected Read error: %v", readResp.Diagnostics)
	}
	var stateAfterRead PaperclipAgentResourceModel
	_ = readResp.State.Get(ctx, &stateAfterRead)
	if stateAfterRead.Env == nil || len(stateAfterRead.Env) != 2 {
		t.Fatalf("expected 2 bindings in state after Read, got %v", stateAfterRead.Env)
	}

	companySecrets = []client.PaperclipSecret{
		{
			ID:        "sec-valid",
			CompanyID: "comp-1",
			Name:      "valid-secret",
		},
	}
	updatePlanModel := stateAfterCreate
	updatePlanModel.Env = map[string]EnvBindingModel{
		"SEC_VAR": {
			Type:     types.StringValue("secret_ref"),
			SecretID: types.StringValue("sec-valid"),
		},
		"NEW_VAR": {
			Type:  types.StringValue("plain"),
			Value: types.StringValue("new-val"),
		},
	}
	var updatePlan tfsdk.Plan
	updatePlan.Schema = schemaResp.Schema
	_ = updatePlan.Set(ctx, &updatePlanModel)
	var updateResp resource.UpdateResponse
	updateResp.State = tfsdk.State{Schema: schemaResp.Schema}
	agentResource.Update(ctx, resource.UpdateRequest{Plan: updatePlan, State: createResp.State}, &updateResp)
	if updateResp.Diagnostics.HasError() {
		t.Fatalf("unexpected Update error: %v", updateResp.Diagnostics)
	}
	updatedEnvMap := lastUpdatedAgent.AdapterConfig["env"].(map[string]interface{})
	if _, ok := updatedEnvMap["PLAIN_VAR"]; ok {
		t.Fatalf("expected PLAIN_VAR to be pruned on update, got: %v", updatedEnvMap)
	}
	if _, ok := updatedEnvMap["NEW_VAR"]; !ok {
		t.Fatalf("expected NEW_VAR in adapterConfig.env on update: %v", updatedEnvMap)
	}

	clearPlanModel := updatePlanModel
	clearPlanModel.Env = map[string]EnvBindingModel{}
	var clearPlan tfsdk.Plan
	clearPlan.Schema = schemaResp.Schema
	_ = clearPlan.Set(ctx, &clearPlanModel)
	var clearResp resource.UpdateResponse
	clearResp.State = tfsdk.State{Schema: schemaResp.Schema}
	agentResource.Update(ctx, resource.UpdateRequest{Plan: clearPlan, State: updateResp.State}, &clearResp)
	if clearResp.Diagnostics.HasError() {
		t.Fatalf("unexpected Update clear error: %v", clearResp.Diagnostics)
	}

	// Update with fresh env when adapterConfig has no "env"
	noEnvAgent := lastCreatedAgent
	noEnvAgent.AdapterConfig = map[string]interface{}{"mode": "fast"}
	lastUpdatedAgent = noEnvAgent
	noEnvStateModel := updatePlanModel
	noEnvStateModel.AdapterConfig = types.StringValue(`{"mode":"fast"}`)
	noEnvStateModel.Env = map[string]EnvBindingModel{}
	var noEnvState tfsdk.State
	noEnvState = tfsdk.State{Schema: schemaResp.Schema}
	_ = noEnvState.Set(ctx, &noEnvStateModel)

	noEnvPlanModel := noEnvStateModel
	noEnvPlanModel.Env = map[string]EnvBindingModel{
		"FRESH_VAR": {
			Type:  types.StringValue("plain"),
			Value: types.StringValue("fresh"),
		},
	}
	var noEnvPlan tfsdk.Plan
	noEnvPlan = tfsdk.Plan{Schema: schemaResp.Schema}
	_ = noEnvPlan.Set(ctx, &noEnvPlanModel)
	var noEnvResp resource.UpdateResponse
	noEnvResp.State = tfsdk.State{Schema: schemaResp.Schema}
	agentResource.Update(ctx, resource.UpdateRequest{Plan: noEnvPlan, State: noEnvState}, &noEnvResp)
	if noEnvResp.Diagnostics.HasError() {
		t.Fatalf("unexpected Update error for fresh env: %v", noEnvResp.Diagnostics)
	}

	companySecrets = nil
	badUpdateModel := updatePlanModel
	badUpdateModel.Env = map[string]EnvBindingModel{
		"SEC_VAR": {
			Type:     types.StringValue("secret_ref"),
			SecretID: types.StringValue("sec-missing"),
		},
	}
	var badUpdatePlan tfsdk.Plan
	badUpdatePlan.Schema = schemaResp.Schema
	_ = badUpdatePlan.Set(ctx, &badUpdateModel)
	var badUpdateResp resource.UpdateResponse
	badUpdateResp.State = tfsdk.State{Schema: schemaResp.Schema}
	agentResource.Update(ctx, resource.UpdateRequest{Plan: badUpdatePlan, State: createResp.State}, &badUpdateResp)
	if !badUpdateResp.Diagnostics.HasError() {
		t.Fatal("expected Update to fail with missing secret")
	}

	badJSONUpdateModel := updatePlanModel
	badJSONUpdateModel.Env = nil
	badJSONUpdateModel.AdapterConfig = types.StringValue(`{"env":{"SEC_VAR":{"type":"secret_ref","secretId":"sec-missing"}}}`)
	var badJSONUpdatePlan tfsdk.Plan
	badJSONUpdatePlan.Schema = schemaResp.Schema
	_ = badJSONUpdatePlan.Set(ctx, &badJSONUpdateModel)
	var badJSONUpdateResp resource.UpdateResponse
	badJSONUpdateResp.State = tfsdk.State{Schema: schemaResp.Schema}
	agentResource.Update(ctx, resource.UpdateRequest{Plan: badJSONUpdatePlan, State: createResp.State}, &badJSONUpdateResp)
	if !badJSONUpdateResp.Diagnostics.HasError() {
		t.Fatal("expected Update to fail with missing secret in adapter_config")
	}

	// Create agent with null adapter_config and non-empty Env (covers adapterMap == nil and !ok in Create)
	createNoAdapterCfgModel := PaperclipAgentResourceModel{
		CompanyID:       types.StringValue("comp-1"),
		Name:            types.StringValue("agent-no-adapter-cfg"),
		CanAssignTasks:  types.BoolValue(true),
		CanCreateAgents: types.BoolValue(true),
		Env: map[string]EnvBindingModel{
			"PLAIN_VAR": {
				Type:  types.StringValue("plain"),
				Value: types.StringValue("val1"),
			},
		},
	}
	var planNoAdapter tfsdk.Plan
	planNoAdapter.Schema = schemaResp.Schema
	_ = planNoAdapter.Set(ctx, &createNoAdapterCfgModel)
	var createNoAdapterResp resource.CreateResponse
	createNoAdapterResp.State = tfsdk.State{Schema: schemaResp.Schema}
	agentResource.Create(ctx, resource.CreateRequest{Plan: planNoAdapter}, &createNoAdapterResp)
	if createNoAdapterResp.Diagnostics.HasError() {
		t.Fatalf("unexpected Create error for no-adapter-cfg: %v", createNoAdapterResp.Diagnostics)
	}

	// Update when adapterConfig is null in both state and plan (covers adapterMap == nil in Update)
	nilAdapterStateModel := updatePlanModel
	nilAdapterStateModel.AdapterConfig = types.StringNull()
	nilAdapterPlanModel := nilAdapterStateModel
	nilAdapterPlanModel.AdapterConfig = types.StringNull()
	nilAdapterPlanModel.Env = map[string]EnvBindingModel{
		"V": {Type: types.StringValue("plain"), Value: types.StringValue("1")},
	}
	var nilAdapterState tfsdk.State
	nilAdapterState = tfsdk.State{Schema: schemaResp.Schema}
	_ = nilAdapterState.Set(ctx, &nilAdapterStateModel)
	var nilAdapterPlan tfsdk.Plan
	nilAdapterPlan = tfsdk.Plan{Schema: schemaResp.Schema}
	_ = nilAdapterPlan.Set(ctx, &nilAdapterPlanModel)
	var nilAdapterResp resource.UpdateResponse
	nilAdapterResp.State = tfsdk.State{Schema: schemaResp.Schema}
	agentResource.Update(ctx, resource.UpdateRequest{Plan: nilAdapterPlan, State: nilAdapterState}, &nilAdapterResp)
	if nilAdapterResp.Diagnostics.HasError() {
		t.Fatalf("unexpected Update error for nil adapterMap: %v", nilAdapterResp.Diagnostics)
	}

	// Clear Env when adapterMap["env"] has no other keys -> delete(adapterMap, "env")
	deleteEnvStateModel := updatePlanModel
	deleteEnvStateModel.AdapterConfig = types.StringValue(`{"env":{"GONE":{"type":"plain","value":"1"}}}`)
	deleteEnvStateModel.Env = map[string]EnvBindingModel{
		"GONE": {Type: types.StringValue("plain"), Value: types.StringValue("1")},
	}
	deleteEnvPlanModel := deleteEnvStateModel
	deleteEnvPlanModel.Env = map[string]EnvBindingModel{}
	var deleteEnvState tfsdk.State
	deleteEnvState = tfsdk.State{Schema: schemaResp.Schema}
	_ = deleteEnvState.Set(ctx, &deleteEnvStateModel)
	var deleteEnvPlan tfsdk.Plan
	deleteEnvPlan = tfsdk.Plan{Schema: schemaResp.Schema}
	_ = deleteEnvPlan.Set(ctx, &deleteEnvPlanModel)
	var deleteEnvResp resource.UpdateResponse
	deleteEnvResp.State = tfsdk.State{Schema: schemaResp.Schema}
	agentResource.Update(ctx, resource.UpdateRequest{Plan: deleteEnvPlan, State: deleteEnvState}, &deleteEnvResp)
	if deleteEnvResp.Diagnostics.HasError() {
		t.Fatalf("unexpected Clear error for delete env: %v", deleteEnvResp.Diagnostics)
	}
}

func TestPaperclipSecretLatestVersionValidateConfig(t *testing.T) {
	ctx := context.Background()
	secretResource := NewPaperclipSecretResource().(*PaperclipSecretResource)
	var schemaResp resource.SchemaResponse
	secretResource.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	badModel := PaperclipSecretResourceModel{
		CompanyID:     types.StringValue("comp-1"),
		Name:          types.StringValue("sec-1"),
		LatestVersion: types.Int64Value(0),
	}
	var plan1 tfsdk.Plan
	plan1.Schema = schemaResp.Schema
	_ = plan1.Set(ctx, &badModel)
	config1 := tfsdk.Config{Schema: schemaResp.Schema, Raw: plan1.Raw}
	var resp1 resource.ValidateConfigResponse
	secretResource.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: config1}, &resp1)
	if !resp1.Diagnostics.HasError() {
		t.Fatal("expected error for latest_version < 1")
	}

	goodModel := PaperclipSecretResourceModel{
		CompanyID:     types.StringValue("comp-1"),
		Name:          types.StringValue("sec-1"),
		LatestVersion: types.Int64Value(1),
	}
	var plan2 tfsdk.Plan
	plan2.Schema = schemaResp.Schema
	_ = plan2.Set(ctx, &goodModel)
	config2 := tfsdk.Config{Schema: schemaResp.Schema, Raw: plan2.Raw}
	var resp2 resource.ValidateConfigResponse
	secretResource.ValidateConfig(ctx, resource.ValidateConfigRequest{Config: config2}, &resp2)
	if resp2.Diagnostics.HasError() {
		t.Fatalf("unexpected error for latest_version = 1: %v", resp2.Diagnostics)
	}
}

func TestPaperclipSecretOutOfBandRotationDrift(t *testing.T) {
	ctx := context.Background()
	secretResource := NewPaperclipSecretResource().(*PaperclipSecretResource)
	var schemaResp resource.SchemaResponse
	secretResource.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	patchOrRotateCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/companies/comp-1/secrets" {
			_ = json.NewEncoder(w).Encode([]client.PaperclipSecret{
				{
					ID:            "sec-1",
					CompanyID:     "comp-1",
					Name:          "test-secret",
					LatestVersion: 2,
					LastRotatedAt: "2026-10-08T00:00:00Z",
				},
			})
			return
		}
		if r.Method == http.MethodPatch || strings.HasSuffix(r.URL.Path, "/rotate") {
			patchOrRotateCalled = true
			_ = json.NewEncoder(w).Encode(client.PaperclipSecret{
				ID:            "sec-1",
				CompanyID:     "comp-1",
				Name:          "test-secret",
				LatestVersion: 2,
				LastRotatedAt: "2026-10-08T00:00:00Z",
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	cli, _ := client.NewClient(server.URL, "token")
	secretResource.client = cli

	// 1. Initial state before refresh: latest_version was 1
	stateModel := PaperclipSecretResourceModel{
		ID:            types.StringValue("sec-1"),
		CompanyID:     types.StringValue("comp-1"),
		Name:          types.StringValue("test-secret"),
		LatestVersion: types.Int64Value(1),
		LastRotatedAt: types.StringValue("2026-10-01T00:00:00Z"),
	}
	rState := tfsdk.State{Schema: schemaResp.Schema}
	_ = rState.Set(ctx, &stateModel)
	var rResp resource.ReadResponse
	rResp.State = tfsdk.State{Schema: schemaResp.Schema}
	secretResource.Read(ctx, resource.ReadRequest{State: rState}, &rResp)
	if rResp.Diagnostics.HasError() {
		t.Fatalf("unexpected read error: %v", rResp.Diagnostics)
	}

	var resState PaperclipSecretResourceModel
	_ = rResp.State.Get(ctx, &resState)
	if resState.LatestVersion.ValueInt64() != 2 {
		t.Fatalf("expected latest_version to reflect remote rotation to 2, got %v", resState.LatestVersion)
	}
	if resState.LastRotatedAt.ValueString() != "2026-10-08T00:00:00Z" {
		t.Fatalf("expected last_rotated_at to reflect remote rotation, got %v", resState.LastRotatedAt)
	}

	// 2. Exercise ModifyPlan: pinned config with latest_version = 1 preserves plan diff against refreshed state (2)
	cfgModel := PaperclipSecretResourceModel{
		ID:            types.StringValue("sec-1"),
		CompanyID:     types.StringValue("comp-1"),
		Name:          types.StringValue("test-secret"),
		LatestVersion: types.Int64Value(1),
		LastRotatedAt: types.StringValue("2026-10-01T00:00:00Z"),
	}
	planModel := cfgModel
	plan := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = plan.Set(ctx, &planModel)
	cfg := tfsdk.Config{Schema: schemaResp.Schema, Raw: plan.Raw}

	var mpResp resource.ModifyPlanResponse
	mpResp.Plan = plan
	secretResource.ModifyPlan(ctx, resource.ModifyPlanRequest{
		State:  rResp.State,
		Config: cfg,
		Plan:   plan,
	}, &mpResp)
	if mpResp.Diagnostics.HasError() {
		t.Fatalf("unexpected modify plan error: %v", mpResp.Diagnostics)
	}

	var plannedModel PaperclipSecretResourceModel
	_ = mpResp.Plan.Get(ctx, &plannedModel)
	if plannedModel.LatestVersion.ValueInt64() != 1 {
		t.Fatalf("expected plan to preserve configured latest_version (1) to show drift, got %v", plannedModel.LatestVersion)
	}

	// 3. Exercise Update with drifted latest_version: must return explicit diagnostic Secret Version Mismatch
	var uResp resource.UpdateResponse
	uResp.State = rResp.State
	secretResource.Update(ctx, resource.UpdateRequest{
		Plan:   mpResp.Plan,
		State:  rResp.State,
		Config: cfg,
	}, &uResp)
	if !uResp.Diagnostics.HasError() {
		t.Fatal("expected update to fail when applying out-of-band rotation drift")
	}
	foundVersionMismatch := false
	for _, diag := range uResp.Diagnostics.Errors() {
		if diag.Summary() == "Secret Version Mismatch" && strings.Contains(diag.Detail(), "reconciled with configured latest_version 1") {
			foundVersionMismatch = true
			break
		}
	}
	if !foundVersionMismatch {
		t.Fatalf("expected 'Secret Version Mismatch' diagnostic error, got: %v", uResp.Diagnostics)
	}
	if patchOrRotateCalled {
		t.Fatal("expected no PATCH or rotate to be called on unacknowledged version mismatch")
	}

	// 4. Exercise Update with drifted last_rotated_at: must return Secret Rotation Timestamp Mismatch
	cfgModelTimestampDrift := cfgModel
	cfgModelTimestampDrift.LatestVersion = types.Int64Value(2) // version acknowledged
	planTimestamp := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = planTimestamp.Set(ctx, &cfgModelTimestampDrift)
	cfgTimestamp := tfsdk.Config{Schema: schemaResp.Schema, Raw: planTimestamp.Raw}

	var uRespTimestamp resource.UpdateResponse
	uRespTimestamp.State = rResp.State
	secretResource.Update(ctx, resource.UpdateRequest{
		Plan:   planTimestamp,
		State:  rResp.State,
		Config: cfgTimestamp,
	}, &uRespTimestamp)
	if !uRespTimestamp.Diagnostics.HasError() {
		t.Fatal("expected update to fail when applying timestamp drift")
	}
	foundTimestampMismatch := false
	for _, diag := range uRespTimestamp.Diagnostics.Errors() {
		if diag.Summary() == "Secret Rotation Timestamp Mismatch" {
			foundTimestampMismatch = true
			break
		}
	}
	if !foundTimestampMismatch {
		t.Fatalf("expected 'Secret Rotation Timestamp Mismatch' diagnostic error, got: %v", uRespTimestamp.Diagnostics)
	}

	// 5. Exercise Update when configuration converges (both latest_version and last_rotated_at match)
	cfgModelConverged := cfgModel
	cfgModelConverged.LatestVersion = types.Int64Value(2)
	cfgModelConverged.LastRotatedAt = types.StringValue("2026-10-08T00:00:00Z")
	planConverged := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = planConverged.Set(ctx, &cfgModelConverged)
	cfgConverged := tfsdk.Config{Schema: schemaResp.Schema, Raw: planConverged.Raw}

	var uRespConverged resource.UpdateResponse
	uRespConverged.State = rResp.State
	secretResource.Update(ctx, resource.UpdateRequest{
		Plan:   planConverged,
		State:  rResp.State,
		Config: cfgConverged,
	}, &uRespConverged)
	if uRespConverged.Diagnostics.HasError() {
		t.Fatalf("expected converged update to succeed, got errors: %v", uRespConverged.Diagnostics)
	}
	var convergedState PaperclipSecretResourceModel
	_ = uRespConverged.State.Get(ctx, &convergedState)
	if convergedState.LatestVersion.ValueInt64() != 2 {
		t.Fatalf("expected converged state latest_version 2, got %v", convergedState.LatestVersion)
	}

	// 6. Exercise Create with mismatched latest_version
	createCfgModel := PaperclipSecretResourceModel{
		CompanyID:     types.StringValue("comp-1"),
		Name:          types.StringValue("test-create-secret"),
		LatestVersion: types.Int64Value(2),
	}
	createPlan := tfsdk.Plan{Schema: schemaResp.Schema}
	_ = createPlan.Set(ctx, &createCfgModel)
	createCfg := tfsdk.Config{Schema: schemaResp.Schema, Raw: createPlan.Raw}

	serverCreate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/api/companies/comp-1/secrets" {
			_ = json.NewEncoder(w).Encode(client.PaperclipSecret{
				ID:            "sec-created",
				CompanyID:     "comp-1",
				Name:          "test-create-secret",
				LatestVersion: 1,
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer serverCreate.Close()
	cliCreate, _ := client.NewClient(serverCreate.URL, "token")
	secretResource.client = cliCreate

	var cResp resource.CreateResponse
	cResp.State = tfsdk.State{Schema: schemaResp.Schema}
	secretResource.Create(ctx, resource.CreateRequest{
		Plan:   createPlan,
		Config: createCfg,
	}, &cResp)
	if !cResp.Diagnostics.HasError() {
		t.Fatal("expected create to fail when configured latest_version does not match initial version")
	}
	var createdState PaperclipSecretResourceModel
	_ = cResp.State.Get(ctx, &createdState)
	if createdState.ID.ValueString() != "sec-created" {
		t.Fatalf("expected created secret ID 'sec-created' in resp.State to prevent orphaning, got: %v", createdState.ID)
	}
	foundCreateMismatch := false
	for _, diag := range cResp.Diagnostics.Errors() {
		if diag.Summary() == "Secret Version Mismatch" && strings.Contains(diag.Detail(), "after creation") {
			foundCreateMismatch = true
			break
		}
	}
	if !foundCreateMismatch {
		t.Fatalf("expected create 'Secret Version Mismatch' diagnostic error, got: %v", cResp.Diagnostics)
	}
}

func TestAgentUpdateEnvPreReadError(t *testing.T) {
	ctx := context.Background()
	patchCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/api/agents/agent-env-err" {
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}
		if r.Method == http.MethodPatch {
			patchCalled = true
			t.Errorf("unexpected PATCH request when GetAgent failed")
			http.Error(w, "should not be called", http.StatusInternalServerError)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	r := NewPaperclipAgentResource().(*PaperclipAgentResource)
	schema := configureTestResource(t, server.URL, r)

	envMap := map[string]EnvBindingModel{
		"FOO": {
			Type:  types.StringValue("plain"),
			Value: types.StringValue("bar"),
		},
	}
	plan := tfsdk.Plan{Schema: schema}
	_ = plan.Set(ctx, &PaperclipAgentResourceModel{
		ID:        types.StringValue("agent-env-err"),
		CompanyID: types.StringValue("comp-1"),
		Env:       envMap,
	})
	state := tfsdk.State{Schema: schema}
	_ = state.Set(ctx, &PaperclipAgentResourceModel{
		ID:        types.StringValue("agent-env-err"),
		CompanyID: types.StringValue("comp-1"),
	})
	config := tfsdk.Config{Schema: schema, Raw: plan.Raw}

	var updateResp resource.UpdateResponse
	updateResp.State = state
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state, Config: config}, &updateResp)
	if !updateResp.Diagnostics.HasError() {
		t.Fatal("expected error when GetAgent fails during Update with env")
	}
	foundErr := false
	for _, diag := range updateResp.Diagnostics.Errors() {
		if diag.Summary() == "Error reading Paperclip agent before update" {
			foundErr = true
			break
		}
	}
	if !foundErr {
		t.Fatalf("expected 'Error reading Paperclip agent before update' diagnostic, got: %v", updateResp.Diagnostics)
	}
	if patchCalled {
		t.Fatal("expected no PATCH to be sent when pre-update GetAgent fails")
	}
}

func TestPaperclipSecretModifyPlan(t *testing.T) {
	ctx := context.Background()
	secretResource := NewPaperclipSecretResource().(*PaperclipSecretResource)
	var schemaResp resource.SchemaResponse
	secretResource.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	// 1. Plan.Raw.IsNull()
	var respNullPlan resource.ModifyPlanResponse
	secretResource.ModifyPlan(ctx, resource.ModifyPlanRequest{
		Plan: tfsdk.Plan{Schema: schemaResp.Schema},
	}, &respNullPlan)
	if respNullPlan.Diagnostics.HasError() {
		t.Fatalf("unexpected error on null plan: %v", respNullPlan.Diagnostics)
	}

	// 2. State.Raw.IsNull() (Create plan)
	planModel := PaperclipSecretResourceModel{
		CompanyID:    types.StringValue("comp-1"),
		Name:         types.StringValue("sec-1"),
		ProviderType: types.StringValue("local_encrypted"),
		ManagedMode:  types.StringValue("paperclip_managed"),
	}
	var planCreate tfsdk.Plan
	planCreate.Schema = schemaResp.Schema
	_ = planCreate.Set(ctx, &planModel)
	var respCreate resource.ModifyPlanResponse
	respCreate.Plan = planCreate
	secretResource.ModifyPlan(ctx, resource.ModifyPlanRequest{
		Plan:  planCreate,
		State: tfsdk.State{Schema: schemaResp.Schema},
	}, &respCreate)
	if respCreate.Diagnostics.HasError() {
		t.Fatalf("unexpected error on create plan: %v", respCreate.Diagnostics)
	}

	// 3. State.Get error
	badState := tfsdk.State{Schema: schemaResp.Schema, Raw: tftypes.NewValue(tftypes.String, "invalid")}
	var respBadState resource.ModifyPlanResponse
	respBadState.Plan = planCreate
	secretResource.ModifyPlan(ctx, resource.ModifyPlanRequest{
		Plan:  planCreate,
		State: badState,
	}, &respBadState)
	if !respBadState.Diagnostics.HasError() {
		t.Fatal("expected error on bad state")
	}

	// 4. Config.Get error
	stateModel := PaperclipSecretResourceModel{
		ID:            types.StringValue("sec-1"),
		CompanyID:     types.StringValue("comp-1"),
		Name:          types.StringValue("sec-1"),
		Rotation:      types.StringValue("rev-1"),
		LatestVersion: types.Int64Value(1),
		LastRotatedAt: types.StringValue("2026-10-01T00:00:00Z"),
	}
	var validState tfsdk.State
	validState.Schema = schemaResp.Schema
	_ = validState.Set(ctx, &stateModel)

	badConfig := tfsdk.Config{Schema: schemaResp.Schema, Raw: tftypes.NewValue(tftypes.String, "invalid")}
	var respBadConfig resource.ModifyPlanResponse
	respBadConfig.Plan = planCreate
	secretResource.ModifyPlan(ctx, resource.ModifyPlanRequest{
		Plan:   planCreate,
		State:  validState,
		Config: badConfig,
	}, &respBadConfig)
	if !respBadConfig.Diagnostics.HasError() {
		t.Fatal("expected error on bad config")
	}

	// 5. Not rotating: plan.Rotation == state.Rotation
	planNotRotatingModel := stateModel
	var planNotRotating tfsdk.Plan
	planNotRotating.Schema = schemaResp.Schema
	_ = planNotRotating.Set(ctx, &planNotRotatingModel)
	configNotRotating := tfsdk.Config{Schema: schemaResp.Schema, Raw: planNotRotating.Raw}
	var respNotRotating resource.ModifyPlanResponse
	respNotRotating.Plan = planNotRotating
	secretResource.ModifyPlan(ctx, resource.ModifyPlanRequest{
		Plan:   planNotRotating,
		State:  validState,
		Config: configNotRotating,
	}, &respNotRotating)
	if respNotRotating.Diagnostics.HasError() {
		t.Fatalf("unexpected error on non-rotating plan: %v", respNotRotating.Diagnostics)
	}

	// 6. Rotating with unconfigured latest_version & last_rotated_at -> becomes unknown
	planRotatingModel := stateModel
	planRotatingModel.Rotation = types.StringValue("rev-2")
	var planRotating tfsdk.Plan
	planRotating.Schema = schemaResp.Schema
	_ = planRotating.Set(ctx, &planRotatingModel)

	configUnconfiguredModel := PaperclipSecretResourceModel{
		CompanyID:     types.StringValue("comp-1"),
		Name:          types.StringValue("sec-1"),
		Rotation:      types.StringValue("rev-2"),
		LatestVersion: types.Int64Null(),
		LastRotatedAt: types.StringNull(),
	}
	var planConfigUnconfigured tfsdk.Plan
	planConfigUnconfigured.Schema = schemaResp.Schema
	_ = planConfigUnconfigured.Set(ctx, &configUnconfiguredModel)
	configUnconfigured := tfsdk.Config{Schema: schemaResp.Schema, Raw: planConfigUnconfigured.Raw}

	var respRotating resource.ModifyPlanResponse
	respRotating.Plan = planRotating
	secretResource.ModifyPlan(ctx, resource.ModifyPlanRequest{
		Plan:   planRotating,
		State:  validState,
		Config: configUnconfigured,
	}, &respRotating)
	if respRotating.Diagnostics.HasError() {
		t.Fatalf("unexpected error on rotating plan: %v", respRotating.Diagnostics)
	}
	var resRotatingModel PaperclipSecretResourceModel
	_ = respRotating.Plan.Get(ctx, &resRotatingModel)
	if !resRotatingModel.LatestVersion.IsUnknown() {
		t.Fatal("expected LatestVersion to be unknown on rotation when unconfigured")
	}
	if !resRotatingModel.LastRotatedAt.IsUnknown() {
		t.Fatal("expected LastRotatedAt to be unknown on rotation when unconfigured")
	}

	// 7. Rotating with configured latest_version & last_rotated_at -> retains values
	configConfiguredModel := configUnconfiguredModel
	configConfiguredModel.LatestVersion = types.Int64Value(2)
	configConfiguredModel.LastRotatedAt = types.StringValue("2026-10-11T00:00:00Z")
	var planConfigConfigured tfsdk.Plan
	planConfigConfigured.Schema = schemaResp.Schema
	_ = planConfigConfigured.Set(ctx, &configConfiguredModel)
	configConfigured := tfsdk.Config{Schema: schemaResp.Schema, Raw: planConfigConfigured.Raw}

	planRotatingConfiguredModel := planRotatingModel
	planRotatingConfiguredModel.LatestVersion = types.Int64Value(2)
	planRotatingConfiguredModel.LastRotatedAt = types.StringValue("2026-10-11T00:00:00Z")
	var planRotatingConfigured tfsdk.Plan
	planRotatingConfigured.Schema = schemaResp.Schema
	_ = planRotatingConfigured.Set(ctx, &planRotatingConfiguredModel)

	var respRotatingConfigured resource.ModifyPlanResponse
	respRotatingConfigured.Plan = planRotatingConfigured
	secretResource.ModifyPlan(ctx, resource.ModifyPlanRequest{
		Plan:   planRotatingConfigured,
		State:  validState,
		Config: configConfigured,
	}, &respRotatingConfigured)
	if respRotatingConfigured.Diagnostics.HasError() {
		t.Fatalf("unexpected error on configured rotating plan: %v", respRotatingConfigured.Diagnostics)
	}
	var resConfiguredModel PaperclipSecretResourceModel
	_ = respRotatingConfigured.Plan.Get(ctx, &resConfiguredModel)
	if resConfiguredModel.LatestVersion.ValueInt64() != 2 {
		t.Fatalf("expected LatestVersion to be 2, got %v", resConfiguredModel.LatestVersion)
	}
	if resConfiguredModel.LastRotatedAt.ValueString() != "2026-10-11T00:00:00Z" {
		t.Fatalf("expected LastRotatedAt to match configured value, got %v", resConfiguredModel.LastRotatedAt)
	}
}

func TestSecretProviderConfigCreateInvalidConfig(t *testing.T) {
	ctx := context.Background()
	r := NewPaperclipSecretProviderConfigResource().(*PaperclipSecretProviderConfigResource)
	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	var received client.PaperclipSecretProviderConfig
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost && r.URL.Path == "/api/companies/comp-1/secret-provider-configs" {
			_ = json.NewDecoder(r.Body).Decode(&received)
			received.ID = "spc-1"
			received.CompanyID = "comp-1"
			_ = json.NewEncoder(w).Encode(received)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	cli, _ := client.NewClient(server.URL, "token")
	r.client = cli

	planModel := PaperclipSecretProviderConfigResourceModel{
		CompanyID:    types.StringValue("comp-1"),
		DisplayName:  types.StringValue("Vault"),
		ProviderType: types.StringValue("vault"),
		Config:       types.StringValue("{invalid-json"),
	}
	var plan tfsdk.Plan
	plan.Schema = schemaResp.Schema
	_ = plan.Set(ctx, &planModel)
	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected Create error: %v", resp.Diagnostics)
	}
	if len(received.Config) != 0 {
		t.Fatalf("expected empty config for invalid JSON, got: %v", received.Config)
	}

	// Test Create with null config (covers else branch in Create)
	planModelNull := planModel
	planModelNull.Config = types.StringNull()
	var planNull tfsdk.Plan
	planNull.Schema = schemaResp.Schema
	_ = planNull.Set(ctx, &planModelNull)
	var respNull resource.CreateResponse
	respNull.State = tfsdk.State{Schema: schemaResp.Schema}
	r.Create(ctx, resource.CreateRequest{Plan: planNull}, &respNull)
	if respNull.Diagnostics.HasError() {
		t.Fatalf("unexpected Create error with null config: %v", respNull.Diagnostics)
	}
	if len(received.Config) != 0 {
		t.Fatalf("expected empty config for null config, got: %v", received.Config)
	}
}
