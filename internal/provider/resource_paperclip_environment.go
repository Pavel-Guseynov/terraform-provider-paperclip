package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type PaperclipEnvironmentResource struct{ client *client.Client }

var environmentDriverKey = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
var environmentSecretID = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)
var environmentVariableKey = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

type PaperclipEnvironmentModel struct {
	ID          types.String               `tfsdk:"id"`
	CompanyID   types.String               `tfsdk:"company_id"`
	Name        types.String               `tfsdk:"name"`
	Description types.String               `tfsdk:"description"`
	Driver      types.String               `tfsdk:"driver"`
	Status      types.String               `tfsdk:"status"`
	Config      types.String               `tfsdk:"config"`
	Env         map[string]EnvBindingModel `tfsdk:"env"`
	Metadata    types.String               `tfsdk:"metadata"`
}

func NewPaperclipEnvironmentResource() resource.Resource { return &PaperclipEnvironmentResource{} }

func (r *PaperclipEnvironmentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_environment"
}

func (r *PaperclipEnvironmentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages an instance-scoped execution environment through the native API. Import Local; never create or replace its singleton. Same-driver config keys cannot be removed: such plans fail before writes. Native delete blocks are errors. Use protected secret references, never credentials in config. Transfer Local ownership by forgetting the old PATH binding without destruction and importing this resource; retain the old binding until the consumer cutover is ready.",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}, Description: "Native environment ID, preserved across updates and imports."},
			"company_id":  schema.StringAttribute{Required: true, Description: "Secret-resolution context only; environments have no company ownership. Import as company_id/environment_id."},
			"name":        schema.StringAttribute{Required: true, Description: "Native environment name."},
			"description": schema.StringAttribute{Optional: true, Description: "Description; omission clears it to null. Empty string is preserved."},
			"driver":      schema.StringAttribute{Required: true, Description: "local, ssh, sandbox or plugin. Local identity cannot change driver."},
			"status":      schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("active"), Description: "active (default) or archived."},
			"config":      schema.StringAttribute{Required: true, Sensitive: true, PlanModifiers: []planmodifier.String{SemanticJSONPlanModifier()}, Description: "Native driver config JSON object (camelCase). SSH: host, username, absolute remoteWorkspacePath, optional port (22), strictHostKeyChecking (true), knownHosts and privateKeySecretRef; plaintext privateKey is rejected. Sandbox: installed provider key, optional reuseLease (false), timeoutMs, streamRunLogs, runnerLifecycleMode, runnerIdleTimeoutMs, archiveOnRelease and provider-specific fields. The fake provider cannot be saved. Plugin: pluginKey, driverKey, driverConfig. Local: native config object. Include normalized plugin defaults; the server owns plugin-specific validation. Removing an existing top-level key on the same driver is unsupported."},
			"env":         EnvBindingMapAttribute("Complete envVars map using shared plain, secret_ref and user_secret_ref bindings. Omission clears bindings; omit PATH to inherit the service PATH. Only one resource may own Local envVars."),
			"metadata":    schema.StringAttribute{Optional: true, PlanModifiers: []planmodifier.String{SemanticJSONPlanModifier()}, Description: "Metadata JSON object; omission clears it to null. Platform-owned fields remain subject to native restrictions."},
		},
	}
}

func (r *PaperclipEnvironmentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func environmentObject(raw string) (map[string]interface{}, error) {
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &obj); err != nil || obj == nil {
		return nil, fmt.Errorf("must be a JSON object")
	}
	return obj, nil
}

