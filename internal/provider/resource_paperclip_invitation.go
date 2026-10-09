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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &PaperclipInvitationResource{}
	_ resource.ResourceWithConfigure   = &PaperclipInvitationResource{}
	_ resource.ResourceWithImportState = &PaperclipInvitationResource{}
)

func NewPaperclipInvitationResource() resource.Resource {
	return &PaperclipInvitationResource{typeName: "paperclip_invitation"}
}

func NewPaperclipInviteResource() resource.Resource {
	return &PaperclipInvitationResource{typeName: "paperclip_invite"}
}

type PaperclipInvitationResource struct {
	client   *client.Client
	typeName string
}

type PaperclipInvitationResourceModel struct {
	ID               types.String `tfsdk:"id"`
	CompanyID        types.String `tfsdk:"company_id"`
	AllowedJoinTypes types.String `tfsdk:"allowed_join_types"`
	HumanRole        types.String `tfsdk:"human_role"`
	AgentMessage     types.String `tfsdk:"agent_message"`
	DefaultsPayload  types.String `tfsdk:"defaults_payload"`
	ExpiresAt        types.String `tfsdk:"expires_at"`
	CreatedAt        types.String `tfsdk:"created_at"`
	RevokedAt        types.String `tfsdk:"revoked_at"`
	AcceptedAt       types.String `tfsdk:"accepted_at"`
	State            types.String `tfsdk:"state"`
}

func (r *PaperclipInvitationResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + strings.TrimPrefix(r.typeName, "paperclip_")
}

