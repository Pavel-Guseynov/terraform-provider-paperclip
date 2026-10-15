package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

var (
	_ resource.Resource                   = &PaperclipWorkspaceResource{}
	_ resource.ResourceWithConfigure      = &PaperclipWorkspaceResource{}
	_ resource.ResourceWithImportState    = &PaperclipWorkspaceResource{}
	_ resource.ResourceWithUpgradeState   = &PaperclipWorkspaceResource{}
	_ resource.ResourceWithValidateConfig = &PaperclipWorkspaceResource{}
	_ resource.ResourceWithModifyPlan     = &PaperclipWorkspaceResource{}
)

func NewPaperclipWorkspaceResource() resource.Resource {
	return &PaperclipWorkspaceResource{}
}

type PaperclipWorkspaceResource struct {
	client *client.Client
}

type PaperclipWorkspaceResourceModel struct {
	ID                 types.String `tfsdk:"id"`
	ProjectID          types.String `tfsdk:"project_id"`
	Name               types.String `tfsdk:"name"`
	RepoURL            types.String `tfsdk:"repo_url"`
	RepoRef            types.String `tfsdk:"repo_ref"`
	DefaultRef         types.String `tfsdk:"default_ref"`
	Cwd                types.String `tfsdk:"cwd"`
	Visibility         types.String `tfsdk:"visibility"`
	SetupCommand       types.String `tfsdk:"setup_command"`
	CleanupCommand     types.String `tfsdk:"cleanup_command"`
	RemoteProvider     types.String `tfsdk:"remote_provider"`
	RemoteWorkspaceRef types.String `tfsdk:"remote_workspace_ref"`
	SharedWorkspaceKey types.String `tfsdk:"shared_workspace_key"`
	Metadata           types.String `tfsdk:"metadata"`
	RuntimeConfig      types.String `tfsdk:"runtime_config"`
	IsPrimary          types.Bool   `tfsdk:"is_primary"`
	SourceType         types.String `tfsdk:"source_type"`
}

func workspaceRequest(projectID string, model PaperclipWorkspaceResourceModel) client.PaperclipWorkspace {
	metadata, _ := nativeJSONObject(model.Metadata)
	runtimeConfig, _ := nativeJSONObject(model.RuntimeConfig)
	return client.PaperclipWorkspace{
		ProjectID:  projectID,
		Name:       model.Name.ValueString(),
		RepoURL:    model.RepoURL.ValueString(),
		RepoRef:    model.RepoRef.ValueString(),
		DefaultRef: model.DefaultRef.ValueString(),
		Cwd:        model.Cwd.ValueString(), Visibility: model.Visibility.ValueString(),
		SetupCommand: model.SetupCommand.ValueString(), CleanupCommand: model.CleanupCommand.ValueString(),
		RemoteProvider: model.RemoteProvider.ValueString(), RemoteWorkspaceRef: model.RemoteWorkspaceRef.ValueString(),
		SharedWorkspaceKey: model.SharedWorkspaceKey.ValueString(), Metadata: metadata, RuntimeConfig: runtimeConfig,
		IsPrimary:  model.IsPrimary.ValueBool(),
		SourceType: model.SourceType.ValueString(),
	}
}

func applyWorkspaceState(model *PaperclipWorkspaceResourceModel, ws *client.PaperclipWorkspace) {
	model.ID = types.StringValue(ws.ID)
	if ws.ProjectID != "" {
		model.ProjectID = types.StringValue(ws.ProjectID)
	}
	model.Name = types.StringValue(ws.Name)
	model.RepoURL = stringOrNull(ws.RepoURL)
	model.RepoRef = stringOrNull(ws.RepoRef)
	model.DefaultRef = stringOrNull(ws.DefaultRef)
	model.Cwd = stringOrNull(ws.Cwd)
	model.Visibility = types.StringValue(ws.Visibility)
	model.SetupCommand = stringOrNull(ws.SetupCommand)
	model.CleanupCommand = stringOrNull(ws.CleanupCommand)
	model.RemoteProvider = stringOrNull(ws.RemoteProvider)
	model.RemoteWorkspaceRef = stringOrNull(ws.RemoteWorkspaceRef)
	model.SharedWorkspaceKey = stringOrNull(ws.SharedWorkspaceKey)
	metadata := make(map[string]interface{}, len(ws.Metadata))
	for k, v := range ws.Metadata {
		if k != "runtimeConfig" {
			metadata[k] = v
		}
	}
	if ws.Metadata == nil {
		metadata = nil
	}
	model.Metadata = refreshSemanticJSON(model.Metadata, metadata)
	model.RuntimeConfig = refreshSemanticJSONNormalized(model.RuntimeConfig, ws.RuntimeConfig, normalizeWorkspaceRuntime)
	model.IsPrimary = types.BoolValue(ws.IsPrimary)
	if ws.SourceType != "" {
		model.SourceType = types.StringValue(ws.SourceType)
	}
}