// Defaults here are the packaged native environment-config persistence schema.
// Plugin-owned fields are deliberately not defaulted or interpreted.
func environmentConfig(driver, raw string) (map[string]interface{}, error) {
	obj, err := environmentObject(raw)
	if err != nil {
		return nil, err
	}
	text := func(key string) string { value, _ := obj[key].(string); return value }
	requireText := func(key string) bool { return strings.TrimSpace(text(key)) != "" }
	switch driver {
	case "local":
	case "ssh":
		for key := range obj {
			if !strings.Contains("|host|port|username|remoteWorkspacePath|privateKey|privateKeySecretRef|knownHosts|strictHostKeyChecking|", "|"+key+"|") {
				return nil, fmt.Errorf("unknown SSH config field %q", key)
			}
		}
		if !requireText("host") || !requireText("username") || !strings.HasPrefix(text("remoteWorkspacePath"), "/") {
			return nil, fmt.Errorf("SSH requires host, username and an absolute remoteWorkspacePath")
		}
		for _, key := range []string{"host", "username", "remoteWorkspacePath"} {
			obj[key] = strings.TrimSpace(text(key))
		}
		if obj["privateKey"] != nil {
			return nil, fmt.Errorf("plaintext privateKey is unsupported; use privateKeySecretRef with type secret_ref and secretId")
		}
		for key, value := range map[string]interface{}{"port": float64(22), "privateKey": nil, "privateKeySecretRef": nil, "knownHosts": nil, "strictHostKeyChecking": true} {
			if _, ok := obj[key]; !ok {
				obj[key] = value
			}
		}
		port, ok := obj["port"].(float64)
		if !ok || port < 1 || port > 65535 || port != float64(int(port)) {
			return nil, fmt.Errorf("SSH port must be an integer from 1 to 65535")
		}
		if _, ok := obj["strictHostKeyChecking"].(bool); !ok {
			return nil, fmt.Errorf("strictHostKeyChecking must be a boolean")
		}
		if obj["knownHosts"] != nil {
			value, ok := obj["knownHosts"].(string)
			if !ok {
				return nil, fmt.Errorf("knownHosts must be a string or null")
			}
			obj["knownHosts"] = nil
			if strings.TrimSpace(value) != "" {
				obj["knownHosts"] = strings.TrimSpace(value)
			}
		}
		if ref := obj["privateKeySecretRef"]; ref != nil {
			binding, ok := ref.(map[string]interface{})
			if !ok || binding["type"] != "secret_ref" {
				return nil, fmt.Errorf("privateKeySecretRef must be a secret_ref object")
			}
			for key := range binding {
				if key != "type" && key != "secretId" && key != "version" {
					return nil, fmt.Errorf("unknown privateKeySecretRef field %q", key)
				}
			}
			secretID, _ := binding["secretId"].(string)
			if !environmentSecretID.MatchString(secretID) {
				return nil, fmt.Errorf("privateKeySecretRef.secretId must be a native secret UUID")
			}
			if _, ok := binding["version"]; !ok {
				binding["version"] = "latest"
			}
			if binding["version"] != "latest" {
				n, ok := binding["version"].(float64)
				if !ok || n < 1 || n != float64(int(n)) {
					return nil, fmt.Errorf("privateKeySecretRef.version must be latest or a positive integer")
				}
			}
		}
	case "sandbox":
		if !requireText("provider") || text("provider") == "fake" {
			return nil, fmt.Errorf("sandbox requires an installed provider key; fake is reserved for internal probes and cannot be saved")
		}
		if !environmentDriverKey.MatchString(text("provider")) {
			return nil, fmt.Errorf("invalid sandbox provider key")
		}
		if _, ok := obj["reuseLease"]; !ok {
			obj["reuseLease"] = false
		}
		for _, key := range []string{"reuseLease", "streamRunLogs", "archiveOnRelease"} {
			if value, exists := obj[key]; exists {
				if _, ok := value.(bool); !ok {
					return nil, fmt.Errorf("%s must be a boolean", key)
				}
			}
		}
		for key, minimum := range map[string]float64{"timeoutMs": 1, "runnerIdleTimeoutMs": 1000} {
			if value, exists := obj[key]; exists {
				n, ok := value.(float64)
				if !ok || n < minimum || n > 86400000 || n != float64(int(n)) {
					return nil, fmt.Errorf("%s must be an integer between %g and 86400000", key, minimum)
				}
			}
		}
		if value, exists := obj["runnerLifecycleMode"]; exists && value != "inherit" && value != "per_turn" && value != "warm" {
			return nil, fmt.Errorf("runnerLifecycleMode must be inherit, per_turn or warm")
		}
		if _, exists := obj["streamAgentSessionOutput"]; exists {
			return nil, fmt.Errorf("streamAgentSessionOutput is obsolete and is stripped by the native API; omit it")
		}
	case "plugin":
		for key := range obj {
			if key != "pluginKey" && key != "driverKey" && key != "driverConfig" {
				return nil, fmt.Errorf("unknown plugin config field %q", key)
			}
		}
		if !requireText("pluginKey") || !environmentDriverKey.MatchString(text("driverKey")) {
			return nil, fmt.Errorf("plugin requires pluginKey and a valid driverKey")
		}
		if _, exists := obj["driverConfig"]; !exists {
			obj["driverConfig"] = map[string]interface{}{}
		}
		if _, ok := obj["driverConfig"].(map[string]interface{}); !ok {
			return nil, fmt.Errorf("driverConfig must be an object")
		}
	default:
		return nil, fmt.Errorf("driver must be local, ssh, sandbox or plugin")
	}
	return obj, nil
}

