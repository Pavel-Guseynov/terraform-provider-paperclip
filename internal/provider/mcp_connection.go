package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Protected references share the binding identity/version/projection contract;
// a tool reference cannot carry plaintext or a user-secret override.
type toolSecretRef struct {
	SecretID               types.String `tfsdk:"secret_id"`
	Version                types.String `tfsdk:"version"`
	ProjectionClass        types.String `tfsdk:"projection_class"`
	ProjectionAllowlistKey types.String `tfsdk:"projection_allowlist_key"`
	Required               types.Bool   `tfsdk:"required"`
	Label                  types.String `tfsdk:"label"`
	KeyScope               types.String `tfsdk:"key_scope"`
	ExpiresAt              types.String `tfsdk:"expires_at"`
}

func toolSecretRefsAttribute() schema.MapNestedAttribute {
	shared := EnvBindingAttributeType()
	attrs := map[string]schema.Attribute{}
	for _, key := range []string{"secret_id", "version", "projection_class", "projection_allowlist_key"} {
		attribute := shared[key].(schema.StringAttribute)
		// Tool references retain omitted metadata; env bindings read native defaults.
		attribute.Computed = false
		attrs[key] = attribute
	}
	attrs["required"] = schema.BoolAttribute{Optional: true, Description: "Whether execution requires this reference."}
	for _, key := range []string{"label", "key_scope", "expires_at"} {
		attrs[key] = schema.StringAttribute{Optional: true, Description: "Native protected-reference " + key + "."}
	}
	return schema.MapNestedAttribute{Optional: true, Description: "Protected company secret references keyed by native configPath. Versions use latest or a positive integer string. Contains reference identities only, never credential values.", NestedObject: schema.NestedAttributeObject{Attributes: attrs}}
}

func addConnectionSchema(s *schema.Schema) {
	for _, field := range []struct {
		name, description string
		values            []string
	}{
		{"connection_purpose", "Native purpose. Custom connection creation defaults to tool and does not persist channel purpose; request native setup and import for channel connections.", []string{"tool", "channel"}},
		{"connection_kind", "Native connection kind; managed is the only native kind.", []string{"managed"}},
		{"ownership", "Native ownership, immutable after creation. Custom creation defaults to customer; omission retains an imported value.", []string{"platform_shared", "platform_provisioned", "customer", "dcr"}},
		{"status", "Native administrative status, defaulting to draft at creation. Omission retains its current value. Destroy archives the connection.", []string{"draft", "active", "disabled"}},
		{"credential_source", "Native credential source. Custom creation uses paperclip_vault. Other sources require native setup and import; source transitions are rejected.", []string{"paperclip_vault", "vercel_connect"}},
	} {
		attr := schema.StringAttribute{Description: field.description, Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, Validators: []validator.String{nativeEnumValidator{values: field.values, archiveViaDestroy: field.name == "status"}}}
		s.Attributes[field.name] = attr
	}
	s.Attributes["enabled"] = schema.BoolAttribute{Description: "Whether the native connection is enabled. Native creation defaults to false; omission retains its current value. Local stdio activation follows the target server's deployment and trusted-runtime policy.", Optional: true, Computed: true, PlanModifiers: []planmodifier.Bool{boolUseStateForUnknownModifier{}}}
	s.Attributes["transport_config"] = schema.StringAttribute{Description: "Native transport configuration JSON object. Omission retains the native value; use {} to clear it. Sensitive keys are rejected recursively.", Optional: true, Computed: true, Validators: []validator.String{nativeTransportConfigValidator{}}, PlanModifiers: []planmodifier.String{SemanticJSONPlanModifier(), stringplanmodifier.UseStateForUnknown()}}
	s.Attributes["credential_secret_refs"] = toolSecretRefsAttribute()
}

var nativeSensitiveConfigKey = regexp.MustCompile(`(?i)^(access[-_]?key([-_]?id)?|api[-_]?key|authorization|bearer|client[-_]?secret|credential|credentials|jwt|password|passwd|private[-_]?key|refresh[-_]?token|secret|secret[-_]?access[-_]?key|secret[-_]?key|session[-_]?token|token)$`)

type nativeTransportConfigValidator struct{}

