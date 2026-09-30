package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
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
	_ resource.Resource                = &PaperclipAgentResource{}
	_ resource.ResourceWithConfigure   = &PaperclipAgentResource{}
	_ resource.ResourceWithImportState = &PaperclipAgentResource{}
)

func NewPaperclipAgentResource() resource.Resource {
	return &PaperclipAgentResource{}
}

type PaperclipAgentResource struct {
	client *client.Client
}

type PaperclipAgentResourceModel struct {
	ID             types.String `tfsdk:"id"`
	CompanyID      types.String `tfsdk:"company_id"`
	Name           types.String `tfsdk:"name"`
	Role           types.String `tfsdk:"role"`
	Title          types.String `tfsdk:"title"`
	AdapterType    types.String `tfsdk:"adapter_type"`
	Status         types.String `tfsdk:"status"`
	RuntimeConfig  types.String `tfsdk:"runtime_config"`
	AdapterConfig  types.String `tfsdk:"adapter_config"`
	Metadata       types.String `tfsdk:"metadata"`
	Instructions   types.String `tfsdk:"instructions"`
	CanAssignTasks types.Bool   `tfsdk:"can_assign_tasks"`
}

func (r *PaperclipAgentResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_agent"
}

func (r *PaperclipAgentResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a Paperclip agent (project or company scope).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Unique identifier for the agent.",
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
				Description: "Agent identifier name.",
				Required:    true,
			},
			"role": schema.StringAttribute{
				Description: "Agent role (e.g. engineer, committer, reviewer).",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"title": schema.StringAttribute{
				Description: "Optional human-readable title.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"adapter_type": schema.StringAttribute{
				Description: "Execution adapter type (e.g. antigravity_local).",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"status": schema.StringAttribute{
				Description: "Agent lifecycle status reported by the board (board-owned runtime state).",
				Computed:    true,
			},
			"runtime_config": schema.StringAttribute{
				Description: "Optional JSON runtime configuration string.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"adapter_config": schema.StringAttribute{
				Description: "Optional JSON adapter configuration string (e.g. skill assignments).",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"metadata": schema.StringAttribute{
				Description: "Optional JSON metadata object for the agent.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"instructions": schema.StringAttribute{
				Description: "Managed AGENTS.md bundle instructions for this agent.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"can_assign_tasks": schema.BoolAttribute{
				Description: "Whether the agent holds the task-assignment permission. The protected project assignment policy gates assignment through the tasks:assign grant that can_assign_tasks mints, but it does not deny an agent key from creating an unassigned issue; a filing made inside a heartbeat run is detected after the fact by the paperclip-fleet-health finding issue_filed_from_heartbeat_run. Harness declares the value for every managed agent, reconciles it after each write, and reads it back from the board.",
				Required:    true,
			},
		},
	}
}

// reconcileTaskAssignment brings the agent's task-assignment permission flag
// to the declared value and proves it from the board's own read.
func (r *PaperclipAgentResource) reconcileTaskAssignment(ctx context.Context, agentID string, desired bool) error {
	agent, err := r.client.GetAgent(ctx, agentID)
	if err != nil {
		return err
	}
	if agent.Permissions == nil {
		// The permissions route replaces every flag, so the unchanged ones are
		// only ever written back from the board's own report.
		return fmt.Errorf("board reported no permissions for agent %s, so its task assignment cannot be changed without altering the other flags", agentID)
	}
	if agent.Permissions.CanAssignTasks == desired {
		return nil
	}
	permissions := *agent.Permissions
	permissions.CanAssignTasks = desired
	if err := r.client.UpdateAgentPermissions(ctx, agentID, permissions); err != nil {
		return err
	}
	agent, err = r.client.GetAgent(ctx, agentID)
	if err != nil {
		return err
	}
	if agent.Permissions == nil || agent.Permissions.CanAssignTasks != desired {
		return fmt.Errorf("agent task assignment did not converge: declared %t, board reports %+v", desired, agent.Permissions)
	}
	return nil
}

func taskAssignmentValue(permissions *client.PaperclipAgentPermissions) types.Bool {
	if permissions == nil {
		return types.BoolNull()
	}
	return types.BoolValue(permissions.CanAssignTasks)
}

