package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/diag"
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
	_ resource.Resource                   = &PaperclipProjectResource{}
	_ resource.ResourceWithConfigure      = &PaperclipProjectResource{}
	_ resource.ResourceWithImportState    = &PaperclipProjectResource{}
	_ resource.ResourceWithUpgradeState   = &PaperclipProjectResource{}
	_ resource.ResourceWithValidateConfig = &PaperclipProjectResource{}
	_ resource.ResourceWithModifyPlan     = &PaperclipProjectResource{}
)

func NewPaperclipProjectResource() resource.Resource {
	return &PaperclipProjectResource{}
}

type PaperclipProjectResource struct {
	client *client.Client
}

type PaperclipProjectResourceModel struct {
	ID                       types.String               `tfsdk:"id"`
	CompanyID                types.String               `tfsdk:"company_id"`
	Name                     types.String               `tfsdk:"name"`
	Description              types.String               `tfsdk:"description"`
	ExecutionWorkspacePolicy types.Object               `tfsdk:"execution_workspace_policy"`
	Status                   types.String               `tfsdk:"status"`
	GoalIDs                  types.Set                  `tfsdk:"goal_ids"`
	TargetDate               types.String               `tfsdk:"target_date"`
	Color                    types.String               `tfsdk:"color"`
	Icon                     types.String               `tfsdk:"icon"`
	Env                      map[string]EnvBindingModel `tfsdk:"env"`
	ArchivedAt               types.String               `tfsdk:"archived_at"`
	ArchiveOnDestroy         types.Bool                 `tfsdk:"archive_on_destroy"`
}

func (r *PaperclipProjectResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project"
}

func (r *PaperclipProjectResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:     2,
		Description: "Manages native project configuration. The lead belongs to paperclip_project_lead. Environment bindings clear with an empty map. environment_id in execution_workspace_policy is stored but does not select run environments. Authorization trust keys are rejected because the native assignment evaluator cannot interpret them. archive_on_destroy selects archival instead of hard deletion.",
		Blocks:      map[string]schema.Block{"execution_workspace_policy": policyBlock(projectPolicyFields)},
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
			},
			"status":             schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("backlog"), Description: "Native project status.", Validators: []validator.String{stringOneOfValidator{allowed: []string{"backlog", "planned", "in_progress", "completed", "cancelled"}}}},
			"goal_ids":           schema.SetAttribute{Optional: true, ElementType: types.StringType, Description: "Associated native goal IDs in the same company; null or an empty set clears associations."},
			"target_date":        schema.StringAttribute{Optional: true, Description: "Native target date string; null clears it."},
			"color":              schema.StringAttribute{Optional: true, Description: "Native project color; null clears it."},
			"icon":               schema.StringAttribute{Optional: true, Description: "Native project icon name; null clears it.", Validators: []validator.String{stringOneOfValidator{allowed: []string{"folder", "rocket", "code", "terminal", "database", "globe", "package", "boxes", "box", "layers", "briefcase", "compass", "target", "flame", "zap", "star", "bug", "wrench", "hammer", "lightbulb", "sparkles", "shield", "lock", "search", "cog", "brain", "cpu", "git-branch", "file-code", "puzzle", "gem", "atom", "heart", "mail", "message-square", "crown", "radar", "telescope", "hexagon"}}}},
			"env":                EnvBindingMapAttribute("Native project environment bindings; null or {} clears all bindings via the API empty object."),
			"archived_at":        schema.StringAttribute{Optional: true, Description: "UTC RFC3339 archive timestamp at native millisecond precision. Configure after creating the project; null unarchives it.", PlanModifiers: []planmodifier.String{projectArchiveTimePlanModifier{}}},
			"archive_on_destroy": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), Description: "Archive on destroy instead of native hard deletion. Defaults to false, preserving existing resource retirement behavior."},
		},
	}
}

// Version zero also owned lead_agent_id. Upgrade only the stored project shape;
// the separate lead resource adopts the unchanged assignment from Paperclip.
func (r *PaperclipProjectResource) UpgradeState(ctx context.Context) map[int64]resource.StateUpgrader {
	upgrader := resource.StateUpgrader{StateUpgrader: func(ctx context.Context, req resource.UpgradeStateRequest, resp *resource.UpgradeStateResponse) {
		var prior map[string]json.RawMessage
		if err := json.Unmarshal(req.RawState.JSON, &prior); err != nil {
			resp.Diagnostics.AddError("Invalid project state", err.Error())
			return
		}
		delete(prior, "lead_agent_id")
		var policyString *string
		if err := json.Unmarshal(prior["execution_workspace_policy"], &policyString); err != nil {
			resp.Diagnostics.AddError("Invalid project policy state", err.Error())
			return
		}
		var remote map[string]interface{}
		if policyString != nil {
			var err error
			remote, err = nativeJSONObject(types.StringValue(*policyString))
			if err != nil {
				resp.Diagnostics.AddError("Invalid project policy state", err.Error())
				return
			}
		}
		policy, ds := policyFromAPI(projectPolicyFields, remote)
		resp.Diagnostics.Append(ds...)
		if resp.Diagnostics.HasError() {
			return
		}
		prior["execution_workspace_policy"] = json.RawMessage("null")
		raw, _ := json.Marshal(prior)
		value, err := (&tfprotov6.RawState{JSON: raw}).Unmarshal(resp.State.Schema.Type().TerraformType(ctx))
		if err != nil {
			resp.Diagnostics.AddError("Invalid project state", err.Error())
			return
		}
		resp.State.Raw = value
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("execution_workspace_policy"), policy)...)
	}}
	return map[int64]resource.StateUpgrader{0: upgrader, 1: upgrader}
}

