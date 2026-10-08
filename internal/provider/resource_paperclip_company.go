package provider

import (
	"context"
	"errors"
	"fmt"

	"git.guseynov.org/guseynov/paperclip/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                 = &PaperclipCompanyResource{}
	_ resource.ResourceWithConfigure    = &PaperclipCompanyResource{}
	_ resource.ResourceWithImportState  = &PaperclipCompanyResource{}
	_ resource.ResourceWithUpgradeState = &PaperclipCompanyResource{}
)

type companyStatusValidator struct{}

func (companyStatusValidator) Description(context.Context) string {
	return "status must be one of: active, paused, archived"
}

func (v companyStatusValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (companyStatusValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	val := req.ConfigValue.ValueString()
	switch val {
	case "active", "paused", "archived":
		return
	default:
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid Company Status",
			fmt.Sprintf("Status %q is invalid. Must be one of: active, paused, archived", val),
		)
	}
}

func CompanyStatusValidator() validator.String {
	return companyStatusValidator{}
}

func NewPaperclipCompanyResource() resource.Resource {
	return &PaperclipCompanyResource{}
}

type PaperclipCompanyResource struct {
	client *client.Client
}

type PaperclipCompanyResourceModel struct {
	ID                               types.String `tfsdk:"id"`
	Name                             types.String `tfsdk:"name"`
	Description                      types.String `tfsdk:"description"`
	Status                           types.String `tfsdk:"status"`
	DefaultResponsibleUserID         types.String `tfsdk:"default_responsible_user_id"`
	RequireBoardApprovalForNewAgents types.Bool   `tfsdk:"require_board_approval_for_new_agents"`
	InteractionResolverGovernance    types.String `tfsdk:"interaction_resolver_governance"`
	FeedbackDataSharingEnabled       types.Bool   `tfsdk:"feedback_data_sharing_enabled"`
	LogoAssetID                      types.String `tfsdk:"logo_asset_id"`
	ArchiveOnDestroy                 types.Bool   `tfsdk:"archive_on_destroy"`
}

func (r *PaperclipCompanyResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_company"
}

func (r *PaperclipCompanyResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:     1,
		Description: "Manages a Paperclip company.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Unique identifier for the company.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Description: "Company identifier name.",
				Required:    true,
			},
			"description": schema.StringAttribute{
				Description: "Optional company description.",
				Optional:    true,
			},
			"status": schema.StringAttribute{
				Description: "Administrative status of the company (active, paused, or archived).",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("active"),
				Validators: []validator.String{
					CompanyStatusValidator(),
				},
			},
			"default_responsible_user_id": schema.StringAttribute{
				Description: "Optional default responsible user identifier.",
				Optional:    true,
			},
			"require_board_approval_for_new_agents": schema.BoolAttribute{
				Description: "Whether adding new agents requires board approval.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
			},
			"interaction_resolver_governance": schema.StringAttribute{
				Description: "Interaction resolver governance policy configuration as a JSON object.",
				Optional:    true,
				Validators: []validator.String{
					JSONAttributeValidator(),
					InteractionResolverGovernanceValidator(),
				},
				PlanModifiers: []planmodifier.String{
					SemanticJSONPlanModifier(),
				},
			},
			"feedback_data_sharing_enabled": schema.BoolAttribute{
				Description: "Whether feedback data sharing is enabled for the company.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
			},
			"logo_asset_id": schema.StringAttribute{
				Description: "Optional logo asset reference identifier.",
				Optional:    true,
			},
			"archive_on_destroy": schema.BoolAttribute{
				Description: "Whether to archive the company on destroy rather than hard-deleting it. Defaults to true.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
			},
		},
	}
}

func (r *PaperclipCompanyResource) UpgradeState(ctx context.Context) map[int64]resource.StateUpgrader {
	return map[int64]resource.StateUpgrader{
		0: {
			PriorSchema: &schema.Schema{
				Attributes: map[string]schema.Attribute{
					"id":                              schema.StringAttribute{Computed: true},
					"name":                            schema.StringAttribute{Required: true},
					"description":                     schema.StringAttribute{Optional: true},
					"require_task_start_confirmation": schema.BoolAttribute{Optional: true, Computed: true},
				},
			},
			StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
				var prior struct {
					ID                           types.String `tfsdk:"id"`
					Name                         types.String `tfsdk:"name"`
					Description                  types.String `tfsdk:"description"`
					RequireTaskStartConfirmation types.Bool   `tfsdk:"require_task_start_confirmation"`
				}
				resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
				if resp.Diagnostics.HasError() {
					return
				}
				resp.Diagnostics.Append(resp.State.Set(ctx, &PaperclipCompanyResourceModel{
					ID:                               prior.ID,
					Name:                             prior.Name,
					Description:                      prior.Description,
					Status:                           types.StringValue("active"),
					DefaultResponsibleUserID:         types.StringNull(),
					RequireBoardApprovalForNewAgents: types.BoolValue(false),
					InteractionResolverGovernance:    types.StringNull(),
					FeedbackDataSharingEnabled:       types.BoolValue(false),
					LogoAssetID:                      types.StringNull(),
					ArchiveOnDestroy:                 types.BoolValue(true),
				})...)
			},
		},
	}
}

