package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"git.guseynov.org/guseynov/paperclip/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type boolUseStateForUnknownModifier struct{}

func (m boolUseStateForUnknownModifier) Description(ctx context.Context) string {
	return "Once set, the value of this attribute in state will not change to unknown."
}

func (m boolUseStateForUnknownModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (m boolUseStateForUnknownModifier) PlanModifyBool(ctx context.Context, req planmodifier.BoolRequest, resp *planmodifier.BoolResponse) {
	if !req.PlanValue.IsUnknown() {
		return
	}
	if req.StateValue.IsNull() {
		return
	}
	resp.PlanValue = req.StateValue
}

var (
	_ resource.Resource                   = &PaperclipSecretProviderConfigResource{}
	_ resource.ResourceWithConfigure      = &PaperclipSecretProviderConfigResource{}
	_ resource.ResourceWithImportState    = &PaperclipSecretProviderConfigResource{}
	_ resource.ResourceWithValidateConfig = &PaperclipSecretProviderConfigResource{}
)

func NewPaperclipSecretProviderConfigResource() resource.Resource {
	return &PaperclipSecretProviderConfigResource{}
}

type PaperclipSecretProviderConfigResource struct {
	client *client.Client
}

type PaperclipSecretProviderConfigResourceModel struct {
	ID            types.String `tfsdk:"id"`
	CompanyID     types.String `tfsdk:"company_id"`
	ProviderType  types.String `tfsdk:"provider_type"`
	DisplayName   types.String `tfsdk:"display_name"`
	Status        types.String `tfsdk:"status"`
	IsDefault     types.Bool   `tfsdk:"is_default"`
	Config        types.String `tfsdk:"config"`
	HealthStatus  types.String `tfsdk:"health_status"`
	HealthMessage types.String `tfsdk:"health_message"`
}

func normalizePaperclipSecretProviderConfigState(state *PaperclipSecretProviderConfigResourceModel, cfg *client.PaperclipSecretProviderConfig) {
	state.ID = types.StringValue(cfg.ID)
	if cfg.CompanyID != "" {
		state.CompanyID = types.StringValue(cfg.CompanyID)
	}
	state.ProviderType = types.StringValue(cfg.Provider)
	state.DisplayName = types.StringValue(cfg.DisplayName)
	if cfg.Status != "" {
		state.Status = types.StringValue(cfg.Status)
	} else if state.Status.IsNull() || state.Status.IsUnknown() {
		state.Status = types.StringValue("ready")
	}
	state.IsDefault = types.BoolValue(cfg.IsDefault)
	state.Config = refreshSemanticJSON(state.Config, cfg.Config)
	state.HealthStatus = stringOrNull(cfg.HealthStatus)
	state.HealthMessage = stringOrNull(cfg.HealthMessage)
}

func (r *PaperclipSecretProviderConfigResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_secret_provider_config"
}

func (r *PaperclipSecretProviderConfigResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a Paperclip secret provider configuration ('vault') for a company.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Unique identifier for the secret provider config.",
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
			"provider_type": schema.StringAttribute{
				Description: "Provider backend type: local_encrypted, aws_secrets_manager, gcp_secret_manager, or vault.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"display_name": schema.StringAttribute{
				Description: "Human-readable display name for the secret provider configuration.",
				Required:    true,
			},
			"status": schema.StringAttribute{
				Description: "Vault status: ready, warning, coming_soon, or disabled.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"is_default": schema.BoolAttribute{
				Description: "Whether this provider config is the default vault for the company.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.Bool{
					boolUseStateForUnknownModifier{},
				},
			},
			"config": schema.StringAttribute{
				Description: "JSON configuration parameters for the provider backend.",
				Optional:    true,
				Validators: []validator.String{
					JSONAttributeValidator(),
				},
				PlanModifiers: []planmodifier.String{
					SemanticJSONPlanModifier(),
				},
			},
			"health_status": schema.StringAttribute{
				Description: "Health status reported by the secret provider backend.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"health_message": schema.StringAttribute{
				Description: "Health status diagnostic message reported by the secret provider backend.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *PaperclipSecretProviderConfigResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config PaperclipSecretProviderConfigResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !config.ProviderType.IsNull() && !config.ProviderType.IsUnknown() {
		p := config.ProviderType.ValueString()
		if p != "local_encrypted" && p != "aws_secrets_manager" && p != "gcp_secret_manager" && p != "vault" {
			resp.Diagnostics.AddAttributeError(path.Root("provider_type"), "Invalid Provider", "provider_type must be one of 'local_encrypted', 'aws_secrets_manager', 'gcp_secret_manager', or 'vault'")
		}
	}

	if !config.Status.IsNull() && !config.Status.IsUnknown() {
		s := config.Status.ValueString()
		if s != "ready" && s != "warning" && s != "coming_soon" && s != "disabled" {
			resp.Diagnostics.AddAttributeError(path.Root("status"), "Invalid Status", "status must be one of 'ready', 'warning', 'coming_soon', or 'disabled'")
		}
	}

	if !config.DisplayName.IsNull() && !config.DisplayName.IsUnknown() {
		d := strings.TrimSpace(config.DisplayName.ValueString())
		if d == "" || len(d) > 120 {
			resp.Diagnostics.AddAttributeError(path.Root("display_name"), "Invalid Display Name", "display_name must be between 1 and 120 characters")
		}
	}
}

func (r *PaperclipSecretProviderConfigResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *PaperclipSecretProviderConfigResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PaperclipSecretProviderConfigResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cfg := client.PaperclipSecretProviderConfig{
		Provider:    plan.ProviderType.ValueString(),
		DisplayName: plan.DisplayName.ValueString(),
	}

	if !plan.Status.IsNull() && !plan.Status.IsUnknown() {
		cfg.Status = plan.Status.ValueString()
	}
	if !plan.IsDefault.IsNull() && !plan.IsDefault.IsUnknown() {
		cfg.IsDefault = plan.IsDefault.ValueBool()
	}
	if !plan.Config.IsNull() && !plan.Config.IsUnknown() && plan.Config.ValueString() != "" {
		var cMap map[string]interface{}
		if err := json.Unmarshal([]byte(plan.Config.ValueString()), &cMap); err == nil {
			cfg.Config = cMap
		}
	} else {
		cfg.Config = map[string]interface{}{}
	}

	created, err := r.client.CreateSecretProviderConfig(ctx, plan.CompanyID.ValueString(), cfg)
	if err != nil {
		resp.Diagnostics.AddError("Error creating Paperclip secret provider config", err.Error())
		return
	}

	normalizePaperclipSecretProviderConfigState(&plan, created)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipSecretProviderConfigResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PaperclipSecretProviderConfigResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cfg, err := r.client.GetSecretProviderConfig(ctx, state.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Paperclip secret provider config", err.Error())
		return
	}

	normalizePaperclipSecretProviderConfigState(&state, cfg)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PaperclipSecretProviderConfigResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan PaperclipSecretProviderConfigResourceModel
	var state PaperclipSecretProviderConfigResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cfg := client.PaperclipSecretProviderConfig{
		Provider:    plan.ProviderType.ValueString(),
		DisplayName: plan.DisplayName.ValueString(),
	}
	if !plan.Status.IsNull() && !plan.Status.IsUnknown() {
		cfg.Status = plan.Status.ValueString()
	}
	if !plan.IsDefault.IsNull() && !plan.IsDefault.IsUnknown() {
		cfg.IsDefault = plan.IsDefault.ValueBool()
	}
	if !plan.Config.IsNull() && !plan.Config.IsUnknown() && plan.Config.ValueString() != "" {
		var cMap map[string]interface{}
		if err := json.Unmarshal([]byte(plan.Config.ValueString()), &cMap); err == nil {
			cfg.Config = cMap
		}
	} else if plan.Config.IsNull() {
		cfg.Config = map[string]interface{}{}
	}

	updated, err := r.client.UpdateSecretProviderConfig(ctx, state.ID.ValueString(), cfg)
	if err != nil {
		resp.Diagnostics.AddError("Error updating Paperclip secret provider config", err.Error())
		return
	}

	normalizePaperclipSecretProviderConfigState(&plan, updated)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipSecretProviderConfigResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PaperclipSecretProviderConfigResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.DeleteSecretProviderConfig(ctx, state.ID.ValueString()); err != nil {
		if !errors.Is(err, client.ErrNotFound) {
			resp.Diagnostics.AddError("Error deleting Paperclip secret provider config", err.Error())
			return
		}
	}
}

func (r *PaperclipSecretProviderConfigResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := req.ID
	if strings.Contains(id, "/") {
		parts := strings.SplitN(id, "/", 2)
		if parts[0] != "" && parts[1] != "" {
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("company_id"), parts[0])...)
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
			return
		}
	}
	if id != "" {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
		return
	}
	resp.Diagnostics.AddError(
		"Invalid Secret Provider Config Import ID",
		fmt.Sprintf("Expected <company_id>/<id> or <id>, got %q", id),
	)
}
