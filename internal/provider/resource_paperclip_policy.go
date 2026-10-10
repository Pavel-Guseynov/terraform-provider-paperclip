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
	_ resource.Resource                = &PaperclipPolicyResource{}
	_ resource.ResourceWithConfigure   = &PaperclipPolicyResource{}
	_ resource.ResourceWithImportState = &PaperclipPolicyResource{}
)

func NewPaperclipPolicyResource() resource.Resource {
	return &PaperclipPolicyResource{}
}

type PaperclipPolicyResource struct {
	client *client.Client
}

type PaperclipPolicyResourceModel struct {
	ID         types.String `tfsdk:"id"`
	CompanyID  types.String `tfsdk:"company_id"`
	Name       types.String `tfsdk:"name"`
	PolicyType types.String `tfsdk:"policy_type"`
	Rules      types.String `tfsdk:"rules"`
	Metadata   types.String `tfsdk:"metadata"`
}

func (r *PaperclipPolicyResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_policy"
}

func (r *PaperclipPolicyResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a Paperclip-native tool permission policy.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Unique identifier for the policy.",
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
				Description: "Policy name.",
				Required:    true,
			},
			"policy_type": schema.StringAttribute{
				Description: "Policy type (e.g. allow, ask; default: allow).",
				Optional:    true,
				Computed:    true,
				Default:     stringdefault.StaticString("allow"),
			},
			"rules": schema.StringAttribute{
				Description: "Optional JSON rules object.",
				Optional:    true,
				Validators: []validator.String{
					JSONAttributeValidator(),
				},
				PlanModifiers: []planmodifier.String{
					SemanticJSONPlanModifier(),
				},
			},
			"metadata": schema.StringAttribute{
				Description: "Optional JSON metadata object for the policy.",
				Optional:    true,
				Validators: []validator.String{
					JSONAttributeValidator(),
				},
				PlanModifiers: []planmodifier.String{
					SemanticJSONPlanModifier(),
				},
			},
		},
	}
}

func (r *PaperclipPolicyResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *PaperclipPolicyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PaperclipPolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var rulesMap, metaMap map[string]interface{}
	if !plan.Rules.IsNull() && !plan.Rules.IsUnknown() && plan.Rules.ValueString() != "" {
		if err := json.Unmarshal([]byte(plan.Rules.ValueString()), &rulesMap); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("rules"), "Invalid JSON", err.Error())
			return
		}
	}
	if !plan.Metadata.IsNull() && !plan.Metadata.IsUnknown() && plan.Metadata.ValueString() != "" {
		if err := json.Unmarshal([]byte(plan.Metadata.ValueString()), &metaMap); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("metadata"), "Invalid JSON", err.Error())
			return
		}
	}

	pol := client.PaperclipToolPolicy{
		CompanyID:  plan.CompanyID.ValueString(),
		Name:       plan.Name.ValueString(),
		PolicyType: plan.PolicyType.ValueString(),
		Rules:      rulesMap,
		Metadata:   metaMap,
	}

	created, err := r.client.CreateToolPolicy(ctx, plan.CompanyID.ValueString(), pol)
	if err != nil {
		resp.Diagnostics.AddError("Error creating Paperclip policy", err.Error())
		return
	}

	plan.ID = types.StringValue(created.ID)
	plan.CompanyID = types.StringValue(created.CompanyID)
	plan.Name = types.StringValue(created.Name)
	if created.PolicyType != "" {
		plan.PolicyType = types.StringValue(created.PolicyType)
	}
	plan.Rules = refreshSemanticJSON(plan.Rules, created.Rules)
	plan.Metadata = refreshSemanticJSON(plan.Metadata, created.Metadata)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipPolicyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PaperclipPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	pol, err := r.client.GetToolPolicy(ctx, state.CompanyID.ValueString(), state.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Paperclip policy", err.Error())
		return
	}

	state.ID = types.StringValue(pol.ID)
	if pol.CompanyID != "" {
		state.CompanyID = types.StringValue(pol.CompanyID)
	}
	state.Name = types.StringValue(pol.Name)
	if pol.PolicyType != "" {
		state.PolicyType = types.StringValue(pol.PolicyType)
	}
	state.Rules = refreshSemanticJSON(state.Rules, pol.Rules)
	state.Metadata = refreshSemanticJSON(state.Metadata, pol.Metadata)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PaperclipPolicyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan PaperclipPolicyResourceModel
	var state PaperclipPolicyResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var rulesMap, metaMap map[string]interface{}
	if !plan.Rules.IsNull() && !plan.Rules.IsUnknown() && plan.Rules.ValueString() != "" {
		if err := json.Unmarshal([]byte(plan.Rules.ValueString()), &rulesMap); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("rules"), "Invalid JSON", err.Error())
			return
		}
	}
	if !plan.Metadata.IsNull() && !plan.Metadata.IsUnknown() && plan.Metadata.ValueString() != "" {
		if err := json.Unmarshal([]byte(plan.Metadata.ValueString()), &metaMap); err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("metadata"), "Invalid JSON", err.Error())
			return
		}
	}

	pol := client.PaperclipToolPolicy{
		CompanyID:  state.CompanyID.ValueString(),
		Name:       plan.Name.ValueString(),
		PolicyType: plan.PolicyType.ValueString(),
		Rules:      rulesMap,
		Metadata:   metaMap,
	}

	updated, err := r.client.UpdateToolPolicy(ctx, state.CompanyID.ValueString(), state.ID.ValueString(), pol)
	if err != nil {
		resp.Diagnostics.AddError("Error updating Paperclip policy", err.Error())
		return
	}

	plan.ID = types.StringValue(updated.ID)
	plan.Name = types.StringValue(updated.Name)
	if updated.PolicyType != "" {
		plan.PolicyType = types.StringValue(updated.PolicyType)
	}
	plan.Rules = refreshSemanticJSON(plan.Rules, updated.Rules)
	plan.Metadata = refreshSemanticJSON(plan.Metadata, updated.Metadata)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipPolicyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PaperclipPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.DeleteToolPolicy(ctx, state.CompanyID.ValueString(), state.ID.ValueString()); err != nil {
		if !errors.Is(err, client.ErrNotFound) {
			resp.Diagnostics.AddError("Error deleting Paperclip policy", err.Error())
			return
		}
	}
}

func (r *PaperclipPolicyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := req.ID
	if strings.Contains(id, "/") {
		parts := strings.SplitN(id, "/", 2)
		if parts[0] != "" && parts[1] != "" {
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("company_id"), parts[0])...)
			resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
			return
		}
	}
	resp.Diagnostics.AddError(
		"Invalid Policy Import ID",
		fmt.Sprintf("Expected <company_id>/<policy_id>, got %q", id),
	)
}
