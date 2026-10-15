package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var secretKeyRegex = regexp.MustCompile(`^[a-zA-Z0-9_.-]+$`)
var envVariableKeyRegex = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type EnvBindingModel struct {
	Type                   types.String `tfsdk:"type"`
	Value                  types.String `tfsdk:"value"`
	SecretID               types.String `tfsdk:"secret_id"`
	Key                    types.String `tfsdk:"key"`
	Version                types.String `tfsdk:"version"`
	ProjectionClass        types.String `tfsdk:"projection_class"`
	ProjectionAllowlistKey types.String `tfsdk:"projection_allowlist_key"`
	Required               types.Bool   `tfsdk:"required"`
	AllowMissingOverride   types.Bool   `tfsdk:"allow_missing_override"`
}

func EnvBindingAttributeType() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"type": schema.StringAttribute{
			Description: "Binding type: plain, secret_ref, or user_secret_ref.",
			Required:    true,
		},
		"value": schema.StringAttribute{
			Description: "Plaintext value for plain bindings. Must not specify a redacted sentinel.",
			Optional:    true,
			Sensitive:   true,
		},
		"secret_id": schema.StringAttribute{
			Description: "Target Paperclip company secret ID for secret_ref bindings.",
			Optional:    true,
		},
		"key": schema.StringAttribute{
			Description: "Target user secret definition key for user_secret_ref bindings.",
			Optional:    true,
		},
		"version": schema.StringAttribute{
			Description: "Version selector for secret references: latest (native default) or a canonical positive integer string, sent as a native JSON integer.",
			Optional:    true,
			Computed:    true,
		},
		"projection_class": schema.StringAttribute{
			Description: "Projection classification for secret_ref bindings: unclassified (native default) or class_3_static_lease.",
			Optional:    true,
			Computed:    true,
		},
		"projection_allowlist_key": schema.StringAttribute{
			Description: "Allowlist key required when projection_class is 'class_3_static_lease'.",
			Optional:    true,
		},
		"required": schema.BoolAttribute{
			Description: "Whether the secret is required for user_secret_ref bindings (default: true).",
			Optional:    true,
			Computed:    true,
		},
		"allow_missing_override": schema.BoolAttribute{
			Description: "Whether to allow missing override for user_secret_ref bindings (default: false).",
			Optional:    true,
			Computed:    true,
		},
	}
}

func EnvBindingMapAttribute(description string) schema.MapNestedAttribute {
	return schema.MapNestedAttribute{
		Description: description,
		Optional:    true,
		NestedObject: schema.NestedAttributeObject{
			Attributes: EnvBindingAttributeType(),
		},
	}
}

type Class3StaticLeaseAllowlistEntry struct {
	Key        string
	TargetType string
	ConfigPath string
	EnvKey     string
}

var Class3StaticLeaseAllowlist = []Class3StaticLeaseAllowlistEntry{
	{Key: "slack.bot_token", TargetType: "agent", ConfigPath: "env.SLACK_BOT_TOKEN", EnvKey: "SLACK_BOT_TOKEN"},
	{Key: "slack.bot_token", TargetType: "routine", ConfigPath: "env.SLACK_BOT_TOKEN", EnvKey: "SLACK_BOT_TOKEN"},
	{Key: "slack.bot_token", TargetType: "tool_connection", ConfigPath: "credentials.bot_token", EnvKey: "SLACK_BOT_TOKEN"},
	{Key: "discord.bot_token", TargetType: "agent", ConfigPath: "env.DISCORD_BOT_TOKEN", EnvKey: "DISCORD_BOT_TOKEN"},
	{Key: "discord.bot_token", TargetType: "routine", ConfigPath: "env.DISCORD_BOT_TOKEN", EnvKey: "DISCORD_BOT_TOKEN"},
	{Key: "discord.bot_token", TargetType: "tool_connection", ConfigPath: "credentials.bot_token", EnvKey: "DISCORD_BOT_TOKEN"},
}

func IsRedactedSentinel(val string) bool {
	trimmed := strings.TrimSpace(val)
	return strings.EqualFold(trimmed, "[redacted]") ||
		strings.EqualFold(trimmed, "<redacted>") ||
		strings.EqualFold(trimmed, "redacted")
}

