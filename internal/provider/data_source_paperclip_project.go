package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/Pavel-Guseynov/terraform-provider-paperclip/internal/client"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ datasource.DataSource              = &PaperclipProjectDataSource{}
	_ datasource.DataSourceWithConfigure = &PaperclipProjectDataSource{}
)

func NewPaperclipProjectDataSource() datasource.DataSource {
	return &PaperclipProjectDataSource{}
}

type PaperclipProjectDataSource struct {
	client *client.Client
}

type PaperclipProjectDataSourceModel struct {
	ID          types.String `tfsdk:"id"`
	CompanyID   types.String `tfsdk:"company_id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	LeadAgentID types.String `tfsdk:"lead_agent_id"`
}

func (d *PaperclipProjectDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_project"
}

func (d *PaperclipProjectDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Reads Paperclip project details.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Project unique identifier.",
				Optional:    true,
				Computed:    true,
			},
			"company_id": schema.StringAttribute{
				Description: "The Paperclip company ID.",
				Optional:    true,
				Computed:    true,
			},
			"name": schema.StringAttribute{
				Description: "Project identifier name.",
				Optional:    true,
				Computed:    true,
			},
			"description": schema.StringAttribute{
				Description: "Project description.",
				Computed:    true,
			},
			"lead_agent_id": schema.StringAttribute{
				Description: "Assigned lead agent ID.",
				Computed:    true,
			},
		},
	}
}

func (d *PaperclipProjectDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *PaperclipProjectDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config PaperclipProjectDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var proj *client.PaperclipProject
	if config.ID.ValueString() != "" {
		p, err := d.client.GetProject(ctx, config.ID.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Error reading Paperclip project", err.Error())
			return
		}
		if config.Name.ValueString() != "" && !strings.EqualFold(p.Name, config.Name.ValueString()) {
			resp.Diagnostics.AddError("Project Not Found", fmt.Sprintf("Project with id %q has name %q, which does not match requested name %q", p.ID, p.Name, config.Name.ValueString()))
			return
		}
		if config.CompanyID.ValueString() != "" && p.CompanyID != config.CompanyID.ValueString() {
			resp.Diagnostics.AddError("Project Not Found", fmt.Sprintf("Project with id %q belongs to company %q, not %q", p.ID, p.CompanyID, config.CompanyID.ValueString()))
			return
		}
		proj = p
	} else if config.CompanyID.ValueString() != "" && config.Name.ValueString() != "" {
		projects, err := d.client.ListProjects(ctx, config.CompanyID.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Error listing Paperclip projects", err.Error())
			return
		}
		var matches []client.PaperclipProject
		for _, p := range projects {
			if strings.EqualFold(p.Name, config.Name.ValueString()) {
				matches = append(matches, p)
			}
		}
		if len(matches) == 0 {
			resp.Diagnostics.AddError("Project Not Found", fmt.Sprintf("Project %s in company %s not found", config.Name.ValueString(), config.CompanyID.ValueString()))
			return
		}
		if len(matches) > 1 {
			var ids []string
			for _, m := range matches {
				ids = append(ids, m.ID)
			}
			resp.Diagnostics.AddError("Ambiguous Project Name", fmt.Sprintf("multiple projects found with name %q in company %s: candidate IDs: %s", config.Name.ValueString(), config.CompanyID.ValueString(), strings.Join(ids, ", ")))
			return
		}
		proj = &matches[0]
	} else {
		resp.Diagnostics.AddError("Invalid Arguments", "Must specify either 'id' or both 'company_id' and 'name'")
		return
	}

	config.ID = types.StringValue(proj.ID)
	if proj.CompanyID != "" {
		config.CompanyID = types.StringValue(proj.CompanyID)
	}
	config.Name = types.StringValue(proj.Name)
	if proj.Description != "" {
		config.Description = types.StringValue(proj.Description)
	} else {
		config.Description = types.StringNull()
	}
	if proj.LeadAgentID != "" {
		config.LeadAgentID = types.StringValue(proj.LeadAgentID)
	} else {
		config.LeadAgentID = types.StringNull()
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
