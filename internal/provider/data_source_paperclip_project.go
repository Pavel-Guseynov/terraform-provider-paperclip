package provider

import (
	"context"
	"fmt"
	"strings"

	"git.guseynov.org/guseynov/paperclip/terraform-provider-paperclip/internal/client"
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

	if config.ID.ValueString() != "" {
		proj, err := d.client.GetProject(ctx, config.ID.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Error reading Paperclip project", err.Error())
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
	} else if config.CompanyID.ValueString() != "" && config.Name.ValueString() != "" {
		projects, err := d.client.ListProjects(ctx, config.CompanyID.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Error listing Paperclip projects", err.Error())
			return
		}
		var found *client.PaperclipProject
		for _, p := range projects {
			if strings.EqualFold(p.Name, config.Name.ValueString()) {
				found = &p
				break
			}
		}
		if found == nil {
			resp.Diagnostics.AddError("Project Not Found", fmt.Sprintf("Project %s in company %s not found", config.Name.ValueString(), config.CompanyID.ValueString()))
			return
		}
		config.ID = types.StringValue(found.ID)
		config.CompanyID = types.StringValue(found.CompanyID)
		config.Name = types.StringValue(found.Name)
		if found.Description != "" {
			config.Description = types.StringValue(found.Description)
		} else {
			config.Description = types.StringNull()
		}
		if found.LeadAgentID != "" {
			config.LeadAgentID = types.StringValue(found.LeadAgentID)
		} else {
			config.LeadAgentID = types.StringNull()
		}
	} else {
		resp.Diagnostics.AddError("Invalid Arguments", "Must specify either 'id' or both 'company_id' and 'name'")
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
