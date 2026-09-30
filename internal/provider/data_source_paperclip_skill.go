package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"git.guseynov.org/guseynov/paperclip/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = &PaperclipSkillDataSource{}
	_ datasource.DataSourceWithConfigure = &PaperclipSkillDataSource{}
)

func NewPaperclipSkillDataSource() datasource.DataSource {
	return &PaperclipSkillDataSource{}
}

type PaperclipSkillDataSource struct {
	client *client.Client
}

type PaperclipSkillDataSourceModel struct {
	ID            types.String `tfsdk:"id"`
	CompanyID     types.String `tfsdk:"company_id"`
	Name          types.String `tfsdk:"name"`
	Slug          types.String `tfsdk:"slug"`
	Description   types.String `tfsdk:"description"`
	Content       types.String `tfsdk:"content"`
	SchemaVersion types.Int64  `tfsdk:"schema_version"`
	Metadata      types.String `tfsdk:"metadata"`
}

func (d *PaperclipSkillDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_skill"
}

func (d *PaperclipSkillDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Reads Paperclip skill details.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Skill unique identifier.",
				Optional:    true,
				Computed:    true,
			},
			"company_id": schema.StringAttribute{
				Description: "The Paperclip company ID.",
				Required:    true,
			},
			"name": schema.StringAttribute{
				Description: "Skill name.",
				Optional:    true,
				Computed:    true,
			},
			"slug": schema.StringAttribute{
				Description: "Skill slug.",
				Optional:    true,
				Computed:    true,
			},
			"description": schema.StringAttribute{
				Description: "Skill description.",
				Computed:    true,
			},
			"content": schema.StringAttribute{
				Description: "Skill markdown content.",
				Computed:    true,
			},
			"schema_version": schema.Int64Attribute{
				Description: "Skill schema version.",
				Computed:    true,
			},
			"metadata": schema.StringAttribute{
				Description: "Skill JSON metadata.",
				Computed:    true,
			},
		},
	}
}

func (d *PaperclipSkillDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *client.Client, got: %T", req.ProviderData),
		)
		return
	}
	d.client = c
}

func (d *PaperclipSkillDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config PaperclipSkillDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	companyID := config.CompanyID.ValueString()
	if companyID == "" {
		resp.Diagnostics.AddError("Invalid Arguments", "company_id is required")
		return
	}

	var skill *client.PaperclipSkill
	if config.ID.ValueString() != "" {
		s, err := d.client.GetSkill(ctx, companyID, config.ID.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Error reading Paperclip skill", err.Error())
			return
		}
		skill = s
	} else if config.Slug.ValueString() != "" || config.Name.ValueString() != "" {
		skills, err := d.client.ListSkills(ctx, companyID)
		if err != nil {
			resp.Diagnostics.AddError("Error listing Paperclip skills", err.Error())
			return
		}
		for _, s := range skills {
			if (config.Slug.ValueString() != "" && strings.EqualFold(s.Slug, config.Slug.ValueString())) ||
				(config.Name.ValueString() != "" && strings.EqualFold(s.Name, config.Name.ValueString())) {
				skill = &s
				break
			}
		}
		if skill == nil {
			resp.Diagnostics.AddError("Skill Not Found", fmt.Sprintf("Skill not found in company %s", companyID))
			return
		}
	} else {
		resp.Diagnostics.AddError("Invalid Arguments", "Must specify either 'id', 'slug', or 'name'")
		return
	}

	config.ID = types.StringValue(skill.ID)
	config.CompanyID = types.StringValue(companyID)
	config.Name = types.StringValue(skill.Name)
	config.Slug = types.StringValue(skill.Slug)
	if skill.Description != "" {
		config.Description = types.StringValue(skill.Description)
	} else {
		config.Description = types.StringNull()
	}
	config.Content = types.StringValue(skill.Content)
	if skill.SchemaVersion > 0 {
		config.SchemaVersion = types.Int64Value(skill.SchemaVersion)
	}
	if skill.Metadata != nil {
		data, _ := json.Marshal(skill.Metadata)
		config.Metadata = types.StringValue(string(data))
	} else {
		config.Metadata = types.StringNull()
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
