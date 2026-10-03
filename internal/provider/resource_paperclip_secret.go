package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"git.guseynov.org/guseynov/paperclip/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                     = &PaperclipSecretResource{}
	_ resource.ResourceWithConfigure        = &PaperclipSecretResource{}
	_ resource.ResourceWithImportState      = &PaperclipSecretResource{}
	_ resource.ResourceWithModifyPlan       = &PaperclipSecretResource{}
	_ resource.ResourceWithValidateConfig   = &PaperclipSecretResource{}
)

func NewPaperclipSecretResource() resource.Resource {
	return &PaperclipSecretResource{}
}

type PaperclipSecretResource struct {
	client *client.Client
}

type PaperclipSecretResourceModel struct {
	ID          types.String `tfsdk:"id"`
	CompanyID   types.String `tfsdk:"company_id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	Value       types.String `tfsdk:"value"`
	Rotation    types.String `tfsdk:"rotation"`
}

func secretDescriptionState(prior types.String, remote string) types.String {
	if remote != "" {
		return types.StringValue(remote)
	}
	if !prior.IsNull() && !prior.IsUnknown() && prior.ValueString() != "" {
		return prior
	}
	return types.StringNull()
}

func normalizePaperclipSecretState(state *PaperclipSecretResourceModel, secret *client.PaperclipSecret) {
	state.ID = types.StringValue(secret.ID)
	if secret.CompanyID != "" {
		state.CompanyID = types.StringValue(secret.CompanyID)
	}
	state.Name = types.StringValue(secret.Name)
	state.Description = secretDescriptionState(state.Description, secret.Description)
}

func (r *PaperclipSecretResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_secret"
}

func (r *PaperclipSecretResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a Paperclip secret in a company.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Unique identifier for the secret.",
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
			"name": schema.StringAttribute{
				Description: "Secret identifier name.",
				Required:    true,
			},
			"description": schema.StringAttribute{
				Description: "Optional secret description.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"value": schema.StringAttribute{
				Description: "Optional write-only secret plaintext value. It must be supplied together with rotation and is never persisted in plan or state.",
				Optional:    true,
				Sensitive:   true,
				WriteOnly:   true,
			},
			"rotation": schema.StringAttribute{
				Description: "Optional non-secret rotation identifier. Change it together with value to perform exactly one secret update; omission preserves the last applied identifier so the same rotation cannot be replayed.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func configuredString(value types.String) bool {
	return !value.IsNull()
}

type secretRotationValidationError struct {
	attribute string
	summary   string
	detail    string
}

func validateSecretRotation(config PaperclipSecretResourceModel) []secretRotationValidationError {
	errors := []secretRotationValidationError{}
	valueConfigured := configuredString(config.Value)
	rotationConfigured := configuredString(config.Rotation)
	if valueConfigured && !config.Value.IsUnknown() && config.Value.ValueString() == "" {
		errors = append(errors, secretRotationValidationError{"value", "Invalid secret value", "value must not be empty when a secret rotation is declared"})
	}
	if rotationConfigured && !config.Rotation.IsUnknown() && config.Rotation.ValueString() == "" {
		errors = append(errors, secretRotationValidationError{"rotation", "Invalid secret rotation", "rotation must be a non-empty, non-secret identifier"})
	}
	if valueConfigured != rotationConfigured {
		errors = append(errors, secretRotationValidationError{"rotation", "Incomplete secret rotation declaration", "value and rotation must either both be configured or both be omitted"})
	}
	return errors
}

func (r *PaperclipSecretResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config PaperclipSecretResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !config.Value.IsNull() && config.Rotation.IsNull() {
		resp.Diagnostics.AddAttributeError(path.Root("rotation"), "Incomplete secret rotation declaration", "value and rotation must either both be configured or both be omitted")
	}
}

func (r *PaperclipSecretResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || req.Plan.Schema == nil || req.Config.Schema == nil {
		return
	}

	var plan PaperclipSecretResourceModel
	var config PaperclipSecretResourceModel
	_ = req.Plan.Get(ctx, &plan)
	_ = req.Config.Get(ctx, &config)

	isCreate := req.State.Raw.IsNull() || req.State.Schema == nil
	rotationChanging := false
	if !isCreate {
		var state PaperclipSecretResourceModel
		_ = req.State.Get(ctx, &state)
		rotationChanging = !plan.Rotation.Equal(state.Rotation)
	}

	if isCreate || rotationChanging || configuredString(config.Value) {
		for _, validationError := range validateSecretRotation(config) {
			resp.Diagnostics.AddAttributeError(path.Root(validationError.attribute), validationError.summary, validationError.detail)
		}
	}
}

func (r *PaperclipSecretResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *PaperclipSecretResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PaperclipSecretResourceModel
	var config PaperclipSecretResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	secret, err := r.client.CreateSecret(
		ctx,
		plan.CompanyID.ValueString(),
		plan.Name.ValueString(),
		plan.Description.ValueString(),
		config.Value.ValueString(),
	)
	if err != nil {
		resp.Diagnostics.AddError("Error creating Paperclip secret", err.Error())
		return
	}

	normalizePaperclipSecretState(&plan, secret)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipSecretResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PaperclipSecretResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	secret, err := r.client.GetSecret(ctx, state.CompanyID.ValueString(), state.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Paperclip secret", err.Error())
		return
	}

	normalizePaperclipSecretState(&state, secret)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PaperclipSecretResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan PaperclipSecretResourceModel
	var state PaperclipSecretResourceModel
	var config PaperclipSecretResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	secret := &client.PaperclipSecret{
		ID:          state.ID.ValueString(),
		CompanyID:   state.CompanyID.ValueString(),
		Name:        plan.Name.ValueString(),
		Description: plan.Description.ValueString(),
	}
	if !plan.Name.Equal(state.Name) || !plan.Description.Equal(state.Description) {
		updated, err := r.client.UpdateSecret(
			ctx,
			state.ID.ValueString(),
			plan.Name.ValueString(),
			plan.Description.ValueString(),
		)
		if err != nil {
			resp.Diagnostics.AddError("Error updating Paperclip secret metadata", err.Error())
			return
		}
		secret = updated
	}

	if value := secretRotationValue(config, plan, state); value != "" {
		rotated, err := r.client.RotateSecret(ctx, state.ID.ValueString(), value)
		if err != nil {
			resp.Diagnostics.AddError("Error rotating Paperclip secret", err.Error())
			return
		}
		secret = rotated
	}

	normalizePaperclipSecretState(&plan, secret)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func secretRotationValue(config, plan, state PaperclipSecretResourceModel) string {
	if plan.Rotation.IsNull() || plan.Rotation.IsUnknown() || plan.Rotation.Equal(state.Rotation) {
		return ""
	}
	return config.Value.ValueString()
}

func (r *PaperclipSecretResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PaperclipSecretResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.DeleteSecret(ctx, state.ID.ValueString()); err != nil {
		if !errors.Is(err, client.ErrNotFound) {
			resp.Diagnostics.AddError("Error deleting Paperclip secret", err.Error())
			return
		}
	}
}

func (r *PaperclipSecretResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := req.ID
	if strings.Contains(id, "/") {
		parts := strings.SplitN(id, "/", 2)
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("company_id"), parts[0])...)
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
