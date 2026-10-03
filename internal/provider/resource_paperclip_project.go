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
	_ resource.Resource                 = &PaperclipProjectResource{}
	_ resource.ResourceWithConfigure    = &PaperclipProjectResource{}
	_ resource.ResourceWithImportState  = &PaperclipProjectResource{}
	_ resource.ResourceWithUpgradeState = &PaperclipProjectResource{}
)

func NewPaperclipProjectResource() resource.Resource {
	return &PaperclipProjectResource{}
}

type PaperclipProjectResource struct {
	client *client.Client
}

type PaperclipProjectResourceModel struct {
	ID                       types.String `tfsdk:"id"`
	CompanyID                types.String `tfsdk:"company_id"`
	Name                     types.String `tfsdk:"name"`
	Description              types.String `tfsdk:"description"`
	ExecutionWorkspacePolicy types.String `tfsdk:"execution_workspace_policy"`
}

func (r *PaperclipProjectResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project"
}

func (r *PaperclipProjectResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:     1,
		Description: "Manages a Paperclip project under a company.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Unique identifier for the project.",
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
				Description: "Project identifier name.",
				Required:    true,
			},
			"description": schema.StringAttribute{
				Description: "Optional project description.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"execution_workspace_policy": schema.StringAttribute{
				Description: "Project-owned Paperclip execution workspace policy as a JSON object.",
				Optional:    true,
				Computed:    true,
				Validators: []validator.String{
					JSONAttributeValidator(),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					SemanticJSONPlanModifier(),
				},
			},
		},
	}
}

// Version zero also owned lead_agent_id. Upgrade only the stored project shape;
// the separate lead resource adopts the unchanged assignment from Paperclip.
func (r *PaperclipProjectResource) UpgradeState(ctx context.Context) map[int64]resource.StateUpgrader {
	return map[int64]resource.StateUpgrader{
		0: {
			PriorSchema: &schema.Schema{
				Attributes: map[string]schema.Attribute{
					"id":                         schema.StringAttribute{Computed: true},
					"company_id":                 schema.StringAttribute{Required: true},
					"name":                       schema.StringAttribute{Required: true},
					"description":                schema.StringAttribute{Optional: true, Computed: true},
					"lead_agent_id":              schema.StringAttribute{Optional: true, Computed: true},
					"execution_workspace_policy": schema.StringAttribute{Optional: true, Computed: true},
				},
			},
			StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
				var prior struct {
					ID                       types.String `tfsdk:"id"`
					CompanyID                types.String `tfsdk:"company_id"`
					Name                     types.String `tfsdk:"name"`
					Description              types.String `tfsdk:"description"`
					LeadAgentID              types.String `tfsdk:"lead_agent_id"`
					ExecutionWorkspacePolicy types.String `tfsdk:"execution_workspace_policy"`
				}
				resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
				if resp.Diagnostics.HasError() {
					return
				}
				resp.Diagnostics.Append(resp.State.Set(ctx, &PaperclipProjectResourceModel{
					ID:                       prior.ID,
					CompanyID:                prior.CompanyID,
					Name:                     prior.Name,
					Description:              prior.Description,
					ExecutionWorkspacePolicy: prior.ExecutionWorkspacePolicy,
				})...)
			},
		},
	}
}

func projectExecutionWorkspacePolicy(value types.String) (map[string]interface{}, error) {
	if value.IsNull() || value.IsUnknown() || value.ValueString() == "" {
		return nil, nil
	}
	var policy map[string]interface{}
	if err := json.Unmarshal([]byte(value.ValueString()), &policy); err != nil {
		return nil, fmt.Errorf("execution_workspace_policy must be a JSON object: %w", err)
	}
	if policy == nil {
		return nil, errors.New("execution_workspace_policy must be a JSON object")
	}
	return policy, nil
}

func (r *PaperclipProjectResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *PaperclipProjectResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PaperclipProjectResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	policy, err := projectExecutionWorkspacePolicy(plan.ExecutionWorkspacePolicy)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Paperclip project workspace policy", err.Error())
		return
	}

	proj, err := r.client.CreateProject(
		ctx,
		plan.CompanyID.ValueString(),
		plan.Name.ValueString(),
		plan.Description.ValueString(),
		policy,
	)
	if err != nil {
		resp.Diagnostics.AddError("Error creating Paperclip project", err.Error())
		return
	}

	plan.ID = types.StringValue(proj.ID)
	plan.CompanyID = types.StringValue(proj.CompanyID)
	plan.Name = types.StringValue(proj.Name)
	plan.Description = stringOrNull(proj.Description)
	plan.ExecutionWorkspacePolicy = refreshSemanticJSON(plan.ExecutionWorkspacePolicy, proj.ExecutionWorkspacePolicy)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipProjectResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PaperclipProjectResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	proj, err := r.client.GetProject(ctx, state.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Paperclip project", err.Error())
		return
	}

	state.ID = types.StringValue(proj.ID)
	if proj.CompanyID != "" {
		state.CompanyID = types.StringValue(proj.CompanyID)
	}
	state.Name = types.StringValue(proj.Name)
	state.Description = stringOrNull(proj.Description)
	state.ExecutionWorkspacePolicy = refreshSemanticJSON(state.ExecutionWorkspacePolicy, proj.ExecutionWorkspacePolicy)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PaperclipProjectResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan PaperclipProjectResourceModel
	var state PaperclipProjectResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	policy, err := projectExecutionWorkspacePolicy(plan.ExecutionWorkspacePolicy)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Paperclip project workspace policy", err.Error())
		return
	}

	proj, err := r.client.UpdateProject(
		ctx,
		state.ID.ValueString(),
		plan.Name.ValueString(),
		plan.Description.ValueString(),
		policy,
	)
	if err != nil {
		resp.Diagnostics.AddError("Error updating Paperclip project", err.Error())
		return
	}

	plan.ID = types.StringValue(proj.ID)
	plan.Name = types.StringValue(proj.Name)
	plan.Description = stringOrNull(proj.Description)
	plan.ExecutionWorkspacePolicy = refreshSemanticJSON(plan.ExecutionWorkspacePolicy, proj.ExecutionWorkspacePolicy)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipProjectResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PaperclipProjectResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.DeleteProject(ctx, state.ID.ValueString()); err != nil {
		if !errors.Is(err, client.ErrNotFound) {
			resp.Diagnostics.AddError("Error deleting Paperclip project", err.Error())
			return
		}
	}
}