func (r *PaperclipWorkspaceResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_workspace"
}

func (r *PaperclipWorkspaceResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:     1,
		Description: "Manages all native project workspace source types. default_ref reads the effective native ref, including repo_ref inheritance; an independent null default_ref with non-null repo_ref is rejected. setup_command is stored but not executed by Paperclip; cleanup_command is executed. metadata excludes the runtimeConfig key owned by runtime_config.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Unique identifier for the workspace.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"project_id": schema.StringAttribute{
				Description: "The Paperclip project ID.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Description: "Workspace identifier name.",
				Required:    true,
			},
			"repo_url": schema.StringAttribute{
				Description: "Git repository URL for checkout.",
				Optional:    true,
			},
			"repo_ref":             schema.StringAttribute{Optional: true, Description: "Independent native repository ref; null clears it."},
			"default_ref":          schema.StringAttribute{Optional: true, Description: "Effective native default ref. When repo_ref is set, configure an explicit default_ref (the same value represents native inheritance); clearing only default_ref is unsupported and rejected before mutation."},
			"cwd":                  schema.StringAttribute{Optional: true, Description: "Local working directory; use a normalized absolute path."},
			"visibility":           schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("default"), Description: "Native visibility: default or advanced.", Validators: []validator.String{stringOneOfValidator{allowed: []string{"default", "advanced"}}}},
			"setup_command":        schema.StringAttribute{Optional: true, Description: "Persisted setup command. The current native API does not execute this command."},
			"cleanup_command":      schema.StringAttribute{Optional: true, Description: "Native cleanup command, executed when the runtime is cleaned up."},
			"remote_provider":      schema.StringAttribute{Optional: true, Description: "Remote workspace provider."},
			"remote_workspace_ref": schema.StringAttribute{Optional: true, Description: "Remote workspace reference."},
			"shared_workspace_key": schema.StringAttribute{Optional: true, Description: "Native shared workspace key."},
			"metadata":             schema.StringAttribute{Optional: true, Description: "Native metadata JSON object, excluding runtimeConfig; null clears it.", Validators: []validator.String{nativeObjectValidator{}}, PlanModifiers: []planmodifier.String{SemanticJSONPlanModifier()}},
			"runtime_config":       schema.StringAttribute{Optional: true, Description: "Native runtime JSON object with workspaceRuntime, desiredState and serviceStates. Null clears it; null members and absent members compare equivalently.", Validators: []validator.String{nativeObjectValidator{}}, PlanModifiers: []planmodifier.String{semanticJSONPlanModifier{normalize: normalizeWorkspaceRuntime}}},
			"is_primary": schema.BoolAttribute{
				Description: "Whether this workspace is the project's primary workspace.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
			},
			"source_type": schema.StringAttribute{
				Description: "Workspace source type (e.g. git_repo).",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("git_repo"),
				Validators:  []validator.String{stringOneOfValidator{allowed: []string{"local_path", "git_repo", "remote_managed", "non_git_path"}}},
			},
		},
	}
}

func normalizeWorkspaceRuntime(value interface{}) interface{} {
	obj, ok := value.(map[string]interface{})
	if !ok {
		return value
	}
	result := map[string]interface{}{}
	for k, v := range obj {
		if v != nil {
			result[k] = v
		}
	}
	return result
}