func projectStringPointer(v types.String) *string {
	if v.IsNull() {
		return nil
	}
	s := v.ValueString()
	return &s
}
func projectStringValue(v *string) types.String {
	if v == nil {
		return types.StringNull()
	}
	return types.StringValue(*v)
}

func projectRequest(ctx context.Context, model PaperclipProjectResourceModel) client.PaperclipProject {
	var goals []string
	_ = model.GoalIDs.ElementsAs(ctx, &goals, false)
	return client.PaperclipProject{Name: model.Name.ValueString(), Description: projectStringPointer(model.Description), Status: model.Status.ValueString(), GoalIDs: goals, TargetDate: projectStringPointer(model.TargetDate), Color: projectStringPointer(model.Color), Icon: projectStringPointer(model.Icon), ArchivedAt: projectStringPointer(model.ArchivedAt), Env: EnvBindingsMapToAPI(model.Env), ExecutionWorkspacePolicy: policyToAPI(model.ExecutionWorkspacePolicy, projectPolicyFields)}
}

func refreshProject(ctx context.Context, model *PaperclipProjectResourceModel, p *client.PaperclipProject) diag.Diagnostics {
	if model.ArchiveOnDestroy.IsNull() || model.ArchiveOnDestroy.IsUnknown() {
		model.ArchiveOnDestroy = types.BoolValue(false)
	}
	model.ID = types.StringValue(p.ID)
	if p.CompanyID != "" {
		model.CompanyID = types.StringValue(p.CompanyID)
	}
	model.Name = types.StringValue(p.Name)
	model.Description = projectStringValue(p.Description)
	model.Status = types.StringValue(p.Status)
	model.TargetDate, model.Color, model.Icon = projectStringValue(p.TargetDate), projectStringValue(p.Color), projectStringValue(p.Icon)
	archivedAt := projectStringValue(p.ArchivedAt)
	if !sameProjectArchiveTime(model.ArchivedAt, archivedAt) {
		model.ArchivedAt = archivedAt
	}
	if len(p.GoalIDs) == 0 && model.GoalIDs.IsNull() {
		model.GoalIDs = types.SetNull(types.StringType)
	} else {
		model.GoalIDs, _ = types.SetValueFrom(ctx, types.StringType, p.GoalIDs)
	}
	model.Env = refreshProjectEnv(model.Env, p.Env)
	policy, ds := policyFromAPI(projectPolicyFields, p.ExecutionWorkspacePolicy)
	model.ExecutionWorkspacePolicy = preservePolicyJSON(model.ExecutionWorkspacePolicy, policy, projectPolicyFields)
	return ds
}

func validateProjectConfig(model PaperclipProjectResourceModel) diag.Diagnostics {
	ds := ValidateEnvBindingsMapPlan(path.Root("env"), model.Env, "project")
	if !model.Name.IsNull() && !model.Name.IsUnknown() && (strings.TrimSpace(model.Name.ValueString()) == "" || strings.TrimSpace(model.Name.ValueString()) != model.Name.ValueString()) {
		ds.AddError("Invalid project name", "name must be non-empty and trimmed so the native project name can round-trip exactly")
	}
	if !model.ArchivedAt.IsNull() && !model.ArchivedAt.IsUnknown() {
		if stamp, err := time.Parse(time.RFC3339Nano, model.ArchivedAt.ValueString()); err != nil || !strings.HasSuffix(model.ArchivedAt.ValueString(), "Z") || stamp.Nanosecond()%1000000 != 0 {
			ds.AddError("Invalid project archive timestamp", "archived_at must be a UTC RFC3339 timestamp ending in Z at millisecond precision, or null to unarchive")
		}
	}
	if !model.TargetDate.IsNull() && !model.TargetDate.IsUnknown() {
		if _, err := time.Parse("2006-01-02", model.TargetDate.ValueString()); err != nil {
			ds.AddError("Invalid project target date", "target_date must be a calendar date in YYYY-MM-DD format, or null to clear it")
		}
	}
	ds.Append(validatePolicyObject(model.ExecutionWorkspacePolicy, projectPolicyFields)...)
	return ds
}

func sameProjectArchiveTime(a, b types.String) bool {
	if a.IsNull() || b.IsNull() || a.IsUnknown() || b.IsUnknown() {
		return false
	}
	left, errLeft := time.Parse(time.RFC3339Nano, a.ValueString())
	right, errRight := time.Parse(time.RFC3339Nano, b.ValueString())
	return errLeft == nil && errRight == nil && left.Equal(right)
}