func (nativeTransportConfigValidator) Description(context.Context) string {
	return "A JSON object without native sensitive configuration keys."
}
func (v nativeTransportConfigValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (nativeTransportConfigValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if err := validateTransportConfig(req.ConfigValue); err != nil {
		resp.Diagnostics.AddError("Invalid connection configuration", err.Error())
	}
}

func unsafeTransportConfig(value interface{}) bool {
	switch obj := value.(type) {
	case map[string]interface{}:
		for key, nested := range obj {
			if nativeSensitiveConfigKey.MatchString(key) || unsafeTransportConfig(nested) {
				return true
			}
		}
	case []interface{}:
		for _, nested := range obj {
			if unsafeTransportConfig(nested) {
				return true
			}
		}
	}
	return false
}

func validateTransportConfig(raw types.String) error {
	if raw.IsNull() || raw.IsUnknown() {
		return nil
	}
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(raw.ValueString()), &obj); err != nil || obj == nil {
		return fmt.Errorf("connection configuration must be a JSON object")
	}
	if unsafeTransportConfig(obj) {
		return fmt.Errorf("connection configuration cannot persist sensitive keys; use credential_secret_refs instead")
	}
	return nil
}

func validateRequiredConnectionConfig(plan PaperclipConnectionResourceModel) error {
	if plan.Config.IsUnknown() || plan.Transport.IsUnknown() || plan.GalleryKey.ValueString() != "" {
		return nil
	}
	var config map[string]interface{}
	if !plan.Config.IsNull() {
		json.Unmarshal([]byte(plan.Config.ValueString()), &config)
	}
	if plan.Transport.ValueString() == "local_stdio" {
		if id, ok := config["templateId"].(string); !ok || strings.TrimSpace(id) == "" {
			return fmt.Errorf("native local_stdio connections require config.templateId referencing an approved template; retaining this field is required and clearing it is unsupported")
		}
	}
	if plan.Transport.ValueString() == "mcp_remote" {
		var endpoint interface{}
		for _, key := range []string{"url", "endpoint", "remoteUrl"} {
			if config[key] != nil {
				endpoint = config[key]
				break
			}
		}
		value, _ := endpoint.(string)
		parsed, err := url.Parse(value)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return fmt.Errorf("native mcp_remote connections require an HTTP(S) config.url, config.endpoint or config.remoteUrl; retain the endpoint instead of clearing config")
		}
		if parsed.User != nil {
			return fmt.Errorf("connection URLs cannot contain credential userinfo; use protected references")
		}
		for key := range parsed.Query() {
			if nativeSensitiveConfigKey.MatchString(key) {
				return fmt.Errorf("connection URLs cannot contain credential query parameters; use protected query references")
			}
		}
	}
	return nil
}

func secretRefType() types.ObjectType {
	return toolSecretRefsAttribute().GetType().(types.MapType).ElemType.(types.ObjectType)
}

