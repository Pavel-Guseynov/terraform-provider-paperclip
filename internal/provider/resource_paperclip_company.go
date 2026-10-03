package provider

import (
	"context"
	"errors"
	"fmt"

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
	_ resource.Resource                = &PaperclipCompanyResource{}
	_ resource.ResourceWithConfigure   = &PaperclipCompanyResource{}
	_ resource.ResourceWithImportState = &PaperclipCompanyResource{}
)

func NewPaperclipCompanyResource() resource.Resource {
	return &PaperclipCompanyResource{}
}

type PaperclipCompanyResource struct {
	client *client.Client
}

type PaperclipCompanyResourceModel struct {
	ID                           types.String `tfsdk:"id"`
	Name                         types.String `tfsdk:"name"`
	Description                  types.String `tfsdk:"description"`
	RequireTaskStartConfirmation types.Bool   `tfsdk:"require_task_start_confirmation"`
}

func (r *PaperclipCompanyResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_company"
}

func (r *PaperclipCompanyResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a Paperclip company.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Unique identifier for the company.",
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Description: "Company identifier name.",
				Required:    true,
			},
			"description": schema.StringAttribute{
				Description: "Optional company description.",
				Optional:    true,
			},
			"require_task_start_confirmation": schema.BoolAttribute{
				Description: "Whether newly runnable tasks require start confirmation in Decisions.",
				Optional:    true,
				Computed:    true,
				Default:     booldefault.StaticBool(false),
			},
		},
	}
}

func (r *PaperclipCompanyResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *PaperclipCompanyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PaperclipCompanyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	comp, err := r.client.CreateCompany(ctx, plan.Name.ValueString(), plan.Description.ValueString(), plan.RequireTaskStartConfirmation.ValueBool())
	if err != nil {
		resp.Diagnostics.AddError("Error creating Paperclip company", err.Error())
		return
	}

	plan.ID = types.StringValue(comp.ID)
	plan.Name = types.StringValue(comp.Name)
	plan.Description = stringOrNull(comp.Description)
	plan.RequireTaskStartConfirmation = types.BoolValue(comp.RequireTaskStartConfirmation)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipCompanyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PaperclipCompanyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	comp, err := r.client.GetCompany(ctx, state.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Paperclip company", err.Error())
		return
	}

	state.ID = types.StringValue(comp.ID)
	state.Name = types.StringValue(comp.Name)
	state.Description = stringOrNull(comp.Description)
	state.RequireTaskStartConfirmation = types.BoolValue(comp.RequireTaskStartConfirmation)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PaperclipCompanyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan PaperclipCompanyResourceModel
	var state PaperclipCompanyResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	comp, err := r.client.UpdateCompany(ctx, state.ID.ValueString(), plan.Name.ValueString(), plan.Description.ValueString(), plan.RequireTaskStartConfirmation.ValueBool())
	if err != nil {
		resp.Diagnostics.AddError("Error updating Paperclip company", err.Error())
		return
	}

	plan.ID = types.StringValue(comp.ID)
	plan.Name = types.StringValue(comp.Name)
	plan.Description = stringOrNull(comp.Description)
	plan.RequireTaskStartConfirmation = types.BoolValue(comp.RequireTaskStartConfirmation)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipCompanyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PaperclipCompanyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.ArchiveCompany(ctx, state.ID.ValueString()); err != nil {
		if !errors.Is(err, client.ErrNotFound) {
			resp.Diagnostics.AddError("Error archiving Paperclip company", err.Error())
			return
		}
	}
}

func (r *PaperclipCompanyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID == "" {
		resp.Diagnostics.AddError("Invalid Company Import ID", "Expected non-empty company_id")
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