func IsAllowlistedClass3Key(key, targetType, configPath string) bool {
	for _, entry := range Class3StaticLeaseAllowlist {
		if entry.Key == key {
			if targetType != "" && entry.TargetType != targetType {
				continue
			}
			if configPath != "" && entry.ConfigPath != configPath {
				continue
			}
			return true
		}
	}
	return false
}

func ValidateEnvBindingPlan(p path.Path, binding EnvBindingModel, targetType, configPath string) diag.Diagnostics {
	var diags diag.Diagnostics

	if binding.Type.IsUnknown() {
		return diags
	}
	if binding.Type.IsNull() || strings.TrimSpace(binding.Type.ValueString()) == "" {
		diags.AddAttributeError(p.AtName("type"), "Missing Environment Binding Type", "Binding type is required (must be 'plain', 'secret_ref', or 'user_secret_ref').")
		return diags
	}

	bType := binding.Type.ValueString()
	switch bType {
	case "plain":
		if binding.Value.IsNull() {
			diags.AddAttributeError(p.AtName("value"), "Missing Plain Binding Value", "Plain environment bindings require an explicit value; an empty string is supported by the native API.")
		} else if !binding.Value.IsUnknown() && IsRedactedSentinel(binding.Value.ValueString()) {
			diags.AddAttributeError(p.AtName("value"), "Invalid Plain Binding Value", "Invalid environment binding value: redacted sentinel \"[REDACTED]\" is prohibited during plan. Plain bindings must contain real configuration values, not redacted masking strings.")
		}

		if !binding.SecretID.IsNull() && (!binding.SecretID.IsUnknown() && binding.SecretID.ValueString() != "") {
			diags.AddAttributeError(p.AtName("secret_id"), "Invalid Attribute for Plain Binding", "Plain bindings cannot declare secret_id.")
		}
		if !binding.Key.IsNull() && (!binding.Key.IsUnknown() && binding.Key.ValueString() != "") {
			diags.AddAttributeError(p.AtName("key"), "Invalid Attribute for Plain Binding", "Plain bindings cannot declare key.")
		}
		if !binding.ProjectionClass.IsNull() && (!binding.ProjectionClass.IsUnknown() && binding.ProjectionClass.ValueString() != "") {
			diags.AddAttributeError(p.AtName("projection_class"), "Invalid Attribute for Plain Binding", "Plain bindings cannot declare projection_class.")
		}
		if !binding.ProjectionAllowlistKey.IsNull() && (!binding.ProjectionAllowlistKey.IsUnknown() && binding.ProjectionAllowlistKey.ValueString() != "") {
			diags.AddAttributeError(p.AtName("projection_allowlist_key"), "Invalid Attribute for Plain Binding", "Plain bindings cannot declare projection_allowlist_key.")
		}

	case "secret_ref":
		if binding.SecretID.IsNull() || (!binding.SecretID.IsUnknown() && strings.TrimSpace(binding.SecretID.ValueString()) == "") {
			diags.AddAttributeError(p.AtName("secret_id"), "Missing Secret ID", "secret_ref bindings require secret_id.")
		}
		if !binding.Value.IsNull() && (!binding.Value.IsUnknown() && binding.Value.ValueString() != "") {
			diags.AddAttributeError(p.AtName("value"), "Invalid Attribute for Secret Reference", "secret_ref bindings cannot declare a plaintext value.")
		}
		if !binding.Key.IsNull() && (!binding.Key.IsUnknown() && binding.Key.ValueString() != "") {
			diags.AddAttributeError(p.AtName("key"), "Invalid Attribute for Secret Reference", "secret_ref bindings cannot declare key.")
		}

		if !binding.ProjectionClass.IsNull() && !binding.ProjectionClass.IsUnknown() {
			proj := strings.TrimSpace(binding.ProjectionClass.ValueString())
			if proj != "unclassified" && proj != "class_3_static_lease" {
				diags.AddAttributeError(p.AtName("projection_class"), "Invalid Projection Class", "projection_class must be either 'unclassified' or 'class_3_static_lease'.")
			} else if proj == "class_3_static_lease" {
				if binding.ProjectionAllowlistKey.IsNull() || (!binding.ProjectionAllowlistKey.IsUnknown() && strings.TrimSpace(binding.ProjectionAllowlistKey.ValueString()) == "") {
					diags.AddAttributeError(p.AtName("projection_allowlist_key"), "Missing Projection Allowlist Key", "class_3_static_lease projection requires projection_allowlist_key ('slack.bot_token' or 'discord.bot_token').")
				} else if !binding.ProjectionAllowlistKey.IsUnknown() {
					alk := strings.TrimSpace(binding.ProjectionAllowlistKey.ValueString())
					if !IsAllowlistedClass3Key(alk, targetType, configPath) {
						diags.AddAttributeError(p.AtName("projection_allowlist_key"), "Unauthorized Projection Class 3 Allowlist Key", fmt.Sprintf("class_3_static_lease projection is not allowlisted for key %q; must be 'slack.bot_token' or 'discord.bot_token'.", alk))
					}
				}
			}
		}
		if !binding.ProjectionClass.IsUnknown() && binding.ProjectionClass.ValueString() != "class_3_static_lease" &&
			!binding.ProjectionAllowlistKey.IsNull() && !binding.ProjectionAllowlistKey.IsUnknown() && strings.TrimSpace(binding.ProjectionAllowlistKey.ValueString()) != "" {
			diags.AddAttributeError(p.AtName("projection_allowlist_key"), "Invalid Projection Allowlist Key", "projection_allowlist_key can only be specified when projection_class is 'class_3_static_lease'.")
		}

	case "user_secret_ref":
		if binding.Key.IsNull() || (!binding.Key.IsUnknown() && strings.TrimSpace(binding.Key.ValueString()) == "") {
			diags.AddAttributeError(p.AtName("key"), "Missing User Secret Key", "user_secret_ref bindings require key.")
		} else if !binding.Key.IsUnknown() {
			k := strings.TrimSpace(binding.Key.ValueString())
			if k != binding.Key.ValueString() {
				diags.AddAttributeError(p.AtName("key"), "Invalid User Secret Key", "key must be trimmed: native normalization would change the configured value")
			}
			if len(k) > 120 || !secretKeyRegex.MatchString(k) {
				diags.AddAttributeError(p.AtName("key"), "Invalid User Secret Key", fmt.Sprintf("Key %q must match ^[a-zA-Z0-9_.-]+$ and have length between 1 and 120.", k))
			}
		}
		if !binding.Value.IsNull() && (!binding.Value.IsUnknown() && binding.Value.ValueString() != "") {
			diags.AddAttributeError(p.AtName("value"), "Invalid Attribute for User Secret Reference", "user_secret_ref bindings cannot declare a plaintext value.")
		}
		if !binding.SecretID.IsNull() && (!binding.SecretID.IsUnknown() && binding.SecretID.ValueString() != "") {
			diags.AddAttributeError(p.AtName("secret_id"), "Invalid Attribute for User Secret Reference", "user_secret_ref bindings cannot declare secret_id.")
		}
		if !binding.ProjectionClass.IsNull() && (!binding.ProjectionClass.IsUnknown() && binding.ProjectionClass.ValueString() != "") {
			diags.AddAttributeError(p.AtName("projection_class"), "Invalid Attribute for User Secret Reference", "user_secret_ref bindings cannot declare projection_class.")
		}
		if !binding.ProjectionAllowlistKey.IsNull() && (!binding.ProjectionAllowlistKey.IsUnknown() && binding.ProjectionAllowlistKey.ValueString() != "") {
			diags.AddAttributeError(p.AtName("projection_allowlist_key"), "Invalid Attribute for User Secret Reference", "user_secret_ref bindings cannot declare projection_allowlist_key.")
		}

	default:
		diags.AddAttributeError(p.AtName("type"), "Unsupported Environment Binding Type", fmt.Sprintf("Unsupported binding type %q; must be 'plain', 'secret_ref', or 'user_secret_ref'.", bType))
	}

	if !binding.Version.IsNull() && !binding.Version.IsUnknown() {
		version := binding.Version.ValueString()
		number, err := strconv.ParseInt(version, 10, 64)
		if bType == "plain" || (version != "latest" && (err != nil || number <= 0 || number > 9007199254740991 || strconv.FormatInt(number, 10) != version)) {
			diags.AddAttributeError(p.AtName("version"), "Invalid Environment Binding Version", "version is supported only for secret references and must be latest or a canonical positive integer string within the native safe integer range")
		}
	}
	if bType != "user_secret_ref" && ((!binding.Required.IsNull() && !binding.Required.IsUnknown()) || (!binding.AllowMissingOverride.IsNull() && !binding.AllowMissingOverride.IsUnknown())) {
		diags.AddAttributeError(p, "Invalid User Secret Binding Flags", "required and allow_missing_override are supported only for user_secret_ref bindings")
	}
	return diags
}

