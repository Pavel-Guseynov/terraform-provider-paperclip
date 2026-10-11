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

var (
	_ resource.Resource                   = &PaperclipSecretResource{}
	_ resource.ResourceWithConfigure      = &PaperclipSecretResource{}
	_ resource.ResourceWithImportState    = &PaperclipSecretResource{}
	_ resource.ResourceWithValidateConfig = &PaperclipSecretResource{}
	_ resource.ResourceWithUpgradeState   = &PaperclipSecretResource{}
	_ resource.ResourceWithModifyPlan     = &PaperclipSecretResource{}
)

func NewPaperclipSecretResource() resource.Resource {
	return &PaperclipSecretResource{}
}

type PaperclipSecretResource struct {
	client *client.Client
}

type PaperclipSecretResourceModel struct {
	ID                 types.String `tfsdk:"id"`
	CompanyID          types.String `tfsdk:"company_id"`
	Key                types.String `tfsdk:"key"`
	Name               types.String `tfsdk:"name"`
	ProviderType       types.String `tfsdk:"provider_type"`
	Status             types.String `tfsdk:"status"`
	ManagedMode        types.String `tfsdk:"managed_mode"`
	ExternalRef        types.String `tfsdk:"external_ref"`
	ProviderConfigID   types.String `tfsdk:"provider_config_id"`
	ProviderMetadata   types.String `tfsdk:"provider_metadata"`
	ProviderVersionRef types.String `tfsdk:"provider_version_ref"`
	Description        types.String `tfsdk:"description"`
	Value              types.String `tfsdk:"value"`
	Rotation           types.String `tfsdk:"rotation"`
	LatestVersion      types.Int64  `tfsdk:"latest_version"`
	LastRotatedAt      types.String `tfsdk:"last_rotated_at"`
}

func normalizePaperclipSecretState(state *PaperclipSecretResourceModel, secret *client.PaperclipSecret) {
	state.ID = types.StringValue(secret.ID)
	if secret.CompanyID != "" {
		state.CompanyID = types.StringValue(secret.CompanyID)
	}
	state.Name = types.StringValue(secret.Name)
	state.Description = stringOrNull(secret.Description)
	state.Key = stringOrNull(secret.Key)
	if secret.Provider != "" {
		state.ProviderType = types.StringValue(secret.Provider)
	} else if state.ProviderType.IsNull() || state.ProviderType.IsUnknown() {
		state.ProviderType = types.StringValue("local_encrypted")
	}
	if secret.Status != "" {
		state.Status = types.StringValue(secret.Status)
	} else if state.Status.IsNull() || state.Status.IsUnknown() {
		state.Status = types.StringValue("active")
	}
	if secret.ManagedMode != "" {
		state.ManagedMode = types.StringValue(secret.ManagedMode)
	} else if state.ManagedMode.IsNull() || state.ManagedMode.IsUnknown() {
		state.ManagedMode = types.StringValue("paperclip_managed")
	}
	state.ExternalRef = stringOrNull(secret.ExternalRef)
	state.ProviderConfigID = stringOrNull(secret.ProviderConfigID)
	state.ProviderVersionRef = stringOrNull(secret.ProviderVersionRef)
	state.ProviderMetadata = refreshSemanticJSON(state.ProviderMetadata, secret.ProviderMetadata)
	if secret.LatestVersion > 0 {
		state.LatestVersion = types.Int64Value(secret.LatestVersion)
	} else if state.LatestVersion.IsUnknown() {
		state.LatestVersion = types.Int64Null()
	}
	state.LastRotatedAt = stringOrNull(secret.LastRotatedAt)
}

func (r *PaperclipSecretResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_secret"
}