func (r *PaperclipInvitationResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a Paperclip company invitation. Revocation serves as its retirement.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Unique identifier for the invitation.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"company_id": schema.StringAttribute{
				Description: "Company identifier owning the invitation.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"allowed_join_types": schema.StringAttribute{
				Description: "Allowed join types: human, agent, or both (defaults to both).",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("both"),
				Validators: []validator.String{
					stringOneOfValidator{allowed: []string{"human", "agent", "both"}},
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"human_role": schema.StringAttribute{
				Description: "Target human role if joining as human: owner, admin, operator, or viewer.",
				Optional:    true,
				Validators: []validator.String{
					stringOneOfValidator{allowed: []string{"owner", "admin", "operator", "viewer"}},
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"agent_message": schema.StringAttribute{
				Description: "Optional custom message for joining agent.",
				Optional:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"defaults_payload": schema.StringAttribute{
				Description: "JSON defaults payload for onboarding parameters.",
				Optional:    true,
				Validators: []validator.String{
					JSONAttributeValidator(),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
					SemanticJSONPlanModifier(),
				},
			},
			"expires_at": schema.StringAttribute{
				Description: "RFC3339 expiration timestamp (72-hour TTL).",
				Computed:    true,
			},
			"created_at": schema.StringAttribute{
				Description: "RFC3339 creation timestamp.",
				Computed:    true,
			},
			"revoked_at": schema.StringAttribute{
				Description: "RFC3339 revocation timestamp if revoked.",
				Computed:    true,
			},
			"accepted_at": schema.StringAttribute{
				Description: "RFC3339 acceptance timestamp if consumed.",
				Computed:    true,
			},
			"state": schema.StringAttribute{
				Description: "Current state: active, accepted, expired, or revoked.",
				Computed:    true,
			},
		},
	}
}

func (r *PaperclipInvitationResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *PaperclipInvitationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PaperclipInvitationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	input := client.CreateCompanyInviteInput{
		AllowedJoinTypes: plan.AllowedJoinTypes.ValueString(),
	}
	if !plan.HumanRole.IsNull() && !plan.HumanRole.IsUnknown() {
		hr := plan.HumanRole.ValueString()
		input.HumanRole = &hr
	}
	if !plan.AgentMessage.IsNull() && !plan.AgentMessage.IsUnknown() {
		msg := plan.AgentMessage.ValueString()
		input.AgentMessage = &msg
	}
	if !plan.DefaultsPayload.IsNull() && !plan.DefaultsPayload.IsUnknown() {
		var defs map[string]interface{}
		if err := json.Unmarshal([]byte(plan.DefaultsPayload.ValueString()), &defs); err != nil {
			resp.Diagnostics.AddError("Invalid Defaults Payload", err.Error())
			return
		}
		input.DefaultsPayload = defs
	}

	created, err := r.client.CreateCompanyInvite(ctx, plan.CompanyID.ValueString(), input)
	if err != nil {
		resp.Diagnostics.AddError("Error creating Paperclip invitation", err.Error())
		return
	}

	plan.ID = types.StringValue(created.ID)
	plan.CompanyID = types.StringValue(created.CompanyID)
	if created.AllowedJoinTypes != "" {
		plan.AllowedJoinTypes = types.StringValue(created.AllowedJoinTypes)
	}
	if created.HumanRole != nil {
		plan.HumanRole = types.StringValue(*created.HumanRole)
	} else {
		plan.HumanRole = types.StringNull()
	}
	if created.InviteMessage != nil {
		plan.AgentMessage = types.StringValue(*created.InviteMessage)
	} else if plan.AgentMessage.IsNull() {
		plan.AgentMessage = types.StringNull()
	}
	plan.DefaultsPayload = refreshSemanticJSON(plan.DefaultsPayload, created.DefaultsPayload)
	plan.ExpiresAt = types.StringValue(created.ExpiresAt)
	plan.CreatedAt = types.StringValue(created.CreatedAt)
	if created.RevokedAt != nil {
		plan.RevokedAt = types.StringValue(*created.RevokedAt)
	} else {
		plan.RevokedAt = types.StringNull()
	}
	if created.AcceptedAt != nil {
		plan.AcceptedAt = types.StringValue(*created.AcceptedAt)
	} else {
		plan.AcceptedAt = types.StringNull()
	}
	plan.State = types.StringValue(created.State)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipInvitationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PaperclipInvitationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	inv, err := r.client.GetCompanyInvite(ctx, state.CompanyID.ValueString(), state.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Paperclip invitation", err.Error())
		return
	}

	if inv.RevokedAt != nil || inv.State == "revoked" {
		resp.State.RemoveResource(ctx)
		return
	}

	state.ID = types.StringValue(inv.ID)
	state.CompanyID = types.StringValue(inv.CompanyID)
	state.AllowedJoinTypes = types.StringValue(inv.AllowedJoinTypes)
	if inv.HumanRole != nil {
		state.HumanRole = types.StringValue(*inv.HumanRole)
	} else {
		state.HumanRole = types.StringNull()
	}
	if inv.InviteMessage != nil {
		state.AgentMessage = types.StringValue(*inv.InviteMessage)
	} else {
		state.AgentMessage = types.StringNull()
	}
	state.DefaultsPayload = refreshSemanticJSON(state.DefaultsPayload, inv.DefaultsPayload)
	state.ExpiresAt = types.StringValue(inv.ExpiresAt)
	state.CreatedAt = types.StringValue(inv.CreatedAt)
	state.RevokedAt = types.StringNull()
	if inv.AcceptedAt != nil {
		state.AcceptedAt = types.StringValue(*inv.AcceptedAt)
	} else {
		state.AcceptedAt = types.StringNull()
	}
	state.State = types.StringValue(inv.State)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PaperclipInvitationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError(
		"Invitation Update Not Supported",
		"Company invitations cannot be updated in-place; all changes require replacement.",
	)
}

func (r *PaperclipInvitationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PaperclipInvitationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.RevokeCompanyInvite(ctx, state.ID.ValueString())
	if err != nil && !errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Error revoking Paperclip invitation", err.Error())
	}
}

func (r *PaperclipInvitationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
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
		"Invalid Invitation Import ID",
		fmt.Sprintf("Expected <company_id>/<invite_id> or <invite_id>, got %q", id),
	)
}
