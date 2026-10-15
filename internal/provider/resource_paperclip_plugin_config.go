package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                   = &PaperclipPluginConfigResource{}
	_ resource.ResourceWithConfigure      = &PaperclipPluginConfigResource{}
	_ resource.ResourceWithImportState    = &PaperclipPluginConfigResource{}
	_ resource.ResourceWithValidateConfig = &PaperclipPluginConfigResource{}
	_ resource.ResourceWithModifyPlan     = &PaperclipPluginConfigResource{}
)

func NewPaperclipPluginConfigResource() resource.Resource {
	return &PaperclipPluginConfigResource{}
}

type PaperclipPluginConfigResource struct {
	client *client.Client
}

type PaperclipPluginConfigResourceModel struct {
	ID        types.String `tfsdk:"id"`
	CompanyID types.String `tfsdk:"company_id"`
	PluginID  types.String `tfsdk:"plugin_id"`
	Config    types.String `tfsdk:"config"`
	LastError types.String `tfsdk:"last_error"`
}

func (r *PaperclipPluginConfigResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_plugin_config"
}

func (r *PaperclipPluginConfigResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages company-scoped configuration for a Paperclip plugin. Destroy relinquishes management without clearing remote configuration; the API has no config deletion route.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Composite identifier in company_id/plugin_id format.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"company_id": schema.StringAttribute{
				Description: "Paperclip company ID owning this plugin configuration.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"plugin_id": schema.StringAttribute{
				Description: "Plugin UUID or plugin key being configured.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"config": schema.StringAttribute{
				Description: "Plugin configuration JSON object conforming to the plugin's instanceConfigSchema.",
				Required:    true,
				Validators: []validator.String{
					JSONAttributeValidator(),
				},
				PlanModifiers: []planmodifier.String{
					semanticJSONPlanModifier{normalize: normalizePluginConfigValue},
				},
			},
			"last_error": schema.StringAttribute{
				Description: "Most recent configuration validation error recorded by Paperclip.",
				Computed:    true,
			},
		},
	}
}

func (r *PaperclipPluginConfigResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *client.Client, got: %T", req.ProviderData))
		return
	}
	r.client = c
}

func (r *PaperclipPluginConfigResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config PaperclipPluginConfigResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !config.Config.IsNull() && !config.Config.IsUnknown() {
		resp.Diagnostics.Append(ValidatePluginConfigJSONPlan(path.Root("config"), config.Config.ValueString())...)
	}
}

func (r *PaperclipPluginConfigResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PaperclipPluginConfigResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(ValidatePluginConfigJSONPlan(path.Root("config"), plan.Config.ValueString())...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(ValidatePluginConfigJSONApply(ctx, r.client, plan.CompanyID.ValueString(), plan.Config.ValueString())...)
	if resp.Diagnostics.HasError() {
		return
	}

	configMap := NormalizePluginConfigForAPI(plan.Config.ValueString())

	created, err := r.client.UpsertPluginConfig(ctx, plan.PluginID.ValueString(), plan.CompanyID.ValueString(), configMap)
	if err != nil {
		if valErr, ok := client.ParsePluginConfigValidationError(err); ok {
			for _, fe := range valErr.FieldErrors {
				resp.Diagnostics.AddAttributeError(
					path.Root("config"),
					"Plugin Configuration Validation Error",
					fmt.Sprintf("Field %s: %s", fe.Field, fe.Message),
				)
			}
		}
		resp.Diagnostics.AddError("Error configuring Paperclip plugin", err.Error())
		return
	}

	plan.ID = types.StringValue(fmt.Sprintf("%s/%s", created.CompanyID, created.PluginID))
	plan.CompanyID = types.StringValue(created.CompanyID)
	plan.PluginID = types.StringValue(created.PluginID)
	plan.Config = refreshSemanticJSONNormalized(plan.Config, created.ConfigJSON, normalizePluginConfigValue)
	plan.LastError = stringPointerOrNull(created.LastError)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipPluginConfigResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || req.State.Raw.IsNull() {
		return
	}
	var plan, state PaperclipPluginConfigResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Attribute modifiers have already reconciled equivalent JSON. With no write
	// planned, preserve the refreshed error, including null after import.
	if plan.CompanyID.Equal(state.CompanyID) && plan.PluginID.Equal(state.PluginID) && plan.Config.Equal(state.Config) {
		resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("last_error"), state.LastError)...)
	}
}

