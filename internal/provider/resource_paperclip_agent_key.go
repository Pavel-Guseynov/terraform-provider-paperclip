package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"git.guseynov.org/guseynov/paperclip/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &PaperclipAgentKeyResource{}
	_ resource.ResourceWithConfigure   = &PaperclipAgentKeyResource{}
	_ resource.ResourceWithImportState = &PaperclipAgentKeyResource{}
	_ resource.ResourceWithModifyPlan  = &PaperclipAgentKeyResource{}
)

func NewPaperclipAgentKeyResource() resource.Resource {
	return &PaperclipAgentKeyResource{}
}

type PaperclipAgentKeyResource struct {
	client *client.Client
}

type AgentKeyScopeModel struct {
	Kind                    types.String `tfsdk:"kind"`
	ProjectID               types.String `tfsdk:"project_id"`
	ProjectIDs              types.List   `tfsdk:"project_ids"`
	ParentIssueID           types.String `tfsdk:"parent_issue_id"`
	ParentIssueIDs          types.List   `tfsdk:"parent_issue_ids"`
	AllowedAssigneeAgentIDs types.List   `tfsdk:"allowed_assignee_agent_ids"`
	IssueID                 types.String `tfsdk:"issue_id"`
}

type PaperclipAgentKeyResourceModel struct {
	ID            types.String        `tfsdk:"id"`
	AgentID       types.String        `tfsdk:"agent_id"`
	Name          types.String        `tfsdk:"name"`
	Generation    types.Int64         `tfsdk:"generation"`
	Scope         *AgentKeyScopeModel `tfsdk:"scope"`
	KeyID         types.String        `tfsdk:"key_id"`
	DeliveryState types.String        `tfsdk:"delivery_state"`
}

func scopeModelToClient(ctx context.Context, model *AgentKeyScopeModel) (*client.AgentKeyScope, error) {
	if model == nil {
		return &client.AgentKeyScope{Kind: "standard"}, nil
	}
	kind := "standard"
	if !model.Kind.IsNull() && !model.Kind.IsUnknown() && model.Kind.ValueString() != "" {
		kind = model.Kind.ValueString()
	}
	scope := &client.AgentKeyScope{
		Kind: kind,
	}
	if !model.ProjectID.IsNull() && !model.ProjectID.IsUnknown() {
		scope.ProjectID = model.ProjectID.ValueString()
	}
	if !model.ProjectIDs.IsNull() && !model.ProjectIDs.IsUnknown() {
		var list []string
		_ = model.ProjectIDs.ElementsAs(ctx, &list, false)
		scope.ProjectIDs = list
	}
	if !model.ParentIssueID.IsNull() && !model.ParentIssueID.IsUnknown() {
		scope.ParentIssueID = model.ParentIssueID.ValueString()
	}
	if !model.ParentIssueIDs.IsNull() && !model.ParentIssueIDs.IsUnknown() {
		var list []string
		_ = model.ParentIssueIDs.ElementsAs(ctx, &list, false)
		scope.ParentIssueIDs = list
	}
	if !model.AllowedAssigneeAgentIDs.IsNull() && !model.AllowedAssigneeAgentIDs.IsUnknown() {
		var list []string
		_ = model.AllowedAssigneeAgentIDs.ElementsAs(ctx, &list, false)
		scope.AllowedAssigneeAgentIDs = list
	}
	if !model.IssueID.IsNull() && !model.IssueID.IsUnknown() {
		scope.IssueID = model.IssueID.ValueString()
	}

	if kind == "task_bridge" {
		hasProject := scope.ProjectID != "" || len(scope.ProjectIDs) > 0
		hasParent := scope.ParentIssueID != "" || len(scope.ParentIssueIDs) > 0
		if !hasProject && !hasParent {
			return nil, errors.New("task_bridge keys require at least one project or parent issue boundary")
		}
	} else if kind == "skill_test" {
		if scope.IssueID == "" {
			return nil, errors.New("skill_test keys require an issue_id boundary")
		}
	}
	return scope, nil
}

