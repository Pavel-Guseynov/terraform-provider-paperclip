package provider

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"git.guseynov.org/guseynov/paperclip/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &PaperclipStdioTemplateResource{}
	_ resource.ResourceWithConfigure   = &PaperclipStdioTemplateResource{}
	_ resource.ResourceWithImportState = &PaperclipStdioTemplateResource{}
)

// stdioTemplateImmutable explains the one lifecycle Paperclip offers approved
// stdio templates: create, list, and disable. Every attribute therefore forces
// replacement under a new template identity, and an in-place update never
// reaches Paperclip.
const stdioTemplateImmutable = "Paperclip exposes no update operation for approved stdio templates; every attribute change replaces the template under a new template identity."

func NewPaperclipStdioTemplateResource() resource.Resource {
	return &PaperclipStdioTemplateResource{}
}

type PaperclipStdioTemplateResource struct {
	client *client.Client
}

type PaperclipStdioTemplateResourceModel struct {
	ID          types.String `tfsdk:"id"`
	CompanyID   types.String `tfsdk:"company_id"`
	TemplateID  types.String `tfsdk:"template_id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	Command     types.String `tfsdk:"command"`
	Args        types.List   `tfsdk:"args"`
	EnvKeys     types.List   `tfsdk:"env_keys"`
}

func templateStrings(ctx context.Context, list types.List) ([]string, diag.Diagnostics) {
	if list.IsNull() || list.IsUnknown() {
		return nil, nil
	}
	var values []string
	diags := list.ElementsAs(ctx, &values, false)
	return values, diags
}

// templateListState reads a live string list back into state; an empty live
// list keeps a null configured list so an omitted attribute stays converged.
func templateListState(ctx context.Context, prior types.List, remote []string) types.List {
	if len(remote) == 0 && prior.IsNull() {
		return prior
	}
	if remote == nil {
		remote = []string{}
	}
	list, _ := types.ListValueFrom(ctx, types.StringType, remote)
	return list
}

func (r *PaperclipStdioTemplateResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_stdio_template"
}

func (r *PaperclipStdioTemplateResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a Paperclip native stdio tool template. Paperclip templates are immutable: every attribute forces replacement, removal disables the template, and a disabled template reads back as absent.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"company_id": schema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"template_id": schema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"description": schema.StringAttribute{
				Optional: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"command": schema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"args": schema.ListAttribute{
				ElementType: types.StringType,
				Optional:    true,
				PlanModifiers: []planmodifier.List{
					listplanmodifier.RequiresReplace(),
				},
			},
			"env_keys": schema.ListAttribute{
				ElementType: types.StringType,
				Optional:    true,
				PlanModifiers: []planmodifier.List{
					listplanmodifier.RequiresReplace(),
				},
			},
		},
	}
}

func (r *PaperclipStdioTemplateResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *PaperclipStdioTemplateResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PaperclipStdioTemplateResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	args, diags := templateStrings(ctx, plan.Args)
	resp.Diagnostics.Append(diags...)
	envKeys, diags := templateStrings(ctx, plan.EnvKeys)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	tpl := client.PaperclipStdioTemplate{
		CompanyID:   plan.CompanyID.ValueString(),
		TemplateID:  plan.TemplateID.ValueString(),
		Name:        plan.Name.ValueString(),
		Description: plan.Description.ValueString(),
		Command:     plan.Command.ValueString(),
		Args:        args,
		EnvKeys:     envKeys,
	}

	created, err := r.client.CreateStdioTemplate(ctx, plan.CompanyID.ValueString(), tpl)
	if err != nil {
		resp.Diagnostics.AddError("Error creating Paperclip stdio template", err.Error())
		return
	}

	if created.ID != "" {
		plan.ID = types.StringValue(created.ID)
	} else {
		plan.ID = types.StringValue(plan.TemplateID.ValueString())
	}
	plan.TemplateID = types.StringValue(created.TemplateID)
	plan.Name = types.StringValue(created.Name)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipStdioTemplateResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PaperclipStdioTemplateResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	lookupID := state.TemplateID.ValueString()
	if lookupID == "" {
		lookupID = state.ID.ValueString()
	}

	tpl, err := r.client.GetStdioTemplate(ctx, state.CompanyID.ValueString(), lookupID)
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Paperclip stdio template", err.Error())
		return
	}
	if tpl.Status == client.StdioTemplateDisabledStatus {
		resp.State.RemoveResource(ctx)
		return
	}

	if tpl.ID != "" {
		state.ID = types.StringValue(tpl.ID)
	} else {
		state.ID = types.StringValue(tpl.TemplateID)
	}
	state.TemplateID = types.StringValue(tpl.TemplateID)
	state.Name = types.StringValue(tpl.Name)
	if tpl.Description != "" {
		state.Description = types.StringValue(tpl.Description)
	}
	state.Command = types.StringValue(tpl.Command)
	state.Args = templateListState(ctx, state.Args, tpl.Args)
	state.EnvKeys = templateListState(ctx, state.EnvKeys, tpl.EnvKeys)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update is unreachable through a plan, because every attribute forces
// replacement; it fails closed so no in-place template mutation is ever
// attempted against Paperclip.
func (r *PaperclipStdioTemplateResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Paperclip stdio templates are immutable", stdioTemplateImmutable)
}

func (r *PaperclipStdioTemplateResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PaperclipStdioTemplateResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	templateID := state.TemplateID.ValueString()
	if templateID == "" {
		templateID = state.ID.ValueString()
	}

	if err := r.client.DisableStdioTemplate(ctx, state.CompanyID.ValueString(), templateID); err != nil {
		if !errors.Is(err, client.ErrNotFound) {
			resp.Diagnostics.AddError("Error disabling Paperclip stdio template", err.Error())
			return
		}
	}
}

func (r *PaperclipStdioTemplateResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := req.ID
	if strings.Contains(id, "/") {
		parts := strings.SplitN(id, "/", 2)
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("company_id"), parts[0])...)
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("template_id"), parts[1])...)
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