func ValidateEnvBindingsMapPlan(p path.Path, bindings map[string]EnvBindingModel, targetType string) diag.Diagnostics {
	var diags diag.Diagnostics
	for k, b := range bindings {
		if !envVariableKeyRegex.MatchString(k) {
			diags.AddAttributeError(p.AtMapKey(k), "Invalid Environment Variable Name", "environment variable names must match ^[A-Za-z_][A-Za-z0-9_]*$")
		}
		configPath := fmt.Sprintf("env.%s", k)
		diags.Append(ValidateEnvBindingPlan(p.AtMapKey(k), b, targetType, configPath)...)
	}
	return diags
}

func ValidateEnvBindingsJSONPlan(p path.Path, rawJSON string, targetType string) diag.Diagnostics {
	var diags diag.Diagnostics
	if rawJSON == "" || strings.TrimSpace(rawJSON) == "null" {
		return diags
	}

	var rawMap map[string]interface{}
	if err := json.Unmarshal([]byte(rawJSON), &rawMap); err != nil {
		diags.AddAttributeError(p, "Invalid JSON", fmt.Sprintf("Environment configuration must be a valid JSON object: %s", err.Error()))
		return diags
	}

	for k, rawVal := range rawMap {
		configPath := fmt.Sprintf("env.%s", k)
		mapKeyPath := p.AtMapKey(k)

		switch val := rawVal.(type) {
		case string:
			if IsRedactedSentinel(val) {
				diags.AddAttributeError(mapKeyPath, "Invalid Plain Binding Value", "Invalid environment binding value: redacted sentinel \"[REDACTED]\" is prohibited during plan. Plain bindings must contain real configuration values, not redacted masking strings.")
			}
		case map[string]interface{}:
			diags.Append(ValidateEnvBindingPlan(mapKeyPath, envBindingFromJSON(val), targetType, configPath)...)
		}
	}

	return diags
}

