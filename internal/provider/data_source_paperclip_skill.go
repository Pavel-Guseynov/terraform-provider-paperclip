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
	ID               types.String `tfsdk:"id"`
	CompanyID        types.String `tfsdk:"company_id"`
	Name             types.String `tfsdk:"name"`
	Slug             types.String `tfsdk:"slug"`
	Description      types.String `tfsdk:"description"`
	Content          types.String `tfsdk:"content"`
	SchemaVersion    types.Int64  `tfsdk:"schema_version"`
	Metadata         types.String `tfsdk:"metadata"`
	IconURL          types.String `tfsdk:"icon_url"`
	Color            types.String `tfsdk:"color"`
	Tagline          types.String `tfsdk:"tagline"`
	AuthorName       types.String `tfsdk:"author_name"`
	HomepageURL      types.String `tfsdk:"homepage_url"`
	Categories       *[]string    `tfsdk:"categories"`
	SharingScope     types.String `tfsdk:"sharing_scope"`
	CurrentVersionID types.String `tfsdk:"current_version_id"`
	SourceType       types.String `tfsdk:"source_type"`
	SourceLocator    types.String `tfsdk:"source_locator"`
	FolderID         types.String `tfsdk:"folder_id"`
	ForkedFromID     types.String `tfsdk:"forked_from_id"`
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
			"icon_url": schema.StringAttribute{
				Description: "Skill icon URL.",
				Computed:    true,
			},
			"color": schema.StringAttribute{
				Description: "Skill UI badge color.",
				Computed:    true,
			},
			"tagline": schema.StringAttribute{
				Description: "Skill catalog tagline.",
				Computed:    true,
			},
			"author_name": schema.StringAttribute{
				Description: "Author display name.",
				Computed:    true,
			},
			"homepage_url": schema.StringAttribute{
				Description: "Homepage or documentation URL.",
				Computed:    true,
			},
			"categories": schema.ListAttribute{
				Description: "Category tags.",
				Computed:    true,
				ElementType: types.StringType,
			},
			"sharing_scope": schema.StringAttribute{
				Description: "Sharing scope.",
				Computed:    true,
			},
			"current_version_id": schema.StringAttribute{
				Description: "Current active version snapshot ID.",
				Computed:    true,
			},
			"source_type": schema.StringAttribute{
				Description: "Provenance source type.",
				Computed:    true,
			},
			"source_locator": schema.StringAttribute{
				Description: "Provenance source locator.",
				Computed:    true,
			},
			"folder_id": schema.StringAttribute{
				Description: "Folder ID.",
				Computed:    true,
			},
			"forked_from_id": schema.StringAttribute{
				Description: "Forked from skill ID.",
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
		if config.Slug.ValueString() != "" && !strings.EqualFold(s.Slug, config.Slug.ValueString()) {
			resp.Diagnostics.AddError("Skill Not Found", fmt.Sprintf("Skill with id %q has slug %q, which does not match requested slug %q", s.ID, s.Slug, config.Slug.ValueString()))
			return
		}
		if config.Name.ValueString() != "" && !strings.EqualFold(s.Name, config.Name.ValueString()) {
			resp.Diagnostics.AddError("Skill Not Found", fmt.Sprintf("Skill with id %q has name %q, which does not match requested name %q", s.ID, s.Name, config.Name.ValueString()))
			return
		}
		skill = s
	} else if config.Slug.ValueString() != "" || config.Name.ValueString() != "" {
		skills, err := d.client.ListSkills(ctx, companyID)
		if err != nil {
			resp.Diagnostics.AddError("Error listing Paperclip skills", err.Error())
			return
		}
		var matches []client.PaperclipSkill
		for _, s := range skills {
			if (config.Slug.ValueString() != "" && strings.EqualFold(s.Slug, config.Slug.ValueString())) ||
				(config.Name.ValueString() != "" && strings.EqualFold(s.Name, config.Name.ValueString())) {
				matches = append(matches, s)
			}
		}
		if len(matches) == 0 {
			resp.Diagnostics.AddError("Skill Not Found", fmt.Sprintf("Skill not found in company %s", companyID))
			return
		}
		if len(matches) > 1 {
			var ids []string
			for _, m := range matches {
				ids = append(ids, m.ID)
			}
			lookupKey := config.Slug.ValueString()
			if lookupKey == "" {
				lookupKey = config.Name.ValueString()
			}
			resp.Diagnostics.AddError("Ambiguous Skill Match", fmt.Sprintf("multiple skills found matching %q in company %s: candidate IDs: %s", lookupKey, companyID, strings.Join(ids, ", ")))
			return
		}
		skill = &matches[0]
	} else {
		resp.Diagnostics.AddError("Invalid Arguments", "Must specify either 'id', 'slug', or 'name'")
		return
	}

	config.ID = types.StringValue(skill.ID)
	config.CompanyID = types.StringValue(companyID)
	config.Name = types.StringValue(skill.Name)
	config.Slug = types.StringValue(skill.Slug)
	config.Description = stringOrNull(skill.Description)
	config.Content = types.StringValue(skill.Content)
	if skill.SchemaVersion > 0 {
		config.SchemaVersion = types.Int64Value(skill.SchemaVersion)
	} else {
		config.SchemaVersion = types.Int64Value(1)
	}
	if skill.Metadata != nil {
		data, _ := json.Marshal(skill.Metadata)
		config.Metadata = types.StringValue(string(data))
	} else {
		config.Metadata = types.StringNull()
	}
	config.IconURL = stringOrNull(skill.IconURL)
	config.Color = stringOrNull(skill.Color)
	config.Tagline = stringOrNull(skill.Tagline)
	config.AuthorName = stringOrNull(skill.AuthorName)
	config.HomepageURL = stringOrNull(skill.HomepageURL)
	config.SharingScope = stringOrNull(skill.SharingScope)
	if skill.Categories != nil {
		cats := append([]string{}, skill.Categories...)
		config.Categories = &cats
	} else {
		config.Categories = nil
	}
	config.CurrentVersionID = stringOrNull(skill.CurrentVersionID)
	config.SourceType = stringOrNull(skill.SourceType)
	config.SourceLocator = stringOrNull(skill.SourceLocator)
	config.FolderID = stringOrNull(skill.FolderID)
	config.ForkedFromID = stringOrNull(skill.ForkedFromID)

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