func (r *PaperclipProjectResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := req.ID
	if strings.Contains(id, "/") {
		parts := strings.SplitN(id, "/", 2)
		if parts[0] != "" && parts[1] != "" {
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("company_id"), parts[0])...)
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
			return
		}
	} else if id != "" {
		resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
		return
	}
	resp.Diagnostics.AddError(
		"Invalid Project Import ID",
		fmt.Sprintf("Expected <company_id>/<project_id> or <project_id>, got %q", id),
	)
}

// PaperclipProjectLeadResource separates assignment from project creation so
// agent instructions can depend on the project without a dependency cycle.
type PaperclipProjectLeadResource struct {
	client *client.Client
}

type PaperclipProjectLeadModel struct {
	ID        types.String `tfsdk:"id"`
	ProjectID types.String `tfsdk:"project_id"`
	AgentID   types.String `tfsdk:"agent_id"`
}

var (
	_ resource.Resource                = &PaperclipProjectLeadResource{}
	_ resource.ResourceWithConfigure   = &PaperclipProjectLeadResource{}
	_ resource.ResourceWithImportState = &PaperclipProjectLeadResource{}
)

func NewPaperclipProjectLeadResource() resource.Resource {
	return &PaperclipProjectLeadResource{}
}

func (r *PaperclipProjectLeadResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project_lead"
}

func (r *PaperclipProjectLeadResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Owns a project's native leadAgentId after the project and agent exist. Import by project ID.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description:   "Project ID identifying this assignment.",
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"project_id": schema.StringAttribute{
				Description:   "Project receiving the lead assignment.",
				Required:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"agent_id": schema.StringAttribute{
				Description: "Native agent assigned as project lead.",
				Required:    true,
			},
		},
	}
}

func (r *PaperclipProjectLeadResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *PaperclipProjectLeadResource) assign(ctx context.Context, model *PaperclipProjectLeadModel) error {
	if strings.TrimSpace(model.ProjectID.ValueString()) == "" || strings.TrimSpace(model.AgentID.ValueString()) == "" {
		return errors.New("project_id and agent_id must be non-empty")
	}
	project, err := r.client.SetProjectLead(ctx, model.ProjectID.ValueString(), model.AgentID.ValueString())
	if err != nil {
		return err
	}
	model.ID = types.StringValue(project.ID)
	return nil
}

func (r *PaperclipProjectLeadResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PaperclipProjectLeadModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.assign(ctx, &plan); err != nil {
		resp.Diagnostics.AddError("Error assigning Paperclip project lead", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipProjectLeadResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PaperclipProjectLeadModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project, err := r.client.GetProject(ctx, state.ProjectID.ValueString())
	if errors.Is(err, client.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading Paperclip project lead", err.Error())
		return
	}
	if project.LeadAgentID == "" {
		resp.State.RemoveResource(ctx)
		return
	}
	state.ID = types.StringValue(project.ID)
	state.AgentID = types.StringValue(project.LeadAgentID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PaperclipProjectLeadResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan PaperclipProjectLeadModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.assign(ctx, &plan); err != nil {
		resp.Diagnostics.AddError("Error updating Paperclip project lead", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipProjectLeadResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PaperclipProjectLeadModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	project, err := r.client.GetProject(ctx, state.ProjectID.ValueString())
	if errors.Is(err, client.ErrNotFound) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading Paperclip project lead", err.Error())
		return
	}
	// A replacement made outside this resource is not ours to remove.
	if project.LeadAgentID != state.AgentID.ValueString() {
		return
	}
	if _, err := r.client.SetProjectLead(ctx, state.ProjectID.ValueString(), ""); err != nil && !errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Error clearing Paperclip project lead", err.Error())
	}
}

func (r *PaperclipProjectLeadResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := req.ID
	if strings.Contains(id, "/") {
		parts := strings.SplitN(id, "/", 2)
		if parts[1] != "" {
			id = parts[1]
		}
	}
	if id == "" {
		resp.Diagnostics.AddError("Invalid Project Lead Import ID", "Expected non-empty project_id")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("project_id"), id)...)
}
