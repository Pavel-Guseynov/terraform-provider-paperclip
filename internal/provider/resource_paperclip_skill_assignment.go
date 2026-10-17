package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &PaperclipSkillAssignmentResource{}
	_ resource.ResourceWithConfigure   = &PaperclipSkillAssignmentResource{}
	_ resource.ResourceWithImportState = &PaperclipSkillAssignmentResource{}
)

func NewPaperclipSkillAssignmentResource() resource.Resource {
	return &PaperclipSkillAssignmentResource{}
}

type PaperclipSkillAssignmentResource struct {
	client *client.Client
}

type PaperclipDesiredSkillModel struct {
	Key       types.String `tfsdk:"key"`
	VersionID types.String `tfsdk:"version_id"`
}

type PaperclipSkillAssignmentResourceModel struct {
	ID      types.String                 `tfsdk:"id"`
	AgentID types.String                 `tfsdk:"agent_id"`
	Skills  []PaperclipDesiredSkillModel `tfsdk:"skills"`
}

func (r *PaperclipSkillAssignmentResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_skill_assignment"
}

func (r *PaperclipSkillAssignmentResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages desired skill assignments and version pins for a Paperclip agent as the sole writer of adapterConfig.paperclipSkillSync. POST /api/agents/:id/skills/sync sends unpinned skills as plain key strings and version-pinned skills as objects with key and versionId; an empty list clears assignments.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Unique identifier for the assignment (matches agent_id).",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"agent_id": schema.StringAttribute{
				Description: "The target Paperclip agent ID.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"skills": schema.ListNestedAttribute{
				Description: "Ordered list of desired skills assigned to the agent.",
				Required:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"key": schema.StringAttribute{
							Description: "Unique skill key or slug in the company catalog.",
							Required:    true,
						},
						"version_id": schema.StringAttribute{
							Description: "Optional immutable version snapshot identifier to pin.",
							Optional:    true,
						},
					},
				},
			},
		},
	}
}

func (r *PaperclipSkillAssignmentResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func toDesiredSkillEntries(skills []PaperclipDesiredSkillModel) []client.PaperclipDesiredSkillEntry {
	if skills == nil {
		return []client.PaperclipDesiredSkillEntry{}
	}
	entries := make([]client.PaperclipDesiredSkillEntry, len(skills))
	for i, s := range skills {
		entry := client.PaperclipDesiredSkillEntry{
			Key: s.Key.ValueString(),
		}
		if !s.VersionID.IsNull() && !s.VersionID.IsUnknown() && s.VersionID.ValueString() != "" {
			v := s.VersionID.ValueString()
			entry.VersionID = &v
		}
		entries[i] = entry
	}
	return entries
}

func snapshotToModelSkills(planned []PaperclipDesiredSkillModel, snapshot *client.PaperclipAgentSkillSnapshot) []PaperclipDesiredSkillModel {
	if snapshot == nil {
		return []PaperclipDesiredSkillModel{}
	}

	if len(snapshot.DesiredSkillEntries) > 0 {
		models := make([]PaperclipDesiredSkillModel, len(snapshot.DesiredSkillEntries))
		for i, entry := range snapshot.DesiredSkillEntries {
			m := PaperclipDesiredSkillModel{
				Key: types.StringValue(entry.Key),
			}
			if entry.VersionID != nil && *entry.VersionID != "" {
				m.VersionID = types.StringValue(*entry.VersionID)
			} else {
				// If plan had explicit null or empty, keep types.StringNull
				m.VersionID = types.StringNull()
			}
			models[i] = m
		}
		return models
	}

	if len(snapshot.DesiredSkills) > 0 {
		models := make([]PaperclipDesiredSkillModel, len(snapshot.DesiredSkills))
		for i, key := range snapshot.DesiredSkills {
			models[i] = PaperclipDesiredSkillModel{
				Key:       types.StringValue(key),
				VersionID: types.StringNull(),
			}
		}
		return models
	}

	return []PaperclipDesiredSkillModel{}
}

func (r *PaperclipSkillAssignmentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PaperclipSkillAssignmentResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	agentID := plan.AgentID.ValueString()
	desired := toDesiredSkillEntries(plan.Skills)

	snapshot, err := r.client.SyncAgentSkills(ctx, agentID, "replace", desired)
	if err != nil {
		resp.Diagnostics.AddError("Error syncing Paperclip agent skills", err.Error())
		return
	}

	plan.ID = types.StringValue(agentID)
	plan.Skills = snapshotToModelSkills(plan.Skills, snapshot)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipSkillAssignmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PaperclipSkillAssignmentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	agentID := state.AgentID.ValueString()
	snapshot, err := r.client.GetAgentSkills(ctx, agentID)
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Paperclip agent skills", err.Error())
		return
	}

	state.ID = types.StringValue(agentID)
	state.Skills = snapshotToModelSkills(state.Skills, snapshot)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PaperclipSkillAssignmentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan PaperclipSkillAssignmentResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	agentID := plan.AgentID.ValueString()
	desired := toDesiredSkillEntries(plan.Skills)

	snapshot, err := r.client.SyncAgentSkills(ctx, agentID, "replace", desired)
	if err != nil {
		resp.Diagnostics.AddError("Error updating Paperclip agent skills", err.Error())
		return
	}

	plan.ID = types.StringValue(agentID)
	plan.Skills = snapshotToModelSkills(plan.Skills, snapshot)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipSkillAssignmentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PaperclipSkillAssignmentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	agentID := state.AgentID.ValueString()
	_, err := r.client.SyncAgentSkills(ctx, agentID, "replace", []client.PaperclipDesiredSkillEntry{})
	if err != nil && !errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Error clearing Paperclip agent skills", err.Error())
		return
	}
}

func (r *PaperclipSkillAssignmentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	agentID := req.ID
	if agentID == "" {
		resp.Diagnostics.AddError("Invalid Skill Assignment Import ID", "Expected <agent_id>")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), agentID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("agent_id"), agentID)...)
}
