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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                = &PaperclipSkillResource{}
	_ resource.ResourceWithConfigure   = &PaperclipSkillResource{}
	_ resource.ResourceWithImportState = &PaperclipSkillResource{}
)

func NewPaperclipSkillResource() resource.Resource {
	return &PaperclipSkillResource{}
}

type PaperclipSkillResource struct {
	client *client.Client
}

type PaperclipSkillResourceModel struct {
	ID            types.String `tfsdk:"id"`
	CompanyID     types.String `tfsdk:"company_id"`
	Name          types.String `tfsdk:"name"`
	Slug          types.String `tfsdk:"slug"`
	Description   types.String `tfsdk:"description"`
	Content       types.String `tfsdk:"content"`
	SchemaVersion types.Int64  `tfsdk:"schema_version"`
	Metadata      types.String `tfsdk:"metadata"`
}

func skillContentState(current types.String, remote string) types.String {
	if remote != "" {
		return types.StringValue(remote)
	}
	if !current.IsNull() && !current.IsUnknown() {
		return current
	}
	return types.StringValue("")
}

func (r *PaperclipSkillResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_skill"
}

func (r *PaperclipSkillResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manages a Paperclip skill in a company's catalog.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Unique identifier for the skill.",
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
				Description: "Human-readable skill name.",
				Required:    true,
			},
			"slug": schema.StringAttribute{
				Description: "Unique skill slug identifier.",
				Required:    true,
			},
			"description": schema.StringAttribute{
				Description: "Optional skill description.",
				Optional:    true,
				Computed:    true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"content": schema.StringAttribute{
				Description: "Markdown instructions and skill content.",
				Required:    true,
			},
			"schema_version": schema.Int64Attribute{
				Description: "Skill schema version (default: 1).",
				Optional:    true,
				Computed:    true,
			},
			"metadata": schema.StringAttribute{
				Description: "Paperclip-owned skill metadata as a computed JSON object.",
				Computed:    true,
			},
		},
	}
}

func (r *PaperclipSkillResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *PaperclipSkillResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan PaperclipSkillResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	schemaVer := int64(1)
	if !plan.SchemaVersion.IsNull() && !plan.SchemaVersion.IsUnknown() && plan.SchemaVersion.ValueInt64() > 0 {
		schemaVer = plan.SchemaVersion.ValueInt64()
	}

	skill := client.PaperclipSkill{
		CompanyID:     plan.CompanyID.ValueString(),
		Name:          plan.Name.ValueString(),
		Slug:          plan.Slug.ValueString(),
		Description:   plan.Description.ValueString(),
		Content:       plan.Content.ValueString(),
		SchemaVersion: schemaVer,
	}

	created, err := r.client.CreateSkill(ctx, plan.CompanyID.ValueString(), skill)
	if err != nil {
		resp.Diagnostics.AddError("Error creating Paperclip skill", err.Error())
		return
	}

	plan.ID = types.StringValue(created.ID)
	plan.CompanyID = types.StringValue(created.CompanyID)
	plan.Name = types.StringValue(created.Name)
	plan.Slug = types.StringValue(created.Slug)
	if created.Description != "" {
		plan.Description = types.StringValue(created.Description)
	} else {
		plan.Description = types.StringNull()
	}
	plan.Content = skillContentState(plan.Content, created.Content)
	if created.SchemaVersion > 0 {
		plan.SchemaVersion = types.Int64Value(created.SchemaVersion)
	} else {
		plan.SchemaVersion = types.Int64Value(1)
	}
	plan.Metadata = jsonStringOrNull(created.Metadata)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipSkillResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state PaperclipSkillResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	skill, err := r.client.GetSkill(ctx, state.CompanyID.ValueString(), state.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error reading Paperclip skill", err.Error())
		return
	}

	state.ID = types.StringValue(skill.ID)
	if skill.CompanyID != "" {
		state.CompanyID = types.StringValue(skill.CompanyID)
	}
	state.Name = types.StringValue(skill.Name)
	state.Slug = types.StringValue(skill.Slug)
	if skill.Description != "" {
		state.Description = types.StringValue(skill.Description)
	} else {
		state.Description = types.StringNull()
	}
	state.Content = skillContentState(state.Content, skill.Content)
	if skill.SchemaVersion > 0 {
		state.SchemaVersion = types.Int64Value(skill.SchemaVersion)
	} else {
		state.SchemaVersion = types.Int64Value(1)
	}
	state.Metadata = jsonStringOrNull(skill.Metadata)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *PaperclipSkillResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan PaperclipSkillResourceModel
	var state PaperclipSkillResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	schemaVer := int64(1)
	if !plan.SchemaVersion.IsNull() && !plan.SchemaVersion.IsUnknown() && plan.SchemaVersion.ValueInt64() > 0 {
		schemaVer = plan.SchemaVersion.ValueInt64()
	}

	skill := client.PaperclipSkill{
		CompanyID:     state.CompanyID.ValueString(),
		Name:          plan.Name.ValueString(),
		Slug:          plan.Slug.ValueString(),
		Description:   plan.Description.ValueString(),
		Content:       plan.Content.ValueString(),
		SchemaVersion: schemaVer,
	}

	updated, err := r.client.UpdateSkill(ctx, state.CompanyID.ValueString(), state.ID.ValueString(), skill)
	if err != nil {
		resp.Diagnostics.AddError("Error updating Paperclip skill", err.Error())
		return
	}

	plan.ID = types.StringValue(updated.ID)
	plan.Name = types.StringValue(updated.Name)
	plan.Slug = types.StringValue(updated.Slug)
	if updated.Description != "" {
		plan.Description = types.StringValue(updated.Description)
	} else {
		plan.Description = types.StringNull()
	}
	plan.Content = skillContentState(plan.Content, updated.Content)
	if updated.SchemaVersion > 0 {
		plan.SchemaVersion = types.Int64Value(updated.SchemaVersion)
	} else {
		plan.SchemaVersion = types.Int64Value(1)
	}
	plan.Metadata = jsonStringOrNull(updated.Metadata)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *PaperclipSkillResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state PaperclipSkillResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.DeleteSkill(ctx, state.CompanyID.ValueString(), state.ID.ValueString()); err != nil {
		if !errors.Is(err, client.ErrNotFound) {
			resp.Diagnostics.AddError("Error deleting Paperclip skill", err.Error())
			return
		}
	}
}

func (r *PaperclipSkillResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := req.ID
	if strings.Contains(id, "/") {
		parts := strings.SplitN(id, "/", 2)
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("company_id"), parts[0])...)
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
