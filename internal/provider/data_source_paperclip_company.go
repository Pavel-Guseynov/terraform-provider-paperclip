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
	_ datasource.DataSource              = &PaperclipCompanyDataSource{}
	_ datasource.DataSourceWithConfigure = &PaperclipCompanyDataSource{}
)

func NewPaperclipCompanyDataSource() datasource.DataSource {
	return &PaperclipCompanyDataSource{}
}

type PaperclipCompanyDataSource struct {
	client *client.Client
}

type PaperclipCompanyDataSourceModel struct {
	ID                           types.String `tfsdk:"id"`
	Name                         types.String `tfsdk:"name"`
	Description                  types.String `tfsdk:"description"`
	RequireTaskStartConfirmation types.Bool   `tfsdk:"require_task_start_confirmation"`
}

func (d *PaperclipCompanyDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_company"
}

func (d *PaperclipCompanyDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Reads Paperclip company details.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Description: "Company unique identifier.",
				Optional:    true,
				Computed:    true,
			},
			"name": schema.StringAttribute{
				Description: "Company name.",
				Optional:    true,
				Computed:    true,
			},
			"description": schema.StringAttribute{
				Description: "Company description.",
				Computed:    true,
			},
			"require_task_start_confirmation": schema.BoolAttribute{
				Description: "Whether newly runnable tasks require start confirmation in Decisions.",
				Computed:    true,
			},
		},
	}
}

func (d *PaperclipCompanyDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *PaperclipCompanyDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config PaperclipCompanyDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var comp *client.PaperclipCompany
	if config.ID.ValueString() != "" {
		c, err := d.client.GetCompany(ctx, config.ID.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Error reading Paperclip company", err.Error())
			return
		}
		if config.Name.ValueString() != "" && !strings.EqualFold(c.Name, config.Name.ValueString()) {
			resp.Diagnostics.AddError("Company Not Found", fmt.Sprintf("Company with id %q has name %q, which does not match requested name %q", c.ID, c.Name, config.Name.ValueString()))
			return
		}
		comp = c
	} else if config.Name.ValueString() != "" {
		c, err := d.client.FindCompanyByName(ctx, config.Name.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Error reading Paperclip company", err.Error())
			return
		}
		comp = c
	} else {
		resp.Diagnostics.AddError("Invalid Arguments", "Either id or name must be specified")
		return
	}

	config.ID = types.StringValue(comp.ID)
	config.Name = types.StringValue(comp.Name)
	if comp.Description != "" {
		config.Description = types.StringValue(comp.Description)
	} else {
		config.Description = types.StringNull()
	}
	config.RequireTaskStartConfirmation = types.BoolValue(comp.RequireTaskStartConfirmation)

	resp.Diagnostics.Append(resp.State.Set(ctx, &config)...)
}