func validateEnvironment(model PaperclipEnvironmentModel) diag.Diagnostics {
	var diags diag.Diagnostics
	if !model.Name.IsUnknown() && strings.TrimSpace(model.Name.ValueString()) == "" {
		diags.AddError("Invalid environment name", "name must not be empty")
	}
	if !model.CompanyID.IsUnknown() && strings.TrimSpace(model.CompanyID.ValueString()) == "" {
		diags.AddError("Missing secret context", "company_id is required as secret-resolution context, not environment ownership")
	}
	if !model.Status.IsUnknown() && !model.Status.IsNull() && model.Status.ValueString() != "active" && model.Status.ValueString() != "archived" {
		diags.AddError("Invalid environment status", "status must be active or archived")
	}
	if !model.Driver.IsUnknown() && !model.Config.IsUnknown() {
		if _, err := environmentConfig(model.Driver.ValueString(), model.Config.ValueString()); err != nil {
			diags.AddAttributeError(path.Root("config"), "Invalid environment config", err.Error())
		}
		diags.Append(ValidatePluginConfigJSONPlan(path.Root("config"), model.Config.ValueString())...)
	}
	if !model.Metadata.IsNull() && !model.Metadata.IsUnknown() {
		if _, err := environmentObject(model.Metadata.ValueString()); err != nil {
			diags.AddAttributeError(path.Root("metadata"), "Invalid environment metadata", err.Error())
		}
	}
	diags.Append(ValidateEnvBindingsMapPlan(path.Root("env"), model.Env, "environment")...)
	for key, binding := range model.Env {
		if !environmentVariableKey.MatchString(key) {
			diags.AddAttributeError(path.Root("env").AtMapKey(key), "Invalid environment variable name", "Use native environment variable names matching ^[A-Za-z_][A-Za-z0-9_]*$")
		}
		if binding.Type.ValueString() == "secret_ref" && !binding.SecretID.IsUnknown() && !environmentSecretID.MatchString(binding.SecretID.ValueString()) {
			diags.AddAttributeError(path.Root("env").AtMapKey(key), "Invalid Secret Reference", "secret_id must be a native secret UUID")
		}
		if !binding.Version.IsNull() && !binding.Version.IsUnknown() && binding.Version.ValueString() != "latest" {
			n, err := strconv.Atoi(binding.Version.ValueString())
			if err != nil || n < 1 {
				diags.AddAttributeError(path.Root("env").AtMapKey(key), "Invalid binding version", "version must be latest or a positive integer string")
			}
		}
	}
	return diags
}

func (r *PaperclipEnvironmentResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var model PaperclipEnvironmentModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(validateEnvironment(model)...)
	}
}