func (r *PaperclipCompanyResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *PaperclipCompanyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PaperclipCompanyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	comp, err := r.client.CreateCompany(ctx, client.PaperclipCompany{
		Name:                     plan.Name.ValueString(),
		Description:              plan.Description.ValueString(),
		DefaultResponsibleUserID: plan.DefaultResponsibleUserID.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Error creating Paperclip company", err.Error())
		return
	}

	patch := make(map[string]interface{})
	needsUpdate := false

	if !plan.Status.IsNull() && !plan.Status.IsUnknown() && plan.Status.ValueString() != "" && plan.Status.ValueString() != "active" {
		patch["status"] = plan.Status.ValueString()
		needsUpdate = true
	}
	if plan.RequireBoardApprovalForNewAgents.ValueBool() {
		patch["requireBoardApprovalForNewAgents"] = true
		needsUpdate = true
	}
	if plan.FeedbackDataSharingEnabled.ValueBool() {
		patch["feedbackDataSharingEnabled"] = true
		needsUpdate = true
	}
	if !plan.LogoAssetID.IsNull() && !plan.LogoAssetID.IsUnknown() && plan.LogoAssetID.ValueString() != "" {
		patch["logoAssetId"] = plan.LogoAssetID.ValueString()
		needsUpdate = true
	}
	if !plan.InteractionResolverGovernance.IsNull() && !plan.InteractionResolverGovernance.IsUnknown() && plan.InteractionResolverGovernance.ValueString() != "" {
		govMap, err := parseInteractionResolverGovernance(plan.InteractionResolverGovernance.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Error parsing interaction resolver governance", err.Error())
			return
		}
		patch["interactionResolverGovernance"] = govMap
		needsUpdate = true
	}

	if needsUpdate {
		updated, err := r.client.UpdateCompany(ctx, comp.ID, patch)
		if err != nil {
			resp.Diagnostics.AddError("Error configuring Paperclip company", err.Error())
			return
		}
		comp = updated
	}

	plan.ID = types.StringValue(comp.ID)
	plan.Name = types.StringValue(comp.Name)
	plan.Description = stringOrNull(comp.Description)
	if comp.Status != "" {
		plan.Status = types.StringValue(comp.Status)
	} else if plan.Status.IsNull() || plan.Status.IsUnknown() {
		plan.Status = types.StringValue("active")
	}
	plan.DefaultResponsibleUserID = stringOrNull(comp.DefaultResponsibleUserID)
	plan.RequireBoardApprovalForNewAgents = types.BoolValue(comp.RequireBoardApprovalForNewAgents)
	plan.FeedbackDataSharingEnabled = types.BoolValue(comp.FeedbackDataSharingEnabled)
	plan.LogoAssetID = stringOrNull(comp.LogoAssetID)
	plan.InteractionResolverGovernance = refreshSemanticJSON(plan.InteractionResolverGovernance, comp.InteractionResolverGovernance)
	if plan.ArchiveOnDestroy.IsNull() || plan.ArchiveOnDestroy.IsUnknown() {
		plan.ArchiveOnDestroy = types.BoolValue(true)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipCompanyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PaperclipCompanyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	comp, err := r.client.GetCompany(ctx, state.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Paperclip company", err.Error())
		return
	}

	state.ID = types.StringValue(comp.ID)
	state.Name = types.StringValue(comp.Name)
	state.Description = stringOrNull(comp.Description)
	if comp.Status != "" {
		state.Status = types.StringValue(comp.Status)
	} else {
		state.Status = types.StringValue("active")
	}
	state.DefaultResponsibleUserID = stringOrNull(comp.DefaultResponsibleUserID)
	state.RequireBoardApprovalForNewAgents = types.BoolValue(comp.RequireBoardApprovalForNewAgents)
	state.FeedbackDataSharingEnabled = types.BoolValue(comp.FeedbackDataSharingEnabled)
	state.LogoAssetID = stringOrNull(comp.LogoAssetID)
	state.InteractionResolverGovernance = refreshSemanticJSON(state.InteractionResolverGovernance, comp.InteractionResolverGovernance)
	if state.ArchiveOnDestroy.IsNull() || state.ArchiveOnDestroy.IsUnknown() {
		state.ArchiveOnDestroy = types.BoolValue(true)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PaperclipCompanyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan PaperclipCompanyResourceModel
	var state PaperclipCompanyResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	patch := make(map[string]interface{})
	patch["name"] = plan.Name.ValueString()

	if plan.Description.IsNull() {
		patch["description"] = nil
	} else {
		patch["description"] = plan.Description.ValueString()
	}

	if !plan.Status.IsNull() && !plan.Status.IsUnknown() {
		patch["status"] = plan.Status.ValueString()
	}

	if !plan.DefaultResponsibleUserID.IsNull() {
		patch["defaultResponsibleUserId"] = plan.DefaultResponsibleUserID.ValueString()
	} else if !state.DefaultResponsibleUserID.IsNull() {
		patch["defaultResponsibleUserId"] = nil
	}

	patch["requireBoardApprovalForNewAgents"] = plan.RequireBoardApprovalForNewAgents.ValueBool()
	patch["feedbackDataSharingEnabled"] = plan.FeedbackDataSharingEnabled.ValueBool()

	if !plan.LogoAssetID.IsNull() {
		patch["logoAssetId"] = plan.LogoAssetID.ValueString()
	} else if !state.LogoAssetID.IsNull() {
		patch["logoAssetId"] = nil
	}

	if !plan.InteractionResolverGovernance.IsNull() && !plan.InteractionResolverGovernance.IsUnknown() && plan.InteractionResolverGovernance.ValueString() != "" {
		govMap, err := parseInteractionResolverGovernance(plan.InteractionResolverGovernance.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Error parsing interaction resolver governance", err.Error())
			return
		}
		patch["interactionResolverGovernance"] = govMap
	} else if !state.InteractionResolverGovernance.IsNull() && state.InteractionResolverGovernance.ValueString() != "" {
		patch["interactionResolverGovernance"] = map[string]interface{}{}
	}

	comp, err := r.client.UpdateCompany(ctx, state.ID.ValueString(), patch)
	if err != nil {
		resp.Diagnostics.AddError("Error updating Paperclip company", err.Error())
		return
	}

	plan.ID = types.StringValue(comp.ID)
	plan.Name = types.StringValue(comp.Name)
	plan.Description = stringOrNull(comp.Description)
	if comp.Status != "" {
		plan.Status = types.StringValue(comp.Status)
	} else {
		plan.Status = types.StringValue("active")
	}
	plan.DefaultResponsibleUserID = stringOrNull(comp.DefaultResponsibleUserID)
	plan.RequireBoardApprovalForNewAgents = types.BoolValue(comp.RequireBoardApprovalForNewAgents)
	plan.FeedbackDataSharingEnabled = types.BoolValue(comp.FeedbackDataSharingEnabled)
	plan.LogoAssetID = stringOrNull(comp.LogoAssetID)
	plan.InteractionResolverGovernance = refreshSemanticJSON(plan.InteractionResolverGovernance, comp.InteractionResolverGovernance)
	if plan.ArchiveOnDestroy.IsNull() || plan.ArchiveOnDestroy.IsUnknown() {
		plan.ArchiveOnDestroy = types.BoolValue(true)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipCompanyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PaperclipCompanyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !state.ArchiveOnDestroy.IsNull() && !state.ArchiveOnDestroy.ValueBool() {
		if err := r.client.DeleteCompany(ctx, state.ID.ValueString()); err != nil {
			if !errors.Is(err, client.ErrNotFound) {
				resp.Diagnostics.AddError("Error deleting Paperclip company", err.Error())
				return
			}
		}
	} else {
		if err := r.client.ArchiveCompany(ctx, state.ID.ValueString()); err != nil {
			if !errors.Is(err, client.ErrNotFound) {
				resp.Diagnostics.AddError("Error archiving Paperclip company", err.Error())
				return
			}
		}
	}
}

func (r *PaperclipCompanyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID == "" {
		resp.Diagnostics.AddError("Invalid Company Import ID", "Expected non-empty company_id")
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
