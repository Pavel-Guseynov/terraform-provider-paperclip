package provider

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"strings"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &PaperclipConnectionInstallResource{}
	_ resource.ResourceWithConfigure   = &PaperclipConnectionInstallResource{}
	_ resource.ResourceWithImportState = &PaperclipConnectionInstallResource{}
	_ resource.ResourceWithModifyPlan  = &PaperclipConnectionInstallResource{}
)

func NewPaperclipConnectionInstallResource() resource.Resource {
	return &PaperclipConnectionInstallResource{}
}

type PaperclipConnectionInstallResource struct {
	client *client.Client
}

type PaperclipConnectionInstallResourceModel struct {
	ID           types.String `tfsdk:"id"`
	ConnectionID types.String `tfsdk:"connection_id"`
	AgentIDs     types.List   `tfsdk:"agent_ids"`
	TargetType   types.String `tfsdk:"target_type"`
	CompanyID    types.String `tfsdk:"company_id"`
	OwnerID      types.String `tfsdk:"owner_id"`
}

func (r *PaperclipConnectionInstallResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_connection_install"
}

func (r *PaperclipConnectionInstallResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Version:     1,
		Description: "Manages tool connection installs across agents or companies in Paperclip.",
		Attributes: map[string]schema.Attribute{
			"owner_id": schema.StringAttribute{Computed: true, Description: "Provider ownership identity for the whole install set. Conflicts within one provider configuration are rejected; separate states require external ownership coordination.", PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"id": schema.StringAttribute{
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"connection_id": schema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"agent_ids": schema.ListAttribute{
				ElementType: types.StringType,
				Optional:    true,
			},
			"target_type": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Default:  stringdefault.StaticString("agent"),
			},
			"company_id": schema.StringAttribute{
				Optional: true,
			},
		},
	}
}

func (r *PaperclipConnectionInstallResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *PaperclipConnectionInstallResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PaperclipConnectionInstallResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := claimAggregate(r.client, "installs", plan.ConnectionID.ValueString(), &plan.OwnerID); err != nil {
		resp.Diagnostics.AddError("Connection install ownership conflict", err.Error())
		return
	}

	var agentIDs []string
	if !plan.AgentIDs.IsNull() && !plan.AgentIDs.IsUnknown() {
		resp.Diagnostics.Append(plan.AgentIDs.ElementsAs(ctx, &agentIDs, false)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	installs := make([]map[string]interface{}, 0)
	targetType := plan.TargetType.ValueString()
	if targetType == "" {
		targetType = "agent"
	}

	if targetType == "company" && plan.CompanyID.ValueString() != "" {
		installs = append(installs, map[string]interface{}{
			"targetType": "company",
			"targetId":   plan.CompanyID.ValueString(),
		})
	}

	for _, aid := range agentIDs {
		installs = append(installs, map[string]interface{}{
			"targetType": "agent",
			"targetId":   aid,
		})
	}

	_, err := r.client.UpdateConnectionInstalls(ctx, plan.ConnectionID.ValueString(), installs)
	if err != nil {
		resp.Diagnostics.AddError("Error creating Paperclip connection installs", err.Error())
		return
	}

	plan.ID = types.StringValue(plan.ConnectionID.ValueString())
	if plan.AgentIDs.IsUnknown() {
		plan.AgentIDs = types.ListNull(types.StringType)
	}
	if plan.TargetType.IsUnknown() {
		plan.TargetType = types.StringValue("agent")
	}
	if plan.CompanyID.IsUnknown() {
		plan.CompanyID = types.StringNull()
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipConnectionInstallResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PaperclipConnectionInstallResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	installs, err := r.client.GetConnectionInstalls(ctx, state.ConnectionID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Paperclip connection installs", err.Error())
		return
	}

	agentIDs := []string{}
	var foundCompanyID string
	for _, inst := range installs {
		tType, _ := inst["targetType"].(string)
		tID, _ := inst["targetId"].(string)
		if tType == "agent" && tID != "" {
			agentIDs = append(agentIDs, tID)
		} else if tType == "company" && tID != "" {
			foundCompanyID = tID
		}
	}

	var priorAgents []string
	if !state.AgentIDs.IsNull() && !state.AgentIDs.IsUnknown() {
		resp.Diagnostics.Append(state.AgentIDs.ElementsAs(ctx, &priorAgents, false)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	state.ID = types.StringValue(state.ConnectionID.ValueString())
	if len(agentIDs) == 0 && (state.AgentIDs.IsNull() || state.AgentIDs.IsUnknown()) {
		state.AgentIDs = types.ListNull(types.StringType)
	} else if !stringListsEqual(priorAgents, agentIDs) || state.AgentIDs.IsNull() || state.AgentIDs.IsUnknown() {
		listVal, diags := types.ListValueFrom(ctx, types.StringType, agentIDs)
		resp.Diagnostics.Append(diags...)
		state.AgentIDs = listVal
	}
	if foundCompanyID != "" {
		state.CompanyID = types.StringValue(foundCompanyID)
		state.TargetType = types.StringValue("company")
	} else {
		state.CompanyID = types.StringNull()
		state.TargetType = types.StringValue("agent")
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PaperclipConnectionInstallResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan PaperclipConnectionInstallResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var state PaperclipConnectionInstallResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if plan.OwnerID.IsNull() || plan.OwnerID.IsUnknown() {
		plan.OwnerID = state.OwnerID
	}
	if err := claimAggregate(r.client, "installs", plan.ConnectionID.ValueString(), &plan.OwnerID); err != nil {
		resp.Diagnostics.AddError("Connection install ownership conflict", err.Error())
		return
	}

	var agentIDs []string
	if !plan.AgentIDs.IsNull() && !plan.AgentIDs.IsUnknown() {
		resp.Diagnostics.Append(plan.AgentIDs.ElementsAs(ctx, &agentIDs, false)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	installs := make([]map[string]interface{}, 0)
	targetType := plan.TargetType.ValueString()
	if targetType == "" {
		targetType = "agent"
	}

	if targetType == "company" && plan.CompanyID.ValueString() != "" {
		installs = append(installs, map[string]interface{}{
			"targetType": "company",
			"targetId":   plan.CompanyID.ValueString(),
		})
	}

	for _, aid := range agentIDs {
		installs = append(installs, map[string]interface{}{
			"targetType": "agent",
			"targetId":   aid,
		})
	}

	_, err := r.client.UpdateConnectionInstalls(ctx, plan.ConnectionID.ValueString(), installs)
	if err != nil {
		resp.Diagnostics.AddError("Error updating Paperclip connection installs", err.Error())
		return
	}

	plan.ID = types.StringValue(plan.ConnectionID.ValueString())
	if plan.AgentIDs.IsUnknown() {
		plan.AgentIDs = types.ListNull(types.StringType)
	}
	if plan.TargetType.IsUnknown() {
		plan.TargetType = types.StringValue("agent")
	}
	if plan.CompanyID.IsUnknown() {
		plan.CompanyID = types.StringNull()
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipConnectionInstallResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PaperclipConnectionInstallResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if _, err := r.client.UpdateConnectionInstalls(ctx, state.ConnectionID.ValueString(), []map[string]interface{}{}); err != nil {
		if !errors.Is(err, client.ErrNotFound) {
			resp.Diagnostics.AddError("Error removing Paperclip connection installs", err.Error())
			return
		}
	}
}

func (r *PaperclipConnectionInstallResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := req.ID
	if strings.Contains(id, "/") {
		parts := strings.SplitN(id, "/", 2)
		if parts[0] != "" && parts[1] != "" {
			id = parts[1]
		} else {
			resp.Diagnostics.AddError("Invalid Connection Install Import ID", "Expected <connection_id> or <company_id>/<connection_id>")
			return
		}
	}
	if id == "" {
		resp.Diagnostics.AddError("Invalid Connection Install Import ID", "Expected <connection_id> or <company_id>/<connection_id>")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("connection_id"), id)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("owner_id"), rand.Text())...)
}

func claimAggregate(c *client.Client, kind, connectionID string, owner *types.String) error {
	if owner.IsNull() || owner.IsUnknown() || owner.ValueString() == "" {
		*owner = types.StringValue(rand.Text())
	}
	return c.ClaimConnectionAggregate(kind, connectionID, owner.ValueString())
}

func (r *PaperclipConnectionInstallResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || r.client == nil {
		return
	}
	var plan PaperclipConnectionInstallResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || plan.ConnectionID.IsUnknown() {
		return
	}
	if err := claimAggregate(r.client, "installs", plan.ConnectionID.ValueString(), &plan.OwnerID); err != nil {
		resp.Diagnostics.AddError("Connection install ownership conflict", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.Plan.SetAttribute(ctx, path.Root("owner_id"), plan.OwnerID)...)
}