func ValidateEnvBindingsApply(ctx context.Context, c *client.Client, companyID string, bindings map[string]EnvBindingModel) diag.Diagnostics {
	var diags diag.Diagnostics
	if c == nil || companyID == "" {
		return diags
	}

	for k, b := range bindings {
		if b.Type.ValueString() != "secret_ref" {
			continue
		}
		secretID := strings.TrimSpace(b.SecretID.ValueString())
		if secretID == "" {
			continue
		}

		secret, err := c.GetSecret(ctx, companyID, secretID)
		if err != nil {
			if errors.Is(err, client.ErrNotFound) {
				diags.AddError(
					"Invalid Secret Reference",
					fmt.Sprintf("Secret reference %q (for key %q) does not exist in target company %q (cross-company secret bindings are prohibited).", secretID, k, companyID),
				)
			} else {
				diags.AddError(
					"Secret Reference Verification Failed",
					fmt.Sprintf("Failed to verify secret reference %q (for key %q): %s", secretID, k, err.Error()),
				)
			}
		} else if secret.CompanyID != companyID {
			diags.AddError("Invalid Secret Reference", "The secret inventory returned a reference from a different company; no mutation was sent.")
		}
	}

	return diags
}

func ValidateEnvBindingsJSONApply(ctx context.Context, c *client.Client, companyID string, rawJSON string) diag.Diagnostics {
	var diags diag.Diagnostics
	if c == nil || companyID == "" || rawJSON == "" || strings.TrimSpace(rawJSON) == "null" {
		return diags
	}

	var rawMap map[string]interface{}
	if err := json.Unmarshal([]byte(rawJSON), &rawMap); err != nil {
		return diags
	}

	bindings := make(map[string]EnvBindingModel)
	for k, rawVal := range rawMap {
		if obj, ok := rawVal.(map[string]interface{}); ok {
			bindings[k] = envBindingFromJSON(obj)
		}
	}
	diags.Append(ValidateEnvBindingsApply(ctx, c, companyID, bindings)...)

	return diags
}

