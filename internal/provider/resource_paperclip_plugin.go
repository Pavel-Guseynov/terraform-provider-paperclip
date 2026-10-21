package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type boolRequiresReplaceModifier struct{}

func (boolRequiresReplaceModifier) Description(context.Context) string {
	return "Requires replacement if changed"
}

func (m boolRequiresReplaceModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (boolRequiresReplaceModifier) PlanModifyBool(ctx context.Context, req planmodifier.BoolRequest, resp *planmodifier.BoolResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	if !req.StateValue.Equal(req.PlanValue) {
		resp.RequiresReplace = true
	}
}

var (
	_ resource.Resource                = &PaperclipPluginResource{}
	_ resource.ResourceWithConfigure   = &PaperclipPluginResource{}
	_ resource.ResourceWithImportState = &PaperclipPluginResource{}
)

func NewPaperclipPluginResource() resource.Resource {
	return &PaperclipPluginResource{}
}

type PaperclipPluginResource struct {
	client *client.Client
}

type PaperclipPluginResourceModel struct {
	ID             types.String `tfsdk:"id"`
	PackageName    types.String `tfsdk:"package_name"`
	Version        types.String `tfsdk:"version"`
	IsLocalPath    types.Bool   `tfsdk:"is_local_path"`
	Enabled        types.Bool   `tfsdk:"enabled"`
	PurgeOnDestroy types.Bool   `tfsdk:"purge_on_destroy"`
	PluginKey      types.String `tfsdk:"plugin_key"`
	Status         types.String `tfsdk:"status"`
	UpgradePending types.Bool   `tfsdk:"upgrade_pending"`
	APIVersion     types.Int64  `tfsdk:"api_version"`
	LastError      types.String `tfsdk:"last_error"`
}

func (r *PaperclipPluginResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_plugin"
}

func (r *PaperclipPluginResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages installation and lifecycle of Paperclip plugins.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Plugin UUID identifier.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"package_name": schema.StringAttribute{
				Description: "Install source: npm package name (e.g. @paperclip/plugin-linear) or local filesystem path. Local source changes use an in-place native upgrade, retaining the plugin ID and configuration. Import restores the API's installed packagePath.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplaceIf(func(ctx context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
						var local types.Bool
						resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("is_local_path"), &local)...)
						resp.RequiresReplace = !local.ValueBool()
					}, "npm package changes require replacement", "npm package changes require replacement"),
				},
			},
			"version": schema.StringAttribute{
				Description: "Plugin semver version. Changing this triggers an in-place upgrade.",
				Optional:    true,
				Computed:    true,
			},
			"is_local_path": schema.BoolAttribute{
				Description: "Whether package_name is a local filesystem path.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
				PlanModifiers: []planmodifier.Bool{
					boolRequiresReplaceModifier{},
				},
			},
			"enabled": schema.BoolAttribute{
				Description: "Whether the plugin is enabled and in ready status. Defaults to true.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
			},
			"purge_on_destroy": schema.BoolAttribute{
				Description: "Whether to permanently purge all plugin data upon uninstallation (defaults to false).",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
			},
			"plugin_key": schema.StringAttribute{
				Description: "Unique key derived from the plugin manifest id.",
				Computed:    true,
			},
			"status": schema.StringAttribute{
				Description: "Current lifecycle status (ready, installed, disabled, upgrade_pending, error, uninstalled).",
				Computed:    true,
			},
			"upgrade_pending": schema.BoolAttribute{
				Description: "True if the plugin is in upgrade_pending status awaiting capability approval.",
				Computed:    true,
			},
			"api_version": schema.Int64Attribute{
				Description: "Plugin API version declared in the manifest.",
				Computed:    true,
			},
			"last_error": schema.StringAttribute{
				Description: "Most recent error message or disable reason.",
				Computed:    true,
			},
		},
	}
}