func environmentTransition(model PaperclipEnvironmentModel, remote *client.PaperclipEnvironment) error {
	if model.Driver.IsUnknown() || model.Config.IsUnknown() {
		return nil
	}
	if (remote.Driver == "local" || model.Driver.ValueString() == "local") && remote.Driver != model.Driver.ValueString() {
		return fmt.Errorf("Local identity cannot change driver; import the singleton Local environment separately")
	}
	if remote.Driver != model.Driver.ValueString() {
		return nil
	}
	config, _ := environmentConfig(model.Driver.ValueString(), model.Config.ValueString())
	var removed []string
	for key := range remote.Config {
		if _, ok := config[key]; !ok {
			removed = append(removed, key)
		}
	}
	if len(removed) > 0 {
		sort.Strings(removed)
		return fmt.Errorf("native same-driver PATCH merges config and cannot remove keys %q; retain these keys or use an independently supported native configuration operation, then refresh; no mutation was issued and identity is preserved", removed)
	}
	return nil
}

func (r *PaperclipEnvironmentResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if r.client == nil || req.Plan.Raw.IsNull() {
		return
	}
	var model PaperclipEnvironmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateEnvironment(model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.CheckEnvironmentCapability(ctx, model.CompanyID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Required capability missing", err.Error())
		return
	}
	if !model.CompanyID.IsUnknown() {
		resp.Diagnostics.Append(ValidateEnvBindingsApply(ctx, r.client, model.CompanyID.ValueString(), model.Env)...)
		if !model.Config.IsUnknown() {
			resp.Diagnostics.Append(ValidatePluginConfigJSONApply(ctx, r.client, model.CompanyID.ValueString(), model.Config.ValueString())...)
		}
	}
	if !model.ID.IsNull() && !model.ID.IsUnknown() {
		remote, err := r.client.GetExecutionEnvironment(ctx, model.ID.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Error checking environment transition", err.Error())
			return
		}
		if err := environmentTransition(model, remote); err != nil {
			resp.Diagnostics.AddError("Unsupported environment transition", err.Error())
		}
	}
}

func (r *PaperclipEnvironmentResource) save(ctx context.Context, model *PaperclipEnvironmentModel, diags *diag.Diagnostics) {
	diags.Append(validateEnvironment(*model)...)
	if diags.HasError() {
		return
	}
	config, _ := environmentConfig(model.Driver.ValueString(), model.Config.ValueString())
	id := model.ID.ValueString()
	if id == "" && model.Driver.ValueString() == "local" {
		diags.AddError("Import singleton Local", "Local is producer-owned; import company_id/environment_id instead of creating or replacing it")
		return
	}
	var remote *client.PaperclipEnvironment
	if id != "" {
		var err error
		remote, err = r.client.GetExecutionEnvironment(ctx, id)
		if err != nil {
			diags.AddError("Error reading environment before write", err.Error())
			return
		}
		if err := environmentTransition(*model, remote); err != nil {
			diags.AddError("Unsupported environment transition", err.Error())
			return
		}
	}
	diags.Append(ValidateEnvBindingsApply(ctx, r.client, model.CompanyID.ValueString(), model.Env)...)
	diags.Append(ValidatePluginConfigJSONApply(ctx, r.client, model.CompanyID.ValueString(), model.Config.ValueString())...)
	if diags.HasError() {
		return
	}
	var metadata interface{}
	if !model.Metadata.IsNull() {
		metadata, _ = environmentObject(model.Metadata.ValueString())
	}
	bindings := EnvBindingsMapToAPI(model.Env)
	for _, value := range bindings {
		binding := value.(map[string]interface{})
		if version, ok := binding["version"].(string); ok && version != "latest" {
			binding["version"], _ = strconv.Atoi(version)
		}
	}
	body := map[string]interface{}{"name": model.Name.ValueString(), "description": model.Description.ValueStringPointer(), "driver": model.Driver.ValueString(), "status": model.Status.ValueString(), "config": config, "envVars": bindings, "metadata": metadata}
	// Do not invoke native config validation/cleanup gates for unrelated edits.
	if remote != nil && remote.Driver == model.Driver.ValueString() && semanticJSONEqual(marshalEnvironmentJSON(config), marshalEnvironmentJSON(remote.Config)) {
		delete(body, "config")
	}
	updated, err := r.client.SaveExecutionEnvironment(ctx, model.CompanyID.ValueString(), id, body)
	if err != nil {
		diags.AddError("Error saving environment", err.Error())
		return
	}
	refreshEnvironment(model, updated)
}

func (r *PaperclipEnvironmentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var model PaperclipEnvironmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.save(ctx, &model, &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
	}
}

func (r *PaperclipEnvironmentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var model PaperclipEnvironmentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.save(ctx, &model, &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
	}
}

func (r *PaperclipEnvironmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var model PaperclipEnvironmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	remote, err := r.client.GetExecutionEnvironment(ctx, model.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
		} else {
			resp.Diagnostics.AddError("Error reading environment", err.Error())
		}
		return
	}
	refreshEnvironment(&model, remote)
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}