func clientScopeToModel(ctx context.Context, scope *client.AgentKeyScope, current *AgentKeyScopeModel) (*AgentKeyScopeModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	if scope == nil {
		return current, diags
	}
	res := &AgentKeyScopeModel{
		Kind: types.StringValue(scope.Kind),
	}
	if scope.ProjectID != "" {
		res.ProjectID = types.StringValue(scope.ProjectID)
	} else {
		res.ProjectID = types.StringNull()
	}
	if len(scope.ProjectIDs) > 0 {
		l, d := types.ListValueFrom(ctx, types.StringType, scope.ProjectIDs)
		diags.Append(d...)
		res.ProjectIDs = l
	} else {
		res.ProjectIDs = types.ListNull(types.StringType)
	}
	if scope.ParentIssueID != "" {
		res.ParentIssueID = types.StringValue(scope.ParentIssueID)
	} else {
		res.ParentIssueID = types.StringNull()
	}
	if len(scope.ParentIssueIDs) > 0 {
		l, d := types.ListValueFrom(ctx, types.StringType, scope.ParentIssueIDs)
		diags.Append(d...)
		res.ParentIssueIDs = l
	} else {
		res.ParentIssueIDs = types.ListNull(types.StringType)
	}
	if len(scope.AllowedAssigneeAgentIDs) > 0 {
		l, d := types.ListValueFrom(ctx, types.StringType, scope.AllowedAssigneeAgentIDs)
		diags.Append(d...)
		res.AllowedAssigneeAgentIDs = l
	} else {
		res.AllowedAssigneeAgentIDs = types.ListNull(types.StringType)
	}
	if scope.IssueID != "" {
		res.IssueID = types.StringValue(scope.IssueID)
	} else {
		res.IssueID = types.StringNull()
	}
	return res, diags
}

func (r *PaperclipAgentKeyResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_agent_key"
}

