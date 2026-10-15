package provider

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &PaperclipUserCompanyAccessResource{}
	_ resource.ResourceWithConfigure   = &PaperclipUserCompanyAccessResource{}
	_ resource.ResourceWithImportState = &PaperclipUserCompanyAccessResource{}
	_ resource.ResourceWithModifyPlan  = &PaperclipUserCompanyAccessResource{}
)

func NewPaperclipUserCompanyAccessResource() resource.Resource {
	return &PaperclipUserCompanyAccessResource{}
}

type PaperclipUserCompanyAccessResource struct {
	client *client.Client
}

type PaperclipUserCompanyAccessResourceModel struct {
	ID         types.String `tfsdk:"id"`
	UserID     types.String `tfsdk:"user_id"`
	CompanyIDs types.List   `tfsdk:"company_ids"`
}

func (r *PaperclipUserCompanyAccessResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user_company_access"
}

func (r *PaperclipUserCompanyAccessResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages the company access list for a specific user.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Matches the user ID.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"user_id": schema.StringAttribute{
				Description: "User identifier whose company access is managed.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"company_ids": schema.ListAttribute{
				Description: "List of company IDs the user has access to.",
				ElementType: types.StringType,
				Required:    true,
			},
		},
	}
}

func (r *PaperclipUserCompanyAccessResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func extractCompanyIDsFromList(ctx context.Context, list types.List) []string {
	if list.IsNull() || list.IsUnknown() {
		return []string{}
	}
	var cids []string
	_ = list.ElementsAs(ctx, &cids, false)
	sort.Strings(cids)
	return cids
}

func stringSliceToTypesList(ctx context.Context, items []string) types.List {
	sort.Strings(items)
	list, _ := types.ListValueFrom(ctx, types.StringType, items)
	return list
}

func (r *PaperclipUserCompanyAccessResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PaperclipUserCompanyAccessResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	uid := plan.UserID.ValueString()
	cids := extractCompanyIDsFromList(ctx, plan.CompanyIDs)

	updated, err := r.client.SetUserCompanyAccess(ctx, uid, cids)
	if err != nil {
		resp.Diagnostics.AddError("Error setting user company access", err.Error())
		return
	}

	plan.ID = types.StringValue(uid)
	remoteCids := make([]string, 0, len(updated.CompanyAccess))
	for _, ca := range updated.CompanyAccess {
		remoteCids = append(remoteCids, ca.CompanyID)
	}
	plan.CompanyIDs = stringSliceToTypesList(ctx, remoteCids)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipUserCompanyAccessResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PaperclipUserCompanyAccessResourceModel
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
		resp.Diagnostics.AddError("Error reading user company access", err.Error())
		return
	}

	state.ID = types.StringValue(uid)
	remoteCids := make([]string, 0, len(res.CompanyAccess))
	for _, ca := range res.CompanyAccess {
		remoteCids = append(remoteCids, ca.CompanyID)
	}
	state.CompanyIDs = stringSliceToTypesList(ctx, remoteCids)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PaperclipUserCompanyAccessResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan PaperclipUserCompanyAccessResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	uid := plan.UserID.ValueString()
	cids := extractCompanyIDsFromList(ctx, plan.CompanyIDs)

	updated, err := r.client.SetUserCompanyAccess(ctx, uid, cids)
	if err != nil {
		resp.Diagnostics.AddError("Error updating user company access", err.Error())
		return
	}

	plan.ID = types.StringValue(uid)
	remoteCids := make([]string, 0, len(updated.CompanyAccess))
	for _, ca := range updated.CompanyAccess {
		remoteCids = append(remoteCids, ca.CompanyID)
	}
	plan.CompanyIDs = stringSliceToTypesList(ctx, remoteCids)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipUserCompanyAccessResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PaperclipUserCompanyAccessResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	uid := state.UserID.ValueString()
	_, err := r.client.SetUserCompanyAccess(ctx, uid, []string{})
	if err != nil && !errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Error clearing user company access on destroy", err.Error())
	}
}

func (r *PaperclipUserCompanyAccessResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("user_id"), req, resp)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

func (r *PaperclipUserCompanyAccessResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || r.client == nil {
		return
	}
	var plan PaperclipUserCompanyAccessResourceModel
	_ = req.Plan.Get(ctx, &plan)
	if uid := plan.UserID.ValueString(); uid != "" {
		if _, err := r.client.GetUserCompanyAccess(ctx, uid); err != nil && strings.Contains(err.Error(), "instance user administration is managed by the hosting operator") {
			resp.Diagnostics.AddError("Instance Access Operator-Managed", err.Error())
		}
	}
}