func (r *PaperclipEnvironmentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var model PaperclipEnvironmentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteExecutionEnvironment(ctx, model.ID.ValueString()); err != nil && !errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Native environment deletion blocked or failed", err.Error())
	}
}

func (r *PaperclipEnvironmentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		resp.Diagnostics.AddError("Invalid Environment Import ID", "Use company_id/environment_id; the company supplies secret context only")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("company_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
}

func marshalEnvironmentJSON(value interface{}) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func refreshEnvironment(model *PaperclipEnvironmentModel, remote *client.PaperclipEnvironment) {
	model.ID, model.Name, model.Driver, model.Status = types.StringValue(remote.ID), types.StringValue(remote.Name), types.StringValue(remote.Driver), types.StringValue(remote.Status)
	model.Description = types.StringPointerValue(remote.Description)
	config, err := environmentConfig(remote.Driver, model.Config.ValueString())
	if err != nil || !semanticJSONEqual(marshalEnvironmentJSON(config), marshalEnvironmentJSON(remote.Config)) {
		model.Config = types.StringValue(marshalEnvironmentJSON(remote.Config))
	}
	model.Metadata = refreshSemanticJSON(model.Metadata, remote.Metadata)
	if remote.Metadata != nil && model.Metadata.IsNull() {
		model.Metadata = types.StringValue(marshalEnvironmentJSON(remote.Metadata))
	}
	model.Env = refreshEnvBindings(model.Env, remote.EnvVars)
}

func refreshEnvBindings(previous map[string]EnvBindingModel, remote map[string]interface{}) map[string]EnvBindingModel {
	env := make(map[string]EnvBindingModel, len(remote))
	for key, value := range remote {
		if text, ok := value.(string); ok {
			value = map[string]interface{}{"type": "plain", "value": text}
		}
		obj, ok := value.(map[string]interface{})
		if !ok {
			continue
		}
		binding := envBindingFromJSON(obj)
		if version, ok := obj["version"].(float64); ok {
			binding.Version = types.StringValue(strconv.FormatFloat(version, 'f', -1, 64))
		}
		if prior, exists := previous[key]; exists {
			if prior.Version.IsNull() && binding.Version.ValueString() == "latest" {
				binding.Version = types.StringNull()
			}
			if prior.ProjectionClass.IsNull() && binding.ProjectionClass.ValueString() == "unclassified" {
				binding.ProjectionClass = types.StringNull()
			}
		}
		if v, ok := obj["required"].(bool); ok {
			binding.Required = types.BoolValue(v)
		}
		if v, ok := obj["allowMissingOverride"].(bool); ok {
			binding.AllowMissingOverride = types.BoolValue(v)
		}
		env[key] = binding
	}
	if len(env) == 0 && previous == nil {
		return nil
	}
	return env
}