func validateWorkspaceConfig(model PaperclipWorkspaceResourceModel) error {
	if model.Cwd.ValueString() == "/__paperclip_repo_only__" {
		return fmt.Errorf("cwd /__paperclip_repo_only__ is a native sentinel that reads as null; use null")
	}
	for name, value := range map[string]types.String{"name": model.Name, "cwd": model.Cwd, "repo_url": model.RepoURL, "repo_ref": model.RepoRef, "default_ref": model.DefaultRef, "setup_command": model.SetupCommand, "cleanup_command": model.CleanupCommand, "remote_provider": model.RemoteProvider, "remote_workspace_ref": model.RemoteWorkspaceRef, "shared_workspace_key": model.SharedWorkspaceKey} {
		if !value.IsNull() && !value.IsUnknown() && strings.TrimSpace(value.ValueString()) == "" {
			return fmt.Errorf("%s cannot be empty or whitespace: use null to clear a nullable field", name)
		}
		if !value.IsNull() && !value.IsUnknown() && strings.TrimSpace(value.ValueString()) != value.ValueString() {
			return fmt.Errorf("%s cannot have leading or trailing whitespace: Paperclip trims this field", name)
		}
	}
	if !model.RepoURL.IsNull() && !model.RepoURL.IsUnknown() {
		u, err := url.Parse(model.RepoURL.ValueString())
		if err != nil || u.Scheme == "" || ((u.Scheme == "http" || u.Scheme == "https" || u.Scheme == "ftp") && u.Host == "") {
			return fmt.Errorf("repo_url must be an absolute repository URL")
		}
	}
	if !model.RepoRef.IsUnknown() && !model.DefaultRef.IsUnknown() && model.RepoRef.ValueString() != "" && model.DefaultRef.IsNull() {
		return fmt.Errorf("default_ref cannot be independently null while repo_ref is set: the Paperclip API reads null as repo_ref. Set default_ref to the effective inherited ref or an explicit distinct ref, or clear both refs")
	}
	if !model.SourceType.IsUnknown() && !model.Cwd.IsUnknown() && !model.RepoURL.IsUnknown() && !model.RemoteWorkspaceRef.IsUnknown() {
		if model.SourceType.ValueString() == "remote_managed" {
			if model.RemoteWorkspaceRef.ValueString() == "" && model.RepoURL.ValueString() == "" {
				return fmt.Errorf("remote_managed requires remote_workspace_ref or repo_url")
			}
		} else if model.Cwd.ValueString() == "" && model.RepoURL.ValueString() == "" {
			return fmt.Errorf("workspace requires cwd or repo_url")
		}
	}
	metadata, err := nativeJSONObject(model.Metadata)
	if err != nil {
		return fmt.Errorf("metadata: %w", err)
	}
	if _, exists := metadata["runtimeConfig"]; exists {
		return fmt.Errorf("metadata.runtimeConfig is owned by runtime_config; configure it there")
	}
	runtimeConfig, err := nativeJSONObject(model.RuntimeConfig)
	if err != nil {
		return fmt.Errorf("runtime_config: %w", err)
	}
	for key, value := range runtimeConfig {
		if key != "workspaceRuntime" && key != "desiredState" && key != "serviceStates" {
			return fmt.Errorf("unsupported runtime_config key %q", key)
		}
		if value == nil {
			continue
		}
		switch key {
		case "workspaceRuntime":
			if _, ok := value.(map[string]interface{}); !ok {
				return fmt.Errorf("runtime_config.workspaceRuntime must be an object or null")
			}
		case "desiredState":
			if value != "running" && value != "stopped" && value != "manual" {
				return fmt.Errorf("runtime_config.desiredState must be running, stopped or manual")
			}
		case "serviceStates":
			states, ok := value.(map[string]interface{})
			if !ok {
				return fmt.Errorf("runtime_config.serviceStates must be an object")
			}
			if len(states) == 0 {
				return fmt.Errorf("runtime_config.serviceStates cannot be empty: the native API reads an empty map as null; use null")
			}
			for _, state := range states {
				if state != "running" && state != "stopped" && state != "manual" {
					return fmt.Errorf("runtime_config.serviceStates values must be running, stopped or manual")
				}
			}
		}
	}
	if runtimeConfig != nil && len(normalizeWorkspaceRuntime(runtimeConfig).(map[string]interface{})) == 0 {
		return fmt.Errorf("runtime_config with no non-null fields is read as null by Paperclip; use null")
	}
	return nil
}

func (r *PaperclipWorkspaceResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config PaperclipWorkspaceResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := validateWorkspaceConfig(config); err != nil {
		resp.Diagnostics.AddError("Unsupported Paperclip workspace configuration", err.Error())
	}
}