type projectArchiveTimePlanModifier struct{}

func (projectArchiveTimePlanModifier) Description(context.Context) string {
	return "compares native archive timestamps by instant"
}
func (m projectArchiveTimePlanModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}
func (projectArchiveTimePlanModifier) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if sameProjectArchiveTime(req.PlanValue, req.StateValue) {
		resp.PlanValue = req.StateValue
	}
}

func validateProjectCreateArchive(value types.String) error {
	if !value.IsNull() && !value.IsUnknown() {
		return fmt.Errorf("archived_at cannot be configured during creation: the current Paperclip create API does not convert archive timestamps. Create the project first, then set archived_at in an update, or select archive_on_destroy")
	}
	return nil
}

func (r *PaperclipProjectResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config PaperclipProjectResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateProjectConfig(config)...)
}

func (r *PaperclipProjectResource) validateReferences(ctx context.Context, model PaperclipProjectResourceModel) diag.Diagnostics {
	var ds diag.Diagnostics
	for _, value := range model.GoalIDs.Elements() {
		goalID := value.(types.String)
		if goalID.IsUnknown() {
			continue
		}
		goal, err := r.client.GetGoal(ctx, goalID.ValueString())
		if err != nil {
			ds.AddError("Invalid project goal reference", err.Error())
		} else if goal.CompanyID != model.CompanyID.ValueString() {
			ds.AddError("Invalid project goal reference", "goal_ids must belong to the project company")
		}
	}
	ds.Append(ValidateEnvBindingsApply(ctx, r.client, model.CompanyID.ValueString(), model.Env)...)
	if !model.ExecutionWorkspacePolicy.IsNull() && !model.ExecutionWorkspacePolicy.IsUnknown() {
		attrs := model.ExecutionWorkspacePolicy.Attributes()
		if value := attrs["environment_id"].(types.String); !value.IsNull() && !value.IsUnknown() {
			environment, err := r.client.GetEnvironment(ctx, value.ValueString())
			if err != nil {
				ds.AddError("Invalid project environment reference", err.Error())
			} else if environment.Status == "archived" || (environment.Driver != "local" && environment.Driver != "ssh" && environment.Driver != "sandbox") || (environment.Driver == "sandbox" && environment.Config["provider"] == "fake") {
				ds.AddError("Invalid project environment reference", "environment_id must identify a non-archived local, ssh or sandbox environment; the probe-only fake sandbox provider cannot execute runs")
			}
		}
		if value := attrs["default_project_workspace_id"].(types.String); !value.IsNull() && !value.IsUnknown() {
			if model.ID.ValueString() == "" {
				ds.AddError("Invalid project workspace reference", "default_project_workspace_id requires an existing workspace in this project; create the project and workspace before setting this policy")
			} else if _, err := r.client.GetWorkspaceInProject(ctx, model.ID.ValueString(), value.ValueString()); err != nil {
				ds.AddError("Invalid project workspace reference", err.Error())
			}
		}
	}
	return ds
}

func (r *PaperclipProjectResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || r.client == nil {
		return
	}
	var plan PaperclipProjectResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if req.State.Raw.IsNull() {
		if err := validateProjectCreateArchive(plan.ArchivedAt); err != nil {
			resp.Diagnostics.AddError("Unsupported project creation archive", err.Error())
			return
		}
	}
	if !plan.CompanyID.IsUnknown() {
		resp.Diagnostics.Append(r.validateReferences(ctx, plan)...)
	}
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
	resp.Diagnostics.Append(validateProjectConfig(plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := validateProjectCreateArchive(plan.ArchivedAt); err != nil {
		resp.Diagnostics.AddError("Unsupported project creation archive", err.Error())
		return
	}
	resp.Diagnostics.Append(r.validateReferences(ctx, plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	proj, err := r.client.CreateProject(
		ctx,
		plan.CompanyID.ValueString(),
		projectRequest(ctx, plan),
	)
	if err != nil {
		resp.Diagnostics.AddError("Error creating Paperclip project", err.Error())
		return
	}

	resp.Diagnostics.Append(refreshProject(ctx, &plan, proj)...)

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

	resp.Diagnostics.Append(refreshProject(ctx, &state, proj)...)
	if resp.Diagnostics.HasError() {
		return
	}

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
	resp.Diagnostics.Append(validateProjectConfig(plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(r.validateReferences(ctx, plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	proj, err := r.client.UpdateProject(
		ctx,
		state.ID.ValueString(),
		projectRequest(ctx, plan),
	)
	if err != nil {
		resp.Diagnostics.AddError("Error updating Paperclip project", err.Error())
		return
	}

	resp.Diagnostics.Append(refreshProject(ctx, &plan, proj)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipProjectResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PaperclipProjectResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var err error
	if state.ArchiveOnDestroy.ValueBool() {
		err = r.client.ArchiveProject(ctx, state.ID.ValueString(), time.Now().UTC().Truncate(time.Millisecond).Format(time.RFC3339Nano))
	} else {
		err = r.client.DeleteProject(ctx, state.ID.ValueString())
	}
	if err != nil {
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
