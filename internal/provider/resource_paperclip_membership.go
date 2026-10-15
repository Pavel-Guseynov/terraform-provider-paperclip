package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
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
	_ resource.Resource                = &PaperclipMembershipResource{}
	_ resource.ResourceWithConfigure   = &PaperclipMembershipResource{}
	_ resource.ResourceWithImportState = &PaperclipMembershipResource{}
)

func NewPaperclipMembershipResource() resource.Resource {
	return &PaperclipMembershipResource{}
}

type PaperclipMembershipResource struct {
	client *client.Client
}

type PaperclipMembershipResourceModel struct {
	ID                  types.String `tfsdk:"id"`
	CompanyID           types.String `tfsdk:"company_id"`
	UserID              types.String `tfsdk:"user_id"`
	Role                types.String `tfsdk:"role"`
	Status              types.String `tfsdk:"status"`
	ReassignmentAgentID types.String `tfsdk:"reassignment_agent_id"`
	ReassignmentUserID  types.String `tfsdk:"reassignment_user_id"`
	ArchiveOnDestroy    types.Bool   `tfsdk:"archive_on_destroy"`
}

func (r *PaperclipMembershipResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_membership"
}

func (r *PaperclipMembershipResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Adopts and manages an existing human company membership (role, status, and archive with reassignment).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Unique identifier for the membership.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"company_id": schema.StringAttribute{
				Description: "Company identifier owning the membership.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"user_id": schema.StringAttribute{
				Description: "User identifier (principalId) of the human member.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"role": schema.StringAttribute{
				Description: "Human membership role: owner, admin, operator, or viewer (defaults to operator).",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("operator"),
				Validators: []validator.String{
					stringOneOfValidator{allowed: []string{"owner", "admin", "operator", "viewer"}},
				},
			},
			"status": schema.StringAttribute{
				Description: "Membership status: active, suspended, or pending (defaults to active).",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("active"),
				Validators: []validator.String{
					stringOneOfValidator{allowed: []string{"active", "suspended", "pending"}},
				},
			},
			"reassignment_agent_id": schema.StringAttribute{
				Description: "Optional target agent identifier for reassignment when archiving on destroy.",
				Optional:    true,
			},
			"reassignment_user_id": schema.StringAttribute{
				Description: "Optional target user identifier for reassignment when archiving on destroy.",
				Optional:    true,
			},
			"archive_on_destroy": schema.BoolAttribute{
				Description: "Whether to archive the membership on destroy via POST /archive (defaults to true).",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
			},
		},
	}
}

func (r *PaperclipMembershipResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	cli, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Provider Data", fmt.Sprintf("Expected *client.Client, got: %T", req.ProviderData))
		return
	}
	r.client = cli
}

