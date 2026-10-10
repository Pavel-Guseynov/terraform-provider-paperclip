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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &PaperclipGoalResource{}
	_ resource.ResourceWithConfigure   = &PaperclipGoalResource{}
	_ resource.ResourceWithImportState = &PaperclipGoalResource{}
	_ resource.ResourceWithModifyPlan  = &PaperclipGoalResource{}
)

func NewPaperclipGoalResource() resource.Resource {
	return &PaperclipGoalResource{}
}

type PaperclipGoalResource struct {
	client *client.Client
}

type PaperclipGoalResourceModel struct {
	ID              types.String `tfsdk:"id"`
	CompanyID       types.String `tfsdk:"company_id"`
	Title           types.String `tfsdk:"title"`
	Description     types.String `tfsdk:"description"`
	Level           types.String `tfsdk:"level"`
	Status          types.String `tfsdk:"status"`
	ParentID        types.String `tfsdk:"parent_id"`
	OwnerAgentID    types.String `tfsdk:"owner_agent_id"`
	CancelOnDestroy types.Bool   `tfsdk:"cancel_on_destroy"`
	Retirement      types.String `tfsdk:"retirement"`
}

func (r *PaperclipGoalResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_goal"
}

func (r *PaperclipGoalResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a Paperclip goal entity and its hierarchy under a company.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Unique identifier for the goal.",
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
			"title": schema.StringAttribute{
				Description: "Goal title.",
				Required:    true,
			},
			"description": schema.StringAttribute{
				Description: "Optional goal description. Explicitly clearable to null.",
				Optional:    true,
			},
			"level": schema.StringAttribute{
				Description: "Goal level: company, team, agent, or task (defaults to task).",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("task"),
				Validators: []validator.String{
					stringOneOfValidator{allowed: []string{"company", "team", "agent", "task"}},
				},
			},
			"status": schema.StringAttribute{
				Description: "Goal status: planned, active, achieved, or cancelled (defaults to planned).",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("planned"),
				Validators: []validator.String{
					stringOneOfValidator{allowed: []string{"planned", "active", "achieved", "cancelled"}},
				},
			},
			"parent_id": schema.StringAttribute{
				Description: "Optional parent goal identifier for hierarchy relationships.",
				Optional:    true,
			},
			"owner_agent_id": schema.StringAttribute{
				Description: "Optional owner agent identifier.",
				Optional:    true,
			},
			"cancel_on_destroy": schema.BoolAttribute{
				Description: "Whether to cancel the goal (status = cancelled) when destroyed if still referenced (defaults to false).",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
			},
			"retirement": schema.StringAttribute{
				Description: "Retirement behavior when destroying a referenced goal: 'fail' (default) or 'cancel'.",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("fail"),
				Validators: []validator.String{
					stringOneOfValidator{allowed: []string{"fail", "cancel", "delete"}},
				},
			},
		},
	}
}

func (r *PaperclipGoalResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *PaperclipGoalResource) validateGoalHierarchy(ctx context.Context, goalID, companyID, parentID, ownerAgentID string) error {
	if r.client == nil {
		return nil
	}
	if parentID != "" {
		if goalID != "" && parentID == goalID {
			return fmt.Errorf("goal cannot be its own parent: goal %s has parent_id %s", goalID, parentID)
		}
		parent, err := r.client.GetGoal(ctx, parentID)
		if err != nil {
			return fmt.Errorf("failed to validate parent goal %s: %w", parentID, err)
		}
		if parent.CompanyID != companyID {
			return fmt.Errorf("parent goal %s belongs to company %s, expected %s", parentID, parent.CompanyID, companyID)
		}

		// Transitive cycle check
		curr := parent.ParentID
		depth := 0
		seen := map[string]bool{parentID: true}
		if goalID != "" {
			seen[goalID] = true
		}
		for curr != "" && depth < 100 {
			if goalID != "" && curr == goalID {
				return fmt.Errorf("goal hierarchy cycle detected: goal %s cannot have parent %s as it creates a cycle", goalID, parentID)
			}
			if seen[curr] {
				return fmt.Errorf("goal hierarchy cycle detected: cycle encountered at goal %s", curr)
			}
			seen[curr] = true
			ancestor, err := r.client.GetGoal(ctx, curr)
			if err != nil {
				return fmt.Errorf("failed to validate ancestor goal %s: %w", curr, err)
			}
			if ancestor.CompanyID != companyID {
				return fmt.Errorf("ancestor goal %s belongs to company %s, expected %s", curr, ancestor.CompanyID, companyID)
			}
			curr = ancestor.ParentID
			depth++
		}
		if curr != "" && depth >= 100 {
			return fmt.Errorf("goal hierarchy validation exceeded maximum depth (%d) at ancestor %s without reaching root", 100, curr)
		}
	}

	if ownerAgentID != "" {
		agent, err := r.client.GetAgent(ctx, ownerAgentID)
		if err != nil {
			return fmt.Errorf("failed to validate owner agent %s: %w", ownerAgentID, err)
		}
		if agent.CompanyID != companyID {
			return fmt.Errorf("owner agent %s belongs to company %s, expected %s", ownerAgentID, agent.CompanyID, companyID)
		}
	}

	return nil
}

