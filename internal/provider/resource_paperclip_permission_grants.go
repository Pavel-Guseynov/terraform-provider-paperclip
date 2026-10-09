package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"git.guseynov.org/guseynov/paperclip/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &PaperclipPermissionGrantsResource{}
	_ resource.ResourceWithConfigure   = &PaperclipPermissionGrantsResource{}
	_ resource.ResourceWithImportState = &PaperclipPermissionGrantsResource{}
)

func NewPaperclipPermissionGrantsResource() resource.Resource {
	return &PaperclipPermissionGrantsResource{typeName: "paperclip_permission_grants"}
}

func NewPaperclipMemberPermissionsResource() resource.Resource {
	return &PaperclipPermissionGrantsResource{typeName: "paperclip_member_permissions"}
}

type PaperclipPermissionGrantsResource struct {
	client   *client.Client
	typeName string
}

type PaperclipGrantModel struct {
	PermissionKey types.String `tfsdk:"permission_key"`
	Scope         types.String `tfsdk:"scope"`
}

type PaperclipPermissionGrantsResourceModel struct {
	ID        types.String          `tfsdk:"id"`
	CompanyID types.String          `tfsdk:"company_id"`
	MemberID  types.String          `tfsdk:"member_id"`
	Grants    []PaperclipGrantModel `tfsdk:"grants"`
}

var allowedPermissionKeys = []string{
	"agents:create",
	"agents:configure",
	"agents:suggest-changes",
	"skills:create",
	"skills:suggest-changes",
	"environments:manage",
	"tools:admin",
	"tools:manage_connections",
	"tools:manage_profiles",
	"tools:view_audit",
	"audit:view_agent_actions",
	"tools:use",
	"tools:manage_runtime",
	"inbox:manage",
	"users:invite",
	"users:manage_permissions",
	"tasks:assign",
	"tasks:assign_scope",
	"tasks:manage_active_checkouts",
	"pipelines:write",
	"joins:approve",
}

func (r *PaperclipPermissionGrantsResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_" + strings.TrimPrefix(r.typeName, "paperclip_")
}

func (r *PaperclipPermissionGrantsResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages all permission grants for a company member as a single owned set.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Composite identifier formatted as <company_id>/<member_id>.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"company_id": schema.StringAttribute{
				Description: "Company identifier owning the member.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"member_id": schema.StringAttribute{
				Description: "Member identifier owning the permission grants.",
				Required:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"grants": schema.ListNestedAttribute{
				Description: "The authoritative set of permission grants for this member.",
				Required:    true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"permission_key": schema.StringAttribute{
							Description: "Permission key identifier.",
							Required:    true,
							Validators: []validator.String{
								stringOneOfValidator{allowed: allowedPermissionKeys},
							},
						},
						"scope": schema.StringAttribute{
							Description: "JSON-formatted structured scope constraints.",
							Optional:    true,
							Validators: []validator.String{
								JSONAttributeValidator(),
							},
							PlanModifiers: []planmodifier.String{
								SemanticJSONPlanModifier(),
							},
						},
					},
				},
			},
		},
	}
}

func (r *PaperclipPermissionGrantsResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func buildMemberGrantInputs(grants []PaperclipGrantModel) ([]client.PaperclipMemberGrantInput, error) {
	inputs := make([]client.PaperclipMemberGrantInput, len(grants))
	for i, g := range grants {
		in := client.PaperclipMemberGrantInput{
			PermissionKey: g.PermissionKey.ValueString(),
		}
		if !g.Scope.IsNull() && !g.Scope.IsUnknown() && g.Scope.ValueString() != "" {
			var sc map[string]interface{}
			if err := json.Unmarshal([]byte(g.Scope.ValueString()), &sc); err != nil {
				return nil, fmt.Errorf("invalid grant scope JSON for %s: %w", in.PermissionKey, err)
			}
			in.Scope = sc
		}
		inputs[i] = in
	}
	return inputs, nil
}

func syncGrantModelsFromRemote(remoteGrants []client.PaperclipMemberGrant, priorGrants []PaperclipGrantModel) []PaperclipGrantModel {
	priorMap := make(map[string]types.String)
	for _, pg := range priorGrants {
		priorMap[pg.PermissionKey.ValueString()] = pg.Scope
	}

	result := make([]PaperclipGrantModel, len(remoteGrants))
	for i, rg := range remoteGrants {
		scopeVal := refreshSemanticJSON(priorMap[rg.PermissionKey], rg.Scope)
		result[i] = PaperclipGrantModel{
			PermissionKey: types.StringValue(rg.PermissionKey),
			Scope:         scopeVal,
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].PermissionKey.ValueString() < result[j].PermissionKey.ValueString()
	})
	return result
}

func (r *PaperclipPermissionGrantsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PaperclipPermissionGrantsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cid := plan.CompanyID.ValueString()
	mid := plan.MemberID.ValueString()

	inputs, err := buildMemberGrantInputs(plan.Grants)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Grants Payload", err.Error())
		return
	}

	updated, err := r.client.SetMemberPermissions(ctx, cid, mid, inputs)
	if err != nil {
		resp.Diagnostics.AddError("Error setting member permissions", err.Error())
		return
	}

	plan.ID = types.StringValue(fmt.Sprintf("%s/%s", cid, mid))
	plan.CompanyID = types.StringValue(cid)
	plan.MemberID = types.StringValue(mid)
	plan.Grants = syncGrantModelsFromRemote(updated.Grants, plan.Grants)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipPermissionGrantsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PaperclipPermissionGrantsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cid := state.CompanyID.ValueString()
	mid := state.MemberID.ValueString()

	member, err := r.client.GetCompanyMember(ctx, cid, mid)
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading member permissions", err.Error())
		return
	}

	if member.Status == "archived" {
		resp.State.RemoveResource(ctx)
		return
	}

	state.ID = types.StringValue(fmt.Sprintf("%s/%s", cid, mid))
	state.CompanyID = types.StringValue(cid)
	state.MemberID = types.StringValue(mid)
	state.Grants = syncGrantModelsFromRemote(member.Grants, state.Grants)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PaperclipPermissionGrantsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan PaperclipPermissionGrantsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cid := plan.CompanyID.ValueString()
	mid := plan.MemberID.ValueString()

	inputs, err := buildMemberGrantInputs(plan.Grants)
	if err != nil {
		resp.Diagnostics.AddError("Invalid Grants Payload", err.Error())
		return
	}

	updated, err := r.client.SetMemberPermissions(ctx, cid, mid, inputs)
	if err != nil {
		resp.Diagnostics.AddError("Error updating member permissions", err.Error())
		return
	}

	plan.ID = types.StringValue(fmt.Sprintf("%s/%s", cid, mid))
	plan.CompanyID = types.StringValue(cid)
	plan.MemberID = types.StringValue(mid)
	plan.Grants = syncGrantModelsFromRemote(updated.Grants, plan.Grants)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipPermissionGrantsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PaperclipPermissionGrantsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cid := state.CompanyID.ValueString()
	mid := state.MemberID.ValueString()

	_, err := r.client.SetMemberPermissions(ctx, cid, mid, []client.PaperclipMemberGrantInput{})
	if err != nil && !errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Error clearing member permissions", err.Error())
	}
}

func (r *PaperclipPermissionGrantsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := req.ID
	if strings.Contains(id, "/") {
		parts := strings.SplitN(id, "/", 2)
		if parts[0] != "" && parts[1] != "" {
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("company_id"), parts[0])...)
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("member_id"), parts[1])...)
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
			return
		}
	}
	resp.Diagnostics.AddError(
		"Invalid Permission Grants Import ID",
		fmt.Sprintf("Expected <company_id>/<member_id>, got %q", id),
	)
}