func (r *PaperclipAgentResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *PaperclipAgentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PaperclipAgentResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var runtimeMap, adapterMap, metaMap map[string]interface{}
	if !plan.RuntimeConfig.IsNull() && !plan.RuntimeConfig.IsUnknown() && plan.RuntimeConfig.ValueString() != "" {
		_ = json.Unmarshal([]byte(plan.RuntimeConfig.ValueString()), &runtimeMap)
	}
	if !plan.AdapterConfig.IsNull() && !plan.AdapterConfig.IsUnknown() && plan.AdapterConfig.ValueString() != "" {
		_ = json.Unmarshal([]byte(plan.AdapterConfig.ValueString()), &adapterMap)
	}
	if !plan.Metadata.IsNull() && !plan.Metadata.IsUnknown() && plan.Metadata.ValueString() != "" {
		_ = json.Unmarshal([]byte(plan.Metadata.ValueString()), &metaMap)
	}

	role := "engineer"
	if !plan.Role.IsNull() && !plan.Role.IsUnknown() && plan.Role.ValueString() != "" {
		role = plan.Role.ValueString()
	}
	adapterType := "antigravity_local"
	if !plan.AdapterType.IsNull() && !plan.AdapterType.IsUnknown() && plan.AdapterType.ValueString() != "" {
		adapterType = plan.AdapterType.ValueString()
	}

	agent := client.PaperclipAgent{
		CompanyID:     plan.CompanyID.ValueString(),
		Name:          plan.Name.ValueString(),
		Role:          role,
		Title:         plan.Title.ValueString(),
		AdapterType:   adapterType,
		RuntimeConfig: runtimeMap,
		AdapterConfig: adapterMap,
		Metadata:      metaMap,
	}

	created, err := r.client.CreateAgent(ctx, plan.CompanyID.ValueString(), agent)
	if err != nil {
		resp.Diagnostics.AddError("Error creating Paperclip agent", err.Error())
		return
	}

	if !plan.Instructions.IsNull() && !plan.Instructions.IsUnknown() && plan.Instructions.ValueString() != "" {
		if err := r.client.UpdateAgentInstructionsFile(ctx, created.ID, "AGENTS.md", plan.Instructions.ValueString()); err != nil {
			resp.Diagnostics.AddError("Error updating Paperclip agent instructions", err.Error())
			return
		}
	}

	if err := r.reconcileTaskAssignment(ctx, created.ID, plan.CanAssignTasks.ValueBool()); err != nil {
		resp.Diagnostics.AddError("Error reconciling Paperclip agent task assignment", err.Error())
		return
	}

	plan.ID = types.StringValue(created.ID)
	plan.CompanyID = types.StringValue(created.CompanyID)
	plan.Name = types.StringValue(created.Name)
	if created.Role != "" {
		plan.Role = types.StringValue(created.Role)
	} else if plan.Role.IsUnknown() {
		plan.Role = types.StringNull()
	}
	if created.Title != "" {
		plan.Title = types.StringValue(created.Title)
	} else if plan.Title.IsUnknown() {
		plan.Title = types.StringNull()
	}
	if created.AdapterType != "" {
		plan.AdapterType = types.StringValue(created.AdapterType)
	} else if plan.AdapterType.IsUnknown() {
		plan.AdapterType = types.StringNull()
	}
	plan.Status = stringOrNull(created.Status)
	if plan.RuntimeConfig.IsUnknown() {
		plan.RuntimeConfig = jsonStringOrNull(created.RuntimeConfig)
	}
	if plan.AdapterConfig.IsUnknown() {
		plan.AdapterConfig = jsonStringOrNull(created.AdapterConfig)
	}
	if plan.Metadata.IsUnknown() {
		plan.Metadata = jsonStringOrNull(created.Metadata)
	}
	if plan.Instructions.IsUnknown() {
		plan.Instructions = types.StringNull()
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipAgentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PaperclipAgentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	agent, err := r.client.GetAgent(ctx, state.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Paperclip agent", err.Error())
		return
	}

	state.ID = types.StringValue(agent.ID)
	if agent.CompanyID != "" {
		state.CompanyID = types.StringValue(agent.CompanyID)
	}
	state.Name = types.StringValue(agent.Name)
	if agent.Role != "" {
		state.Role = types.StringValue(agent.Role)
	} else {
		state.Role = types.StringNull()
	}
	if agent.Title != "" {
		state.Title = types.StringValue(agent.Title)
	} else {
		state.Title = types.StringNull()
	}
	if agent.AdapterType != "" {
		state.AdapterType = types.StringValue(agent.AdapterType)
	} else {
		state.AdapterType = types.StringNull()
	}
	state.Status = stringOrNull(agent.Status)

	state.RuntimeConfig = refreshManagedJSONState(state.RuntimeConfig, agent.RuntimeConfig)
	state.AdapterConfig = refreshManagedJSONState(state.AdapterConfig, agent.AdapterConfig)
	state.Metadata = refreshManagedJSONState(state.Metadata, agent.Metadata)
	state.CanAssignTasks = taskAssignmentValue(agent.Permissions)

	instructions, err := r.client.GetAgentInstructionsFile(ctx, state.ID.ValueString(), "AGENTS.md")
	if err == nil && instructions != "" {
		state.Instructions = types.StringValue(instructions)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func refreshManagedJSONState(current types.String, remote map[string]interface{}) types.String {
	if current.IsNull() || current.IsUnknown() {
		return jsonStringOrNull(remote)
	}
	var managed map[string]interface{}
	if err := json.Unmarshal([]byte(current.ValueString()), &managed); err == nil && jsonContains(remote, managed) {
		return current
	}
	return jsonStringOrNull(remote)
}

func jsonContains(remote, managed interface{}) bool {
	managedMap, managedIsMap := managed.(map[string]interface{})
	if !managedIsMap {
		return reflect.DeepEqual(remote, managed)
	}
	remoteMap, remoteIsMap := remote.(map[string]interface{})
	if !remoteIsMap {
		return false
	}
	for key, managedValue := range managedMap {
		remoteValue, ok := remoteMap[key]
		if !ok || !jsonContains(remoteValue, managedValue) {
			return false
		}
	}
	return true
}

func (r *PaperclipAgentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan PaperclipAgentResourceModel
	var state PaperclipAgentResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var runtimeMap, adapterMap, metaMap map[string]interface{}
	if !plan.RuntimeConfig.IsNull() && !plan.RuntimeConfig.IsUnknown() && plan.RuntimeConfig.ValueString() != "" {
		_ = json.Unmarshal([]byte(plan.RuntimeConfig.ValueString()), &runtimeMap)
	}
	if !plan.AdapterConfig.IsNull() && !plan.AdapterConfig.IsUnknown() && plan.AdapterConfig.ValueString() != "" {
		_ = json.Unmarshal([]byte(plan.AdapterConfig.ValueString()), &adapterMap)
	}
	if !plan.Metadata.IsNull() && !plan.Metadata.IsUnknown() && plan.Metadata.ValueString() != "" {
		_ = json.Unmarshal([]byte(plan.Metadata.ValueString()), &metaMap)
	}

	role := state.Role.ValueString()
	if !plan.Role.IsNull() && !plan.Role.IsUnknown() && plan.Role.ValueString() != "" {
		role = plan.Role.ValueString()
	}
	adapterType := state.AdapterType.ValueString()
	if !plan.AdapterType.IsNull() && !plan.AdapterType.IsUnknown() && plan.AdapterType.ValueString() != "" {
		adapterType = plan.AdapterType.ValueString()
	}

	agent := client.PaperclipAgent{
		CompanyID:     state.CompanyID.ValueString(),
		Name:          plan.Name.ValueString(),
		Role:          role,
		Title:         plan.Title.ValueString(),
		AdapterType:   adapterType,
		RuntimeConfig: runtimeMap,
		AdapterConfig: adapterMap,
		Metadata:      metaMap,
	}

	updated, err := r.client.UpdateAgent(ctx, state.ID.ValueString(), agent)
	if err != nil {
		resp.Diagnostics.AddError("Error updating Paperclip agent", err.Error())
		return
	}

	if !plan.Instructions.IsNull() && !plan.Instructions.IsUnknown() && plan.Instructions.ValueString() != "" {
		if err := r.client.UpdateAgentInstructionsFile(ctx, state.ID.ValueString(), "AGENTS.md", plan.Instructions.ValueString()); err != nil {
			resp.Diagnostics.AddError("Error updating Paperclip agent instructions", err.Error())
			return
		}
	}

	if err := r.reconcileTaskAssignment(ctx, state.ID.ValueString(), plan.CanAssignTasks.ValueBool()); err != nil {
		resp.Diagnostics.AddError("Error reconciling Paperclip agent task assignment", err.Error())
		return
	}

	plan.ID = types.StringValue(updated.ID)
	plan.Name = types.StringValue(updated.Name)
	plan.Role = types.StringValue(updated.Role)
	if updated.Title != "" {
		plan.Title = types.StringValue(updated.Title)
	} else {
		plan.Title = types.StringNull()
	}
	plan.AdapterType = types.StringValue(updated.AdapterType)
	plan.Status = stringOrNull(updated.Status)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipAgentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PaperclipAgentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.DeleteAgent(ctx, state.ID.ValueString()); err != nil {
		if !errors.Is(err, client.ErrNotFound) {
			resp.Diagnostics.AddError("Error deleting Paperclip agent", err.Error())
			return
		}
	}
}

func (r *PaperclipAgentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := req.ID
	if strings.Contains(id, "/") {
		parts := strings.SplitN(id, "/", 2)
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("company_id"), parts[0])...)
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