func (r *PaperclipGoalResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan PaperclipGoalResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Synchronize retirement and cancel_on_destroy
	if !plan.CancelOnDestroy.IsNull() && !plan.CancelOnDestroy.IsUnknown() && plan.CancelOnDestroy.ValueBool() {
		if plan.Retirement.IsNull() || plan.Retirement.IsUnknown() || plan.Retirement.ValueString() == "fail" {
			plan.Retirement = types.StringValue("cancel")
		}
	} else if !plan.Retirement.IsNull() && !plan.Retirement.IsUnknown() && plan.Retirement.ValueString() == "cancel" {
		plan.CancelOnDestroy = types.BoolValue(true)
	}

	if plan.CompanyID.IsUnknown() {
		resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
		return
	}

	cid := plan.CompanyID.ValueString()
	gid := ""
	if !plan.ID.IsNull() && !plan.ID.IsUnknown() {
		gid = plan.ID.ValueString()
	}
	pid := ""
	if !plan.ParentID.IsNull() && !plan.ParentID.IsUnknown() {
		pid = plan.ParentID.ValueString()
	}
	oid := ""
	if !plan.OwnerAgentID.IsNull() && !plan.OwnerAgentID.IsUnknown() {
		oid = plan.OwnerAgentID.ValueString()
	}

	if pid != "" || oid != "" {
		if err := r.validateGoalHierarchy(ctx, gid, cid, pid, oid); err != nil {
			resp.Diagnostics.AddAttributeError(
				path.Root("parent_id"),
				"Invalid Goal Hierarchy",
				err.Error(),
			)
			return
		}
	}

	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
}