func (r *PaperclipSecretResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:     1,
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
			"key": schema.StringAttribute{
				Description: "Optional native secret key.",
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
				},
			},
			"status": schema.StringAttribute{
				Description: "Administrative secret status: active, disabled, or archived.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"managed_mode": schema.StringAttribute{
				Description: "Secret managed mode: paperclip_managed or external_reference.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"external_ref": schema.StringAttribute{
				Description: "External vault reference string. Required for external_reference secrets; prohibited for paperclip_managed secrets.",
				Optional:    true,
			},
			"provider_config_id": schema.StringAttribute{
				Description: "Optional vault provider configuration identifier.",
				Optional:    true,
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
			"provider_version_ref": schema.StringAttribute{
				Description: "Optional provider-specific version reference.",
				Optional:    true,
			},
			"description": schema.StringAttribute{
				Description: "Optional secret description.",
				Optional:    true,
			},
			"value": schema.StringAttribute{
				Description: "Optional write-only secret plaintext value. It must be supplied together with rotation for paperclip_managed secrets and is never persisted in plan or state. Prohibited for external_reference secrets.",
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
			"latest_version": schema.Int64Attribute{
				Description: "The latest secret revision version number reported by Paperclip. When specified in configuration, detects out-of-band rotations as plan drift.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.Int64{
					int64UseStateForUnknown(),
				},
			},
			"last_rotated_at": schema.StringAttribute{
				Description: "Timestamp when the secret was last rotated, reflecting remote rotations as drift. When specified in configuration, detects out-of-band rotations as plan drift.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *PaperclipSecretResource) UpgradeState(ctx context.Context) map[int64]resource.StateUpgrader {
	return map[int64]resource.StateUpgrader{
		0: {
			PriorSchema: &schema.Schema{
				Attributes: map[string]schema.Attribute{
					"id": schema.StringAttribute{
						Computed: true,
					},
					"company_id": schema.StringAttribute{
						Required: true,
					},
					"name": schema.StringAttribute{
						Required: true,
					},
					"description": schema.StringAttribute{
						Optional: true,
					},
					"value": schema.StringAttribute{
						Optional:  true,
						Sensitive: true,
						WriteOnly: true,
					},
					"rotation": schema.StringAttribute{
						Optional: true,
						Computed: true,
					},
				},
			},
			StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
				var prior struct {
					ID          types.String `tfsdk:"id"`
					CompanyID   types.String `tfsdk:"company_id"`
					Name        types.String `tfsdk:"name"`
					Description types.String `tfsdk:"description"`
					Value       types.String `tfsdk:"value"`
					Rotation    types.String `tfsdk:"rotation"`
				}
				resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
				if resp.Diagnostics.HasError() {
					return
				}

				upgraded := PaperclipSecretResourceModel{
					ID:                 prior.ID,
					CompanyID:          prior.CompanyID,
					Name:               prior.Name,
					Description:        prior.Description,
					Value:              types.StringNull(),
					Rotation:           prior.Rotation,
					Key:                types.StringNull(),
					ProviderType:       types.StringValue("local_encrypted"),
					Status:             types.StringValue("active"),
					ManagedMode:        types.StringValue("paperclip_managed"),
					ExternalRef:        types.StringNull(),
					ProviderConfigID:   types.StringNull(),
					ProviderMetadata:   types.StringNull(),
					ProviderVersionRef: types.StringNull(),
					LatestVersion:      types.Int64Null(),
					LastRotatedAt:      types.StringNull(),
				}

				resp.Diagnostics.Append(resp.State.Set(ctx, &upgraded)...)
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
	mode := "paperclip_managed"
	if !config.ManagedMode.IsNull() && !config.ManagedMode.IsUnknown() && config.ManagedMode.ValueString() != "" {
		mode = config.ManagedMode.ValueString()
	}

	valueConfigured := configuredString(config.Value)
	rotationConfigured := configuredString(config.Rotation)
	externalRefConfigured := configuredString(config.ExternalRef)

	if mode == "external_reference" {
		if valueConfigured {
			errors = append(errors, secretRotationValidationError{"value", "Invalid secret value", "external_reference secrets cannot declare a plaintext value"})
		}
		if !externalRefConfigured || (!config.ExternalRef.IsUnknown() && config.ExternalRef.ValueString() == "") {
			errors = append(errors, secretRotationValidationError{"external_ref", "Missing external_ref", "external_reference secrets require an external_ref"})
		}
	} else {
		if externalRefConfigured && !config.ExternalRef.IsUnknown() && config.ExternalRef.ValueString() != "" {
			errors = append(errors, secretRotationValidationError{"external_ref", "Invalid external_ref", "paperclip_managed secrets cannot set external_ref"})
		}
		if valueConfigured && !config.Value.IsUnknown() && config.Value.ValueString() == "" {
			errors = append(errors, secretRotationValidationError{"value", "Invalid secret value", "value must not be empty when a secret rotation is declared"})
		}
		if rotationConfigured && !config.Rotation.IsUnknown() && config.Rotation.ValueString() == "" {
			errors = append(errors, secretRotationValidationError{"rotation", "Invalid secret rotation", "rotation must be a non-empty, non-secret identifier"})
		}
		if valueConfigured != rotationConfigured {
			errors = append(errors, secretRotationValidationError{"rotation", "Incomplete secret rotation declaration", "value and rotation must either both be configured or both be omitted"})
		}
	}

	return errors
}

func (r *PaperclipSecretResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config PaperclipSecretResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	for _, validationError := range validateSecretRotation(config) {
		resp.Diagnostics.AddAttributeError(path.Root(validationError.attribute), validationError.summary, validationError.detail)
	}

	if !config.Status.IsNull() && !config.Status.IsUnknown() {
		st := config.Status.ValueString()
		if st != "active" && st != "disabled" && st != "archived" {
			resp.Diagnostics.AddAttributeError(path.Root("status"), "Invalid Secret Status", "status must be one of 'active', 'disabled', or 'archived'")
		}
	}

	if !config.ProviderType.IsNull() && !config.ProviderType.IsUnknown() {
		p := config.ProviderType.ValueString()
		if p != "local_encrypted" && p != "aws_secrets_manager" && p != "gcp_secret_manager" && p != "vault" {
			resp.Diagnostics.AddAttributeError(path.Root("provider_type"), "Invalid Secret Provider", "provider_type must be one of 'local_encrypted', 'aws_secrets_manager', 'gcp_secret_manager', or 'vault'")
		}
	}

	if !config.ManagedMode.IsNull() && !config.ManagedMode.IsUnknown() {
		m := config.ManagedMode.ValueString()
		if m != "paperclip_managed" && m != "external_reference" {
			resp.Diagnostics.AddAttributeError(path.Root("managed_mode"), "Invalid Secret Managed Mode", "managed_mode must be either 'paperclip_managed' or 'external_reference'")
		}
	}

	if !config.Key.IsNull() && !config.Key.IsUnknown() {
		k := strings.TrimSpace(config.Key.ValueString())
		if len(k) > 120 || !secretKeyRegex.MatchString(k) {
			resp.Diagnostics.AddAttributeError(path.Root("key"), "Invalid Secret Key", fmt.Sprintf("Key %q must match ^[a-zA-Z0-9_.-]+$ and have length between 1 and 120", k))
		}
	}

	if !config.LatestVersion.IsNull() && !config.LatestVersion.IsUnknown() && config.LatestVersion.ValueInt64() < 1 {
		resp.Diagnostics.AddAttributeError(path.Root("latest_version"), "Invalid Latest Version", "latest_version must be a positive integer (>= 1)")
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

func (r *PaperclipSecretResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var plan PaperclipSecretResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !req.State.Raw.IsNull() {
		var state PaperclipSecretResourceModel
		resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
		if resp.Diagnostics.HasError() {
			return
		}

		var config PaperclipSecretResourceModel
		if !req.Config.Raw.IsNull() {
			resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
			if resp.Diagnostics.HasError() {
				return
			}
		}

		isRotating := !plan.Rotation.IsNull() && !plan.Rotation.IsUnknown() && !plan.Rotation.Equal(state.Rotation)
		if isRotating {
			if config.LatestVersion.IsNull() {
				plan.LatestVersion = types.Int64Unknown()
			}
			if config.LastRotatedAt.IsNull() {
				plan.LastRotatedAt = types.StringUnknown()
			}
			resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
		}
	}
}

func (r *PaperclipSecretResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PaperclipSecretResourceModel
	var config PaperclipSecretResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	opts := client.CreateSecretOptions{
		Name:               plan.Name.ValueString(),
		Key:                plan.Key.ValueString(),
		Provider:           plan.ProviderType.ValueString(),
		ProviderConfigID:   plan.ProviderConfigID.ValueString(),
		ManagedMode:        plan.ManagedMode.ValueString(),
		Description:        plan.Description.ValueString(),
		ExternalRef:        plan.ExternalRef.ValueString(),
		ProviderVersionRef: plan.ProviderVersionRef.ValueString(),
	}

	if !plan.ProviderMetadata.IsNull() && !plan.ProviderMetadata.IsUnknown() && plan.ProviderMetadata.ValueString() != "" {
		var meta map[string]interface{}
		if err := json.Unmarshal([]byte(plan.ProviderMetadata.ValueString()), &meta); err == nil {
			opts.ProviderMetadata = meta
		}
	}

	isExternal := plan.ManagedMode.ValueString() == "external_reference"
	if !isExternal && configuredString(config.Value) {
		val := config.Value.ValueString()
		opts.Value = &val
	}

	secret, err := r.client.CreateSecretWithOptions(ctx, plan.CompanyID.ValueString(), opts)
	if err != nil {
		resp.Diagnostics.AddError("Error creating Paperclip secret", err.Error())
		return
	}

	desiredStatus := plan.Status.ValueString()
	if desiredStatus == "disabled" || desiredStatus == "archived" {
		updated, err := r.client.UpdateSecretWithOptions(ctx, secret.ID, client.UpdateSecretOptions{
			Status: desiredStatus,
		})
		if err != nil {
			resp.Diagnostics.AddError("Error setting Paperclip secret initial status", err.Error())
			return
		}
		secret = updated
	}

	if !config.LatestVersion.IsNull() && !config.LatestVersion.IsUnknown() && config.LatestVersion.ValueInt64() != secret.LatestVersion {
		resp.Diagnostics.AddError(
			"Secret Version Mismatch",
			fmt.Sprintf("Secret %q is at version %d remotely after creation, which cannot be reconciled with configured latest_version %d",
				secret.Name, secret.LatestVersion, config.LatestVersion.ValueInt64()),
		)
		return
	}

	normalizePaperclipSecretState(&plan, secret)
	if plan.Rotation.IsUnknown() {
		plan.Rotation = types.StringNull()
	}

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
		ID:                 state.ID.ValueString(),
		CompanyID:          state.CompanyID.ValueString(),
		Name:               plan.Name.ValueString(),
		Description:        plan.Description.ValueString(),
		Key:                plan.Key.ValueString(),
		Provider:           plan.ProviderType.ValueString(),
		Status:             plan.Status.ValueString(),
		ManagedMode:        plan.ManagedMode.ValueString(),
		ExternalRef:        plan.ExternalRef.ValueString(),
		ProviderConfigID:   plan.ProviderConfigID.ValueString(),
		ProviderVersionRef: plan.ProviderVersionRef.ValueString(),
	}
	if !state.LatestVersion.IsNull() && !state.LatestVersion.IsUnknown() {
		secret.LatestVersion = state.LatestVersion.ValueInt64()
	}
	if !state.LastRotatedAt.IsNull() && !state.LastRotatedAt.IsUnknown() {
		secret.LastRotatedAt = state.LastRotatedAt.ValueString()
	}

	metaChanged := !plan.ProviderMetadata.Equal(state.ProviderMetadata)
	statusChanged := !plan.Status.IsNull() && !plan.Status.IsUnknown() && !plan.Status.Equal(state.Status)
	keyChanged := !plan.Key.Equal(state.Key)
	nameChanged := !plan.Name.Equal(state.Name)
	descChanged := !plan.Description.Equal(state.Description)
	pcfgChanged := !plan.ProviderConfigID.Equal(state.ProviderConfigID)
	extRefChanged := !plan.ExternalRef.Equal(state.ExternalRef)

	needsMetadataUpdate := nameChanged || descChanged || keyChanged || statusChanged || pcfgChanged || extRefChanged || metaChanged

	if needsMetadataUpdate {
		name := plan.Name.ValueString()
		if plan.Name.IsNull() || plan.Name.IsUnknown() {
			name = state.Name.ValueString()
		}
		opts := client.UpdateSecretOptions{
			Name: name,
			Key:  plan.Key.ValueString(),
		}
		if statusChanged {
			opts.Status = plan.Status.ValueString()
		}
		if descChanged {
			desc := plan.Description.ValueString()
			opts.Description = &desc
		}
		if pcfgChanged {
			pcfg := plan.ProviderConfigID.ValueString()
			opts.ProviderConfigID = &pcfg
		}
		if extRefChanged {
			extRef := plan.ExternalRef.ValueString()
			opts.ExternalRef = &extRef
		}

		if !plan.ProviderMetadata.IsNull() && !plan.ProviderMetadata.IsUnknown() && plan.ProviderMetadata.ValueString() != "" {
			var meta map[string]interface{}
			if err := json.Unmarshal([]byte(plan.ProviderMetadata.ValueString()), &meta); err == nil {
				opts.ProviderMetadata = meta
			}
		} else if metaChanged && (plan.ProviderMetadata.IsNull() || plan.ProviderMetadata.ValueString() == "") {
			opts.ProviderMetadata = make(map[string]interface{})
		}

		updated, err := r.client.UpdateSecretWithOptions(ctx, state.ID.ValueString(), opts)
		if err != nil {
			resp.Diagnostics.AddError("Error updating Paperclip secret metadata", err.Error())
			return
		}
		secret = updated
	}

	if value := secretRotationValue(config, plan, state); value != "" {
		rotateOpts := client.RotateSecretOptions{Value: &value}
		if !plan.ExternalRef.IsNull() && !plan.ExternalRef.IsUnknown() && plan.ExternalRef.ValueString() != "" {
			extRef := plan.ExternalRef.ValueString()
			rotateOpts.ExternalRef = &extRef
		}
		rotated, err := r.client.RotateSecretWithOptions(ctx, state.ID.ValueString(), rotateOpts)
		if err != nil {
			resp.Diagnostics.AddError("Error rotating Paperclip secret", err.Error())
			return
		}
		secret = rotated
	}

	if !config.LatestVersion.IsNull() && !config.LatestVersion.IsUnknown() && config.LatestVersion.ValueInt64() != secret.LatestVersion {
		resp.Diagnostics.AddError(
			"Secret Version Mismatch",
			fmt.Sprintf("Secret %q is at version %d remotely, which cannot be reconciled with configured latest_version %d; update configuration to %d to acknowledge rotation",
				secret.Name, secret.LatestVersion, config.LatestVersion.ValueInt64(), secret.LatestVersion),
		)
		return
	}
	if !config.LastRotatedAt.IsNull() && !config.LastRotatedAt.IsUnknown() && config.LastRotatedAt.ValueString() != secret.LastRotatedAt {
		resp.Diagnostics.AddError(
			"Secret Rotation Timestamp Mismatch",
			fmt.Sprintf("Secret %q has last_rotated_at %q remotely, which cannot be reconciled with configured last_rotated_at %q; update configuration to acknowledge rotation",
				secret.Name, secret.LastRotatedAt, config.LastRotatedAt.ValueString()),
		)
		return
	}

	normalizePaperclipSecretState(&plan, secret)
	if plan.Rotation.IsUnknown() {
		plan.Rotation = state.Rotation
	}

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
		if parts[0] != "" && parts[1] != "" {
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("company_id"), parts[0])...)
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
			return
		}
	}
	resp.Diagnostics.AddError(
		"Invalid Secret Import ID",
		fmt.Sprintf("Expected <company_id>/<secret_id>, got %q", id),
	)
}