func EnvBindingsMapToAPI(bindings map[string]EnvBindingModel) map[string]interface{} {
	result := make(map[string]interface{}, len(bindings))
	for k, b := range bindings {
		switch b.Type.ValueString() {
		case "plain":
			result[k] = map[string]interface{}{
				"type":  "plain",
				"value": b.Value.ValueString(),
			}
		case "secret_ref":
			item := map[string]interface{}{
				"type":     "secret_ref",
				"secretId": b.SecretID.ValueString(),
			}
			if !b.Version.IsNull() && !b.Version.IsUnknown() && b.Version.ValueString() != "" {
				item["version"] = envBindingVersionToAPI(b.Version.ValueString())
			}
			if !b.ProjectionClass.IsNull() && !b.ProjectionClass.IsUnknown() && b.ProjectionClass.ValueString() != "" {
				item["projectionClass"] = b.ProjectionClass.ValueString()
			}
			if !b.ProjectionAllowlistKey.IsNull() && !b.ProjectionAllowlistKey.IsUnknown() && b.ProjectionAllowlistKey.ValueString() != "" {
				item["projectionAllowlistKey"] = b.ProjectionAllowlistKey.ValueString()
			}
			result[k] = item
		case "user_secret_ref":
			item := map[string]interface{}{
				"type": "user_secret_ref",
				"key":  b.Key.ValueString(),
			}
			if !b.Version.IsNull() && !b.Version.IsUnknown() && b.Version.ValueString() != "" {
				item["version"] = envBindingVersionToAPI(b.Version.ValueString())
			}
			if !b.Required.IsNull() && !b.Required.IsUnknown() {
				item["required"] = b.Required.ValueBool()
			}
			if !b.AllowMissingOverride.IsNull() && !b.AllowMissingOverride.IsUnknown() {
				item["allowMissingOverride"] = b.AllowMissingOverride.ValueBool()
			}
			result[k] = item
		}
	}
	return result
}

func envBindingVersionToAPI(version string) interface{} {
	if version == "latest" {
		return version
	}
	number, _ := strconv.ParseInt(version, 10, 64)
	return number
}

// normalizeEnvBindingJSON is the shared wire-key authority for structured bindings.
func normalizeEnvBindingJSON(obj map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{}, len(obj))
	for k, v := range obj {
		result[k] = v
	}
	for snake, camel := range map[string]string{
		"secret_id":                "secretId",
		"projection_class":         "projectionClass",
		"projection_allowlist_key": "projectionAllowlistKey",
		"allow_missing_override":   "allowMissingOverride",
	} {
		if value, ok := result[snake]; ok {
			if _, exists := result[camel]; !exists {
				result[camel] = value
			}
			delete(result, snake)
		}
	}
	return result
}

// envBindingFromJSON routes JSON references through the same model and rules as typed HCL bindings.
func envBindingFromJSON(obj map[string]interface{}) EnvBindingModel {
	obj = normalizeEnvBindingJSON(obj)
	stringField := func(key string) types.String {
		if key == "version" {
			switch version := obj[key].(type) {
			case float64:
				return types.StringValue(strconv.FormatFloat(version, 'f', -1, 64))
			case int64:
				return types.StringValue(strconv.FormatInt(version, 10))
			}
		}
		if value, ok := obj[key].(string); ok {
			return types.StringValue(value)
		}
		return types.StringNull()
	}
	result := EnvBindingModel{
		Type:                   stringField("type"),
		Value:                  stringField("value"),
		SecretID:               stringField("secretId"),
		Key:                    stringField("key"),
		Version:                stringField("version"),
		ProjectionClass:        stringField("projectionClass"),
		ProjectionAllowlistKey: stringField("projectionAllowlistKey"),
	}
	if value, ok := obj["required"].(bool); ok {
		result.Required = types.BoolValue(value)
	}
	if value, ok := obj["allowMissingOverride"].(bool); ok {
		result.AllowMissingOverride = types.BoolValue(value)
	}
	return result
}
