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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &PaperclipInstanceAdminResource{}
	_ resource.ResourceWithConfigure   = &PaperclipInstanceAdminResource{}
	_ resource.ResourceWithImportState = &PaperclipInstanceAdminResource{}
	_ resource.ResourceWithModifyPlan  = &PaperclipInstanceAdminResource{}
)

func NewPaperclipInstanceAdminResource() resource.Resource {
	return &PaperclipInstanceAdminResource{}
}

type PaperclipInstanceAdminResource struct {
	client *client.Client
}

type PaperclipInstanceAdminResourceModel struct {
	ID              types.String `tfsdk:"id"`
	UserID          types.String `tfsdk:"user_id"`
	IsInstanceAdmin types.Bool   `tfsdk:"is_instance_admin"`
}

func (r *PaperclipInstanceAdminResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_instance_admin"
}

func (r *PaperclipInstanceAdminResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages instance administrator promotion and demotion for a user.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Matches the user ID.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"user_id": schema.StringAttribute{
				Description: "User identifier to promote or demote.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"is_instance_admin": schema.BoolAttribute{
				Description: "Whether the user is an instance administrator (defaults to true).",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(true),
			},
		},
	}
}

func (r *PaperclipInstanceAdminResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *PaperclipInstanceAdminResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PaperclipInstanceAdminResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	uid := plan.UserID.ValueString()
	if plan.IsInstanceAdmin.ValueBool() {
		if err := r.client.PromoteInstanceAdmin(ctx, uid); err != nil {
			resp.Diagnostics.AddError("Error promoting user to instance admin", err.Error())
			return
		}
	}

	plan.ID = types.StringValue(uid)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipInstanceAdminResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PaperclipInstanceAdminResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	uid := state.UserID.ValueString()
	accessRes, err := r.client.GetUserCompanyAccess(ctx, uid)
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading instance admin status", err.Error())
		return
	}

	state.ID = types.StringValue(uid)
	if accessRes.User != nil {
		state.IsInstanceAdmin = types.BoolValue(accessRes.User.IsInstanceAdmin)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PaperclipInstanceAdminResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state PaperclipInstanceAdminResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	uid := state.UserID.ValueString()
	if plan.IsInstanceAdmin.ValueBool() != state.IsInstanceAdmin.ValueBool() {
		var err error
		if plan.IsInstanceAdmin.ValueBool() {
			err = r.client.PromoteInstanceAdmin(ctx, uid)
		} else {
			err = r.client.DemoteInstanceAdmin(ctx, uid)
		}
		if err != nil {
			resp.Diagnostics.AddError("Error updating instance admin status", err.Error())
			return
		}
	}

	plan.ID = state.ID
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipInstanceAdminResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PaperclipInstanceAdminResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	uid := state.UserID.ValueString()
	if err := r.client.DemoteInstanceAdmin(ctx, uid); err != nil && !errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Error demoting instance admin on destroy", err.Error())
	}
}

func (r *PaperclipInstanceAdminResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("user_id"), req, resp)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

func (r *PaperclipInstanceAdminResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || r.client == nil {
		return
	}
	var plan PaperclipInstanceAdminResourceModel
	_ = req.Plan.Get(ctx, &plan)
	if uid := plan.UserID.ValueString(); uid != "" {
		if _, err := r.client.GetUserCompanyAccess(ctx, uid); err != nil && strings.Contains(err.Error(), "instance user administration is managed by the hosting operator") {
			resp.Diagnostics.AddError("Instance Access Operator-Managed", err.Error())
		}
	}
}