func (r *PaperclipMembershipResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PaperclipMembershipResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cid := plan.CompanyID.ValueString()
	uid := plan.UserID.ValueString()

	existing, err := r.client.GetCompanyMember(ctx, cid, uid)
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.Diagnostics.AddError(
				"Cannot Create Human Membership Directly",
				fmt.Sprintf("Membership for user %s in company %s does not exist. Human memberships come into existence through invite acceptance or join approval; invite user first or adopt an existing membership.", uid, cid),
			)
			return
		}
		resp.Diagnostics.AddError("Error finding company membership", err.Error())
		return
	}

	mid := existing.ID
	desiredRole := plan.Role.ValueString()
	desiredStatus := plan.Status.ValueString()

	currentRole := ""
	if existing.MembershipRole != nil {
		currentRole = *existing.MembershipRole
	}
	currentStatus := existing.Status

	if currentRole != desiredRole || currentStatus != desiredStatus {
		updateInput := client.UpdateCompanyMemberInput{}
		if currentRole != desiredRole {
			updateInput.MembershipRole = &desiredRole
		}
		if currentStatus != desiredStatus {
			updateInput.Status = &desiredStatus
		}
		updated, err := r.client.UpdateCompanyMember(ctx, cid, mid, updateInput)
		if err != nil {
			resp.Diagnostics.AddError("Error updating adopted membership", err.Error())
			return
		}
		if updated.MembershipRole != nil {
			plan.Role = types.StringValue(*updated.MembershipRole)
		}
		plan.Status = types.StringValue(updated.Status)
	} else {
		if existing.MembershipRole != nil {
			plan.Role = types.StringValue(*existing.MembershipRole)
		}
		plan.Status = types.StringValue(existing.Status)
	}

	plan.ID = types.StringValue(mid)
	plan.CompanyID = types.StringValue(cid)
	plan.UserID = types.StringValue(uid)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipMembershipResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PaperclipMembershipResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cid := state.CompanyID.ValueString()
	mid := state.ID.ValueString()

	member, err := r.client.GetCompanyMember(ctx, cid, mid)
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Paperclip membership", err.Error())
		return
	}

	if member.Status == "archived" {
		resp.State.RemoveResource(ctx)
		return
	}

	state.ID = types.StringValue(member.ID)
	state.CompanyID = types.StringValue(member.CompanyID)
	state.UserID = types.StringValue(member.PrincipalID)
	if member.MembershipRole != nil {
		state.Role = types.StringValue(*member.MembershipRole)
	}
	state.Status = types.StringValue(member.Status)

	if state.ArchiveOnDestroy.IsNull() || state.ArchiveOnDestroy.IsUnknown() {
		state.ArchiveOnDestroy = types.BoolValue(true)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PaperclipMembershipResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state PaperclipMembershipResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cid := state.CompanyID.ValueString()
	mid := state.ID.ValueString()

	desiredRole := plan.Role.ValueString()
	desiredStatus := plan.Status.ValueString()

	input := client.UpdateCompanyMemberInput{}
	if !plan.Role.Equal(state.Role) {
		input.MembershipRole = &desiredRole
	}
	if !plan.Status.Equal(state.Status) {
		input.Status = &desiredStatus
	}

	if input.MembershipRole != nil || input.Status != nil {
		updated, err := r.client.UpdateCompanyMember(ctx, cid, mid, input)
		if err != nil {
			resp.Diagnostics.AddError("Error updating Paperclip membership", err.Error())
			return
		}
		if updated.MembershipRole != nil {
			plan.Role = types.StringValue(*updated.MembershipRole)
		}
		plan.Status = types.StringValue(updated.Status)
	}

	plan.ID = state.ID
	plan.CompanyID = state.CompanyID
	plan.UserID = state.UserID

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipMembershipResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PaperclipMembershipResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !state.ArchiveOnDestroy.ValueBool() {
		return
	}

	var reassignment *client.MemberReassignment
	if (!state.ReassignmentAgentID.IsNull() && !state.ReassignmentAgentID.IsUnknown()) ||
		(!state.ReassignmentUserID.IsNull() && !state.ReassignmentUserID.IsUnknown()) {
		reassignment = &client.MemberReassignment{}
		if !state.ReassignmentAgentID.IsNull() && !state.ReassignmentAgentID.IsUnknown() {
			aid := state.ReassignmentAgentID.ValueString()
			reassignment.AssigneeAgentID = &aid
		}
		if !state.ReassignmentUserID.IsNull() && !state.ReassignmentUserID.IsUnknown() {
			uid := state.ReassignmentUserID.ValueString()
			reassignment.AssigneeUserID = &uid
		}
	}

	err := r.client.ArchiveCompanyMember(ctx, state.CompanyID.ValueString(), state.ID.ValueString(), reassignment)
	if err != nil && !errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Error archiving Paperclip membership", err.Error())
	}
}

func (r *PaperclipMembershipResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
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
		"Invalid Membership Import ID",
		fmt.Sprintf("Expected <company_id>/<member_id> or <member_id>, got %q", id),
	)
}