func (r *PaperclipGoalResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PaperclipGoalResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cid := plan.CompanyID.ValueString()
	pid := ""
	if !plan.ParentID.IsNull() && !plan.ParentID.IsUnknown() {
		pid = plan.ParentID.ValueString()
	}
	oid := ""
	if !plan.OwnerAgentID.IsNull() && !plan.OwnerAgentID.IsUnknown() {
		oid = plan.OwnerAgentID.ValueString()
	}

	// Validate read-only before the first mutation
	if err := r.validateGoalHierarchy(ctx, "", cid, pid, oid); err != nil {
		resp.Diagnostics.AddError("Invalid Goal Hierarchy", err.Error())
		return
	}

	goalReq := client.PaperclipGoal{
		Title:  plan.Title.ValueString(),
		Level:  plan.Level.ValueString(),
		Status: plan.Status.ValueString(),
	}
	if !plan.Description.IsNull() && !plan.Description.IsUnknown() {
		goalReq.Description = plan.Description.ValueString()
	}
	if pid != "" {
		goalReq.ParentID = pid
	}
	if oid != "" {
		goalReq.OwnerAgentID = oid
	}

	created, err := r.client.CreateGoal(ctx, cid, goalReq)
	if err != nil {
		resp.Diagnostics.AddError("Error creating Paperclip goal", err.Error())
		return
	}

	plan.ID = types.StringValue(created.ID)
	plan.CompanyID = types.StringValue(created.CompanyID)
	plan.Title = types.StringValue(created.Title)
	plan.Description = stringOrNull(created.Description)
	if created.Level != "" {
		plan.Level = types.StringValue(created.Level)
	}
	if created.Status != "" {
		plan.Status = types.StringValue(created.Status)
	}
	plan.ParentID = stringOrNull(created.ParentID)
	plan.OwnerAgentID = stringOrNull(created.OwnerAgentID)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipGoalResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PaperclipGoalResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	goal, err := r.client.GetGoal(ctx, state.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Paperclip goal", err.Error())
		return
	}

	state.ID = types.StringValue(goal.ID)
	state.CompanyID = types.StringValue(goal.CompanyID)
	state.Title = types.StringValue(goal.Title)
	state.Description = stringOrNull(goal.Description)
	if goal.Level != "" {
		state.Level = types.StringValue(goal.Level)
	}
	if goal.Status != "" {
		state.Status = types.StringValue(goal.Status)
	}
	state.ParentID = stringOrNull(goal.ParentID)
	state.OwnerAgentID = stringOrNull(goal.OwnerAgentID)

	if state.CancelOnDestroy.IsNull() || state.CancelOnDestroy.IsUnknown() {
		state.CancelOnDestroy = types.BoolValue(false)
	}
	if state.Retirement.IsNull() || state.Retirement.IsUnknown() {
		state.Retirement = types.StringValue("fail")
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PaperclipGoalResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan PaperclipGoalResourceModel
	var state PaperclipGoalResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	gid := state.ID.ValueString()
	cid := plan.CompanyID.ValueString()
	pid := ""
	if !plan.ParentID.IsNull() && !plan.ParentID.IsUnknown() {
		pid = plan.ParentID.ValueString()
	}
	oid := ""
	if !plan.OwnerAgentID.IsNull() && !plan.OwnerAgentID.IsUnknown() {
		oid = plan.OwnerAgentID.ValueString()
	}

	// Validate read-only before mutation
	if err := r.validateGoalHierarchy(ctx, gid, cid, pid, oid); err != nil {
		resp.Diagnostics.AddError("Invalid Goal Hierarchy", err.Error())
		return
	}

	patch := make(map[string]interface{})
	patch["title"] = plan.Title.ValueString()
	if plan.Description.IsNull() {
		patch["description"] = nil
	} else {
		patch["description"] = plan.Description.ValueString()
	}
	if !plan.Level.IsNull() && !plan.Level.IsUnknown() {
		patch["level"] = plan.Level.ValueString()
	}
	if !plan.Status.IsNull() && !plan.Status.IsUnknown() {
		patch["status"] = plan.Status.ValueString()
	}
	if plan.ParentID.IsNull() {
		patch["parentId"] = nil
	} else {
		patch["parentId"] = plan.ParentID.ValueString()
	}
	if plan.OwnerAgentID.IsNull() {
		patch["ownerAgentId"] = nil
	} else {
		patch["ownerAgentId"] = plan.OwnerAgentID.ValueString()
	}

	updated, err := r.client.UpdateGoal(ctx, gid, patch)
	if err != nil {
		resp.Diagnostics.AddError("Error updating Paperclip goal", err.Error())
		return
	}

	plan.ID = types.StringValue(updated.ID)
	plan.CompanyID = types.StringValue(updated.CompanyID)
	plan.Title = types.StringValue(updated.Title)
	plan.Description = stringOrNull(updated.Description)
	if updated.Level != "" {
		plan.Level = types.StringValue(updated.Level)
	}
	if updated.Status != "" {
		plan.Status = types.StringValue(updated.Status)
	}
	plan.ParentID = stringOrNull(updated.ParentID)
	plan.OwnerAgentID = stringOrNull(updated.OwnerAgentID)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipGoalResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PaperclipGoalResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	gid := state.ID.ValueString()
	cid := state.CompanyID.ValueString()

	// Check if the goal is still referenced by other child goals
	isReferenced := false
	goals, err := r.client.ListGoals(ctx, cid)
	if err == nil {
		for _, g := range goals {
			if g.ParentID == gid {
				isReferenced = true
				break
			}
		}
	}

	shouldCancel := state.CancelOnDestroy.ValueBool() || state.Retirement.ValueString() == "cancel"

	if isReferenced {
		if shouldCancel {
			_, updateErr := r.client.UpdateGoal(ctx, gid, map[string]interface{}{"status": "cancelled"})
			if updateErr != nil {
				resp.Diagnostics.AddError("Error cancelling referenced Paperclip goal", updateErr.Error())
			}
			return
		}
		resp.Diagnostics.AddError(
			"Goal Is Referenced",
			fmt.Sprintf("cannot delete goal %s (%q): goal is still referenced by child goals. Retiring a goal that is still referenced requires an explicit documented choice: set cancel_on_destroy = true or retirement = \"cancel\" to cancel it on destroy, or remove referencing goals first.", gid, state.Title.ValueString()),
		)
		return
	}

	deleteErr := r.client.DeleteGoal(ctx, gid)
	if deleteErr != nil {
		if shouldCancel {
			_, updateErr := r.client.UpdateGoal(ctx, gid, map[string]interface{}{"status": "cancelled"})
			if updateErr != nil {
				resp.Diagnostics.AddError("Error cancelling referenced Paperclip goal after failed delete", deleteErr.Error())
			}
			return
		}
		resp.Diagnostics.AddError(
			"Goal Deletion Error",
			fmt.Sprintf("cannot delete goal %s (%q): %s. If this goal is still referenced by other entities (projects, issues, or child goals), set cancel_on_destroy = true or retirement = \"cancel\" to cancel it instead of deleting.", gid, state.Title.ValueString(), deleteErr.Error()),
		)
	}
}

func (r *PaperclipGoalResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
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
		"Invalid Goal Import ID",
		fmt.Sprintf("Expected <company_id>/<goal_id> or <goal_id>, got %q", id),
	)
}