func (r *PaperclipAgentKeyResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the durable, non-secret lifecycle record of a Paperclip agent API key.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "OpenTofu resource identifier matching the Paperclip key ID.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"agent_id": schema.StringAttribute{
				Description: "Paperclip agent identifier.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Description: "Agent key label.",
				Required:    true,
			},
			"generation": schema.Int64Attribute{
				Description: "Declared generation. Changing generation mints a new key, delivers it, and revokes the superseded key.",
				Required:    true,
			},
			"scope": schema.SingleNestedAttribute{
				Description: "Native agent key scope: standard, task_bridge, or skill_test.",
				Optional:    true,
				Computed:    true,
				Attributes: map[string]schema.Attribute{
					"kind": schema.StringAttribute{
						Description: "Scope kind: standard, task_bridge, or skill_test.",
						Optional:    true,
						Computed:    true,
					},
					"project_id": schema.StringAttribute{
						Description: "Primary project boundary for task_bridge scope.",
						Optional:    true,
					},
					"project_ids": schema.ListAttribute{
						Description: "Additional project boundaries for task_bridge scope.",
						Optional:    true,
						ElementType: types.StringType,
					},
					"parent_issue_id": schema.StringAttribute{
						Description: "Primary parent issue boundary for task_bridge scope.",
						Optional:    true,
					},
					"parent_issue_ids": schema.ListAttribute{
						Description: "Additional parent issue boundaries for task_bridge scope.",
						Optional:    true,
						ElementType: types.StringType,
					},
					"allowed_assignee_agent_ids": schema.ListAttribute{
						Description: "Allowed assignee agent boundaries for task_bridge scope.",
						Optional:    true,
						ElementType: types.StringType,
					},
					"issue_id": schema.StringAttribute{
						Description: "Issue boundary for skill_test scope.",
						Optional:    true,
					},
				},
			},
			"key_id": schema.StringAttribute{
				Description: "Paperclip agent key identifier.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"delivery_state": schema.StringAttribute{
				Description: "Lifecycle delivery state (e.g. delivered).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *PaperclipAgentKeyResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *PaperclipAgentKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PaperclipAgentKeyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	scope, err := scopeModelToClient(ctx, plan.Scope)
	if err != nil {
		resp.Diagnostics.AddError("Invalid agent key scope", err.Error())
		return
	}

	key, err := r.client.CreateAgentKey(ctx, plan.AgentID.ValueString(), plan.Name.ValueString(), scope)
	if err != nil {
		resp.Diagnostics.AddError("Error creating Paperclip agent key", err.Error())
		return
	}

	r.client.RegisterMintedAgentKey(plan.AgentID.ValueString(), key.ID, key.Token)

	plan.ID = types.StringValue(key.ID)
	plan.KeyID = types.StringValue(key.ID)
	plan.DeliveryState = types.StringValue("pending")

	if key.Scope != nil {
		modelScope, d := clientScopeToModel(ctx, key.Scope, plan.Scope)
		resp.Diagnostics.Append(d...)
		plan.Scope = modelScope
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipAgentKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PaperclipAgentKeyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	keyID := state.KeyID.ValueString()
	if keyID == "" {
		keyID = state.ID.ValueString()
	}
	if keyID == "" || state.AgentID.ValueString() == "" {
		resp.State.RemoveResource(ctx)
		return
	}

	key, err := r.client.GetAgentKey(ctx, state.AgentID.ValueString(), keyID)
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Paperclip agent key", err.Error())
		return
	}

	state.ID = types.StringValue(key.ID)
	state.KeyID = types.StringValue(key.ID)
	state.Name = types.StringValue(key.Name)
	if key.Scope != nil {
		modelScope, d := clientScopeToModel(ctx, key.Scope, state.Scope)
		resp.Diagnostics.Append(d...)
		state.Scope = modelScope
	}
	if r.client.IsTokenDelivered(keyID) {
		state.DeliveryState = types.StringValue("delivered")
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PaperclipAgentKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan PaperclipAgentKeyResourceModel
	var state PaperclipAgentKeyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	oldKeyID := state.KeyID.ValueString()
	if oldKeyID == "" {
		oldKeyID = state.ID.ValueString()
	}

	shouldRotate := !plan.Generation.Equal(state.Generation) || plan.KeyID.IsUnknown()

	if shouldRotate {
		scope, err := scopeModelToClient(ctx, plan.Scope)
		if err != nil {
			resp.Diagnostics.AddError("Invalid agent key scope", err.Error())
			return
		}

		newKey, err := r.client.CreateAgentKey(ctx, plan.AgentID.ValueString(), plan.Name.ValueString(), scope)
		if err != nil {
			resp.Diagnostics.AddError("Error rotating Paperclip agent key", err.Error())
			return
		}

		r.client.RegisterMintedAgentKey(plan.AgentID.ValueString(), newKey.ID, newKey.Token)
		if oldKeyID != "" && oldKeyID != newKey.ID {
			r.client.RecordSupersededKey(newKey.ID, oldKeyID, false, plan.AgentID.ValueString())
		}

		plan.ID = types.StringValue(newKey.ID)
		plan.KeyID = types.StringValue(newKey.ID)
		plan.DeliveryState = types.StringValue("pending")
		if newKey.Scope != nil {
			modelScope, d := clientScopeToModel(ctx, newKey.Scope, plan.Scope)
			resp.Diagnostics.Append(d...)
			plan.Scope = modelScope
		}

		resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
		return
	}

	plan.ID = state.ID
	plan.KeyID = state.KeyID
	plan.DeliveryState = state.DeliveryState
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipAgentKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PaperclipAgentKeyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	keyID := state.KeyID.ValueString()
	if keyID == "" {
		keyID = state.ID.ValueString()
	}
	if keyID != "" && state.AgentID.ValueString() != "" {
		if err := r.client.RevokeAgentKey(ctx, state.AgentID.ValueString(), keyID); err != nil {
			resp.Diagnostics.AddError("Error revoking Paperclip agent key", err.Error())
			return
		}
	}
}

func (r *PaperclipAgentKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := req.ID
	separator := "/"
	if !strings.Contains(id, "/") && strings.Contains(id, ":") {
		separator = ":"
	}
	parts := strings.SplitN(id, separator, 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError("Invalid agent key import ID", "Use agent_id/key_id or agent_id:key_id")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("agent_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("key_id"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
}

func (r *PaperclipAgentKeyResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || req.State.Raw.IsNull() {
		return
	}
	var plan PaperclipAgentKeyResourceModel
	var state PaperclipAgentKeyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !plan.Generation.Equal(state.Generation) {
		plan.ID = types.StringUnknown()
		plan.KeyID = types.StringUnknown()
		plan.DeliveryState = types.StringUnknown()
		resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
	}
}