func (r *PaperclipWorkspaceResource) validateWrite(ctx context.Context, model PaperclipWorkspaceResourceModel, id string) error {
	if err := validateWorkspaceConfig(model); err != nil {
		return err
	}
	if !model.IsPrimary.ValueBool() {
		workspaces, err := r.client.ListWorkspaces(ctx, model.ProjectID.ValueString())
		if err != nil {
			return err
		}
		for _, ws := range workspaces {
			if ws.ID != id {
				return nil
			}
		}
		return fmt.Errorf("is_primary cannot be false for the only workspace: Paperclip always selects a primary. Create another primary workspace before demoting this one")
	}
	return nil
}

func (r *PaperclipWorkspaceResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || r.client == nil {
		return
	}
	var plan PaperclipWorkspaceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !plan.ProjectID.IsUnknown() && !plan.IsPrimary.IsUnknown() {
		if err := r.validateWrite(ctx, plan, plan.ID.ValueString()); err != nil {
			resp.Diagnostics.AddError("Unsupported Paperclip workspace configuration", err.Error())
		}
	}
}

func (r *PaperclipWorkspaceResource) UpgradeState(ctx context.Context) map[int64]resource.StateUpgrader {
	return map[int64]resource.StateUpgrader{0: {StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
		var prior map[string]json.RawMessage
		if err := json.Unmarshal(req.RawState.JSON, &prior); err != nil {
			resp.Diagnostics.AddError("Invalid workspace state", err.Error())
			return
		}
		prior["repo_ref"], prior["default_ref"] = prior["branch"], prior["branch"]
		delete(prior, "branch")
		raw, _ := json.Marshal(prior)
		// Framework decoding fills newly introduced attributes with null without writes.
		value, err := (&tfprotov6.RawState{JSON: raw}).Unmarshal(resp.State.Schema.Type().TerraformType(ctx))
		if err != nil {
			resp.Diagnostics.AddError("Invalid workspace state", err.Error())
			return
		}
		resp.State.Raw = value
	}}}
}

func (r *PaperclipWorkspaceResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *client.Client, got: %T", req.ProviderData),
		)
		return
	}
	r.client = c
}

func (r *PaperclipWorkspaceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PaperclipWorkspaceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	projectID := plan.ProjectID.ValueString()
	if err := r.validateWrite(ctx, plan, ""); err != nil {
		resp.Diagnostics.AddError("Unsupported Paperclip workspace configuration", err.Error())
		return
	}
	created, err := r.client.CreateWorkspace(ctx, projectID, workspaceRequest(projectID, plan))
	if err != nil {
		resp.Diagnostics.AddError("Error creating Paperclip workspace", err.Error())
		return
	}

	applyWorkspaceState(&plan, created)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipWorkspaceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PaperclipWorkspaceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	ws, err := r.client.GetWorkspaceInProject(
		ctx, state.ProjectID.ValueString(), state.ID.ValueString(),
	)
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Paperclip workspace", err.Error())
		return
	}

	applyWorkspaceState(&state, ws)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PaperclipWorkspaceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan PaperclipWorkspaceResourceModel
	var state PaperclipWorkspaceResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	projectID := state.ProjectID.ValueString()
	if err := r.validateWrite(ctx, plan, state.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Unsupported Paperclip workspace configuration", err.Error())
		return
	}
	updated, err := r.client.UpdateWorkspace(
		ctx,
		projectID,
		state.ID.ValueString(),
		workspaceRequest(projectID, plan),
	)
	if err != nil {
		resp.Diagnostics.AddError("Error updating Paperclip workspace", err.Error())
		return
	}

	applyWorkspaceState(&plan, updated)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipWorkspaceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PaperclipWorkspaceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.DeleteWorkspace(
		ctx,
		state.ProjectID.ValueString(),
		state.ID.ValueString(),
	); err != nil {
		if !errors.Is(err, client.ErrNotFound) {
			resp.Diagnostics.AddError("Error deleting Paperclip workspace", err.Error())
			return
		}
	}
}

func (r *PaperclipWorkspaceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := req.ID
	if strings.Contains(id, "/") {
		parts := strings.SplitN(id, "/", 2)
		if parts[0] != "" && parts[1] != "" {
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("project_id"), parts[0])...)
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
			return
		}
	}
	resp.Diagnostics.AddError(
		"Invalid Workspace Import ID",
		fmt.Sprintf("Expected <project_id>/<workspace_id>, got %q", id),
	)
}
