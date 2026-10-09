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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &PaperclipUserInstanceAccessResource{}
	_ resource.ResourceWithConfigure   = &PaperclipUserInstanceAccessResource{}
	_ resource.ResourceWithImportState = &PaperclipUserInstanceAccessResource{}
	_ resource.ResourceWithModifyPlan  = &PaperclipUserInstanceAccessResource{}
)

func NewPaperclipUserInstanceAccessResource() resource.Resource {
	return &PaperclipUserInstanceAccessResource{}
}

type PaperclipUserInstanceAccessResource struct {
	client *client.Client
}

type PaperclipUserInstanceAccessResourceModel struct {
	ID              types.String `tfsdk:"id"`
	UserID          types.String `tfsdk:"user_id"`
	IsInstanceAdmin types.Bool   `tfsdk:"is_instance_admin"`
	CompanyIDs      types.List   `tfsdk:"company_ids"`
}

func (r *PaperclipUserInstanceAccessResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user_instance_access"
}

func (r *PaperclipUserInstanceAccessResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages combined instance administration status and company access for a user.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Matches the user ID.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"user_id": schema.StringAttribute{
				Description: "User identifier whose instance and company access is managed.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"is_instance_admin": schema.BoolAttribute{
				Description: "Whether the user is an instance administrator (defaults to false).",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
			},
			"company_ids": schema.ListAttribute{
				Description: "List of company IDs the user has access to.",
				ElementType: types.StringType,
				Optional:    true,
				Computed:    true,
			},
		},
	}
}

func (r *PaperclipUserInstanceAccessResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *PaperclipUserInstanceAccessResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PaperclipUserInstanceAccessResourceModel
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

	cids := extractCompanyIDsFromList(ctx, plan.CompanyIDs)

	updated, err := r.client.SetUserCompanyAccess(ctx, uid, cids)
	if err != nil {
		resp.Diagnostics.AddError("Error setting user company access", err.Error())
		return
	}

	plan.ID = types.StringValue(uid)
	if updated.User != nil {
		plan.IsInstanceAdmin = types.BoolValue(updated.User.IsInstanceAdmin)
	}
	remoteCids := make([]string, 0, len(updated.CompanyAccess))
	for _, ca := range updated.CompanyAccess {
		remoteCids = append(remoteCids, ca.CompanyID)
	}
	plan.CompanyIDs = stringSliceToTypesList(ctx, remoteCids)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipUserInstanceAccessResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PaperclipUserInstanceAccessResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	uid := state.UserID.ValueString()
	res, err := r.client.GetUserCompanyAccess(ctx, uid)
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading user instance access", err.Error())
		return
	}

	state.ID = types.StringValue(uid)
	if res.User != nil {
		state.IsInstanceAdmin = types.BoolValue(res.User.IsInstanceAdmin)
	}
	remoteCids := make([]string, 0, len(res.CompanyAccess))
	for _, ca := range res.CompanyAccess {
		remoteCids = append(remoteCids, ca.CompanyID)
	}
	state.CompanyIDs = stringSliceToTypesList(ctx, remoteCids)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PaperclipUserInstanceAccessResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state PaperclipUserInstanceAccessResourceModel
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

	cids := extractCompanyIDsFromList(ctx, plan.CompanyIDs)

	updated, err := r.client.SetUserCompanyAccess(ctx, uid, cids)
	if err != nil {
		resp.Diagnostics.AddError("Error updating user company access", err.Error())
		return
	}

	plan.ID = state.ID
	if updated.User != nil {
		plan.IsInstanceAdmin = types.BoolValue(updated.User.IsInstanceAdmin)
	}
	remoteCids := make([]string, 0, len(updated.CompanyAccess))
	for _, ca := range updated.CompanyAccess {
		remoteCids = append(remoteCids, ca.CompanyID)
	}
	plan.CompanyIDs = stringSliceToTypesList(ctx, remoteCids)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipUserInstanceAccessResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PaperclipUserInstanceAccessResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	uid := state.UserID.ValueString()
	_ = r.client.DemoteInstanceAdmin(ctx, uid)
	_, _ = r.client.SetUserCompanyAccess(ctx, uid, []string{})
}

func (r *PaperclipUserInstanceAccessResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("user_id"), req, resp)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

func (r *PaperclipUserInstanceAccessResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || r.client == nil {
		return
	}
	var plan PaperclipUserInstanceAccessResourceModel
	_ = req.Plan.Get(ctx, &plan)
	if uid := plan.UserID.ValueString(); uid != "" {
		if _, err := r.client.GetUserCompanyAccess(ctx, uid); err != nil && strings.Contains(err.Error(), "instance user administration is managed by the hosting operator") {
			resp.Diagnostics.AddError("Instance Access Operator-Managed", err.Error())
		}
	}
}