func (r *PaperclipPluginConfigResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PaperclipPluginConfigResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	config, err := r.client.GetPluginConfig(ctx, state.PluginID.ValueString(), state.CompanyID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Paperclip plugin config", err.Error())
		return
	}

	state.ID = types.StringValue(fmt.Sprintf("%s/%s", config.CompanyID, config.PluginID))
	state.CompanyID = types.StringValue(config.CompanyID)
	state.PluginID = types.StringValue(config.PluginID)
	state.Config = refreshSemanticJSONNormalized(state.Config, config.ConfigJSON, normalizePluginConfigValue)
	state.LastError = stringPointerOrNull(config.LastError)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PaperclipPluginConfigResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan PaperclipPluginConfigResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(ValidatePluginConfigJSONPlan(path.Root("config"), plan.Config.ValueString())...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(ValidatePluginConfigJSONApply(ctx, r.client, plan.CompanyID.ValueString(), plan.Config.ValueString())...)
	if resp.Diagnostics.HasError() {
		return
	}

	configMap := NormalizePluginConfigForAPI(plan.Config.ValueString())

	updated, err := r.client.UpsertPluginConfig(ctx, plan.PluginID.ValueString(), plan.CompanyID.ValueString(), configMap)
	if err != nil {
		if valErr, ok := client.ParsePluginConfigValidationError(err); ok {
			for _, fe := range valErr.FieldErrors {
				resp.Diagnostics.AddAttributeError(
					path.Root("config"),
					"Plugin Configuration Validation Error",
					fmt.Sprintf("Field %s: %s", fe.Field, fe.Message),
				)
			}
		}
		resp.Diagnostics.AddError("Error configuring Paperclip plugin", err.Error())
		return
	}

	plan.ID = types.StringValue(fmt.Sprintf("%s/%s", updated.CompanyID, updated.PluginID))
	plan.CompanyID = types.StringValue(updated.CompanyID)
	plan.PluginID = types.StringValue(updated.PluginID)
	plan.Config = refreshSemanticJSONNormalized(plan.Config, updated.ConfigJSON, normalizePluginConfigValue)
	plan.LastError = stringPointerOrNull(updated.LastError)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipPluginConfigResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	// Relinquish management of this plugin config row; the server API provides no config deletion endpoint.
	_ = req
}

func (r *PaperclipPluginConfigResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError(
			"Invalid Plugin Config Import ID",
			fmt.Sprintf("Expected <company_id>/<plugin_id>, got %q", req.ID),
		)
		return
	}
	companyID := parts[0]
	pluginID := parts[1]
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("company_id"), companyID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("plugin_id"), pluginID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), fmt.Sprintf("%s/%s", companyID, pluginID))...)
}

// ---------------------------------------------------------------------------
// Plugin Config Validation and Normalization Helpers
// ---------------------------------------------------------------------------

func ValidatePluginConfigJSONPlan(p path.Path, rawJSON string) diag.Diagnostics {
	var diags diag.Diagnostics
	if rawJSON == "" || strings.TrimSpace(rawJSON) == "null" {
		return diags
	}

	var rawMap map[string]interface{}
	if err := json.Unmarshal([]byte(rawJSON), &rawMap); err != nil {
		diags.AddAttributeError(p, "Invalid JSON", fmt.Sprintf("Plugin configuration must be a valid JSON object: %s", err.Error()))
		return diags
	}

	var walk func(val interface{}, curPath path.Path)
	walk = func(val interface{}, curPath path.Path) {
		switch v := val.(type) {
		case string:
			if IsRedactedSentinel(v) {
				diags.AddAttributeError(curPath, "Invalid Plain Binding Value", "Invalid environment binding value: redacted sentinel \"[REDACTED]\" is prohibited during plan. Plain bindings must contain real configuration values, not redacted masking strings.")
			}
		case map[string]interface{}:
			bType, _ := v["type"].(string)
			switch bType {
			case "plain", "secret_ref", "user_secret_ref":
				diags.Append(ValidateEnvBindingPlan(curPath, envBindingFromJSON(v), "", "")...)
			default:
				for k, child := range v {
					walk(child, curPath.AtMapKey(k))
				}
			}
		case []interface{}:
			for i, elem := range v {
				walk(elem, curPath.AtListIndex(i))
			}
		}
	}

	for k, v := range rawMap {
		walk(v, p.AtMapKey(k))
	}

	return diags
}

func ValidatePluginConfigJSONApply(ctx context.Context, c *client.Client, companyID string, rawJSON string) diag.Diagnostics {
	var diags diag.Diagnostics
	if c == nil || companyID == "" || rawJSON == "" || strings.TrimSpace(rawJSON) == "null" {
		return diags
	}

	var rawMap map[string]interface{}
	if err := json.Unmarshal([]byte(rawJSON), &rawMap); err != nil {
		return diags
	}

	var walk func(val interface{}, curKey string)
	walk = func(val interface{}, curKey string) {
		switch v := val.(type) {
		case map[string]interface{}:
			if v["type"] == "secret_ref" {
				diags.Append(ValidateEnvBindingsApply(ctx, c, companyID, map[string]EnvBindingModel{
					curKey: envBindingFromJSON(v),
				})...)
				return
			}
			for k, child := range v {
				walk(child, k)
			}
		case []interface{}:
			for _, elem := range v {
				walk(elem, curKey)
			}
		}
	}

	for k, v := range rawMap {
		walk(v, k)
	}

	return diags
}

func NormalizePluginConfigForAPI(rawJSON string) map[string]interface{} {
	var rawMap map[string]interface{}
	if err := json.Unmarshal([]byte(rawJSON), &rawMap); err != nil {
		return make(map[string]interface{})
	}

	return normalizePluginConfigValue(rawMap).(map[string]interface{})
}

func normalizePluginConfigValue(val interface{}) interface{} {
	switch v := val.(type) {
	case map[string]interface{}:
		res := make(map[string]interface{}, len(v))
		for k, child := range v {
			res[k] = normalizePluginConfigValue(child)
		}
		if res["type"] == "secret_ref" || res["type"] == "user_secret_ref" || res["type"] == "plain" {
			return normalizeEnvBindingJSON(res)
		}
		return res
	case []interface{}:
		res := make([]interface{}, len(v))
		for i, elem := range v {
			res[i] = normalizePluginConfigValue(elem)
		}
		return res
	default:
		return val
	}
}
