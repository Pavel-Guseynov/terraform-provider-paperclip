package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                   = &PaperclipUserSecretDefinitionResource{}
	_ resource.ResourceWithConfigure      = &PaperclipUserSecretDefinitionResource{}
	_ resource.ResourceWithImportState    = &PaperclipUserSecretDefinitionResource{}
	_ resource.ResourceWithValidateConfig = &PaperclipUserSecretDefinitionResource{}
)

func NewPaperclipUserSecretDefinitionResource() resource.Resource {
	return &PaperclipUserSecretDefinitionResource{}
}

type PaperclipUserSecretDefinitionResource struct {
	client *client.Client
}

type PaperclipUserSecretDefinitionResourceModel struct {
	ID               types.String `tfsdk:"id"`
	CompanyID        types.String `tfsdk:"company_id"`
	Key              types.String `tfsdk:"key"`
	Name             types.String `tfsdk:"name"`
	Description      types.String `tfsdk:"description"`
	Status           types.String `tfsdk:"status"`
	ProviderType     types.String `tfsdk:"provider_type"`
	ProviderConfigID types.String `tfsdk:"provider_config_id"`
	ManagedMode      types.String `tfsdk:"managed_mode"`
	ProviderMetadata types.String `tfsdk:"provider_metadata"`
	UsageGuidance    types.String `tfsdk:"usage_guidance"`
}

func normalizePaperclipUserSecretDefinitionState(state *PaperclipUserSecretDefinitionResourceModel, def *client.PaperclipUserSecretDefinition) {
	state.ID = types.StringValue(def.ID)
	if def.CompanyID != "" {
		state.CompanyID = types.StringValue(def.CompanyID)
	}
	state.Key = types.StringValue(def.Key)
	state.Name = types.StringValue(def.Name)
	state.Description = stringOrNull(def.Description)
	if def.Status != "" {
		state.Status = types.StringValue(def.Status)
	} else if state.Status.IsNull() || state.Status.IsUnknown() {
		state.Status = types.StringValue("active")
	}
	if def.Provider != "" {
		state.ProviderType = types.StringValue(def.Provider)
	} else if state.ProviderType.IsNull() || state.ProviderType.IsUnknown() {
		state.ProviderType = types.StringValue("local_encrypted")
	}
	state.ProviderConfigID = stringOrNull(def.ProviderConfigID)
	if def.ManagedMode != "" {
		state.ManagedMode = types.StringValue(def.ManagedMode)
	} else if state.ManagedMode.IsNull() || state.ManagedMode.IsUnknown() {
		state.ManagedMode = types.StringValue("paperclip_managed")
	}
	state.ProviderMetadata = refreshSemanticJSON(state.ProviderMetadata, def.ProviderMetadata)
	state.UsageGuidance = stringOrNull(def.UsageGuidance)
}

func (r *PaperclipUserSecretDefinitionResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user_secret_definition"
}

func (r *PaperclipUserSecretDefinitionResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a Paperclip user-secret definition in a company.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Unique identifier for the user-secret definition.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"company_id": schema.StringAttribute{
				Description: "The Paperclip company ID.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"key": schema.StringAttribute{
				Description: "Unique secret key identifier for user secret bindings (must match ^[a-zA-Z0-9_.-]+$).",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Description: "Human-readable name for the user-secret definition.",
				Required:    true,
			},
			"description": schema.StringAttribute{
				Description: "Optional description of the secret and its purpose.",
				Optional:    true,
			},
			"status": schema.StringAttribute{
				Description: "Secret definition status: active, disabled, or archived.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"provider_type": schema.StringAttribute{
				Description: "Secret provider backend: local_encrypted, aws_secrets_manager, gcp_secret_manager, or vault.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"provider_config_id": schema.StringAttribute{
				Description: "Optional vault provider configuration identifier.",
				Optional:    true,
			},
			"managed_mode": schema.StringAttribute{
				Description: "Secret managed mode: paperclip_managed or external_reference.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},
			"provider_metadata": schema.StringAttribute{
				Description: "Optional JSON metadata object for the secret provider backend.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					SemanticJSONPlanModifier(),
				},
				Validators: []validator.String{
					JSONAttributeValidator(),
				},
			},
			"usage_guidance": schema.StringAttribute{
				Description: "Optional guidance displayed to users when fulfilling secret values.",
				Optional:    true,
			},
		},
	}
}

func (r *PaperclipUserSecretDefinitionResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config PaperclipUserSecretDefinitionResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !config.Key.IsNull() && !config.Key.IsUnknown() {
		k := strings.TrimSpace(config.Key.ValueString())
		if k == "" || len(k) > 120 || !secretKeyRegex.MatchString(k) {
			resp.Diagnostics.AddAttributeError(path.Root("key"), "Invalid User Secret Key", fmt.Sprintf("Key %q must match ^[a-zA-Z0-9_.-]+$ and have length between 1 and 120", k))
		}
	}

	if !config.Status.IsNull() && !config.Status.IsUnknown() {
		s := config.Status.ValueString()
		if s != "active" && s != "disabled" && s != "archived" {
			resp.Diagnostics.AddAttributeError(path.Root("status"), "Invalid Status", "status must be one of 'active', 'disabled', or 'archived'")
		}
	}

	if !config.ProviderType.IsNull() && !config.ProviderType.IsUnknown() {
		p := config.ProviderType.ValueString()
		if p != "local_encrypted" && p != "aws_secrets_manager" && p != "gcp_secret_manager" && p != "vault" {
			resp.Diagnostics.AddAttributeError(path.Root("provider_type"), "Invalid Provider", "provider_type must be one of 'local_encrypted', 'aws_secrets_manager', 'gcp_secret_manager', or 'vault'")
		}
	}

	if !config.ManagedMode.IsNull() && !config.ManagedMode.IsUnknown() {
		m := config.ManagedMode.ValueString()
		if m != "paperclip_managed" && m != "external_reference" {
			resp.Diagnostics.AddAttributeError(path.Root("managed_mode"), "Invalid Managed Mode", "managed_mode must be either 'paperclip_managed' or 'external_reference'")
		}
	}
}

func (r *PaperclipUserSecretDefinitionResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *PaperclipUserSecretDefinitionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PaperclipUserSecretDefinitionResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	def := client.PaperclipUserSecretDefinition{
		Key:              plan.Key.ValueString(),
		Name:             plan.Name.ValueString(),
		Description:      plan.Description.ValueString(),
		Status:           plan.Status.ValueString(),
		Provider:         plan.ProviderType.ValueString(),
		ProviderConfigID: plan.ProviderConfigID.ValueString(),
		ManagedMode:      plan.ManagedMode.ValueString(),
		UsageGuidance:    plan.UsageGuidance.ValueString(),
	}

	if !plan.ProviderMetadata.IsNull() && !plan.ProviderMetadata.IsUnknown() && plan.ProviderMetadata.ValueString() != "" {
		var meta map[string]interface{}
		if err := json.Unmarshal([]byte(plan.ProviderMetadata.ValueString()), &meta); err == nil {
			def.ProviderMetadata = meta
		}
	}

	created, err := r.client.CreateUserSecretDefinition(ctx, plan.CompanyID.ValueString(), def)
	if err != nil {
		resp.Diagnostics.AddError("Error creating Paperclip user-secret definition", err.Error())
		return
	}

	normalizePaperclipUserSecretDefinitionState(&plan, created)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipUserSecretDefinitionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PaperclipUserSecretDefinitionResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	def, err := r.client.GetUserSecretDefinition(ctx, state.CompanyID.ValueString(), state.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Paperclip user-secret definition", err.Error())
		return
	}

	normalizePaperclipUserSecretDefinitionState(&state, def)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PaperclipUserSecretDefinitionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan PaperclipUserSecretDefinitionResourceModel
	var state PaperclipUserSecretDefinitionResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	def := client.PaperclipUserSecretDefinition{
		Key:              plan.Key.ValueString(),
		Name:             plan.Name.ValueString(),
		Description:      plan.Description.ValueString(),
		Status:           plan.Status.ValueString(),
		Provider:         plan.ProviderType.ValueString(),
		ProviderConfigID: plan.ProviderConfigID.ValueString(),
		ManagedMode:      plan.ManagedMode.ValueString(),
		UsageGuidance:    plan.UsageGuidance.ValueString(),
	}

	if !plan.ProviderMetadata.IsNull() && !plan.ProviderMetadata.IsUnknown() && plan.ProviderMetadata.ValueString() != "" {
		var meta map[string]interface{}
		if err := json.Unmarshal([]byte(plan.ProviderMetadata.ValueString()), &meta); err == nil {
			def.ProviderMetadata = meta
		}
	} else if plan.ProviderMetadata.IsNull() {
		def.ProviderMetadata = map[string]interface{}{}
	}

	updated, err := r.client.UpdateUserSecretDefinition(ctx, state.CompanyID.ValueString(), state.ID.ValueString(), def)
	if err != nil {
		resp.Diagnostics.AddError("Error updating Paperclip user-secret definition", err.Error())
		return
	}

	normalizePaperclipUserSecretDefinitionState(&plan, updated)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipUserSecretDefinitionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PaperclipUserSecretDefinitionResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.DeleteUserSecretDefinition(ctx, state.CompanyID.ValueString(), state.ID.ValueString()); err != nil {
		if !errors.Is(err, client.ErrNotFound) {
			resp.Diagnostics.AddError("Error deleting Paperclip user-secret definition", err.Error())
			return
		}
	}
}

func (r *PaperclipUserSecretDefinitionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := req.ID
	if strings.Contains(id, "/") {
		parts := strings.SplitN(id, "/", 2)
		if parts[0] != "" && parts[1] != "" {
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("company_id"), parts[0])...)
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
			return
		}
	}
	resp.Diagnostics.AddError(
		"Invalid User Secret Definition Import ID",
		fmt.Sprintf("Expected <company_id>/<id>, got %q", id),
	)
}