func (r *PaperclipPluginResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *PaperclipPluginResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PaperclipPluginResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	input := client.PaperclipPluginInstallInput{
		PackageName: plan.PackageName.ValueString(),
		IsLocalPath: plan.IsLocalPath.ValueBool(),
	}
	if !plan.Version.IsNull() && !plan.Version.IsUnknown() && plan.Version.ValueString() != "" {
		input.Version = plan.Version.ValueString()
	}

	created, err := r.client.InstallPlugin(ctx, input)
	if err != nil {
		resp.Diagnostics.AddError("Error installing Paperclip plugin", err.Error())
		return
	}

	if !plan.Enabled.IsNull() && !plan.Enabled.IsUnknown() && !plan.Enabled.ValueBool() {
		disabled, err := r.client.DisablePlugin(ctx, created.ID, "Disabled by OpenTofu")
		if err != nil {
			resp.Diagnostics.AddError("Error disabling Paperclip plugin after install", err.Error())
			return
		}
		created = disabled
	}

	plan.ID = types.StringValue(created.ID)
	plan.PluginKey = types.StringValue(created.PluginKey)
	plan.Version = types.StringValue(created.Version)
	plan.IsLocalPath = types.BoolValue(plan.IsLocalPath.ValueBool())
	plan.Enabled = types.BoolValue(created.Status != "disabled")
	plan.Status = types.StringValue(created.Status)
	plan.UpgradePending = types.BoolValue(created.Status == "upgrade_pending")
	plan.APIVersion = types.Int64Value(created.APIVersion)
	plan.LastError = stringPointerOrNull(created.LastError)
	if plan.PurgeOnDestroy.IsNull() || plan.PurgeOnDestroy.IsUnknown() {
		plan.PurgeOnDestroy = types.BoolValue(false)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipPluginResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PaperclipPluginResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	plugin, err := r.client.GetPlugin(ctx, state.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Paperclip plugin", err.Error())
		return
	}

	state.ID = types.StringValue(plugin.ID)
	state.PluginKey = types.StringValue(plugin.PluginKey)
	state.Version = types.StringValue(plugin.Version)
	state.IsLocalPath = types.BoolValue(plugin.PackagePath != nil && *plugin.PackagePath != "")
	if state.IsLocalPath.ValueBool() {
		if state.PackageName.IsNull() {
			state.PackageName = types.StringValue(*plugin.PackagePath)
		}
	} else {
		state.PackageName = types.StringValue(plugin.PackageName)
	}
	state.Enabled = types.BoolValue(plugin.Status != "disabled")
	state.Status = types.StringValue(plugin.Status)
	state.UpgradePending = types.BoolValue(plugin.Status == "upgrade_pending")
	state.APIVersion = types.Int64Value(plugin.APIVersion)
	state.LastError = stringPointerOrNull(plugin.LastError)
	if state.PurgeOnDestroy.IsNull() || state.PurgeOnDestroy.IsUnknown() {
		state.PurgeOnDestroy = types.BoolValue(false)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PaperclipPluginResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state PaperclipPluginResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	pluginID := state.ID.ValueString()
	upgrade := client.PaperclipPluginUpgradeInput{}
	if plan.IsLocalPath.ValueBool() && !plan.PackageName.Equal(state.PackageName) {
		if plan.PackageName.ValueString() == "" {
			resp.Diagnostics.AddError("Invalid local plugin source", "Local package_name must not be empty; the prior provider state is preserved.")
			return
		}
		upgrade.PackagePath = plan.PackageName.ValueString()
	}

	if !plan.Version.IsNull() && !plan.Version.IsUnknown() && plan.Version.ValueString() != "" &&
		(!state.Version.IsNull() && plan.Version.ValueString() != state.Version.ValueString()) {
		upgrade.Version = plan.Version.ValueString()
	}
	if upgrade.Version != "" || upgrade.PackagePath != "" {
		if _, err := r.client.UpgradePlugin(ctx, pluginID, upgrade); err != nil {
			resp.Diagnostics.AddError("Error upgrading Paperclip plugin", err.Error())
			return
		}
	}

	if !plan.Enabled.IsNull() && !plan.Enabled.IsUnknown() &&
		(!state.Enabled.IsNull() && plan.Enabled.ValueBool() != state.Enabled.ValueBool()) {
		if plan.Enabled.ValueBool() {
			if _, err := r.client.EnablePlugin(ctx, pluginID); err != nil {
				resp.Diagnostics.AddError("Error enabling Paperclip plugin", err.Error())
				return
			}
		} else {
			if _, err := r.client.DisablePlugin(ctx, pluginID, "Disabled by OpenTofu"); err != nil {
				resp.Diagnostics.AddError("Error disabling Paperclip plugin", err.Error())
				return
			}
		}
	}

	current, err := r.client.GetPlugin(ctx, pluginID)
	if err != nil {
		resp.Diagnostics.AddError("Error fetching updated Paperclip plugin", err.Error())
		return
	}
	if upgrade.PackagePath != "" && (current.PackagePath == nil || *current.PackagePath != upgrade.PackagePath) {
		resp.Diagnostics.AddError("Native plugin source recovery was not retained", "The plugin API did not retain the requested packagePath. Native plugin source recovery support is required; the prior provider state is preserved.")
		return
	}

	plan.ID = types.StringValue(current.ID)
	plan.PluginKey = types.StringValue(current.PluginKey)
	plan.Version = types.StringValue(current.Version)
	plan.IsLocalPath = types.BoolValue(plan.IsLocalPath.ValueBool())
	plan.Enabled = types.BoolValue(current.Status != "disabled")
	plan.Status = types.StringValue(current.Status)
	plan.UpgradePending = types.BoolValue(current.Status == "upgrade_pending")
	plan.APIVersion = types.Int64Value(current.APIVersion)
	plan.LastError = stringPointerOrNull(current.LastError)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipPluginResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PaperclipPluginResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	purge := false
	if !state.PurgeOnDestroy.IsNull() && !state.PurgeOnDestroy.IsUnknown() {
		purge = state.PurgeOnDestroy.ValueBool()
	}

	_, err := r.client.UninstallPlugin(ctx, state.ID.ValueString(), purge)
	if err != nil && !errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Error uninstalling Paperclip plugin", err.Error())
		return
	}
}

func (r *PaperclipPluginResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