func sortedToolRefPaths(refs map[string]toolSecretRef) []string {
	keys := make([]string, 0, len(refs))
	for key := range refs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func validateToolRefs(ctx context.Context, c *client.Client, companyID string, value types.Map) diag.Diagnostics {
	var diags diag.Diagnostics
	if value.IsNull() || value.IsUnknown() {
		return diags
	}
	var refs map[string]toolSecretRef
	diags.Append(value.ElementsAs(ctx, &refs, false)...)
	if diags.HasError() {
		return diags
	}
	for configPath, ref := range refs {
		p := path.Root("credential_secret_refs").AtMapKey(configPath)
		binding := EnvBindingModel{Type: types.StringValue("secret_ref"), SecretID: ref.SecretID, Version: ref.Version, ProjectionClass: ref.ProjectionClass, ProjectionAllowlistKey: ref.ProjectionAllowlistKey}
		diags.Append(ValidateEnvBindingPlan(p, binding, "tool_connection", configPath)...)
		if configPath == "" {
			diags.AddAttributeError(p, "Invalid credential path", "configPath must not be empty")
		}
		if !ref.Version.IsNull() && !ref.Version.IsUnknown() && ref.Version.ValueString() != "latest" {
			version, err := strconv.Atoi(ref.Version.ValueString())
			if err != nil || version < 1 {
				diags.AddAttributeError(p.AtName("version"), "Invalid secret version", "Use latest or a positive integer version string")
			}
		}
		if !diags.HasError() {
			diags.Append(ValidateEnvBindingsApply(ctx, c, companyID, map[string]EnvBindingModel{configPath: binding})...)
		}
	}
	return diags
}

func toolRefsToAPI(ctx context.Context, value types.Map) []map[string]interface{} {
	refs := map[string]toolSecretRef{}
	if !value.IsNull() {
		value.ElementsAs(ctx, &refs, false)
	}
	result := []map[string]interface{}{}
	// Sorting fixes the native array's order across plans and grant rotation.
	for _, configPath := range sortedToolRefPaths(refs) {
		ref := refs[configPath]
		item := map[string]interface{}{"configPath": configPath, "secretId": ref.SecretID.ValueString()}
		if !ref.Version.IsNull() {
			version := ref.Version.ValueString()
			if version == "latest" {
				item["versionSelector"] = version
			} else {
				number, _ := strconv.Atoi(version)
				item["versionSelector"] = float64(number)
			}
		}
		for key, val := range map[string]types.String{"projectionClass": ref.ProjectionClass, "projectionAllowlistKey": ref.ProjectionAllowlistKey, "label": ref.Label, "keyScope": ref.KeyScope, "expiresAt": ref.ExpiresAt} {
			if !val.IsNull() {
				item[key] = val.ValueString()
			}
		}
		if !ref.Required.IsNull() {
			item["required"] = ref.Required.ValueBool()
		}
		result = append(result, item)
	}
	return result
}

func applyConnectionFields(ctx context.Context, plan PaperclipConnectionResourceModel, conn *client.PaperclipToolConnection) {
	conn.ConnectionPurpose, conn.ConnectionKind, conn.Ownership = plan.ConnectionPurpose.ValueString(), plan.ConnectionKind.ValueString(), plan.Ownership.ValueString()
	conn.Status = plan.Status.ValueString()
	if !plan.Enabled.IsNull() {
		enabled := plan.Enabled.ValueBool()
		conn.Enabled = &enabled
	}
	conn.TransportConfig = map[string]interface{}{}
	if !plan.TransportConfig.IsNull() {
		json.Unmarshal([]byte(plan.TransportConfig.ValueString()), &conn.TransportConfig)
	}
	conn.CredentialSecretRefs = toolRefsToAPI(ctx, plan.CredentialSecretRefs)
}

func refreshConnectionFields(ctx context.Context, state *PaperclipConnectionResourceModel, conn *client.PaperclipToolConnection) {
	for key, remote := range map[*types.String]string{&state.ConnectionPurpose: conn.ConnectionPurpose, &state.ConnectionKind: conn.ConnectionKind, &state.Ownership: conn.Ownership, &state.CredentialSource: conn.CredentialSource, &state.Status: conn.Status} {
		if remote != "" {
			*key = types.StringValue(remote)
		}
	}
	if conn.Enabled != nil {
		state.Enabled = types.BoolValue(*conn.Enabled)
	}
	state.TransportConfig = refreshSemanticJSON(state.TransportConfig, conn.TransportConfig)
	refs := map[string]toolSecretRef{}
	for _, raw := range conn.CredentialSecretRefs {
		ref := toolSecretRef{SecretID: types.StringValue(fmt.Sprint(raw["secretId"])), Version: types.StringNull(), ProjectionClass: types.StringNull(), ProjectionAllowlistKey: types.StringNull(), Required: types.BoolNull(), Label: types.StringNull(), KeyScope: types.StringNull(), ExpiresAt: types.StringNull()}
		if version, exists := raw["versionSelector"]; exists {
			ref.Version = types.StringValue(fmt.Sprint(version))
		}
		for key, field := range map[string]*types.String{"projectionClass": &ref.ProjectionClass, "projectionAllowlistKey": &ref.ProjectionAllowlistKey, "label": &ref.Label, "keyScope": &ref.KeyScope, "expiresAt": &ref.ExpiresAt} {
			if value, ok := raw[key].(string); ok {
				*field = types.StringValue(value)
			}
		}
		if required, ok := raw["required"].(bool); ok {
			ref.Required = types.BoolValue(required)
		}
		refs[fmt.Sprint(raw["configPath"])] = ref
	}
	if len(refs) == 0 && state.CredentialSecretRefs.IsNull() {
		return
	}
	state.CredentialSecretRefs, _ = types.MapValueFrom(ctx, secretRefType(), refs)
}

func (r *PaperclipConnectionResource) validateConnection(ctx context.Context, plan, state PaperclipConnectionResourceModel, allowPlannedTemplates bool) diag.Diagnostics {
	var diags diag.Diagnostics
	if err := rejectDeclaredArchive(plan.Status); err != nil {
		diags.AddError("Archive via resource destruction", err.Error())
	}
	refs, err := validateCredentialProjection(plan.CredentialRefs)
	if err != nil {
		diags.AddError("Invalid credential references", err.Error())
	}
	for _, ref := range refs {
		id, _ := ref["secretId"].(string)
		diags.Append(ValidateEnvBindingsApply(ctx, r.client, plan.CompanyID.ValueString(), map[string]EnvBindingModel{"credential_refs": {Type: types.StringValue("secret_ref"), SecretID: types.StringValue(id)}})...)
	}
	if r.client != nil && len(refs) > 0 && state.ID.ValueString() != "" {
		if err := r.client.ClaimConnectionAggregate("grants", state.ID.ValueString(), "connection:"+state.ID.ValueString()); err != nil {
			diags.AddError("Connection grant ownership conflict", err.Error())
		}
	}
	for key, raw := range map[string]types.String{"config": plan.Config, "transport_config": plan.TransportConfig} {
		if err := validateTransportConfig(raw); err != nil {
			diags.AddError("Invalid connection configuration", key+": "+err.Error())
		}
	}
	if err := validateRequiredConnectionConfig(plan); err != nil {
		diags.AddError("Unsupported connection configuration", err.Error())
	}
	if !diags.HasError() && r.client != nil && plan.CompanyID.ValueString() != "" && plan.GalleryKey.ValueString() == "" {
		if state.ID.IsNull() && plan.AppID.ValueString() != "" {
			if _, err := r.client.GetToolApp(ctx, plan.CompanyID.ValueString(), plan.AppID.ValueString()); err != nil {
				diags.AddError("Invalid connection application", err.Error())
			}
		}
		if plan.Transport.ValueString() == "local_stdio" && !plan.Config.IsUnknown() {
			var config map[string]interface{}
			json.Unmarshal([]byte(plan.Config.ValueString()), &config)
			if id, ok := config["templateId"].(string); ok {
				if !allowPlannedTemplates || !r.client.HasPlannedStdioTemplate(plan.CompanyID.ValueString(), id) {
					if _, err := r.client.GetStdioTemplate(ctx, plan.CompanyID.ValueString(), id); err != nil {
						diags.AddError("Invalid approved stdio template", err.Error())
					}
				}
			}
		}
	}
	if state.ID.IsNull() && plan.ConnectionPurpose.ValueString() == "channel" {
		diags.AddError("Unsupported connection purpose", "The native custom create service does not persist channel purpose; use native setup and import an existing channel connection. No mutation was sent.")
	}
	if state.ID.IsNull() && plan.CredentialSource.ValueString() == "vercel_connect" {
		diags.AddError("Unsupported credential source setup", "vercel_connect requires native interactive setup and import; custom creation cannot select this source. No mutation was sent.")
	}
	if !state.ID.IsNull() {
		for _, field := range []struct {
			name           string
			planned, prior types.String
		}{
			{"transport", plan.Transport, state.Transport}, {"auth_kind", plan.AuthKind, state.AuthKind}, {"connection_purpose", plan.ConnectionPurpose, state.ConnectionPurpose}, {"connection_kind", plan.ConnectionKind, state.ConnectionKind}, {"ownership", plan.Ownership, state.Ownership}, {"credential_source", plan.CredentialSource, state.CredentialSource},
			{"app_id", plan.AppID, state.AppID}, {"gallery_key", plan.GalleryKey, state.GalleryKey},
		} {
			if !field.prior.IsNull() && !field.prior.IsUnknown() && !field.planned.IsUnknown() && !field.planned.Equal(field.prior) {
				diags.AddAttributeError(path.Root(field.name), "Unsupported connection transition", "The native update API cannot change "+field.name+"; retain its current value. No mutation or replacement was performed.")
			}
		}
	}
	diags.Append(validateToolRefs(ctx, r.client, plan.CompanyID.ValueString(), plan.CredentialSecretRefs)...)
	return diags
}
